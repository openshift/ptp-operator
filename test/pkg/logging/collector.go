package logging

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	testclient "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
)

// LogCollector manages continuous log streaming from PTP pods
type LogCollector struct {
	logDir string
	ctx    context.Context
	cancel context.CancelFunc

	// Map: node name -> file handle
	daemonContainerFiles map[string]*os.File
	cloudProxyFiles      map[string]*os.File
	ptpOperatorFile      *os.File

	// Track which pods we're already streaming from
	streamingPods map[string]bool

	mu sync.Mutex
}

var (
	// globalCollector is a singleton to ensure only one log collector runs per process.
	// This prevents conflicts when multiple test suites (serial/parallel) run in the same process.
	globalCollector *LogCollector
)

// ShouldCollectLogs checks if log collection is enabled via environment variable
func ShouldCollectLogs() bool {
	value := strings.ToLower(os.Getenv("COLLECT_POD_LOGS"))
	return value == "true" || value == "1"
}

// ShouldWriteTestMarkers checks if test markers should be written to logs
func ShouldWriteTestMarkers() bool {
	value := strings.ToLower(os.Getenv("LOG_TEST_MARKERS"))
	// Default to true if COLLECT_POD_LOGS is enabled and LOG_TEST_MARKERS is not explicitly set
	if value == "" {
		return ShouldCollectLogs()
	}
	return value == "true" || value == "1"
}

// GetLogArtifactsDir returns the directory for log artifacts
func GetLogArtifactsDir() string {
	if dir := os.Getenv("LOG_ARTIFACTS_DIR"); dir != "" {
		return dir
	}
	return "./test-logs"
}

// StartLogCollection initializes and starts the log collector.
// This function is idempotent - calling it multiple times is safe and will only initialize once per process.
// suiteName is used to distinguish between different test suites (e.g., "serial", "parallel")
func StartLogCollection(suiteName string) error {
	if !ShouldCollectLogs() {
		logrus.Info("Log collection disabled. Set COLLECT_POD_LOGS=true to enable.")
		return nil
	}

	// If log collection is already running, skip re-initialization
	// This prevents conflicts when multiple test suites run in the same process
	if globalCollector != nil {
		logrus.Info("Log collection already started, skipping re-initialization")
		return nil
	}

	// Default suite name if not provided
	if suiteName == "" {
		suiteName = "default"
	}

	baseDir := GetLogArtifactsDir()
	// Include suite name to distinguish between serial and parallel test suites
	logDir := filepath.Join(baseDir, fmt.Sprintf("run_%s_%s", time.Now().Format("2006-01-02_15-04-05"), suiteName))

	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory %s: %w", logDir, err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	globalCollector = &LogCollector{
		logDir:               logDir,
		ctx:                  ctx,
		cancel:               cancel,
		daemonContainerFiles: make(map[string]*os.File),
		cloudProxyFiles:      make(map[string]*os.File),
		streamingPods:        make(map[string]bool),
	}

	// Create ptp-operator log file
	var err error
	globalCollector.ptpOperatorFile, err = os.OpenFile(
		filepath.Join(logDir, "ptp-operator.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0644,
	)
	if err != nil {
		return fmt.Errorf("failed to create ptp-operator log file: %w", err)
	}

	// Write suite start marker
	if ShouldWriteTestMarkers() {
		globalCollector.WriteSuiteStart()
	}

	// Start pod watchers in background
	go globalCollector.WatchPTPOperatorPods()
	go globalCollector.WatchLinuxPTPDaemonPods()

	// Stream from existing pods
	globalCollector.StreamExistingPods()

	logrus.Infof("Log collection started. Logs will be saved to: %s", logDir)
	if ShouldWriteTestMarkers() {
		logrus.Info("Test markers enabled. Test boundaries will be marked in log files.")
	}
	return nil
}

// StopLogCollection stops the log collector and closes all files.
// This function is idempotent - calling it multiple times is safe (only the first call does anything).
func StopLogCollection() {
	if globalCollector == nil {
		return
	}

	logrus.Info("Stopping log collection...")

	// Store log dir before we nil the collector
	logDir := globalCollector.logDir

	// Write suite end marker
	if ShouldWriteTestMarkers() {
		globalCollector.WriteSuiteEnd()
	}

	// Cancel context to stop all goroutines
	globalCollector.cancel()

	// Give goroutines time to finish
	time.Sleep(2 * time.Second)

	// Close all files
	globalCollector.mu.Lock()

	if globalCollector.ptpOperatorFile != nil {
		globalCollector.ptpOperatorFile.Close()
	}

	for _, f := range globalCollector.daemonContainerFiles {
		f.Close()
	}

	for _, f := range globalCollector.cloudProxyFiles {
		f.Close()
	}

	globalCollector.mu.Unlock()

	// Set to nil to prevent double-cleanup
	globalCollector = nil

	logrus.Infof("Logs saved to: %s", logDir)
}

// WriteTestStart writes a test start marker to all log files
func WriteTestStart(report ginkgo.SpecReport) {
	if globalCollector != nil && ShouldWriteTestMarkers() {
		globalCollector.WriteTestStart(report)
	}
}

// WriteTestEnd writes a test end marker to all log files
func WriteTestEnd(report ginkgo.SpecReport) {
	if globalCollector != nil && ShouldWriteTestMarkers() {
		globalCollector.WriteTestEnd(report)
	}
}

// WriteTestStart writes a test start marker to all log files
func (lc *LogCollector) WriteTestStart(report ginkgo.SpecReport) {
	fileName := "unknown"
	lineNumber := 0

	if len(report.ContainerHierarchyLocations) > 0 {
		loc := report.ContainerHierarchyLocations[len(report.ContainerHierarchyLocations)-1]
		fileName = filepath.Base(loc.FileName)
		lineNumber = loc.LineNumber
	}

	marker := fmt.Sprintf(`
#################### TEST START ####################
# Test: %s
# Started: %s
# File: %s:%d
####################################################
`,
		report.FullText(),
		report.StartTime.Format(time.RFC3339),
		fileName,
		lineNumber,
	)

	lc.WriteMarkerToAllFiles(marker)
}

// WriteTestEnd writes a test end marker to all log files
func (lc *LogCollector) WriteTestEnd(report ginkgo.SpecReport) {
	status := "PASSED"
	if report.Failed() {
		status = "FAILED"
	} else if report.State.Is(types.SpecStateSkipped) {
		status = "SKIPPED"
	} else if report.State.Is(types.SpecStatePending) {
		status = "PENDING"
	}

	marker := fmt.Sprintf(`
#################### TEST END ######################
# Test: %s
# Status: %s
# Duration: %s
# Ended: %s
####################################################
`,
		report.FullText(),
		status,
		report.RunTime.String(),
		report.EndTime.Format(time.RFC3339),
	)

	lc.WriteMarkerToAllFiles(marker)
}

// WriteSuiteStart writes a suite start marker
func (lc *LogCollector) WriteSuiteStart() {
	marker := fmt.Sprintf(`
##################################################
# TEST SUITE STARTED
# Suite: PTP e2e integration tests
# Time: %s
##################################################
`, time.Now().Format(time.RFC3339))

	lc.WriteMarkerToAllFiles(marker)
}

// WriteSuiteEnd writes a suite end marker
func (lc *LogCollector) WriteSuiteEnd() {
	marker := fmt.Sprintf(`
##################################################
# TEST SUITE ENDED
# Time: %s
##################################################
`, time.Now().Format(time.RFC3339))

	lc.WriteMarkerToAllFiles(marker)
}

// WriteMarkerToAllFiles writes a marker to all open log files
func (lc *LogCollector) WriteMarkerToAllFiles(marker string) {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	// Write to ptp-operator file
	if lc.ptpOperatorFile != nil {
		fmt.Fprintln(lc.ptpOperatorFile, marker)
		lc.ptpOperatorFile.Sync()
	}

	// Write to all daemon container files
	for _, file := range lc.daemonContainerFiles {
		fmt.Fprintln(file, marker)
		file.Sync()
	}

	// Write to all cloud-event-proxy files
	for _, file := range lc.cloudProxyFiles {
		fmt.Fprintln(file, marker)
		file.Sync()
	}
}

// getOrCreateLogFile gets or creates a log file for a specific node and container
func (lc *LogCollector) getOrCreateLogFile(nodeName, containerName string) (*os.File, error) {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	var fileMap map[string]*os.File
	switch containerName {
	case pkg.PtpContainerName:
		fileMap = lc.daemonContainerFiles
	case pkg.EventProxyContainerName:
		fileMap = lc.cloudProxyFiles
	default:
		return nil, fmt.Errorf("unknown container name: %s", containerName)
	}

	// Check if file already open
	if file, exists := fileMap[nodeName]; exists {
		return file, nil
	}

	// Create new file
	filename := fmt.Sprintf("linuxptp-daemon-%s-%s.log", nodeName, containerName)
	file, err := os.OpenFile(
		filepath.Join(lc.logDir, filename),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0644,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file %s: %w", filename, err)
	}

	fileMap[nodeName] = file
	return file, nil
}

// WatchPTPOperatorPods watches for ptp-operator pod events and streams logs
func (lc *LogCollector) WatchPTPOperatorPods() {
	watcher, err := testclient.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).Watch(
		lc.ctx,
		metav1.ListOptions{
			LabelSelector: pkg.PtPOperatorPodsLabel,
		},
	)
	if err != nil {
		logrus.Errorf("Failed to create watcher for ptp-operator pods: %v", err)
		return
	}
	defer watcher.Stop()

	for {
		select {
		case <-lc.ctx.Done():
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				// Watcher closed, try to recreate
				time.Sleep(5 * time.Second)
				go lc.WatchPTPOperatorPods()
				return
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}

			switch event.Type {
			case watch.Added, watch.Modified:
				if pod.Status.Phase == corev1.PodRunning {
					podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
					lc.mu.Lock()
					if !lc.streamingPods[podKey] {
						lc.streamingPods[podKey] = true
						lc.mu.Unlock()
						go lc.streamPTPOperatorPod(pod.Name)
					} else {
						lc.mu.Unlock()
					}
				}
			}
		}
	}
}

// WatchLinuxPTPDaemonPods watches for linuxptp-daemon pod events and streams logs
func (lc *LogCollector) WatchLinuxPTPDaemonPods() {
	watcher, err := testclient.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).Watch(
		lc.ctx,
		metav1.ListOptions{
			LabelSelector: pkg.PtpLinuxDaemonPodsLabel,
		},
	)
	if err != nil {
		logrus.Errorf("Failed to create watcher for linuxptp-daemon pods: %v", err)
		return
	}
	defer watcher.Stop()

	for {
		select {
		case <-lc.ctx.Done():
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				// Watcher closed, try to recreate
				time.Sleep(5 * time.Second)
				go lc.WatchLinuxPTPDaemonPods()
				return
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}

			switch event.Type {
			case watch.Added, watch.Modified:
				if pod.Status.Phase == corev1.PodRunning && pod.Spec.NodeName != "" {
					// Stream both containers
					podKey := fmt.Sprintf("%s/%s/%s", pod.Namespace, pod.Name, pkg.PtpContainerName)
					lc.mu.Lock()
					if !lc.streamingPods[podKey] {
						lc.streamingPods[podKey] = true
						lc.mu.Unlock()
						go lc.streamPodToNodeFile(pod.Name, pkg.PtpContainerName, pod.Spec.NodeName)
					} else {
						lc.mu.Unlock()
					}

					podKey = fmt.Sprintf("%s/%s/%s", pod.Namespace, pod.Name, pkg.EventProxyContainerName)
					lc.mu.Lock()
					if !lc.streamingPods[podKey] {
						lc.streamingPods[podKey] = true
						lc.mu.Unlock()
						go lc.streamPodToNodeFile(pod.Name, pkg.EventProxyContainerName, pod.Spec.NodeName)
					} else {
						lc.mu.Unlock()
					}
				}
			}
		}
	}
}

// streamPTPOperatorPod streams logs from a ptp-operator pod
func (lc *LogCollector) streamPTPOperatorPod(podName string) {
	defer func() {
		lc.mu.Lock()
		podKey := fmt.Sprintf("%s/%s", pkg.PtpLinuxDaemonNamespace, podName)
		delete(lc.streamingPods, podKey)
		lc.mu.Unlock()
	}()

	file := lc.ptpOperatorFile
	if file == nil {
		return
	}

	// Write marker
	marker := fmt.Sprintf("\n========== [%s] Starting stream from pod: %s ==========\n",
		time.Now().Format(time.RFC3339), podName)
	lc.mu.Lock()
	fmt.Fprint(file, marker)
	file.Sync()
	lc.mu.Unlock()

	// Stream logs
	lc.streamLogs(podName, "", file)

	// Write end marker
	marker = fmt.Sprintf("\n========== [%s] Stream ended for pod: %s ==========\n\n",
		time.Now().Format(time.RFC3339), podName)
	lc.mu.Lock()
	fmt.Fprint(file, marker)
	file.Sync()
	lc.mu.Unlock()
}

// streamPodToNodeFile streams logs from a pod container to a node-specific file
func (lc *LogCollector) streamPodToNodeFile(podName, containerName, nodeName string) {
	defer func() {
		lc.mu.Lock()
		podKey := fmt.Sprintf("%s/%s/%s", pkg.PtpLinuxDaemonNamespace, podName, containerName)
		delete(lc.streamingPods, podKey)
		lc.mu.Unlock()
	}()

	file, err := lc.getOrCreateLogFile(nodeName, containerName)
	if err != nil {
		logrus.Errorf("Failed to get log file for node %s, container %s: %v", nodeName, containerName, err)
		return
	}

	// Write marker
	marker := fmt.Sprintf("\n========== [%s] Starting stream from pod: %s, container: %s ==========\n",
		time.Now().Format(time.RFC3339), podName, containerName)
	lc.mu.Lock()
	fmt.Fprint(file, marker)
	file.Sync()
	lc.mu.Unlock()

	// Stream logs
	lc.streamLogs(podName, containerName, file)

	// Write end marker
	marker = fmt.Sprintf("\n========== [%s] Stream ended for pod: %s, container: %s ==========\n\n",
		time.Now().Format(time.RFC3339), podName, containerName)
	lc.mu.Lock()
	fmt.Fprint(file, marker)
	file.Sync()
	lc.mu.Unlock()
}

// streamLogs streams logs from a pod/container to a file
func (lc *LogCollector) streamLogs(podName, containerName string, file *os.File) {
	logOptions := &corev1.PodLogOptions{
		Follow:     true,
		Timestamps: true,
	}

	if containerName != "" {
		logOptions.Container = containerName
	}

	req := testclient.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).GetLogs(podName, logOptions)
	stream, err := req.Stream(lc.ctx)
	if err != nil {
		logrus.Debugf("Failed to stream logs from pod %s, container %s: %v", podName, containerName, err)
		return
	}
	defer stream.Close()

	reader := bufio.NewReader(stream)
	for {
		select {
		case <-lc.ctx.Done():
			return
		default:
			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					logrus.Debugf("Error reading logs from pod %s: %v", podName, err)
				}
				return
			}

			lc.mu.Lock()
			fmt.Fprintf(file, "[%s] %s", podName, line)
			file.Sync()
			lc.mu.Unlock()
		}
	}
}

// StreamExistingPods starts streaming from pods that already exist
func (lc *LogCollector) StreamExistingPods() {
	// Stream from existing ptp-operator pods
	ptpOpPods, err := testclient.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(
		lc.ctx,
		metav1.ListOptions{
			LabelSelector: pkg.PtPOperatorPodsLabel,
		},
	)
	if err != nil {
		logrus.Errorf("Failed to list ptp-operator pods: %v", err)
	} else {
		for i := range ptpOpPods.Items {
			pod := &ptpOpPods.Items[i]
			if pod.Status.Phase == corev1.PodRunning {
				podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
				lc.mu.Lock()
				if !lc.streamingPods[podKey] {
					lc.streamingPods[podKey] = true
					lc.mu.Unlock()
					go lc.streamPTPOperatorPod(pod.Name)
				} else {
					lc.mu.Unlock()
				}
			}
		}
	}

	// Stream from existing linuxptp-daemon pods
	daemonPods, err := testclient.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(
		lc.ctx,
		metav1.ListOptions{
			LabelSelector: pkg.PtpLinuxDaemonPodsLabel,
		},
	)
	if err != nil {
		logrus.Errorf("Failed to list linuxptp-daemon pods: %v", err)
	} else {
		for i := range daemonPods.Items {
			pod := &daemonPods.Items[i]
			if pod.Status.Phase == corev1.PodRunning && pod.Spec.NodeName != "" {
				// Stream both containers
				podKey := fmt.Sprintf("%s/%s/%s", pod.Namespace, pod.Name, pkg.PtpContainerName)
				lc.mu.Lock()
				if !lc.streamingPods[podKey] {
					lc.streamingPods[podKey] = true
					lc.mu.Unlock()
					go lc.streamPodToNodeFile(pod.Name, pkg.PtpContainerName, pod.Spec.NodeName)
				} else {
					lc.mu.Unlock()
				}

				podKey = fmt.Sprintf("%s/%s/%s", pod.Namespace, pod.Name, pkg.EventProxyContainerName)
				lc.mu.Lock()
				if !lc.streamingPods[podKey] {
					lc.streamingPods[podKey] = true
					lc.mu.Unlock()
					go lc.streamPodToNodeFile(pod.Name, pkg.EventProxyContainerName, pod.Spec.NodeName)
				} else {
					lc.mu.Unlock()
				}
			}
		}
	}
}

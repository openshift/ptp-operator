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

// ============================================================================
// Configuration and Setup
// ============================================================================

const (
	logChannelBuffer    = 100             // Buffer size per file writer channel
	watcherRetryDelay   = 5 * time.Second // Delay before recreating watcher
	syncInterval        = 1 * time.Second // How often to sync file to disk
	shutdownGracePeriod = 2 * time.Second // Time to wait for goroutines on shutdown
)

const (
	// Marker delimiters
	streamMarkerLine = "=========="
	suiteMarkerLine  = "##################################################"
	testMarkerLine   = "####################################################"
	testMarkerHeader = "####################"

	// Suite name
	suiteNameConst = "PTP e2e integration tests"
)

var (
	collector *PodLogCollector // Global singleton
)

// PodLogCollector manages log streaming from PTP pods
type PodLogCollector struct {
	logDir        string
	ctx           context.Context
	cancel        context.CancelFunc
	writers       *fileWriterSet
	streamTracker *streamTracker
	wg            sync.WaitGroup // Wait for all goroutines to finish
}

// fileWriterSet manages individual file writers
type fileWriterSet struct {
	ptpOperatorWriter *fileWriter
	nodeWriters       map[string]*nodeFileWriters // key: nodeName
	mu                sync.Mutex                  // Only for nodeWriters map creation
}

// nodeFileWriters holds writers for a single node's containers
type nodeFileWriters struct {
	daemonWriter *fileWriter
	proxyWriter  *fileWriter
}

// fileWriter manages writing to a single file via a channel
type fileWriter struct {
	file    *os.File
	channel chan string
	ctx     context.Context
	cancel  context.CancelFunc
}

// streamTracker prevents duplicate streams (simple goroutine-safe map)
type streamTracker struct {
	active map[string]bool
	mu     sync.Mutex
}

func ShouldCollectLogs() bool {
	value := strings.ToLower(os.Getenv("COLLECT_POD_LOGS"))
	return value == "true" || value == "1"
}

func ShouldWriteTestMarkers() bool {
	value := strings.ToLower(os.Getenv("LOG_TEST_MARKERS"))
	if value == "" {
		return ShouldCollectLogs()
	}
	return value == "true" || value == "1"
}

func GetLogArtifactsDir() string {
	if dir := os.Getenv("LOG_ARTIFACTS_DIR"); dir != "" {
		return dir
	}
	return "./test-logs"
}

// ============================================================================
// Public API
// ============================================================================

// StartLogCollection starts collecting logs from PTP pods
func StartLogCollection(suiteName string) error {
	if !ShouldCollectLogs() {
		logrus.Info("Log collection disabled. Set COLLECT_POD_LOGS=true to enable.")
		return nil
	}

	if collector != nil {
		logrus.Info("Log collection already started")
		return nil
	}

	if suiteName == "" {
		suiteName = "default"
	}

	// Create log directory
	baseDir := GetLogArtifactsDir()
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	logDir := filepath.Join(baseDir, fmt.Sprintf("run_%s_%s", timestamp, suiteName))

	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Initialize collector
	ctx, cancel := context.WithCancel(context.Background())
	collector = &PodLogCollector{
		logDir: logDir,
		ctx:    ctx,
		cancel: cancel,
		writers: &fileWriterSet{
			nodeWriters: make(map[string]*nodeFileWriters),
		},
		streamTracker: &streamTracker{
			active: make(map[string]bool),
		},
	}

	// Create ptp-operator file writer
	var err error
	collector.writers.ptpOperatorWriter, err = newFileWriter(
		ctx,
		filepath.Join(logDir, "ptp-operator.log"),
	)
	if err != nil {
		return fmt.Errorf("failed to create ptp-operator writer: %w", err)
	}

	// Start the writer goroutine
	collector.wg.Add(1)
	go collector.writers.ptpOperatorWriter.run(&collector.wg)

	// Write suite start marker
	if ShouldWriteTestMarkers() {
		collector.writers.writeToAll(createSuiteStartMarker())
	}

	// Start watching for pods
	collector.wg.Add(2)
	go func() {
		defer collector.wg.Done()
		collector.watchPTPOperatorPods()
	}()
	go func() {
		defer collector.wg.Done()
		collector.watchLinuxPTPDaemonPods()
	}()

	// Stream from existing pods
	collector.streamExistingPods()

	logrus.Infof("Log collection started. Logs will be saved to: %s", logDir)
	return nil
}

// StopLogCollection stops log collection and closes all files
func StopLogCollection() {
	if collector == nil {
		return
	}

	logrus.Info("Stopping log collection...")
	logDir := collector.logDir

	// Write suite end marker
	if ShouldWriteTestMarkers() {
		collector.writers.writeToAll(createSuiteEndMarker())
	}

	// Cancel context to stop all operations
	collector.cancel()

	// Wait for all goroutines to finish (with timeout for safety)
	done := make(chan struct{})
	go func() {
		collector.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All goroutines finished cleanly
	case <-time.After(shutdownGracePeriod):
		logrus.Warn("Shutdown timeout exceeded, forcing close")
	}

	// Close all file writers
	collector.writers.closeAll()

	collector = nil
	logrus.Infof("Logs saved to: %s", logDir)
}

// WriteTestStart writes a test start marker
func WriteTestStart(report ginkgo.SpecReport) {
	if collector != nil && ShouldWriteTestMarkers() {
		collector.writers.writeToAll(createTestStartMarker(report))
	}
}

// WriteTestEnd writes a test end marker
func WriteTestEnd(report ginkgo.SpecReport) {
	if collector != nil && ShouldWriteTestMarkers() {
		collector.writers.writeToAll(createTestEndMarker(report))
	}
}

// ============================================================================
// File Writer - One goroutine per file
// ============================================================================

// newFileWriter creates a new file writer
func newFileWriter(ctx context.Context, filePath string) (*fileWriter, error) {
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}

	writerCtx, cancel := context.WithCancel(ctx)
	return &fileWriter{
		file:    file,
		channel: make(chan string, logChannelBuffer),
		ctx:     writerCtx,
		cancel:  cancel,
	}, nil
}

// run is the writer goroutine - one per file
func (fw *fileWriter) run(wg *sync.WaitGroup) {
	defer wg.Done()
	defer fw.file.Close()

	// Sync timer to batch disk writes
	syncTicker := time.NewTicker(syncInterval)
	defer syncTicker.Stop()

	for {
		select {
		case <-fw.ctx.Done():
			// Drain remaining messages
			fw.drainAndClose()
			return

		case msg, ok := <-fw.channel:
			if !ok {
				fw.file.Sync()
				return
			}
			fmt.Fprint(fw.file, msg)

		case <-syncTicker.C:
			// Periodic sync to disk
			fw.file.Sync()
		}
	}
}

// drainAndClose drains remaining messages and syncs before closing
func (fw *fileWriter) drainAndClose() {
	for {
		select {
		case msg := <-fw.channel:
			fmt.Fprint(fw.file, msg)
		default:
			fw.file.Sync()
			return
		}
	}
}

// write sends a message to the writer's channel (blocks if full)
func (fw *fileWriter) write(msg string) {
	select {
	case fw.channel <- msg:
		// Message sent successfully
	case <-fw.ctx.Done():
		// Context cancelled, don't block
	}
}

// close stops the writer
func (fw *fileWriter) close() {
	fw.cancel()
	close(fw.channel)
}

// ============================================================================
// File Writer Set Management
// ============================================================================

// getOrCreateNodeWriters gets or creates writers for a node
func (fws *fileWriterSet) getOrCreateNodeWriters(nodeName string) *nodeFileWriters {
	fws.mu.Lock()
	defer fws.mu.Unlock()

	if writers, exists := fws.nodeWriters[nodeName]; exists {
		return writers
	}

	// Create new writers for this node
	daemonWriter, err := newFileWriter(
		collector.ctx,
		filepath.Join(collector.logDir, fmt.Sprintf("linuxptp-daemon-%s-%s.log", nodeName, pkg.PtpContainerName)),
	)
	if err != nil {
		logrus.Errorf("Failed to create daemon writer for node %s: %v", nodeName, err)
		return nil
	}

	proxyWriter, err := newFileWriter(
		collector.ctx,
		filepath.Join(collector.logDir, fmt.Sprintf("linuxptp-daemon-%s-%s.log", nodeName, pkg.EventProxyContainerName)),
	)
	if err != nil {
		logrus.Errorf("Failed to create proxy writer for node %s: %v", nodeName, err)
		daemonWriter.close()
		return nil
	}

	writers := &nodeFileWriters{
		daemonWriter: daemonWriter,
		proxyWriter:  proxyWriter,
	}

	// Start writer goroutines
	collector.wg.Add(2)
	go daemonWriter.run(&collector.wg)
	go proxyWriter.run(&collector.wg)

	fws.nodeWriters[nodeName] = writers
	return writers
}

// writeToAll writes a message to all files
func (fws *fileWriterSet) writeToAll(msg string) {
	// Write to ptp-operator
	if fws.ptpOperatorWriter != nil {
		fws.ptpOperatorWriter.write(msg + "\n")
	}

	// Write to all node files
	fws.mu.Lock()
	nodeWriters := make([]*nodeFileWriters, 0, len(fws.nodeWriters))
	for _, writers := range fws.nodeWriters {
		nodeWriters = append(nodeWriters, writers)
	}
	fws.mu.Unlock()

	for _, writers := range nodeWriters {
		if writers.daemonWriter != nil {
			writers.daemonWriter.write(msg + "\n")
		}
		if writers.proxyWriter != nil {
			writers.proxyWriter.write(msg + "\n")
		}
	}
}

// closeAll closes all file writers
func (fws *fileWriterSet) closeAll() {
	if fws.ptpOperatorWriter != nil {
		fws.ptpOperatorWriter.close()
	}

	fws.mu.Lock()
	defer fws.mu.Unlock()

	for _, writers := range fws.nodeWriters {
		if writers.daemonWriter != nil {
			writers.daemonWriter.close()
		}
		if writers.proxyWriter != nil {
			writers.proxyWriter.close()
		}
	}
}

// ============================================================================
// Stream Tracker
// ============================================================================

func (st *streamTracker) markActive(key string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()

	if st.active[key] {
		return false // Already active
	}
	st.active[key] = true
	return true
}

func (st *streamTracker) markInactive(key string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.active, key)
}

// ============================================================================
// Pod Watching
// ============================================================================

func (c *PodLogCollector) watchPTPOperatorPods() {
	c.processWatchEvents(pkg.PtPOperatorPodsLabel, func(pod *corev1.Pod) {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.streamPodLogs(pod.Name, "", c.writers.ptpOperatorWriter)
		}()
	})
}

func (c *PodLogCollector) watchLinuxPTPDaemonPods() {
	c.processWatchEvents(pkg.PtpLinuxDaemonPodsLabel, func(pod *corev1.Pod) {
		if pod.Spec.NodeName == "" {
			return
		}

		c.wg.Add(2)
		go func() {
			defer c.wg.Done()
			c.streamDaemonContainer(pod.Name, pod.Spec.NodeName, pkg.PtpContainerName)
		}()
		go func() {
			defer c.wg.Done()
			c.streamDaemonContainer(pod.Name, pod.Spec.NodeName, pkg.EventProxyContainerName)
		}()
	})
}

func (c *PodLogCollector) createWatcher(labelSelector string) (watch.Interface, error) {
	return testclient.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).Watch(
		c.ctx,
		metav1.ListOptions{LabelSelector: labelSelector},
	)
}

func (c *PodLogCollector) processWatchEvents(labelSelector string, onPodReady func(*corev1.Pod)) {
	var watcher watch.Interface
	var err error

	for {
		// Create or recreate watcher
		if watcher == nil {
			watcher, err = c.createWatcher(labelSelector)
			if err != nil {
				logrus.Errorf("Failed to create watcher for %s: %v", labelSelector, err)
				select {
				case <-c.ctx.Done():
					return
				case <-time.After(watcherRetryDelay):
					continue
				}
			}
		}

		// Process events from watcher
		select {
		case <-c.ctx.Done():
			if watcher != nil {
				watcher.Stop()
			}
			return

		case event, ok := <-watcher.ResultChan():
			if !ok {
				// Watcher closed (timeout, error, etc.) - recreate it
				logrus.Debugf("Watcher closed for %s, recreating", labelSelector)
				watcher.Stop()
				watcher = nil

				select {
				case <-c.ctx.Done():
					return
				case <-time.After(watcherRetryDelay):
					continue
				}
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}

			switch event.Type {
			case watch.Added, watch.Modified:
				if pod.Status.Phase == corev1.PodRunning {
					onPodReady(pod)
				}
			case watch.Deleted:
				logrus.Debugf("Pod deleted: %s", pod.Name)
			}
		}
	}
}

// ============================================================================
// Log Streaming
// ============================================================================

func (c *PodLogCollector) streamDaemonContainer(podName, nodeName, containerName string) {
	// Get the appropriate writer for this node/container
	writers := c.writers.getOrCreateNodeWriters(nodeName)
	if writers == nil {
		return
	}

	var writer *fileWriter
	switch containerName {
	case pkg.PtpContainerName:
		writer = writers.daemonWriter
	case pkg.EventProxyContainerName:
		writer = writers.proxyWriter
	default:
		logrus.Errorf("Unknown container: %s", containerName)
		return
	}

	// Write stream start marker
	writer.write(createStreamStartMarker(podName, containerName))

	// Stream logs
	c.streamPodLogs(podName, containerName, writer)

	// Write stream end marker
	writer.write(createStreamEndMarker(podName, containerName))
}

func (c *PodLogCollector) streamPodLogs(podName, containerName string, writer *fileWriter) {
	streamKey := fmt.Sprintf("%s/%s/%s", pkg.PtpLinuxDaemonNamespace, podName, containerName)

	// Check if already streaming
	if !c.streamTracker.markActive(streamKey) {
		return // Already streaming
	}
	defer c.streamTracker.markInactive(streamKey)

	// Get log stream
	logOptions := &corev1.PodLogOptions{
		Follow:     true,
		Timestamps: true,
	}
	if containerName != "" {
		logOptions.Container = containerName
	}

	req := testclient.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).GetLogs(podName, logOptions)
	stream, err := req.Stream(c.ctx)
	if err != nil {
		logrus.Debugf("Failed to stream logs from pod %s: %v", podName, err)
		return
	}
	defer stream.Close()

	// Read and write logs
	reader := bufio.NewReader(stream)
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					logrus.Debugf("Error reading logs from pod %s: %v", podName, err)
				}
				return
			}

			writer.write(fmt.Sprintf("[%s] %s", podName, line))
		}
	}
}

func (c *PodLogCollector) streamExistingPods() {
	c.streamExistingPTPOperatorPods()
	c.streamExistingLinuxPTPDaemonPods()
}

func (c *PodLogCollector) streamExistingPTPOperatorPods() {
	pods, err := testclient.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(
		c.ctx,
		metav1.ListOptions{LabelSelector: pkg.PtPOperatorPodsLabel},
	)
	if err != nil {
		logrus.Errorf("Failed to list ptp-operator pods: %v", err)
		return
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase == corev1.PodRunning {
			c.wg.Add(1)
			go func(p *corev1.Pod) {
				defer c.wg.Done()
				c.streamPodLogs(p.Name, "", c.writers.ptpOperatorWriter)
			}(pod)
		}
	}
}

func (c *PodLogCollector) streamExistingLinuxPTPDaemonPods() {
	pods, err := testclient.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(
		c.ctx,
		metav1.ListOptions{LabelSelector: pkg.PtpLinuxDaemonPodsLabel},
	)
	if err != nil {
		logrus.Errorf("Failed to list linuxptp-daemon pods: %v", err)
		return
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase == corev1.PodRunning && pod.Spec.NodeName != "" {
			c.wg.Add(2)
			go func(p *corev1.Pod) {
				defer c.wg.Done()
				c.streamDaemonContainer(p.Name, p.Spec.NodeName, pkg.PtpContainerName)
			}(pod)
			go func(p *corev1.Pod) {
				defer c.wg.Done()
				c.streamDaemonContainer(p.Name, p.Spec.NodeName, pkg.EventProxyContainerName)
			}(pod)
		}
	}
}

// ============================================================================
// Marker Creation Functions
// ============================================================================

func createStreamStartMarker(podName, containerName string) string {
	return fmt.Sprintf("\n%s [%s] Starting stream from pod: %s, container: %s %s\n",
		streamMarkerLine, time.Now().Format(time.RFC3339), podName, containerName, streamMarkerLine)
}

func createStreamEndMarker(podName, containerName string) string {
	return fmt.Sprintf("\n%s [%s] Stream ended for pod: %s, container: %s %s\n\n",
		streamMarkerLine, time.Now().Format(time.RFC3339), podName, containerName, streamMarkerLine)
}

func createSuiteStartMarker() string {
	return fmt.Sprintf(`
%s
# TEST SUITE STARTED
# Suite: %s
# Time: %s
%s
`, suiteMarkerLine, suiteNameConst, time.Now().Format(time.RFC3339), suiteMarkerLine)
}

func createSuiteEndMarker() string {
	return fmt.Sprintf(`
%s
# TEST SUITE ENDED
# Time: %s
%s
`, suiteMarkerLine, time.Now().Format(time.RFC3339), suiteMarkerLine)
}

func createTestStartMarker(report ginkgo.SpecReport) string {
	fileName := "unknown"
	lineNumber := 0

	if len(report.ContainerHierarchyLocations) > 0 {
		loc := report.ContainerHierarchyLocations[len(report.ContainerHierarchyLocations)-1]
		fileName = filepath.Base(loc.FileName)
		lineNumber = loc.LineNumber
	}

	return fmt.Sprintf(`
%s TEST START %s
# Test: %s
# Started: %s
# File: %s:%d
%s
`,
		testMarkerHeader,
		testMarkerHeader,
		report.FullText(),
		report.StartTime.Format(time.RFC3339),
		fileName,
		lineNumber,
		testMarkerLine,
	)
}

func createTestEndMarker(report ginkgo.SpecReport) string {
	status := "PASSED"
	if report.Failed() {
		status = "FAILED"
	} else if report.State.Is(types.SpecStateSkipped) {
		status = "SKIPPED"
	} else if report.State.Is(types.SpecStatePending) {
		status = "PENDING"
	}

	return fmt.Sprintf(`
%s TEST END %s
# Test: %s
# Status: %s
# Duration: %s
# Ended: %s
%s
`,
		testMarkerHeader,
		testMarkerHeader,
		report.FullText(),
		status,
		report.RunTime.String(),
		report.EndTime.Format(time.RFC3339),
		testMarkerLine,
	)
}

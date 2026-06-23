package pmc

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	v1core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/pods"
)

// AnnounceData holds the parsed data from PMC commands related to Announce messages
type AnnounceData struct {
	// From TIME_PROPERTIES_DATA_SET
	CurrentUtcOffset      int
	Leap61                bool
	Leap59                bool
	CurrentUtcOffsetValid bool
	PtpTimescale          bool
	TimeTraceable         bool
	FrequencyTraceable    bool
	TimeSource            string

	// From CURRENT_DATA_SET
	StepsRemoved     int
	OffsetFromMaster float64
	MeanPathDelay    float64

	// From PARENT_DATA_SET
	ParentPortIdentity                    string
	ParentStats                           int
	GrandmasterIdentity                   string
	GrandmasterClockQuality               ClockQuality
	GrandmasterPriority1                  int
	GrandmasterPriority2                  int
	ObservedParentOffsetScaledLogVariance uint64
	ObservedParentClockPhaseChangeRate    uint64
}

// ClockQuality represents the grandmaster clock quality parameters
type ClockQuality struct {
	ClockClass              int
	ClockAccuracy           string
	OffsetScaledLogVariance string
}

// String returns a formatted string representation of AnnounceData
func (a *AnnounceData) String() string {
	return fmt.Sprintf(`AnnounceData:
	TIME_PROPERTIES_DATA_SET:
		currentUtcOffset:      %d
		leap61:                %v
		leap59:                %v
		currentUtcOffsetValid: %v
		ptpTimescale:          %v
		timeTraceable:         %v
		frequencyTraceable:    %v
		timeSource:            %s
	CURRENT_DATA_SET
		stepsRemoved:     %d
		offsetFromMaster: %f
		meanPathDelay:    %f
	PARENT_DATA_SET
		parentPortIdentity                    %s
		parentStats                           %d
		observedParentOffsetScaledLogVariance %x
		observedParentClockPhaseChangeRate    %x
		grandmasterPriority1                  %d
		gm.clockClass                         %d
		gm.ClockAccuracy                      %x
		gm.OffsetScaledLogVariance            %x
		grandmasterPriority2                  %d
		grandmasterIdentity                   %s`,
		a.CurrentUtcOffset,
		a.Leap61,
		a.Leap59,
		a.CurrentUtcOffsetValid,
		a.PtpTimescale,
		a.TimeTraceable,
		a.FrequencyTraceable,
		a.TimeSource,
		a.StepsRemoved,
		a.OffsetFromMaster,
		a.MeanPathDelay,
		a.ParentPortIdentity,
		a.ParentStats,
		a.ObservedParentOffsetScaledLogVariance,
		a.ObservedParentClockPhaseChangeRate,
		a.GrandmasterPriority1,
		a.GrandmasterClockQuality.ClockClass,
		a.GrandmasterClockQuality.ClockAccuracy,
		a.GrandmasterClockQuality.OffsetScaledLogVariance,
		a.GrandmasterPriority2,
		a.GrandmasterIdentity,
	)
}

// CheckSame compares two ClockQuality structs and returns an error if they differ
func (cq *ClockQuality) CheckSame(other *ClockQuality) error {
	if cq == nil && other == nil {
		return nil
	}
	if cq == nil {
		return fmt.Errorf("first ClockQuality is nil, second is not")
	}
	if other == nil {
		return fmt.Errorf("second ClockQuality is nil, first is not")
	}

	var diffs []string

	if cq.ClockClass != other.ClockClass {
		diffs = append(diffs, fmt.Sprintf("gm.clockClass: %d vs %d", cq.ClockClass, other.ClockClass))
	}
	if !strings.EqualFold(cq.ClockAccuracy, other.ClockAccuracy) {
		diffs = append(diffs, fmt.Sprintf("gm.ClockAccuracy: %s vs %s", cq.ClockAccuracy, other.ClockAccuracy))
	}
	if !strings.EqualFold(cq.OffsetScaledLogVariance, other.OffsetScaledLogVariance) {
		diffs = append(diffs, fmt.Sprintf("gm.OffsetScaledLogVariance: %s vs %s", cq.OffsetScaledLogVariance, other.OffsetScaledLogVariance))
	}

	if len(diffs) > 0 {
		return fmt.Errorf("ClockQuality mismatches: %s", strings.Join(diffs, "; "))
	}

	return nil
}

// CheckSame compares two AnnounceData structs and returns an error if they differ
// Note: offsetFromMaster and meanPathDelay are dynamic values and compared exactly
func (a *AnnounceData) CheckSame(other *AnnounceData) error {
	if a == nil && other == nil {
		return nil
	}
	if a == nil {
		return fmt.Errorf("first AnnounceData is nil, second is not")
	}
	if other == nil {
		return fmt.Errorf("second AnnounceData is nil, first is not")
	}

	var diffs []string

	if a.CurrentUtcOffset != other.CurrentUtcOffset {
		diffs = append(diffs, fmt.Sprintf("currentUtcOffset: %d vs %d", a.CurrentUtcOffset, other.CurrentUtcOffset))
	}
	if a.Leap61 != other.Leap61 {
		diffs = append(diffs, fmt.Sprintf("leap61: %v vs %v", a.Leap61, other.Leap61))
	}
	if a.Leap59 != other.Leap59 {
		diffs = append(diffs, fmt.Sprintf("leap59: %v vs %v", a.Leap59, other.Leap59))
	}
	if a.CurrentUtcOffsetValid != other.CurrentUtcOffsetValid {
		diffs = append(diffs, fmt.Sprintf("currentUtcOffsetValid: %v vs %v", a.CurrentUtcOffsetValid, other.CurrentUtcOffsetValid))
	}
	if a.PtpTimescale != other.PtpTimescale {
		diffs = append(diffs, fmt.Sprintf("ptpTimescale: %v vs %v", a.PtpTimescale, other.PtpTimescale))
	}
	if a.TimeTraceable != other.TimeTraceable {
		diffs = append(diffs, fmt.Sprintf("timeTraceable: %v vs %v", a.TimeTraceable, other.TimeTraceable))
	}
	if a.FrequencyTraceable != other.FrequencyTraceable {
		diffs = append(diffs, fmt.Sprintf("frequencyTraceable: %v vs %v", a.FrequencyTraceable, other.FrequencyTraceable))
	}
	if !strings.EqualFold(a.TimeSource, other.TimeSource) {
		diffs = append(diffs, fmt.Sprintf("timeSource: %s vs %s", a.TimeSource, other.TimeSource))
	}
	if a.StepsRemoved != other.StepsRemoved {
		diffs = append(diffs, fmt.Sprintf("stepsRemoved: %d vs %d", a.StepsRemoved, other.StepsRemoved))
	}
	if a.OffsetFromMaster != other.OffsetFromMaster {
		diffs = append(diffs, fmt.Sprintf("offsetFromMaster: %f vs %f", a.OffsetFromMaster, other.OffsetFromMaster))
	}
	if a.MeanPathDelay != other.MeanPathDelay {
		diffs = append(diffs, fmt.Sprintf("meanPathDelay: %f vs %f", a.MeanPathDelay, other.MeanPathDelay))
	}
	if !strings.EqualFold(a.ParentPortIdentity, other.ParentPortIdentity) {
		diffs = append(diffs, fmt.Sprintf("parentPortIdentity: %s vs %s", a.ParentPortIdentity, other.ParentPortIdentity))
	}
	if a.ParentStats != other.ParentStats {
		diffs = append(diffs, fmt.Sprintf("parentStats: %d vs %d", a.ParentStats, other.ParentStats))
	}
	if !strings.EqualFold(a.GrandmasterIdentity, other.GrandmasterIdentity) {
		diffs = append(diffs, fmt.Sprintf("grandmasterIdentity: %s vs %s", a.GrandmasterIdentity, other.GrandmasterIdentity))
	}
	if err := a.GrandmasterClockQuality.CheckSame(&other.GrandmasterClockQuality); err != nil {
		diffs = append(diffs, fmt.Sprintf("grandmasterClockQuality: %v", err))
	}
	if a.GrandmasterPriority1 != other.GrandmasterPriority1 {
		diffs = append(diffs, fmt.Sprintf("grandmasterPriority1: %d vs %d", a.GrandmasterPriority1, other.GrandmasterPriority1))
	}
	if a.GrandmasterPriority2 != other.GrandmasterPriority2 {
		diffs = append(diffs, fmt.Sprintf("grandmasterPriority2: %d vs %d", a.GrandmasterPriority2, other.GrandmasterPriority2))
	}
	if a.ObservedParentOffsetScaledLogVariance != other.ObservedParentOffsetScaledLogVariance {
		diffs = append(diffs, fmt.Sprintf("observedParentOffsetScaledLogVariance: %x vs %x", a.ObservedParentOffsetScaledLogVariance, other.ObservedParentOffsetScaledLogVariance))
	}
	if a.ObservedParentClockPhaseChangeRate != other.ObservedParentClockPhaseChangeRate {
		diffs = append(diffs, fmt.Sprintf("observedParentClockPhaseChangeRate: %x vs %x", a.ObservedParentClockPhaseChangeRate, other.ObservedParentClockPhaseChangeRate))
	}

	if len(diffs) > 0 {
		return fmt.Errorf("AnnounceData mismatches:\n  - %s", strings.Join(diffs, "\n  - "))
	}

	return nil
}

// Monitor continuously monitors PMC output and sends data on a channel
type Monitor struct {
	pod        *v1core.Pod
	configFile string
	dataChan   chan *AnnounceData
	errorChan  chan error
	stopChan   chan struct{}
	interval   time.Duration
}

// NewMonitor creates a new PMC monitor
func NewMonitor(pod *v1core.Pod, configFile string, interval time.Duration) (*Monitor, error) {
	if pod == nil {
		return nil, fmt.Errorf("PMC monitor requires a non-nil pod")
	}
	return &Monitor{
		pod:        pod,
		configFile: configFile,
		dataChan:   make(chan *AnnounceData, 10), // Buffered channel
		errorChan:  make(chan error, 10),
		stopChan:   make(chan struct{}),
		interval:   interval,
	}, nil
}

// Start begins the PMC monitoring goroutine
func (pm *Monitor) Start() {
	go pm.monitorLoop()
}

// Stop stops the PMC monitoring goroutine
func (pm *Monitor) Stop() {
	close(pm.stopChan)
}

// DataChan returns the channel for receiving PMC data
func (pm *Monitor) DataChan() <-chan *AnnounceData {
	return pm.dataChan
}

// ErrorChan returns the channel for receiving errors
func (pm *Monitor) ErrorChan() <-chan error {
	return pm.errorChan
}

// monitorLoop is the main monitoring loop running in a goroutine
func (pm *Monitor) monitorLoop() {
	ticker := time.NewTicker(pm.interval)
	defer ticker.Stop()
	defer close(pm.dataChan)
	defer close(pm.errorChan)

	for {
		select {
		case <-pm.stopChan:
			logrus.Info("PMC monitor stopped")
			return
		case <-ticker.C:
			data, err := pm.queryPmc()
			if err != nil {
				select {
				case pm.errorChan <- err:
				default:
					// Error channel full, drop error
				}
				continue
			}
			// Skip data with uninitialised ClockClass (0 is never a valid
			// PTP clock class; it means ptp4l hasn't populated PARENT_DATA_SET yet
			// or strconv.Atoi failed silently).
			if data.GrandmasterClockQuality.ClockClass == 0 {
				continue
			}
			select {
			case pm.dataChan <- data:
			default:
				// Data channel full, drop oldest data (consumer not keeping up)
			}
		}
	}
}

func (pm *Monitor) refreshPod() error {
	podList, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: pkg.PtpLinuxDaemonPodsLabel,
	})
	if err != nil {
		return err
	}
	targetNode := ""
	if pm.pod != nil {
		targetNode = pm.pod.Spec.NodeName
	}
	for i := range podList.Items {
		pod := &podList.Items[i]
		if targetNode == "" || pod.Spec.NodeName == targetNode {
			if pm.pod != nil {
				*pm.pod = *pod
			} else {
				pm.pod = pod
			}
			return nil
		}
	}
	return fmt.Errorf("no linuxptp-daemon pod found for node %q", targetNode)
}

func shouldRefreshPod(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "not found") || strings.Contains(msg, "NotFound")
}

// queryPmc executes PMC commands and returns parsed data as a struct
func (pm *Monitor) queryPmc() (*AnnounceData, error) {
	commands := []string{
		"GET TIME_PROPERTIES_DATA_SET",
		"GET CURRENT_DATA_SET",
		"GET PARENT_DATA_SET",
		"GET DEFAULT_DATA_SET",
	}

	data := &AnnounceData{}

	for _, cmd := range commands {
		var buf strings.Builder
		var execErr error
		for attempt := 0; attempt < 2; attempt++ {
			pmcCmd := []string{"pmc", "-b", "0", "-u", "-f", pm.configFile, cmd}
			stdoutBuf, _, err := pods.ExecCommand(client.Client, false, pm.pod, pkg.PtpContainerName, pmcCmd)
			if err == nil {
				buf.Reset()
				buf.WriteString(stdoutBuf.String())
				execErr = nil
				break
			}
			execErr = err
			if attempt == 0 && shouldRefreshPod(err) {
				if refreshErr := pm.refreshPod(); refreshErr == nil {
					logrus.Warnf("PMC monitor refreshed pod to %s after exec error", pm.pod.Name)
					continue
				}
			}
			break
		}
		if execErr != nil {
			return nil, fmt.Errorf("failed to execute pmc command %s: %v", cmd, execErr)
		}

		// Parse the output into the struct
		output := buf.String()
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// Split into fields once
			fields := strings.Fields(line)
			numFields := len(fields)
			// Most PMC fields need at least 2 fields (key + value)
			if numFields < 2 {
				continue
			}

			// Use switch on the field name for efficient parsing
			switch fields[0] {
			// TIME_PROPERTIES_DATA_SET fields
			case "currentUtcOffset":
				data.CurrentUtcOffset, _ = strconv.Atoi(fields[1])
			case "leap61":
				data.Leap61 = fields[1] == "1"
			case "leap59":
				data.Leap59 = fields[1] == "1"
			case "currentUtcOffsetValid":
				data.CurrentUtcOffsetValid = fields[1] == "1"
			case "ptpTimescale":
				data.PtpTimescale = fields[1] == "1"
			case "timeTraceable":
				data.TimeTraceable = fields[1] == "1"
			case "frequencyTraceable":
				data.FrequencyTraceable = fields[1] == "1"
			case "timeSource":
				data.TimeSource = fields[1]

			// CURRENT_DATA_SET fields
			case "stepsRemoved":
				data.StepsRemoved, _ = strconv.Atoi(fields[1])
			case "offsetFromMaster":
				data.OffsetFromMaster, _ = strconv.ParseFloat(fields[1], 64)
			case "meanPathDelay":
				data.MeanPathDelay, _ = strconv.ParseFloat(fields[1], 64)
			case "parentPortIdentity":
				data.ParentPortIdentity = fields[1]
			case "parentStats":
				data.ParentStats, _ = strconv.Atoi(fields[1])
			case "observedParentOffsetScaledLogVariance":
				data.ObservedParentOffsetScaledLogVariance, _ = strconv.ParseUint(fields[1], 16, 64)
			case "observedParentClockPhaseChangeRate":
				data.ObservedParentClockPhaseChangeRate, _ = strconv.ParseUint(fields[1], 16, 64)

			// PARENT_DATA_SET fields
			case "grandmasterIdentity":
				data.GrandmasterIdentity = fields[1]
			case "gm.ClockClass":
				data.GrandmasterClockQuality.ClockClass, _ = strconv.Atoi(fields[1])
			case "gm.ClockAccuracy":
				data.GrandmasterClockQuality.ClockAccuracy = fields[1]
			case "gm.OffsetScaledLogVariance":
				data.GrandmasterClockQuality.OffsetScaledLogVariance = fields[1]
			case "grandmasterPriority1":
				data.GrandmasterPriority1, _ = strconv.Atoi(fields[1])
			case "grandmasterPriority2":
				data.GrandmasterPriority2, _ = strconv.Atoi(fields[1])
			}
		}
	}

	return data, nil
}

// Query executes PMC commands once and returns parsed data as a struct
func Query(pod *v1core.Pod, configFile string) (*AnnounceData, error) {
	monitor, err := NewMonitor(pod, configFile, 0)
	if err != nil {
		return nil, err
	}
	return monitor.queryPmc()
}

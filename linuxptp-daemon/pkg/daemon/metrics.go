package daemon

import (
	"fmt"
	"github.com/golang/glog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilwait "k8s.io/apimachinery/pkg/util/wait"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	ptpv1 "github.com/openshift/ptp-operator/pkg/apis/ptp/v1"
)

const (
	PTPNamespace = "openshift"
	PTPSubsystem = "ptp"

	ptp4lProcessName   = "ptp4l"
	phc2sysProcessName = "phc2sys"
	clockRealTime      = "CLOCK_REALTIME"
	master             = "master"
)

const (
	//LOCKED ...
	LOCKED string = "LOCKED"
	//FREERUN ...
	FREERUN = "FREERUN"
)

type ptpPortRole int

const (
	PASSIVE ptpPortRole = iota
	SLAVE
	MASTER
	FAULTY
	UNKNOWN
)

var (
	NodeName = ""

	OffsetFromMaster = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "offset_from_master",
			Help:      "",
		}, []string{"process", "node", "iface"})

	MaxOffsetFromMaster = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "max_offset_from_master",
			Help:      "",
		}, []string{"process", "node", "iface"})

	OffsetFromSystem = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "offset_from_system",
			Help:      "",
		}, []string{"process", "node", "iface"})

	MaxOffsetFromSystem = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "max_offset_from_system",
			Help:      "",
		}, []string{"process", "node", "iface"})

	FrequencyAdjustment = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "frequency_adjustment",
			Help:      "",
		}, []string{"process", "node", "iface"})

	DelayFromMaster = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "delay_from_master",
			Help:      "",
		}, []string{"process", "node", "iface"})

	// ClockState metrics to show current clock state
	ClockState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "clock_state",
			Help:      "0 = FREERUN, 1 = LOCKED, 2 = HOLDOVER",
		}, []string{"process", "node", "iface"})

	// Threshold metrics to show current ptp threshold
	InterfaceRole = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "ptp_interface_role",
			Help:      "0 = PASSIVE 1 = SLAVE 2 = MASTER 3 = FAULTY",
		}, []string{"process", "node", "iface"})
)

var registerMetrics sync.Once

func RegisterMetrics(nodeName string) {
	registerMetrics.Do(func() {
		prometheus.MustRegister(OffsetFromMaster)
		prometheus.MustRegister(MaxOffsetFromMaster)
		prometheus.MustRegister(FrequencyAdjustment)
		prometheus.MustRegister(DelayFromMaster)
		prometheus.MustRegister(OffsetFromSystem)
		prometheus.MustRegister(MaxOffsetFromSystem)
		prometheus.MustRegister(InterfaceRole)
		prometheus.MustRegister(ClockState)

		// Including these stats kills performance when Prometheus polls with multiple targets
		prometheus.Unregister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
		prometheus.Unregister(prometheus.NewGoCollector())

		NodeName = nodeName
	})
}

// updatePTPMetrics ...
func updatePTPMetrics(process, iface string, offsetFromMaster, maxOffsetFromMaster, frequencyAdjustment, delayFromMaster float64) {
	OffsetFromMaster.With(prometheus.Labels{
		"process": process, "node": NodeName, "iface": iface}).Set(offsetFromMaster)

	MaxOffsetFromMaster.With(prometheus.Labels{
		"process": process, "node": NodeName, "iface": iface}).Set(maxOffsetFromMaster)

	FrequencyAdjustment.With(prometheus.Labels{
		"process": process, "node": NodeName, "iface": iface}).Set(frequencyAdjustment)

	DelayFromMaster.With(prometheus.Labels{
		"process": process, "node": NodeName, "iface": iface}).Set(delayFromMaster)
}

// updatePTPSystemMetrics ...
func updatePTPSystemMetrics(process, iface string, offsetFromMaster, maxOffsetFromMaster, frequencyAdjustment, delayFromMaster float64) {
	OffsetFromSystem.With(prometheus.Labels{
		"process": process, "node": NodeName, "iface": iface}).Set(offsetFromMaster)

	MaxOffsetFromSystem.With(prometheus.Labels{
		"process": process, "node": NodeName, "iface": iface}).Set(maxOffsetFromMaster)

	FrequencyAdjustment.With(prometheus.Labels{
		"process": process, "node": NodeName, "iface": iface}).Set(frequencyAdjustment)

	DelayFromMaster.With(prometheus.Labels{
		"process": process, "node": NodeName, "iface": iface}).Set(delayFromMaster)
}

// extractMetrics ...
func extractMetrics(configName, processName string, ifaces []string, output string) {
	if strings.Contains(output, " max ") {
		ifaceName, offsetFromMaster, maxOffsetFromMaster, frequencyAdjustment, delayFromMaster := extractSummaryMetrics(configName, processName, output)
		if ifaceName != "" {
			if ifaceName == clockRealTime || ifaceName == master {
				updatePTPMetrics(processName, ifaceName, offsetFromMaster, maxOffsetFromMaster, frequencyAdjustment, delayFromMaster)
			} else {
				updatePTPSystemMetrics(processName, ifaceName, offsetFromMaster, maxOffsetFromMaster, frequencyAdjustment, delayFromMaster)
			}
		}

	} else if strings.Contains(output, " offset ") {
		ifaceName, clockstate, offsetFromMaster, maxOffsetFromMaster, frequencyAdjustment, delayFromMaster := extractRegularMetrics(configName, processName, output)
		if ifaceName != "" {
			if ifaceName == clockRealTime || ifaceName == master {
				updatePTPMetrics(processName, ifaceName, offsetFromMaster, maxOffsetFromMaster, frequencyAdjustment, delayFromMaster)
			} else {
				updatePTPSystemMetrics(processName, ifaceName, offsetFromMaster, maxOffsetFromMaster, frequencyAdjustment, delayFromMaster)
			}
			updateClockStateMetrics(processName, ifaceName, clockstate)
		}
	}
	if processName == ptp4lProcessName {
		if portId, role := extractPTP4lEventState(output); portId > 0 {
			if len(ifaces) >= portId-1 {
				UpdateInterfaceRoleMetrics(processName, ifaces[portId-1], role)
			}
		}
	}
}

func extractSummaryMetrics(configName, processName, output string) (iface string, offsetFromMaster, maxOffsetFromMaster, frequencyAdjustment, delayFromMaster float64) {
	// remove everything before the rms string
	// This makes the out to equals

	indx := strings.Index(output, "rms")
	if indx < 0 {
		return
	}

	replacer := strings.NewReplacer("[", " ", "]", " ", ":", " ")
	output = replacer.Replace(output)

	indx = strings.Index(output, configName)
	output = output[indx:]
	fields := strings.Fields(output)

	//ptp4l.0.config CLOCK_REALTIME rms   31 max   31 freq -77331 +/-   0 delay  1233 +/-   0
	if len(fields) < 8 {
		glog.Errorf("%s failed to parse output %s: unexpected number of fields", processName, output)
		return
	}

	iface = fields[1]

	offsetFromMaster, err := strconv.ParseFloat(fields[3], 64)
	if err != nil {
		glog.Errorf("%s failed to parse offset from master output %s error %v", processName, fields[3], err)
	}

	maxOffsetFromMaster, err = strconv.ParseFloat(fields[5], 64)
	if err != nil {
		glog.Errorf("%s failed to parse max offset from master output %s error %v", processName, fields[5], err)
	}

	frequencyAdjustment, err = strconv.ParseFloat(fields[7], 64)
	if err != nil {
		glog.Errorf("%s failed to parse frequency adjustment output %s error %v", processName, fields[7], err)
	}

	if len(fields) >= 11 {
		delayFromMaster, err = strconv.ParseFloat(fields[11], 64)
		if err != nil {
			glog.Errorf("%s failed to parse delay from master output %s error %v", processName, fields[11], err)
		}
	} else {
		// If there is no delay from master this mean we are out of sync
		glog.Warningf("no delay from master process %s out of sync", processName)
	}

	return
}

func extractRegularMetrics(configName, processName, output string) (iface, clockState string, offsetFromMaster, maxOffsetFromMaster, frequencyAdjustment, delayFromMaster float64) {
	// remove everything before the rms string
	// This makes the out to equals
	indx := strings.Index(output, "offset")
	if indx < 0 {
		return
	}

	output = strings.Replace(output, "path", "", 1)
	replacer := strings.NewReplacer("[", " ", "]", " ", ":", " ", "phc", "")
	output = replacer.Replace(output)

	index := strings.Index(output, configName)
	if index == -1 {
		return
	}
	output = output[index:]
	fields := strings.Fields(output)

	if len(fields) < 7 {
		glog.Errorf("%s failed to parse output %s: unexpected number of fields", processName, output)
		return
	}

	iface = fields[1]

	//ptp4l.0.config master offset   -2162130 s2 freq +22451884 path delay    374976
	offsetFromMaster, err := strconv.ParseFloat(fields[3], 64)
	if err != nil {
		glog.Errorf("%s failed to parse offset from master output %s error %v", processName, fields[1], err)
	}

	maxOffsetFromMaster, err = strconv.ParseFloat(fields[3], 64)
	if err != nil {
		glog.Errorf("%s failed to parse max offset from master output %s error %v", processName, fields[1], err)
	}

	switch fields[4] {
	case "s0":
		clockState = FREERUN
	case "s1":
		clockState = FREERUN
	case "s2":
		clockState = LOCKED
	default:
		clockState = FREERUN
	}

	frequencyAdjustment, err = strconv.ParseFloat(fields[6], 64)
	if err != nil {
		glog.Errorf("%s failed to parse frequency adjustment output %s error %v", processName, fields[6], err)
	}

	if len(fields) > 8 {
		delayFromMaster, err = strconv.ParseFloat(fields[8], 64)
		if err != nil {
			glog.Errorf("%s failed to parse delay from master output %s error %v", processName, fields[8], err)
		}
	} else {
		// If there is no delay from master this mean we are out of sync
		glog.Warningf("no delay from master process %s out of sync", processName)
	}

	return
}

// updateClockStateMetrics ...
func updateClockStateMetrics(process, iface string, state string) {
	if state == LOCKED {
		ClockState.With(prometheus.Labels{
			"process": process, "node": NodeName, "iface": iface}).Set(1)
	} else {
		ClockState.With(prometheus.Labels{
			"process": process, "node": NodeName, "iface": iface}).Set(0)
	}
}

func UpdateInterfaceRoleMetrics(process string, iface string, role ptpPortRole) {
	InterfaceRole.With(prometheus.Labels{
		"process": process, "node": NodeName, "iface": iface}).Set(float64(role))
}

func extractPTP4lEventState(output string) (portId int, role ptpPortRole) {
	replacer := strings.NewReplacer("[", " ", "]", " ", ":", " ")
	output = replacer.Replace(output)

	index := strings.Index(output, " port ")
	if index == -1 {
		return
	}
	output = output[index:]
	fields := strings.Fields(output)
	portIndex := fields[1]
	role = UNKNOWN
	var e error
	portId, e = strconv.Atoi(portIndex)
	if e != nil {
		glog.Errorf("error parsing port id %s", e)
		portId = 0
		return
	}

	if strings.Contains(output, "UNCALIBRATED to SLAVE") {
		role = SLAVE
	} else if strings.Contains(output, "UNCALIBRATED to PASSIVE") || strings.Contains(output, "MASTER to PASSIVE") ||
		strings.Contains(output, "SLAVE to PASSIVE") {
		role = PASSIVE
	} else if strings.Contains(output, "UNCALIBRATED to MASTER") || strings.Contains(output, "LISTENING to MASTER") {
		role = MASTER
	} else if strings.Contains(output, "FAULT_DETECTED") || strings.Contains(output, "SYNCHRONIZATION_FAULT") {
		role = FAULTY
	} else {
		portId = 0
	}
	return
}

func addFlagsForMonitor(nodeProfile *ptpv1.PtpProfile, stdoutToSocket bool) {
	// If output doesn't exist we add it for the prometheus exporter
	if nodeProfile.Phc2sysOpts != nil {
		if !strings.Contains(*nodeProfile.Phc2sysOpts, "-m") {
			glog.Info("adding -m to print messages to stdout for phc2sys to use prometheus exporter")
			*nodeProfile.Phc2sysOpts = fmt.Sprintf("%s -m", *nodeProfile.Phc2sysOpts)
		}
		// stdoutToSocket is for sidecar to consume events, -u  will not generate logs with offset and clock state.
		// disable -u for  events
		if stdoutToSocket && strings.Contains(*nodeProfile.Phc2sysOpts, "-u") {
			glog.Error("-u option will not generate clock state events,  remove -u option")
		} else if !stdoutToSocket && !strings.Contains(*nodeProfile.Phc2sysOpts, "-u") {
			glog.Info("adding -u 1 to print summary messages to stdout for phc2sys to use prometheus exporter")
			*nodeProfile.Phc2sysOpts = fmt.Sprintf("%s -u 1", *nodeProfile.Phc2sysOpts)
		}
	}

	// If output doesn't exist we add it for the prometheus exporter
	if nodeProfile.Ptp4lOpts != nil {
		if !strings.Contains(*nodeProfile.Ptp4lOpts, "-m") {
			glog.Info("adding -m to print messages to stdout for ptp4l to use prometheus exporter")
			*nodeProfile.Ptp4lOpts = fmt.Sprintf("%s -m", *nodeProfile.Ptp4lOpts)
		}

		if !strings.Contains(*nodeProfile.Ptp4lOpts, "--summary_interval") {
			glog.Info("adding --summary_interval 1 to print summary messages to stdout for ptp4l to use prometheus exporter")
			*nodeProfile.Ptp4lOpts = fmt.Sprintf("%s --summary_interval 1", *nodeProfile.Ptp4lOpts)
		}
	}
}

// StartMetricsServer runs the prometheus listner so that metrics can be collected
func StartMetricsServer(bindAddress string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	go utilwait.Until(func() {
		err := http.ListenAndServe(bindAddress, mux)
		if err != nil {
			utilruntime.HandleError(fmt.Errorf("starting metrics server failed: %v", err))
		}
	}, 5*time.Second, utilwait.NeverStop)
}

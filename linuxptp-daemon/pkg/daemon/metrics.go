package daemon

import (
	"fmt"
	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilwait "k8s.io/apimachinery/pkg/util/wait"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"
)

const (
	PTPNamespace = "openshift"
	PTPSubsystem = "ptp"

	ptp4lProcessName = "ptp4l"
	phcProcessName   = "phc2sys"
	clockRealTime    = "CLOCK_REALTIME"
	master           = "master"

	faultyOffset = 999999

	offset = "offset"
	rms    = "rms"

	// offset source
	phc = "phc"
	sys = "sys"
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
	masterOffsetIfaceName map[string]string // by slave iface with masked index
	slaveIfaceName        map[string]string // current slave iface name
	NodeName              = ""
	ptp4lConfigIndex      = regexp.MustCompile("[0-9]+")

	Offset = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "offset_ns",
			Help:      "",
		}, []string{"from", "process", "node", "iface"})

	MaxOffset = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "max_offset_ns",
			Help:      "",
		}, []string{"from", "process", "node", "iface"})

	FrequencyAdjustment = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "frequency_adjustment_ns",
			Help:      "",
		}, []string{"from", "process", "node", "iface"})

	Delay = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "delay_ns",
			Help:      "",
		}, []string{"from", "process", "node", "iface"})

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
			Name:      "interface_role",
			Help:      "0 = PASSIVE, 1 = SLAVE, 2 = MASTER, 3 = FAULTY, 4 = UNKNOWN",
		}, []string{"process", "node", "iface"})
)

var registerMetrics sync.Once

func RegisterMetrics(nodeName string) {
	registerMetrics.Do(func() {
		prometheus.MustRegister(Offset)
		prometheus.MustRegister(MaxOffset)
		prometheus.MustRegister(FrequencyAdjustment)
		prometheus.MustRegister(Delay)
		prometheus.MustRegister(InterfaceRole)
		prometheus.MustRegister(ClockState)

		// Including these stats kills performance when Prometheus polls with multiple targets
		prometheus.Unregister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
		prometheus.Unregister(collectors.NewGoCollector())

		NodeName = nodeName
	})

	masterOffsetIfaceName = map[string]string{}
	slaveIfaceName = map[string]string{}
}

// updatePTPMetrics ...
func updatePTPMetrics(from, process, iface string, ptpOffset, maxPtpOffset, frequencyAdjustment, delay float64) {
	Offset.With(prometheus.Labels{"from": from,
		"process": process, "node": NodeName, "iface": iface}).Set(ptpOffset)

	MaxOffset.With(prometheus.Labels{"from": from,
		"process": process, "node": NodeName, "iface": iface}).Set(maxPtpOffset)

	FrequencyAdjustment.With(prometheus.Labels{"from": from,
		"process": process, "node": NodeName, "iface": iface}).Set(frequencyAdjustment)

	Delay.With(prometheus.Labels{"from": from,
		"process": process, "node": NodeName, "iface": iface}).Set(delay)
}

// extractMetrics ...
func extractMetrics(configName, processName string, ifaces []string, output string) {
	if strings.Contains(output, " max ") {
		ifaceName, ptpOffset, maxPtpOffset, frequencyAdjustment, delay := extractSummaryMetrics(configName, processName, output)
		if ifaceName != "" {
			if ifaceName == clockRealTime {
				updatePTPMetrics(phc, processName, ifaceName, ptpOffset, maxPtpOffset, frequencyAdjustment, delay)
			} else {
				updatePTPMetrics(master, processName, ifaceName, ptpOffset, maxPtpOffset, frequencyAdjustment, delay)
			}
		}
	} else if strings.Contains(output, " offset ") {
		err, ifaceName, clockstate, ptpOffset, maxPtpOffset, frequencyAdjustment, delay := extractRegularMetrics(configName, processName, output)
		if err != nil {
			glog.Error(err.Error())

		} else if ifaceName != "" {
			offsetSource := master
			if strings.Contains(output, "sys offset") {
				offsetSource = sys
			} else if strings.Contains(output, "phc offset") {
				offsetSource = phc
			}
			updatePTPMetrics(offsetSource, processName, ifaceName, ptpOffset, maxPtpOffset, frequencyAdjustment, delay)
			updateClockStateMetrics(processName, ifaceName, clockstate)
		}
	}
	if processName == ptp4lProcessName {
		if portId, role := extractPTP4lEventState(output); portId > 0 {
			if len(ifaces) >= portId-1 {
				UpdateInterfaceRoleMetrics(processName, ifaces[portId-1], role)
				if role == SLAVE {
					r := []rune(ifaces[portId-1])
					masterOffsetIfaceName[configName] = string(r[:len(r)-1]) + "x"
					slaveIfaceName[configName] = ifaces[portId-1]
				} else if role == FAULTY {
					if isSlaveFaulty(configName, ifaces[portId-1]) {
						updatePTPMetrics(master, processName, getMasterOffsetIfaceName(configName), faultyOffset, faultyOffset, 0, 0)
						updatePTPMetrics(phc, phcProcessName, clockRealTime, faultyOffset, faultyOffset, 0, 0)
						masterOffsetIfaceName[configName] = ""
						slaveIfaceName[configName] = ""
					}
				}
			}
		}
	}
}

func extractSummaryMetrics(configName, processName, output string) (iface string, ptpOffset, maxPtpOffset, frequencyAdjustment, delay float64) {

	// phc2sys[5196755.139]: [ptp4l.0.config] ens5f0 rms 3152778 max 3152778 freq -6083928 +/-   0 delay  2791 +/-   0
	// phc2sys[3560354.300]: [ptp4l.0.config] CLOCK_REALTIME rms    4 max    4 freq -76829 +/-   0 delay  1085 +/-   0
	// ptp4l[74737.942]: [ptp4l.0.config] rms  53 max   74 freq -16642 +/-  40 delay  1089 +/-  20
	// or
	// ptp4l[365195.391]: [ptp4l.0.config] master offset         -1 s2 freq   -3972 path delay        89

	rmsIndex := strings.Index(output, rms)
	if rmsIndex < 0 {
		return
	}

	replacer := strings.NewReplacer("[", " ", "]", " ", ":", " ")
	output = replacer.Replace(output)

	indx := strings.Index(output, configName)
	if indx == -1 {
		return
	}
	output = output[indx:]
	fields := strings.Fields(output)

	// 0                1            2     3 4      5  6    7      8    9  10     11
	//ptp4l.0.config CLOCK_REALTIME rms   31 max   31 freq -77331 +/-   0 delay  1233 +/-   0
	if len(fields) < 8 {
		glog.Errorf("%s failed to parse output %s: unexpected number of fields", processName, output)
		return
	}

	// when ptp4l log for master offset
	if fields[1] == rms { // if first field is rms , then add master
		fields = append(fields, "") // Making space for the new element
		//  0             1     2
		//ptp4l.0.config rms   53 max   74 freq -16642 +/-  40 delay  1089 +/-  20
		copy(fields[2:], fields[1:])                     // Shifting elements
		fields[1] = getMasterOffsetIfaceName(configName) // Copying/inserting the value
		//  0             0       1   2
		//ptp4l.0.config master rms   53 max   74 freq -16642 +/-  40 delay  1089 +/-  20
	} else if fields[1] != "CLOCK_REALTIME" {
		// phc2sys[5196755.139]: [ptp4l.0.config] ens5f0 rms 3152778 max 3152778 freq -6083928 +/-   0 delay  2791 +/-   0
		return // do not register offset value for master port reported by phc2sys
	}

	iface = fields[1]

	ptpOffset, err := strconv.ParseFloat(fields[3], 64)
	if err != nil {
		glog.Errorf("%s failed to parse offset from the output %s error %v", processName, fields[3], err)
	}

	maxPtpOffset, err = strconv.ParseFloat(fields[5], 64)
	if err != nil {
		glog.Errorf("%s failed to parse max offset from the output %s error %v", processName, fields[5], err)
	}

	frequencyAdjustment, err = strconv.ParseFloat(fields[7], 64)
	if err != nil {
		glog.Errorf("%s failed to parse frequency adjustment output %s error %v", processName, fields[7], err)
	}

	if len(fields) >= 11 {
		delay, err = strconv.ParseFloat(fields[11], 64)
		if err != nil {
			glog.Errorf("%s failed to parse delay from the output %s error %v", processName, fields[11], err)
		}
	} else {
		// If there is no delay from master this mean we are out of sync
		glog.Warningf("no delay from master process %s out of sync", processName)
	}

	return
}

func extractRegularMetrics(configName, processName, output string) (err error, iface, clockState string, ptpOffset, maxPtpOffset, frequencyAdjustment, delay float64) {
	indx := strings.Index(output, offset)
	if indx < 0 {
		return
	}

	output = strings.Replace(output, "path", "", 1)
	replacer := strings.NewReplacer("[", " ", "]", " ", ":", " ", "phc", "", "sys", "")
	output = replacer.Replace(output)

	index := strings.Index(output, configName)
	if index == -1 {
		return
	}

	output = output[index:]
	fields := strings.Fields(output)

	//       0         1      2          3    4   5    6          7     8
	//ptp4l.0.config master offset          4 s2 freq   -3964 path delay        91
	if len(fields) < 7 {
		err = fmt.Errorf("%s failed to parse output %s: unexpected number of fields", processName, output)
		return
	}

	if fields[2] != offset {
		err = fmt.Errorf("%s failed to parse offset from the output %s error %s", processName, fields[1], "offset is not in right order")
		return
	}

	iface = fields[1]
	if iface != clockRealTime && iface != master {
		return // ignore master port offsets
	}

	// replace master offset from master to slaveInterface - index + x ens01== ens0X
	if iface == master {
		iface = getMasterOffsetIfaceName(configName)
	}

	ptpOffset, e := strconv.ParseFloat(fields[3], 64)
	if e != nil {
		err = fmt.Errorf("%s failed to parse offset from the output %s error %v", processName, fields[1], err)
		return
	}

	maxPtpOffset, err = strconv.ParseFloat(fields[3], 64)
	if err != nil {
		err = fmt.Errorf("%s failed to parse max offset from the output %s error %v", processName, fields[1], err)
		return
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
		err = fmt.Errorf("%s failed to parse frequency adjustment output %s error %v", processName, fields[6], err)
		return
	}

	if len(fields) > 8 {
		delay, err = strconv.ParseFloat(fields[8], 64)
		if err != nil {
			err = fmt.Errorf("%s failed to parse delay from the output %s error %v", processName, fields[8], err)
		}
	} else {
		// If there is no delay this mean we are out of sync
		glog.Warningf("no delay from the process %s out of sync", processName)
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

	//ptp4l 4268779.809 ptp4l.o.config port 2: LISTENING to PASSIVE on RS_PASSIVE
	//ptp4l 4268779.809 ptp4l.o.config port 1: delay timeout
	index := strings.Index(output, " port ")
	if index == -1 {
		return
	}

	output = output[index:]
	fields := strings.Fields(output)

	//port 1: delay timeout
	if len(fields) < 2 {
		glog.Errorf("failed to parse output %s: unexpected number of fields", output)
		return
	}

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

func addFlagsForMonitor(nodeProfile *ptpv1.PtpProfile, conf *ptp4lConf, stdoutToSocket bool) {
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
			_, exist := conf.sections["[global]"].options["summary_interval"]
			if !exist {
				glog.Info("adding summary_interval 1 to print summary messages to stdout for ptp4l to use prometheus exporter")
				conf.sections["[global]"].options["summary_interval"] = "1"
			}
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

func getMasterOffsetIfaceName(configName string) string {
	if s, found := masterOffsetIfaceName[configName]; found {
		return s
	}
	return ""
}

func isSlaveFaulty(configName string, iface string) bool {
	if s, found := slaveIfaceName[configName]; found {
		if s == iface {
			return true
		}
	}
	return false
}

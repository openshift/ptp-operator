package daemon

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/openshift/linuxptp-daemon/pkg/config"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilwait "k8s.io/apimachinery/pkg/util/wait"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	PTPNamespace = "openshift"
	PTPSubsystem = "ptp"

	ptp4lProcessName   = "ptp4l"
	phc2sysProcessName = "phc2sys"
	ts2phcProcessName  = "ts2phc"
	clockRealTime      = "CLOCK_REALTIME"
	master             = "master"

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

const (
	PtpProcessDown int64 = 0
	PtpProcessUp   int64 = 1
)

type ptpPortRole int

const (
	PASSIVE ptpPortRole = iota
	SLAVE
	MASTER
	FAULTY
	UNKNOWN
)

type masterOffsetInterface struct { // by slave iface with masked index
	sync.RWMutex
	iface map[string]ptpInterface
}
type ptpInterface struct {
	name  string
	alias string
}
type slaveInterface struct { // current slave iface name
	sync.RWMutex
	name map[string]string
}

type masterOffsetSourceProcess struct { // current slave iface name
	sync.RWMutex
	name map[string]string
}

var (
	masterOffsetIface  *masterOffsetInterface     // by slave iface with masked index
	slaveIface         *slaveInterface            // current slave iface name
	masterOffsetSource *masterOffsetSourceProcess // master offset source
	NodeName           = ""

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

	// ClockClassMetrics metrics to show current clock class
	ClockClassMetrics = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "clock_class",
			Help:      "6 = Locked, 7 = PRC unlocked in-spec, 52/187 = PRC unlocked out-of-spec, 135 = T-BC holdover in-spec, 165 = T-BC holdover out-of-spec, 248 = Default, 255 = Slave Only Clock",
		}, []string{"process", "node"})

	// InterfaceRole metrics to show current interface role
	InterfaceRole = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "interface_role",
			Help:      "0 = PASSIVE, 1 = SLAVE, 2 = MASTER, 3 = FAULTY, 4 = UNKNOWN",
		}, []string{"process", "node", "iface"})

	ProcessStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "process_status",
			Help:      "0 = DOWN, 1 = UP",
		}, []string{"process", "node", "config"})

	ProcessRestartCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "process_restart_count",
			Help:      "",
		}, []string{"process", "node", "config"})
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
		prometheus.MustRegister(ProcessStatus)
		prometheus.MustRegister(ProcessRestartCount)
		prometheus.MustRegister(ClockClassMetrics)

		// Including these stats kills performance when Prometheus polls with multiple targets
		prometheus.Unregister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
		prometheus.Unregister(collectors.NewGoCollector())

		NodeName = nodeName
	})

}

// InitializeOffsetMaps ... initialize maps
func InitializeOffsetMaps() {
	masterOffsetIface = &masterOffsetInterface{
		RWMutex: sync.RWMutex{},
		iface:   map[string]ptpInterface{},
	}
	slaveIface = &slaveInterface{
		RWMutex: sync.RWMutex{},
		name:    map[string]string{},
	}
	masterOffsetSource = &masterOffsetSourceProcess{
		RWMutex: sync.RWMutex{},
		name:    map[string]string{},
	}
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
func extractMetrics(messageTag string, processName string, ifaces []config.Iface, output string) (configName, source string, offset float64, state string, iface string) {
	configName = strings.Replace(strings.Replace(messageTag, "]", "", 1), "[", "", 1)
	if strings.Contains(output, " max ") {
		ifaceName, ptpOffset, maxPtpOffset, frequencyAdjustment, delay := extractSummaryMetrics(configName, processName, output)
		if ifaceName != "" {
			if ifaceName == clockRealTime {
				updatePTPMetrics(phc, processName, ifaceName, ptpOffset, maxPtpOffset, frequencyAdjustment, delay)
			} else {
				updatePTPMetrics(master, processName, ifaceName, ptpOffset, maxPtpOffset, frequencyAdjustment, delay)
				masterOffsetSource.set(configName, processName)
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
			if offsetSource == master {
				masterOffsetSource.set(configName, processName)
			}
			updatePTPMetrics(offsetSource, processName, ifaceName, ptpOffset, maxPtpOffset, frequencyAdjustment, delay)
			updateClockStateMetrics(processName, ifaceName, clockstate)
		}
		source = processName
		offset = ptpOffset
		state = clockstate
		iface = ifaceName
	}
	if processName == ptp4lProcessName {
		if portId, role := extractPTP4lEventState(output); portId > 0 {
			if len(ifaces) >= portId-1 {
				UpdateInterfaceRoleMetrics(processName, ifaces[portId-1].Name, role)
				if role == SLAVE {
					masterOffsetIface.set(configName, ifaces[portId-1].Name)
					slaveIface.set(configName, ifaces[portId-1].Name)
				} else if role == FAULTY {
					if slaveIface.isFaulty(configName, ifaces[portId-1].Name) &&
						masterOffsetSource.get(configName) == ptp4lProcessName {
						updatePTPMetrics(master, processName, masterOffsetIface.get(configName).alias, faultyOffset, faultyOffset, 0, 0)
						updatePTPMetrics(phc, phc2sysProcessName, clockRealTime, faultyOffset, faultyOffset, 0, 0)
						updateClockStateMetrics(processName, masterOffsetIface.get(configName).alias, FREERUN)
						masterOffsetIface.set(configName, "")
						slaveIface.set(configName, "")
					}
				}
			}
		}
	}
	return
}

func extractSummaryMetrics(configName, processName, output string) (iface string, ptpOffset, maxPtpOffset, frequencyAdjustment, delay float64) {

	// phc2sys[5196755.139]: [ptp4l.0.config] ens5f0 rms 3152778 max 3152778 freq -6083928 +/-   0 delay  2791 +/-   0
	// phc2sys[3560354.300]: [ptp4l.0.config] CLOCK_REALTIME rms    4 max    4 freq -76829 +/-   0 delay  1085 +/-   0
	// ptp4l[74737.942]: [ptp4l.0.config] rms  53 max   74 freq -16642 +/-  40 delay  1089 +/-  20
	// or
	// ptp4l[365195.391]: [ptp4l.0.config] master offset         -1 s2 freq   -3972 path delay        89
	// ts2phc[82674.465]: [ts2phc.0.cfg] nmea delay: 88403525 ns
	// ts2phc[82674.465]: [ts2phc.0.cfg] ens2f1 extts index 0 at 1673031129.000000000 corr 0 src 1673031129.911642976 diff 0
	// ts2phc[82674.465]: [ts2phc.0.cfg] ens2f1 master offset          0 s2 freq      -0
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
		return
	}

	// when ptp4l log for master offset
	if fields[1] == rms { // if first field is rms , then add master
		fields = append(fields, "") // Making space for the new element
		//  0             1     2
		//ptp4l.0.config rms   53 max   74 freq -16642 +/-  40 delay  1089 +/-  20
		copy(fields[2:], fields[1:])                       // Shifting elements
		fields[1] = masterOffsetIface.get(configName).name // Copying/inserting the value
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
	replacer := strings.NewReplacer("[", " ", "]", " ", ":", " ", " phc ", " ", " sys ", "")
	output = replacer.Replace(output)

	index := strings.Index(output, configName)
	if index == -1 {
		return
	}

	output = output[index:]
	fields := strings.Fields(output)

	//       0         1      2          3     4   5    6          7     8
	// ptp4l.0.config master offset   -2162130 s2 freq +22451884  delay 374976
	// ts2phc.0.cfg  ens2f1  master    offset          0 s2 freq      -0
	// for multi card GNSS
	// ts2phc.0.cfg  /dev/ptp0  master    offset          0 s2 freq      -0
	// ts2phc.0.cfg  /def/ptp4  master    offset          0 s2 freq      -0
	// (ts2phc.0.cfg  master  offset      0    s2 freq     -0)
	if len(fields) < 7 {
		return
	}

	if fields[3] == offset && processName == ts2phcProcessName {
		// Remove the element at index 1 from fields.
		masterOffsetIface.set(configName, fields[1])
		slaveIface.set(configName, fields[1])
		copy(fields[1:], fields[2:])
		// ts2phc.0.cfg  master    offset          0 s2 freq      -0
		fields = fields[:len(fields)-1] // Truncate slice.
		delay = 0
	}

	//       0         1      2          3    4   5    6          7     8
	//ptp4l.0.config master offset          4 s2 freq   -3964 path delay        91
	//ts2phc.0.cfg  ens2f1  master    offset          0 s2 freq      -0
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

	if iface == master {
		iface = masterOffsetIface.get(configName).alias
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

	if processName == ts2phcProcessName {
		// ts2phc acts as GM so no path delay
		delay = 0
	} else if len(fields) > 8 {
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

// UpdateClockClassMetrics ... update clock class metrics
func UpdateClockClassMetrics(clockClass float64) {
	ClockClassMetrics.With(prometheus.Labels{
		"process": ptp4lProcessName, "node": NodeName}).Set(float64(clockClass))
}

func UpdateProcessStatusMetrics(process, cfgName string, status int64) {
	ProcessStatus.With(prometheus.Labels{
		"process": process, "node": NodeName, "config": cfgName}).Set(float64(status))
	if status == PtpProcessUp {
		ProcessRestartCount.With(prometheus.Labels{
			"process": process, "node": NodeName, "config": cfgName}).Inc()
	}
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

func addFlagsForMonitor(process string, configOpts *string, conf *ptp4lConf, stdoutToSocket bool) {
	switch process {
	case "ptp4l":
		// If output doesn't exist we add it for the prometheus exporter
		if configOpts != nil {
			if !strings.Contains(*configOpts, "-m") {
				glog.Info("adding -m to print messages to stdout for ptp4l to use prometheus exporter")
				*configOpts = fmt.Sprintf("%s -m", *configOpts)
			}

			if !strings.Contains(*configOpts, "--summary_interval") {
				for index, section := range conf.sections {
					if section.sectionName == "[global]" {
						_, exist := section.options["summary_interval"]
						if !exist {
							glog.Info("adding summary_interval 1 to print summary messages to stdout for ptp4l to use prometheus exporter")
							section.options["summary_interval"] = "1"
						}
						conf.sections[index] = section
					}
				}
			}
		}
	case "phc2sys":
		// If output doesn't exist we add it for the prometheus exporter
		if configOpts != nil && *configOpts != "" {
			if !strings.Contains(*configOpts, "-m") {
				glog.Info("adding -m to print messages to stdout for phc2sys to use prometheus exporter")
				*configOpts = fmt.Sprintf("%s -m", *configOpts)
			}
			// stdoutToSocket is for sidecar to consume events, -u  will not generate logs with offset and clock state.
			// disable -u for  events
			if stdoutToSocket && strings.Contains(*configOpts, "-u") {
				glog.Error("-u option will not generate clock state events,  remove -u option")
			} else if !stdoutToSocket && !strings.Contains(*configOpts, "-u") {
				glog.Info("adding -u 1 to print summary messages to stdout for phc2sys to use prometheus exporter")
				*configOpts = fmt.Sprintf("%s -u 1", *configOpts)
			}
		}
	case "ts2phc":
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

func (m *masterOffsetInterface) get(configName string) ptpInterface {
	m.RLock()
	defer m.RUnlock()
	if s, found := m.iface[configName]; found {
		return s
	}
	return ptpInterface{
		name:  "",
		alias: "",
	}
}
func (m *masterOffsetInterface) getByAlias(configName string, alias string) ptpInterface {
	m.RLock()
	defer m.RUnlock()
	if s, found := m.iface[configName]; found {
		if s.alias == alias {
			return s
		}
	}
	return ptpInterface{
		name:  alias,
		alias: alias,
	}
}

func (m *masterOffsetInterface) getAliasByName(configName string, name string) ptpInterface {
	if name == clockRealTime || name == master {
		return ptpInterface{
			name:  name,
			alias: name,
		}
	}
	m.RLock()
	defer m.RUnlock()
	if s, found := m.iface[configName]; found {
		if s.name == name {
			return s
		}
	}
	return ptpInterface{
		name:  name,
		alias: name,
	}
}

func (m *masterOffsetInterface) set(configName string, value string) {
	m.Lock()
	defer m.Unlock()
	alias := ""
	if value != "" {
		r := []rune(value)
		alias = string(r[:len(r)-1]) + "x"
	}
	m.iface[configName] = ptpInterface{
		name:  value,
		alias: alias,
	}
}

func (s *slaveInterface) set(configName string, value string) {
	s.Lock()
	defer s.Unlock()
	s.name[configName] = value
}

func (s *slaveInterface) isFaulty(configName string, iface string) bool {
	s.RLock()
	defer s.RUnlock()

	if si, found := s.name[configName]; found {
		if si == iface {
			return true
		}
	}
	return false
}

func (mp *masterOffsetSourceProcess) set(configName string, value string) {
	mp.Lock()
	defer mp.Unlock()
	mp.name[configName] = value
}

func (mp *masterOffsetSourceProcess) get(configName string) string {
	if s, found := mp.name[configName]; found {
		return s
	}
	return ptp4lProcessName // default is ptp4l
}

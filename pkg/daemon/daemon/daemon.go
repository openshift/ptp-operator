package daemon

import (
	"bufio"
	"cmp"
	"fmt"
	"k8s.io/utils/pointer"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/golang/glog"
	"k8s.io/client-go/kubernetes"

	ptpnetwork "github.com/openshift/linuxptp-daemon/pkg/network"
	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
)

const (
	PtpNamespace                    = "openshift-ptp"
	PTP4L_CONF_FILE_PATH            = "/etc/ptp4l.conf"
	PTP4L_CONF_DIR                  = "/ptp4l-conf"
	connectionRetryInterval         = 1 * time.Second
	ClockClassChangeIndicator       = "selected best master clock"
	GPSDDefaultGNSSSerialPort       = "/dev/gnss0"
	NMEASourceDisabledIndicator     = "nmea source timed out"
	NMEASourceDisabledIndicator2    = "source ts not valid"
	InvalidMasterTimestampIndicator = "ignoring invalid master time stamp"
	PTP_HA_IDENTIFIER               = "haProfiles"
	HAInDomainIndicator             = "as domain source clock"
	HAOutOfDomainIndicator          = "as out-of-domain source"
	MessageTagSuffixSeperator       = ":"
)

var (
	haInDomainRegEx       = regexp.MustCompile("selecting ([\\w\\-]+) as domain source clock")
	haOutDomainRegEx      = regexp.MustCompile("selecting ([\\w\\-]+) as out-of-domain source clock")
	messageTagSuffixRegEx = regexp.MustCompile(`([a-zA-Z0-9]+\.[a-zA-Z0-9]+\.config):[a-zA-Z0-9]+(:[a-zA-Z0-9]+)?`)
	clockIDRegEx          = regexp.MustCompile(`\/dev\/ptp\d+`)
)

// ProcessManager manages a set of ptpProcess
// which could be ptp4l, phc2sys or timemaster.
// Processes in ProcessManager will be started
// or stopped simultaneously.
type ProcessManager struct {
	process []*ptpProcess
}

// NewProcessManager is used by unit tests
func NewProcessManager() *ProcessManager {
	processPTP := &ptpProcess{}
	processPTP.ptpClockThreshold = &ptpv1.PtpClockThreshold{
		HoldOverTimeout:    5,
		MaxOffsetThreshold: 100,
		MinOffsetThreshold: -100,
	}
	return &ProcessManager{
		process: []*ptpProcess{processPTP},
	}
}

// SetTestProfile ...
func (p *ProcessManager) SetTestProfileProcess(name string, ifaces config.IFaces, socketPath,
	ptp4lConfigPath string, nodeProfile ptpv1.PtpProfile) {
	p.process = append(p.process, &ptpProcess{
		name:            name,
		ifaces:          ifaces,
		ptp4lSocketPath: socketPath,
		ptp4lConfigPath: ptp4lConfigPath,
		execMutex:       sync.Mutex{},
		nodeProfile:     nodeProfile,
	})
}

// SetTestData is used by unit tests
func (p *ProcessManager) SetTestData(name, msgTag string, ifaces config.IFaces) {
	if len(p.process) < 1 || p.process[0] == nil {
		glog.Error("process is not initialized in SetTestData()")
		return
	}
	p.process[0].name = name
	p.process[0].messageTag = msgTag
	p.process[0].ifaces = ifaces
}

// RunProcessPTPMetrics is used by unit tests
func (p *ProcessManager) RunProcessPTPMetrics(log string) {
	if len(p.process) < 1 || p.process[0] == nil {
		glog.Error("process is not initialized in RunProcessPTPMetrics()")
		return
	}
	p.process[0].processPTPMetrics(log)
}

type ptpProcess struct {
	name              string
	ifaces            config.IFaces
	ptp4lSocketPath string
	ptp4lConfigPath string
	configName      string
	messageTag      string
	exitCh          chan bool
	execMutex       sync.Mutex
	stopped         bool
	logFilterRegex  string
	cmd             *exec.Cmd
	depProcess      []process // these are list of dependent process which needs to be started/stopped if the parent process is starts/stops
	nodeProfile      ptpv1.PtpProfile
	parentClockClass float64
	pmcCheck          bool
	clockType         event.ClockType
	ptpClockThreshold *ptpv1.PtpClockThreshold
	haProfile         map[string][]string // stores list of interface name for each profile
}

func (p *ptpProcess) Stopped() bool {
	p.execMutex.Lock()
	me := p.stopped
	p.execMutex.Unlock()
	return me
}

func (p *ptpProcess) setStopped(val bool) {
	p.execMutex.Lock()
	p.stopped = val
	p.execMutex.Unlock()
}

// Daemon is the main structure for linuxptp instance.
// It contains all the necessary data to run linuxptp instance.
type Daemon struct {
	// node name where daemon is running
	nodeName  string
	namespace string

	// kubeClient allows interaction with Kubernetes, including the node we are running on.
	kubeClient *kubernetes.Clientset

	ptpUpdate *LinuxPTPConfUpdate

	processManager *ProcessManager

	hwconfigs *[]ptpv1.HwConfig

	refreshNodePtpDevice *bool

	// channel ensure LinuxPTP.Run() exit when main function exits.
	// stopCh is created by main function and passed by Daemon via NewLinuxPTP()
	stopCh <-chan struct{}
	pmcPollInterval int


	// Allow vendors to include plugins
	pluginManager PluginManager
}

// New LinuxPTP is called by daemon to generate new linuxptp instance
func New(
	nodeName string,
	namespace string,
	kubeClient *kubernetes.Clientset,
	ptpUpdate *LinuxPTPConfUpdate,
	stopCh <-chan struct{},
	plugins []string,
	hwconfigs *[]ptpv1.HwConfig,
	refreshNodePtpDevice *bool,
	pmcPollInterval int,
) *Daemon {
	RegisterMetrics(nodeName)
	InitializeOffsetMaps()
	pluginManager := registerPlugins(plugins)
	return &Daemon{
		nodeName:       nodeName,
		namespace:      namespace,
		kubeClient:     kubeClient,
		ptpUpdate:      ptpUpdate,
		processManager: &ProcessManager{},
		stopCh:         stopCh,
		pluginManager:  pluginManager,
		hwconfigs:      hwconfigs,
		refreshNodePtpDevice: refreshNodePtpDevice,
	}
}

// Run in a for loop to listen for any LinuxPTPConfUpdate changes
func (dn *Daemon) Run() {
	tickerPmc := time.NewTicker(time.Second * time.Duration(dn.pmcPollInterval))
	defer tickerPmc.Stop()
	for {
		select {
		case <-dn.ptpUpdate.UpdateCh:
			err := dn.applyNodePTPProfiles()
			if err != nil {
				glog.Errorf("linuxPTP apply node profile failed: %v", err)
			}
		case <-tickerPmc.C:
			dn.HandlePmcTicker()
		case <-dn.stopCh:
			for _, p := range dn.processManager.process {
				if p != nil {
					for _, d := range p.depProcess {
						if d != nil {
							d.CmdStop()
							d = nil
						}
					}
					p.cmdStop()
					p = nil
				}
			}
			glog.Infof("linuxPTP stop signal received, existing..")
			return
		}
	}
}

func printWhenNotNil(p interface{}, description string) {
	switch v := p.(type) {
	case *string:
		if v != nil {
			glog.Info(description, ": ", *v)
		}
	case *int64:
		if v != nil {
			glog.Info(description, ": ", *v)
		}
	default:
		glog.Info(description, ": ", v)
	}
}

// SetProcessManager ...
func (dn *Daemon) SetProcessManager(p *ProcessManager) {
	dn.processManager = p
}

func (dn *Daemon) applyNodePTPProfiles() error {
	glog.Infof("in applyNodePTPProfiles")
	for _, p := range dn.processManager.process {
		if p != nil {
			glog.Infof("stopping process.... %s", p.name)
			p.cmdStop()
			if p.depProcess != nil {
				for _, d := range p.depProcess {
					if d != nil {
						d.CmdStop()
						d = nil
					}
				}
			}
			p.depProcess = nil
			//cleanup metrics
			deleteMetrics(p.ifaces, p.haProfile, p.name, p.configName)
			p = nil
		}
	}

	// All process should have been stopped,
	// clear process in process manager.
	// Assigning processManager.process to nil releases
	// the underlying slice to the garbage
	// collector (assuming there are no other
	// references).
	dn.processManager.process = nil

	// TODO:
	// compare nodeProfile with previous config,
	// only apply when nodeProfile changes

	//clear hwconfig before updating
	*dn.hwconfigs = []ptpv1.HwConfig{}

	glog.Infof("updating NodePTPProfiles to:")
	runID := 0
	slices.SortFunc(dn.ptpUpdate.NodeProfiles, func(a, b ptpv1.PtpProfile) int {
		aHasPhc2sysOpts := a.Phc2sysOpts != nil && strings.TrimSpace(*a.Phc2sysOpts) != ""
		bHasPhc2sysOpts := b.Phc2sysOpts != nil && strings.TrimSpace(*b.Phc2sysOpts) != ""
		//sorted in ascending order
		// here having phc2sysOptions is considered a high number
		if !aHasPhc2sysOpts && bHasPhc2sysOpts {
			return -1 //  a<b return -1
		} else if aHasPhc2sysOpts && !bHasPhc2sysOpts {
			return 1 //  a>b return
		}
		return cmp.Compare(*a.Name, *b.Name)
	})
	for _, profile := range dn.ptpUpdate.NodeProfiles {
		err := dn.applyNodePtpProfile(runID, &profile)
		if err != nil {
			return err
		}
		runID++
	}

	// Start all the process
	for _, p := range dn.processManager.process {
		if p != nil {
			p.eventCh = dn.processManager.eventChannel
			// start ptp4l process early , it doesn't have
			if p.depProcess == nil {
				go p.cmdRun(dn.stdoutToSocket)
			} else {
				for _, d := range p.depProcess {
					if d != nil {
						time.Sleep(3 * time.Second)
						go d.CmdRun(false)
						time.Sleep(3 * time.Second)
						dn.pluginManager.AfterRunPTPCommand(&p.nodeProfile, d.Name())
						d.MonitorProcess(config.ProcessConfig{
							ClockType:    p.clockType,
							ConfigName:   p.configName,
							EventChannel: dn.processManager.eventChannel,
							GMThreshold: config.Threshold{
								Max:             p.ptpClockThreshold.MaxOffsetThreshold,
								Min:             p.ptpClockThreshold.MinOffsetThreshold,
								HoldOverTimeout: p.ptpClockThreshold.HoldOverTimeout,
							},
							InitialPTPState: event.PTP_FREERUN,
						})
						glog.Infof("enabling dep process %s with Max %d Min %d Holdover %d", d.Name(), p.ptpClockThreshold.MaxOffsetThreshold, p.ptpClockThreshold.MinOffsetThreshold, p.ptpClockThreshold.HoldOverTimeout)
					}
				}
				go p.cmdRun()
			}
			dn.pluginManager.AfterRunPTPCommand(&p.nodeProfile, p.name)
		}
	}
	dn.pluginManager.PopulateHwConfig(dn.hwconfigs)
	*dn.refreshNodePtpDevice = true
	return nil
}

func getLogFilterRegex(nodeProfile *ptpv1.PtpProfile) string {
	logFilterRegex := "^$"
	if filter, ok := (*nodeProfile).PtpSettings["stdoutFilter"]; ok {
		logFilterRegex = filter
	}
	if logReduce, ok := (*nodeProfile).PtpSettings["logReduce"]; ok {
		if strings.ToLower(logReduce) == "true" {
			logFilterRegex = fmt.Sprintf("%s|^.*master offset.*$", logFilterRegex)
		}
	}
	if logFilterRegex != "^$" {
		glog.Infof("%s logFilterRegex='%s'\n", *nodeProfile.Name, logFilterRegex)
	}
	return logFilterRegex
}

func printNodeProfile(nodeProfile *ptpv1.PtpProfile) {
	glog.Infof("------------------------------------")
	printWhenNotNil(nodeProfile.Name, "Profile Name")
	printWhenNotNil(nodeProfile.Interface, "Interface")
	printWhenNotNil(nodeProfile.Ptp4lOpts, "Ptp4lOpts")
	printWhenNotNil(nodeProfile.Ptp4lConf, "Ptp4lConf")
	printWhenNotNil(nodeProfile.Phc2sysOpts, "Phc2sysOpts")
	printWhenNotNil(nodeProfile.Phc2sysConf, "Phc2sysConf")
	printWhenNotNil(nodeProfile.Ts2PhcOpts, "Ts2PhcOpts")
	printWhenNotNil(nodeProfile.Ts2PhcConf, "Ts2PhcConf")
	printWhenNotNil(nodeProfile.PtpSchedulingPolicy, "PtpSchedulingPolicy")
	printWhenNotNil(nodeProfile.PtpSchedulingPriority, "PtpSchedulingPriority")
	printWhenNotNil(nodeProfile.PtpSettings, "PtpSettings")
	glog.Infof("------------------------------------")
}

/*
update: March 7th 2024
To support PTP HA phc2sys profile is appended to the end
since phc2sysOpts needs to collect profile information from applied
ptpconfig profiles for ptp4l
*/
func (dn *Daemon) applyNodePtpProfile(runID int, nodeProfile *ptpv1.PtpProfile) error {

	dn.pluginManager.OnPTPConfigChange(nodeProfile)

	ptpProcesses := []string{
		ts2phcProcessName,  // there can be only one ts2phc process in the system
		ptp4lProcessName,   // there could be more than one ptp4l in the system
		phc2sysProcessName, // there can be only one phc2sys process in the system
	}

	var err error
	var cmdLine string
	var configPath string
	var socketPath string
	var configFile string
	var configInput *string
	var configOpts *string
	var messageTag string
	var cmd *exec.Cmd
	var pProcess string
	var haProfile map[string][]string

	ptpHAEnabled := len(listHaProfiles(nodeProfile)) > 0

	for _, p := range ptpProcesses {
		pProcess = p
		switch pProcess {
		case ptp4lProcessName:
			configInput = nodeProfile.Ptp4lConf
			configOpts = nodeProfile.Ptp4lOpts
			if configOpts == nil {
				_configOpts := " "
				configOpts = &_configOpts
			}
			socketPath = fmt.Sprintf("/var/run/ptp4l.%d.socket", runID)
			configFile = fmt.Sprintf("ptp4l.%d.config", runID)
			configPath = fmt.Sprintf("/var/run/%s", configFile)
			messageTag = fmt.Sprintf("[ptp4l.%d.config:{level}]", runID)
		case phc2sysProcessName:
			configInput = nodeProfile.Phc2sysConf
			configOpts = nodeProfile.Phc2sysOpts
			if !ptpHAEnabled {
				socketPath = fmt.Sprintf("/var/run/ptp4l.%d.socket", runID)
				messageTag = fmt.Sprintf("[ptp4l.%d.config:{level}]", runID)
			} else { // when ptp ha enabled it has its own valid config
				messageTag = fmt.Sprintf("[phc2sys.%d.config:{level}]", runID)
			}
			configFile = fmt.Sprintf("phc2sys.%d.config", runID)
			configPath = fmt.Sprintf("/var/run/%s", configFile)
		case ts2phcProcessName:
			clockType = event.GM
			configInput = nodeProfile.Ts2PhcConf
			configOpts = nodeProfile.Ts2PhcOpts
			socketPath = fmt.Sprintf("/var/run/ptp4l.%d.socket", runID)
			configFile = fmt.Sprintf("ts2phc.%d.config", runID)
			configPath = fmt.Sprintf("/var/run/%s", configFile)
			messageTag = fmt.Sprintf("[ts2phc.%d.config:{level}]", runID)
		}

		if configOpts == nil || strings.TrimSpace(*configOpts) == "" {
			glog.Infof("configOpts empty, skipping: %s", pProcess)
			continue
		}

		output := &ptp4lConf{}
		err = output.populatePtp4lConf(configInput)
		if err != nil {
			printNodeProfile(nodeProfile)
			return err
		}

		clockType := output.clock_type
		output.profile_name = *nodeProfile.Name

		if nodeProfile.Interface != nil && *nodeProfile.Interface != "" {
			output.sections = append([]ptp4lConfSection{{
				options:     map[string]string{},
				sectionName: fmt.Sprintf("[%s]", *nodeProfile.Interface)}}, output.sections...)
		} else {
			iface := string("")
			nodeProfile.Interface = &iface
		}

		for index, section := range output.sections {
			if section.sectionName == "[global]" {
				section.options["message_tag"] = messageTag
				if socketPath != "" {
					section.options["uds_address"] = socketPath
				}
				if gnssSerialPort, ok := section.options["ts2phc.nmea_serialport"]; ok {
					output.gnss_serial_port = strings.TrimSpace(gnssSerialPort)
					section.options["ts2phc.nmea_serialport"] = GPSPIPE_SERIALPORT
				}
				output.sections[index] = section
			}
		}

		// This adds the flags needed for monitor
		addFlagsForMonitor(p, configOpts, output, false)
		configOutput, ifaces := output.renderPtp4lConf()
		for i := range ifaces {
			ifaces[i].PhcId = ptpnetwork.GetPhcId(ifaces[i].Name)
		}

		if configInput != nil {
			*configInput = configOutput
		}

		cmdLine = fmt.Sprintf("/usr/sbin/%s -f %s  %s ", pProcess, configPath, *configOpts)
		cmdLine = addScheduling(nodeProfile, cmdLine)
		if pProcess == phc2sysProcessName {
			haProfile, cmdLine = dn.ApplyHaProfiles(nodeProfile, cmdLine)
		}
		args := strings.Split(cmdLine, " ")
		cmd = exec.Command(args[0], args[1:]...)
		dprocess := ptpProcess{
			name:              p,
			ifaces:            ifaces,
			ptp4lConfigPath:   configPath,
			ptp4lSocketPath:   socketPath,
			configName:        configFile,
			messageTag:        messageTag,
			exitCh:            make(chan bool),
			stopped:           false,
			logFilterRegex:    getLogFilterRegex(nodeProfile),
			cmd:               cmd,
			depProcess:        []process{},
			nodeProfile:       *nodeProfile,
			clockType:         clockType,
			ptpClockThreshold: getPTPThreshold(nodeProfile),
			haProfile:         haProfile,
		}
		// TODO HARDWARE PLUGIN for e810
		if pProcess == ts2phcProcessName { //& if the x plugin is enabled
			if output.gnss_serial_port == "" {
				output.gnss_serial_port = GPSPIPE_SERIALPORT
			}
			// TODO: move this to plugin or call it from hwplugin or leave it here and remove Hardcoded
			gmInterface := dprocess.ifaces.GetGMInterface().Name

			if e := mkFifo(); e != nil {
				glog.Errorf("Error creating named pipe, GNSS monitoring will not work as expected %s", e.Error())
			}
			gpsDaemon := &GPSD{
				name:        GPSD_PROCESSNAME,
				execMutex:   sync.Mutex{},
				cmd:         nil,
				serialPort:  output.gnss_serial_port,
				exitCh:      make(chan struct{}),
				gmInterface: gmInterface,
				stopped:     false,
				messageTag:  messageTag,
			}
			gpsDaemon.CmdInit()
			gpsDaemon.cmdLine = addScheduling(nodeProfile, gpsDaemon.cmdLine)
			args = strings.Split(gpsDaemon.cmdLine, " ")
			gpsDaemon.cmd = exec.Command(args[0], args[1:]...)
			dprocess.depProcess = append(dprocess.depProcess, gpsDaemon)

			// init gpspipe
			gpsPipeDaemon := &gpspipe{
				name:       GPSPIPE_PROCESSNAME,
				execMutex:  sync.Mutex{},
				cmd:        nil,
				serialPort: GPSPIPE_SERIALPORT,
				exitCh:     make(chan struct{}),
				stopped:    false,
				messageTag: messageTag,
			}
			gpsPipeDaemon.CmdInit()
			gpsPipeDaemon.cmdLine = addScheduling(nodeProfile, gpsPipeDaemon.cmdLine)
			args = strings.Split(gpsPipeDaemon.cmdLine, " ")
			gpsPipeDaemon.cmd = exec.Command(args[0], args[1:]...)
			dprocess.depProcess = append(dprocess.depProcess, gpsPipeDaemon)

			// init dpll
			// TODO: Try to inject DPLL depProcess via plugin ?
			var localMaxHoldoverOffSet uint64 = dpll.LocalMaxHoldoverOffSet
			var localHoldoverTimeout uint64 = dpll.LocalHoldoverTimeout
			var maxInSpecOffset uint64 = dpll.MaxInSpecOffset
			var clockId uint64
			phaseOffsetPinFilter := map[string]string{}
			for _, iface := range dprocess.ifaces {
				var eventSource []event.EventSource
				if iface.Source == event.GNSS || iface.Source == event.PPS {
					glog.Info("Init dpll: ptp settings ", (*nodeProfile).PtpSettings)
					for k, v := range (*nodeProfile).PtpSettings {
						glog.Info("Init dpll: ptp kv ", k, " ", v)
						if strings.Contains(k, strings.Join([]string{iface.Name, "phaseOffset"}, ".")) {
							filterKey := strings.Split(k, ".")
							property := filterKey[len(filterKey)-1]
							phaseOffsetPinFilter[property] = v
							glog.Infof("dpll phase offset filter property: %s[%s]=%s", iface.Name, property, v)
							continue
						}
						i, err := strconv.ParseUint(v, 10, 64)
						if err != nil {
							continue
						}
						if k == dpll.LocalMaxHoldoverOffSetStr {
							localMaxHoldoverOffSet = i
						}
						if k == dpll.LocalHoldoverTimeoutStr {
							localHoldoverTimeout = i
						}
						if k == dpll.MaxInSpecOffsetStr {
							maxInSpecOffset = i
						}
						if k == fmt.Sprintf("%s[%s]", dpll.ClockIdStr, iface.Name) {
							clockId = i
						}
					}
					if iface.Source == event.PPS {
						eventSource = []event.EventSource{event.PPS}
					} else {
						eventSource = []event.EventSource{event.GNSS}
					}
					// pass array of ifaces which has source + clockId -
					// here we have multiple dpll objects identified by clock id
					// depends on will be either PPS or  GNSS,
					// ONLY the one with GNSS dependcy will go to HOLDOVER
					dpllDaemon := dpll.NewDpll(clockId, localMaxHoldoverOffSet, localHoldoverTimeout,
						maxInSpecOffset, iface.Name, eventSource, dpll.NONE, dn.GetPhaseOffsetPinFilter(nodeProfile))
					glog.Infof("depending on %s", dpllDaemon.DependsOn())
					dpllDaemon.CmdInit()
					dprocess.depProcess = append(dprocess.depProcess, dpllDaemon)
				}

			}
		}
		err = os.WriteFile(configPath, []byte(configOutput), 0644)
		if err != nil {
			printNodeProfile(nodeProfile)
			return fmt.Errorf("failed to write the configuration file named %s: %v", configPath, err)
		}

		printNodeProfile(nodeProfile)
		dn.processManager.process = append(dn.processManager.process, &dprocess)

	}
	return nil
}

func (dn *Daemon) GetPhaseOffsetPinFilter(nodeProfile *ptpv1.PtpProfile) map[string]map[string]string {
	phaseOffsetPinFilter := map[string]map[string]string{}
	for k, v := range (*nodeProfile).PtpSettings {
		if strings.Contains(k, "phaseOffsetFilter") {
			filterKey := strings.Split(k, ".")
			property := filterKey[len(filterKey)-1]
			clockIdStr := filterKey[len(filterKey)-2]
			if len(phaseOffsetPinFilter[clockIdStr]) == 0 {
				phaseOffsetPinFilter[clockIdStr] = map[string]string{}
			}
			phaseOffsetPinFilter[clockIdStr][property] = v
			continue
		}
	}
	return phaseOffsetPinFilter
}

// HandlePmcTicker  ....
func (dn *Daemon) HandlePmcTicker() {
	for _, p := range dn.processManager.process {
		if p.name == ptp4lProcessName {
			p.pmcCheck = true
		}
	}

// Add fifo scheduling if specified in nodeProfile
func addScheduling(nodeProfile *ptpv1.PtpProfile, cmdLine string) string {
	if nodeProfile.PtpSchedulingPolicy != nil && *nodeProfile.PtpSchedulingPolicy == "SCHED_FIFO" {
		if nodeProfile.PtpSchedulingPriority == nil {
			glog.Errorf("Priority must be set for SCHED_FIFO; using default scheduling.")
			return cmdLine
		}
		priority := *nodeProfile.PtpSchedulingPriority
		if priority < 1 || priority > 65 {
			glog.Errorf("Invalid priority %d; using default scheduling.", priority)
			return cmdLine
		}
		cmdLine = fmt.Sprintf("/bin/chrt -f %d %s", priority, cmdLine)
		glog.Infof(cmdLine)
		return cmdLine
	}
	return cmdLine
}

func processStatus(processName, messageTag string, status int64) {
	cfgName := strings.Replace(strings.Replace(messageTag, "]", "", 1), "[", "", 1)
	if cfgName != "" {
		cfgName = strings.Split(cfgName, MessageTagSuffixSeperator)[0]
	}
	// ptp4l[5196819.100]: [ptp4l.0.config] PTP_PROCESS_STOPPED:0/1
	deadProcessMsg := fmt.Sprintf("%s[%d]:[%s] PTP_PROCESS_STATUS:%d\n", processName, time.Now().Unix(), cfgName, status)
	UpdateProcessStatusMetrics(processName, cfgName, status)
	glog.Infof("%s\n", deadProcessMsg)
}

func (p *ptpProcess) updateClockClass(c *net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			glog.Errorf("Recovered in f %#v", r)
		}
	}()
	if _, matches, e := pmc.RunPMCExp(p.configName, pmc.CmdGetParentDataSet, pmc.ClockClassChangeRegEx); e == nil {
		//regex: 'gm.ClockClass[[:space:]]+(\d+)'
		//match  1: 'gm.ClockClass                         135'
		//match  2: '135'
		if len(matches) > 1 {
			var parseError error
			var clockClass float64
			if clockClass, parseError = strconv.ParseFloat(matches[1], 64); parseError == nil {
				if clockClass != p.parentClockClass {
					p.parentClockClass = clockClass
					glog.Infof("clock change event identified")
					//ptp4l[5196819.100]: [ptp4l.0.config] CLOCK_CLASS_CHANGE:248
					clockClassOut := fmt.Sprintf("%s[%d]:[%s] CLOCK_CLASS_CHANGE %f\n", p.name, time.Now().Unix(), p.configName, clockClass)
					fmt.Printf("%s", clockClassOut)
					if c == nil {
						UpdateClockClassMetrics(clockClass) // no socket then update metrics
					} else {
						_, err := (*c).Write([]byte(clockClassOut))
						if err != nil {
							glog.Errorf("failed to write class change event %s", err.Error())
						}
					}
				}
			} else {
				glog.Errorf("parse error in clock class value %s", parseError)
			}
		} else {
			glog.Infof("clock class change value not found via PMC")
		}
	} else {
		glog.Error("error parsing PMC util for clock class change event")
	}
}

// cmdRun runs given ptpProcess and restarts on errors
func (p *ptpProcess) cmdRun() {
	done := make(chan struct{}) // Done setting up logging.  Go ahead and wait for process
	defer func() {
		p.exitCh <- true
	}()

	logFilterRegex, regexErr := regexp.Compile(p.logFilterRegex)
	if regexErr != nil {
		glog.Infof("Failed parsing regex %s for %s: %d.  Defaulting to accept all", p.logFilterRegex, p.configName, regexErr)
	}

	for {
		glog.Infof("Starting %s...", p.name)
		glog.Infof("%s cmd: %+v", p.name, p.cmd)

		//
		// don't discard process stderr output
		//
		p.cmd.Stderr = os.Stderr
		cmdReader, err := p.cmd.StdoutPipe()
		if err != nil {
			glog.Errorf("CmdRun() error creating StdoutPipe for %s: %v", p.name, err)
			break
		}
		scanner := bufio.NewScanner(cmdReader)
		processStatus(p.name, p.messageTag, PtpProcessUp)
		go func() {
			for scanner.Scan() {
				output := scanner.Text()
				if p.pmcCheck {
					p.pmcCheck = false
					go p.updateClockClass(&c)
				}

				if regexErr != nil || !logFilterRegex.MatchString(output) {
					fmt.Printf("%s\n", output)
				}
				p.processPTPMetrics(output)
				if p.name == ptp4lProcessName {
					if strings.Contains(output, ClockClassChangeIndicator) {
						go p.updateClockClass(nil)
					}
				} else if p.name == phc2sysProcessName && len(p.haProfile) > 0 {
					p.announceHAFailOver(nil, output) // do not use go routine since order of execution is important here
				}
			}
			done <- struct{}{}
		}()
		// Don't restart after termination
		if !p.Stopped() {
			err = p.cmd.Start() // this is asynchronous call,
			if err != nil {
				glog.Errorf("CmdRun() error starting %s: %v", p.name, err)
			}
		}
		<-done // goroutine is done
		err = p.cmd.Wait()
		if err != nil {
			glog.Errorf("CmdRun() error waiting for %s: %v", p.name, err)
		}
		processStatus(p.name, p.messageTag, PtpProcessDown)

		time.Sleep(connectionRetryInterval) // Delay to prevent flooding restarts if startup fails
		// Don't restart after termination
		if p.Stopped() {
			glog.Infof("Not recreating %s...", p.name)
			break
		} else {
			glog.Infof("Recreating %s...", p.name)
			newCmd := exec.Command(p.cmd.Args[0], p.cmd.Args[1:]...)
			p.cmd = newCmd
		}
	}
}

// for ts2phc along with processing metrics need to identify event
func (p *ptpProcess) processPTPMetrics(output string) {
	if p.name == ts2phcProcessName && (strings.Contains(output, NMEASourceDisabledIndicator) ||
		strings.Contains(output, InvalidMasterTimestampIndicator) ||
		strings.Contains(output, NMEASourceDisabledIndicator2)) { //TODO identify which interface lost nmea or 1pps
		iface := p.ifaces.GetGMInterface().Name
		p.ProcessTs2PhcEvents(faultyOffset, ts2phcProcessName, iface, map[event.ValueType]interface{}{event.NMEA_STATUS: int64(0)})
		glog.Error("nmea string lost") //TODO: add for 1pps lost
	} else {
		configName, source, ptpOffset, _, iface := extractMetrics(p.messageTag, p.name, p.ifaces, output)
		if iface != "" { // for ptp4l/phc2sys this function only update metrics
			var values map[event.ValueType]interface{}
			ifaceName := masterOffsetIface.getByAlias(configName, iface).name
			if iface != clockRealTime && p.name == ts2phcProcessName {
				eventSource := p.ifaces.GetEventSource(ifaceName)
				if eventSource == event.GNSS {
					values = map[event.ValueType]interface{}{event.NMEA_STATUS: int64(1)}
				}
			}
			p.ProcessTs2PhcEvents(ptpOffset, source, ifaceName, values)
		}
	}
}

// cmdStop stops ptpProcess launched by cmdRun
func (p *ptpProcess) cmdStop() {
	glog.Infof("stopping %s...", p.name)
	if p.cmd == nil {
		return
	}
	p.setStopped(true)
	if p.cmd.Process != nil {
		glog.Infof("Sending TERM to (%s) PID: %d", p.name, p.cmd.Process.Pid)
		err := p.cmd.Process.Signal(syscall.SIGTERM)
		if err != nil {
			// If the process is already terminated, we will get an error here
			glog.Errorf("failed to send SIGTERM to %s (%d): %v", p.name, p.cmd.Process.Pid, err)
			return
		}
	}
	glog.Infof("removing config path %s for %s ", p.ptp4lConfigPath, p.name)
	if p.ptp4lConfigPath != "" {
		err := os.Remove(p.ptp4lConfigPath)
		if err != nil {
			glog.Errorf("failed to remove ptp4l config path %s: %v", p.ptp4lConfigPath, err)
		}
	}
	<-p.exitCh
	glog.Infof("Process %s (%d) terminated", p.name, p.cmd.Process.Pid)
}

func getPTPThreshold(nodeProfile *ptpv1.PtpProfile) *ptpv1.PtpClockThreshold {
	if nodeProfile.PtpClockThreshold != nil {
		return &ptpv1.PtpClockThreshold{
			HoldOverTimeout:    nodeProfile.PtpClockThreshold.HoldOverTimeout,
			MaxOffsetThreshold: nodeProfile.PtpClockThreshold.MaxOffsetThreshold,
			MinOffsetThreshold: nodeProfile.PtpClockThreshold.MinOffsetThreshold,
		}
	} else {
		return &ptpv1.PtpClockThreshold{
			HoldOverTimeout:    5,
			MaxOffsetThreshold: 100,
			MinOffsetThreshold: -100,
		}
	}
}

func (p *ptpProcess) MonitorEvent(offset float64, clockState string) {
	// not implemented
}

func (p *ptpProcess) ProcessTs2PhcEvents(ptpOffset float64, source string, iface string, extraValue map[event.ValueType]interface{}) {
	var ptpState event.PTPState
	ptpState = event.PTP_FREERUN
	ptpOffsetInt64 := int64(ptpOffset)
	if ptpOffsetInt64 <= p.ptpClockThreshold.MaxOffsetThreshold &&
		ptpOffsetInt64 >= p.ptpClockThreshold.MinOffsetThreshold {
		ptpState = event.PTP_LOCKED
	}
	if source == ts2phcProcessName { // for ts2phc send it to event to create metrics and events
		var values = make(map[event.ValueType]interface{})

		values[event.OFFSET] = ptpOffsetInt64
		for k, v := range extraValue {
			values[k] = v
		}
		select {
		case p.eventCh <- event.EventChannel{
			ProcessName: event.TS2PHC,
			State:       ptpState,
			CfgName:     p.configName,
			IFace:       iface,
			Values:      values,
			ClockType:   p.clockType,
			Time:        time.Now().UnixMilli(),
			WriteToLog: func() bool { // only write to log if there is something extra
				if len(extraValue) > 0 {
					return true
				}
				return false
			}(),
			Reset: false,
		}:
		default:
		}
	} else {
		if iface != "" && iface != clockRealTime {
			r := []rune(iface)
			iface = string(r[:len(r)-1]) + "x"
		}
		if ptpState == event.PTP_LOCKED {
			updateClockStateMetrics(p.name, iface, LOCKED)
		} else {
			updateClockStateMetrics(p.name, iface, FREERUN)
		}
	}
}

func (dn *Daemon) ApplyHaProfiles(nodeProfile *ptpv1.PtpProfile, cmdLine string) (map[string][]string, string) {
	lsProfiles := listHaProfiles(nodeProfile)
	haProfiles := make(map[string][]string, len(lsProfiles))
	updateHaProfileToSocketPath := make([]string, 0, len(lsProfiles))
	for _, profileName := range lsProfiles {
		for _, dmProcess := range dn.processManager.process {
			if dmProcess.nodeProfile.Name != nil && *dmProcess.nodeProfile.Name == profileName {
				updateHaProfileToSocketPath = append(updateHaProfileToSocketPath, "-z "+dmProcess.ptp4lSocketPath)
				var ifaces []string
				for _, iface := range dmProcess.ifaces {
					ifaces = append(ifaces, iface.Name)
				}
				haProfiles[profileName] = ifaces
				break // Exit inner loop if profile found
			}
		}
	}
	if len(updateHaProfileToSocketPath) > 0 {
		cmdLine = fmt.Sprintf("%s%s", cmdLine, strings.Join(updateHaProfileToSocketPath, " "))
	}
	glog.Infof(cmdLine)
	return haProfiles, cmdLine
}

func listHaProfiles(nodeProfile *ptpv1.PtpProfile) (haProfiles []string) {
	if profiles, ok := nodeProfile.PtpSettings[PTP_HA_IDENTIFIER]; ok {
		haProfiles = strings.Split(profiles, ",")
		for index, profile := range haProfiles {
			haProfiles[index] = strings.TrimSpace(profile)
		}
	}
	return
}

func (p *ptpProcess) announceHAFailOver(c *net.Conn, output string) {
	defer func() {
		if r := recover(); r != nil {
			glog.Errorf("Recovered in f %#v", r)
		}
	}()
	var activeIFace string
	var match []string
	// selecting ens2f2 as out-of-domain source clock - 0
	// selecting ens2f0 as domain source clock - 1
	domainState, activeState := failOverIndicator(output, len(p.haProfile))

	if domainState == 1 {
		match = haInDomainRegEx.FindStringSubmatch(output)
	} else if domainState == 0 && activeState == 1 {
		match = haOutDomainRegEx.FindStringSubmatch(output)
	} else {
		return
	}

	if match != nil {
		activeIFace = match[1]
	} else {
		glog.Errorf("couldn't retrieve interface name from fail over logs %s\n", output)
		return
	}
	// find profile name and construct the log-out and metrics
	var currentProfile string
	var inActiveProfiles []string
	for profile, ifaces := range p.haProfile {
		for _, iface := range ifaces {
			if iface == activeIFace {
				currentProfile = profile
				break
			}
		}
		// mark all other profiles as inactive
		if currentProfile != profile && activeState == 1 {
			inActiveProfiles = append(inActiveProfiles, profile)
		}
	}
	// log both active and inactive profiles
	logString := []string{fmt.Sprintf("%s[%d]:[%s] ptp_ha_profile %s state %d\n", p.name, time.Now().Unix(), p.configName, currentProfile, activeState)}
	for _, inActive := range inActiveProfiles {
		logString = append(logString, fmt.Sprintf("%s[%d]:[%s] ptp_ha_profile %s state %d\n", p.name, time.Now().Unix(), p.configName, inActive, 0))
	}
	if c == nil {
		for _, logProfile := range logString {
			fmt.Printf("%s", logProfile)
		}
		UpdatePTPHAMetrics(currentProfile, inActiveProfiles, activeState)
	} else {
		for _, logProfile := range logString {
			_, err := (*c).Write([]byte(logProfile))
			if err != nil {
				glog.Errorf("failed to write class change event %s", err.Error())
			}
		}
	}
}

// 1= In domain 0 out of domain
// All the profiles are in domain for their own domain.
// If there are multiple domains/profiles, then both are active in their own domain, and one of them is also active out of domain
// returns domain state and activeState 3 and 1 = Active,2 is inActive
func failOverIndicator(output string, count int) (int64, int64) {
	if strings.Contains(output, HAInDomainIndicator) { // when single profile then it's always 1
		if count == 1 {
			return 1, 1 // 1= in ; 1= active profile =3
		} else {
			return 1, 0 // 1= in ,1= inactive ==2
		}
	} else if strings.Contains(output, HAOutOfDomainIndicator) {
		return 0, 1 //0=out; 1=active == 1
	}
	return 0, 0
}

func removeMessageSuffix(input string) (output string) {
	// container log output  "ptp4l[2464681.628]: [phc2sys.1.config:7] master offset -4 s2 freq -26835 path delay 525"
	// make sure non-supported version can handle suffix tags
	// clear {} from unparsed template
	//"ptp4l[2464681.628]: [phc2sys.1.config:{level}] master offset -4 s2 freq -26835 path delay 525"
	replacer := strings.NewReplacer("{", "", "}", "")
	output = replacer.Replace(input)
	// Replace matching parts in the input string
	output = messageTagSuffixRegEx.ReplaceAllString(output, "$1")
	return output
}

// linuxptp 4.2 uses clock id ; this function will replace the clockid to interface name
func (p *ptpProcess) replaceClockID(input string) (output string) {
	if p.name != ts2phcProcessName {
		return input
	}
	// replace only for value with offset
	if indx := strings.Index(input, offset); indx < 0 {
		return input
	}
	// Replace all occurrences of the pattern with the replacement string
	// ts2phc[1896327.319]: [ts2phc.0.config] dev/ptp4  offset    -1 s2 freq      -2
	// Find the first match
	match := clockIDRegEx.FindStringSubmatch(input)
	if match == nil {
		return input
	}
	// Extract the captured interface string (group 1)
	iface := p.ifaces.GetPhcID2IFace(match[0])
	output = clockIDRegEx.ReplaceAllString(input, iface)
	return output
}

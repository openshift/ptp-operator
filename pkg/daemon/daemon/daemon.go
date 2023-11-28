package daemon

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
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
	InvalidMasterTimestampIndicator = "ignoring invalid master time stamp"
)

// ProcessManager manages a set of ptpProcess
// which could be ptp4l, phc2sys or timemaster.
// Processes in ProcessManager will be started
// or stopped simultaneously.
type ProcessManager struct {
	process []*ptpProcess
}

type ptpProcess struct {
	name            string
	ifaces          config.IFaces
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
	nodeProfile     *ptpv1.PtpProfile
	parentClockClass float64
	pmcCheck          bool
	clockType         event.ClockType
	ptpClockThreshold *ptpv1.PtpClockThreshold
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
			// start ptp4l process early , it doesnt have
			if p.depProcess == nil {
				go p.cmdRun(dn.stdoutToSocket)
			} else {
				for _, d := range p.depProcess {
					if d != nil {
						time.Sleep(3 * time.Second)
						go d.CmdRun(false)
						time.Sleep(3 * time.Second)
						dn.pluginManager.AfterRunPTPCommand(p.nodeProfile, d.Name())
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
						glog.Infof("Max %d Min %d Holdover %d", p.ptpClockThreshold.MaxOffsetThreshold, p.ptpClockThreshold.MinOffsetThreshold, p.ptpClockThreshold.HoldOverTimeout)
					}
				}
				go p.cmdRun()
			}
			dn.pluginManager.AfterRunPTPCommand(p.nodeProfile, p.name)
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

func (dn *Daemon) applyNodePtpProfile(runID int, nodeProfile *ptpv1.PtpProfile) error {

	dn.pluginManager.OnPTPConfigChange(nodeProfile)

	ptp_processes := []string{
		ts2phcProcessName,
		ptp4lProcessName,
		phcProcessName,
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

	for _, p := range ptp_processes {
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
			messageTag = fmt.Sprintf("[ptp4l.%d.config]", runID)
		case phcProcessName:
			configInput = nodeProfile.Phc2sysConf
			configOpts = nodeProfile.Phc2sysOpts
			socketPath = fmt.Sprintf("/var/run/ptp4l.%d.socket", runID)
			configFile = fmt.Sprintf("phc2sys.%d.config", runID)
			configPath = fmt.Sprintf("/var/run/%s", configFile)
			messageTag = fmt.Sprintf("[ptp4l.%d.config]", runID)
		case ts2phcProcessName:
			clockType = event.GM
			configInput = nodeProfile.Ts2PhcConf
			configOpts = nodeProfile.Ts2PhcOpts
			socketPath = fmt.Sprintf("/var/run/ptp4l.%d.socket", runID)
			configFile = fmt.Sprintf("ts2phc.%d.config", runID)
			configPath = fmt.Sprintf("/var/run/%s", configFile)
			messageTag = fmt.Sprintf("[ts2phc.%d.config]", runID)
		}

		if configOpts == nil || *configOpts == "" {
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
			output.sections = append([]ptp4lConfSection{{options: map[string]string{}, sectionName: fmt.Sprintf("[%s]", *nodeProfile.Interface)}}, output.sections...)
		} else {
			iface := string("")
			nodeProfile.Interface = &iface
		}

		for index, section := range output.sections {
			if section.sectionName == "[global]" {
				section.options["message_tag"] = messageTag
				section.options["uds_address"] = socketPath
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

		cmdLine = fmt.Sprintf("/usr/sbin/%s -f %s  %s ", p, configPath, *configOpts)
		cmdLine = addScheduling(nodeProfile, cmdLine)
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
			nodeProfile:       nodeProfile,
			clockType:         clockType,
			ptpClockThreshold: getPTPThreshold(nodeProfile),
		}
		//TODO HARDWARE PLUGIN for e810
		if pProcess == ts2phcProcessName { //& if the x plugin is enabled
			if output.gnss_serial_port == "" {
				output.gnss_serial_port = GPSPIPE_SERIALPORT
			}
			//TODO: move this to plugin or call it from hwplugin or leave it here and remove Hardcoded
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
			for _, iface := range dprocess.ifaces {
				var eventSource []event.EventSource
				if iface.Source == event.GNSS || iface.Source == event.PPS {
					for k, v := range (*nodeProfile).PtpSettings {
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
					dpllDaemon := dpll.NewDpll(clockId, localMaxHoldoverOffSet, localHoldoverTimeout,
						maxInSpecOffset, iface.Name, eventSource, dpll.NONE)
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
		strings.Contains(output, InvalidMasterTimestampIndicator)) { //TODO identify which interface lost nmea or 1pps
		iface := p.ifaces.GetGMInterface().Name
		if iface != "" {
			r := []rune(iface)
			iface = string(r[:len(r)-1]) + "x"
		}
		p.ProcessTs2PhcEvents(faultyOffset, ts2phcProcessName, iface, map[event.ValueType]interface{}{event.NMEA_STATUS: int64(0)})
		glog.Error("nmea string lost") //TODO: add for 1pps lost
	} else {
		configName, source, ptpOffset, _, iface := extractMetrics(p.messageTag, p.name, p.ifaces, output)
		if iface != "" { // for ptp4l/phc2sys this function only update metrics
			var values map[event.ValueType]interface{}
			if iface != clockRealTime && p.name == ts2phcProcessName {
				eventSource := p.ifaces.GetEventSource(masterOffsetIface.getByAlias(configName, iface).name)
				if eventSource == event.GNSS {
					values = map[event.ValueType]interface{}{event.NMEA_STATUS: int64(1)}
				}
			}
			p.ProcessTs2PhcEvents(ptpOffset, source, iface, values)
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
		if ptpState == event.PTP_LOCKED {
			updateClockStateMetrics(p.name, iface, LOCKED)
		} else {
			updateClockStateMetrics(p.name, iface, FREERUN)
		}
	}

}

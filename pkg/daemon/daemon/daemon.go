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

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
)

const (
	PtpNamespace              = "ptp"
	PTP4L_CONF_FILE_PATH      = "/etc/ptp4l.conf"
	PTP4L_CONF_DIR            = "/ptp4l-conf"
	connectionRetryInterval   = 1 * time.Second
	ClockClassChangeIndicator = "selected best master clock"
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
	ifaces          []string
	ptp4lSocketPath string
	ptp4lConfigPath string
	configName      string
	messageTag      string
	exitCh          chan bool
	execMutex       sync.Mutex
	stopped         bool
	logFilterRegex  string
	cmd             *exec.Cmd
	depProcess      []process // this could gpsd and other process which needs to be stopped if the parent process is stopped
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
	pmcPollInterval int,
) *Daemon {
	RegisterMetrics(nodeName)
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
			if p.depProcess != nil {
				for _, d := range p.depProcess {
					if d != nil {
						time.Sleep(3 * time.Second)
						go d.CmdRun(false)
						time.Sleep(3 * time.Second)
						dn.pluginManager.AfterRunPTPCommand(p.nodeProfile, d.Name())
						//TODO: Maybe Move DPLL start and stop as part of pluign
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
					}
				}
			}
			time.Sleep(1 * time.Second)
			go p.cmdRun()
			p.eventCh = dn.processManager.eventChannel
			dn.pluginManager.AfterRunPTPCommand(p.nodeProfile, p.name)
		}
	}
	dn.pluginManager.PopulateHwConfig(dn.hwconfigs)
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
	var configOutput string
	var configInput *string
	var configOpts *string
	var messageTag string
	var cmd *exec.Cmd
	var ifaces string
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
				output.sections[index] = section
			}
		}

		// This add the flags needed for monitor
		addFlagsForMonitor(p, configOpts, output, false)

		configOutput, ifaces = output.renderPtp4lConf()

		if configInput != nil {
			*configInput = configOutput
		}

		cmdLine = fmt.Sprintf("/usr/sbin/%s -f %s  %s ", p, configPath, *configOpts)
		cmdLine = addScheduling(nodeProfile, cmdLine)
		args := strings.Split(cmdLine, " ")
		cmd = exec.Command(args[0], args[1:]...)

		//end rendering

		ifacesList := strings.Split(ifaces, ",")

		dprocess := ptpProcess{
			name:              p,
			ifaces:            ifacesList,
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
			//TODO: move this to plugin or call it from hwplugin or leave it here and remove Hardcoded
			gmInterface := ""
			if len(ifacesList) > 0 {
				gmInterface = ifacesList[0]
			}
			if e := mkFifo(); e != nil {
				glog.Errorf("Error creating named pipe, GNSS monitoring will not work as expected %s", e.Error())
			}
			gpsDaemon := &gpsd{
				name:        GPSD_PROCESSNAME,
				execMutex:   sync.Mutex{},
				cmd:         nil,
				serialPort:  GPSD_SERIALPORT,
				exitCh:      make(chan struct{}),
				gmInterface: gmInterface,
				stopped:     false,
			}
			gpsDaemon.CmdInit()
			dprocess.depProcess = append(dprocess.depProcess, gpsDaemon)

			// init gpspipe
			gpsPipeDaemon := &gpspipe{
				name:       GPSPIPE_PROCESSNAME,
				execMutex:  sync.Mutex{},
				cmd:        nil,
				serialPort: GPSPIPE_SERIALPORT,
				exitCh:     make(chan struct{}),
				stopped:    false,
			}
			gpsPipeDaemon.CmdInit()
			dprocess.depProcess = append(dprocess.depProcess, gpsPipeDaemon)
			// init dpll
			// TODO: Try to inject DPLL depProcess via plugin ?
			dpllDaemon := dpll.NewDpll(dpll.LocalMaxHoldoverOffSet, dpll.LocalHoldoverTimeout, dpll.MaxInSpecOffset,
				gmInterface, []event.EventSource{event.GNSS})
			dprocess.depProcess = append(dprocess.depProcess, dpllDaemon)
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
	for _, process := range dn.processManager.process {
		if process.name == ptp4lProcessName {
			process.pmcCheck = true
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

					_, err := (*c).Write([]byte(clockClassOut))
					if err != nil {
						glog.Errorf("failed to write class change event %s", err.Error())
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
				source, ptpOffset, _, iface := extractMetrics(p.messageTag, p.name, p.ifaces, output)
				if iface != "" {
					var ptpState event.PTPState
					ptpState = event.PTP_FREERUN
					if int64(ptpOffset) < p.ptpClockThreshold.MaxOffsetThreshold &&
						int64(ptpOffset) > p.ptpClockThreshold.MinOffsetThreshold {
						ptpState = event.PTP_LOCKED
						updateClockStateMetrics(p.name, iface, LOCKED)
					} else {
						updateClockStateMetrics(p.name, iface, FREERUN)
					}
					if source == ts2phcProcessName && p.clockType == event.GM {
						if len(p.ifaces) > 0 {
							iface = p.ifaces[0]
						}
						p.eventCh <- event.EventChannel{
							ProcessName: event.TS2PHC,
							State:       ptpState,
							CfgName:     p.configName,
							IFace:       iface,
							Values: map[event.ValueType]int64{
								event.OFFSET: int64(ptpOffset),
							},
							ClockType:  p.clockType,
							Time:       time.Now().Unix(),
							WriteToLog: false,
							Reset:      false,
						}
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

// cmdStop stops ptpProcess launched by cmdRun
func (p *ptpProcess) cmdStop() {
	glog.Infof("stopping %s...", p.name)
	if p.cmd == nil {
		return
	}
	p.setStopped(true)
	if p.cmd.Process != nil {
		glog.Infof("Sending TERM to (%s) PID: %d", p.name, p.cmd.Process.Pid)
		p.cmd.Process.Signal(syscall.SIGTERM)
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
			MinOffsetThreshold: 100,
		}
	}
}

func (p *ptpProcess) MonitorEvent(offset float64, clockState string) {
	// not implemented
}

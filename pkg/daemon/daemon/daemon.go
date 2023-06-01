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
	}
}

// Run in a for loop to listen for any LinuxPTPConfUpdate changes
func (dn *Daemon) Run() {
	for {
		select {
		case <-dn.ptpUpdate.UpdateCh:
			err := dn.applyNodePTPProfiles()
			if err != nil {
				glog.Errorf("linuxPTP apply node profile failed: %v", err)
			}
		case <-dn.stopCh:
			for _, p := range dn.processManager.process {
				if p != nil {
					for _, d := range p.depProcess {
						if d != nil {
							d.cmdStop()
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
			glog.Infof("stopping process.... %+v", p)
			if p.depProcess != nil {
				for _, d := range p.depProcess {
					if d != nil {
						d.cmdStop()
						d = nil
					}
				}
			}
			p.cmdStop()
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
						go d.cmdRun(false)
						time.Sleep(3 * time.Second)
						dn.pluginManager.AfterRunPTPCommand(p.nodeProfile, d.Name())
						d.monitorEvent(clockType, p.configName, p.exitCh, dn.processManager.eventChannel)
					}
				}
			}
			time.Sleep(1 * time.Second)
			go p.cmdRun()
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
			name:            p,
			ifaces:          ifacesList,
			ptp4lConfigPath: configPath,
			ptp4lSocketPath: socketPath,
			configName:      configFile,
			messageTag:      messageTag,
			exitCh:          make(chan bool),
			stopped:         false,
			logFilterRegex:  getLogFilterRegex(nodeProfile),
			cmd:             cmd,
			depProcess:      []process{},
			nodeProfile:     nodeProfile,
		}
		//TODO HARDWARE PLUGIN for e810
		if pProcess == ts2phcProcessName { //& if the x plugin is enabled
			if e := mkFifo(); e != nil {
				glog.Errorf("Error creating named pipe, GNSS monitoring will not work as expected %s", e.Error())
			}
			gpsDaemon := &gpsd{
				name:       GPSD_PROCESSNAME,
				execMutex:  sync.Mutex{},
				cmd:        nil,
				serialPort: GPSD_SERIALPORT,
				exitCh:     make(chan bool),
				stopped:    false,
			}
			gpsDaemon.cmdInit()
			dprocess.depProcess = append(dprocess.depProcess, gpsDaemon)

			// init gpspipe
			gpsPipeDaemon := &gpspipe{
				name:       GPSPIPE_PROCESSNAME,
				execMutex:  sync.Mutex{},
				cmd:        nil,
				serialPort: GPSPIPE_SERIALPORT,
				exitCh:     make(chan bool),
				stopped:    false,
			}
			gpsPipeDaemon.cmdInit()
			dprocess.depProcess = append(dprocess.depProcess, gpsPipeDaemon)
		}
		err = os.WriteFile(configPath, []byte(configOutput), 0644)
		if err != nil {
			printNodeProfile(nodeProfile)
			return fmt.Errorf("failed to write the configuration file named %s: %v", configPath, err)
		}
		dn.processManager.process = append(dn.processManager.process, &dprocess)
	}

	return nil
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
			glog.Errorf("cmdRun() error creating StdoutPipe for %s: %v", p.name, err)
			break
		}
		scanner := bufio.NewScanner(cmdReader)
		processStatus(p.name, p.messageTag, PtpProcessUp)
		go func() {
			for scanner.Scan() {
				output := scanner.Text()
				if regexErr != nil || !logFilterRegex.MatchString(output) {
					fmt.Printf("%s\n", output)
				}
				extractMetrics(p.messageTag, p.name, p.ifaces, output)
			}
			done <- struct{}{}
		}()
		// Don't restart after termination
		if !p.Stopped() {
			err = p.cmd.Start() // this is asynchronous call,
			if err != nil {
				glog.Errorf("cmdRun() error starting %s: %v", p.name, err)
			}
		}
		<-done // goroutine is done
		err = p.cmd.Wait()
		if err != nil {
			glog.Errorf("cmdRun() error waiting for %s: %v", p.name, err)
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
	glog.Infof("Stopping %s...", p.name)
	if p.cmd == nil {
		return
	}

	p.setStopped(true)

	if p.cmd.Process != nil {
		glog.Infof("Sending TERM to PID: %d", p.cmd.Process.Pid)
		p.cmd.Process.Signal(syscall.SIGTERM)
	}

	if p.ptp4lConfigPath != "" {
		err := os.Remove(p.ptp4lConfigPath)
		if err != nil {
			glog.Errorf("failed to remove ptp4l config path %s: %v", p.ptp4lConfigPath, err)
		}
	}

	<-p.exitCh
	glog.Infof("Process %d terminated", p.cmd.Process.Pid)
}

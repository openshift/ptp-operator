package daemon

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/golang/glog"
	"k8s.io/client-go/kubernetes"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"
)

const (
	PtpNamespace            = "openshift-ptp"
	PTP4L_CONF_FILE_PATH    = "/etc/ptp4l.conf"
	PTP4L_CONF_DIR          = "/ptp4l-conf"
	connectionRetryInterval = 1 * time.Second
	eventSocket             = "/cloud-native/events.sock"
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
	exitCh          chan bool
	cmd             *exec.Cmd
}

// Daemon is the main structure for linuxptp instance.
// It contains all the necessary data to run linuxptp instance.
type Daemon struct {
	// node name where daemon is running
	nodeName  string
	namespace string
	// write logs to socket, this will also send metrics to the socket
	stdoutToSocket bool

	// kubeClient allows interaction with Kubernetes, including the node we are running on.
	kubeClient *kubernetes.Clientset

	ptpUpdate *LinuxPTPConfUpdate

	processManager *ProcessManager

	// channel ensure LinuxPTP.Run() exit when main function exits.
	// stopCh is created by main function and passed by Daemon via NewLinuxPTP()
	stopCh <-chan struct{}
}

// NewLinuxPTP is called by daemon to generate new linuxptp instance
func New(
	nodeName string,
	namespace string,
	stdoutToSocket bool,
	kubeClient *kubernetes.Clientset,
	ptpUpdate *LinuxPTPConfUpdate,
	stopCh <-chan struct{},
) *Daemon {
	if !stdoutToSocket {
		RegisterMetrics(nodeName)
	}
	return &Daemon{
		nodeName:       nodeName,
		namespace:      namespace,
		stdoutToSocket: stdoutToSocket,
		kubeClient:     kubeClient,
		ptpUpdate:      ptpUpdate,
		processManager: &ProcessManager{},
		stopCh:         stopCh,
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
					cmdStop(p)
					p = nil
				}
			}
			glog.Infof("linuxPTP stop signal received, existing..")
			return
		}
	}
}

func printWhenNotNil(p *string, description string) {
	if p != nil {
		glog.Info(description, ": ", *p)
	}
}

func (dn *Daemon) applyNodePTPProfiles() error {
	glog.Infof("in applyNodePTPProfiles")

	for _, p := range dn.processManager.process {
		if p != nil {
			glog.Infof("stopping process.... %+v", p)
			cmdStop(p)
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
			time.Sleep(1 * time.Second)
			go cmdRun(p, dn.stdoutToSocket)
		}
	}
	return nil
}

func (dn *Daemon) applyNodePtpProfile(runID int, nodeProfile *ptpv1.PtpProfile) error {
	// This add the flags needed for monitor
	addFlagsForMonitor(nodeProfile, dn.stdoutToSocket)

	socketPath := fmt.Sprintf("/var/run/ptp4l.%d.socket", runID)
	configFile := fmt.Sprintf("ptp4l.%d.config", runID)
	// This will create the configuration needed to run the ptp4l and phc2sys
	err := dn.addProfileConfig(socketPath, configFile, nodeProfile)
	if err != nil {
		return fmt.Errorf("failed to add profile config %s: %v", configFile, err)
	}

	glog.Infof("------------------------------------")
	printWhenNotNil(nodeProfile.Name, "Profile Name")
	printWhenNotNil(nodeProfile.Interface, "Interface")
	printWhenNotNil(nodeProfile.Ptp4lOpts, "Ptp4lOpts")
	printWhenNotNil(nodeProfile.Ptp4lConf, "Ptp4lConf")
	printWhenNotNil(nodeProfile.Phc2sysOpts, "Phc2sysOpts")
	glog.Infof("------------------------------------")

	if nodeProfile.Phc2sysOpts != nil {
		dn.processManager.process = append(dn.processManager.process, &ptpProcess{
			name:       "phc2sys",
			ifaces:     strings.Split(*nodeProfile.Interface, ","),
			configName: configFile,
			exitCh:     make(chan bool),
			cmd:        phc2sysCreateCmd(nodeProfile)})
	} else {
		glog.Infof("applyNodePtpProfile: not starting phc2sys, phc2sysOpts is empty")
	}

	configPath := fmt.Sprintf("/var/run/%s", configFile)
	err = ioutil.WriteFile(configPath, []byte(*nodeProfile.Ptp4lConf), 0644)
	if err != nil {
		return fmt.Errorf("failed to write the configuration file named %s: %v", configPath, err)
	}

	dn.processManager.process = append(dn.processManager.process, &ptpProcess{
		name:            "ptp4l",
		ifaces:          strings.Split(*nodeProfile.Interface, ","),
		ptp4lConfigPath: configPath,
		ptp4lSocketPath: socketPath,
		configName:      configFile,
		exitCh:          make(chan bool),
		cmd:             ptp4lCreateCmd(nodeProfile, configPath)})

	return nil
}

func (dn *Daemon) addProfileConfig(socketPath string, configFile string, nodeProfile *ptpv1.PtpProfile) error {
	// TODO: later implement a merge capability
	if nodeProfile.Ptp4lConf == nil || *nodeProfile.Ptp4lConf == "" {
		// We need to copy this to another variable because is a pointer
		config := string(dn.ptpUpdate.defaultPTP4lConfig)
		nodeProfile.Ptp4lConf = &config
	}

	if nodeProfile.Ptp4lOpts == nil || *nodeProfile.Ptp4lOpts == "" {
		// We need to copy this to another variable because is a pointer
		opts := string("")
		nodeProfile.Ptp4lOpts = &opts
	}

	output := &ptp4lConf{}
	err := output.populatePtp4lConf(nodeProfile.Ptp4lConf)
	if err != nil {
		return err
	}

	if nodeProfile.Interface != nil && *nodeProfile.Interface != "" {
		ifaceSection := fmt.Sprintf("[%s]", *nodeProfile.Interface)
		output.sections[ifaceSection] = ptp4lConfSection{options: map[string]string{}}
	} else {
		iface := string("")
		nodeProfile.Interface = &iface
	}

	section := output.sections["[global]"]
	section.options["message_tag"] = fmt.Sprintf("[%s]", configFile)
	section.options["uds_address"] = socketPath
	output.sections["[global]"] = section

	*nodeProfile.Ptp4lConf, *nodeProfile.Interface = output.renderPtp4lConf()

	if nodeProfile.Phc2sysOpts != nil {
		commandLine := fmt.Sprintf("%s -z %s -t [%s]",
			*nodeProfile.Phc2sysOpts,
			socketPath,
			configFile)
		nodeProfile.Phc2sysOpts = &commandLine
	}

	return nil
}

// phc2sysCreateCmd generate phc2sys command
func phc2sysCreateCmd(nodeProfile *ptpv1.PtpProfile) *exec.Cmd {
	cmdLine := fmt.Sprintf("/usr/sbin/phc2sys %s", *nodeProfile.Phc2sysOpts)
	args := strings.Split(cmdLine, " ")
	return exec.Command(args[0], args[1:]...)
}

// ptp4lCreateCmd generate ptp4l command
func ptp4lCreateCmd(nodeProfile *ptpv1.PtpProfile, confFilePath string) *exec.Cmd {
	cmdLine := fmt.Sprintf("/usr/sbin/ptp4l -f %s %s",
		confFilePath,
		*nodeProfile.Ptp4lOpts)

	args := strings.Split(cmdLine, " ")
	return exec.Command(args[0], args[1:]...)
}

// cmdRun runs given ptpProcess and wait for errors
func cmdRun(p *ptpProcess, stdoutToSocket bool) {
	glog.Infof("Starting %s...", p.name)
	glog.Infof("%s cmd: %+v", p.name, p.cmd)
	var c net.Conn
	var err error
	defer func() {
		if stdoutToSocket && c != nil {
			if err := c.Close(); err != nil {
				glog.Errorf("closing connection returned error %s", err)
			}
		}
		p.exitCh <- true
	}()

	//
	// don't discard process stderr output
	//
	p.cmd.Stderr = os.Stderr
	cmdReader, err := p.cmd.StdoutPipe()
	if err != nil {
		glog.Errorf("cmdRun() error creating StdoutPipe for %s: %v", p.name, err)
		return
	}

	done := make(chan struct{})

	if !stdoutToSocket {
		scanner := bufio.NewScanner(cmdReader)
		go func() {
			for scanner.Scan() {
				output := scanner.Text()
				fmt.Printf("%s\n", output)
				extractMetrics(p.configName, p.name, p.ifaces, output)
			}
			done <- struct{}{}
		}()
	} else {
		go func() {
		connect:
			select {
			case <-p.exitCh:
				done <- struct{}{}
			default:
				c, err = net.Dial("unix", eventSocket)
				if err != nil {
					glog.Errorf("error trying to connect to event socket")
					time.Sleep(connectionRetryInterval)
					goto connect
				}
			}
			scanner := bufio.NewScanner(cmdReader)
			for scanner.Scan() {
				output := scanner.Text()
				out := fmt.Sprintf("%s\n", output)
				fmt.Printf("%s", out)
				_, err := c.Write([]byte(out))
				if err != nil {
					glog.Errorf("Write error:", err)
					goto connect
				}
			}
			done <- struct{}{}
		}()
	}

	err = p.cmd.Start()
	if err != nil {
		glog.Errorf("cmdRun() error starting %s: %v", p.name, err)
		return
	}

	<-done

	err = p.cmd.Wait()
	if err != nil {
		glog.Errorf("cmdRun() error waiting for %s: %v", p.name, err)
		return
	}
	return
}

// cmdStop stops ptpProcess launched by cmdRun
func cmdStop(p *ptpProcess) {
	glog.Infof("Stopping %s...", p.name)
	if p.cmd == nil {
		return
	}

	if p.cmd.Process != nil {
		glog.Infof("Sending TERM to PID: %d", p.cmd.Process.Pid)
		p.cmd.Process.Signal(syscall.SIGTERM)
	}

	if p.ptp4lSocketPath != "" {
		err := os.Remove(p.ptp4lSocketPath)
		if err != nil {
			glog.Errorf("failed to remove ptp4l socket path %s: %v", p.ptp4lSocketPath, err)
		}
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

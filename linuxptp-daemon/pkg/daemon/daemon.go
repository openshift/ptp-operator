package daemon

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/golang/glog"
	ptpv1 "github.com/openshift/ptp-operator/pkg/apis/ptp/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	PtpNamespace = "ptp"
	PTP4L_CONF_FILE_PATH = "/etc/ptp4l.conf"
)

// ProcessManager manages a set of ptpProcess
// which could be ptp4l, phc2sys or timemaster.
// Processes in ProcessManager will be started
// or stopped simultaneously.
type ProcessManager struct {
	process	[]*ptpProcess
}

type ptpProcess struct {
	name	string
	exitCh	chan bool
	cmd	*exec.Cmd
}

// LinuxPTPUpdate controls whether to update linuxPTP conf
// and contains linuxPTP conf to be updated. It's rendered
// and passed to linuxptp instance by daemon.
type LinuxPTPConfUpdate struct {
	UpdateCh	chan bool
	NodeProfile	*ptpv1.PtpProfile
}

// Daemon is the main structure for linuxptp instance.
// It contains all the necessary data to run linuxptp instance.
type Daemon struct {
	// node name where daemon is running
	nodeName	string
	namespace	string

        // kubeClient allows interaction with Kubernetes, including the node we are running on.
        kubeClient      *kubernetes.Clientset

	ptpUpdate	*LinuxPTPConfUpdate
	// channel ensure LinuxPTP.Run() exit when main function exits.
	// stopCh is created by main function and passed by Daemon via NewLinuxPTP()
	stopCh <-chan struct{}
}

// NewLinuxPTP is called by daemon to generate new linuxptp instance
func New(
	nodeName	string,
	namespace	string,
	kubeClient	*kubernetes.Clientset,
	ptpUpdate	*LinuxPTPConfUpdate,
	stopCh		<-chan struct{},
) *Daemon {
	return &Daemon{
		nodeName:	nodeName,
		namespace:	namespace,
                kubeClient:     kubeClient,
		ptpUpdate:	ptpUpdate,
		stopCh:		stopCh,
	}
}

// Run in a for loop to listen for any LinuxPTPConfUpdate changes
func (dn *Daemon) Run() {
	processManager := &ProcessManager{}
	for {
		select {
		case <-dn.ptpUpdate.UpdateCh:
			err := applyNodePTPProfile(processManager, dn.ptpUpdate.NodeProfile)
			if err != nil {
				glog.Errorf("linuxPTP apply node profile failed: %v", err)
			}
		case <-dn.stopCh:
			for _, p := range processManager.process {
				if p != nil {
					cmdStop(p)
					p = nil
				}
			}
			glog.Infof("linuxPTP stop signal received, existing..")
			return
		}
	}
	return
}

func printWhenNotNil(p *string, description string) {
	if p != nil {
		glog.Info(description, ": ", *p)
	}
}

func applyNodePTPProfile(pm *ProcessManager, nodeProfile *ptpv1.PtpProfile) error {
	glog.Infof("in applyNodePTPProfile")

	glog.Infof("updating NodePTPProfile to:")
	glog.Infof("------------------------------------")
	printWhenNotNil(nodeProfile.Name, "Profile Name")
	printWhenNotNil(nodeProfile.Interface, "Interface")
	printWhenNotNil(nodeProfile.Ptp4lOpts, "Ptp4lOpts")
	printWhenNotNil(nodeProfile.Ptp4lConf, "Ptp4lConf")
	printWhenNotNil(nodeProfile.Phc2sysOpts, "Phc2sysOpts")
	glog.Infof("------------------------------------")

	for _, p := range pm.process {
		if p != nil {
			glog.Infof("stopping process.... %+v", p)
			cmdStop(p)
			p = nil
		}
	}

	// All process should have been stopped,
	// clear process in process manager.
	// Assigning pm.process to nil releases
	// the underlying slice to the garbage
	// collector (assuming there are no other
	// references).
	pm.process = nil

	// TODO:
	// compare nodeProfile with previous config,
	// only apply when nodeProfile changes

	if nodeProfile.Phc2sysOpts != nil {
		pm.process = append(pm.process, &ptpProcess{
			name: "phc2sys",
			exitCh: make(chan bool),
			cmd: phc2sysCreateCmd(nodeProfile)})
	} else {
		glog.Infof("applyNodePTPProfile: not starting phc2sys, phc2sysOpts is empty")
	}

	if nodeProfile.Ptp4lOpts != nil && nodeProfile.Interface != nil {
		pm.process = append(pm.process, &ptpProcess{
			name: "ptp4l",
			exitCh: make(chan bool),
			cmd: ptp4lCreateCmd(nodeProfile)})
	} else {
		glog.Infof("applyNodePTPProfile: not starting ptp4l, ptp4lOpts or interface is empty")
	}

	for _, p := range pm.process {
		if p != nil {
			time.Sleep(1*time.Second)
			go cmdRun(p)
		}
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
func ptp4lCreateCmd(nodeProfile *ptpv1.PtpProfile) *exec.Cmd {
	cmdLine := fmt.Sprintf("/usr/sbin/ptp4l -m -f %s -i %s %s",
		PTP4L_CONF_FILE_PATH,
		*nodeProfile.Interface,
		*nodeProfile.Ptp4lOpts)

	args := strings.Split(cmdLine, " ")
	return exec.Command(args[0], args[1:]...)
}


// cmdRun runs given ptpProcess and wait for errors
func cmdRun(p *ptpProcess) {
	glog.Infof("Starting %s...", p.name)
	glog.Infof("%s cmd: %+v", p.name, p.cmd)

	defer func() {
		p.exitCh <- true
	}()

	cmdReader, err := p.cmd.StdoutPipe()
	if err != nil {
		glog.Errorf("cmdRun() error creating StdoutPipe for %s: %v", p.name, err)
		return
	}

	done := make(chan struct{})

	scanner := bufio.NewScanner(cmdReader)
	go func() {
		for scanner.Scan() {
			fmt.Printf("%s\n", scanner.Text())
		}
		done <- struct{}{}
	}()

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
func cmdStop (p *ptpProcess) {
	glog.Infof("Stopping %s...", p.name)
	if p.cmd == nil {
		return
	}

	if p.cmd.Process != nil {
		glog.Infof("Sending TERM to PID: %d", p.cmd.Process.Pid)
		p.cmd.Process.Signal(syscall.SIGTERM)
	}

	<-p.exitCh
	glog.Infof("Process %d terminated", p.cmd.Process.Pid)
}

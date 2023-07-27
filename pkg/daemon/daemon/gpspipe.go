package daemon

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/openshift/linuxptp-daemon/pkg/config"

	"github.com/golang/glog"
)

const (
	// GPSPIPE_PROCESSNAME ... gpspipe process name
	GPSPIPE_PROCESSNAME = "gpspipe"
	// GPSPIPE_SERIALPORT ... gpspipe serial port
	GPSPIPE_SERIALPORT = "/gpsd/data"
	// GPSD_DIR ... gpsd directory
	GPSD_DIR = "/gpsd"
)

type gpspipe struct {
	name       string
	execMutex  sync.Mutex
	cmd        *exec.Cmd
	serialPort string
	exitCh     chan struct{}
	stopped    bool
	messageTag string
}

// Name ... Process name
func (gp *gpspipe) Name() string {
	return gp.name
}

// ExitCh ... exit channel
func (gp *gpspipe) ExitCh() chan struct{} {
	return gp.exitCh
}

// SerialPort ... get SerialPort
func (gp *gpspipe) SerialPort() string {
	return gp.serialPort
}
func (gp *gpspipe) setStopped(val bool) {
	gp.execMutex.Lock()
	gp.stopped = val
	gp.execMutex.Unlock()
}

// Stopped ... check if gpspipe is stopped
func (gp *gpspipe) Stopped() bool {
	gp.execMutex.Lock()
	me := gp.stopped
	gp.execMutex.Unlock()
	return me
}

// CmdStop ... stop gpspipe
func (gp *gpspipe) CmdStop() {
	glog.Infof("stopping %s...", gp.name)
	if gp.cmd == nil {
		return
	}
	gp.setStopped(true)
	processStatus(nil, gp.name, gp.messageTag, PtpProcessDown)
	if gp.cmd.Process != nil {
		glog.Infof("Sending TERM to (%s) PID: %d", gp.name, gp.cmd.Process.Pid)
		err := gp.cmd.Process.Signal(syscall.SIGTERM)
		if err != nil {
			glog.Errorf("Failed to send term signal to named pipe: %s", GPSPIPE_SERIALPORT)
			return
		}
	}
	// Clean up (delete) the named pipe
	err := os.Remove(GPSPIPE_SERIALPORT)
	if err != nil {
		glog.Errorf("Failed to delete named pipe: %s", GPSPIPE_SERIALPORT)
	}
	glog.Infof("Process %s (%d) terminated", gp.name, gp.cmd.Process.Pid)
}

// CmdInit ... initialize gpspipe
func (gp *gpspipe) CmdInit() {
	if gp.name == "" {
		gp.name = GPSPIPE_PROCESSNAME
	}
	gp.cmd = exec.Command("/usr/bin/bash", "-c", fmt.Sprintf("gpspipe -v -d -r -l -o  %s ", gp.SerialPort()))

}

// CmdRun ... run gpspipe
func (gp *gpspipe) CmdRun(stdoutToSocket bool) {
	glog.Infof("running process %s", gp.name)
	stdout, err := gp.cmd.Output()
	if err != nil {
		glog.Errorf("error gpspipe %s", err.Error())
		processStatus(nil, gp.name, gp.messageTag, PtpProcessDown)
	} else {
		processStatus(nil, gp.name, gp.messageTag, PtpProcessUp)
	}
	glog.Infof(string(stdout))
}

func output(reader io.ReadCloser) error {
	buf := make([]byte, 1024)
	for {
		num, err := reader.Read(buf)
		if err != nil && err != io.EOF {
			return err
		}
		if num > 0 {
			fmt.Printf("%s", string(buf[:num]))
		}
	}
}

func mkFifo() error {
	//TODO:this could be used as mount volume
	_ = os.Mkdir(GPSD_DIR, os.ModePerm)
	if err := syscall.Mkfifo(GPSPIPE_SERIALPORT, 0600); err != nil {
		return err
	}
	return nil
}

// MonitorProcess ... monitor gpspipe
func (gp *gpspipe) MonitorProcess(config config.ProcessConfig) {
	//TODO implement me
	glog.Infof("monitoring for gpspipe not implemented")
}

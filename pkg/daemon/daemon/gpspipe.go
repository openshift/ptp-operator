package daemon

import (
	"fmt"
	"github.com/openshift/linuxptp-daemon/pkg/config"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

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
	cmdLine    string
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
	glog.Infof("Process %s terminated", gp.name)
}

// CmdInit ... initialize gpspipe
func (gp *gpspipe) CmdInit() {
	if gp.name == "" {
		gp.name = GPSPIPE_PROCESSNAME
	}
	gp.cmdLine = fmt.Sprintf("/usr/local/bin/gpspipe -v -r -l -o %s", gp.SerialPort())
}

// CmdRun ... run gpspipe
func (gp *gpspipe) CmdRun(stdoutToSocket bool) {
	defer func() {
		gp.exitCh <- struct{}{}
	}()
	processStatus(nil, gp.name, gp.messageTag, PtpProcessUp)
	for {
		glog.Infof("Starting %s...", gp.Name())
		glog.Infof("%s cmd: %+v", gp.Name(), gp.cmd)
		gp.cmd.Stderr = os.Stderr
		var err error
		if err != nil {
			glog.Errorf("CmdRun() error creating StdoutPipe for %s: %v", gp.Name(), err)
			if gp.Stopped() {
				return
			}
			time.Sleep(1 * time.Second)
			continue
		}
		// Don't restart after termination
		if !gp.Stopped() {
			time.Sleep(1 * time.Second)
			err = gp.cmd.Start() // this is asynchronous call,
			if err != nil {
				glog.Errorf("CmdRun() error starting %s: %v", gp.Name(), err)
			}
			err = gp.cmd.Wait()
			if err != nil {
				glog.Errorf("CmdRun() error waiting for %s: %v, atempting to restart", gp.Name(), err)
			}
			newCmd := exec.Command(gp.cmd.Args[0], gp.cmd.Args[1:]...)
			gp.cmd = newCmd
		} else {
			processStatus(nil, gp.name, gp.messageTag, PtpProcessDown)
			gp.exitCh <- struct{}{}
			break
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

package daemon

import (
	"context"
	"fmt"
	"github.com/openshift/linuxptp-daemon/pkg/config"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/golang/glog"
)

const (
	GPSPIPE_PROCESSNAME = "gpspipe"
	GPSPIPE_SERIALPORT  = "/gpsd/data"
	GPSD_DIR            = "/gpsd"
)

type gpspipe struct {
	name       string
	execMutex  sync.Mutex
	cmd        *exec.Cmd
	serialPort string
	exitCh     chan bool
	stopped    bool
	cancelFn   context.CancelFunc
	ctx        context.Context
}

func (gp *gpspipe) Name() string {
	return gp.name
}

func (gp *gpspipe) SerialPort() string {
	return gp.serialPort
}
func (gp *gpspipe) setStopped(val bool) {
	gp.execMutex.Lock()
	gp.stopped = val
	gp.execMutex.Unlock()
}

func (gp *gpspipe) Stopped() bool {
	gp.execMutex.Lock()
	me := gp.stopped
	gp.execMutex.Unlock()
	return me
}

func (gp *gpspipe) CmdStop() {
	glog.Infof("Stopping %s...", gp.name)
	if gp.cmd == nil {
		return
	}

	gp.setStopped(true)

	if gp.cmd.Process != nil {
		glog.Infof("Sending TERM to PID: %d", gp.cmd.Process.Pid)
		gp.cmd.Process.Signal(syscall.SIGTERM)
	}
	// Clean up (delete) the named pipe
	err := os.Remove(GPSPIPE_SERIALPORT)
	if err != nil {
		glog.Errorf("Failed to delete named pipe: %s", GPSD_SERIALPORT)
	}
	<-gp.exitCh
	glog.Infof("Process %d terminated", gp.cmd.Process.Pid)
}

// ubxtool -w 5 -v 1 -p MON-VER -P 29.20
func (gp *gpspipe) CmdInit() {
	if gp.name == "" {
		gp.name = GPSPIPE_PROCESSNAME
	}
	gp.ctx, gp.cancelFn = context.WithCancel(context.Background())

	//cmdLine := fmt.Sprintf("gpspipe -v -d -r -l -o  %s ", gp.SerialPort())
	//args := strings.Split(cmdLine, " ")
	//gp.cmd = exec.Command(args[0], args[1:]...)
	gp.cmd = exec.Command("/usr/bin/bash", "-c", fmt.Sprintf("gpspipe -v -d -r -l -o  %s ", gp.SerialPort()))

}
func (gp *gpspipe) CmdRun(stdoutToSocket bool) {
	glog.Infof("running process %s", gp.name)
	stdout, err := gp.cmd.Output()
	if err != nil {
		glog.Errorf("error gpspipe %s", err.Error())
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
	return nil
}

func mkFifo() error {
	//TODO:this could be used as mount volume
	_ = os.Mkdir(GPSD_DIR, os.ModePerm)
	if err := syscall.Mkfifo(GPSPIPE_SERIALPORT, 0600); err != nil {
		return err
	}
	return nil
}
func (gp *gpspipe) MonitorProcess(config config.ProcessConfig) {
	//TODO implement me
	glog.Infof("monitoring for gpspipe not implemented")
}

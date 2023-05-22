package daemon

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/golang/glog"
	"github.com/openshift/linuxptp-daemon/pkg/event"
	"github.com/openshift/linuxptp-daemon/pkg/gnss"
)

const (
	GPSD_PROCESSNAME = "gpsd"
	GPSD_SERIALPORT  = "/dev/gnss0"
)

type gpsd struct {
	name       string
	execMutex  sync.Mutex
	cmd        *exec.Cmd
	serialPort string
	exitCh     chan bool
	stopped    bool
}

func (g *gpsd) monitorEvent(clockType event.ClockType, cfgName string, chClose chan bool, chEventChannel chan<- event.EventChannel) {
	go gnss.MonitorGNSSEvents(clockType, cfgName, "E810", chClose, chEventChannel)
}

func (g *gpsd) Name() string {
	return g.name
}

func (g *gpsd) SerialPort() string {
	return g.serialPort
}
func (g *gpsd) setStopped(val bool) {
	g.execMutex.Lock()
	g.stopped = val
	g.execMutex.Unlock()
}

func (g *gpsd) Stopped() bool {
	g.execMutex.Lock()
	me := g.stopped
	g.execMutex.Unlock()
	return me
}

func (g *gpsd) cmdStop() {
	glog.Infof("Stopping %s...", g.name)
	if g.cmd == nil {
		return
	}

	g.setStopped(true)

	if g.cmd.Process != nil {
		glog.Infof("Sending TERM to PID: %d", g.cmd.Process.Pid)
		g.cmd.Process.Signal(syscall.SIGTERM)
	}

	<-g.exitCh
	glog.Infof("Process %d terminated", g.cmd.Process.Pid)
}

// ubxtool -w 5 -v 1 -p MON-VER -P 29.20
func (g *gpsd) cmdInit() {
	if g.name == "" {
		g.name = "gpsd"
	}
	cmdLine := fmt.Sprintf("/usr/local/sbin/%s -p -n -S 2947 -G -N -D 5 %s", g.Name(), g.SerialPort())
	args := strings.Split(cmdLine, " ")
	g.cmd = exec.Command(args[0], args[1:]...)

}

func (g *gpsd) cmdRun(stdoutToSocket bool) {
	done := make(chan struct{}) // Done setting up logging.  Go ahead and wait for process
	defer func() {
		g.exitCh <- true
	}()
	for {
		glog.Infof("Starting %s...", g.Name())
		glog.Infof("%s cmd: %+v", g.Name(), g.cmd)
		g.cmd.Stderr = os.Stderr
		cmdReader, err := g.cmd.StdoutPipe()

		if err != nil {
			glog.Errorf("cmdRun() error creating StdoutPipe for %s: %v", g.Name(), err)
			break
		}
		if !stdoutToSocket {
			scanner := bufio.NewScanner(cmdReader)
			go func() {
				for scanner.Scan() {
					//TODO: suppress logs for
					output := scanner.Text()
					fmt.Printf("%s\n", output)
				}
				done <- struct{}{}
			}()
		}
		// Don't restart after termination
		if !g.Stopped() {
			time.Sleep(1 * time.Second)
			err = g.cmd.Start() // this is asynchronous call,
			if err != nil {
				glog.Errorf("cmdRun() error starting %s: %v", g.Name(), err)
			}
		}
		<-done // goroutine is done
		err = g.cmd.Wait()
		if err != nil {
			glog.Errorf("cmdRun() error waiting for %s: %v", g.Name(), err)
		}
	}
}

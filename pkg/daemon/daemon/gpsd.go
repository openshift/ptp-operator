package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/golang/glog"
	"github.com/openshift/linuxptp-daemon/pkg/event"
	"github.com/openshift/linuxptp-daemon/pkg/gnss"
)

const (
	GPSD_PROCESSNAME     = "gpsd"
	GNSSMONITOR_INTERVAL = 5 * time.Second
)

type gpsd struct {
	name          string
	execMutex     sync.Mutex
	cmd           *exec.Cmd
	serialPort    string
	exitCh        chan struct{}
	stopped       bool
	state         event.PTPState
	offset        int64
	processConfig config.ProcessConfig
	gmInterface   string
	messageTag    string
}

// MonitorProcess ... Monitor gpsd process
func (g *gpsd) MonitorProcess(p config.ProcessConfig) {
	go g.monitorGNSSEvents(p, "E810")
}

// Name ... Process name
func (g *gpsd) Name() string {
	return g.name
}

// ExitCh ... exit channel
func (g *gpsd) ExitCh() chan struct{} {
	return g.exitCh
}

// SerialPort ... get SerialPort
func (g *gpsd) SerialPort() string {
	return g.serialPort
}
func (g *gpsd) setStopped(val bool) {
	g.execMutex.Lock()
	g.stopped = val
	g.execMutex.Unlock()
}

// Stopped ...
func (g *gpsd) Stopped() bool {
	g.execMutex.Lock()
	me := g.stopped
	g.execMutex.Unlock()
	return me
}

// CmdStop .... stop
func (g *gpsd) CmdStop() {
	glog.Infof("stopping %s...", g.name)
	if g.cmd == nil {
		return
	}
	g.setStopped(true)
	processStatus(nil, g.name, g.messageTag, PtpProcessDown)
	if g.cmd.Process != nil {
		glog.Infof("Sending TERM to PID: %d", g.cmd.Process.Pid)
		err := g.cmd.Process.Signal(syscall.SIGTERM)
		glog.Infof("Process %s (%d) failed to terminate", g.name, g.cmd.Process.Pid)
		if err != nil {
			return
		}
	}
	<-g.exitCh
	glog.Infof("Process %s (%d) terminated", g.name, g.cmd.Process.Pid)
}

// CmdInit .... ubxtool -w 5 -v 1 -p MON-VER -P 29.20
func (g *gpsd) CmdInit() {
	if g.name == "" {
		g.name = GPSD_PROCESSNAME
	}
	cmdLine := fmt.Sprintf("/usr/local/sbin/%s -p -n -S 2947 -G -N %s", g.Name(), g.SerialPort())
	args := strings.Split(cmdLine, " ")
	g.cmd = exec.Command(args[0], args[1:]...)
}

// CmdRun ... run gpsd
func (g *gpsd) CmdRun(stdoutToSocket bool) {
	defer func() {
		g.exitCh <- struct{}{}
	}()
	processStatus(nil, g.name, g.messageTag, PtpProcessUp)
	for {
		glog.Infof("Starting %s...", g.Name())
		glog.Infof("%s cmd: %+v", g.Name(), g.cmd)
		g.cmd.Stderr = os.Stderr
		var err error

		if err != nil {
			glog.Errorf("CmdRun() error creating StdoutPipe for %s: %v", g.Name(), err)
			break
		}
		// Don't restart after termination
		if !g.Stopped() {
			time.Sleep(1 * time.Second)
			err = g.cmd.Start() // this is asynchronous call,
			if err != nil {
				glog.Errorf("CmdRun() error starting %s: %v", g.Name(), err)
			}
			err = g.cmd.Wait()
			if err != nil {
				glog.Errorf("CmdRun() error waiting for %s: %v", g.Name(), err)
			}
		} else {
			processStatus(nil, g.name, g.messageTag, PtpProcessDown)
			g.exitCh <- struct{}{}
			break
		}
	}
}

// monitorGNSSEvents ... monitor gnss events
func (g *gpsd) monitorGNSSEvents(processCfg config.ProcessConfig, pluginName string) error {
	//done := make(chan struct{}) // Done setting up logging.  Go ahead and wait for process
	// var currentNavStatus int64
	g.processConfig = processCfg
	var err error
	//currentNavStatus = 0
	glog.Infof("Starting GNSS Monitoring for plugin  %s...", pluginName)

	var ublx *ublox.UBlox
	g.state = event.PTP_FREERUN
retry:
	if ublx, err = ublox.NewUblox(); err != nil {
		glog.Errorf("failed to initialize GNSS monitoring via ublox %s", err)
		time.Sleep(GNSSMONITOR_INTERVAL)
		goto retry
	} else {
		//TODO: monitor on 1PPS  events trigger
		ticker := time.NewTicker(GNSSMONITOR_INTERVAL)
		var lastState int64
		var lastOffset int64
		lastState = -1
		lastOffset = -1
		nStatus := int64(0)
		offsetS := "99999999"
		var err error
		for {
			select {
			case <-ticker.C:
				// do stuff
				if nStatus, err = ublx.NavStatus(); err != nil {
					glog.Errorf("failed to get nav status %s", err)
					nStatus = 0
				}
				if offsetS, err = ublx.GetNavOffset(); err != nil {
					glog.Errorf("failed to get nav offset %s", err)
					g.offset = 99999999
				} else {
					g.offset, _ = strconv.ParseInt(offsetS, 10, 64)
				}
				if nStatus >= 3 && g.isOffsetInRange() {
					g.state = event.PTP_LOCKED
				} else {
					g.state = event.PTP_FREERUN
				}
				if lastState != nStatus || lastOffset != g.offset {
					lastState = nStatus
					lastOffset = g.offset
					processCfg.EventChannel <- event.EventChannel{
						ProcessName: event.GNSS,
						State:       g.state,
						CfgName:     processCfg.ConfigName,
						IFace:       g.gmInterface,
						Values: map[event.ValueType]int64{
							event.GPS_STATUS: nStatus,
							event.OFFSET:     g.offset,
						},
						ClockType:  processCfg.ClockType,
						Time:       time.Now().Unix(),
						WriteToLog: true,
						Reset:      false,
					}
				}
			case <-g.exitCh:
				processCfg.EventChannel <- event.EventChannel{
					ProcessName: event.GNSS,
					CfgName:     processCfg.ConfigName,
					ClockType:   processCfg.ClockType,
					Time:        time.Now().Unix(),
					Reset:       true,
				}
				ticker.Stop()
				return nil
			}
		}
	}
}

func (g *gpsd) isOffsetInRange() bool {
	if g.offset < g.processConfig.GMThreshold.Max && g.offset > g.processConfig.GMThreshold.Min {
		return true
	}
	return false
}

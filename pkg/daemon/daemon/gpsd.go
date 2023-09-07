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

	"github.com/openshift/linuxptp-daemon/pkg/event"
	"github.com/openshift/linuxptp-daemon/pkg/gnss"
	gpsdlib "github.com/stratoberry/go-gpsd"
)

const (
	GPSD_PROCESSNAME     = "gpsd"
	GNSSMONITOR_INTERVAL = 5 * time.Second
)

type GPSD struct {
	name                 string
	execMutex            sync.Mutex
	cmdLine              string
	cmd                  *exec.Cmd
	serialPort           string
	exitCh               chan struct{}
	stopped              bool
	state                event.PTPState
	noFixStateOccurrence int // number of times no fix state has occurred
	offset               int64
	processConfig        config.ProcessConfig
	gmInterface          string
	messageTag           string
	ublxTool             *ublox.UBlox
	gpsdSession          *gpsdlib.Session
	gpsdDoneCh           chan bool
	sourceLost           bool
}

// Subscriber ... event subscriber
type Subscriber struct {
	source     event.EventSource
	gpsd       *GPSD
	monitoring bool
	id         string
}

// Monitor ...
func (s Subscriber) Monitor() {
	glog.Info("Starting GNSS Monitoring")
	go s.gpsd.MonitorGNSSEventsWithGPSD()
	s.monitoring = true
}

// Topic ... event topic
func (s Subscriber) Topic() event.EventSource {
	return s.source
}
func (s Subscriber) ID() string {
	return s.id
}

// Notify ... event notification
func (s Subscriber) Notify(source event.EventSource, state event.PTPState) {
	// not implemented
}
func (s Subscriber) MonitoringStarted() bool {
	return s.monitoring
}

// MonitorProcess ... Monitor GPSD process
func (g *GPSD) MonitorProcess(p config.ProcessConfig) {
	g.processConfig = p
	event.StateRegisterer.Register(Subscriber{source: event.NIL, gpsd: g, monitoring: false, id: string(event.GNSS)})
	//go g.MonitorGNSSEventsWithGPSD(p, "E810")
}

// Name ... Process name
func (g *GPSD) Name() string {
	return g.name
}

// ExitCh ... exit channel
func (g *GPSD) ExitCh() chan struct{} {
	return g.exitCh
}

// SerialPort ... get SerialPort
func (g *GPSD) SerialPort() string {
	return g.serialPort
}
func (g *GPSD) setStopped(val bool) {
	g.execMutex.Lock()
	g.stopped = val
	g.execMutex.Unlock()
}

// Stopped ...
func (g *GPSD) Stopped() bool {
	g.execMutex.Lock()
	me := g.stopped
	g.execMutex.Unlock()
	return me
}

// CmdStop .... stop
func (g *GPSD) CmdStop() {
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
	event.StateRegisterer.Unregister(Subscriber{source: event.NIL, gpsd: g, monitoring: false, id: string(event.GNSS)})
	<-g.exitCh
	glog.Infof("Process %s terminated", g.name)
}

// CmdInit ... initialize GPSD
func (g *GPSD) CmdInit() {
	if g.name == "" {
		g.name = GPSD_PROCESSNAME
	}
	g.cmdLine = fmt.Sprintf("/usr/local/sbin/%s -p -n -S 2947 -G -N %s", g.Name(), g.SerialPort())
}

// CmdRun ... run GPSD
func (g *GPSD) CmdRun(stdoutToSocket bool) {
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
			if g.stopped {
				return
			}
			time.Sleep(5 * time.Second)
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
			newCmd := exec.Command(g.cmd.Args[0], g.cmd.Args[1:]...)
			g.cmd = newCmd
		} else {
			processStatus(nil, g.name, g.messageTag, PtpProcessDown)
			g.exitCh <- struct{}{}
			break
		}
	}
}

// MonitorGNSSEventsWithGPSD ... monitor GNSS events
func (g *GPSD) MonitorGNSSEventsWithGPSD() {
	// close the session if it is already open
	defer func() {
		if g.gpsdSession != nil {
			err := g.gpsdSession.Close()
			if err != nil {
				return
			}
		}
	}()
	g.offset = 5 // default to 5 nano secs
	var err error
	noFixThreshold := 3 // default
retry:
	if g.gpsdSession, err = gpsdlib.Dial(gpsdlib.DefaultAddress); err != nil {
		glog.Errorf("Failed to connect to GPSD: %s, retrying", err)
		time.Sleep(2 * time.Second)
		goto retry
	}
	// TPV data like gps state
	g.gpsdSession.AddFilter("TPV", func(r interface{}) {
		tpv := r.(*gpsdlib.TPVReport)
		// gate it to the serial port
		if strings.TrimSpace(tpv.Device) != strings.TrimSpace(g.serialPort) {
			glog.Infof("Device %s is not %s set for monitoring", tpv.Device, g.serialPort)
			return // do not send event
		}
		// prevent noise, wait for 3 occurrence of no fix state
		if tpv.Mode == gpsdlib.NoFix || tpv.Mode == gpsdlib.NoValueSeen {
			g.noFixStateOccurrence++
		} else {
			g.noFixStateOccurrence = 0
		}

		if g.noFixStateOccurrence > noFixThreshold {
			g.state = event.PTP_FREERUN
			g.sourceLost = true
		} else if tpv.Mode == gpsdlib.Mode2D || tpv.Mode == gpsdlib.Mode3D {
			g.sourceLost = false
			if g.isOffsetInRange() {
				g.state = event.PTP_LOCKED
			} else {
				g.state = event.PTP_FREERUN
			}
		} else {
			return // do not send event yet
		}
		select {
		case g.processConfig.EventChannel <- event.EventChannel{
			ProcessName: event.GNSS,
			State:       g.state,
			CfgName:     g.processConfig.ConfigName,
			IFace:       g.gmInterface,
			Values: map[event.ValueType]int64{
				event.GPS_STATUS: int64(tpv.Mode),
				event.OFFSET:     g.offset,
			},
			ClockType:  g.processConfig.ClockType,
			Time:       time.Now().UnixMilli(),
			SourceLost: g.sourceLost,
			WriteToLog: true,
			Reset:      false,
		}:
		default:
			glog.Error("failed to send gpsd event to event handler")
		}

	})
	g.gpsdDoneCh = g.gpsdSession.Watch()
	for {
		select {
		case <-g.exitCh:
			glog.Infof("GPSD Monitor() exitCh")
			goto exit
		case <-g.gpsdDoneCh:
			if !g.stopped {
				time.Sleep(1 * time.Second)
				glog.Infof("restarting gpsd monitoring")
				goto retry
			}
			glog.Infof("GPSD Monitor() gpsdDone closed")
			goto exit
		default:
			// do nothing
			time.Sleep(500 * time.Millisecond)
		}
	}
exit:
	select {
	case g.processConfig.EventChannel <- event.EventChannel{
		ProcessName: event.GNSS,
		CfgName:     g.processConfig.ConfigName,
		ClockType:   g.processConfig.ClockType,
		Time:        time.Now().UnixMilli(),
		Reset:       true,
	}:
	default:
		glog.Error("failed to send gnss terminated event to eventHandler")
	}

}
func (g *GPSD) getUbloxOffset() {
	if g.ublxTool == nil {
		return
	}
	if offsetS, err := g.ublxTool.GetNavOffset(); err != nil {
		glog.Errorf("failed to get nav offset %s", err)
		g.offset = 99999999
	} else {
		if g.offset, err = strconv.ParseInt(offsetS, 10, 64); err != nil {
			glog.Errorf("failed to parse offset %s", err)
			g.offset = 99999999
		}
	}
}

// MonitorGNSSEventsWithUblox ... monitor GNSS events with ublox
func (g *GPSD) MonitorGNSSEventsWithUblox(processCfg config.ProcessConfig, pluginName string) {
	//done := make(chan struct{}) // Done setting up logging.  Go ahead and wait for process
	// var currentNavStatus int64
	g.processConfig = processCfg
	var err error
	//currentNavStatus = 0
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
		//var lastState int64
		//var lastOffset int64
		//lastState = -1
		//	lastOffset = -1
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
				//if lastState != nStatus || lastOffset != g.offset {
				//lastState = nStatus
				//lastOffset = g.offset
				select {
				case processCfg.EventChannel <- event.EventChannel{
					ProcessName: event.GNSS,
					State:       g.state,
					CfgName:     processCfg.ConfigName,
					IFace:       g.gmInterface,
					Values: map[event.ValueType]int64{
						event.GPS_STATUS: nStatus,
						event.OFFSET:     g.offset,
					},
					ClockType:  processCfg.ClockType,
					Time:       time.Now().UnixMilli(),
					SourceLost: false,
					WriteToLog: true,
					Reset:      false,
				}:
				default:
					glog.Error("failed to send gnss terminated event to eventHandler")
				}
				//}
			case <-g.exitCh:
				select {
				case processCfg.EventChannel <- event.EventChannel{
					ProcessName: event.GNSS,
					CfgName:     processCfg.ConfigName,
					ClockType:   processCfg.ClockType,
					Time:        time.Now().UnixMilli(),
					Reset:       true,
				}:
				default:
					glog.Error("failed t send gnss terminated event to eventHandler")
				}
				ticker.Stop()
				return // exit
			}
		}
	}
}

// isOffsetInRange ... check if offset is in range
func (g *GPSD) isOffsetInRange() bool {
	if g.offset <= g.processConfig.GMThreshold.Max && g.offset >= g.processConfig.GMThreshold.Min {
		return true
	}
	return false
}

package daemon

import (
	"fmt"
	"os"
	"os/exec"

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
	GNSSMONITOR_INTERVAL = 1 * time.Second
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
	subscriber           *GPSDSubscriber
}

// GPSDSubscriber ... event subscriber
type GPSDSubscriber struct {
	source event.EventSource
	gpsd   *GPSD
	id     string
}

// Monitor ...
func (s GPSDSubscriber) Monitor() {
	glog.Info("Starting GNSS Monitoring")
	go s.gpsd.MonitorGNSSEventsWithUblox()
}

// Topic ... event topic
func (s GPSDSubscriber) Topic() event.EventSource {
	return s.source
}
func (s GPSDSubscriber) ID() string {
	return s.id
}

// Notify ... event notification
func (s GPSDSubscriber) Notify(source event.EventSource, state event.PTPState) {
	// not implemented
}

// MonitorProcess ... Monitor GPSD process
func (g *GPSD) MonitorProcess(p config.ProcessConfig) {
	g.processConfig = p
}

func (g *GPSD) registerSubscriber() {
	event.StateRegisterer.Register(g.subscriber)
}

func (g *GPSD) unRegisterSubscriber() {
	event.StateRegisterer.Unregister(g.subscriber)
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
		if err != nil {
			glog.Infof("Process %s (%d) failed to terminate", g.name, g.cmd.Process.Pid)
		}
	}
	g.unRegisterSubscriber()
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
		if g.subscriber != nil {
			g.unRegisterSubscriber()
		}
	}()
	// clean up
	if g.subscriber != nil {
		g.unRegisterSubscriber()
	}
	g.subscriber = &GPSDSubscriber{source: event.MONITORING, gpsd: g, id: string(event.GNSS)}
	g.registerSubscriber()
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

// MonitorGNSSEventsWithUblox ... monitor GNSS events with ublox
func (g *GPSD) MonitorGNSSEventsWithUblox() {
	//done := make(chan struct{}) // Done setting up logging.  Go ahead and wait for process
	// var currentNavStatus int64
	processCfg := g.processConfig

	//currentNavStatus = 0
	//var ublx *ublox.UBlox
	g.state = event.PTP_FREERUN
retry:
	if ublx, err := ublox.NewUblox(); err != nil {
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
		nOffset := int64(99999999)
		missedTickers := 0

		for {
			select {
			case <-ticker.C:

				emptyCount := 0

				for {
					//UbloxPollInit only initializes if not running
					ublx.UbloxPollInit()
					output := ublx.UbloxPollPull()
					if strings.Contains(output, "UBX-NAV-CLOCK") {
						nextLine := ublx.UbloxPollPull()
						//parse
						nOffset = ublox.ExtractOffset(nextLine)
						emptyCount = 0
						missedTickers = 0
					} else if strings.Contains(output, "UBX-NAV-STATUS") {
						nextLine := ublx.UbloxPollPull()
						//parse
						nStatus = ublox.ExtractNavStatus(nextLine)
						emptyCount = 0
						missedTickers = 0
					} else if len(output) == 0 {
						emptyCount++
					}
					if emptyCount >= 10 {
						missedTickers++
						if missedTickers > 3 {
							ublx.UbloxPollReset()
							missedTickers = 0
						}
						break
					}

				}

				//glog.Infof("MonitorGNSSEventsWithUblox nStatus=%d ; nOffset=%d", nStatus, nOffset)

				g.offset = nOffset
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
					Values: map[event.ValueType]interface{}{
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
					glog.Error("failed to send gnss terminated event to eventHandler")
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

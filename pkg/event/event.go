package event

import (
	"net"
	"strconv"
	"strings"

	"github.com/openshift/linuxptp-daemon/pkg/pmc"

	"fmt"
	"time"

	"github.com/golang/glog"
)

type ClockType string

const (
	GM                = "GM"
	BC                = "BC"
	OC                = "OC"
	PTP4lProcessName  = "ptp4l"
	TS2PHCProcessName = "ts2phc"
)

// EventType ...
type EventType string

const (
	// CLOCK_CLASS_CHANGE ...
	CLOCK_CLASS_CHANGE = "CLOCK_CLASS_CHANGE"
	// PTP_FREERUN ...
	PTP_FREERUN = "PTP_FREERUN"
	// PTP_HOLDOVER ...
	PTP_HOLDOVER = "PTP_HOLDOVER"
	// PTP_LOCKED ...
	PTP_LOCKED = "PTP_LOCKED"
	// GNSS_STATUS ...
	GNSS_STATUS = "GNSS_STATUS"
	// GNSS FREERUN
	GNSS_FREERUN = "GNSS_FREERUN"
	// GNSS LOCKED
	GNSS_LOCKED             = "GNSS_LOCKED"
	connectionRetryInterval = 1 * time.Second
)

// EventHandler ... event handler to process events
type EventHandler struct {
	stdOutSocket   string
	stdoutToSocket bool
	processChannel <-chan EventChannel
	closeCh        chan bool
}

// EventChannel .. event channel to subscriber to events
type EventChannel struct {
	ProcessName string
	Type        EventType
	CfgName     string
	Value       int64
	ClockType   ClockType
	Update      bool
}

var (
	mockTest bool = false
)

func (e *EventHandler) MockEnable() {
	mockTest = true
}

// Init ... initialize event manager
func Init(stdOutToSocket bool, socketName string, processChannel chan EventChannel, closeCh chan bool) *EventHandler {
	ptpEvent := &EventHandler{
		stdOutSocket:   socketName,
		stdoutToSocket: stdOutToSocket,
		closeCh:        closeCh,
		processChannel: processChannel,
	}
	return ptpEvent

}

// ProcessEvents ... process events to generate new events
func (e *EventHandler) ProcessEvents() {
	var c net.Conn
	var err error
	defer func() {
		if e.stdoutToSocket && c != nil {
			if err = c.Close(); err != nil {
				glog.Errorf("closing connection returned error %s", err)
			}
		}
	}()
	go func() {
	connect:
		select {
		case <-e.closeCh:
			return
		default:
			if e.stdoutToSocket {
				c, err = net.Dial("unix", e.stdOutSocket)
				if err != nil {
					glog.Errorf("event process error trying to connect to event socket %s", err)
					time.Sleep(connectionRetryInterval)
					goto connect
				}
			}
		}
		glog.Info("Starting event monitoring...")
		writeEventToLog := false
		var logOut string
		for {
			select {
			case event := <-e.processChannel:
				if event.Type == PTP_FREERUN ||
					event.Type == PTP_HOLDOVER ||
					event.Type == PTP_LOCKED {
					// call pmc command and change
					glog.Infof("EVENT: received ptp clock status change request %s", event.Type)
					e.updateCLockClass(event.CfgName, event.Type, event.ClockType)
					writeEventToLog = false
				} else if event.Type == GNSS_STATUS && event.Update == true {
					glog.Infof("EVENT: received GNSS status change request %s", event.Type)
					if event.Value < 3 {
						e.updateCLockClass(event.CfgName, GNSS_FREERUN, event.ClockType)
					} else {
						e.updateCLockClass(event.CfgName, GNSS_LOCKED, event.ClockType)
					}
					writeEventToLog = true
				}
				logOut = fmt.Sprintf("%s[%d]:[%s] %s %d\n", event.ProcessName,
					time.Now().Unix(), event.CfgName, event.Type, event.Value)
				if writeEventToLog {
					if e.stdoutToSocket {
						_, err := c.Write([]byte(logOut))
						if err != nil {
							glog.Errorf("Write error %s:", err)
							goto connect
						}
					} else {
						fmt.Printf("%s", logOut)
					}
				}
			case <-e.closeCh:
				return
			}
		}
		return
	}()
}

func (e *EventHandler) updateCLockClass(cfgName string, eventType EventType, clockType ClockType) {
	switch eventType {
	case PTP_LOCKED, GNSS_LOCKED:
		switch clockType {
		case GM:
			changeClockType(cfgName, pmc.CmdUpdateGMClass_LOCKED, 6)
		case OC:
		case BC:
		}
	case PTP_FREERUN, GNSS_FREERUN:
		switch clockType {
		case GM:
			changeClockType(cfgName, pmc.CmdUpdateGMClass_FREERUN, 7)
		case OC:
		case BC:
		}
	case PTP_HOLDOVER:
		switch clockType {
		case GM:
			changeClockType(cfgName, pmc.CmdUpdateGMClass_LOCKED, 248)
		case OC:
		case BC:
		}
	default:

	}

}

func changeClockType(cfgName, cmd string, value int64) {
	if mockTest {
		return
	}
	cfgName = strings.Replace(cfgName, TS2PHCProcessName, PTP4lProcessName, 1)
	currentClockClass := getCurrentClockClass(cfgName)
	if currentClockClass != value {
		if _, _, e := pmc.RunPMCExp(cfgName, fmt.Sprintf(cmd, value), pmc.ClockClassUpdateRegEx); e != nil {
			glog.Infof("updating clock class based on antenna status failed %s", e)
		} else {
			glog.Errorf("updating clock class based on antenna status to %d", value)
		}
	} else {
		glog.Infof("current clock class is set to %d", currentClockClass)
	}
}

func getCurrentClockClass(cfgName string) int64 {
	if mockTest {
		return 248
	}
	cfgName = strings.Replace(cfgName, TS2PHCProcessName, PTP4lProcessName, 1)
	if _, matches, e := pmc.RunPMCExp(cfgName, pmc.CmdParentDataSet, pmc.ClockClassChangeRegEx); e == nil {
		//regex: 'gm.ClockClass[[:space:]]+(\d+)'
		//match  1: 'gm.ClockClass                         135'
		//match  2: '135'
		if len(matches) > 1 {
			var parseError error
			var clockClass int64
			if clockClass, parseError = strconv.ParseInt(matches[1], 10, 32); parseError == nil {
				return clockClass
			}
		}
	}
	return 0
}

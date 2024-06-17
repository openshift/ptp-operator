package event

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/pmc"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/protocol"

	fbprotocol "github.com/facebook/time/ptp/protocol"
	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
)

type ValueType string

const (
	PTPNamespace = "openshift"
	PTPSubsystem = "ptp"
)
const (
	OFFSET     ValueType = "offset"
	STATE      ValueType = "state"
	GPS_STATUS ValueType = "gnss_status"
	//Status           ValueType = "status"
	PHASE_STATUS         ValueType = "phase_status"
	FREQUENCY_STATUS     ValueType = "frequency_status"
	NMEA_STATUS          ValueType = "nmea_status"
	PPS_STATUS           ValueType = "pps_status"
	GM_INTERFACE_UNKNOWN string    = "unknown"
)

var valueTypeHelpTxt = map[ValueType]string{
	OFFSET:           "0 = FREERUN, 1 = LOCKED, 2 = HOLDOVER",
	GPS_STATUS:       "0=NOFIX, 1=Dead Reckoning Only, 2=2D-FIX, 3=3D-FIX, 4=GPS+dead reckoning fix, 5=Time only fix",
	PHASE_STATUS:     "-1=UNKNOWN, 0=INVALID, 1=FREERUN, 2=LOCKED, 3=LOCKED_HO_ACQ, 4=HOLDOVER",
	FREQUENCY_STATUS: "-1=UNKNOWN, 0=INVALID, 1=FREERUN, 2=LOCKED, 3=LOCKED_HO_ACQ, 4=HOLDOVER",
	NMEA_STATUS:      "0 = UNAVAILABLE, 1 = AVAILABLE",
	PPS_STATUS:       "0 = UNAVAILABLE, 1 = AVAILABLE",
}

// ClockType ...
type ClockType string

// ClockClassRequest ...
type ClockClassRequest struct {
	cfgName    string
	gmState    PTPState
	clockType  ClockType
	clockClass fbprotocol.ClockClass
}

var (
	//  make sure only one clock class update is tried if it fails next  try will pass
	// this will also stop flooding
	clockClassRequestCh = make(chan ClockClassRequest, 1)

	PMCGMGetter = func(cfgName string) (protocol.GrandmasterSettings, error) {
		cfgName = strings.Replace(cfgName, TS2PHCProcessName, PTP4lProcessName, 1)
		return pmc.RunPMCExpGetGMSettings(cfgName)
	}
	PMCGMSetter = func(cfgName string, g protocol.GrandmasterSettings) error {
		cfgName = strings.Replace(cfgName, TS2PHCProcessName, PTP4lProcessName, 1)
		err := pmc.RunPMCExpSetGMSettings(cfgName, g)
		if err != nil {
			return fmt.Errorf("failed to update GRANDMASTER_SETTINGS_NP: %s", err)
		}
		return nil
	}
)

const (
	// GM ..
	GM ClockType = "GM"
	// BC ...
	BC ClockType = "BC"
	// OC ...
	OC ClockType = "OC"
)

// PTP4lProcessName ...
const PTP4lProcessName = "ptp4l"

// TS2PHCProcessName ...
const TS2PHCProcessName = "ts2phc"

// EventSource ...
type EventSource string

const (
	GNSS       EventSource = "gnss"
	DPLL       EventSource = "dpll"
	TS2PHC     EventSource = "ts2phc"
	PTP4l      EventSource = "ptp4l"
	PHC2SYS    EventSource = "phc2sys"
	PPS        EventSource = "1pps"
	SYNCE      EventSource = "syncE"
	MONITORING EventSource = "monitoring"
)

// PTPState ...
type PTPState string

const (

	// PTP_FREERUN ...
	PTP_FREERUN PTPState = "s0"
	// PTP_HOLDOVER ...
	PTP_HOLDOVER PTPState = "s1"
	// PTP_LOCKED ...
	PTP_LOCKED PTPState = "s2"
	// PTP_UNKNOWN
	PTP_UNKNOWN PTPState = "-1"
	// PTP_NOTSET
	PTP_NOTSET PTPState = "-2"
)

const connectionRetryInterval = 1 * time.Second

type grandMasterSyncState struct {
	state          PTPState
	clockClass     fbprotocol.ClockClass
	sourceLost     bool
	gmLog          string
	lastLoggedTime int64
	gmIFace        string
}

// EventHandler ... event handler to process events
type EventHandler struct {
	sync.Mutex
	nodeName         string
	stdoutSocket     string
	stdoutToSocket   bool
	processChannel   <-chan EventChannel
	closeCh          chan bool
	data             map[string][]*Data
	offsetMetric     *prometheus.GaugeVec
	clockMetric      *prometheus.GaugeVec
	clockClassMetric *prometheus.GaugeVec
	clockClass       fbprotocol.ClockClass
	gmSyncState      map[string]*grandMasterSyncState
	outOfSpec        bool // is offset out of spec, used for Lost Source,In Spec and OPut of Spec state transitions
	ReduceLog        bool // reduce logs for every announce
}

// EventChannel .. event channel to subscriber to events
type EventChannel struct {
	ProcessName EventSource               // ptp4l, gnss etc
	State       PTPState                  // PTP locked etc
	IFace       string                    // Interface that is causing the event
	CfgName     string                    // ptp config profile name
	Values      map[ValueType]interface{} // either offset or status , 3 information  offset , phase state and frequency state
	ClockType   ClockType                 // oc bc gm
	Time        int64                     // time.Unix.Now()
	OutOfSpec   bool                      // out of Spec for offset
	WriteToLog  bool                      // send to log in predefined format %s[%d]:[%s] %s %d
	Reset       bool                      // reset data on ptp deletes or process died
	SourceLost  bool
}

var (
	mockTest        bool = false
	StateRegisterer *StateNotifier
)

// MockEnable ...
func (e *EventHandler) MockEnable() {
	mockTest = true
}

// Init ... initialize event manager
func Init(nodeName string, stdOutToSocket bool, socketName string, processChannel chan EventChannel, closeCh chan bool,
	offsetMetric *prometheus.GaugeVec, clockMetric *prometheus.GaugeVec, clockClassMetric *prometheus.GaugeVec) *EventHandler {
	ptpEvent := &EventHandler{
		nodeName:         nodeName,
		stdoutSocket:     socketName,
		stdoutToSocket:   stdOutToSocket,
		closeCh:          closeCh,
		processChannel:   processChannel,
		data:             map[string][]*Data{},
		clockMetric:      clockMetric,
		offsetMetric:     offsetMetric,
		clockClassMetric: clockClassMetric,
		clockClass:       protocol.ClockClassUninitialized,
		gmSyncState:      map[string]*grandMasterSyncState{},
		outOfSpec:        false,
		ReduceLog:        true,
	}
	if clockClassMetric != nil {
		clockClassMetric.With(prometheus.Labels{
			"process": PTP4lProcessName, "node": nodeName}).Set(248)
	}
	StateRegisterer = NewStateNotifier()
	return ptpEvent

}

func (e *EventChannel) GetLogData() string {
	logData := make([]string, 0, len(e.Values))
	for k, v := range e.Values {
		switch val := v.(type) {
		case int64, int, int32:
			logData = append(logData, fmt.Sprintf("%s %d", k, val))
		case float64:
			logData = append(logData, fmt.Sprintf("%s %f", k, val))
		case string:
			logData = append(logData, fmt.Sprintf("%s %s", k, val))
		default:
			continue //ignore string for metrics
		}
	}
	sort.Strings(logData)
	return fmt.Sprintf("%s[%d]:[%s] %s %s %s\n", e.ProcessName,
		time.Now().Unix(), e.CfgName, e.IFace, strings.Join(logData, " "), e.State)
}

// getGMState ... get lowest state of all the interfaces
/*
GNSS State + DPLL State= DPLL State
DPLL STate + Ts2phc State =GM State
----------------------------------------------------------------
GNSS| Mode              | Offset   | State
1.  | 0-2(Source LOST)  | in Range | FREERUN
2.  | 0-2(Source LOST ) | out Range| FREERUN
3.  | 3                 | in Range | LOCKED
4.  | 3                 | out Range| FREERUN
----------------------------------------------------------------
DPLL | Frequency/Phase  	|  Offset  | GNSS STATE |  DPLL PTP STATE
------------------------------------------------------------------
1.  | -1/1/0           	| in Range |  LOCKED    | FREERUN
2.  | -1/1/0           	| out Range|  FREERUN   | FREERUN
-----------------------------------------------------------------
3.  |  2 (LOCKED)       	| in Range |  LOCKED      | LOCKED
4.  |  2 (LOCKED)       	| in Range |  FREERUN     | LOCKED
-----------------------------------------------------------------
SL :-> Source Lost
------------------------------------------------------------------------------------------
DPLL| Frequency/Phase      | Offset      | GNSS STATE               | DPLL PTP State
------------------------------------------------------------------------------------------
5   | 2 (LOCKED)           | Out Range   | All State                | FREERUN
6.  | 3 (LOCK_ACQ_HOLDOVER)| In Range    | LOCKED                   | LOCKED
7.  | 3 (LOCK_ACQ_HOLDOVER)| In/Out Range| FREERUN (SL)             | FREERUN
8.  | 3 (LOCK_ACQ_HOLDOVER)| Out Range   | LOCKED                   | FREERUN
*9. | 3 (LOCK_ACQ_HOLDOVER)| In/Out Range| FREERUN (SL)             | HOLDOVER
------------------------------------------------------------------------------------------
*10.| 4 (HOLDOVER)		| IN/Out Range   | FREERUN (SL)	            | HOLDOVER
*11.| 4 (HOLDOVER)		| in/Out Range   | FREERUN (SL)             | AFTER TIME OUT
                                                                    FREERUN OUT OF SPEC

12. | 4 (HOLDOVER)		| in Range	     | LOCKED                   | LOCKED
13. | 4 (HOLDOVER)		| Out Range	     | LOCKED                   | FREERUN
14. | 4 (HOLDOVER)		| in Range       | FREERUN (SL)             | LOCKED
15. | 4 (HOLDOVER)		| Out Range      | FREERUN (SL)             | FREERUN
------------------------------------------------------------------------------------------
FINAL GM STATE  *SL = Source Lost
---------------------------------------------------------------------------------------------
| DPLL PTP State        | GNSS PTP STATE    | TS2PHC PTP STATE | GM STATE  | Clock Class
---------------------------------------------------------------------------------------------
| FREERUN               | NA                | NA                | FREERUN  | 248
| HOLDOVER IN SPEC      | NA                | NA                | HOLDOVER | 7
| FREERUN OUT OF SPEC   | NA                | NA                | FREERUN  | 140
| LOCKED                | LOCKED            | LOCKED            | LOCKED   | 6
| LOCKED                | LOCKED            | FREERUN           | FREERUN  | 248
| LOCKED                | *FREERUN (SL)     | LOCKED            | NA       | Wait for DPLL
                                                                           | to move to HOLDOVER

| LOCKED                | *FREERUN (SL)     | FREERUN           | NA       | Wait for DPLL
                                                                           |to move to HOLDOVER

| LOCKED                | *FREERUN(offset)  | LOCKED            | FREERUN  | 248
| LOCKED                | *FREERUN(offset)  | FREERUN           | FREERUN  | 248
 Final GM State When DPLL not available
---------------------------------------------------------------------------------------------
DPLL PTP State |  GNSS PTP STATE  |	TS2PHC PTP STATE| GM STATE | Clock Class
---------------------------------------------------------------------------------------------
| NA           |  FREERUN         |	LOCKED          | FREERUN  | 248
| NA           |  FREERUN         |	FREERUN         | FREERUN  | 248
| NA           |  LOCKED          |	FREERUN         | FREERUN  | 248
| NA           |  LOCKED          |	LOCKED          | LOCKED   | 6

*/
func (e *EventHandler) updateGMState(cfgName string) grandMasterSyncState {
	dpllState := PTP_NOTSET
	gnssState := PTP_FREERUN
	ts2phcState := PTP_FREERUN
	gnssSrcLost := e.isSourceLost(cfgName)
	gmInterface := e.getGNSSInterface(cfgName)

	if gmInterface == GM_INTERFACE_UNKNOWN {
		glog.Infof("GM-GNSS interface is not yet identified, gm state reporting delayed.")
		return grandMasterSyncState{gmIFace: gmInterface}
	}

	if _, ok := e.gmSyncState[cfgName]; !ok {
		e.gmSyncState[cfgName] = &grandMasterSyncState{
			state:      PTP_FREERUN,
			clockClass: protocol.ClockClassUninitialized,
			sourceLost: gnssSrcLost,
			gmIFace:    gmInterface,
		}
	}
	// right now if GPS offset || mode is bad then consider source lost
	e.gmSyncState[cfgName].sourceLost = gnssSrcLost
	e.gmSyncState[cfgName].gmIFace = gmInterface
	if data, ok := e.data[cfgName]; ok {
		for _, d := range data {
			switch d.ProcessName {
			case DPLL:
				dpllState = d.State
			case GNSS:
				gnssState = d.State
				// expecting to have atleast one enterfae
			case TS2PHCProcessName:
				ts2phcState = d.State
			}
		}
	} else {
		e.gmSyncState[cfgName].state = PTP_FREERUN
		e.gmSyncState[cfgName].clockClass = 248
		e.gmSyncState[cfgName].lastLoggedTime = time.Now().Unix()
		e.gmSyncState[cfgName].gmIFace = gmInterface
		e.gmSyncState[cfgName].gmLog = fmt.Sprintf("%s[%d]:[%s] %s T-GM-STATUS %s\n", GM, e.gmSyncState[cfgName].lastLoggedTime, cfgName, gmInterface, e.gmSyncState[cfgName].state)
		return *e.gmSyncState[cfgName]
	}
	e.gmSyncState[cfgName].gmIFace = gmInterface
	switch dpllState {
	case PTP_FREERUN:
		e.gmSyncState[cfgName].state = dpllState
		if e.outOfSpec {
			// T-GM in holdover, out of holdover specification
			e.gmSyncState[cfgName].clockClass = protocol.ClockClassOutOfSpec
		} else { // from holdover it goes to out of spec to free run
			// T-GM or T-BC in free-run mode
			e.gmSyncState[cfgName].clockClass = protocol.ClockClassFreerun
		}
	case PTP_HOLDOVER:
		e.gmSyncState[cfgName].state = dpllState
		// T-GM in holdover, within holdover specification
		e.gmSyncState[cfgName].clockClass = fbprotocol.ClockClass7
	case PTP_LOCKED, PTP_NOTSET: // consider DPLL is locked if DPLL is not available
		switch gnssState {
		case PTP_LOCKED:
			switch ts2phcState {
			case PTP_FREERUN:
				e.gmSyncState[cfgName].state = PTP_FREERUN
				// T-GM or T-BC in free-run mode
				e.gmSyncState[cfgName].clockClass = protocol.ClockClassFreerun
			case PTP_LOCKED:
				e.gmSyncState[cfgName].state = PTP_LOCKED
				// T-GM connected to a PRTC in locked mode (e.g., PRTC traceable to GNSS)
				e.gmSyncState[cfgName].clockClass = fbprotocol.ClockClass6
			}
		case PTP_FREERUN:
			if gnssSrcLost {
				switch ts2phcState {
				case PTP_LOCKED, PTP_FREERUN:
					// stay with last GM state and wait for DPLL to move to HOLDOVER
				}
			} else {
				switch ts2phcState {
				case PTP_FREERUN, PTP_LOCKED, PTP_UNKNOWN, PTP_NOTSET:
					e.gmSyncState[cfgName].state = PTP_FREERUN
					// T-GM or T-BC in free-run mode
					e.gmSyncState[cfgName].clockClass = protocol.ClockClassFreerun
				}
			}
		}
	default:
		switch gnssState {
		case PTP_LOCKED:
			switch ts2phcState {
			case PTP_FREERUN, PTP_UNKNOWN, PTP_NOTSET:
				e.gmSyncState[cfgName].state = PTP_FREERUN
				// T-GM or T-BC in free-run mode
				e.gmSyncState[cfgName].clockClass = protocol.ClockClassFreerun
			case PTP_LOCKED:
				e.gmSyncState[cfgName].state = PTP_LOCKED
				// T-GM connected to a PRTC in locked mode (e.g., PRTC traceable to GNSS)
				e.gmSyncState[cfgName].clockClass = fbprotocol.ClockClass6
			}
		case PTP_FREERUN:
			switch ts2phcState {
			case PTP_FREERUN, PTP_LOCKED:
				e.gmSyncState[cfgName].state = PTP_FREERUN
				e.gmSyncState[cfgName].clockClass = protocol.ClockClassFreerun
			}
		default:
			e.gmSyncState[cfgName].state = ts2phcState
			switch ts2phcState {
			case PTP_FREERUN:
				e.gmSyncState[cfgName].clockClass = protocol.ClockClassFreerun
			case PTP_LOCKED:
				e.gmSyncState[cfgName].clockClass = fbprotocol.ClockClass7
			}
		}
	}
	gSycState := e.gmSyncState[cfgName]
	rGrandMasterSyncState := grandMasterSyncState{
		state:      gSycState.state,
		clockClass: gSycState.clockClass,
		sourceLost: gSycState.sourceLost,
		gmIFace:    gSycState.gmIFace,
	}
	// this will reduce log noise and prints 1 per sec
	logTime := time.Now().Unix()
	if e.gmSyncState[cfgName].lastLoggedTime != logTime {
		gmLog := fmt.Sprintf("%s[%d]:[%s] %s T-GM-STATUS %s\n", GM, logTime, cfgName, gSycState.gmIFace, gSycState.state)
		e.gmSyncState[cfgName].lastLoggedTime = logTime
		e.gmSyncState[cfgName].gmLog = gmLog
		rGrandMasterSyncState.gmLog = gmLog
		glog.Infof("dpll State %s, gnss State %s, tsphc state %s, gm state %s,", dpllState, gnssState, ts2phcState, e.gmSyncState[cfgName].state)
	}
	return rGrandMasterSyncState
}

func (e *EventHandler) getGMState(cfgName string) grandMasterSyncState {
	if g, ok := e.gmSyncState[cfgName]; ok {
		return *g
	}
	return grandMasterSyncState{
		state:      PTP_NOTSET,
		clockClass: 0,
		sourceLost: false,
	}
}

func (e *EventHandler) getGMClockClass(cfgName string) fbprotocol.ClockClass {
	if g, ok := e.gmSyncState[cfgName]; ok {
		return g.clockClass
	}
	return protocol.ClockClassUninitialized
}

func (e *EventHandler) isSourceLost(cfgName string) bool {
	if data, ok := e.data[cfgName]; ok {
		for _, d := range data {
			if d.ProcessName == GNSS && len(d.Details) > 0 && d.Details[0] != nil {
				return d.Details[0].sourceLost
			}
		}
	}
	return false
}
func (e *EventHandler) getGNSSInterface(cfgName string) string {
	if data, ok := e.data[cfgName]; ok {
		for _, d := range data {
			if d.ProcessName == GNSS && len(d.Details) > 0 {
				return d.Details[0].IFace
			} else if d.ProcessName == TS2PHCProcessName && len(d.Details) > 0 {
				for _, dd := range d.Details {
					if dd.signalSource == GNSS {
						return dd.IFace
					}
				}
			}
		}
	}
	return GM_INTERFACE_UNKNOWN
}

func (e *EventHandler) updateSpecState(event EventChannel) {
	// update if DPLL holdover is out of spec
	if event.ProcessName == DPLL {
		e.outOfSpec = event.OutOfSpec
	}
}
func (e *EventHandler) toString() string {
	// update if DPLL holdover is out of spec
	out := strings.Builder{}
	for cfgName, eData := range e.data {
		out.WriteString("  data key : " + string(cfgName) + "\r\n")
		for _, data := range eData {
			out.WriteString("  state: " + string(data.State) + "\r\n")
			out.WriteString("  process name: " + string(data.ProcessName) + "\r\n")
			for _, dataDetails := range data.Details {
				for mn, mv := range dataDetails.Metrics {
					out.WriteString("  metric key: " + string(mn) + "\r\n")
					out.WriteString("  metric Name: " + mv.Name + "\r\n")
					out.WriteString("  registered: " + strconv.FormatBool(mv.isRegistered) + "\r\n")
				}
				out.WriteString("  signal source: " + string(dataDetails.signalSource) + "\r\n")
				out.WriteString("  details state: " + string(dataDetails.State) + "\r\n")
				out.WriteString("  log: " + string(dataDetails.logData) + "\r\n")
				out.WriteString("  iface: " + string(dataDetails.IFace) + "\r\n")
				out.WriteString("  source lost : " + strconv.FormatBool(dataDetails.sourceLost) + "\r\n")
			}
			out.WriteString("-----\r\n")
		}
	}
	return out.String()
}

func (e *EventHandler) hasMetric(name string) (*prometheus.GaugeVec, bool) {
	// update if DPLL holdover is out of spec
	for _, eData := range e.data {
		for _, data := range eData {
			for _, dataDetails := range data.Details {
				for _, mv := range dataDetails.Metrics {
					if mv.Name == name {
						return mv.GaugeMetric, true
					}
				}
			}
		}
	}
	return nil, false
}

// ProcessEvents ... process events to generate new events
func (e *EventHandler) ProcessEvents() {
	var c net.Conn
	var err error
	redialClockClass := true
	retryCount := 0
	defer func() {
		if e.stdoutToSocket && c != nil {
			if err = c.Close(); err != nil {
				glog.Errorf("closing connection returned error %s", err)
			}
		}
	}()

connect:
	select {
	case <-e.closeCh:
		return
	default:
		if e.stdoutToSocket {
			c, err = net.Dial("unix", e.stdoutSocket)
			if err != nil {
				// reduce log spam
				if retryCount == 0 || retryCount%5 == 0 {
					glog.Errorf("waiting for event socket, retrying %s", err)
				}
				retryCount = (retryCount + 1) % 6
				time.Sleep(connectionRetryInterval)
				goto connect
			}
			retryCount = 0
		}
	}

	if redialClockClass {
		go func(eConn *net.Conn) {
			defer func() {
				if err := recover(); err != nil {
					glog.Errorf("restored from clock class update: %s", err)
				}
			}()
			for {
				select {
				case clk := <-clockClassRequestCh:
					e.UpdateClockClass(c, clk)
				case <-e.closeCh:
					return
				}
			}
		}(&c)
		redialClockClass = false
	}
	// call all monitoring candidates; verify every 5 secs for any new
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for {
			select {
			case <-e.closeCh:
				return
			case <-ticker.C:
				StateRegisterer.monitor()
			}
		}
	}()

	glog.Info("starting state monitoring...")
	for {
		select {
		case event := <-e.processChannel: // for non GM this thread will be in sleep forever
			// ts2phc[123455]:[ts2phc.0.config] 12345 s0 offset/gps
			// replace ts2phc logs here
			if event.Reset { // clean up
				if event.ProcessName == TS2PHC {
					e.unregisterMetrics(event.CfgName, "")
					delete(e.data, event.CfgName) // this will delete all index
					e.clockClass = protocol.ClockClassUninitialized

				} else {
					// Check if the index is within the slice bounds
					for indexToRemove, d := range e.data[event.CfgName] {
						if d.ProcessName == event.ProcessName {
							e.unregisterMetrics(event.CfgName, string(event.ProcessName))
							if indexToRemove < len(e.data[event.CfgName]) {
								e.data[event.CfgName] = append(e.data[event.CfgName][:indexToRemove], e.data[event.CfgName][indexToRemove+1:]...)
							}
						}
					}
					delete(e.gmSyncState, event.CfgName) // delete the gmSyncState
					e.outOfSpec = false
				}
				continue
			}
			var logOut []string
			logDataValues := ""

			// Update the in MemData
			dataDetails := e.addEvent(event)
			logDataValues = dataDetails.logData

			if event.WriteToLog && logDataValues != "" {
				logOut = append(logOut, logDataValues)
			}
			// Computes GM state
			gmState := e.updateGMState(event.CfgName)

			if gmState.gmLog != "" && gmState.gmIFace != GM_INTERFACE_UNKNOWN {
				logOut = append(logOut, gmState.gmLog)
			}

			// Update the metrics
			if !e.stdoutToSocket { // if events not enabled
				eventIface := event.IFace
				if eventIface != "" {
					r := []rune(eventIface)
					eventIface = string(r[:len(r)-1]) + "x"
				}
				e.UpdateClockStateMetrics(event.State, string(event.ProcessName), eventIface)
				//  update all metric that was sent to events
				e.updateMetrics(event.CfgName, event.ProcessName, event.Values, dataDetails)
				if gmState.gmIFace != GM_INTERFACE_UNKNOWN { // race condition ;
					gmIface := gmState.gmIFace
					if gmIface != "" {
						r := []rune(gmIface)
						gmIface = string(r[:len(r)-1]) + "x"
					}
					e.UpdateClockStateMetrics(gmState.state, string(GM), gmIface)
				}
			}

			if uint8(gmState.clockClass) != uint8(e.clockClass) {
				glog.Infof("clock class change request from %d to %d", uint8(e.clockClass), uint8(gmState.clockClass))
				go func() {
					select {
					case clockClassRequestCh <- ClockClassRequest{
						cfgName:    event.CfgName,
						gmState:    gmState.state,
						clockType:  event.ClockType,
						clockClass: gmState.clockClass,
					}:
					default:
						glog.Error("clock class request busy updating previous request, will try next event")
					}
				}()
			}

			if len(logOut) > 0 {
				if e.stdoutToSocket {
					for _, l := range logOut {
						fmt.Printf("%s", l)
						_, err = c.Write([]byte(l))
						if err != nil {
							glog.Errorf("Write %s error %s:", l, err)
							goto connect
						}
					}
				} else {
					for _, l := range logOut {
						fmt.Printf("%s", l)
					}
				}
			}
		case <-e.closeCh:
			return
		}
	}
}

func (e *EventHandler) updateCLockClass(cfgName string, clkClass fbprotocol.ClockClass, clockType ClockType,
	gmGetterFn func(string) (protocol.GrandmasterSettings, error),
	gmSetterFn func(string, protocol.GrandmasterSettings) error) (err error, clockClass fbprotocol.ClockClass) {
	g, err := gmGetterFn(cfgName)
	if err != nil {
		glog.Errorf("failed to get current GRANDMASTER_SETTINGS_NP: %s", err)
		return err, clockClass
	}
	switch clockType {
	case GM:
		switch clkClass {
		case fbprotocol.ClockClass6: // T-GM connected to a PRTC in locked mode (e.g., PRTC traceable to GNSS)
			// update only when ClockClass is changed
			if g.ClockQuality.ClockClass != fbprotocol.ClockClass6 {
				g.ClockQuality.ClockClass = fbprotocol.ClockClass6
				g.ClockQuality.ClockAccuracy = fbprotocol.ClockAccuracyNanosecond100
				g.TimePropertiesDS.TimeTraceable = true
				g.TimePropertiesDS.FrequencyTraceable = true
				g.TimePropertiesDS.CurrentUtcOffsetValid = true
				g.TimePropertiesDS.TimeSource = fbprotocol.TimeSourceGNSS
				// T-REC-G.8275.1-202211-I section 6.3.5
				g.ClockQuality.OffsetScaledLogVariance = 0x4e5d
				err = gmSetterFn(cfgName, g)
			}
		case protocol.ClockClassOutOfSpec: // GM out of holdover specification, traceable to Category 3
			if g.ClockQuality.ClockClass != protocol.ClockClassOutOfSpec {
				g.ClockQuality.ClockClass = protocol.ClockClassOutOfSpec
				g.ClockQuality.ClockAccuracy = fbprotocol.ClockAccuracyUnknown
				g.TimePropertiesDS.TimeSource = fbprotocol.TimeSourceGNSS
				g.TimePropertiesDS.TimeTraceable = false
				g.TimePropertiesDS.FrequencyTraceable = false
				g.TimePropertiesDS.CurrentUtcOffsetValid = false
				// T-REC-G.8275.1-202211-I section 6.3.5
				g.ClockQuality.OffsetScaledLogVariance = 0xffff
				err = gmSetterFn(cfgName, g)
			}
		case fbprotocol.ClockClass7: // T-GM in holdover, within holdover specification
			if g.ClockQuality.ClockClass != fbprotocol.ClockClass7 {
				g.ClockQuality.ClockClass = fbprotocol.ClockClass7
				g.ClockQuality.ClockAccuracy = fbprotocol.ClockAccuracyUnknown
				g.TimePropertiesDS.TimeSource = fbprotocol.TimeSourceGNSS
				g.TimePropertiesDS.TimeTraceable = true
				g.TimePropertiesDS.FrequencyTraceable = true
				g.TimePropertiesDS.CurrentUtcOffsetValid = true
				// T-REC-G.8275.1-202211-I section 6.3.5
				g.ClockQuality.OffsetScaledLogVariance = 0xffff
				err = gmSetterFn(cfgName, g)
			}
		case protocol.ClockClassFreerun: // T-GM or T-BC in free-run mode
			if g.ClockQuality.ClockClass != protocol.ClockClassFreerun {
				g.ClockQuality.ClockClass = protocol.ClockClassFreerun
				g.ClockQuality.ClockAccuracy = fbprotocol.ClockAccuracyUnknown
				g.TimePropertiesDS.TimeSource = fbprotocol.TimeSourceGNSS
				g.TimePropertiesDS.TimeTraceable = false
				g.TimePropertiesDS.FrequencyTraceable = false
				g.TimePropertiesDS.CurrentUtcOffsetValid = false
				// T-REC-G.8275.1-202211-I section 6.3.5
				g.ClockQuality.OffsetScaledLogVariance = 0xffff
				err = gmSetterFn(cfgName, g)
			}
		default:
			glog.Infof("No clock class identified for %d", clkClass)
		}
	default:
	}

	return err, g.ClockQuality.ClockClass
}

// GetPTPState ...
func (e *EventHandler) GetPTPState(source EventSource, cfgName string) PTPState {
	if m, ok := e.data[cfgName]; ok {
		for _, v := range m {
			if v.ProcessName == source {
				return v.State
			}
		}
	}
	return PTP_UNKNOWN
}

// UpdateClockStateMetrics ...
func (e *EventHandler) UpdateClockStateMetrics(state PTPState, process, iFace string) {
	labels := prometheus.Labels{}
	labels = prometheus.Labels{
		"process": process, "node": e.nodeName, "iface": iFace}
	if state == PTP_LOCKED {
		e.clockMetric.With(labels).Set(1)
	} else if state == PTP_FREERUN {
		e.clockMetric.With(labels).Set(0)
	} else if state == PTP_HOLDOVER {
		e.clockMetric.With(labels).Set(2)
	} else {
		e.clockMetric.With(labels).Set(3)
	}
}

func (e *EventHandler) updateMetrics(cfgName string, process EventSource, processData map[ValueType]interface{}, d *DataDetails) {
	iface := d.IFace
	if len(d.IFace) > 0 {
		r := []rune(iface)
		iface = string(r[:len(r)-1]) + "x"
	}

	for dataType, value := range processData { // update process with metrics
		var dataValue float64
		switch val := value.(type) {
		case int64:
			dataValue = float64(val)
		case float64:
			dataValue = val
		default:
			continue //ignore string for metrics
		}

		if _, found := d.Metrics[dataType]; !found {
			if dataType == OFFSET {
				pName := string(process)
				if process == TS2PHCProcessName {
					pName = "master"
				}
				if d.Metrics[dataType].GaugeMetric == nil {
					m := d.Metrics[dataType]
					m.GaugeMetric = e.offsetMetric
					m.isRegistered = true
					d.Metrics[dataType] = m
				}
				pLabels := map[string]string{"from": pName, "node": e.nodeName,
					"process": string(process), "iface": iface}
				d.Metrics[dataType].GaugeMetric.With(pLabels).Set(dataValue)
			} else {
				metric := DataMetric{
					isRegistered: true,
					GaugeMetric: prometheus.NewGaugeVec(
						prometheus.GaugeOpts{
							Namespace: PTPNamespace,
							Subsystem: PTPSubsystem,
							Name:      getMetricName(dataType),
							Help:      valueTypeHelpTxt[dataType],
						}, []string{"from", "node", "process", "iface"}),
					CounterMetric: nil,
					Name:          string(dataType),
					ValueType:     prometheus.GaugeValue,
					Labels: map[string]string{"from": string(process), "node": e.nodeName,
						"process": string(process), "iface": iface},
					Value: dataValue,
				}

				if gaugeMetric, ok := e.hasMetric(getMetricName(dataType)); ok {
					metric.GaugeMetric = gaugeMetric
				} else {
					glog.Infof("trying to register metrics %#v for %s", metric, dataType)
					registerMetrics(metric.GaugeMetric)
				}
				metric.GaugeMetric.With(metric.Labels).Set(dataValue)
				d.Metrics[dataType] = metric
			}
		} else {
			pName := string(process)
			if dataType == OFFSET && process == TS2PHCProcessName {
				pName = "master"
			}
			s := d.Metrics[dataType]
			s.Labels = map[string]string{"from": pName, "node": e.nodeName,
				"process": string(process), "iface": iface}
			s.Value = dataValue
			d.Metrics[dataType].GaugeMetric.With(s.Labels).Set(dataValue)
		}
	}

}

func registerMetrics(m *prometheus.GaugeVec) {
	defer func() {
		if err := recover(); err != nil {
			glog.Errorf("restored from registering metrics: %s", err)
		}
	}()
	prometheus.MustRegister(m)
}

func (e *EventHandler) unregisterMetrics(configName string, processName string) {
	if data, ok := e.data[configName]; ok {
		for _, v := range data {
			if string(v.ProcessName) == processName || processName == "" {
				for _, d := range v.Details {
					for _, metric := range d.Metrics {
						if metric.GaugeMetric != nil {
							metric.GaugeMetric.Delete(metric.Labels)
						}
					}
				}
			}
		}
	}
}

// GetData returns the queried Data and create one if not exist
func (e *EventHandler) GetData(cfgName string, processName EventSource) *Data {
	if e.data[cfgName] == nil {
		e.data[cfgName] = []*Data{}
	}

	for _, d := range e.data[cfgName] {
		if d.ProcessName == processName {
			return d
		}
	}

	d := &Data{
		ProcessName: processName,
		State:       PTP_UNKNOWN,
	}
	e.data[cfgName] = append(e.data[cfgName], d)
	return d
}

func (e *EventHandler) addEvent(event EventChannel) *DataDetails {
	d := e.GetData(event.CfgName, event.ProcessName)
	d.AddEvent(event)

	// update if DPLL holdover is out of spec
	e.updateSpecState(event)
	d.UpdateState()
	return d.GetDataDetails(event.IFace)
}

// UpdateClockClass ... update clock class
func (e *EventHandler) UpdateClockClass(c net.Conn, clk ClockClassRequest) {
	classErr, clockClass := e.updateCLockClass(clk.cfgName, clk.clockClass, clk.clockType,
		PMCGMGetter, PMCGMSetter)
	if classErr != nil {
		glog.Errorf("error updating clock class %s", classErr)
	} else {
		glog.Infof("updated clock class for last clock class %d to %d ", e.clockClass, clockClass)
		e.clockClass = clockClass
		clockClassOut := fmt.Sprintf("%s[%d]:[%s] CLOCK_CLASS_CHANGE %d\n", PTP4l, time.Now().Unix(), clk.cfgName, clockClass)
		if e.stdoutToSocket {
			if c != nil {
				_, err := c.Write([]byte(clockClassOut))
				if err != nil {
					glog.Errorf("failed to write class change event %s", err.Error())
				}
			} else {
				glog.Errorf("failed to write class change event, connection is nil")
			}
		} else {
			e.clockClassMetric.With(prometheus.Labels{
				"process": PTP4lProcessName, "node": e.nodeName}).Set(float64(clockClass))
		}
		fmt.Printf("%s", clockClassOut)
	}
}

func getMetricName(valueType ValueType) string {
	if strings.HasSuffix(string(valueType), string(OFFSET)) {
		return fmt.Sprintf("%s_%s", valueType, "ns")
	}
	return string(valueType)
}

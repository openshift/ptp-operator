package event_test

import (
	"bufio"
	fbprotocol "github.com/facebook/time/ptp/protocol"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/protocol"
	"github.com/stretchr/testify/assert"
	"log"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang/glog"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/event"
)

var (
	staleSocketTimeout = 100 * time.Millisecond
)

func monkeyPatch() {

	event.PMCGMGetter = func(cfgName string) (protocol.GrandmasterSettings, error) {
		cfgName = strings.Replace(cfgName, event.TS2PHCProcessName, event.PTP4lProcessName, 1)
		return protocol.GrandmasterSettings{
			ClockQuality: fbprotocol.ClockQuality{
				ClockClass:              0,
				ClockAccuracy:           0,
				OffsetScaledLogVariance: 0,
			},
			TimePropertiesDS: protocol.TimePropertiesDS{
				CurrentUtcOffset:      0,
				CurrentUtcOffsetValid: false,
				Leap59:                false,
				Leap61:                false,
				TimeTraceable:         true,
				FrequencyTraceable:    true,
				PtpTimescale:          false,
				TimeSource:            0,
			},
		}, nil
	}
	event.PMCGMSetter = func(cfgName string, g protocol.GrandmasterSettings) error {
		cfgName = strings.Replace(cfgName, event.TS2PHCProcessName, event.PTP4lProcessName, 1)
		return nil
	}
}

type PTPEvents struct {
	processName      event.EventSource
	clockState       event.PTPState
	cfgName          string
	outOfSpec        bool
	values           map[event.ValueType]interface{}
	wantGMState      string // want is the expected output.
	wantClockState   string
	wantProcessState string
	desc             string
	sourceLost       bool
}

func TestEventHandler_ProcessEvents(t *testing.T) {
	monkeyPatch()
	tests := []PTPEvents{
		{
			processName:      event.DPLL,
			cfgName:          "ts2phc.0.config",
			clockState:       event.PTP_LOCKED,
			outOfSpec:        false,
			values:           map[event.ValueType]interface{}{event.OFFSET: 0, event.PHASE_STATUS: 3, event.FREQUENCY_STATUS: 3, event.PPS_STATUS: 1},
			wantGMState:      "GM[0]:[ts2phc.0.config] unknown T-GM-STATUS s0",
			wantClockState:   "ptp4l[0]:[ts2phc.0.config] CLOCK_CLASS_CHANGE 248",
			wantProcessState: "dpll[0]:[ts2phc.0.config] ens1f0 frequency_status 3 offset 0 phase_status 3 pps_status 1 s2",
			desc:             "Initial state, gnss is not set, so expect GM to be FREERUN",
		},
		{
			processName:      event.GNSS,
			cfgName:          "ts2phc.0.config",
			clockState:       event.PTP_LOCKED,
			values:           map[event.ValueType]interface{}{event.OFFSET: 0, event.GPS_STATUS: 3},
			wantGMState:      "GM[0]:[ts2phc.0.config] ens1f0 T-GM-STATUS s0",
			wantClockState:   "ptp4l[0]:[ts2phc.0.config] CLOCK_CLASS_CHANGE 248",
			wantProcessState: "gnss[0]:[ts2phc.0.config] ens1f0 gnss_status 3 offset 0 s2",
			desc:             "gnss is locked and has dpll in locked state,but ts2phc has not yet reported so GM will be FREERUN ",
		},
		{
			processName:      event.TS2PHCProcessName,
			cfgName:          "ts2phc.0.config",
			clockState:       event.PTP_LOCKED,
			values:           map[event.ValueType]interface{}{event.OFFSET: 0},
			wantGMState:      "GM[0]:[ts2phc.0.config] ens1f0 T-GM-STATUS s2",
			wantClockState:   "ptp4l[0]:[ts2phc.0.config] CLOCK_CLASS_CHANGE 6",
			wantProcessState: "ts2phc[0]:[ts2phc.0.config] ens1f0 offset 0 s2",
			desc:             "ts2phc is now reported as locked, GM should be in locked state",
		},
		{
			processName:      event.TS2PHCProcessName,
			cfgName:          "ts2phc.0.config",
			clockState:       event.PTP_FREERUN,
			values:           map[event.ValueType]interface{}{event.OFFSET: 5000},
			wantGMState:      "GM[0]:[ts2phc.0.config] ens1f0 T-GM-STATUS s0",
			wantClockState:   "ptp4l[0]:[ts2phc.0.config] CLOCK_CLASS_CHANGE 248",
			wantProcessState: "ts2phc[0]:[ts2phc.0.config] ens1f0 offset 5000 s0",
			desc:             "ts2phc is reporting FREERUN, GM should be in FREERUN state",
		},
		{
			processName:      event.TS2PHCProcessName,
			cfgName:          "ts2phc.0.config",
			clockState:       event.PTP_LOCKED,
			values:           map[event.ValueType]interface{}{event.OFFSET: 0},
			wantGMState:      "GM[0]:[ts2phc.0.config] ens1f0 T-GM-STATUS s2",
			wantClockState:   "ptp4l[0]:[ts2phc.0.config] CLOCK_CLASS_CHANGE 6",
			wantProcessState: "ts2phc[0]:[ts2phc.0.config] ens1f0 offset 0 s2",
			desc:             "ts2phc is also reporting locked, GM should be in locked state",
		},
		{
			processName:      event.GNSS,
			cfgName:          "ts2phc.0.config",
			clockState:       event.PTP_FREERUN,
			outOfSpec:        false,
			values:           map[event.ValueType]interface{}{event.OFFSET: 0, event.GPS_STATUS: 0},
			wantGMState:      "GM[0]:[ts2phc.0.config] ens1f0 T-GM-STATUS s2",
			wantClockState:   "ptp4l[0]:[ts2phc.0.config] CLOCK_CLASS_CHANGE 6",
			wantProcessState: "gnss[0]:[ts2phc.0.config] ens1f0 gnss_status 0 offset 0 s0",
			sourceLost:       true,
			desc:             "GPS is free run ,source is lost when everything else is locked(Do nothing and wait  for DPLL to switch to HOLDOVER)",
		},

		{
			processName:      event.DPLL,
			cfgName:          "ts2phc.0.config",
			clockState:       event.PTP_HOLDOVER,
			outOfSpec:        false,
			values:           map[event.ValueType]interface{}{event.OFFSET: 0, event.PHASE_STATUS: 4, event.FREQUENCY_STATUS: 4, event.PPS_STATUS: 1},
			wantGMState:      "GM[0]:[ts2phc.0.config] ens1f0 T-GM-STATUS s1",
			wantClockState:   "ptp4l[0]:[ts2phc.0.config] CLOCK_CLASS_CHANGE 7",
			wantProcessState: "dpll[0]:[ts2phc.0.config] ens1f0 frequency_status 4 offset 0 phase_status 4 pps_status 1 s1",
			desc:             "dpll is on Holdover, where source is lost, move to holdover state",
		},
		{
			processName:      event.DPLL,
			cfgName:          "ts2phc.0.config",
			clockState:       event.PTP_FREERUN,
			outOfSpec:        true,
			values:           map[event.ValueType]interface{}{event.OFFSET: 0, event.PHASE_STATUS: 1, event.FREQUENCY_STATUS: 1, event.PPS_STATUS: 1},
			wantGMState:      "GM[0]:[ts2phc.0.config] ens1f0 T-GM-STATUS s0",
			wantClockState:   "ptp4l[0]:[ts2phc.0.config] CLOCK_CLASS_CHANGE 140",
			wantProcessState: "dpll[0]:[ts2phc.0.config] ens1f0 frequency_status 1 offset 0 phase_status 1 pps_status 1 s0",
			desc:             "dpll move to FREERUN from holdover (out of spec)",
		},
		{
			processName:      event.GNSS,
			cfgName:          "ts2phc.0.config",
			clockState:       event.PTP_LOCKED,
			outOfSpec:        false,
			values:           map[event.ValueType]interface{}{event.OFFSET: 0, event.GPS_STATUS: 3},
			wantGMState:      "GM[0]:[ts2phc.0.config] ens1f0 T-GM-STATUS s0",
			wantClockState:   "ptp4l[0]:[ts2phc.0.config] CLOCK_CLASS_CHANGE 140",
			wantProcessState: "gnss[0]:[ts2phc.0.config] ens1f0 gnss_status 3 offset 0 s2",
			sourceLost:       false,
			desc:             "GPS is locked but dpll is in FREERUN and out of spec, yet to switch over in that case GM should stay with last state",
		},
		{
			processName:      event.DPLL,
			cfgName:          "ts2phc.0.config",
			clockState:       event.PTP_LOCKED,
			outOfSpec:        true,
			values:           map[event.ValueType]interface{}{event.OFFSET: 0, event.PHASE_STATUS: 3, event.FREQUENCY_STATUS: 3, event.PPS_STATUS: 1},
			wantGMState:      "GM[0]:[ts2phc.0.config] ens1f0 T-GM-STATUS s2",
			wantClockState:   "ptp4l[0]:[ts2phc.0.config] CLOCK_CLASS_CHANGE 6",
			wantProcessState: "dpll[0]:[ts2phc.0.config] ens1f0 frequency_status 3 offset 0 phase_status 3 pps_status 1 s2",
			desc:             "everything is in locked state",
		},
		{
			processName:      event.TS2PHCProcessName,
			cfgName:          "ts2phc.0.config",
			clockState:       event.PTP_FREERUN,
			outOfSpec:        true,
			values:           map[event.ValueType]interface{}{event.OFFSET: 99999, event.NMEA_STATUS: 0},
			wantGMState:      "GM[0]:[ts2phc.0.config] ens1f0 T-GM-STATUS s0",
			wantClockState:   "ptp4l[0]:[ts2phc.0.config] CLOCK_CLASS_CHANGE 248",
			wantProcessState: "ts2phc[0]:[ts2phc.0.config] ens1f0 nmea_status 0 offset 99999 s0",
			desc:             "ts2phc is not in locked state",
		},
		{
			processName:      event.TS2PHCProcessName,
			cfgName:          "ts2phc.0.config",
			clockState:       event.PTP_LOCKED,
			outOfSpec:        true,
			values:           map[event.ValueType]interface{}{event.OFFSET: 0, event.NMEA_STATUS: 1},
			wantGMState:      "GM[0]:[ts2phc.0.config] ens1f0 T-GM-STATUS s2",
			wantClockState:   "ptp4l[0]:[ts2phc.0.config] CLOCK_CLASS_CHANGE 6",
			wantProcessState: "ts2phc[0]:[ts2phc.0.config] ens1f0 nmea_status 1 offset 0 s2",
			desc:             "everything is in locked state",
		},
	}
	logOut := make(chan string, 100)
	eChannel := make(chan event.EventChannel, 100)
	closeChn := make(chan bool)
	go listenToEvents(closeChn, logOut)
	eventManager := event.Init("node", true, "/tmp/go.sock", eChannel, closeChn, nil, nil, nil)
	eventManager.MockEnable()
	go eventManager.ProcessEvents()
	time.Sleep(1 * time.Second)
	for _, test := range tests {
		select {
		case eChannel <- sendEvents(test.cfgName, test.processName, test.clockState, test.values, test.outOfSpec, test.sourceLost):
			log.Println("sent data to channel")
			log.Println(test.cfgName, test.processName, test.clockState, test.outOfSpec, test.values)
			time.Sleep(1 * time.Second)
		default:
			log.Println("nothing to read")
		}
	retry:
		for i := 0; i < len(logOut); i++ {
			select {
			case c := <-logOut:
				s1 := strings.Index(c, "[")
				s2 := strings.Index(c, "]")
				rs := strings.Replace(c, c[s1+1:s2], "0", -1)

				if strings.HasPrefix(c, string(test.processName)) {
					assert.Equal(t, test.wantProcessState, rs, test.desc)
				}
				if strings.HasPrefix(c, "GM[") {
					assert.Equal(t, test.wantGMState, rs, test.desc)
				}
				if strings.HasPrefix(c, "ptp4l[") {
					assert.Equal(t, test.wantClockState, rs, test.desc)
				}
			default:
			}
			if len(logOut) > 0 {
				goto retry
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	closeChn <- true
	time.Sleep(1 * time.Second)
}

func listenToEvents(closeChn chan bool, logOut chan string) {
	l, sErr := Listen("/tmp/go.sock")
	if sErr != nil {
		glog.Infof("error setting up socket %s", sErr)
		return
	}
	glog.Infof("connection established successfully")

	for {
		select {
		case <-closeChn:
			log.Println("closing socket")
			return
		default:
			fd, err := l.Accept()
			if err != nil {
				glog.Infof("accept error: %s", err)
			} else {
				go ProcessTestEvents(fd, logOut)
			}
		}
	}
}

// Listen ... listen to ptp daemon logs
func Listen(addr string) (l net.Listener, e error) {
	uAddr, err := net.ResolveUnixAddr("unix", addr)
	if err != nil {
		return nil, err
	}

	// Try to listen on the socket. If that fails we check to see if it's a stale
	// socket and remove it if it is. Then we try to listen one more time.
	l, err = net.ListenUnix("unix", uAddr)
	if err != nil {
		if err = removeIfStaleUnixSocket(addr); err != nil {
			return nil, err
		}
		if l, err = net.ListenUnix("unix", uAddr); err != nil {
			return nil, err
		}
	}
	return l, err
}

// removeIfStaleUnixSocket takes in a path and removes it iff it is a socket
// that is refusing connections
func removeIfStaleUnixSocket(socketPath string) error {
	// Ensure it's a socket; if not return without an error
	if st, err := os.Stat(socketPath); err != nil || st.Mode()&os.ModeType != os.ModeSocket {
		return nil
	}
	// Try to connect
	conn, err := net.DialTimeout("unix", socketPath, staleSocketTimeout)
	if err != nil { // =syscall.ECONNREFUSED {
		return os.Remove(socketPath)
	}
	return conn.Close()
}

func ProcessTestEvents(c net.Conn, logOut chan<- string) {
	// echo received messages
	remoteAddr := c.RemoteAddr().String()
	log.Println("Client connected from", remoteAddr)
	scanner := bufio.NewScanner(c)
	for {
		ok := scanner.Scan()
		if !ok {
			break
		}
		msg := scanner.Text()
		glog.Infof("events received %s", msg)
		logOut <- msg
	}
}

func sendEvents(cfgName string, processName event.EventSource, state event.PTPState,
	values map[event.ValueType]interface{}, outOfSpec bool, sourceLost bool) event.EventChannel {
	glog.Info("sending Nav status event to event handler Process")
	return event.EventChannel{
		ProcessName: processName,
		State:       state,
		IFace:       "ens1f0",
		CfgName:     cfgName,
		Values:      values,
		SourceLost:  sourceLost,
		ClockType:   "GM",
		Time:        0,
		OutOfSpec:   outOfSpec,
		WriteToLog:  true,
		Reset:       false,
	}
}

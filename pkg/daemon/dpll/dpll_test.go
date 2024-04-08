package dpll_test

import (
	"fmt"
	nl "github.com/openshift/linuxptp-daemon/pkg/dpll-netlink"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang/glog"
	"github.com/openshift/linuxptp-daemon/pkg/config"
	"github.com/openshift/linuxptp-daemon/pkg/daemon/dpll"
	"github.com/openshift/linuxptp-daemon/pkg/event"
	"github.com/stretchr/testify/assert"
)

const (
	clockid    = 123454566
	id         = 123456
	moduleName = "test"
)

type DpllTestCase struct {
	reply                     *nl.DoDeviceGetReply
	sourceLost                bool
	source                    event.EventSource
	offset                    int64
	expectedIntermediateState event.PTPState
	expectedState             event.PTPState
	sleep                     time.Duration
	expectedPhaseStatus       int64
	expectedPhaseOffset       int64
	expectedFrequencyStatus   int64
	expectedInSpecState       bool
	desc                      string
}

// mode 1: "manual",	2: "automatic", 3: "holdover", 4: "freerun",
// 1: "unlocked", 2: "locked", 3: "locked-ho-acquired", 4: "holdover"
func getTestData(source event.EventSource, pinType uint32) []DpllTestCase {
	return []DpllTestCase{{
		reply: &nl.DoDeviceGetReply{
			Id:            id,
			ModuleName:    moduleName,
			Mode:          1,
			ModeSupported: 0,
			LockStatus:    2, //LOCKED,
			ClockId:       clockid,
			Type:          2, //1 pps 2 eec
		},
		sourceLost:                false,
		source:                    source,
		offset:                    dpll.FaultyPhaseOffset,
		expectedIntermediateState: event.PTP_FREERUN,
		expectedState:             event.PTP_FREERUN,
		expectedPhaseStatus:       0, //no phase status event for eec
		expectedPhaseOffset:       dpll.FaultyPhaseOffset * 1000000,
		expectedFrequencyStatus:   2, // locked
		expectedInSpecState:       true,
		desc:                      "1.locked frequency status, unknonw Phase status ",
	}, {
		reply: &nl.DoDeviceGetReply{
			Id:            id,
			ModuleName:    moduleName,
			Mode:          1,
			ModeSupported: 0,
			LockStatus:    2, //LOCKED,
			ClockId:       clockid,
			Type:          1, //1 pps 2 eec
		},
		sourceLost:                false,
		source:                    source,
		offset:                    5,
		expectedIntermediateState: event.PTP_LOCKED,
		expectedState:             event.PTP_LOCKED,
		expectedPhaseStatus:       2, //no phase status event for eec
		expectedPhaseOffset:       50,
		expectedFrequencyStatus:   2, // locked
		expectedInSpecState:       true,
		desc:                      "2. with locked frequency status, reading phase status ",
	},
		{
			reply: &nl.DoDeviceGetReply{
				Id:            id,
				ModuleName:    moduleName,
				Mode:          1,
				ModeSupported: 0,
				LockStatus: func() uint32 {
					if pinType == 2 {
						return 4 // holdover
					} else {
						return 4 // locked
					}
				}(), // holdover,
				ClockId: clockid,
				Type:    pinType, //1 pps 2 eec
			},
			sourceLost:                true,
			sleep:                     2,
			source:                    source,
			offset:                    5,
			expectedIntermediateState: event.PTP_FREERUN,
			expectedState:             event.PTP_FREERUN,
			expectedPhaseStatus: func() int64 {
				if pinType == 2 {
					return 2 // locked
				} else {
					return 4 //holdover
				}
			}(), //no phase status event for eec
			expectedPhaseOffset: dpll.FaultyPhaseOffset * 1000000,
			expectedFrequencyStatus: func() int64 {
				if pinType == 2 {
					return 4
				} else {
					return 2
				}
			}(), // holdover to free run
			expectedInSpecState: func() bool {
				if pinType == 2 {
					return false
				} else {
					return true
				}
			}(),
			desc: "3. Holdover frequency/Phase status status times out in 1sec ",
		},
		{
			reply: &nl.DoDeviceGetReply{
				Id:            id,
				ModuleName:    moduleName,
				Mode:          1,
				ModeSupported: 0,
				LockStatus:    4, // holdover,
				ClockId:       clockid,
				Type:          pinType, //1 pps 2 eec
			},
			sourceLost:                true,
			sleep:                     2,
			source:                    source,
			offset:                    6,
			expectedIntermediateState: event.PTP_FREERUN,
			expectedState:             event.PTP_FREERUN,
			expectedPhaseStatus: func() int64 {
				if pinType == 2 {
					return 2
				} else {
					return 4
				}
			}(), //no phase status event for eec
			expectedPhaseOffset: dpll.FaultyPhaseOffset * 1000000,
			expectedFrequencyStatus: func() int64 {
				if pinType == 2 {
					return 4
				} else {
					return 2
				}
			}(),
			expectedInSpecState: func() bool {
				if pinType == 2 {
					return false
				} else {
					return true
				}
			}(),
			desc: "4.Out of holdover state.or stays in free run for pps , gns:=declare outofSpec Freerun within a sec",
		},
	}
}
func TestDpllConfig_MonitorProcessGNSS(t *testing.T) {
	dpll.MockDpllReplies = make(chan *nl.DoDeviceGetReply, 1)
	assert.True(t, dpll.MockDpllReplies != nil)
	eChannel := make(chan event.EventChannel, 10)
	closeChn := make(chan bool)
	// event has to be running before dpll is started
	eventProcessor := event.Init("node", false, "/tmp/go.sock", eChannel, closeChn, nil, nil, nil)
	d := dpll.NewDpll(clockid, 1400, 2, 10, "ens01",
		[]event.EventSource{event.GNSS}, dpll.MOCK, map[string]map[string]string{})
	d.CmdInit()
	eventChannel := make(chan event.EventChannel, 10)
	go eventProcessor.ProcessEvents()

	time.Sleep(5 * time.Second)
	if d != nil {
		d.MonitorProcess(config.ProcessConfig{
			ClockType:       "GM",
			ConfigName:      "test",
			EventChannel:    eventChannel,
			GMThreshold:     config.Threshold{},
			InitialPTPState: event.PTP_FREERUN,
		})
	}
	fmt.Println("starting Mock replies ")
	for _, tt := range getTestData(event.GNSS, 2) {
		d.SetSourceLost(tt.sourceLost)
		d.SetPhaseOffset(tt.expectedPhaseOffset)
		d.SetDependsOn([]event.EventSource{tt.source})
		dpll.MockDpllReplies <- tt.reply
		d.MonitorDpllMock()
		time.Sleep(1 * time.Second)
		assert.Equal(t, tt.expectedIntermediateState, d.State(), tt.desc)
		time.Sleep(tt.sleep * time.Second)
		assert.Equal(t, tt.expectedPhaseStatus, d.PhaseStatus(), tt.desc)
		assert.Equal(t, tt.expectedFrequencyStatus, d.FrequencyStatus(), tt.desc)
		assert.Equal(t, tt.expectedPhaseOffset/1000000, d.PhaseOffset(), tt.desc)
		assert.Equal(t, tt.expectedState, d.State(), tt.desc)
		assert.Equal(t, tt.expectedInSpecState, d.InSpec())

	}
	closeChn <- true
}

func TestDpllConfig_MonitorProcessPPS(t *testing.T) {
	dpll.MockDpllReplies = make(chan *nl.DoDeviceGetReply, 1)
	assert.True(t, dpll.MockDpllReplies != nil)
	eChannel := make(chan event.EventChannel, 10)
	closeChn := make(chan bool)
	// event has to be running before dpll is started
	eventProcessor := event.Init("node", false, "/tmp/go.sock", eChannel, closeChn, nil, nil, nil)
	d := dpll.NewDpll(clockid, 1400, 2, 10, "ens01",
		[]event.EventSource{event.GNSS}, dpll.MOCK, map[string]map[string]string{})
	d.CmdInit()
	eventChannel := make(chan event.EventChannel, 10)
	go eventProcessor.ProcessEvents()

	time.Sleep(5 * time.Second)
	if d != nil {
		d.MonitorProcess(config.ProcessConfig{
			ClockType:       "GM",
			ConfigName:      "test",
			EventChannel:    eventChannel,
			GMThreshold:     config.Threshold{},
			InitialPTPState: event.PTP_FREERUN,
		})
	}
	fmt.Println("starting Mock replies ")
	for _, tt := range getTestData(event.PPS, 1) {
		d.SetSourceLost(tt.sourceLost)
		d.SetPhaseOffset(tt.expectedPhaseOffset)
		d.SetDependsOn([]event.EventSource{tt.source})
		dpll.MockDpllReplies <- tt.reply
		d.MonitorDpllMock()
		time.Sleep(tt.sleep * time.Second)
		assert.Equal(t, tt.expectedPhaseStatus, d.PhaseStatus(), tt.desc)
		assert.Equal(t, tt.expectedFrequencyStatus, d.FrequencyStatus(), tt.desc)
		assert.Equal(t, tt.expectedPhaseOffset/1000000, d.PhaseOffset(), tt.desc)
		assert.Equal(t, tt.expectedState, d.State(), tt.desc)
		assert.Equal(t, tt.expectedInSpecState, d.InSpec(), tt.desc)

	}
	closeChn <- true
}

func TestSysfs(t *testing.T) {
	//indexStr := fmt.Sprintf("/sys/class/net/%s/ifindex", "lo")
	//fContent, err := os.ReadFile(indexStr)
	//assert.Nil(t, err)
	fcontentStr := strings.ReplaceAll("-26644444444444444", "\n", "")
	index, err2 := strconv.ParseInt(fcontentStr, 10, 64)
	glog.Errorf("errr %s", err2)
	assert.Nil(t, err2)
	assert.GreaterOrEqual(t, index, int64(-26644444444444444))
}

type dpllTestCase struct {
	localMaxHoldoverOffSet uint64
	localHoldoverTimeout   uint64
	maxInSpecOffset        uint64
	expectedSlope          float64
	expectedTimeout        int64
}

func TestSlopeAndTimer(t *testing.T) {

	testCase := []dpllTestCase{
		{
			localMaxHoldoverOffSet: 6000,
			localHoldoverTimeout:   100,
			maxInSpecOffset:        100,
			expectedSlope:          60000,
			expectedTimeout:        2,
		},
	}
	for _, tt := range testCase {
		d := dpll.NewDpll(100, tt.localMaxHoldoverOffSet, tt.localHoldoverTimeout, tt.maxInSpecOffset,
			"test", []event.EventSource{}, dpll.MOCK, map[string]map[string]string{})
		assert.Equal(t, tt.localMaxHoldoverOffSet, d.LocalMaxHoldoverOffSet, "localMaxHoldover offset")
		assert.Equal(t, tt.localHoldoverTimeout, d.LocalHoldoverTimeout, "Local holdover timeout")
		assert.Equal(t, tt.maxInSpecOffset, d.MaxInSpecOffset, "Max In Spec Offset")
		assert.Equal(t, tt.expectedTimeout, d.Timer(), "Timer in secs")
		assert.Equal(t, tt.expectedSlope, d.Slope(), "Slope")
	}
}

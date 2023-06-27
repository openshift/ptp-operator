package dpll

import (
	"encoding/binary"
	"fmt"
	"os"
	"time"

	"github.com/golang/glog"
	"github.com/openshift/linuxptp-daemon/pkg/config"
	"github.com/openshift/linuxptp-daemon/pkg/event"
)

const (
	DPLL_UNKNOWN       = -1
	DPLL_INVALID       = 0
	DPLL_FREERUN       = 1
	DPLL_LOCKED        = 2
	DPLL_LOCKED_HO_ACQ = 3
	DPLL_HOLDOVER      = 4

	LocalMaxHoldoverOffSet = 1500  //ns
	LocalHoldoverTimeout   = 14400 //secs
	MaxInSpecOffset        = 100   //ns
)

type DpllConfig struct {
	LocalMaxHoldoverOffSet int64
	LocalHoldoverTimeout   int64
	MaxInSpecOffset        int64
	iface                  string
	name                   string
	slope                  float64
	timer                  int64 //secs
	inSpec                 bool
	offset                 int64
	state                  event.PTPState
	onHoldover             bool
	sourceLost             bool
	processConfig          config.ProcessConfig
	dependingState         []event.EventSource
}

func (d *DpllConfig) Name() string {
	//TODO implement me
	return "dpll"
}

func (d *DpllConfig) Stopped() bool {
	//TODO implement me
	panic("implement me")
}

func (d *DpllConfig) CmdStop() {

	glog.Infof("Process %s terminated", d.Name())
}

func (d *DpllConfig) CmdInit() {
	//TODO implement me
	glog.Infof("cmdInit not implemented %s", d.Name())
}

func (d *DpllConfig) CmdRun(stdToSocket bool) {
	//TODO implement me
	glog.Infof("cmdRun not implemented %s", d.Name())
}

func NewDpll(localMaxHoldoverOffSet, localHoldoverTimeout, maxInSpecOffset int64,
	iface string, dependingState []event.EventSource) *DpllConfig {
	d := &DpllConfig{
		LocalMaxHoldoverOffSet: localMaxHoldoverOffSet,
		LocalHoldoverTimeout:   localHoldoverTimeout,
		MaxInSpecOffset:        maxInSpecOffset,
		slope: func() float64 {
			return float64((localMaxHoldoverOffSet / localHoldoverTimeout) * 1000)
		}(),
		timer:          0,
		offset:         0,
		state:          event.PTP_FREERUN,
		iface:          iface,
		onHoldover:     false,
		sourceLost:     false,
		dependingState: dependingState,
	}
	d.timer = int64(float64(d.MaxInSpecOffset) / d.slope)
	return d
}
func (d *DpllConfig) MonitorProcess(processCfg config.ProcessConfig) {
	go d.MonitorDpll(processCfg)
}

// MonitorDpll ... monitor dpll events
func (d *DpllConfig) MonitorDpll(processCfg config.ProcessConfig) {
	ticker := time.NewTicker(1 * time.Second)
	dpll_state := event.PTP_FREERUN
	var closeCh chan bool
	// determine dpll state
	responseChannel := make(chan event.PTPState)
	var responseState event.PTPState
	d.inSpec = true
	for {
		select {
		case <-ticker.C:
			// monitor DPLL
			//TODO: netlink to monitor DPLL start here
			phase_status, frequency_status, phase_offset := d.sysfs(d.iface)
			// check GPS status data lost ?
			// send event
			lowestState := event.PTP_UNKNOWN
			var dependingProcessState []event.PTPState
			for _, stateSource := range d.dependingState {
				event.GetPTPStateRequest(event.StatusRequest{
					Source:          stateSource,
					CfgName:         processCfg.ConfigName,
					ResponseChannel: responseChannel,
				})
				select {
				case responseState = <-responseChannel:
				case <-time.After(1 * time.Second):
					responseState = event.PTP_UNKNOWN
				}
				dependingProcessState = append(dependingProcessState, responseState)
			}

			for i, state := range dependingProcessState {
				if i == 0 || state < lowestState {
					lowestState = state
				}
			}

			// check dpll status
			if lowestState == event.PTP_LOCKED {
				d.sourceLost = false
			} else {
				d.sourceLost = true
			}
			// calculate dpll status
			dpllStatus := d.getWorseState(phase_status, frequency_status)
			switch dpllStatus {
			case DPLL_FREERUN, DPLL_INVALID, DPLL_UNKNOWN:
				d.inSpec = true
				if d.onHoldover {
					closeCh <- true
				}
				d.state = event.PTP_FREERUN
			case DPLL_LOCKED:
				d.inSpec = true
				if !d.sourceLost && d.isOffsetInRange() {
					d.state = event.PTP_LOCKED
				} else {
					d.state = event.PTP_FREERUN
				}
			case DPLL_LOCKED_HO_ACQ, DPLL_HOLDOVER:
				if !d.sourceLost && d.isOffsetInRange() {
					d.inSpec = true
					d.state = event.PTP_LOCKED
					if d.onHoldover {
						d.inSpec = true
						closeCh <- true
					}
				} else if d.sourceLost && d.inSpec == true {
					closeCh = make(chan bool)
					d.inSpec = false
					go d.holdover(closeCh)
				} else {
					d.state = event.PTP_FREERUN
				}
			}
			processCfg.EventChannel <- event.EventChannel{
				ProcessName: event.DPLL,
				State:       dpll_state,
				IFace:       d.iface,
				CfgName:     processCfg.ConfigName,
				Values: map[event.ValueType]int64{
					event.FREQUENCY_STATUS: frequency_status,
					event.OFFSET:           phase_offset,
					event.PHASE_STATUS:     phase_status,
				},
				ClockType:  processCfg.ClockType,
				Time:       time.Now().Unix(),
				WriteToLog: true,
				Reset:      false,
			}
		case <-processCfg.CloseCh:
			processCfg.EventChannel <- event.EventChannel{
				ProcessName: event.DPLL,
				IFace:       d.iface,
				CfgName:     processCfg.ConfigName,
				ClockType:   processCfg.ClockType,
				Time:        time.Now().Unix(),
				Reset:       true,
			}
			ticker.Stop()
		}
	}
}

// getStateQuality maps the state with relatively worse signal quality with
// a lower number for easy comparison
// Ref: ITU-T G.781 section 6.3.1 Auto selection operation
func (d *DpllConfig) getStateQuality() map[int64]float64 {
	return map[int64]float64{
		DPLL_UNKNOWN:       -1,
		DPLL_INVALID:       0,
		DPLL_FREERUN:       1,
		DPLL_HOLDOVER:      2,
		DPLL_LOCKED:        3,
		DPLL_LOCKED_HO_ACQ: 4,
	}
}

// getWorseState returns the state with worse signal quality
func (d *DpllConfig) getWorseState(pstate, fstate int64) int64 {
	sq := d.getStateQuality()
	if sq[pstate] < sq[fstate] {
		return pstate
	}
	return fstate
}

func (d *DpllConfig) holdover(closeCh chan bool) {
	start := time.Now()
	ticker := time.NewTicker(1 * time.Second)
	d.state = event.PTP_HOLDOVER
	for timeout := time.After(time.Duration(d.timer)); ; {
		select {
		case <-ticker.C:
			//calculate offset
			d.offset = int64(d.slope * float64(time.Since(start)))
		case <-timeout:
			d.state = event.PTP_FREERUN
			return
		case <-closeCh:
			return
		}
	}
}

func (d *DpllConfig) isOffsetInRange() bool {
	if d.offset < d.processConfig.GMThreshold.Max && d.offset > d.processConfig.GMThreshold.Min {
		return true
	}
	return false
}

// Index of DPLL being configured [0:EEC (DPLL0), 1:PPS (DPLL1)]
// Frequency State (EEC_DPLL)
// cat /sys/class/net/interface_name/device/dpll_0_state
// Phase State
// cat /sys/class/net/ens7f0/device/dpll_1_state
// Phase Offset
// cat /sys/class/net/ens7f0/device/dpll_1_offset
func (d *DpllConfig) sysfs(iface string) (phaseState, frequencyState, phaseOffset int64) {
	if iface == "" {
		phaseState = DPLL_INVALID
		frequencyState = DPLL_INVALID
		phaseOffset = 0
		return
	}
	frequencyStateStr := fmt.Sprintf("/sys/class/net/%s/device/dpll_0_state", iface)
	phaseStateStr := fmt.Sprintf("/sys/class/net/%s/device/dpll_1_state", iface)
	phaseOffsetStr := fmt.Sprintf("/sys/class/net/%s/device/dpll_1_offset", iface)
	// Read the content of the sysfs path
	fContent, err := os.ReadFile(frequencyStateStr)
	if err != nil {
		glog.Infof("error reading sysfs path %s %s:", frequencyStateStr, err)
	} else {
		frequencyState = int64(binary.BigEndian.Uint64(fContent))
	}
	// Read the content of the sysfs path
	pContent, err2 := os.ReadFile(phaseStateStr)
	if err2 != nil {
		glog.Infof("error reading sysfs path %s %s:", phaseStateStr, err2)
	} else {
		phaseState = int64(binary.BigEndian.Uint64(pContent))
	}
	// Read the content of the sysfs path
	offsetContent, err3 := os.ReadFile(phaseOffsetStr)
	if err3 != nil {
		glog.Infof("error reading sysfs path %s %s:", phaseOffsetStr, err3)
	} else {
		phaseOffset = int64(binary.BigEndian.Uint64(offsetContent))
	}
	return
}

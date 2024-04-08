package dpll

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/mdlayher/genetlink"
	"github.com/openshift/linuxptp-daemon/pkg/config"
	nl "github.com/openshift/linuxptp-daemon/pkg/daemon/dpll-netlink"
	"github.com/openshift/linuxptp-daemon/pkg/event"
	"golang.org/x/sync/semaphore"
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
	monitoringInterval     = 1 * time.Second

	LocalMaxHoldoverOffSetStr = "LocalMaxHoldoverOffSet"
	LocalHoldoverTimeoutStr   = "LocalHoldoverTimeout"
	MaxInSpecOffsetStr        = "MaxInSpecOffset"
	ClockIdStr                = "clockId"
	FaultyPhaseOffset         = 99999999999
)

type dpllApiType string

var MockDpllReplies chan *nl.DoDeviceGetReply

const (
	SYSFS   dpllApiType = "sysfs"
	NETLINK dpllApiType = "netlink"
	MOCK    dpllApiType = "mock"
	NONE    dpllApiType = "none"
)

type DependingStates struct {
	sync.Mutex
	states       map[event.EventSource]event.PTPState
	currentState event.PTPState
}

// DpllSubscriber ... event subscriber
type DpllSubscriber struct {
	source event.EventSource
	dpll   *DpllConfig
	id     string
}

var dependingProcessStateMap = DependingStates{
	states: make(map[event.EventSource]event.PTPState),
}

// GetCurrentState ... get current state
func (d *DependingStates) GetCurrentState() event.PTPState {
	return d.currentState
}

func (d *DependingStates) UpdateState(source event.EventSource) {
	// do not lock here, since this function is called from other locks
	lowestState := event.PTP_FREERUN
	for _, state := range d.states {
		if state < lowestState {
			lowestState = state
		}
	}
	d.currentState = lowestState
}

// DpllConfig ... DPLL configuration
type DpllConfig struct {
	LocalMaxHoldoverOffSet uint64
	LocalHoldoverTimeout   uint64
	MaxInSpecOffset        uint64
	iface                  string
	name                   string
	slope                  float64
	timer                  int64 //secs
	inSpec                 bool
	state                  event.PTPState
	onHoldover             bool
	sourceLost             bool
	processConfig          config.ProcessConfig
	dependsOn              []event.EventSource
	exitCh                 chan struct{}
	holdoverCloseCh        chan bool
	ticker                 *time.Ticker
	apiType                dpllApiType
	// DPLL netlink connection pointer. If 'nil', use sysfs
	conn *nl.Conn
	// We need to keep latest DPLL status values, since Netlink device
	// change indications don't contain all the status fields, but
	// only the changed one(s)
	phaseStatus     int64
	frequencyStatus int64
	phaseOffset     int64
	// clockId is needed to distinguish between DPLL associated with the particular
	// iface from other DPLL units that might be present on the system. Clock ID implementation
	// is driver-specific and vendor-specific.
	clockId uint64
	sync.Mutex
	isMonitoring         bool
	subscriber           []*DpllSubscriber
	phaseOffsetPinFilter map[string]map[string]string
}

func (d *DpllConfig) InSpec() bool {
	return d.inSpec
}

// DependsOn ...  depends on other events
func (d *DpllConfig) DependsOn() []event.EventSource {
	return d.dependsOn
}

// SetDependsOn ... set depends on ..
func (d *DpllConfig) SetDependsOn(dependsOn []event.EventSource) {
	d.dependsOn = dependsOn
}

// State ... get dpll state
func (d *DpllConfig) State() event.PTPState {
	return d.state
}

// SetPhaseOffset ...  set phaseOffset
// Measured phase offset values are fractional with 3-digit decimal places and shall be
// divided by DPLL_PIN_PHASE_OFFSET_DIVIDER to get integer part
// The units are picoseconds.
// We further divide it by 1000 to report nanoseconds
func (d *DpllConfig) SetPhaseOffset(phaseOffset int64) {
	d.phaseOffset = int64(math.Round(float64(phaseOffset / nl.DPLL_PHASE_OFFSET_DIVIDER / 1000)))
}

// SourceLost ... get source status
func (d *DpllConfig) SourceLost() bool {
	return d.sourceLost
}

// SetSourceLost ... set source status
func (d *DpllConfig) SetSourceLost(sourceLost bool) {
	d.sourceLost = sourceLost
}

// PhaseOffset ... get phase offset
func (d *DpllConfig) PhaseOffset() int64 {
	return d.phaseOffset
}

// FrequencyStatus ... get frequency status
func (d *DpllConfig) FrequencyStatus() int64 {
	return d.frequencyStatus
}

// PhaseStatus get phase status
func (d *DpllConfig) PhaseStatus() int64 {
	return d.phaseStatus
}

// Monitor ...
func (s DpllSubscriber) Monitor() {
	glog.Infof("Starting dpll monitoring %s", s.id)
	s.dpll.MonitorDpll()
}

// Topic ... event topic
func (s DpllSubscriber) Topic() event.EventSource {
	return s.source
}

func (s DpllSubscriber) ID() string {
	return s.id
}

// Notify ... event notification
func (s DpllSubscriber) Notify(source event.EventSource, state event.PTPState) {
	if s.dpll == nil || !s.dpll.isMonitoring {
		glog.Errorf("dpll subscriber %s is not initialized (monitoring state %t)", s.source, s.dpll.isMonitoring)
		return
	}
	dependingProcessStateMap.Lock()
	defer dependingProcessStateMap.Unlock()
	currentState := dependingProcessStateMap.states[source]
	if currentState != state {
		glog.Infof("%s notified on state change: from state %v to state %v", source, currentState, state)
		dependingProcessStateMap.states[source] = state
		if source == event.GNSS {
			if state == event.PTP_LOCKED {
				s.dpll.sourceLost = false
			} else {
				s.dpll.sourceLost = true
			}
			glog.Infof("sourceLost %v", s.dpll.sourceLost)
		}
		s.dpll.stateDecision()
		glog.Infof("%s notified on state change: state %v", source, state)
		dependingProcessStateMap.UpdateState(source)
	}
}

// Name ... name of the process
func (d *DpllConfig) Name() string {
	return string(event.DPLL)
}

// Stopped ... stopped
func (d *DpllConfig) Stopped() bool {
	//TODO implement me
	panic("implement me")
}

// ExitCh ... exit channel
func (d *DpllConfig) ExitCh() chan struct{} {
	return d.exitCh
}

// ExitCh ... exit channel
func (d *DpllConfig) hasGNSSAsSource() bool {
	if d.dependsOn[0] == event.GNSS {
		return true
	}
	return false
}

// ExitCh ... exit channel
func (d *DpllConfig) hasPPSAsSource() bool {
	if d.dependsOn[0] == event.PPS {
		return true
	}
	return false
}

// CmdStop ... stop command
func (d *DpllConfig) CmdStop() {
	glog.Infof("stopping %s", d.Name())
	d.ticker.Stop()
	glog.Infof("Ticker stopped %s", d.Name())
	close(d.exitCh) // terminate loop
	glog.Infof("Process %s terminated", d.Name())
}

// CmdInit ... init command
func (d *DpllConfig) CmdInit() {
	// register to event notification from other processes
	if d.apiType != MOCK { // use mock type unit test DPLL
		d.setAPIType()
	}
	glog.Infof("api type %v", d.apiType)
}

// CmdRun ... run command
func (d *DpllConfig) CmdRun(stdToSocket bool) {
	// noting to run, monitor() function takes care of dpll run
}

func (d *DpllConfig) unRegisterAll() {
	// register to event notification from other processes
	for _, s := range d.subscriber {
		event.StateRegisterer.Unregister(s)
	}
}

// NewDpll ... create new DPLL process
func NewDpll(clockId uint64, localMaxHoldoverOffSet, localHoldoverTimeout, maxInSpecOffset uint64,
	iface string, dependsOn []event.EventSource, apiType dpllApiType, phaseOffsetPinFilter map[string]map[string]string) *DpllConfig {
	glog.Infof("Calling NewDpll with clockId %x, localMaxHoldoverOffSet=%d, localHoldoverTimeout=%d, maxInSpecOffset=%d, iface=%s, phase offset pin filter=%v", clockId, localMaxHoldoverOffSet, localHoldoverTimeout, maxInSpecOffset, iface, phaseOffsetPinFilter)
	d := &DpllConfig{
		clockId:                clockId,
		LocalMaxHoldoverOffSet: localMaxHoldoverOffSet,
		LocalHoldoverTimeout:   localHoldoverTimeout,
		MaxInSpecOffset:        maxInSpecOffset,
		slope: func() float64 {
			return (float64(localMaxHoldoverOffSet) / float64(localHoldoverTimeout)) * 1000.0
		}(),
		timer:                0,
		state:                event.PTP_FREERUN,
		iface:                iface,
		onHoldover:           false,
		sourceLost:           false,
		dependsOn:            dependsOn,
		exitCh:               make(chan struct{}),
		ticker:               time.NewTicker(monitoringInterval),
		isMonitoring:         false,
		apiType:              apiType,
		phaseOffsetPinFilter: phaseOffsetPinFilter,
		phaseOffset:          FaultyPhaseOffset,
	}

	d.timer = int64(math.Round(float64(d.MaxInSpecOffset*1000) / d.slope))
	glog.Infof("slope %f ps/s, offset %f ns, timer %d sec", d.slope, float64(d.MaxInSpecOffset), d.timer)
	return d
}
func (d *DpllConfig) Slope() float64 {
	return d.slope
}

func (d *DpllConfig) Timer() int64 {
	return d.timer
}

func (d *DpllConfig) PhaseOffsetPin(pin *nl.DoPinGetReply) bool {
	if pin.ClockId == d.clockId && pin.ParentDevice.PhaseOffset != math.MaxInt64 {
		for k, v := range d.phaseOffsetPinFilter[strconv.FormatUint(d.clockId, 10)] {
			switch k {
			case "boardLabel":
				if strings.Compare(pin.BoardLabel, v) != 0 {
					return false
				}
			case "panelLabel":
				if strings.Compare(pin.PanelLabel, v) != 0 {
					return false
				}
			default:
				glog.Warningf("unsupported phase offset pin filter key: %s", k)
			}
		}
		return true
	}
	return false
}

// nlUpdateState updates DPLL state in the DpllConfig structure.
func (d *DpllConfig) nlUpdateState(devices []*nl.DoDeviceGetReply, pins []*nl.DoPinGetReply) bool {
	valid := false

	for _, reply := range devices {
		if reply.ClockId == d.clockId {
			if reply.LockStatus == DPLL_INVALID {
				glog.Info("discarding on invalid lock status: ", nl.GetDpllStatusHR(reply))
				continue
			}
			glog.Info(nl.GetDpllStatusHR(reply))
			switch nl.GetDpllType(reply.Type) {
			case "eec":
				d.frequencyStatus = int64(reply.LockStatus)
				valid = true
			case "pps":
				d.phaseStatus = int64(reply.LockStatus)
				valid = true
			}
		} else {
			glog.Infof("discarding on clock ID %v (%s): ", nl.GetDpllStatusHR(reply), d.iface)
		}
	}
	for _, pin := range pins {
		if d.PhaseOffsetPin(pin) {
			d.SetPhaseOffset(pin.ParentDevice.PhaseOffset)
			glog.Info("setting phase offset to ", d.phaseOffset, " ns for clock id ", d.clockId)
			valid = true
		}
	}
	return valid
}

// monitorNtf receives a multicast unsolicited notification and
// calls dpll state updating function.
func (d *DpllConfig) monitorNtf(c *genetlink.Conn) {
	for {
		msgs, _, err := c.Receive()
		if err != nil {
			if err.Error() == "netlink receive: use of closed file" {
				glog.Infof("netlink connection has been closed - stop monitoring for %s", d.iface)
			} else {
				glog.Error(err)
			}
			return
		}
		devices, pins := []*nl.DoDeviceGetReply{}, []*nl.DoPinGetReply{}
		for _, msg := range msgs {
			devices, pins = []*nl.DoDeviceGetReply{}, []*nl.DoPinGetReply{}
			switch msg.Header.Command {
			case nl.DPLL_CMD_DEVICE_CHANGE_NTF:
				devices, err = nl.ParseDeviceReplies([]genetlink.Message{msg})
				if err != nil {
					glog.Error(err)
					return
				}

			case nl.DPLL_CMD_PIN_CHANGE_NTF:
				pins, err = nl.ParsePinReplies([]genetlink.Message{msg})
				if err != nil {
					glog.Error(err)
					return
				}

			default:
				glog.Info("unhandled dpll message", msg.Header.Command, msg.Data)

			}

		}
		if d.nlUpdateState(devices, pins) {
			d.stateDecision()
		}
	}
}

// checks whether sysfs file structure exists for dpll associated with the interface
func (d *DpllConfig) isSysFsPresent() bool {
	path := fmt.Sprintf("/sys/class/net/%s/device/dpll_0_state", d.iface)
	if _, err := os.Stat(path); err == nil {
		return true
	}
	return false
}

// MonitorDpllSysfs monitors DPLL through sysfs
func (d *DpllConfig) isNetLinkPresent() bool {
	conn, err := nl.Dial(nil)
	if err != nil {
		glog.Infof("failed to establish dpll netlink connection (%s): %s", d.iface, err)
		return false
	}
	conn.Close()
	return true
}

// setAPIType
func (d *DpllConfig) setAPIType() {
	if d.isSysFsPresent() {
		d.apiType = SYSFS
	} else if d.isNetLinkPresent() {
		d.apiType = NETLINK
	} else {
		d.apiType = NONE
	}
}

func (d *DpllConfig) MonitorDpllMock() {
	glog.Info("starting dpll mock monitoring")

	if d.nlUpdateState([]*nl.DoDeviceGetReply{<-MockDpllReplies}, []*nl.DoPinGetReply{}) {
		d.stateDecision()
	}

	glog.Infof("closing dpll mock ")
}

// MonitorDpllNetlink monitors DPLL through netlink
func (d *DpllConfig) MonitorDpllNetlink() {
	redial := true
	var replies []*nl.DoDeviceGetReply
	var err error
	var sem *semaphore.Weighted
	for {
		if redial {
			if d.conn == nil {
				if conn, err2 := nl.Dial(nil); err2 != nil {
					d.conn = nil
					glog.Infof("failed to establish dpll netlink connection (%s): %s", d.iface, err2)
					goto checkExit
				} else {
					d.conn = conn
				}
			}

			c := d.conn.GetGenetlinkConn()
			mcastId, found := d.conn.GetMcastGroupId(nl.DPLL_MCGRP_MONITOR)
			if !found {
				glog.Warning("multicast ID ", nl.DPLL_MCGRP_MONITOR, " not found")
				goto abort
			}

			replies, err = d.conn.DumpDeviceGet()
			if err != nil {
				goto abort
			}

			if d.nlUpdateState(replies, []*nl.DoPinGetReply{}) {
				d.stateDecision()
			}

			err = c.JoinGroup(mcastId)
			if err != nil {
				goto abort
			}

			sem = semaphore.NewWeighted(1)
			err = sem.Acquire(context.Background(), 1)
			if err != nil {
				goto abort
			}

			go func() {
				defer sem.Release(1)
				d.monitorNtf(c)
			}()

			goto checkExit

		abort:
			d.stopDpll()
		}

	checkExit:
		select {
		case <-d.exitCh:
			glog.Infof("terminating netlink dpll monitoring")
			select {
			case d.processConfig.EventChannel <- event.EventChannel{
				ProcessName: event.DPLL,
				IFace:       d.iface,
				CfgName:     d.processConfig.ConfigName,
				ClockType:   d.processConfig.ClockType,
				Time:        time.Now().UnixMilli(),
				Reset:       true,
			}:
			default:
				glog.Error("failed to send dpll event terminated event")
			}
			// unregister from event notification from other processes
			d.unRegisterAllSubscriber()

			if d.onHoldover {
				close(d.holdoverCloseCh)
				glog.Infof("closing holdover for %s", d.iface)
				d.onHoldover = false
			}

			d.stopDpll()
			return

		default:
			redial = func() bool {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
				defer cancel()

				if sem == nil {
					return false
				}

				if err = sem.Acquire(ctx, 1); err != nil {
					return false
				}

				glog.Infof("dpll monitoring exited, initiating redial (%s)", d.iface)
				d.stopDpll()
				return true
			}()
			time.Sleep(time.Millisecond * 250) // cpu saver
		}
	}
}

// stopDpll stops DPLL monitoring
func (d *DpllConfig) stopDpll() {
	if d.conn != nil {
		if err := d.conn.Close(); err != nil {
			glog.Errorf("error closing DPLL netlink connection: (%s) %s", d.iface, err)
		}
		d.conn = nil
	}
}

// MonitorProcess is initiating monitoring of DPLL associated with a process
func (d *DpllConfig) MonitorProcess(processCfg config.ProcessConfig) {
	d.processConfig = processCfg
	// register to event notification from other processes
	for _, dep := range d.dependsOn {
		if dep == event.GNSS { //TODO: fow now no subscription for pps
			dependingProcessStateMap.states[dep] = event.PTP_UNKNOWN
			// register to event notification from other processes
			d.subscriber = append(d.subscriber, &DpllSubscriber{source: dep, dpll: d, id: fmt.Sprintf("%s-%x", event.DPLL, d.clockId)})
		}
	}
	// register monitoring process to be called by event
	d.subscriber = append(d.subscriber, &DpllSubscriber{
		source: event.MONITORING,
		dpll:   d,
		id:     fmt.Sprintf("%s-%x", event.DPLL, d.clockId),
	})
	d.registerAllSubscriber()
}

func (d *DpllConfig) unRegisterAllSubscriber() {
	for _, s := range d.subscriber {
		event.StateRegisterer.Unregister(s)
	}
	d.subscriber = []*DpllSubscriber{}
}

func (d *DpllConfig) registerAllSubscriber() {
	for _, s := range d.subscriber {
		event.StateRegisterer.Register(s)
	}
}

// MonitorDpll monitors DPLL on the discovered API, if any
func (d *DpllConfig) MonitorDpll() {
	fmt.Println(d.apiType)
	if d.apiType == MOCK {
		return
	} else if d.apiType == SYSFS {
		go d.MonitorDpllSysfs()
		d.isMonitoring = true
	} else if d.apiType == NETLINK {
		go d.MonitorDpllNetlink()
		d.isMonitoring = true
	} else {
		glog.Errorf("dpll monitoring is not possible, both sysfs is not available or netlink implementation is not present")
		return
	}
}

// stateDecision
func (d *DpllConfig) stateDecision() {
	dpllStatus := d.getWorseState(d.phaseStatus, d.frequencyStatus)
	glog.Infof("%s-dpll decision: Status %d, Offset %d, In spec %v, Source lost %v, On holdover %v",
		d.iface, dpllStatus, d.phaseOffset, d.inSpec, d.sourceLost, d.onHoldover)
	if d.hasPPSAsSource() {
		d.sourceLost = false //TODO: do not have a handler to catch pps source , so we will set to false
		// and to true if state changes to holdover for source PPS based DPLL
	}
	switch dpllStatus {
	case DPLL_FREERUN, DPLL_INVALID, DPLL_UNKNOWN:
		d.inSpec = true
		if d.hasGNSSAsSource() && d.onHoldover {
			d.holdoverCloseCh <- true
		} else if d.hasPPSAsSource() {
			d.sourceLost = true
		}
		d.state = event.PTP_FREERUN
		d.phaseOffset = FaultyPhaseOffset
		glog.Infof("dpll is in FREERUN, state is FREERUN (%s)", d.iface)
		d.sendDpllEvent()
	case DPLL_LOCKED:
		if !d.sourceLost && d.isOffsetInRange() { // right now pps always source not lost
			if d.hasGNSSAsSource() && d.onHoldover {
				d.holdoverCloseCh <- true
			}
			glog.Infof("dpll is locked, offset is in range, state is LOCKED(%s)", d.iface)
			d.state = event.PTP_LOCKED
		} else { // what happens if source is lost and DPLL is locked? goto holdover?
			glog.Infof("dpll is locked, offset is out of range, state is FREERUN(%s)", d.iface)
			d.state = event.PTP_FREERUN
		}
		d.inSpec = true
		d.sendDpllEvent()
	case DPLL_LOCKED_HO_ACQ, DPLL_HOLDOVER:
		if d.hasPPSAsSource() {
			if dpllStatus == DPLL_HOLDOVER {
				d.state = event.PTP_FREERUN // pps when moved to HOLDOVER we declare freerun
				d.phaseOffset = FaultyPhaseOffset
				d.sourceLost = true
			} else if dpllStatus == DPLL_LOCKED_HO_ACQ && d.isOffsetInRange() {
				d.state = event.PTP_LOCKED
				d.sourceLost = false
			} else {
				d.state = event.PTP_FREERUN
				d.sourceLost = false
				d.phaseOffset = FaultyPhaseOffset
			}
		} else if !d.sourceLost && d.isOffsetInRange() {
			glog.Infof("dpll is locked, source is not lost, offset is in range, state is DPLL_LOCKED_HO_ACQ or DPLL_HOLDOVER(%s)", d.iface)
			if d.hasGNSSAsSource() {
				if d.onHoldover {
					d.holdoverCloseCh <- true
					glog.Infof("closing holdover for %s", d.iface)
				}
				d.inSpec = true
			}
			d.state = event.PTP_LOCKED
		} else if d.sourceLost && d.inSpec {
			glog.Infof("dpll state is DPLL_LOCKED_HO_ACQ or DPLL_HOLDOVER,  source is lost, state is HOLDOVER(%s)", d.iface)
			if !d.onHoldover {
				d.holdoverCloseCh = make(chan bool)
				d.onHoldover = true
				d.state = event.PTP_HOLDOVER
				go d.holdover()
			}
			return // sending events are handled by holdover return here
		} else if !d.inSpec {
			glog.Infof("dpll is not in spec ,state is DPLL_LOCKED_HO_ACQ or DPLL_HOLDOVER, offset is out of range, state is FREERUN(%s)", d.iface)
			d.state = event.PTP_FREERUN
			d.phaseOffset = FaultyPhaseOffset
		}
		d.sendDpllEvent()
	}
}

// sendDpllEvent sends DPLL event to the event channel
func (d *DpllConfig) sendDpllEvent() {
	if d.processConfig.EventChannel == nil {
		glog.Info("Skip event - dpll is not yet initialized")
		return
	}
	eventData := event.EventChannel{
		ProcessName: event.DPLL,
		State:       d.state,
		IFace:       d.iface,
		CfgName:     d.processConfig.ConfigName,
		Values: map[event.ValueType]interface{}{
			event.FREQUENCY_STATUS: d.frequencyStatus,
			event.OFFSET:           d.phaseOffset,
			event.PHASE_STATUS:     d.phaseStatus,
			event.PPS_STATUS: func() int {
				if d.sourceLost {
					return 0
				}
				return 1
			}(),
		},
		ClockType:  d.processConfig.ClockType,
		Time:       time.Now().UnixMilli(),
		OutOfSpec:  !d.inSpec,
		SourceLost: d.sourceLost, // Here source lost is either GNSS or PPS , nmea string lost is captured by ts2phc
		WriteToLog: true,
		Reset:      false,
	}
	select {
	case d.processConfig.EventChannel <- eventData:
		glog.Infof("dpll event sent for (%s)", d.iface)
	default:
		glog.Infof("failed to send dpll event, retying.(%s)", d.iface)
	}
}

// MonitorDpllSysfs ... monitor dpPPS_STATUSll events
func (d *DpllConfig) MonitorDpllSysfs() {
	defer func() {
		if r := recover(); r != nil {
			glog.Warning("Recovered from panic from MonitorDpll: ", r)
			// Handle the closed channel panic if necessary
		}
	}()

	d.ticker = time.NewTicker(monitoringInterval)

	// Determine DPLL state
	d.inSpec = true

	for {
		select {
		case <-d.exitCh:
			glog.Infof("Terminating sysfs DPLL monitoring")
			d.sendDpllTerminationEvent()

			if d.onHoldover {
				close(d.holdoverCloseCh) // Cancel any holdover
			}
			return
		case <-d.ticker.C:
			// Monitor DPLL
			d.phaseStatus, d.frequencyStatus, d.phaseOffset = d.sysfs(d.iface)
			d.stateDecision()
		}
	}
}

// sendDpllTerminationEvent sends a termination event to the event channel
func (d *DpllConfig) sendDpllTerminationEvent() {
	select {
	case d.processConfig.EventChannel <- event.EventChannel{
		ProcessName: event.DPLL,
		IFace:       d.iface,
		CfgName:     d.processConfig.ConfigName,
		ClockType:   d.processConfig.ClockType,
		Time:        time.Now().UnixMilli(),
		Reset:       true,
	}:
	default:
		glog.Error("failed to send dpll terminated event")
	}

	// unregister from event notification from other processes
	d.unRegisterAllSubscriber()
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

func (d *DpllConfig) holdover() {
	start := time.Now()
	ticker := time.NewTicker(1 * time.Second)
	defer func() {
		ticker.Stop()
		d.onHoldover = false
		d.stateDecision()
	}()
	d.sendDpllEvent()
	for timeout := time.After(time.Duration(d.timer * int64(time.Second))); ; {
		select {
		case <-ticker.C:
			//calculate offset
			d.phaseOffset = int64(math.Round((d.slope / 1000) * float64(time.Since(start).Seconds())))
			glog.Infof("(%s) time since holdover start %f, offset %d nanosecond holdover %s", d.iface, float64(time.Since(start).Seconds()), d.phaseOffset, strconv.FormatBool(d.onHoldover))
			d.sendDpllEvent()
			if !d.isLocalOffsetInRange() { // when holdover verify with local max holdover not with regular threshold
				glog.Infof("offset is out of range: %v, max %v",
					d.phaseOffset, d.LocalMaxHoldoverOffSet)
				return
			}
		case <-timeout:
			d.inSpec = false // in HO, Out of spec
			d.state = event.PTP_FREERUN
			d.phaseOffset = FaultyPhaseOffset
			glog.Infof("holdover timer %d expired", d.timer)
			d.sendDpllEvent()
			return
		case <-d.holdoverCloseCh:
			glog.Info("holdover was closed")
			d.inSpec = true // if someone else is closing then it should be back in spec (if it was not in spec before)
			return
		}
	}
}

func (d *DpllConfig) isLocalOffsetInRange() bool {
	if d.phaseOffset <= int64(d.LocalMaxHoldoverOffSet) {
		return true
	}
	glog.Infof("in holdover- dpll offset is out of range:  max %d, current %d",
		d.LocalMaxHoldoverOffSet, d.phaseOffset)
	return false
}

func (d *DpllConfig) isOffsetInRange() bool {
	if d.phaseOffset <= d.processConfig.GMThreshold.Max && d.phaseOffset >= d.processConfig.GMThreshold.Min {
		return true
	}
	glog.Infof("dpll offset out of range: min %d, max %d, current %d",
		d.processConfig.GMThreshold.Min, d.processConfig.GMThreshold.Max, d.phaseOffset)
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
		return DPLL_INVALID, DPLL_INVALID, 0
	}

	readInt64FromFile := func(path string) (int64, error) {
		content, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		contentStr := strings.TrimSpace(string(content))
		value, err := strconv.ParseInt(contentStr, 10, 64)
		if err != nil {
			return 0, err
		}
		return value, nil
	}

	frequencyStateStr := fmt.Sprintf("/sys/class/net/%s/device/dpll_0_state", iface)
	phaseStateStr := fmt.Sprintf("/sys/class/net/%s/device/dpll_1_state", iface)
	phaseOffsetStr := fmt.Sprintf("/sys/class/net/%s/device/dpll_1_offset", iface)

	frequencyState, err := readInt64FromFile(frequencyStateStr)
	if err != nil {
		glog.Errorf("Error reading frequency state from %s: %v", frequencyStateStr, err)
	}

	phaseState, err = readInt64FromFile(phaseStateStr)
	if err != nil {
		glog.Errorf("Error reading phase state from %s: %v %s", phaseStateStr, err, d.iface)
	}

	phaseOffset, err = readInt64FromFile(phaseOffsetStr)
	if err != nil {
		glog.Errorf("Error reading phase offset from %s: %v %s", phaseOffsetStr, err, d.iface)
	} else {
		phaseOffset /= 100 // Convert to nanoseconds from tens of picoseconds (divide by 100)
	}
	return phaseState, frequencyState, phaseOffset
}

package main

import (
	"log"
	"sync"
	"time"
)

// DPLL states match the linuxptp-daemon's frequency/phase status values
// and the clock classes used in T-GM testing.
//
//	frequency_status / phase_status:
//	  1 = FREERUN, 2 = HOLDOVER, 3 = LOCKED_HO_ACQ, 4 = HOLDOVER
//	Clock classes (ITU-T G.8275.1):
//	  6 = locked to primary reference
//	  7 = holdover (in-spec)
//	  248 = freerun / degraded
type DPLLState int

const (
	DPLLFreerun DPLLState = iota
	DPLLHoldover
	DPLLLocked
)

func (s DPLLState) String() string {
	switch s {
	case DPLLFreerun:
		return "FREERUN"
	case DPLLHoldover:
		return "HOLDOVER"
	case DPLLLocked:
		return "LOCKED"
	default:
		return "UNKNOWN"
	}
}

// FrequencyStatus returns the dpll frequency_status value for Prometheus metrics.
func (s DPLLState) FrequencyStatus() int {
	switch s {
	case DPLLLocked:
		return 3 // LOCKED_HO_ACQ
	case DPLLHoldover:
		return 4 // HOLDOVER
	default:
		return 1 // FREERUN
	}
}

// PhaseStatus returns the dpll phase_status value for Prometheus metrics.
func (s DPLLState) PhaseStatus() int {
	return s.FrequencyStatus()
}

// ClockClass returns the ITU-T G.8275.1 clock class for this DPLL state.
func (s DPLLState) ClockClass() int {
	switch s {
	case DPLLLocked:
		return 6
	case DPLLHoldover:
		return 7
	default:
		return 248
	}
}

// DPLLSimulator models the DPLL hardware state machine.
//
// In real hardware, the DPLL locks to the GNSS 1PPS signal. When GNSS
// is lost, the DPLL transitions through HOLDOVER (coasting on the last
// good frequency) to FREERUN after the holdover timeout expires.
//
// The simulator derives its state from the parent SimState's signal:
//   - GNSS active → DPLL LOCKED (after a brief acquisition period)
//   - GNSS lost → DPLL HOLDOVER → DPLL FREERUN (after holdoverTimeout)
type DPLLSimulator struct {
	mu sync.RWMutex

	state           DPLLState
	holdoverTimeout time.Duration // how long to stay in holdover before freerun
	signalLostAt    time.Time     // when GNSS signal was lost (zero if not lost)
	parentState     *SimState     // reads GNSS signal state
	stop            chan struct{}
	done            chan struct{}
}

// DPLLStateView is the JSON-serializable DPLL state snapshot.
type DPLLStateView struct {
	State           string `json:"state"`
	FrequencyStatus int    `json:"frequencyStatus"`
	PhaseStatus     int    `json:"phaseStatus"`
	ClockClass      int    `json:"clockClass"`
	PPSStatus       int    `json:"ppsStatus"`
}

// NewDPLLSimulator creates a DPLL simulator that derives state from the
// parent GNSS SimState. The holdoverTimeout controls how long the DPLL
// stays in HOLDOVER before transitioning to FREERUN.
func NewDPLLSimulator(parent *SimState, holdoverTimeout time.Duration) *DPLLSimulator {
	return &DPLLSimulator{
		state:           DPLLLocked,
		holdoverTimeout: holdoverTimeout,
		parentState:     parent,
		stop:            make(chan struct{}),
		done:            make(chan struct{}),
	}
}

// Snapshot returns the current DPLL state as a JSON-safe view.
func (d *DPLLSimulator) Snapshot() DPLLStateView {
	d.mu.RLock()
	defer d.mu.RUnlock()
	pps := 1
	if d.state == DPLLFreerun {
		pps = 0
	}
	return DPLLStateView{
		State:           d.state.String(),
		FrequencyStatus: d.state.FrequencyStatus(),
		PhaseStatus:     d.state.PhaseStatus(),
		ClockClass:      d.state.ClockClass(),
		PPSStatus:       pps,
	}
}

// Run starts the DPLL state machine loop. It polls the parent GNSS signal
// state every 500ms and transitions accordingly.
func (d *DPLLSimulator) Run() {
	defer close(d.done)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	log.Println("DPLL simulator started")

	for {
		select {
		case <-d.stop:
			log.Println("DPLL simulator stopped")
			return
		case now := <-ticker.C:
			d.update(now)
		}
	}
}

// Stop signals the DPLL loop to exit.
func (d *DPLLSimulator) Stop() {
	close(d.stop)
	<-d.done
}

func (d *DPLLSimulator) update(now time.Time) {
	gnssSnap := d.parentState.Snapshot()
	gnssActive := gnssSnap.SignalActive

	d.mu.Lock()
	defer d.mu.Unlock()

	prevState := d.state

	switch d.state {
	case DPLLLocked:
		if !gnssActive {
			d.state = DPLLHoldover
			d.signalLostAt = now
			log.Printf("DPLL: LOCKED → HOLDOVER (GNSS signal lost)")
		}

	case DPLLHoldover:
		if gnssActive {
			d.state = DPLLLocked
			d.signalLostAt = time.Time{}
			log.Printf("DPLL: HOLDOVER → LOCKED (GNSS signal restored)")
		} else if now.Sub(d.signalLostAt) >= d.holdoverTimeout {
			d.state = DPLLFreerun
			log.Printf("DPLL: HOLDOVER → FREERUN (holdover timeout expired after %v)", d.holdoverTimeout)
		}

	case DPLLFreerun:
		if gnssActive {
			d.state = DPLLLocked
			d.signalLostAt = time.Time{}
			log.Printf("DPLL: FREERUN → LOCKED (GNSS signal restored)")
		}
	}

	if d.state != prevState {
		log.Printf("DPLL state: %s (CC%d, freq=%d, phase=%d)",
			d.state, d.state.ClockClass(), d.state.FrequencyStatus(), d.state.PhaseStatus())
	}
}

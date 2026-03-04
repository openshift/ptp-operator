package main

import (
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// GPSFix represents the U-blox NAV-STATUS gpsFix values that the
// cloud-event-proxy's GetGPSFixState() maps to O-RAN sync states.
type GPSFix int

const (
	GPSFixNoFix         GPSFix = 0 // No fix → FAILURE_NOFIX
	GPSFixDeadReckoning GPSFix = 1 // Dead reckoning only → ACQUIRING_SYNC
	GPSFix2D            GPSFix = 2 // 2D fix → ACQUIRING_SYNC
	GPSFix3D            GPSFix = 3 // 3D fix → SYNCHRONIZED (if locked)
	GPSFixGPSDR         GPSFix = 4 // GPS + dead reckoning → SYNCHRONIZED
	GPSFixTimeOnly      GPSFix = 5 // Time-only fix → SYNCHRONIZED
)

// SimState holds the mutable state of the GNSS simulator, protected by a
// mutex so the HTTP API can modify it concurrently with the generation loop.
type SimState struct {
	mu sync.RWMutex

	// Signal controls whether the receiver reports a valid fix.
	SignalActive bool `json:"signalActive"`

	// GPSFix is the fix type reported when signal is active.
	// Mapped by cloud-event-proxy to O-RAN sync states.
	GPSFix GPSFix `json:"gpsFix"`

	// Satellites is the number of satellites in use (GNGGA field).
	Satellites int `json:"satellites"`

	// Position is the reported geographic position.
	Position NMEAPosition `json:"position"`

	// HDOP (Horizontal Dilution of Precision) controls GNGGA signal quality.
	// Lower values = better quality (0.5–1.0 excellent, 2.0–5.0 moderate, >10 poor).
	HDOP float64 `json:"hdop"`

	// OffsetNs is an artificial time offset added to the NMEA timestamps.
	// Allows testing threshold-based LOCKED/FREERUN transitions.
	OffsetNs int64 `json:"offsetNs"`
}

// SimStateView is a mutex-free snapshot for JSON serialization.
type SimStateView struct {
	SignalActive bool         `json:"signalActive"`
	GPSFix       GPSFix       `json:"gpsFix"`
	Satellites   int          `json:"satellites"`
	Position     NMEAPosition `json:"position"`
	HDOP         float64      `json:"hdop"`
	OffsetNs     int64        `json:"offsetNs"`
}

// Snapshot returns a mutex-free copy of the current state, safe for
// JSON marshalling without triggering the copylock vet check.
func (s *SimState) Snapshot() SimStateView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return SimStateView{
		SignalActive: s.SignalActive,
		GPSFix:       s.GPSFix,
		Satellites:   s.Satellites,
		Position:     s.Position,
		HDOP:         s.HDOP,
		OffsetNs:     s.OffsetNs,
	}
}

func (s *SimState) SetSignal(active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SignalActive = active
}

func (s *SimState) SetGPSFix(fix GPSFix) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.GPSFix = fix
}

func (s *SimState) SetSatellites(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Satellites = n
}

func (s *SimState) SetHDOP(hdop float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.HDOP = hdop
}

func (s *SimState) SetOffset(ns int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.OffsetNs = ns
}

func (s *SimState) SetPosition(pos NMEAPosition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Position = pos
}

// DefaultState returns a SimState representing a healthy GNSS receiver
// with a 3D fix, 12 satellites, and a position near Red Hat Tower, Raleigh.
func DefaultState() *SimState {
	return &SimState{
		SignalActive: true,
		GPSFix:       GPSFix3D,
		Satellites:   12,
		HDOP:         0.9,
		Position: NMEAPosition{
			LatDeg:    35.7796,
			LonDeg:    -78.6382,
			AltMeters: 96.0,
		},
		OffsetNs: 0,
	}
}

// Simulator is the main GNSS sentence generation engine.
type Simulator struct {
	State   *SimState
	Writers []io.Writer // all outputs receive every sentence
	stop    chan struct{}
	done    chan struct{}
}

// NewSimulator creates a simulator writing to the given outputs.
// If no writers are provided, os.Stdout is used.
func NewSimulator(state *SimState, writers ...io.Writer) *Simulator {
	if len(writers) == 0 {
		writers = []io.Writer{os.Stdout}
	}
	return &Simulator{
		State:   state,
		Writers: writers,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Run starts the 1 Hz generation loop. It blocks until Stop() is called.
// Each tick generates GNRMC, GNGGA, and GPZDA sentences and writes them
// to all configured writers.
func (sim *Simulator) Run() {
	defer close(sim.done)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	log.Println("GNSS simulator started, generating NMEA at 1 Hz")

	for {
		select {
		case <-sim.stop:
			log.Println("GNSS simulator stopped")
			return
		case now := <-ticker.C:
			sim.generate(now)
		}
	}
}

// Stop signals the generation loop to exit and waits for it to finish.
func (sim *Simulator) Stop() {
	close(sim.stop)
	<-sim.done
}

func (sim *Simulator) generate(now time.Time) {
	snap := sim.State.Snapshot()

	// Apply artificial time offset for testing threshold transitions
	t := now.Add(time.Duration(snap.OffsetNs) * time.Nanosecond)

	active := snap.SignalActive
	sats := snap.Satellites
	hdop := snap.HDOP
	gpsFix := snap.GPSFix
	if !active {
		sats = 0
		hdop = 99.9
		gpsFix = GPSFixNoFix
	}

	sentences := []string{
		GenerateGNRMC(t, snap.Position, active),
		GenerateGNGGA(t, snap.Position, active, sats, hdop, gpsFix),
		GenerateGPZDA(t),
	}

	data := []byte(strings.Join(sentences, ""))

	for _, w := range sim.Writers {
		if _, err := w.Write(data); err != nil {
			log.Printf("write error: %v", err)
		}
	}
}

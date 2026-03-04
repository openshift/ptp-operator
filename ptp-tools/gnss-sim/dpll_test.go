package main

import (
	"testing"
	"time"
)

func TestDPLLStateString(t *testing.T) {
	tests := []struct {
		state DPLLState
		str   string
		freq  int
		phase int
		cc    int
	}{
		{DPLLLocked, "LOCKED", 3, 3, 6},
		{DPLLHoldover, "HOLDOVER", 4, 4, 7},
		{DPLLFreerun, "FREERUN", 1, 1, 248},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.str {
			t.Errorf("DPLLState(%d).String() = %q, want %q", tt.state, got, tt.str)
		}
		if got := tt.state.FrequencyStatus(); got != tt.freq {
			t.Errorf("DPLLState(%d).FrequencyStatus() = %d, want %d", tt.state, got, tt.freq)
		}
		if got := tt.state.PhaseStatus(); got != tt.phase {
			t.Errorf("DPLLState(%d).PhaseStatus() = %d, want %d", tt.state, got, tt.phase)
		}
		if got := tt.state.ClockClass(); got != tt.cc {
			t.Errorf("DPLLState(%d).ClockClass() = %d, want %d", tt.state, got, tt.cc)
		}
	}
}

func TestDPLLTransitionLockedToHoldoverToFreerun(t *testing.T) {
	state := DefaultState()
	dpllSim := NewDPLLSimulator(state, 2*time.Second)
	go dpllSim.Run()
	defer dpllSim.Stop()

	// Let the ticker fire at least once while signal is active
	time.Sleep(700 * time.Millisecond)
	snap := dpllSim.Snapshot()
	if snap.State != "LOCKED" {
		t.Fatalf("expected LOCKED, got %s", snap.State)
	}

	// Simulate GNSS signal loss — wait >500ms for the next poll
	state.SetSignal(false)
	time.Sleep(700 * time.Millisecond)
	snap = dpllSim.Snapshot()
	if snap.State != "HOLDOVER" {
		t.Fatalf("expected HOLDOVER after signal loss, got %s", snap.State)
	}
	if snap.ClockClass != 7 {
		t.Fatalf("expected CC7 in holdover, got CC%d", snap.ClockClass)
	}

	// Wait for holdover timeout (2s) + polling margin
	time.Sleep(2500 * time.Millisecond)
	snap = dpllSim.Snapshot()
	if snap.State != "FREERUN" {
		t.Fatalf("expected FREERUN after holdover timeout, got %s", snap.State)
	}
	if snap.ClockClass != 248 {
		t.Fatalf("expected CC248 in freerun, got CC%d", snap.ClockClass)
	}
}

func TestDPLLRecoveryFromHoldover(t *testing.T) {
	state := DefaultState()
	dpllSim := NewDPLLSimulator(state, 10*time.Second)
	go dpllSim.Run()
	defer dpllSim.Stop()

	time.Sleep(700 * time.Millisecond)

	// Lose signal
	state.SetSignal(false)
	time.Sleep(700 * time.Millisecond)
	snap := dpllSim.Snapshot()
	if snap.State != "HOLDOVER" {
		t.Fatalf("expected HOLDOVER, got %s", snap.State)
	}

	// Restore signal before holdover timeout
	state.SetSignal(true)
	time.Sleep(700 * time.Millisecond)
	snap = dpllSim.Snapshot()
	if snap.State != "LOCKED" {
		t.Fatalf("expected LOCKED after recovery, got %s", snap.State)
	}
	if snap.ClockClass != 6 {
		t.Fatalf("expected CC6 after recovery, got CC%d", snap.ClockClass)
	}
}

func TestDPLLRecoveryFromFreerun(t *testing.T) {
	state := DefaultState()
	state.SetSignal(false)
	dpllSim := NewDPLLSimulator(state, 1*time.Second)
	go dpllSim.Run()
	defer dpllSim.Stop()

	// Let it go through holdover to freerun (1s holdover + 500ms poll margin)
	time.Sleep(2500 * time.Millisecond)
	snap := dpllSim.Snapshot()
	if snap.State != "FREERUN" {
		t.Fatalf("expected FREERUN, got %s", snap.State)
	}

	// Restore signal
	state.SetSignal(true)
	time.Sleep(700 * time.Millisecond)
	snap = dpllSim.Snapshot()
	if snap.State != "LOCKED" {
		t.Fatalf("expected LOCKED after recovery from freerun, got %s", snap.State)
	}
}

func TestDPLLPPSStatus(t *testing.T) {
	state := DefaultState()
	dpllSim := NewDPLLSimulator(state, 1*time.Second)
	go dpllSim.Run()
	defer dpllSim.Stop()

	time.Sleep(700 * time.Millisecond)
	snap := dpllSim.Snapshot()
	if snap.PPSStatus != 1 {
		t.Fatalf("expected PPS=1 when locked, got %d", snap.PPSStatus)
	}

	state.SetSignal(false)
	// Wait for holdover (1s) + freerun transition + poll margin
	time.Sleep(2500 * time.Millisecond)
	snap = dpllSim.Snapshot()
	if snap.PPSStatus != 0 {
		t.Fatalf("expected PPS=0 in freerun, got %d", snap.PPSStatus)
	}
}

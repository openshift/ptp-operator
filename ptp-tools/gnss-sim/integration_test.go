package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sb *syncBuffer) Write(p []byte) (int, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *syncBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

func (sb *syncBuffer) Reset() {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.buf.Reset()
}

func (sb *syncBuffer) Len() int {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Len()
}

// ---------------------------------------------------------------------------
// Simulator → Writer integration
// ---------------------------------------------------------------------------

func TestSimulatorWritesNMEASentences(t *testing.T) {
	var buf bytes.Buffer
	state := DefaultState()
	sim := NewSimulator(state, &buf)
	go sim.Run()
	time.Sleep(1200 * time.Millisecond)
	sim.Stop()

	out := buf.String()
	for _, prefix := range []string{"$GNRMC,", "$GNGGA,", "$GPZDA,"} {
		if !strings.Contains(out, prefix) {
			t.Errorf("expected %s in output, got:\n%s", prefix, out)
		}
	}
}

func TestSimulatorNoFixOnSignalLoss(t *testing.T) {
	var buf bytes.Buffer
	state := DefaultState()
	state.SetSignal(false)
	sim := NewSimulator(state, &buf)
	go sim.Run()
	time.Sleep(1200 * time.Millisecond)
	sim.Stop()

	out := buf.String()
	if !strings.Contains(out, ",V,") {
		t.Error("GNRMC should report void (V) status when signal is lost")
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "$GNGGA,") {
			fields := strings.Split(line, ",")
			if len(fields) > 6 && fields[6] != "0" {
				t.Errorf("GNGGA fix quality should be 0 on signal loss, got %s", fields[6])
			}
		}
	}
}

func TestSimulatorMultipleWriters(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	state := DefaultState()
	sim := NewSimulator(state, &buf1, &buf2)
	go sim.Run()
	time.Sleep(1200 * time.Millisecond)
	sim.Stop()

	if buf1.Len() == 0 {
		t.Error("writer 1 received no data")
	}
	if buf2.Len() == 0 {
		t.Error("writer 2 received no data")
	}
	if buf1.String() != buf2.String() {
		t.Error("both writers should receive identical data")
	}
}

func TestSimulatorAppliesTimeOffset(t *testing.T) {
	var buf bytes.Buffer
	state := DefaultState()
	const offsetSec = 5
	state.SetOffset(offsetSec * 1e9)

	sim := NewSimulator(state, &buf)
	go sim.Run()
	time.Sleep(1200 * time.Millisecond)
	sim.Stop()

	out := buf.String()
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "$GNRMC,") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 2 || len(fields[1]) < 6 {
			continue
		}
		hhmmss := fields[1][:6]
		nmeaTime, err := time.Parse("150405", hhmmss)
		if err != nil {
			t.Fatalf("failed to parse NMEA time %q: %v", hhmmss, err)
		}
		nowHHMMSS := time.Now().UTC().Format("150405")
		wallTime, _ := time.Parse("150405", nowHHMMSS)
		diff := nmeaTime.Sub(wallTime).Seconds()
		if diff < float64(offsetSec-2) || diff > float64(offsetSec+2) {
			t.Errorf("NMEA time %s shows offset ~%.0fs from wall clock, expected ~%ds (±2s tolerance)",
				hhmmss, diff, offsetSec)
		}
		return
	}
	t.Error("no GNRMC sentence found in output")
}

// ---------------------------------------------------------------------------
// DPLL + Simulator end-to-end: signal loss → holdover → freerun → recovery
// ---------------------------------------------------------------------------

func TestDPLLFullCycleWithSimulator(t *testing.T) {
	buf := &syncBuffer{}
	state := DefaultState()
	sim := NewSimulator(state, buf)
	dpllSim := NewDPLLSimulator(state, 1*time.Second, "")

	go sim.Run()
	go dpllSim.Run()
	defer sim.Stop()
	defer dpllSim.Stop()

	// Phase 1: locked
	time.Sleep(700 * time.Millisecond)
	snap := dpllSim.Snapshot()
	assertDPLLState(t, snap, "LOCKED", 6, 3, 3, 1)

	// Phase 2: signal loss → holdover
	state.SetSignal(false)
	time.Sleep(700 * time.Millisecond)
	snap = dpllSim.Snapshot()
	assertDPLLState(t, snap, "HOLDOVER", 7, 4, 4, 1)

	// Verify NMEA reflects signal loss. Simulator.Run emits ~1 Hz at a
	// phase independent of the test clock, so poll instead of a fixed sleep.
	if !waitFor(2*time.Second, func() bool { return strings.Contains(buf.String(), ",V,") }) {
		t.Error("NMEA should report void after signal loss")
	}

	// Phase 3: holdover timeout → freerun
	time.Sleep(1500 * time.Millisecond)
	snap = dpllSim.Snapshot()
	assertDPLLState(t, snap, "FREERUN", 248, 1, 1, 0)

	// Phase 4: signal restore → locked
	buf.Reset()
	state.SetSignal(true)
	time.Sleep(700 * time.Millisecond)
	snap = dpllSim.Snapshot()
	assertDPLLState(t, snap, "LOCKED", 6, 3, 3, 1)

	// Verify NMEA reflects active signal
	if !waitFor(2*time.Second, func() bool { return strings.Contains(buf.String(), ",A,") }) {
		t.Error("NMEA should report active after signal restore")
	}
}

func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}

func assertDPLLState(t *testing.T, snap DPLLStateView, state string, cc, freq, phase, pps int) {
	t.Helper()
	if snap.State != state {
		t.Errorf("expected state %s, got %s", state, snap.State)
	}
	if snap.ClockClass != cc {
		t.Errorf("expected CC%d, got CC%d", cc, snap.ClockClass)
	}
	if snap.FrequencyStatus != freq {
		t.Errorf("expected freq=%d, got %d", freq, snap.FrequencyStatus)
	}
	if snap.PhaseStatus != phase {
		t.Errorf("expected phase=%d, got %d", phase, snap.PhaseStatus)
	}
	if snap.PPSStatus != pps {
		t.Errorf("expected PPS=%d, got %d", pps, snap.PPSStatus)
	}
}

// ---------------------------------------------------------------------------
// API integration tests
// ---------------------------------------------------------------------------

func TestAPIHealth(t *testing.T) {
	state := DefaultState()
	dpllSim := NewDPLLSimulator(state, 5*time.Second, "")
	go dpllSim.Run()
	defer dpllSim.Stop()

	api := NewAPIServer(state, dpllSim)
	srv := httptest.NewServer(api.mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestAPIStatusReflectsState(t *testing.T) {
	state := DefaultState()
	dpllSim := NewDPLLSimulator(state, 5*time.Second, "")
	go dpllSim.Run()
	defer dpllSim.Stop()

	api := NewAPIServer(state, dpllSim)
	srv := httptest.NewServer(api.mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var status GNSSSimStatusView
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("failed to decode status response: %v", err)
	}
	if !status.SignalActive {
		t.Error("default state should have active signal")
	}
	if status.Satellites != 12 {
		t.Errorf("expected 12 satellites, got %d", status.Satellites)
	}
}

type GNSSSimStatusView = SimStateView

func TestAPIDPLLStatusReflectsState(t *testing.T) {
	state := DefaultState()
	dpllSim := NewDPLLSimulator(state, 5*time.Second, "")
	go dpllSim.Run()
	defer dpllSim.Stop()
	time.Sleep(600 * time.Millisecond)

	api := NewAPIServer(state, dpllSim)
	srv := httptest.NewServer(api.mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/dpll")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var dpllState DPLLStateView
	if err := json.NewDecoder(resp.Body).Decode(&dpllState); err != nil {
		t.Fatalf("failed to decode DPLL state response: %v", err)
	}
	if dpllState.State != "LOCKED" {
		t.Errorf("expected LOCKED, got %s", dpllState.State)
	}
	if dpllState.ClockClass != 6 {
		t.Errorf("expected CC6, got CC%d", dpllState.ClockClass)
	}
}

func TestAPISignalLossTriggersHoldover(t *testing.T) {
	state := DefaultState()
	dpllSim := NewDPLLSimulator(state, 5*time.Second, "")
	go dpllSim.Run()
	defer dpllSim.Stop()
	time.Sleep(600 * time.Millisecond)

	api := NewAPIServer(state, dpllSim)
	srv := httptest.NewServer(api.mux)
	defer srv.Close()

	// Trigger signal loss
	resp, err := http.Post(srv.URL+"/api/signal/loss", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("signal/loss expected 200, got %d", resp.StatusCode)
	}

	// Verify simulator state
	snap := state.Snapshot()
	if snap.SignalActive {
		t.Error("signal should be inactive after loss")
	}
	if snap.GPSFix != GPSFixNoFix {
		t.Errorf("GPS fix should be NoFix, got %d", snap.GPSFix)
	}

	// Wait for DPLL to transition
	time.Sleep(700 * time.Millisecond)
	dpllSnap := dpllSim.Snapshot()
	if dpllSnap.State != "HOLDOVER" {
		t.Errorf("DPLL expected HOLDOVER after signal loss, got %s", dpllSnap.State)
	}
}

func TestAPISignalRestoreRecovers(t *testing.T) {
	state := DefaultState()
	state.SetSignal(false)
	dpllSim := NewDPLLSimulator(state, 5*time.Second, "")
	go dpllSim.Run()
	defer dpllSim.Stop()
	time.Sleep(700 * time.Millisecond)

	api := NewAPIServer(state, dpllSim)
	srv := httptest.NewServer(api.mux)
	defer srv.Close()

	// Restore signal
	resp, err := http.Post(srv.URL+"/api/signal/restore", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	snap := state.Snapshot()
	if !snap.SignalActive {
		t.Error("signal should be active after restore")
	}
	if snap.GPSFix != GPSFix3D {
		t.Errorf("GPS fix should be 3D, got %d", snap.GPSFix)
	}
	if snap.Satellites != 12 {
		t.Errorf("satellites should be 12, got %d", snap.Satellites)
	}

	time.Sleep(700 * time.Millisecond)
	dpllSnap := dpllSim.Snapshot()
	if dpllSnap.State != "LOCKED" {
		t.Errorf("DPLL expected LOCKED after restore, got %s", dpllSnap.State)
	}
}

func TestAPIConfigUpdate(t *testing.T) {
	state := DefaultState()
	dpllSim := NewDPLLSimulator(state, 5*time.Second, "")
	go dpllSim.Run()
	defer dpllSim.Stop()

	api := NewAPIServer(state, dpllSim)
	srv := httptest.NewServer(api.mux)
	defer srv.Close()

	body := `{"satellites": 8, "hdop": 2.5, "gpsFix": 5}`
	resp, err := http.Post(srv.URL+"/api/config", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	snap := state.Snapshot()
	if snap.Satellites != 8 {
		t.Errorf("expected 8 satellites, got %d", snap.Satellites)
	}
	if snap.HDOP != 2.5 {
		t.Errorf("expected HDOP 2.5, got %f", snap.HDOP)
	}
	if snap.GPSFix != GPSFixTimeOnly {
		t.Errorf("expected GPSFix 5, got %d", snap.GPSFix)
	}
	// signal should remain active (not specified in config)
	if !snap.SignalActive {
		t.Error("signal should remain active when not modified")
	}
}

func TestAPIConfigPartialUpdate(t *testing.T) {
	state := DefaultState()
	dpllSim := NewDPLLSimulator(state, 5*time.Second, "")
	go dpllSim.Run()
	defer dpllSim.Stop()

	api := NewAPIServer(state, dpllSim)
	srv := httptest.NewServer(api.mux)
	defer srv.Close()

	body := `{"offsetNs": 1000000}`
	resp, err := http.Post(srv.URL+"/api/config", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	snap := state.Snapshot()
	if snap.OffsetNs != 1000000 {
		t.Errorf("expected offset 1000000, got %d", snap.OffsetNs)
	}
	if snap.Satellites != 12 {
		t.Error("satellites should be unchanged")
	}
	if !snap.SignalActive {
		t.Error("signal should be unchanged")
	}
}

func TestAPIMethodNotAllowed(t *testing.T) {
	state := DefaultState()
	dpllSim := NewDPLLSimulator(state, 5*time.Second, "")
	go dpllSim.Run()
	defer dpllSim.Stop()

	api := NewAPIServer(state, dpllSim)
	srv := httptest.NewServer(api.mux)
	defer srv.Close()

	tests := []struct {
		method string
		path   string
	}{
		{"POST", "/api/status"},
		{"POST", "/api/dpll"},
		{"GET", "/api/signal/loss"},
		{"GET", "/api/signal/restore"},
		{"GET", "/api/config"},
	}
	for _, tt := range tests {
		req, _ := http.NewRequest(tt.method, srv.URL+tt.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: expected 405, got %d", tt.method, tt.path, resp.StatusCode)
		}
	}
}

// ---------------------------------------------------------------------------
// SimState concurrency safety
// ---------------------------------------------------------------------------

func TestSimStateConcurrentAccess(t *testing.T) {
	state := DefaultState()
	done := make(chan struct{})

	go func() {
		for i := 0; i < 1000; i++ {
			state.SetSignal(i%2 == 0)
			state.SetSatellites(i % 20)
			state.SetGPSFix(GPSFix(i % 6))
			state.SetHDOP(float64(i) * 0.1)
			state.SetOffset(int64(i) * 100)
		}
		close(done)
	}()

	for i := 0; i < 1000; i++ {
		snap := state.Snapshot()
		_ = snap.SignalActive
		_ = snap.Satellites
		_ = snap.GPSFix
	}
	<-done
}

// ---------------------------------------------------------------------------
// DPLL snapshot JSON serialization
// ---------------------------------------------------------------------------

func TestDPLLSnapshotJSON(t *testing.T) {
	state := DefaultState()
	dpllSim := NewDPLLSimulator(state, 5*time.Second, "")
	go dpllSim.Run()
	defer dpllSim.Stop()
	time.Sleep(600 * time.Millisecond)

	snap := dpllSim.Snapshot()
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("failed to marshal DPLL snapshot: %v", err)
	}

	var decoded DPLLStateView
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal DPLL snapshot: %v", err)
	}

	if decoded.State != "LOCKED" {
		t.Errorf("expected LOCKED, got %s", decoded.State)
	}
	if decoded.ClockClass != 6 {
		t.Errorf("expected CC6, got CC%d", decoded.ClockClass)
	}
}

// ---------------------------------------------------------------------------
// Full T-GM log pattern matching (what the e2e test searches for)
// ---------------------------------------------------------------------------

func TestDPLLProducesExpectedLogPatterns(t *testing.T) {
	state := DefaultState()
	dpllSim := NewDPLLSimulator(state, 5*time.Second, "")
	go dpllSim.Run()
	defer dpllSim.Stop()
	time.Sleep(600 * time.Millisecond)

	snap := dpllSim.Snapshot()

	// The e2e test matches: dpll.*frequency_status 3 offset (.*?) phase_status 3
	if snap.FrequencyStatus != 3 {
		t.Errorf("locked DPLL must have frequency_status=3, got %d", snap.FrequencyStatus)
	}
	if snap.PhaseStatus != 3 {
		t.Errorf("locked DPLL must have phase_status=3, got %d", snap.PhaseStatus)
	}
	if snap.PPSStatus != 1 {
		t.Errorf("locked DPLL must have pps_status=1, got %d", snap.PPSStatus)
	}

	// After signal loss the e2e test expects clock class != 6
	state.SetSignal(false)
	time.Sleep(700 * time.Millisecond)
	snap = dpllSim.Snapshot()
	if snap.ClockClass == 6 {
		t.Error("DPLL clock class must not be 6 after signal loss")
	}
}

// ---------------------------------------------------------------------------
// API full signal loss/restore round-trip via HTTP (as ptphelper would call)
// ---------------------------------------------------------------------------

func TestAPIFullRoundTrip(t *testing.T) {
	state := DefaultState()
	dpllSim := NewDPLLSimulator(state, 1*time.Second, "")
	go dpllSim.Run()
	defer dpllSim.Stop()
	time.Sleep(600 * time.Millisecond)

	api := NewAPIServer(state, dpllSim)
	srv := httptest.NewServer(api.mux)
	defer srv.Close()

	// 1. Verify healthy + locked
	assertHTTPGet(t, srv.URL+"/health", http.StatusOK)
	dpllBody := getJSON(t, srv.URL+"/api/dpll")
	if !strings.Contains(dpllBody, `"state":"LOCKED"`) {
		t.Errorf("expected LOCKED in DPLL response: %s", dpllBody)
	}

	// 2. Trigger signal loss
	postAndExpect(t, srv.URL+"/api/signal/loss", http.StatusOK)
	time.Sleep(700 * time.Millisecond)

	dpllBody = getJSON(t, srv.URL+"/api/dpll")
	if !strings.Contains(dpllBody, `"state":"HOLDOVER"`) {
		t.Errorf("expected HOLDOVER: %s", dpllBody)
	}

	// 3. Wait for freerun
	time.Sleep(1500 * time.Millisecond)
	dpllBody = getJSON(t, srv.URL+"/api/dpll")
	if !strings.Contains(dpllBody, `"state":"FREERUN"`) {
		t.Errorf("expected FREERUN: %s", dpllBody)
	}
	if !strings.Contains(dpllBody, `"clockClass":248`) {
		t.Errorf("expected CC248 in freerun: %s", dpllBody)
	}

	// 4. Restore signal
	postAndExpect(t, srv.URL+"/api/signal/restore", http.StatusOK)
	time.Sleep(700 * time.Millisecond)

	dpllBody = getJSON(t, srv.URL+"/api/dpll")
	if !strings.Contains(dpllBody, `"state":"LOCKED"`) {
		t.Errorf("expected LOCKED after restore: %s", dpllBody)
	}
	if !strings.Contains(dpllBody, `"clockClass":6`) {
		t.Errorf("expected CC6 after restore: %s", dpllBody)
	}

	statusBody := getJSON(t, srv.URL+"/api/status")
	if !strings.Contains(statusBody, `"signalActive":true`) {
		t.Errorf("expected signalActive:true: %s", statusBody)
	}
}

func assertHTTPGet(t *testing.T, url string, expectedStatus int) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp.Body.Close()
	if resp.StatusCode != expectedStatus {
		t.Errorf("GET %s: expected %d, got %d", url, expectedStatus, resp.StatusCode)
	}
}

func getJSON(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return string(data)
}

func postAndExpect(t *testing.T, url string, expectedStatus int) {
	t.Helper()
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	resp.Body.Close()
	if resp.StatusCode != expectedStatus {
		t.Errorf("POST %s: expected %d, got %d", url, expectedStatus, resp.StatusCode)
	}
}

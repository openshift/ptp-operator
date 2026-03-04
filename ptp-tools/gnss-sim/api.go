package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// APIServer provides an HTTP control plane for the GNSS simulator.
// Tests use these endpoints to trigger signal loss/recovery scenarios
// (replacing the ubxtool commands used with real hardware).
type APIServer struct {
	state *SimState
	dpll  *DPLLSimulator
	mux   *http.ServeMux
}

// NewAPIServer creates the HTTP handler with all routes registered.
func NewAPIServer(state *SimState, dpll *DPLLSimulator) *APIServer {
	s := &APIServer{
		state: state,
		dpll:  dpll,
		mux:   http.NewServeMux(),
	}
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/api/status", s.handleStatus)
	s.mux.HandleFunc("/api/dpll", s.handleDPLLStatus)
	s.mux.HandleFunc("/api/signal/loss", s.handleSignalLoss)
	s.mux.HandleFunc("/api/signal/restore", s.handleSignalRestore)
	s.mux.HandleFunc("/api/config", s.handleConfig)
	return s
}

// ListenAndServe starts the HTTP server on the given address (e.g. ":9200").
func (s *APIServer) ListenAndServe(addr string) error {
	log.Printf("API server listening on %s", addr)
	return http.ListenAndServe(addr, s.mux)
}

// GET /health — simple health check for readiness probes.
func (s *APIServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

// GET /api/status — returns the current simulator state as JSON.
func (s *APIServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snap := s.state.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snap)
}

// GET /api/dpll — returns the current DPLL state machine status.
//
// The DPLL state automatically derives from the GNSS signal:
//
//	GNSS active → DPLL LOCKED (CC6, freq=3, phase=3)
//	GNSS lost   → DPLL HOLDOVER (CC7, freq=4, phase=4)
//	Timeout     → DPLL FREERUN (CC248, freq=1, phase=1)
func (s *APIServer) handleDPLLStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snap := s.dpll.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snap)
}

// POST /api/signal/loss — simulates GNSS signal loss.
//
// Equivalent to:
//
//	ubxtool -P 29.25 -w 1 -v 3 -z CFG-NAVSPG-INFIL_NCNOTHRS,50,1
//
// Sets signal inactive and GPS fix to 0 (NoFix), which causes:
//   - linuxptp-daemon: ts2phc reports nmea_status 0, servo s0
//   - cloud-event-proxy: publishes GnssStateChange with FAILURE_NOFIX
//   - DPLL: transitions LOCKED → HOLDOVER → FREERUN
//   - Clock class: 6 → 7 → 248
func (s *APIServer) handleSignalLoss(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.state.SetSignal(false)
	s.state.SetGPSFix(GPSFixNoFix)
	log.Println("API: signal loss triggered")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "signal_lost"})
}

// POST /api/signal/restore — restores GNSS signal.
//
// Equivalent to:
//
//	ubxtool -P 29.25 -w 1 -v 3 -z CFG-NAVSPG-INFIL_NCNOTHRS,0,1
//
// Sets signal active with 3D fix and 12 satellites.
func (s *APIServer) handleSignalRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.state.SetSignal(true)
	s.state.SetGPSFix(GPSFix3D)
	s.state.SetSatellites(12)
	log.Println("API: signal restored")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "signal_restored"})
}

// POST /api/config — updates simulator parameters.
//
// Accepts a JSON body with any subset of configurable fields:
//
//	{
//	  "signalActive": true,
//	  "gpsFix": 3,
//	  "satellites": 12,
//	  "hdop": 0.9,
//	  "offsetNs": 0,
//	  "position": { "latDeg": 35.78, "lonDeg": -78.64, "altMeters": 96.0 }
//	}
//
// Unspecified fields retain their current values.
func (s *APIServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req configRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if req.SignalActive != nil {
		s.state.SetSignal(*req.SignalActive)
	}
	if req.GPSFix != nil {
		s.state.SetGPSFix(GPSFix(*req.GPSFix))
	}
	if req.Satellites != nil {
		s.state.SetSatellites(*req.Satellites)
	}
	if req.HDOP != nil {
		s.state.SetHDOP(*req.HDOP)
	}
	if req.OffsetNs != nil {
		s.state.SetOffset(*req.OffsetNs)
	}
	if req.Position != nil {
		s.state.SetPosition(*req.Position)
	}

	log.Printf("API: config updated %+v", req)
	snap := s.state.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snap)
}

// configRequest uses pointers for optional fields so that unset fields
// are distinguishable from zero values.
type configRequest struct {
	SignalActive *bool         `json:"signalActive,omitempty"`
	GPSFix       *int          `json:"gpsFix,omitempty"`
	Satellites   *int          `json:"satellites,omitempty"`
	HDOP         *float64      `json:"hdop,omitempty"`
	OffsetNs     *int64        `json:"offsetNs,omitempty"`
	Position     *NMEAPosition `json:"position,omitempty"`
}

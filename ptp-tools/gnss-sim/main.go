// gnss-sim is a software GNSS NMEA sentence generator for CI testing.
//
// It produces valid NMEA sentences (GNRMC, GNGGA, GPZDA) at 1 Hz on one
// or more output paths (typically socat PTY endpoints), and exposes an
// HTTP API for test scripts to trigger signal loss/recovery — replacing
// the ubxtool commands used with real U-blox hardware.
//
// Usage:
//
//	gnss-sim --outputs /dev/ttyGNSS_TS2PHC,/dev/ttyGNSS_GNSS0 --api-port 9200
//
// In CI, the entrypoint.sh script creates socat PTY pairs and starts this
// binary with the writer-side PTY paths as outputs.
package main

import (
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	var (
		outputs         string
		apiPort         string
		holdoverTimeout int
	)
	flag.StringVar(&outputs, "outputs", "", "Comma-separated output paths for NMEA data (default: stdout)")
	flag.StringVar(&apiPort, "api-port", "9200", "HTTP API listen port")
	flag.IntVar(&holdoverTimeout, "holdover-timeout", 5, "DPLL holdover timeout in seconds before transitioning to FREERUN")
	flag.Parse()

	state := DefaultState()

	// Open output writers
	writers, closers := openWriters(outputs)
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()

	sim := NewSimulator(state, writers...)

	// Start DPLL state machine (derives state from GNSS signal)
	dpllSim := NewDPLLSimulator(state, time.Duration(holdoverTimeout)*time.Second)
	go dpllSim.Run()

	// Start HTTP API
	api := NewAPIServer(state, dpllSim)
	go func() {
		if err := api.ListenAndServe(":" + apiPort); err != nil {
			log.Fatalf("API server error: %v", err)
		}
	}()

	// Start generation loop in background
	go sim.Run()

	// Wait for termination signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("received %v, shutting down", sig)
	sim.Stop()
	dpllSim.Stop()
}

// openWriters parses the comma-separated output paths and opens each
// as a file for writing. If the path list is empty, os.Stdout is used.
// Returns the writers and a slice of closers for deferred cleanup.
func openWriters(paths string) ([]io.Writer, []*os.File) {
	if paths == "" {
		return []io.Writer{os.Stdout}, nil
	}

	parts := strings.Split(paths, ",")
	writers := make([]io.Writer, 0, len(parts))
	closers := make([]*os.File, 0, len(parts))

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		f, err := os.OpenFile(p, os.O_WRONLY, 0)
		if err != nil {
			log.Fatalf("failed to open output %q: %v", p, err)
		}
		writers = append(writers, f)
		closers = append(closers, f)
		log.Printf("opened output: %s", p)
	}

	if len(writers) == 0 {
		return []io.Writer{os.Stdout}, nil
	}
	return writers, closers
}

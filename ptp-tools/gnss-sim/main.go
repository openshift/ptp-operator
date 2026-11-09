// gnss-sim is a software GNSS NMEA sentence generator for CI testing.
//
// It produces valid NMEA sentences (GNRMC, GNGGA, GPZDA) at 1 Hz on one
// or more output paths, and exposes an HTTP API for test scripts to
// trigger signal loss/recovery — replacing the ubxtool commands used
// with real U-blox hardware.
//
// Usage:
//
//	gnss-sim --gnss-dev /dev/gnss0 --pty-links /dev/ttyGNSS_GNSS0 --api-port 9200
//	gnss-sim --outputs /dev/ttyGNSS_TS2PHC,/dev/ttyGNSS_GNSS0 --api-port 9200
//
// The --gnss-dev flag writes NMEA to a kernel GNSS character device
// (/dev/gnssN) created by the netdevsim DPLL+GNSS emulation. Data
// written is relayed into the device's read FIFO via the kernel's
// gnss_insert_raw(), so readers (e.g. ts2phc) on the same device
// receive the NMEA stream.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	var (
		outputs         string
		gnssDev         string
		ptyLinks        string
		apiPort         string
		holdoverTimeout int
		dpllSysfsPath   string
	)
	flag.StringVar(&outputs, "outputs", "", "Comma-separated output file paths for NMEA data (default: stdout)")
	flag.StringVar(&gnssDev, "gnss-dev", "", "Kernel GNSS device path (e.g. /dev/gnss0); writes NMEA into the kernel read FIFO")
	flag.StringVar(&ptyLinks, "pty-links", "", "Comma-separated symlink paths; creates a PTY pair per path and writes to the master")
	flag.StringVar(&apiPort, "api-port", "9200", "HTTP API listen port")
	// Default 60s so CI holdover tests can observe CC7 in Prometheus before FREERUN.
	flag.IntVar(&holdoverTimeout, "holdover-timeout", 60, "DPLL holdover timeout in seconds before transitioning to FREERUN")
	flag.StringVar(&dpllSysfsPath, "dpll-sysfs", "", "Path to DPLL lock_status sysfs file (e.g. /sys/bus/pci/devices/0001:1f:01.0/dpll/lock_status)")
	flag.Parse()

	state := DefaultState()

	writers, closers := openAllWriters(outputs, gnssDev, ptyLinks)
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()

	sim := NewSimulator(state, writers...)

	// Start DPLL state machine (derives state from GNSS signal)
	dpllSim := NewDPLLSimulator(state, time.Duration(holdoverTimeout)*time.Second, dpllSysfsPath)
	go dpllSim.Run()

	// Start HTTP API
	api := NewAPIServer(state, dpllSim)
	server := &http.Server{Addr: ":" + apiPort, Handler: api.mux}
	go func() {
		log.Printf("API server listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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

	// Shutdown HTTP server gracefully
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("API server shutdown error: %v", err)
	}

	sim.Stop()
	dpllSim.Stop()
}

type closer interface {
	Close() error
}

// openAllWriters creates writers from --gnss-dev (kernel GNSS device),
// --outputs (plain files), and --pty-links (self-managed PTY pairs).
// Falls back to stdout if none are specified.
func openAllWriters(outputs, gnssDev, ptyLinks string) ([]io.Writer, []closer) {
	var writers []io.Writer
	var closers []closer

	if gnssDev != "" {
		f, err := openGNSSDev(gnssDev)
		if err != nil {
			log.Fatalf("failed to open GNSS device %q: %v", gnssDev, err)
		}
		writers = append(writers, f)
		closers = append(closers, f)
		log.Printf("opened GNSS device: %s", gnssDev)
	}

	for _, p := range splitPaths(outputs) {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|syscall.O_NONBLOCK, 0o644)
		if err != nil {
			log.Fatalf("failed to open output %q: %v", p, err)
		}
		writers = append(writers, f)
		closers = append(closers, f)
		log.Printf("opened output: %s", p)
	}

	for _, p := range splitPaths(ptyLinks) {
		pw, err := openPTYLink(p)
		if err != nil {
			log.Fatalf("failed to create PTY %q: %v", p, err)
		}
		writers = append(writers, pw)
		closers = append(closers, pw)
	}

	if len(writers) == 0 {
		return []io.Writer{os.Stdout}, nil
	}
	return writers, closers
}

// gnssDevWriter opens the kernel GNSS char device for each write and
// closes it immediately afterward. This prevents holding a persistent
// file descriptor on /dev/gnssN, which would conflict with gpsd's
// exclusive-open detection in the linuxptp-daemon pod (which uses
// hostPID and can see our process's open fds).
type gnssDevWriter struct {
	path string
}

func (g *gnssDevWriter) Write(p []byte) (int, error) {
	f, err := os.OpenFile(g.path, os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	n, writeErr := f.Write(p)
	f.Close()
	return n, writeErr
}

func (g *gnssDevWriter) Close() error { return nil }

// openGNSSDev waits for a kernel GNSS character device to appear and
// returns a writer that uses per-write open/close to avoid holding the
// device fd open permanently (which conflicts with gpsd's lock detection).
func openGNSSDev(path string) (*gnssDevWriter, error) {
	for i := 0; i < 30; i++ {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if i == 0 {
			log.Printf("waiting for GNSS device %s to appear...", path)
		}
		time.Sleep(1 * time.Second)
	}

	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("device %s not found: %w", path, err)
	}
	return &gnssDevWriter{path: path}, nil
}

func splitPaths(s string) []string {
	if s == "" {
		return nil
	}
	var result []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

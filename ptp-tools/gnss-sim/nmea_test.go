package main

import (
	"strings"
	"testing"
	"time"
)

func TestNMEAChecksum(t *testing.T) {
	// Standard NMEA checksum: XOR of all bytes between $ and *
	// "GPGGA,..." known checksum example
	body := "GPGGA,092750.000,5321.6802,N,00630.3372,W,1,8,1.03,61.7,M,55.2,M,,"
	cs := nmeaChecksum(body)
	if cs != "76" {
		t.Errorf("expected checksum 76, got %s", cs)
	}
}

func TestFormatNMEA(t *testing.T) {
	s := formatNMEA("GPGGA,092750.000,5321.6802,N,00630.3372,W,1,8,1.03,61.7,M,55.2,M,,")
	if !strings.HasPrefix(s, "$") {
		t.Error("sentence must start with $")
	}
	if !strings.HasSuffix(s, "\r\n") {
		t.Error("sentence must end with \\r\\n")
	}
	if !strings.Contains(s, "*76") {
		t.Errorf("expected *76 checksum in sentence, got: %s", s)
	}
}

func TestGenerateGNRMC_Active(t *testing.T) {
	ts := time.Date(2026, 2, 16, 12, 35, 19, 0, time.UTC)
	pos := NMEAPosition{LatDeg: 35.7796, LonDeg: -78.6382, AltMeters: 96.0}

	s := GenerateGNRMC(ts, pos, true)

	if !strings.HasPrefix(s, "$GNRMC,") {
		t.Errorf("expected $GNRMC prefix, got: %s", s)
	}
	if !strings.Contains(s, "123519.00") {
		t.Errorf("expected time 123519.00, got: %s", s)
	}
	if !strings.Contains(s, ",A,") {
		t.Errorf("expected active status 'A', got: %s", s)
	}
	if !strings.Contains(s, "160226") {
		t.Errorf("expected date 160226, got: %s", s)
	}
	if !strings.HasSuffix(s, "\r\n") {
		t.Error("must end with CRLF")
	}
}

func TestGenerateGNRMC_Void(t *testing.T) {
	ts := time.Date(2026, 2, 16, 12, 35, 19, 0, time.UTC)
	pos := NMEAPosition{}

	s := GenerateGNRMC(ts, pos, false)

	if !strings.Contains(s, ",V,") {
		t.Errorf("expected void status 'V', got: %s", s)
	}
	if !strings.Contains(s, ",N*") {
		t.Errorf("expected mode N (not valid), got: %s", s)
	}
}

func TestGenerateGNGGA_Active(t *testing.T) {
	ts := time.Date(2026, 2, 16, 12, 35, 19, 0, time.UTC)
	pos := NMEAPosition{LatDeg: 35.7796, LonDeg: -78.6382, AltMeters: 96.0}

	s := GenerateGNGGA(ts, pos, true, 12, 0.9, GPSFix3D)

	if !strings.HasPrefix(s, "$GNGGA,") {
		t.Errorf("expected $GNGGA prefix, got: %s", s)
	}
	if !strings.Contains(s, ",12,") {
		t.Errorf("expected 12 satellites, got: %s", s)
	}
	if !strings.Contains(s, ",0.9,") {
		t.Errorf("expected HDOP 0.9, got: %s", s)
	}
	// fix quality = 1 for GPS fix (gpsFix 3D maps to GGA quality 1)
	fields := strings.Split(s, ",")
	if len(fields) < 7 {
		t.Fatalf("not enough fields: %s", s)
	}
	if fields[6] != "1" {
		t.Errorf("expected fix quality 1, got: %s", fields[6])
	}
}

func TestGenerateGNGGA_NoSignal(t *testing.T) {
	ts := time.Date(2026, 2, 16, 12, 35, 19, 0, time.UTC)
	pos := NMEAPosition{}

	s := GenerateGNGGA(ts, pos, false, 0, 99.9, GPSFixNoFix)

	fields := strings.Split(s, ",")
	if fields[6] != "0" {
		t.Errorf("expected fix quality 0 when no signal, got: %s", fields[6])
	}
	if fields[7] != "00" {
		t.Errorf("expected 00 satellites when no signal, got: %s", fields[7])
	}
}

func TestGenerateGNGGA_DeadReckoning(t *testing.T) {
	ts := time.Date(2026, 2, 16, 12, 35, 19, 0, time.UTC)
	pos := NMEAPosition{LatDeg: 35.7796, LonDeg: -78.6382, AltMeters: 96.0}

	// gpsFix=1 (dead reckoning) → GGA quality=6 (estimated)
	s := GenerateGNGGA(ts, pos, true, 4, 5.0, GPSFixDeadReckoning)

	fields := strings.Split(s, ",")
	if fields[6] != "6" {
		t.Errorf("expected fix quality 6 for dead reckoning, got: %s", fields[6])
	}
	if !strings.Contains(s, ",5.0,") {
		t.Errorf("expected HDOP 5.0, got: %s", s)
	}
}

func TestGpsFixToGGAQuality(t *testing.T) {
	tests := []struct {
		gpsFix   GPSFix
		expected int
	}{
		{GPSFixNoFix, 0},
		{GPSFixDeadReckoning, 6},
		{GPSFix2D, 6},
		{GPSFix3D, 1},
		{GPSFixGPSDR, 1},
		{GPSFixTimeOnly, 1},
	}
	for _, tt := range tests {
		got := gpsFixToGGAQuality(tt.gpsFix)
		if got != tt.expected {
			t.Errorf("gpsFixToGGAQuality(%d) = %d, want %d", tt.gpsFix, got, tt.expected)
		}
	}
}

func TestGenerateGPZDA(t *testing.T) {
	ts := time.Date(2026, 2, 16, 12, 35, 19, 0, time.UTC)

	s := GenerateGPZDA(ts)

	if !strings.HasPrefix(s, "$GPZDA,") {
		t.Errorf("expected $GPZDA prefix, got: %s", s)
	}
	if !strings.Contains(s, "123519.00") {
		t.Errorf("expected time 123519.00, got: %s", s)
	}
	if !strings.Contains(s, ",16,02,2026,") {
		t.Errorf("expected date 16,02,2026, got: %s", s)
	}
}

// TestGNRMCTimeFormatMatchesTestValidation verifies that the time field
// format matches what checkGNMRCString in ptphelper.go expects:
//
//	formattedTime := time.Now().UTC().Format("150405") + ".00"
func TestGNRMCTimeFormatMatchesTestValidation(t *testing.T) {
	now := time.Now()
	pos := NMEAPosition{LatDeg: 35.7796, LonDeg: -78.6382}

	s := GenerateGNRMC(now, pos, true)

	// Extract time field (field[1] after splitting by comma)
	fields := strings.Split(s, ",")
	timeVal := fields[1]

	expected := now.UTC().Format("150405") + ".00"
	if timeVal != expected {
		t.Errorf("time field %q does not match expected %q", timeVal, expected)
	}
}

// TestChecksumIntegrity verifies the checksum embedded in each sentence
// actually matches recomputation from the sentence body.
func TestChecksumIntegrity(t *testing.T) {
	ts := time.Date(2026, 2, 16, 12, 35, 19, 0, time.UTC)
	pos := NMEAPosition{LatDeg: 40.6892, LonDeg: -74.0445, AltMeters: 10.0}

	sentences := []string{
		GenerateGNRMC(ts, pos, true),
		GenerateGNRMC(ts, pos, false),
		GenerateGNGGA(ts, pos, true, 8, 1.2, GPSFix3D),
		GenerateGNGGA(ts, pos, false, 0, 99.9, GPSFixNoFix),
		GenerateGPZDA(ts),
	}

	for _, s := range sentences {
		s = strings.TrimRight(s, "\r\n")
		dollarIdx := strings.Index(s, "$")
		starIdx := strings.LastIndex(s, "*")
		if dollarIdx == -1 || starIdx == -1 {
			t.Errorf("malformed sentence: %s", s)
			continue
		}
		body := s[dollarIdx+1 : starIdx]
		expected := s[starIdx+1:]
		actual := nmeaChecksum(body)
		if actual != expected {
			t.Errorf("checksum mismatch for %q: embedded=%s computed=%s", s, expected, actual)
		}
	}
}

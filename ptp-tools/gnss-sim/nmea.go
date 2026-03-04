package main

import (
	"fmt"
	"strings"
	"time"
)

// nmeaChecksum computes the XOR checksum of all bytes between '$' and '*'
// (exclusive). Returns the two-character uppercase hex string.
func nmeaChecksum(body string) string {
	var cs byte
	for i := 0; i < len(body); i++ {
		cs ^= body[i]
	}
	return fmt.Sprintf("%02X", cs)
}

// formatNMEA wraps a raw sentence body (without '$' and '*XX') into a
// complete NMEA sentence: $body*checksum\r\n
func formatNMEA(body string) string {
	return fmt.Sprintf("$%s*%s\r\n", body, nmeaChecksum(body))
}

// NMEAPosition holds a geographic coordinate pair in NMEA notation.
type NMEAPosition struct {
	LatDeg    float64 `json:"latDeg"`    // Decimal degrees (positive = N, negative = S)
	LonDeg    float64 `json:"lonDeg"`    // Decimal degrees (positive = E, negative = W)
	AltMeters float64 `json:"altMeters"` // Altitude above mean sea level
}

// latField returns "DDMM.MMMM,N" or "DDMM.MMMM,S".
func (p NMEAPosition) latField() string {
	lat := p.LatDeg
	dir := "N"
	if lat < 0 {
		dir = "S"
		lat = -lat
	}
	deg := int(lat)
	min := (lat - float64(deg)) * 60.0
	return fmt.Sprintf("%02d%07.4f,%s", deg, min, dir)
}

// lonField returns "DDDMM.MMMM,E" or "DDDMM.MMMM,W".
func (p NMEAPosition) lonField() string {
	lon := p.LonDeg
	dir := "E"
	if lon < 0 {
		dir = "W"
		lon = -lon
	}
	deg := int(lon)
	min := (lon - float64(deg)) * 60.0
	return fmt.Sprintf("%03d%07.4f,%s", deg, min, dir)
}

// GenerateGNRMC produces a GNRMC (Recommended Minimum) sentence.
//
// When signalActive is false the status field is 'V' (void) and
// position/speed/course fields are empty, matching a real receiver
// that has lost its GNSS fix.
//
// The time field uses the format HHMMSS.00 to match the validation in
// ptp-operator test/pkg/ptphelper (checkGNMRCString).
func GenerateGNRMC(t time.Time, pos NMEAPosition, signalActive bool) string {
	utc := t.UTC()
	timeFld := utc.Format("150405") + ".00"
	dateFld := utc.Format("020106") // DDMMYY

	var body string
	if signalActive {
		body = strings.Join([]string{
			"GNRMC",
			timeFld,
			"A", // A = active
			pos.latField(),
			pos.lonField(),
			"0.0", // speed over ground (knots)
			"0.0", // course over ground (degrees)
			dateFld,
			"0.0", // magnetic variation
			"W",   // variation direction
			"A",   // mode indicator: A = autonomous
		}, ",")
	} else {
		body = strings.Join([]string{
			"GNRMC",
			timeFld,
			"V", // V = void (no fix)
			"",  // latitude
			"",  // N/S
			"",  // longitude
			"",  // E/W
			"",  // speed
			"",  // course
			dateFld,
			"",  // variation
			"",  // variation dir
			"N", // mode: N = not valid
		}, ",")
	}
	return formatNMEA(body)
}

// GenerateGNGGA produces a GNGGA (Global Positioning System Fix Data) sentence.
//
// Parameters:
//   - numSatellites: number of satellites in use
//   - hdop: horizontal dilution of precision (lower = better quality;
//     0.5-1.0 excellent, 2.0-5.0 moderate, >10 poor)
//   - gpsFix: the GPS fix type (0-5) used to derive the GGA fix quality field:
//     gpsFix 0 → GGA quality 0 (invalid)
//     gpsFix 1-2 → GGA quality 6 (estimated/dead reckoning)
//     gpsFix 3-5 → GGA quality 1 (GPS fix)
//
// When signalActive is false, fix quality=0, numSatellites=0, HDOP is empty,
// and position fields are blank — matching a real receiver that lost its fix.
func GenerateGNGGA(t time.Time, pos NMEAPosition, signalActive bool, numSatellites int, hdop float64, gpsFix GPSFix) string {
	utc := t.UTC()
	timeFld := utc.Format("150405") + ".00"

	var body string
	if signalActive {
		// Map GPSFix (U-blox NAV-STATUS scale 0-5) to GGA fix quality indicator
		ggaQuality := gpsFixToGGAQuality(gpsFix)

		body = strings.Join([]string{
			"GNGGA",
			timeFld,
			pos.latField(),
			pos.lonField(),
			fmt.Sprintf("%d", ggaQuality),      // fix quality
			fmt.Sprintf("%02d", numSatellites), // satellites in use
			fmt.Sprintf("%.1f", hdop),          // HDOP
			fmt.Sprintf("%.1f", pos.AltMeters), // altitude
			"M",                                // altitude units
			"-34.0",                            // geoid separation
			"M",                                // geoid units
			"",                                 // age of differential
			"",                                 // differential station ID
		}, ",")
	} else {
		body = strings.Join([]string{
			"GNGGA",
			timeFld,
			"",   // latitude
			"",   // N/S
			"",   // longitude
			"",   // E/W
			"0",  // fix quality: invalid
			"00", // satellites
			"",   // HDOP
			"",   // altitude
			"M",  // altitude units
			"",   // geoid separation
			"M",  // geoid units
			"",   // age of differential
			"",   // differential station ID
		}, ",")
	}
	return formatNMEA(body)
}

// gpsFixToGGAQuality maps U-blox NAV-STATUS gpsFix values (0-5) to
// NMEA GGA fix quality indicators.
//
//	gpsFix 0     → 0 (fix not available)
//	gpsFix 1-2   → 6 (estimated / dead reckoning)
//	gpsFix 3-5   → 1 (GPS fix)
func gpsFixToGGAQuality(gpsFix GPSFix) int {
	switch {
	case gpsFix <= GPSFixNoFix:
		return 0 // invalid
	case gpsFix <= GPSFix2D:
		return 6 // estimated / dead reckoning
	default:
		return 1 // GPS fix (3D, GPS+DR, time-only)
	}
}

// GenerateGPZDA produces a GPZDA (Time & Date) sentence.
// This sentence provides unambiguous UTC time including the full year.
func GenerateGPZDA(t time.Time) string {
	utc := t.UTC()
	body := strings.Join([]string{
		"GPZDA",
		utc.Format("150405") + ".00",          // time
		fmt.Sprintf("%02d", utc.Day()),        // day
		fmt.Sprintf("%02d", int(utc.Month())), // month
		fmt.Sprintf("%04d", utc.Year()),       // year
		"00",                                  // local TZ hours
		"00",                                  // local TZ minutes
	}, ",")
	return formatNMEA(body)
}

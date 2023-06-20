package ublox

// // Get GNSS Antenna receiver antenna status in both blocks
// # ubxtool -v 1 -P 29.20 -p MON-RF
// UBX-MON-RF:
// Poll request
// UBX-MON-RF:
// version 0 nBlocks 2 reserved1 0 0
// blockId 0 flags x0 antStatus 2 antPower 1 postStatus 0 reserved2 0 0 0 0
// blockId 1 flags x0 antStatus 2 antPower 1 postStatus 0 reserved2 0 0 0 0

// ANT_STATUS ...
type ANT_STATUS int

// POWER_STATUS ...
type POWER_STATUS int

const (
	// OFF ...
	OFF POWER_STATUS = iota
	// ON ...
	ON
)
const (
	// NOT_OK ...
	NOT_OK ANT_STATUS = iota
	// UNKNOWN ...
	UNKNOWN
	// OK ...
	OK
)

func (p POWER_STATUS) String() string {
	return [...]string{"OFF", "ON"}[p]
}

// IntString ...
func (p POWER_STATUS) IntString() string {
	return [...]string{"0", "1"}[p]
}

func (a ANT_STATUS) String() string {
	return [...]string{"NOT_OK", "UNKNOWN", "OK"}[a]
}

// IntString ...
func (a ANT_STATUS) IntString() string {
	return [...]string{"0", "1", "2"}[a]
}

// GNSSAntStatus ...
// Passed:  Status of the Antenna is OK (i.e., antStatus equals 2) and Antena power status is Ok (i.e., antPower equals 1)
// Failure: antStatus not equals 2 and antPower not equals 1
// GNSSAntStatus ...
type GNSSAntStatus struct {
	blockID     int32
	antStatus   ANT_STATUS
	powerStatus POWER_STATUS
}

// AntennaOk ...
func (g *GNSSAntStatus) AntennaOk() bool {
	return g.antStatus == OK && g.powerStatus == ON
}

// Status ...
func (g *GNSSAntStatus) Status() ANT_STATUS {
	return g.antStatus
}

// Power ...
func (g *GNSSAntStatus) Power() POWER_STATUS {
	return g.powerStatus
}

// SetAntStatus ...
func (g *GNSSAntStatus) SetAntStatus(antStatus ANT_STATUS) {
	g.antStatus = antStatus
}

// SetAntPower ..
func (g *GNSSAntStatus) SetAntPower(power POWER_STATUS) {
	g.powerStatus = power
}

// NewAntStatus ... get antenna status
func NewAntStatus(ant ANT_STATUS, power POWER_STATUS) GNSSAntStatus {
	return GNSSAntStatus{
		blockID:     0,
		antStatus:   ant,
		powerStatus: power,
	}
}

package ublox

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	expect "github.com/google/goexpect"

	"github.com/golang/glog"
)

var (
	// PorotoVersionRegEx ...
	PorotoVersionRegEx = regexp.MustCompile(`PROTVER=+(\d+)`)
	// AntennaStatusRegEx ...
	AntennaStatusRegEx = regexp.MustCompile(`antStatus[[:space:]]+(\d+)[[:space:]]antPower[[:space:]]+(\d+)`)
	// NavStatusRegEx ...
	NavStatusRegEx    = regexp.MustCompile(`gpsFix[[:space:]]+(\d+)`)
	cmdTimeout        = 10 * time.Second
	ubloxProtoVersion = "29.20"
)

const (
	// CMD_PROTO_VERSION ...
	CMD_PROTO_VERSION = " -p MON-VER"
	// CMD_VOLTAGE_CONTROLER ...
	CMD_VOLTAGE_CONTROLER = " -v 1 -z CFG-HW-ANT_CFG_VOLTCTRL,%d"
	// CMD_NAV_STATUS ...
	CMD_NAV_STATUS = " -t -p NAV-STATUS"
	UBXCommand     = "ubxtool"
)

// UBlox ... UBlox type
type UBlox struct {
	protoVersion *string
	mockExp      func(cmdStr string) ([]string, error)
}

// NewUblox ... create new Ublox
func NewUblox() (*UBlox, error) {
	u := &UBlox{
		protoVersion: &ubloxProtoVersion,
		mockExp:      nil,
	}
	u.EnableNMEA()
	u.DisableBinary()
	//u.DisableNMEA()
	return u, nil
}

/*
 ENVIRONMENT:
#    Options in the UBXOPTS environment variable will be parsed before
#    the CLI options.  A handy place to put your '-f /dev/ttyXX -s SPEED'
#
# To see what constellations are enabled:
#       ubxtool -p CFG-GNSS -f /dev/ttyXX
#
# To disable GLONASS and enable GALILEO:
#       ubxtool -d GLONASS -f /dev/ttyXX
#       ubxtool -e GALILEO -f /dev/ttyXX
#

*/

// Init ...
func (u *UBlox) Init() (err error) {
	var protoVersion *string
	if protoVersion, err = u.MonVersion(CMD_PROTO_VERSION, PorotoVersionRegEx); err == nil {
		u.protoVersion = protoVersion
		u.DisableBinary()
		u.EnableNMEA()
	} else {
		err = fmt.Errorf("UBlox could not find version for method %s with error %s", "MON-VER", err)
	}
	return
}

// MonVersion  ... get monitor version
func (u *UBlox) MonVersion(command string, regEx *regexp.Regexp) (*string, error) {
	var stdout string
	var err error
	stdout, err = u.query(CMD_PROTO_VERSION, PorotoVersionRegEx)
	glog.Infof("Queried Ublox output %s", stdout)
	if err != nil {
		glog.Errorf("error reading ublox MON-VER command  %s", err)
		return nil, err
	}
	if err != nil {
		glog.Errorf("error reading gnss status %s", err)
		return nil, err
	}

	if err != nil {
		glog.Errorf("error %s", err)
		return nil, err
	}
	return &stdout, nil
}

func (u *UBlox) queryVersion(command string, promptRE *regexp.Regexp) (result string, matches []string, err error) {
	e, _, err := expect.Spawn("/usr/bin/bash", -1)
	if err != nil {
		return
	}
	defer func(e *expect.GExpect) {
		err := e.Close()
		if err != nil {
			glog.Errorf("error closing expect %s", err)
		}
	}(e)
	if err = e.Send(fmt.Sprintf("%s -p %s", UBXCommand, command) + "\n"); err == nil {
		result, matches, err = e.Expect(promptRE, cmdTimeout)
		if err != nil {
			glog.Errorf("result match error %s", err)
			return
		}
		err = e.Send("\x03")
	}
	return
}

// Query ... used for testing only
func (u *UBlox) Query(command string, promptRE *regexp.Regexp) (result string, matches []string, err error) {
	e, _, err := expect.Spawn("/usr/bin/bash", -1)
	if err != nil {
		return
	}
	defer func(e *expect.GExpect) {
		err := e.Close()
		if err != nil {
			glog.Errorf("error closing expect %s", err)
		}
	}(e)
	if err = e.Send(fmt.Sprintf("%s %s", UBXCommand, command) + "\n"); err == nil {
		result, matches, err = e.Expect(promptRE, cmdTimeout)
		if err != nil {
			glog.Errorf("result match error %s", err)
			return
		}
		err = e.Send("\x03")
	}
	return
}

// EnableDisableVoltageController ... enable disable voltage controler 1 or 0
// Enable GNSS Antenna voltage control
// # ubxtool -v 1  -P 29.20 -z CFG-HW-ANT_CFG_VOLTCTRL,1
// connected to tcp://localhost:2947
// sent:
// UBX-CFG-VALSET:
// version 0 layer 0x7 transaction 0x0 reserved 0
// layers (ram bbr flash) transaction (Transactionless)
// item CFG-HW-ANT_CFG_VOLTCTRL/0x10a3002e val 1

// EnableDisableVoltageController ...
// UBX-ACK-ACK:
// ACK to Class x06 (CFG) ID x8a (VALSET)
// TODO: Should read ACK-ACK to confirm right and read the item
func (u *UBlox) EnableDisableVoltageController(command string, value int) ([]byte, error) {
	if u.protoVersion == nil {
		return []byte{}, fmt.Errorf("Cannot query UBlox without protocol version ")
	}
	commandArgs := []string{"/usr/bin/bash", "-c", fmt.Sprintf("\"%s  -v 1 -P %s  -p %s,%d\"", UBXCommand, *u.protoVersion, command, value)}

	stdout, err := exec.Command(commandArgs[0], commandArgs[1:]...).Output()
	return stdout, err
}

// NavStatus ...
/* NavStatus ...
ubxtool -t -w 3 -p NAV-STATUS -P 29.20
1683631651.3422
UBX-NAV-STATUS:
iTOW 214069000 gpsFix 5 flags 0xdd fixStat 0x0 flags2 0x8
ttff 22392, msss 1029803710
*/
// NavStatus ... GPS status 0-1 Out of sync  3-5 in sync
func (u *UBlox) NavStatus() (int64, error) {
	var stdout string
	var err error
	// -t -w 3 -p NAV-STATUS
	glog.Infof("Fetching GNSS status %s", fmt.Sprintf("-P %s %s", *u.protoVersion, CMD_NAV_STATUS))
	stdout, err = u.query(fmt.Sprintf("-P %s %s", *u.protoVersion, CMD_NAV_STATUS), NavStatusRegEx)
	glog.Infof("queried ublox output %s", stdout)
	if err != nil {
		glog.Errorf("error in reading gnss status %s", err)
		return 0, err
	}
	var parseError error
	var status int64
	status, parseError = strconv.ParseInt(stdout, 10, 64)
	return status, parseError
}

func (u *UBlox) query(command string, promptRE *regexp.Regexp) (string, error) {
	var stdBuffer bytes.Buffer
	mw := io.MultiWriter(os.Stdout, &stdBuffer)
	cmdName := fmt.Sprintf("%s %s", UBXCommand, command)
	cmdArgs := strings.Fields(cmdName)
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Stdout = mw
	cmd.Stderr = mw
	// Execute the command
	if err := cmd.Run(); err != nil {
		glog.Errorf("error executing cmd %s", fmt.Sprintf("%s %s", UBXCommand, command))
		return "", err
	}
	glog.Infof("Ublox cmd %s returned\n %s", fmt.Sprintf("ubxtool %s", command), stdBuffer.String())
	return match(stdBuffer.String(), promptRE)

}

func match(stdout string, ubLoxRegex *regexp.Regexp) (string, error) {
	match := ubLoxRegex.FindStringSubmatch(string(stdout))
	if len(match) > 0 {
		return match[1], nil
	}
	return "", fmt.Errorf("error parsing %s", stdout)
}

// GetNavOffset ... get gnss offset
func (u *UBlox) GetNavOffset() (string, error) {
	args := []string{"-t", "-p", "NAV-CLOCK", "-P", "29.20"}

	output, err := exec.Command(UBXCommand, args...).Output()
	if err != nil {
		return "", fmt.Errorf("error executing ubxtool command: %s", err)
	}

	offset := extractOffset(string(output))
	return offset, nil
}

// DisableBinary ...  disable binary
func (u *UBlox) DisableBinary() {
	// Enable binary protocol
	args := []string{"-d", "BINARY", "-P", "29.20"}
	if err := exec.Command(UBXCommand, args...).Run(); err != nil {
		glog.Errorf("error executing ubxtool command: %s", err)
	} else {
		glog.Info("disable binary")
	}
}

// EnableNMEA ... enable nmea
func (u *UBlox) EnableNMEA() {
	// Enable binary protocol
	args := []string{"-e", "NMEA", "-P", "29.20"}
	if err := exec.Command(UBXCommand, args...).Run(); err != nil {
		glog.Errorf("error executing ubxtool command: %s", err)
	} else {
		glog.Info("Enable NMEA")
	}
}

func extractOffset(output string) string {
	// Find the line that contains "tAcc"
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "tAcc") {
			// Extract the offset value
			fields := strings.Fields(line)
			for i, field := range fields {
				if field == "tAcc" {
					return fields[i+1]
				}
			}
		}
	}

	return ""
}

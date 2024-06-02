package ublox

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
	UBXCommand     = "/usr/local/bin/ubxtool"
)

// UBlox ... UBlox type
type UBlox struct {
	active       bool
	protoVersion *string
	mockExp      func(cmdStr string) ([]string, error)
	cmd          *exec.Cmd
	reader       *bufio.Reader
	match        string
	buffer       []string
	bufferlen    int
	buffermutex  sync.Mutex
}

// NewUblox ... create new Ublox
func NewUblox() (*UBlox, error) {
	u := &UBlox{
		protoVersion: &ubloxProtoVersion,
		mockExp:      nil,
		active:       false,
		bufferlen:    0,
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

func (u *UBlox) UbloxPollPull() string {
	output := ""
	u.buffermutex.Lock()
	if u.bufferlen > 0 {
		output = u.buffer[0]
		u.buffer = u.buffer[1:]
		u.bufferlen--
	}
	u.buffermutex.Unlock()
	return output
}

func (u *UBlox) UbloxPollInit() {
	if !u.active {
		u.buffermutex.Lock()
		u.bufferlen = 0
		u.buffer = nil
		u.buffermutex.Unlock()
		wait := 1000000000
		args := []string{"-u", UBXCommand, "-t", "-P", "29.20", "-w", fmt.Sprintf("%d", wait)}
		//python -u /usr/local/bin/ubxtool -t -p NAV-CLOCK -p NAV-STATUS -P 29.20 -w 10
		u.cmd = exec.Command("python", args...)
		stdoutreader, _ := u.cmd.StdoutPipe()
		u.reader = bufio.NewReader(stdoutreader)
		u.active = true
		err := u.cmd.Start()
		if err != nil {
			glog.Errorf("UbloxPoll err=%s", err.Error())
			u.active = false
		}
		go u.UbloxPollPushThread()
	}
}

func (u *UBlox) UbloxPollPushThread() {
	for {
		output, err := u.reader.ReadString('\n')
		if err != nil {
			u.active = false
			return
		} else if len(output) > 0 {
			u.buffermutex.Lock()
			u.bufferlen++
			u.buffer = append(u.buffer, output)
			u.buffermutex.Unlock()
		}
	}
}

func (u *UBlox) UbloxPollReset() {
	u.cmd.Process.Kill()
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

func ExtractOffset(output string) int64 {
	// Find the line that contains "tAcc"
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "tAcc") {
			// Extract the offset value
			fields := strings.Fields(line)
			for i, field := range fields {
				if field == "tAcc" {
					ret, _ := strconv.ParseInt(fields[i+1], 10, 64)
					return ret
				}
			}
		}
	}

	return -1
}

func ExtractNavStatus(output string) int64 {
	// Find the line that contains "gpsFix"
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "gpsFix") {
			// Extract the offset value
			fields := strings.Fields(line)
			for i, field := range fields {
				if field == "gpsFix" {
					ret, _ := strconv.ParseInt(fields[i+1], 10, 64)
					return ret
				}
			}
		}
	}
	return -1
}

type TimeLs struct {
	//Information source for the current number
	// of leap seconds
	SrcOfCurrLs uint8
	// Current number of leap seconds since
	// start of GPS time (Jan 6, 1980). It reflects
	// how much GPS time is ahead of UTC time.
	// Galileo number of leap seconds is the
	// same as GPS. BeiDou number of leap
	// seconds is 14 less than GPS. GLONASS
	// follows UTC time, so no leap seconds
	CurrLs int8
	// Information source for the future leap
	// second event.
	SrcOfLsChange uint8
	// Future leap second change if one is
	// scheduled. +1 = positive leap second, -1 =
	// negative leap second, 0 = no future leap
	// second event scheduled or no information
	// available. If the value is 0, then the
	// amount of leap seconds did not change
	// and the event should be ignored
	LsChange int8
	// Number of seconds until the next leap
	// second event, or from the last leap second
	// event if no future event scheduled. If > 0
	// event is in the future, = 0 event is now, < 0
	// event is in the past. Valid only if
	// validTimeToLsEvent = 1
	TimeToLsEvent int
	// GPS week number (WN) of the next leap
	// second event or the last one if no future
	// event scheduled. Valid only if
	// validTimeToLsEvent = 1.
	DateOfLsGpsWn uint
	// GPS day of week number (DN) for the next
	// leap second event or the last one if no
	// future event scheduled. Valid only if
	// validTimeToLsEvent = 1. (GPS and Galileo
	// DN: from 1 = Sun to 7 = Sat. BeiDou DN:
	// from 0 = Sun to 6 = Sat.
	DateOfLsGpsDn uint8
	// Validity flags
	// 1<<0 validCurrLs 1 = Valid current number of leap seconds value.
	// 1<<1 validTimeToLsEvent 1 = Valid time to next leap second event
	// or from the last leap second event if no future event scheduled.
	Valid uint8
}

func ExtractLeapSec(output []string) *TimeLs {
	var data = TimeLs{}
	for _, line := range output {
		fields := strings.Fields(line)
		for i, field := range fields {
			switch field {
			case "srcOfCurrLs":
				tmp, _ := strconv.ParseUint(fields[i+1], 10, 8)
				data.SrcOfCurrLs = uint8(tmp)
			case "currLs":
				tmp, _ := strconv.ParseInt(fields[i+1], 10, 8)
				data.CurrLs = int8(tmp)
			case "srcOfLsChange":
				tmp, _ := strconv.ParseUint(fields[i+1], 10, 8)
				data.SrcOfLsChange = uint8(tmp)
			case "lsChange":
				tmp, _ := strconv.ParseInt(fields[i+1], 10, 8)
				data.LsChange = int8(tmp)
			case "timeToLsEvent":
				tmp, _ := strconv.ParseInt(fields[i+1], 10, 32)
				data.TimeToLsEvent = int(tmp)
			case "dateOfLsGpsWn":
				tmp, _ := strconv.ParseUint(fields[i+1], 10, 16)
				data.DateOfLsGpsWn = uint(tmp)
			case "dateOfLsGpsDn":
				tmp, _ := strconv.ParseUint(fields[i+1], 10, 16)
				data.DateOfLsGpsDn = uint8(tmp)
			case "valid":
				tmp, _ := strconv.ParseUint(fmt.Sprintf("0%s", fields[i+1]), 0, 8)
				data.Valid = uint8(tmp)
			}
		}
	}
	return &data
}

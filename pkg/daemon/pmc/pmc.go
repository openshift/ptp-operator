package pmc

import (
	"fmt"
	"github.com/golang/glog"
	"github.com/google/goexpect"
	"regexp"
	"time"
)

var (
	ClockClassChangeRegEx   = regexp.MustCompile(`gm.ClockClass[[:space:]]+(\d+)`)
	ClockClassUpdateRegEx   = regexp.MustCompile(`clockClass[[:space:]]+(\d+)`)
	CmdParentDataSet        = "GET PARENT_DATA_SET"
	CmdUpdateGMClass_LOCKED = `SET GRANDMASTER_SETTINGS_NP clockClass %d  \
		                    clockAccuracy 0x21 offsetScaledLogVariance 0x436a \
                             currentUtcOffset 37 leap61 0 leap59 0 currentUtcOffsetValid 1 \
                             ptpTimescale 1 timeTraceable 0 frequencyTraceable 0 
                             timeSource 0xa0`
	CmdUpdateGMClass_FREERUN = `SET GRANDMASTER_SETTINGS_NP clockClass %d  \
		                    clockAccuracy 0xFE offsetScaledLogVariance 0x436a \
                             currentUtcOffset 37 leap61 0 leap59 0 currentUtcOffsetValid 1 \
                             ptpTimescale 1 timeTraceable 0 frequencyTraceable 0 
                             timeSource 0xa0`
	cmdTimeout = 500 * time.Millisecond
)

// RunPMCExp ... go expect to run PMC util cmd
func RunPMCExp(configFileName, cmdStr string, promptRE *regexp.Regexp) (result string, matches []string, err error) {
	e, _, err := expect.Spawn(fmt.Sprintf("pmc -u -b 1 -f /var/run/%s", configFileName), -1)
	if err != nil {
		return "", []string{}, err
	}
	defer e.Close()
	if err = e.Send(cmdStr + "\n"); err == nil {
		result, matches, err = e.Expect(promptRE, cmdTimeout)
		if err != nil {
			glog.Errorf("pmc result match error %s", err)
			return
		}
		err = e.Send("\x03")
	}
	return
}

// RunUpdatePMCExp ... run pmc update command
func RunUpdatePMCExp(configFileName, cmdStr string, promptRE *regexp.Regexp) (result string, matches []string, err error) {
	e, _, err := expect.Spawn(fmt.Sprintf("pmc -u -b 1 -f /var/run/%s", configFileName), -1)
	if err != nil {
		return "", []string{}, err
	}
	defer e.Close()
	if err = e.Send(cmdStr + "\n"); err == nil {
		result, matches, err = e.Expect(promptRE, cmdTimeout)
		if err != nil {
			glog.Errorf("pmc result match error %s", err)
			return
		}
		err = e.Send("\x03")
	}
	return
}

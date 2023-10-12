package pmc

import (
	"fmt"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/golang/glog"
	expect "github.com/google/goexpect"
	"github.com/openshift/k8snetworkplumbingwg/pkg/protocol"
)

var (
	ClockClassChangeRegEx = regexp.MustCompile(`gm.ClockClass[[:space:]]+(\d+)`)
	ClockClassUpdateRegEx = regexp.MustCompile(`clockClass[[:space:]]+(\d+)`)
	GetGMSettingsRegEx    = regexp.MustCompile(`clockClass[[:space:]]+(\d+)[[:space:]]+clockAccuracy[[:space:]]+(0x\d+)`)
	CmdGetParentDataSet   = "GET PARENT_DATA_SET"
	CmdGetGMSettings      = "GET GRANDMASTER_SETTINGS_NP"
	CmdSetGMSettings      = "SET GRANDMASTER_SETTINGS_NP"
	cmdTimeout            = 2000 * time.Millisecond
	sigTimeout            = 500 * time.Millisecond
	numRetry              = 6
)

// RunPMCExp ... go expect to run PMC util cmd
func RunPMCExp(configFileName, cmdStr string, promptRE *regexp.Regexp) (result string, matches []string, err error) {
	pmcCmd := fmt.Sprintf("pmc -u -b 0 -f /var/run/%s", configFileName)
	glog.Infof("%s \"%s\"", pmcCmd, cmdStr)
	e, r, err := expect.Spawn(pmcCmd, -1)
	if err != nil {
		return "", []string{}, err
	}
	defer func() {
		e.SendSignal(syscall.SIGTERM)
		for timeout := time.After(sigTimeout); ; {
			select {
			case <-r:
				e.Close()
				return
			case <-timeout:
				e.Send("\x03")
				e.Close()
				return
			}
		}
	}()

	if err = e.Send(cmdStr + "\n"); err == nil {
		result, matches, err = e.Expect(promptRE, cmdTimeout)
		if err != nil {
			glog.Errorf("pmc result match error %s", err)
			return
		}
		glog.Infof("pmc result: %s", result)
	}
	return
}

// RunPMCExpGetGMSettings ... get current GRANDMASTER_SETTINGS_NP
func RunPMCExpGetGMSettings(configFileName string) (g protocol.GrandmasterSettings, err error) {
	cmdStr := CmdGetGMSettings
	pmcCmd := fmt.Sprintf("pmc -u -b 0 -f /var/run/%s", configFileName)
	glog.Infof("%s \"%s\"", pmcCmd, cmdStr)
	e, r, err := expect.Spawn(pmcCmd, -1)
	if err != nil {
		return g, err
	}
	defer func() {
		e.SendSignal(syscall.SIGTERM)
		for timeout := time.After(sigTimeout); ; {
			select {
			case <-r:
				e.Close()
				return
			case <-timeout:
				e.Send("\x03")
				e.Close()
				return
			}
		}
	}()

	for i := 0; i < numRetry; i++ {
		if err = e.Send(cmdStr + "\n"); err == nil {
			result, matches, err := e.Expect(regexp.MustCompile(g.RegEx()), cmdTimeout)
			if err != nil {
				if _, ok := err.(expect.TimeoutError); ok {
					continue
				}
				glog.Errorf("pmc result match error %v", err)
				return g, err
			}
			glog.Infof("pmc result: %s", result)
			for i, m := range matches[1:] {
				g.Update(g.Keys()[i], m)
			}
			break
		}
	}
	return
}

// RunPMCExpSetGMSettings ... set GRANDMASTER_SETTINGS_NP
func RunPMCExpSetGMSettings(configFileName string, g protocol.GrandmasterSettings) (err error) {
	cmdStr := CmdSetGMSettings
	cmdStr += strings.Replace(g.String(), "\n", " ", -1)
	pmcCmd := fmt.Sprintf("pmc -u -b 0 -f /var/run/%s", configFileName)
	e, r, err := expect.Spawn(pmcCmd, -1)
	if err != nil {
		return err
	}
	defer func() {
		e.SendSignal(syscall.SIGTERM)
		for timeout := time.After(sigTimeout); ; {
			select {
			case <-r:
				e.Close()
				return
			case <-timeout:
				e.Send("\x03")
				e.Close()
				return
			}
		}
	}()

	if err = e.Send(cmdStr + "\n"); err == nil {
		result, _, err := e.Expect(regexp.MustCompile(g.RegEx()), cmdTimeout)
		if err != nil {
			glog.Errorf("pmc result match error %v", err)
			return err
		}
		glog.Infof("pmc result: %s", result)
	}
	return
}

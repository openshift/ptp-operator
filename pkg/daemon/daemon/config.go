package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/openshift/linuxptp-daemon/pkg/event"
	"io/ioutil"
	"os"
	"strings"

	"github.com/golang/glog"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
)

// LinuxPTPUpdate controls whether to update linuxPTP conf
// and contains linuxPTP conf to be updated. It's rendered
// and passed to linuxptp instance by daemon.
type LinuxPTPConfUpdate struct {
	UpdateCh               chan bool
	NodeProfiles           []ptpv1.PtpProfile
	appliedNodeProfileJson []byte
	defaultPTP4lConfig     []byte
}

type ptp4lConfSection struct {
	sectionName string
	options     map[string]string
}

type ptp4lConf struct {
	sections     []ptp4lConfSection
	mapping      []string
	profile_name string
	clock_type   event.ClockType
}

func NewLinuxPTPConfUpdate() (*LinuxPTPConfUpdate, error) {
	if _, err := os.Stat(PTP4L_CONF_FILE_PATH); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("ptp.conf file doesn't exist")
		} else {
			return nil, fmt.Errorf("unknow error searching for the %s file: %v", PTP4L_CONF_FILE_PATH, err)
		}
	}

	defaultPTP4lConfig, err := ioutil.ReadFile(PTP4L_CONF_FILE_PATH)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %v", PTP4L_CONF_FILE_PATH, err)
	}

	return &LinuxPTPConfUpdate{UpdateCh: make(chan bool), defaultPTP4lConfig: defaultPTP4lConfig}, nil
}

func (l *LinuxPTPConfUpdate) UpdateConfig(nodeProfilesJson []byte) error {
	if string(l.appliedNodeProfileJson) == string(nodeProfilesJson) {
		return nil
	}

	if nodeProfiles, ok := tryToLoadConfig(nodeProfilesJson); ok {
		glog.Info("load profiles")
		l.appliedNodeProfileJson = nodeProfilesJson
		l.NodeProfiles = nodeProfiles
		l.UpdateCh <- true

		return nil
	}

	if nodeProfiles, ok := tryToLoadOldConfig(nodeProfilesJson); ok {
		// Support empty old config
		// '{"name":null,"interface":null}'
		if nodeProfiles[0].Name == nil || nodeProfiles[0].Interface == nil {
			glog.Infof("Skip no profile %+v", nodeProfiles[0])
			return nil
		}

		glog.Info("load profiles using old method")
		l.appliedNodeProfileJson = nodeProfilesJson
		l.NodeProfiles = nodeProfiles
		l.UpdateCh <- true

		return nil
	}

	return fmt.Errorf("unable to load profile config")
}

// Try to load the multiple policy config
func tryToLoadConfig(nodeProfilesJson []byte) ([]ptpv1.PtpProfile, bool) {
	ptpConfig := []ptpv1.PtpProfile{}
	err := json.Unmarshal(nodeProfilesJson, &ptpConfig)
	if err != nil {
		return nil, false
	}

	return ptpConfig, true
}

// For backward compatibility we also try to load the one policy scenario
func tryToLoadOldConfig(nodeProfilesJson []byte) ([]ptpv1.PtpProfile, bool) {
	ptpConfig := &ptpv1.PtpProfile{}
	err := json.Unmarshal(nodeProfilesJson, ptpConfig)
	if err != nil {
		return nil, false
	}

	return []ptpv1.PtpProfile{*ptpConfig}, true
}

// Takes as input a PtpProfile.Ptp4lConf and outputs as ptp4lConf struct
func (output *ptp4lConf) populatePtp4lConf(config *string) error {
	var currentSectionName string
	var currentSection ptp4lConfSection
	output.sections = make([]ptp4lConfSection, 0)
	globalIsDefined := false
	hasSlaveConfigDefined := false

	if config != nil {
		for _, line := range strings.Split(*config, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") {
				continue
			} else if strings.HasPrefix(line, "[") {
				if currentSectionName != "" {
					output.sections = append(output.sections, currentSection)
				}
				currentLine := strings.Split(line, "]")

				if len(currentLine) < 2 {
					return errors.New("Section missing closing ']': " + line)
				}

				currentSectionName = fmt.Sprintf("%s]", currentLine[0])
				if currentSectionName == "[global]" {
					globalIsDefined = true
				}
				currentSection = ptp4lConfSection{options: map[string]string{}, sectionName: currentSectionName}
			} else if currentSectionName != "" {
				split := strings.IndexByte(line, ' ')
				if split > 0 {
					currentSection.options[line[:split]] = line[split:]
					if (line[:split] == "masterOnly" && line[split:] == "0") ||
						(line[:split] == "serverOnly" && line[split:] == "0") ||
						(line[:split] == "slaveOnly" && line[split:] == "1") ||
						(line[:split] == "clientOnly" && line[split:] == "1") {
						hasSlaveConfigDefined = true
					}
				}
			} else {
				return errors.New("Config option not in section: " + line)
			}
		}
		if currentSectionName != "" {
			output.sections = append(output.sections, currentSection)
		}
	}

	if !globalIsDefined {
		output.sections = append(output.sections, ptp4lConfSection{options: map[string]string{}, sectionName: "[global]"})
	}

	if !hasSlaveConfigDefined {
		// No Slave Interfaces defined
		output.clock_type = event.GM
	} else if len(output.sections) > 2 {
		// Multiple interfaces with at least one slave Interface defined
		output.clock_type = event.BC
	} else {
		// Single slave Interface defined
		output.clock_type = event.OC
	}
	return nil
}

func (conf *ptp4lConf) renderPtp4lConf() (string, string) {
	configOut := fmt.Sprintf("#profile: %s\n", conf.profile_name)
	conf.mapping = nil

	for _, section := range conf.sections {
		configOut = fmt.Sprintf("%s\n%s", configOut, section.sectionName)
		if section.sectionName != "[global]" && section.sectionName != "[nmea]" {
			iface := section.sectionName
			iface = strings.ReplaceAll(iface, "[", "")
			iface = strings.ReplaceAll(iface, "]", "")
			conf.mapping = append(conf.mapping, iface)
		}
		for k, v := range section.options {
			configOut = fmt.Sprintf("%s\n%s %s", configOut, k, v)
		}
	}
	return configOut, strings.Join(conf.mapping, ",")
}

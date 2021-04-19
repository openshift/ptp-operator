package daemon

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/user"

	"github.com/golang/glog"

	"path/filepath"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"
)

const (
	DefaultUpdateInterval = 30
	DefaultProfilePath    = "/etc/linuxptp"
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

func GetKubeConfig() (*rest.Config, error) {
	configFromFlags := func(kubeConfig string) (*rest.Config, error) {
		if _, err := os.Stat(kubeConfig); err != nil {
			return nil, fmt.Errorf("Cannot stat kubeconfig '%s'", kubeConfig)
		}
		return clientcmd.BuildConfigFromFlags("", kubeConfig)
	}

	// If an env variable is specified with the config location, use that
	kubeConfig := os.Getenv("KUBECONFIG")
	if len(kubeConfig) > 0 {
		return configFromFlags(kubeConfig)
	}
	// If no explicit location, try the in-cluster config
	if c, err := rest.InClusterConfig(); err == nil {
		return c, nil
	}
	// If no in-cluster config, try the default location in the user's home directory
	if usr, err := user.Current(); err == nil {
		kubeConfig := filepath.Join(usr.HomeDir, ".kube", "config")
		return configFromFlags(kubeConfig)
	}

	return nil, fmt.Errorf("Could not locate a kubeconfig")
}

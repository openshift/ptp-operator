package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/event"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	DefaultUpdateInterval  = 30
	DefaultProfilePath     = "/etc/linuxptp"
	DefaultLeapConfigPath  = "/etc/leap"
	DefaultPmcPollInterval = 60
)

type IFaces []Iface
type Iface struct {
	Name     string
	IsMaster bool
	Source   event.EventSource
	PhcId    string
}

// GetGMInterface ... get grandmaster interface
func (i *IFaces) GetGMInterface() Iface {
	for _, iface := range *i {
		if iface.Source == event.GNSS {
			return iface
		}
	}
	return Iface{}
}

// Add ...  append interfaces
func (i *IFaces) Add(iface Iface) {
	*i = append(*i, iface)
}

// GetEventSource ... get event source
func (i *IFaces) GetEventSource(iface string) event.EventSource {
	for _, ii := range *i {
		if ii.Name == iface {
			return ii.Source
		}
	}
	return event.PTP4l
}

// GetPhcID2IFace ... get interface name match phcID
func (i *IFaces) GetPhcID2IFace(phcId string) string {
	for _, ii := range *i {
		if ii.PhcId == phcId {
			return ii.Name
		}
	}
	return phcId
}

// String ... get string
func (i *IFaces) String() string {
	b := strings.Builder{}
	for _, ii := range *i {
		b.WriteString("name :" + ii.Name + "\n")
		b.WriteString(fmt.Sprintf("source %s\n", ii.Source))
		b.WriteString(fmt.Sprintf("IsMaster %v\n", ii.IsMaster))
		b.WriteString(fmt.Sprintf("phcid %s\n", ii.PhcId))
	}
	return b.String()
}

func GetKubeConfig() (*rest.Config, error) {
	configFromFlags := func(kubeConfig string) (*rest.Config, error) {
		if _, err := os.Stat(kubeConfig); err != nil {
			return nil, fmt.Errorf("cannot start kubeconfig '%s'", kubeConfig)
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

	return nil, fmt.Errorf("could not locate a kubeconfig")
}

type ProcessConfig struct {
	ClockType       event.ClockType
	ConfigName      string
	EventChannel    chan<- event.EventChannel
	GMThreshold     Threshold
	InitialPTPState event.PTPState
}
type Threshold struct {
	Max             int64
	Min             int64
	HoldOverTimeout int64
}

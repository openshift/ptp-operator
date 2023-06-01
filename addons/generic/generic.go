package generic

import (
	"github.com/golang/glog"
	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/plugin"
)

func onPTPConfigChangeGeneric(*interface{}, *ptpv1.PtpProfile) error {
	glog.Infof("calling onPTPConfigChangeGeneric")
	return nil
}

func PopulateHwConfigGeneric(*interface{}, *[]ptpv1.HwConfig) error {
	return nil
}

func AfterRunPTPCommandGeneric(*interface{}, *ptpv1.PtpProfile, string) error {
	return nil
}

func Reference(name string) (*plugin.Plugin, *interface{}) {
	if name != "reference" {
		glog.Errorf("Plugin must be initialized as 'reference'")
		return nil, nil
	}
	_plugin := plugin.Plugin{Name: "reference",
		OnPTPConfigChange:  onPTPConfigChangeGeneric,
		PopulateHwConfig:   PopulateHwConfigGeneric,
		AfterRunPTPCommand: AfterRunPTPCommandGeneric,
	}
	return &_plugin, nil
}

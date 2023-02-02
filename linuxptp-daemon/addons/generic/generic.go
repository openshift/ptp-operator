package generic

import (
	"github.com/golang/glog"
	"github.com/openshift/linuxptp-daemon/pkg/plugin"
	ptpv1 "github.com/openshift/ptp-operator/api/v1"
)

func onPTPConfigChangeGeneric(*ptpv1.PtpProfile) error {
	glog.Infof("calling onPTPConfigChangeGeneric")
	return nil
}

func PopulateHwConfigGeneric(hwconfig *ptpv1.HwConfig) error {
	return nil
}

func Reference(name string) *plugin.Plugin {
	if name != "reference" {
		glog.Errorf("Plugin must be initialized as 'reference'")
		return nil
	}
	return &plugin.Plugin{Name: "reference",
		OnPTPConfigChange: onPTPConfigChangeGeneric,
		PopulateHwConfig:  PopulateHwConfigGeneric,
	}
}

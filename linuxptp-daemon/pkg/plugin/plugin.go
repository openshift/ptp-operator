package plugin

import (
	ptpv1 "github.com/openshift/ptp-operator/api/v1"
)

type New func(string) *Plugin
type OnPTPConfigChange func(*ptpv1.PtpProfile) error
type PopulateHwConfig func(*[]ptpv1.HwConfig) error

type Plugin struct {
	Name              string
	Options           interface{}
	OnPTPConfigChange OnPTPConfigChange
	PopulateHwConfig  PopulateHwConfig
}

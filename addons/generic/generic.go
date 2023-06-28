package generic

import (
	"encoding/json"
	"github.com/golang/glog"
	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/plugin"
)

type GenericPluginData struct {
	referenceString *string
}

func OnPTPConfigChangeGeneric(data *interface{}, nodeProfile *ptpv1.PtpProfile) error {
	var pluginOpts string = ""
	var err error
	var optsByteArray []byte

	for name, opts := range (*nodeProfile).Plugins {
		if name == "reference" {
			optsByteArray, _ = json.Marshal(opts)
			err = json.Unmarshal(optsByteArray, &pluginOpts)
		}
	}

	if data != nil && pluginOpts != "" {
		_data := *data
		glog.Infof("Saving status to hwconfig: %s", string(pluginOpts))
		var pluginData *GenericPluginData = _data.(*GenericPluginData)
		_pluginData := *pluginData
		*_pluginData.referenceString = pluginOpts
	}
	glog.Infof("OnPTPConfigChangeGeneric: (%s)", pluginOpts)
	return err

}

func PopulateHwConfigGeneric(data *interface{}, hwconfigs *[]ptpv1.HwConfig) error {
	glog.Infof("PopulateHwConfigGeneric")
	var referenceString string = ""
	if data != nil {
		_data := *data
		var pluginData *GenericPluginData = _data.(*GenericPluginData)
		_pluginData := *pluginData
		if _pluginData.referenceString != nil {
			referenceString = *_pluginData.referenceString
			hwConfig := ptpv1.HwConfig{}
			hwConfig.DeviceID = "reference"
			hwConfig.Status = referenceString
			*hwconfigs = append(*hwconfigs, hwConfig)
		}
	}
	glog.Infof("PopulateHwConfigGeneric: (%s)", referenceString)
	return nil
}

func AfterRunPTPCommandGeneric(data *interface{}, nodeProfile *ptpv1.PtpProfile, command string) error {
	var pluginOpts string = ""
	var err error
	var optsByteArray []byte

	for name, opts := range (*nodeProfile).Plugins {
		if name == "reference" {
			optsByteArray, _ = json.Marshal(opts)
			err = json.Unmarshal(optsByteArray, &pluginOpts)
		}
	}
	glog.Infof("AfterRunPTPCommandGeneric: (%s,%s)", command, pluginOpts)
	return err
}

func Reference(name string) (*plugin.Plugin, *interface{}) {
	if name != "reference" {
		glog.Errorf("Plugin must be initialized as 'reference'")
		return nil, nil
	}
	_plugin := plugin.Plugin{Name: "reference",
		OnPTPConfigChange:  OnPTPConfigChangeGeneric,
		PopulateHwConfig:   PopulateHwConfigGeneric,
		AfterRunPTPCommand: AfterRunPTPCommandGeneric,
	}
	var referenceString string = ""
	pluginData := GenericPluginData{referenceString: &referenceString}
	var iface interface{} = &pluginData
	return &_plugin, &iface
}

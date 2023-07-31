package intel

import (
	"encoding/json"
	"fmt"
	"github.com/golang/glog"
	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/plugin"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type E810Opts struct {
	EnableDefaultConfig bool                         `json:"enableDefaultConfig"`
	UblxCmds            []E810UblxCmds               `json:"ublxCmds"`
	DevicePins          map[string]map[string]string `json:"pins"`
	DpllSettings        map[string]int64             `json:"settings"`
}

type E810UblxCmds struct {
	ReportOutput bool     `json:"reportOutput"`
	Args         []string `json:"args"`
}

type E810PluginData struct {
	hwplugins *[]string
}

// Sourced from https://github.com/RHsyseng/oot-ice/blob/main/ptp-config.sh
var EnableE810PTPConfig = `
#!/bin/bash
set -eu

ETH=$(grep -e 000e -e 000f /sys/class/net/*/device/subsystem_device | awk -F"/" '{print $5}')

for DEV in $ETH; do
  if [ -f /sys/class/net/$DEV/device/ptp/ptp*/pins/U.FL2 ]; then
    echo 0 2 > /sys/class/net/$DEV/device/ptp/ptp*/pins/U.FL2
    echo 0 1 > /sys/class/net/$DEV/device/ptp/ptp*/pins/U.FL1
    echo 0 2 > /sys/class/net/$DEV/device/ptp/ptp*/pins/SMA2
    echo 0 1 > /sys/class/net/$DEV/device/ptp/ptp*/pins/SMA1
  fi
done

echo "Disabled all SMA and U.FL Connections"
`

func OnPTPConfigChangeE810(data *interface{}, nodeProfile *ptpv1.PtpProfile) error {
	glog.Info("calling onPTPConfigChange for e810 plugin")
	var e810Opts E810Opts
	var err error
	var optsByteArray []byte
	var stdout []byte

	e810Opts.EnableDefaultConfig = false

	for name, opts := range (*nodeProfile).Plugins {
		if name == "e810" {
			optsByteArray, _ = json.Marshal(opts)
			err = json.Unmarshal(optsByteArray, &e810Opts)
			if err != nil {
				glog.Error("e810 failed to unmarshal opts: " + err.Error())
			}
			if e810Opts.EnableDefaultConfig {
				stdout, err = exec.Command("/usr/bin/bash", "-c", EnableE810PTPConfig).Output()
				glog.Infof(string(stdout))
			}
			for device, pins := range e810Opts.DevicePins {
				for pin, value := range pins {
					deviceDir := fmt.Sprintf("/sys/class/net/%s/device/ptp/", device)
					phcs, err := os.ReadDir(deviceDir)
					if err != nil {
						glog.Error("e810 failed to read " + deviceDir + ": " + err.Error())
						continue
					}
					for _, phc := range phcs {
						pinPath := fmt.Sprintf("/sys/class/net/%s/device/ptp/%s/pins/%s", device, phc.Name(), pin)
						glog.Infof("echo %s > %s", value, pinPath)
						err = os.WriteFile(pinPath, []byte(value), 0666)
						if err != nil {
							glog.Error("e810 failed to write " + value + " to " + pinPath + ": " + err.Error())
						}
					}
				}
			}
			for k, v := range e810Opts.DpllSettings {
				if (*nodeProfile).PtpSettings == nil {
					(*nodeProfile).PtpSettings = make(map[string]string)
				}
				if _, ok := (*nodeProfile).PtpSettings[k]; !ok {
					(*nodeProfile).PtpSettings[k] = strconv.FormatInt(v, 10)
				}
			}
		}
	}
	return nil
}

func AfterRunPTPCommandE810(data *interface{}, nodeProfile *ptpv1.PtpProfile, command string) error {
	glog.Info("calling AfterRunPTPCommandE810 for e810 plugin")
	var e810Opts E810Opts
	var err error
	var optsByteArray []byte
	var stdout []byte

	e810Opts.EnableDefaultConfig = false

	for name, opts := range (*nodeProfile).Plugins {
		if name == "e810" {
			optsByteArray, _ = json.Marshal(opts)
			err = json.Unmarshal(optsByteArray, &e810Opts)
			if err != nil {
				glog.Error("e810 failed to unmarshal opts: " + err.Error())
			}
			if command == "gpspipe" {
				glog.Infof("AfterRunPTPCommandE810 doing ublx config for command: %s", command)
				for _, ublxOpt := range e810Opts.UblxCmds {
					ublxArgs := ublxOpt.Args
					glog.Infof("Running /usr/bin/ubxtool with args %s", strings.Join(ublxArgs, ", "))
					stdout, err = exec.Command("/usr/local/bin/ubxtool", ublxArgs...).CombinedOutput()
					//stdout, err = exec.Command("/usr/local/bin/ubxtool", "-p", "STATUS").CombinedOutput()
					_data := *data
					if data != nil && ublxOpt.ReportOutput {
						glog.Infof("Saving status to hwconfig: %s", string(stdout))
						var pluginData *E810PluginData = _data.(*E810PluginData)
						_pluginData := *pluginData
						statusString := fmt.Sprintf("ublx data: %s", string(stdout))
						*_pluginData.hwplugins = append(*_pluginData.hwplugins, statusString)
					} else {
						glog.Infof("Not saving status to hwconfig: %s", string(stdout))
					}
				}
			} else {
				glog.Infof("AfterRunPTPCommandE810 doing nothing for command: %s", command)
			}
		}
	}
	return nil
}

func PopulateHwConfigE810(data *interface{}, hwconfigs *[]ptpv1.HwConfig) error {
	//hwConfig := ptpv1.HwConfig{}
	//hwConfig.DeviceID = "e810"
	//*hwconfigs = append(*hwconfigs, hwConfig)
	if data != nil {
		_data := *data
		var pluginData *E810PluginData = _data.(*E810PluginData)
		_pluginData := *pluginData
		if _pluginData.hwplugins != nil {
			for _, _hwconfig := range *_pluginData.hwplugins {
				hwConfig := ptpv1.HwConfig{}
				hwConfig.DeviceID = "e810"
				hwConfig.Status = _hwconfig
				*hwconfigs = append(*hwconfigs, hwConfig)
			}
		}
	}
	return nil
}

func E810(name string) (*plugin.Plugin, *interface{}) {
	if name != "e810" {
		glog.Errorf("Plugin must be initialized as 'e810'")
		return nil, nil
	}
	glog.Infof("registering e810 plugin")
	hwplugins := []string{}
	pluginData := E810PluginData{hwplugins: &hwplugins}
	_plugin := plugin.Plugin{Name: "e810",
		OnPTPConfigChange:  OnPTPConfigChangeE810,
		AfterRunPTPCommand: AfterRunPTPCommandE810,
		PopulateHwConfig:   PopulateHwConfigE810,
	}
	var iface interface{} = &pluginData
	return &_plugin, &iface
}

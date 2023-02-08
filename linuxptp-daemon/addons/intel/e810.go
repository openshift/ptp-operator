package intel

import (
	"encoding/json"
	"github.com/golang/glog"
	"github.com/openshift/linuxptp-daemon/pkg/plugin"
	ptpv1 "github.com/openshift/ptp-operator/api/v1"
	"os/exec"
)

type E810Opts struct {
	EnableDefaultPTPConfig bool `json:"enableDefaultPTPConfig"`
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

func OnPTPConfigChangeE810(nodeProfile *ptpv1.PtpProfile) error {
	glog.Info("calling onPTPConfigChange for exec plugin")
	var e810Opts E810Opts
	var err error
	var optsByteArray []byte
	var stdout []byte

	e810Opts.EnableDefaultPTPConfig = false

	for name, opts := range (*nodeProfile).Plugins {
		if name == "e810" {
			optsByteArray, _ = json.Marshal(opts)
			err = json.Unmarshal(optsByteArray, &e810Opts)
			if err != nil {
				glog.Error("exec failed to unmarshal opts: " + err.Error())
			}
			if e810Opts.EnableDefaultPTPConfig {
				stdout, err = exec.Command("/usr/bin/bash", "-c", EnableE810PTPConfig).Output()
				glog.Infof(string(stdout))
			}
		}
	}
	return nil
}

func E810(name string) *plugin.Plugin {
	if name != "e810" {
		glog.Errorf("Plugin must be initialized as 'e810'")
		return nil
	}
	glog.Infof("registering e810 plugin")
	return &plugin.Plugin{Name: "e810",
		OnPTPConfigChange: OnPTPConfigChangeE810,
	}
}

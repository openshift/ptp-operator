package daemon

import (
	"github.com/golang/glog"
	ptpv1 "github.com/openshift/ptp-operator/pkg/apis/ptp/v1"
	ptpnetwork "github.com/openshift/linuxptp-daemon/pkg/network"
)

func getDevStatusUpdate(nodePTPDev *ptpv1.NodePtpDevice) (*ptpv1.NodePtpDevice, error) {
	hostDevs, err := ptpnetwork.DiscoverPTPDevices()
	if err != nil {
		glog.Errorf("discover PTP devices failed: %v", err)
		return nodePTPDev, err
	}
	glog.Infof("PTP capable NICs: %v", hostDevs)
	for _, hostDev := range hostDevs {
		contained := false
		for _, crDev := range nodePTPDev.Status.Devices {
			if hostDev == crDev.Name {
				contained = true
				break
			}
		}
		if !contained {
			nodePTPDev.Status.Devices = append(nodePTPDev.Status.Devices,
				ptpv1.PtpDevice{Name: hostDev, Profile: ""})
		}
	}
	return nodePTPDev, nil
}

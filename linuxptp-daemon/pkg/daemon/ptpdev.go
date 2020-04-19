package daemon

import (
	"github.com/golang/glog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"

	ptpv1 "github.com/openshift/ptp-operator/pkg/apis/ptp/v1"
	ptpclient "github.com/openshift/ptp-operator/pkg/client/clientset/versioned"

	ptpnetwork "github.com/openshift/linuxptp-daemon/pkg/network"
)

func GetDevStatusUpdate(nodePTPDev *ptpv1.NodePtpDevice) (*ptpv1.NodePtpDevice, error) {
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

func runDeviceStatusUpdate(ptpClient *ptpclient.Clientset, nodeName string) {
	// Discover PTP capable devices
	// Don't return in case of discover failure
	ptpDevs, err := ptpnetwork.DiscoverPTPDevices()
	if err != nil {
		glog.Errorf("discover PTP devices failed: %v", err)
	}
	glog.Infof("PTP capable NICs: %v", ptpDevs)

	// Assume NodePtpDevice CR for this particular node
	// is already created manually or by PTP-Operator.
	ptpDev, err := ptpClient.PtpV1().NodePtpDevices(PtpNamespace).Get(nodeName, metav1.GetOptions{})
	if err != nil {
		glog.Errorf("failed to get NodePtpDevice CR for node %s: %v", nodeName, err)
	}

	// Render status of NodePtpDevice CR by inspecting PTP capability of node network devices
	ptpDev, err = GetDevStatusUpdate(ptpDev)
	if err != nil {
		glog.Errorf("failed to get device status: %v", err)
	}

	// Update NodePtpDevice CR
	_, err = ptpClient.PtpV1().NodePtpDevices(PtpNamespace).UpdateStatus(ptpDev)
	if err != nil {
		glog.Errorf("failed to update Node PTP device CR: %v", err)
	}
}

func RunDeviceStatusUpdate(ptpClient *ptpclient.Clientset, nodeName string) {
	t := time.Tick(1 * time.Minute)
	for {
		glog.Info("run device status update function")
		runDeviceStatusUpdate(ptpClient, nodeName)
		<-t
	}
}

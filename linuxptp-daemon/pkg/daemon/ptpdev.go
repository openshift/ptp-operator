package daemon

import (
	"context"
	"github.com/golang/glog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"
	ptpclient "github.com/openshift/ptp-operator/pkg/client/clientset/versioned"

	ptpnetwork "github.com/openshift/linuxptp-daemon/pkg/network"
)

func populateNodePTPDevices(nodePTPDev *ptpv1.NodePtpDevice, hwconfigs *[]ptpv1.HwConfig) (*ptpv1.NodePtpDevice, error) {
	nodePTPDev.Status.Hwconfig = []ptpv1.HwConfig{}
	for _, hw := range *hwconfigs {
		nodePTPDev.Status.Hwconfig = append(nodePTPDev.Status.Hwconfig, hw)
	}
	return nodePTPDev, nil
}

func GetDevStatusUpdate(nodePTPDev *ptpv1.NodePtpDevice) (*ptpv1.NodePtpDevice, error) {
	hostDevs, err := ptpnetwork.DiscoverPTPDevices()
	if err != nil {
		glog.Errorf("discover PTP devices failed: %v", err)
		return nodePTPDev, err
	}
	glog.Infof("PTP capable NICs: %v", hostDevs)

	newDevices := make([]ptpv1.PtpDevice, 0)
	for _, hostDev := range hostDevs {
		newDevices = append(newDevices, ptpv1.PtpDevice{Name: hostDev, Profile: ""})
	}
	nodePTPDev.Status.Devices = newDevices
	return nodePTPDev, nil
}

func runDeviceStatusUpdate(ptpClient *ptpclient.Clientset, nodeName string, hwconfigs *[]ptpv1.HwConfig) {
	// Discover PTP capable devices
	// Don't return in case of discover failure
	ptpDevs, err := ptpnetwork.DiscoverPTPDevices()
	if err != nil {
		glog.Errorf("discover PTP devices failed: %v", err)
	}
	glog.Infof("PTP capable NICs: %v", ptpDevs)

	// Assume NodePtpDevice CR for this particular node
	// is already created manually or by PTP-Operator.
	ptpDev, err := ptpClient.PtpV1().NodePtpDevices(PtpNamespace).Get(context.TODO(), nodeName, metav1.GetOptions{})
	if err != nil {
		glog.Errorf("failed to get NodePtpDevice CR for node %s: %v", nodeName, err)
	}

	// Render status of NodePtpDevice CR by inspecting PTP capability of node network devices
	ptpDev, err = GetDevStatusUpdate(ptpDev)
	if err != nil {
		glog.Errorf("failed to get device status: %v", err)
	}

	//Populate hwconfig
	ptpDev, err = populateNodePTPDevices(ptpDev, hwconfigs)
	if err != nil {
		glog.Errorf("failed to populate node ptp devices: %v", err)
	}

	// Update NodePtpDevice CR
	_, err = ptpClient.PtpV1().NodePtpDevices(PtpNamespace).UpdateStatus(context.TODO(), ptpDev, metav1.UpdateOptions{})
	if err != nil {
		glog.Errorf("failed to update Node PTP device CR: %v", err)
	}
}

func RunDeviceStatusUpdate(ptpClient *ptpclient.Clientset, nodeName string, hwconfigs *[]ptpv1.HwConfig) {
	glog.Info("run device status update function")
	runDeviceStatusUpdate(ptpClient, nodeName, hwconfigs)
}

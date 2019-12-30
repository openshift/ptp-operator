package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/golang/glog"
	"github.com/openshift/linuxptp-daemon/pkg/config"
	"github.com/openshift/linuxptp-daemon/pkg/daemon"
	"github.com/openshift/linuxptp-daemon/pkg/network"
	ptpv1 "github.com/openshift/ptp-operator/pkg/apis/ptp/v1"
	ptpclient "github.com/openshift/ptp-operator/pkg/client/clientset/versioned"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
        "k8s.io/client-go/kubernetes"
)

type cliParams struct {
	updateInterval	int
	profileDir	string
}

// Parse Command line flags
func flagInit(cp *cliParams) {
        flag.IntVar(&cp.updateInterval, "update-interval", config.DefaultUpdateInterval,
		"Interval to update PTP status")
        flag.StringVar(&cp.profileDir, "linuxptp-profile-path", config.DefaultProfilePath,
		"profile to start linuxptp processes")
}


func main() {
	cp := &cliParams{}
	flag.Parse()
	flagInit(cp)

	glog.Infof("resync period set to: %d [s]", cp.updateInterval)
	glog.Infof("linuxptp profile path set to: %s", cp.profileDir)

	cfg, err := config.GetKubeConfig()
	if err != nil {
		glog.Errorf("get kubeconfig failed: %v", err)
		return
	}
	glog.Infof("successfully get kubeconfig")

        kubeClient, err := kubernetes.NewForConfig(cfg)
        if err != nil {
                glog.Errorf("cannot create new config for kubeClient: %v", err)
                return
        }

	ptpClient, err := ptpclient.NewForConfig(cfg)
	if err != nil {
		glog.Errorf("cannot create new config for ptpClient: %v", err)
		return
	}

	// Discover PTP capable devices
	// Don't return in case of discover failure
	ptpDevs, err := network.DiscoverPTPDevices()
	if err != nil {
		glog.Errorf("discover PTP devices failed: %v", err)
	}
	glog.Infof("PTP capable NICs: %v", ptpDevs)


	// Update NodePtpDevice Custom Resource
	// TODO: move this one-shoot update to a loop thread for continuous update

	// The name of NodePtpDevice CR for this node is equal to the node name
	nodeName := os.Getenv("NODE_NAME")

	// Assume NodePtpDevice CR for this particular node
	// is already created manually or by PTP-Operator.
	ptpDev, err := ptpClient.PtpV1().NodePtpDevices(daemon.PtpNamespace).Get(nodeName, metav1.GetOptions{})
	if err != nil {
		glog.Errorf("failed to get NodePtpDevice CR for node %s: %v", nodeName, err)
	}

	// Render status of NodePtpDevice CR by inspecting PTP capability of node network devices
	ptpDev, _ = daemon.GetDevStatusUpdate(ptpDev)

	// Update NodePtpDevice CR
	_, err = ptpClient.PtpV1().NodePtpDevices(daemon.PtpNamespace).UpdateStatus(ptpDev)
	if err != nil {
		glog.Errorf("failed to update Node PTP device CR: %v", err)
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	ptpConfUpdate := &daemon.LinuxPTPConfUpdate{UpdateCh: make(chan bool)}
	go daemon.New(
		nodeName,
		daemon.PtpNamespace,
		kubeClient,
		ptpConfUpdate,
		stopCh,
	).Run()

	tickerPull := time.NewTicker(time.Second * time.Duration(cp.updateInterval))
	defer tickerPull.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	var appliedNodeProfileJson []byte
	ptpConfig := &ptpv1.PtpProfile{}

	daemon.StartMetricsServer("0.0.0.0:9091")

	for {
		select {
		case <-tickerPull.C:
			glog.Infof("ticker pull")
			nodeProfile := filepath.Join(cp.profileDir, nodeName)
			if _, err := os.Stat(nodeProfile); err != nil {
				if os.IsNotExist(err) {
					glog.Infof("ptp profile doesn't exist for node: %v", nodeName)
					continue
				} else {
					glog.Errorf("error stating node profile %v: %v", nodeName, err)
					continue
				}
			}
			nodeProfileJson, err := ioutil.ReadFile(nodeProfile)
			if err != nil {
				glog.Errorf("error reading node profile: %v", nodeProfile)
				continue
			}
			if string(appliedNodeProfileJson) == string(nodeProfileJson) {
				continue
			}
			err = json.Unmarshal(nodeProfileJson, ptpConfig)
			if err != nil {
				glog.Errorf("failed to json.Unmarshal ptp profile: %v", err)
				continue
			}
			appliedNodeProfileJson = nodeProfileJson
			ptpConfUpdate.NodeProfile = ptpConfig
			ptpConfUpdate.UpdateCh <- true
		case sig := <-sigCh:
			glog.Infof("signal received, shutting down", sig)
			return
		}
	}
}

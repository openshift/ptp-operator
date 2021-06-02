package main

import (
	"flag"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/golang/glog"
	"k8s.io/client-go/kubernetes"

	"github.com/openshift/linuxptp-daemon/pkg/config"
	"github.com/openshift/linuxptp-daemon/pkg/daemon"
	ptpclient "github.com/openshift/ptp-operator/pkg/client/clientset/versioned"
)

type cliParams struct {
	updateInterval int
	profileDir     string
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

	// The name of NodePtpDevice CR for this node is equal to the node name
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		glog.Error("cannot find NODE_NAME environment variable")
		return
	}

	// The name of NodePtpDevice CR for this node is equal to the node name
	var stdoutToSocket = false
	if val, ok := os.LookupEnv("LOGS_TO_SOCKET"); ok && val != "" {
		if ret, err := strconv.ParseBool(val); err == nil {
			stdoutToSocket = ret
		}
	}

	// Run a loop to update the device status
	go daemon.RunDeviceStatusUpdate(ptpClient, nodeName)

	stopCh := make(chan struct{})
	defer close(stopCh)

	ptpConfUpdate, err := daemon.NewLinuxPTPConfUpdate()
	if err != nil {
		glog.Errorf("failed to create a ptp config update: %v", err)
		return
	}

	go daemon.New(
		nodeName,
		daemon.PtpNamespace,
		stdoutToSocket,
		kubeClient,
		ptpConfUpdate,
		stopCh,
	).Run()

	tickerPull := time.NewTicker(time.Second * time.Duration(cp.updateInterval))
	defer tickerPull.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	// by default metrics is hosted here,if LOGS_TO_SOCKET variable is set then metrics are disabled
	if !stdoutToSocket { // if not sending metrics (log) out to a socket then host metrics here
		daemon.StartMetricsServer("0.0.0.0:9091")
	}

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
			nodeProfilesJson, err := ioutil.ReadFile(nodeProfile)
			if err != nil {
				glog.Errorf("error reading node profile: %v", nodeProfile)
				continue
			}

			err = ptpConfUpdate.UpdateConfig(nodeProfilesJson)
			if err != nil {
				glog.Errorf("error updating the node configuration using the profiles loaded: %v", err)
			}
		case sig := <-sigCh:
			glog.Info("signal received, shutting down", sig)
			return
		}
	}
}

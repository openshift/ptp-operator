package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/golang/glog"
	"github.com/openshift/linuxptp-daemon/pkg/config"
	"github.com/openshift/linuxptp-daemon/pkg/daemon"

        "k8s.io/client-go/kubernetes"
)

type cliParams struct {
	updateInterval	int
	profile		string
}

// Parse Command line flags
func flagInit(cp *cliParams) {
        flag.IntVar(&cp.updateInterval, "update-interval", config.DefaultUpdateInterval,
		"Interval to update PTP status")
        flag.StringVar(&cp.profile, "linuxptp-profile", config.DefaultProfilePath,
		"profile to start linuxptp processes")
}


func main() {
	cp := &cliParams{}
	flag.Parse()
	flagInit(cp)

	glog.Infof("resync period set to: %d [s]", cp.updateInterval)
	glog.Infof("linuxptp profile path set to: %s", cp.profile)

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

	stopCh := make(chan struct{})
	defer close(stopCh)

	ptpConfUpdate := &daemon.LinuxPTPConfUpdate{UpdateCh: make(chan bool)}
	go daemon.New(
		os.Getenv("PTP_NODE_NAME"),
		daemon.PtpNamespace,
		kubeClient,
		ptpConfUpdate,
		stopCh,
	).Run()

	cfgWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		glog.Fatalf("error starting fsnotify watcher: %v", err)
	}
	defer cfgWatcher.Close()
	cfgWatcher.Add(cp.profile)

	tickerPull := time.NewTicker(time.Second * time.Duration(cp.updateInterval))
	defer tickerPull.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	for {
		select {
		case <-tickerPull.C:
			glog.Infof("ticker pull")
		case event, ok := <-cfgWatcher.Events:
			if !ok {
				continue
			}
			glog.Infof("cfgWatcher event: %v", event)
			if event.Op&fsnotify.Remove == fsnotify.Remove {
				ptpConfUpdate.UpdateCh <- true
			}
		case sig := <-sigCh:
			glog.Infof("signal received, shutting down", sig)
			return
		}
	}
}

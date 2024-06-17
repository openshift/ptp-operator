package gnss

import (
	"time"

	"github.com/golang/glog"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/event"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/ublox"
)

const (
	connectionRetryInterval = 1 * time.Second
	eventSocket             = "/cloud-native/events.sock"
)
const (
	processName = "GNSS"
)

// MonitorGNSSEvents ... monitor gnss events
func MonitorGNSSEvents(clockType event.ClockType, cfgName string, pluginName string, closeCh <-chan bool, eventChannel chan<- event.EventChannel) error {
	//done := make(chan struct{}) // Done setting up logging.  Go ahead and wait for process
	var currentNavStatus int64
	var err error
	currentNavStatus = 0
	glog.Infof("Starting GNSS Monitoring for plugin  %s...", pluginName)
	var ublx *ublox.UBlox
retry:
	if ublx, err = ublox.NewUblox(); err != nil {
		glog.Errorf("failed to initialize GNSS monitoring via ublox %s", err)
		time.Sleep(10 * time.Second)
		goto retry
	} else {
		//TODO: monitor on 1PPS  events trigger
		ticker := time.NewTicker(1 * time.Second)
		for {
			select {
			case <-ticker.C:
				// do stuff
				nStatus, errs := ublx.NavStatus()
				if errs == nil && currentNavStatus != nStatus { // only if status changed
					glog.Infof("sending Nav status event to event handler Process:%s Type: "+
						"%s Status %s", processName, event.GNSS_STATUS, nStatus)
					currentNavStatus = nStatus
					eventChannel <- event.EventChannel{
						ProcessName: processName,
						Type:        event.GNSS_STATUS,
						CfgName:     cfgName,
						Value:       nStatus,
						ClockType:   clockType,
						Update:      true,
					}
				} else if errs != nil {
					glog.Errorf("failed to monitor nav status %s", errs)
				}
			case <-closeCh:
				ticker.Stop()
				return nil
			}
		}
	}
}

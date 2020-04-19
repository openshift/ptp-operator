package daemon

import (
	"fmt"
	"github.com/golang/glog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilwait "k8s.io/apimachinery/pkg/util/wait"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	ptpv1 "github.com/openshift/ptp-operator/pkg/apis/ptp/v1"
)

const (
	PTPNamespace = "openshift"
	PTPSubsystem = "ptp"

	ptp4lProcessName   = "ptp4l"
	phc2sysProcessName = "phc2sys"
)

var (
	NodeName = ""

	OffsetFromMaster = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "offset_from_master",
			Help:      "",
		}, []string{"process", "node"})

	MaxOffsetFromMaster = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "max_offset_from_master",
			Help:      "",
		}, []string{"process", "node"})

	FrequencyAdjustment = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "frequency_adjustment",
			Help:      "",
		}, []string{"process", "node"})

	DelayFromMaster = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name:      "delay_from_master",
			Help:      "",
		}, []string{"process", "node"})
)

var registerMetrics sync.Once

func RegisterMetrics(nodeName string) {
	registerMetrics.Do(func() {
		prometheus.MustRegister(OffsetFromMaster)
		prometheus.MustRegister(MaxOffsetFromMaster)
		prometheus.MustRegister(FrequencyAdjustment)
		prometheus.MustRegister(DelayFromMaster)

		// Including these stats kills performance when Prometheus polls with multiple targets
		prometheus.Unregister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
		prometheus.Unregister(prometheus.NewGoCollector())

		NodeName = nodeName
	})
}

// updatePTPMetrics ...
func updatePTPMetrics(process string, offsetFromMaster, maxOffsetFromMaster, frequencyAdjustment, delayFromMaster float64) {
	OffsetFromMaster.With(prometheus.Labels{
		"process": process, "node": NodeName}).Set(offsetFromMaster)

	MaxOffsetFromMaster.With(prometheus.Labels{
		"process": process, "node": NodeName}).Set(maxOffsetFromMaster)

	FrequencyAdjustment.With(prometheus.Labels{
		"process": process, "node": NodeName}).Set(frequencyAdjustment)

	DelayFromMaster.With(prometheus.Labels{
		"process": process, "node": NodeName}).Set(delayFromMaster)
}

// extractMetrics ...
func extractMetrics(processName, output string) {
	if strings.Contains(output, "max") {
		offsetFromMaster, maxOffsetFromMaster, frequencyAdjustment, delayFromMaster := extractSummaryMetrics(processName, output)
		updatePTPMetrics(processName, offsetFromMaster, maxOffsetFromMaster, frequencyAdjustment, delayFromMaster)
	}
}

func extractSummaryMetrics(processName, output string) (offsetFromMaster, maxOffsetFromMaster, frequencyAdjustment, delayFromMaster float64) {
	// remove everything before the rms string
	// This makes the out to equals
	indx := strings.Index(output, "rms")
	output = output[indx:]
	fields := strings.Fields(output)

	offsetFromMaster, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		glog.Errorf("%s failed to parse offset from master output %s error %v", processName, fields[1], err)
	}

	maxOffsetFromMaster, err = strconv.ParseFloat(fields[3], 64)
	if err != nil {
		glog.Errorf("%s failed to parse max offset from master output %s error %v", processName, fields[3], err)
	}

	frequencyAdjustment, err = strconv.ParseFloat(fields[5], 64)
	if err != nil {
		glog.Errorf("%s failed to parse frequency adjustment output %s error %v", processName, fields[5], err)
	}

	if len(fields) >= 10 {
		delayFromMaster, err = strconv.ParseFloat(fields[9], 64)
		if err != nil {
			glog.Errorf("%s failed to parse delay from master output %s error %v", processName, fields[9], err)
		}
	} else {
		// If there is no delay from master this mean we are out of sync
		glog.Warningf("no delay from master process %s out of sync", processName)
	}

	return
}

func addFlagsForMonitor(nodeProfile *ptpv1.PtpProfile) {
	// If output doesn't exist we add it for the prometheus exporter
	if nodeProfile.Phc2sysOpts != nil {
		if !strings.Contains(*nodeProfile.Phc2sysOpts, "-m") {
			glog.Info("adding -m to print messages to stdout for phc2sys to use prometheus exporter")
			*nodeProfile.Phc2sysOpts = fmt.Sprintf("%s -m", *nodeProfile.Phc2sysOpts)
		}

		if !strings.Contains(*nodeProfile.Phc2sysOpts, "-u") {
			glog.Info("adding -u 1 to print summary messages to stdout for phc2sys to use prometheus exporter")
			*nodeProfile.Phc2sysOpts = fmt.Sprintf("%s -u 1", *nodeProfile.Phc2sysOpts)
		}
	}

	// If output doesn't exist we add it for the prometheus exporter
	if nodeProfile.Ptp4lOpts != nil {
		if !strings.Contains(*nodeProfile.Ptp4lOpts, "-m") {
			glog.Info("adding -m to print messages to stdout for ptp4l to use prometheus exporter")
			*nodeProfile.Ptp4lOpts = fmt.Sprintf("%s -m", *nodeProfile.Ptp4lOpts)
		}

		if !strings.Contains(*nodeProfile.Ptp4lOpts, "--summary_interval") {
			glog.Info("adding --summary_interval 1 to print summary messages to stdout for ptp4l to use prometheus exporter")
			*nodeProfile.Ptp4lOpts = fmt.Sprintf("%s --summary_interval 1", *nodeProfile.Ptp4lOpts)
		}
	}
}

// StartMetricsServer runs the prometheus listner so that metrics can be collected
func StartMetricsServer(bindAddress string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	go utilwait.Until(func() {
		err := http.ListenAndServe(bindAddress, mux)
		if err != nil {
			utilruntime.HandleError(fmt.Errorf("starting metrics server failed: %v", err))
		}
	}, 5*time.Second, utilwait.NeverStop)
}

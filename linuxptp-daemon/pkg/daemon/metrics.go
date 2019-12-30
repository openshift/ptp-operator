package daemon

import (
	"fmt"
	"github.com/golang/glog"
	"strconv"
	"strings"
	"sync"
	"time"
	"net/http"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilwait "k8s.io/apimachinery/pkg/util/wait"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	PTPNamespace = "openshift"
	PTPSubsystem = "ptp"

	ptp4lProcessName = "ptp4l"
	phc2sysProcessName = "phc2sys"
)

var (
	NodeName = ""

	OffsetFromMaster = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name: "offset_from_master",
			Help: "",
		},[]string{"process","node"})

	StateOfPI = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name: "state_of_pi",
			Help: "",
		},[]string{"process","node"})

	FrequencyAdjustment = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name: "frequency_adjustment",
			Help: "",
		},[]string{"process","node"})

	DelayFromMaster = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: PTPNamespace,
			Subsystem: PTPSubsystem,
			Name: "delay_from_master",
			Help: "",
		},[]string{"process","node"})
)

var registerMetrics sync.Once

func RegisterMetrics(nodeName string) {
	registerMetrics.Do(func() {
		prometheus.MustRegister(OffsetFromMaster)
		prometheus.MustRegister(StateOfPI)
		prometheus.MustRegister(FrequencyAdjustment)
		prometheus.MustRegister(DelayFromMaster)

		// Including these stats kills performance when Prometheus polls with multiple targets
		prometheus.Unregister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
		prometheus.Unregister(prometheus.NewGoCollector())

		NodeName = nodeName
	})
}

// updatePTPMetrics ...
func updatePTPMetrics(process string, offsetFromMaster, stateOfPI, frequencyAdjustment, delayFromMaster float64) {
	OffsetFromMaster.With(prometheus.Labels{
	"process": process,"node": NodeName}).Set(offsetFromMaster)

	StateOfPI.With(prometheus.Labels{
	"process": process,"node": NodeName}).Set(stateOfPI)

	FrequencyAdjustment.With(prometheus.Labels{
		"process": process,"node": NodeName}).Set(frequencyAdjustment)

	DelayFromMaster.With(prometheus.Labels{
		"process": process,"node": NodeName}).Set(delayFromMaster)
}

// extractMetrics ...
func extractMetrics(processName, output string){
	if processName == ptp4lProcessName && strings.Contains(output,"offset") {
		offsetFromMaster, stateOfPI, frequencyAdjustment, delayFromMaster := extractPtp4lMetrics(output)
		updatePTPMetrics(processName,offsetFromMaster, stateOfPI, frequencyAdjustment, delayFromMaster)
	}

	if processName == phc2sysProcessName && strings.Contains(output,"offset") {
		offsetFromMaster, stateOfPI, frequencyAdjustment, delayFromMaster := extractPhc2sysMetrics(output)
		updatePTPMetrics(processName,offsetFromMaster, stateOfPI, frequencyAdjustment, delayFromMaster)
	}
}

func extractPtp4lMetrics(output string) (offsetFromMaster, stateOfPI, frequencyAdjustment, delayFromMaster float64) {
	fields := strings.Fields(output)

	if len(fields) != 10 {
		glog.Error("ptp4l failed to extract metrics unknown output format")
		return
	}

	offsetFromMaster, err := strconv.ParseFloat(fields[3], 64)
	if err != nil {
		glog.Errorf("ptp4l failed to parse offset from master output %s error %v",fields[3], err)
	}

	if len(fields[4]) != 2 {
		glog.Errorf("ptp4l failed to parse state of pi output %s",fields[4])
	}

	stateOfPI, err = strconv.ParseFloat(string(fields[4][1]), 64)
	if err != nil {
		glog.Errorf("ptp4l failed to parse parse state of pi output %s error %v",string(fields[4][1]), err)
	}

	frequencyAdjustment, err = strconv.ParseFloat(fields[6], 64)
	if err != nil {
		glog.Errorf("ptp4l failed to parse frequency adjustment output %s error %v",fields[6], err)
	}

	delayFromMaster, err = strconv.ParseFloat(fields[9], 64)
	if err != nil {
		glog.Errorf("ptp4l failed to parse delay from master output %s error %v",fields[9], err)
	}

	return
}

func extractPhc2sysMetrics(output string) (offsetFromMaster, stateOfPI, frequencyAdjustment, delayFromMaster float64) {
	fields := strings.Fields(output)

	if len(fields) != 10 {
		glog.Info("phc2sys failed to extract metrics unknown output format")
		return
	}

	offsetFromMaster, err := strconv.ParseFloat(fields[4], 64)
	if err != nil {
		glog.Errorf("phc2sys failed to parse offset from master output %s error %v",fields[4], err)
	}

	if len(fields[5]) != 2 {
		glog.Errorf("phc2sys failed to parse state of pi output %s",fields[5])
	}

	stateOfPI, err = strconv.ParseFloat(string(fields[5][1]), 64)
	if err != nil {
		glog.Errorf("phc2sys failed to parse parse state of pi output %s error %v",string(fields[5][1]), err)
	}

	frequencyAdjustment, err = strconv.ParseFloat(fields[7], 64)
	if err != nil {
		glog.Errorf("phc2sys failed to parse frequency adjustment output %s error %v",fields[7], err)
	}

	delayFromMaster, err = strconv.ParseFloat(fields[9], 64)
	if err != nil {
		glog.Errorf("phc2sys failed to parse delay from master output %s error %v",fields[9], err)
	}

	return
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

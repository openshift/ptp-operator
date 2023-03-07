package test

import (
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func TestLoadConfiguration(t *testing.T) {
	cfg := PtpTestConfig{}
	path, _ := os.Getwd()

	cfg.loadPtpTestConfig(fmt.Sprintf("%s/%s", path, "testdata/cfg1.yaml"))
	assert.Equal(t, int64(5), cfg.SoakTestConfig.SlaveClockSyncConfig.TestSpec.Duration)
	assert.Equal(t, 100, cfg.GlobalConfig.MaxOffset)
	assert.Equal(t, -100, cfg.GlobalConfig.MinOffset)
	assert.Equal(t, true, cfg.SoakTestConfig.SlaveClockSyncConfig.TestSpec.Enable)
	assert.Equal(t, 10, cfg.SoakTestConfig.SlaveClockSyncConfig.TestSpec.FailureThreshold)

	cfg = PtpTestConfig{}
	cfg.loadPtpTestConfig(fmt.Sprintf("%s/%s", path, "testdata/cfg2.yaml"))
	assert.Equal(t, int64(9), cfg.SoakTestConfig.SlaveClockSyncConfig.TestSpec.Duration)
	assert.Equal(t, 19, cfg.GlobalConfig.MaxOffset)
	assert.Equal(t, -15, cfg.GlobalConfig.MinOffset)

	assert.Equal(t, 5, cfg.SoakTestConfig.FailureThreshold)
	assert.Equal(t, true, cfg.SoakTestConfig.SlaveClockSyncConfig.TestSpec.Enable)
	assert.Equal(t, "some-description", cfg.SoakTestConfig.SlaveClockSyncConfig.Description)
}

func TestLoadCpuUsageTestConfig(t *testing.T) {
	cpuTestStr := `
global:
  maxoffset: 100
  minoffset: -100
soaktest:
  disable_all: false
  event_output_file: "./event-output.csv"
  duration: 2
  failure_threshold: 8
  cpu_utilization:
    spec:
      enable: true
      duration: 2
      failure_threshold: 8
      custom_params:
        prometheus_rate_time_window: "61s"
        node:
          cpu_threshold_mcores: 2
        pod:
          - pod_type: "ptp-operator"
            cpu_threshold_mcores: 4

          - pod_type: "linuxptp-daemon"
            container: "cloud-event-proxy"
            cpu_threshold_mcores: 6
    desc: "The test measures PTP CPU usage and fails if >15mcores"
`

	cfg := PtpTestConfig{}
	err := yaml.Unmarshal([]byte(cpuTestStr), &cfg)
	if err != nil {
		log.Fatalf("Unmarshal: %v", err)
	}

	cpuTest := &cfg.SoakTestConfig.CpuUtilization
	t.Logf("cpuTest: %+v", cpuTest)

	assert.Equal(t, true, cpuTest.CpuTestSpec.Enable)
	assert.Equal(t, int64(2), cpuTest.CpuTestSpec.Duration)
	assert.Equal(t, 8, cpuTest.CpuTestSpec.FailureThreshold)

	promTimeWindow, err := cpuTest.PromRateTimeWindow()
	assert.Nil(t, err)

	assert.Equal(t, 61*time.Second, promTimeWindow)

	assert.NotNil(t, cpuTest.CpuTestSpec.CustomParams.Node)
	assert.NotNil(t, cpuTest.CpuTestSpec.CustomParams.Pod)

	checkNodeCpu, nodeCpuThreshold := cpuTest.ShouldCheckNodeTotalCpuUsage()
	assert.Equal(t, true, checkNodeCpu)
	assert.Equal(t, float64(.002), nodeCpuThreshold)

	checkPodCpu, _ := cpuTest.ShouldCheckPodCpuUsage("asdfasdf")
	assert.Equal(t, false, checkPodCpu)

	checkPodCpu, podCpuThreshold := cpuTest.ShouldCheckPodCpuUsage("ptp-operator-3jxx3")
	assert.Equal(t, true, checkPodCpu)
	assert.Equal(t, float64(.004), podCpuThreshold)

	checkPodCpu, podCpuThreshold = cpuTest.ShouldCheckContainerCpuUsage("linuxptp-daemon-abcde", "cloud-event-proxy")
	assert.Equal(t, true, checkPodCpu)
	assert.Equal(t, float64(.006), podCpuThreshold)
}

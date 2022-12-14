//go:build !unittests
// +build !unittests

package test

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	v1 "k8s.io/api/core/v1"

	ptptestconfig "github.com/openshift/ptp-operator/test/conformance/config"
	"github.com/openshift/ptp-operator/test/pkg"
	"github.com/openshift/ptp-operator/test/pkg/client"
	testclient "github.com/openshift/ptp-operator/test/pkg/client"
	"github.com/openshift/ptp-operator/test/pkg/execute"
	"github.com/openshift/ptp-operator/test/pkg/pods"
	"github.com/openshift/ptp-operator/test/pkg/ptptesthelper"
	"github.com/openshift/ptp-operator/test/pkg/testconfig"
	v1core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
)

var _ = Describe("[ptp-long-running]", func() {
	var fullConfig testconfig.TestConfig
	var testParameters ptptestconfig.PtpTestConfig

	execute.BeforeAll(func() {
		testParameters = ptptestconfig.GetPtpTestConfig()
		testclient.Client = testclient.New("")
		Expect(testclient.Client).NotTo(BeNil())
	})

	Context("Soak testing", func() {

		BeforeEach(func() {
			if fullConfig.Status == testconfig.DiscoveryFailureStatus {
				Skip("Failed to find a valid ptp slave configuration")
			}

			ptpPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: pkg.PtpLinuxDaemonPodsLabel})
			Expect(err).NotTo(HaveOccurred())
			Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")

			ptpSlaveRunningPods := []v1core.Pod{}
			ptpMasterRunningPods := []v1core.Pod{}

			for podIndex := range ptpPods.Items {
				if role, _ := pods.PodRole(&ptpPods.Items[podIndex], pkg.PtpClockUnderTestNodeLabel); role {
					pods.WaitUntilLogIsDetected(&ptpPods.Items[podIndex], pkg.TimeoutIn3Minutes, "Profile Name:")
					ptpSlaveRunningPods = append(ptpSlaveRunningPods, ptpPods.Items[podIndex])
				} else if role, _ := pods.PodRole(&ptpPods.Items[podIndex], pkg.PtpGrandmasterNodeLabel); role {
					pods.WaitUntilLogIsDetected(&ptpPods.Items[podIndex], pkg.TimeoutIn3Minutes, "Profile Name:")
					ptpMasterRunningPods = append(ptpMasterRunningPods, ptpPods.Items[podIndex])
				}
			}

			if testconfig.GlobalConfig.DiscoveredGrandMasterPtpConfig != nil {
				Expect(len(ptpMasterRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP master pods on Cluster")
				Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
			} else {
				Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
			}
			//ptpRunningPods = append(ptpMasterRunningPods, ptpSlaveRunningPods...)
		})

		It("PTP Slave Clock Sync", func() {
			testPtpSlaveClockSync(fullConfig, testParameters) // Implementation of the test case
		})

		It("PTP CPU Utilization", func() {
			testPtpCpuUtilization(fullConfig, testParameters)
		})
	})
})

func testPtpSlaveClockSync(fullConfig testconfig.TestConfig, testParameters ptptestconfig.PtpTestConfig) {
	Expect(testclient.Client).NotTo(BeNil())

	if fullConfig.Status == testconfig.DiscoveryFailureStatus {
		Fail("failed to find a valid ptp slave configuration")
	}

	if testParameters.SoakTestConfig.DisableSoakTest {
		Skip("skip the test as the entire suite is disabled")
	}

	soakTestConfig := testParameters.SoakTestConfig
	slaveClockSyncTestSpec := testParameters.SoakTestConfig.SlaveClockSyncConfig.TestSpec

	if !slaveClockSyncTestSpec.Enable {
		Skip("skip the test - the test is disabled")
	}

	logrus.Info("Test description ", soakTestConfig.SlaveClockSyncConfig.Description)

	// populate failure threshold
	failureThreshold := slaveClockSyncTestSpec.FailureThreshold
	if failureThreshold == 0 {
		failureThreshold = soakTestConfig.FailureThreshold
	}
	if failureThreshold == 0 {
		failureThreshold = 1
	}
	logrus.Info("Failure threshold = ", failureThreshold)
	// Actual implementation
}

// This test will run for configured minutes or until failure_threshold reached,
// whatever comes first. A failure_threshold is reached each time the cpu usage
// of any of the ptp pods (daemonset & operator) is higher than the expected one,
// which is hardcoded to 15milliCores. The cpu usage is retrieved once per minute.
func testPtpCpuUtilization(fullConfig testconfig.TestConfig, testParameters ptptestconfig.PtpTestConfig) {
	const (
		minimumFailureThreshold  = 1
		cpuUsageCheckingInterval = 1 * time.Minute
		milliCoresThreshold      = ptptestconfig.PtpDefaultMilliCoresUsageThreshold
	)

	logrus.Debugf("CPU Utilization TC Config: %+v", testParameters.SoakTestConfig.CpuUtilization)

	params := testParameters.SoakTestConfig.CpuUtilization.TestSpec
	if !params.Enable {
		Skip("skip the test - the test is disabled")
		return
	}

	// Create timer channel for test case timeout.
	testCaseDuration := time.Duration(params.Duration) * time.Minute
	tcEndChan := time.After(testCaseDuration)

	// Create ticker for cpu usage checker function.
	cpuUsageCheckTicker := time.NewTicker(cpuUsageCheckingInterval)

	// Set failureThresold limit number.
	failureThreshold := minimumFailureThreshold
	if params.FailureThreshold > minimumFailureThreshold {
		failureThreshold = params.FailureThreshold
	}

	logrus.Infof("Running TC for %s (failure threshold: %d)", testCaseDuration.String(), failureThreshold)

	failureCounter := 0
	for {
		select {
		case <-tcEndChan:
			// TC ended: report & return.
			logrus.Infof("CPU utilization threshold reached %d times.", failureCounter)
			return
		case <-cpuUsageCheckTicker.C:
			logrus.Infof("Retrieving cpu usage of the ptp pods.")

			thresholdReached, err := isCpuUsageThresholdReachedInPtpPods(milliCoresThreshold)
			Expect(err).To(BeNil(), "failed to get cpu usage: ", err)

			if thresholdReached {
				failureCounter++
				Expect(failureCounter).To(BeNumerically("<", failureThreshold),
					fmt.Sprintf("Failure threshold (%d) reached", failureThreshold))
			}
		}
	}
}

// isCpuUsageThresholdReachedInPtpPods is a helper that checks whether the usage on each
// ptp-daemonset's pods is lower than a given threshold in milliCores.
func isCpuUsageThresholdReachedInPtpPods(milliCoresThreshold int64) (bool, error) {
	daemonsetPodsCpuUsageThresholdReached, err := ptptesthelper.IsPtpDaemonPodsCpuUsageHigherThan(milliCoresThreshold)
	if err != nil {
		return false, fmt.Errorf("failed to get linux-ptp daemonset pods cpu usage: %v", err)
	}

	operatorPodsCpuUsageThresholdReached, err := ptptesthelper.IsPtpOperatorPodsCpuUsageHigherThan(milliCoresThreshold)
	if err != nil {
		return false, fmt.Errorf("failed to get ptp operator pods cpu usage: %v", err)
	}

	return (daemonsetPodsCpuUsageThresholdReached || operatorPodsCpuUsageThresholdReached), nil
}

func GetPodLogs(namespace, podName, containerName string, min, max int, messages chan string, ctx context.Context) {
	var re = regexp.MustCompile(`(?ms)rms\s*\d*\smax`)
	count := int64(100)
	podLogOptions := v1.PodLogOptions{
		Container: containerName,
		Follow:    true,
		TailLines: &count,
	}
	id := fmt.Sprintf("%s/%s:%s", namespace, podName, containerName)
	podLogRequest := testclient.Client.CoreV1().
		Pods(namespace).
		GetLogs(podName, &podLogOptions)
	stream, err := podLogRequest.Stream(context.TODO())
	if err != nil {
		messages <- fmt.Sprintf("error streaming logs from %s", id)
		return
	}
	file, _ := os.Create(podName)
	ticker := time.NewTicker(time.Minute)
	seen := false
	defer stream.Close()
	buf := make([]byte, 2000)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !seen {
				messages <- fmt.Sprintf("can't find master offset logs %s", id)
			}
			seen = false
		default:
			numBytes, err := stream.Read(buf)
			if numBytes == 0 {
				continue
			}
			file.Write(buf[:numBytes])
			if err == io.EOF {
				break
			}
			if err != nil {
				messages <- fmt.Sprintf("error streaming logs from %s", id)
				return
			}
			message := string(buf[:numBytes])
			match := re.FindAllString(message, -1)
			if len(match) != 0 {
				seen = true
				expression := strings.Fields(match[0])
				offset, err := strconv.Atoi(expression[1])
				if err != nil {
					messages <- fmt.Sprintf("can't parse log from %s %s", id, message)
				}
				if offset > max || offset < min {
					messages <- fmt.Sprintf("bad offset found at  %s value=%d", id, offset)
				}
			}
			logrus.Debug(id, message)
		}
	}
}

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
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	v1 "k8s.io/api/core/v1"

	v1core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ptp-operator/test/pkg"
	"github.com/openshift/ptp-operator/test/pkg/client"
	testclient "github.com/openshift/ptp-operator/test/pkg/client"
	"github.com/openshift/ptp-operator/test/pkg/execute"
	"github.com/openshift/ptp-operator/test/pkg/pods"
	"github.com/openshift/ptp-operator/test/pkg/testconfig"

	ptptestconfig "github.com/openshift/ptp-operator/test/conformance/config"

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

			ptpPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
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

		It("Execute TestCase A", func() {
			done := make(chan interface{})
			go func() {
				logrus.Info("start: TestCase A")
				time.Sleep(100 * time.Millisecond)
				logrus.Info("end: TestCase A")
				close(done)
			}()
			Eventually(done, pkg.TimeoutIn5Minutes).Should(BeClosed())
		})
		It("Execute TestCase B", func() {
			done := make(chan interface{})
			go func() {
				logrus.Info("start: Testing TestCase B")
				time.Sleep(500 * time.Millisecond)
				logrus.Info("end: Testing TestCase B")
				close(done)
			}()
			Eventually(done, pkg.TimeoutIn10Minutes).Should(BeClosed())
		})

		It("Execute TestCase C", func() {
			done := make(chan interface{})
			go func() {
				logrus.Info("start: Testing TestCase C")
				time.Sleep(500 * time.Millisecond)
				logrus.Info("end: Testing TestCase C")
				close(done)
			}()
			Eventually(done, pkg.TimeoutIn3Minutes).Should(BeClosed())
		})

		It("PTP Offset testing", func() {

			Expect(testclient.Client).NotTo(BeNil())

			logrus.Info("config=", testParameters)
			//
			if fullConfig.Status == testconfig.DiscoveryFailureStatus {
				Fail("failed to find a valid ptp slave configuration")
			}
			// Get All PTP pods
			slaveNodes, err := testclient.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
				LabelSelector: pkg.PtpClockUnderTestNodeLabel,
			})
			if err != nil {
				logrus.Error("cannot list slave nodes")
				Fail("Can't list slave nodes")
			}

			var slavePods []v1.Pod

			for _, s := range slaveNodes.Items {
				ptpPods, err := testclient.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(),
					metav1.ListOptions{LabelSelector: "app=linuxptp-daemon", FieldSelector: fmt.Sprintf("spec.nodeName=%s", s.Name)})
				if err != nil {
					logrus.Error("error in getting ptp pods")
					Fail("can't find ptp pods, test skipped")
				}
				slavePods = append(slavePods, ptpPods.Items...)
			}

			if len(slavePods) == 0 {
				logrus.Error("no slave pod found")
				Fail("no slave pods found")
			}
			messages := make(chan string)
			duration := time.Duration(testParameters.SoakTestConfig.MasterOffsetConfig.Duration) * time.Minute
			ticker := time.NewTicker(duration)
			var wg sync.WaitGroup
			ctx, cancel := context.WithTimeout(context.Background(), duration)
			for _, p := range slavePods {
				logrus.Debug("node=", p.Spec.NodeName, ", pod=", p.Name, " label=", p.Labels)
				wg.Add(1)
				go func(namespace, pod, container string, min, max int, messages chan string, ctx context.Context) {
					defer wg.Done()
					GetPodLogs(namespace, pod, container, min, max, messages, ctx)
				}(p.Namespace, p.Name, pkg.PtpContainerName,
					testParameters.GlobalConfig.MinOffset,
					testParameters.GlobalConfig.MaxOffset,
					messages, ctx)
			}
			asyncCounter := 0
		L1:
			for {
				select {
				case msg := <-messages:
					if testParameters.SoakTestConfig.MasterOffsetConfig.FailFast {
						cancel()
						Fail(msg)
						break L1
					} else {
						logrus.Error(msg)
						asyncCounter++
					}
				case <-ticker.C:
					logrus.Info("test duration ended")
					cancel()
					break L1
				}
			}
			wg.Wait()
			if asyncCounter != 0 {
				Fail("error found in master offset sync, please check the logs")
			}
		})
	})

})

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

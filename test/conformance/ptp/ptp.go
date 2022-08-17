package ptp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/openshift/ptp-operator/test/utils"
	"github.com/openshift/ptp-operator/test/utils/daemonsets"
	"github.com/openshift/ptp-operator/test/utils/event"
	"github.com/openshift/ptp-operator/test/utils/execute"

	"github.com/sirupsen/logrus"
	v1app "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	v1core "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"

	"github.com/openshift/ptp-operator/test/utils/client"
	testclient "github.com/openshift/ptp-operator/test/utils/client"
	"github.com/openshift/ptp-operator/test/utils/nodes"
	testnode "github.com/openshift/ptp-operator/test/utils/nodes"
	"github.com/openshift/ptp-operator/test/utils/pods"
	"github.com/openshift/ptp-operator/test/utils/testconfig"
)

type TestCase string

const (
	Reboot TestCase = "reboot"

	masterOffsetLowerBound  = -100
	masterOffsetHigherBound = 100

	timeoutIn3Minutes  = 3 * time.Minute
	timeoutIn5Minutes  = 5 * time.Minute
	timeoutIn10Minutes = 10 * time.Minute

	metricsEndPoint       = "127.0.0.1:9091/metrics"
	ptpConfigOperatorName = "default"

	rebootDaemonSetNamespace     = "default"
	rebootDaemonSetName          = "ptp-reboot"
	rebootDaemonSetContainerName = "container-00"
)

var _ = Describe("[ptp-long-running]", func() {

	BeforeEach(func() {
		Expect(client.Client).NotTo(BeNil())
	})

	Context("Soak testing", func() {

		/*BeforeEach(func() {
			if fullConfig.Status == testconfig.DiscoveryFailureStatus {
				Skip("Failed to find a valid ptp slave configuration")
			}
			ptpPods, err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
			Expect(err).NotTo(HaveOccurred())
			Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")

			ptpSlaveRunningPods := []v1core.Pod{}
			ptpMasterRunningPods := []v1core.Pod{}

			for podIndex := range ptpPods.Items {
				if podRole(&ptpPods.Items[podIndex], fullConfig.DiscoveredSlavePtpConfig.Label) {
					waitUntilLogIsDetected(&ptpPods.Items[podIndex], timeoutIn3Minutes, "Profile Name:")
					ptpSlaveRunningPods = append(ptpSlaveRunningPods, ptpPods.Items[podIndex])
				} else if podRole(&ptpPods.Items[podIndex], fullConfig.DiscoveredMasterPtpConfig.Label) {
					waitUntilLogIsDetected(&ptpPods.Items[podIndex], timeoutIn3Minutes, "Profile Name:")
					ptpMasterRunningPods = append(ptpMasterRunningPods, ptpPods.Items[podIndex])
				}
			}
			if testconfig.GlobalConfig.ConfiguredMasterPresent {
				Expect(len(ptpMasterRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP master pods on Cluster")
				Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
			} else {
				Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
			}
			//ptpRunningPods = append(ptpMasterRunningPods, ptpSlaveRunningPods...)
		})*/

		Context("PTP parallel tests", func() {
			It("test-feature-1", func() {
				done := make(chan interface{})
				go func() {
					logrus.Info("Testing feature 1")
					time.Sleep(100 * time.Millisecond)
					logrus.Info("Ending testing feature 1")
					close(done)
				}()
				Eventually(done, timeoutIn5Minutes).Should(BeClosed())
			})
			It("test-feature-2", func() {
				done := make(chan interface{})
				go func() {
					logrus.Info("Testing feature 2")
					time.Sleep(500 * time.Millisecond)
					logrus.Info("Ending testing feature 2")
					close(done)
				}()
				Eventually(done, timeoutIn10Minutes).Should(BeClosed())
			})

			It("test-feature-3", func() {
				done := make(chan interface{})
				go func() {
					logrus.Info("Testing feature 3", 0)
					time.Sleep(500 * time.Millisecond)
					logrus.Info("Ending testing feature 3")
					close(done)
				}()
				Eventually(done, timeoutIn10Minutes).Should(BeClosed())
			})
		})

		/*It("continuous-offset-testing", func() {
			logrus.Debug("soak-testing started")

			logrus.Info("config=", testParameters)
			//
			if fullConfig.Status == testconfig.DiscoveryFailureStatus {
				Fail("Failed to find a valid ptp slave configuration")
			}
			// Get All PTP pods
			slaveNodes, err := client.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
				LabelSelector: "ptp/test-slave",
			})
			if err != nil {
				logrus.Error("Can't list slave nodes")
				Fail("Can't list slave nodes")
			}

			var slavePods []v1.Pod

			for _, s := range slaveNodes.Items {
				ptpPods, err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).List(context.Background(),
					metav1.ListOptions{LabelSelector: "app=linuxptp-daemon", FieldSelector: fmt.Sprintf("spec.nodeName=%s", s.Name)})
				if err != nil {
					logrus.Error("Error in getting ptp pods")
					Fail("can't find ptp pods, test skipped")
				}
				slavePods = append(slavePods, ptpPods.Items...)
			}

			if len(slavePods) == 0 {
				logrus.Error("No slave pod found")
				Fail("no slave pods found")
			}
			messages := make(chan string)
			duration := time.Duration(testParameters.MasterOffsetContinuousConfig.Duration) * time.Minute
			ticker := time.NewTicker(duration)
			var wg sync.WaitGroup
			ctx, cancel := context.WithTimeout(context.Background(), duration)
			for _, p := range slavePods {
				logrus.Debug("node=", p.Spec.NodeName, ", pod=", p.Name, " label=", p.Labels)
				wg.Add(1)
				go func(namespace, pod, container string, min, max int, messages chan string, ctx context.Context) {
					defer wg.Done()
					GetPodLogs(namespace, pod, container, min, max, messages, ctx)
				}(p.Namespace, p.Name, PtpContainerName,
					testParameters.MasterOffsetContinuousConfig.MinOffset,
					testParameters.MasterOffsetContinuousConfig.MaxOffset,
					messages, ctx)
			}
			asyncCounter := 0
		L1:
			for {
				select {
				case msg := <-messages:
					if testParameters.MasterOffsetContinuousConfig.FailFast {
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
				Fail("Error found in master offset sync, please check the logs")
			}
		})*/
	})
})

var _ = Describe("[ptp]", func() {

	BeforeEach(func() {
		Expect(client.Client).NotTo(BeNil())
	})

	Context("PTP configuration verifications", func() {
		// Setup verification
		// if requested enabled  ptp events
		It("Should check whether PTP operator needs to enable PTP events", func() {
			By("Find if variable set to enable ptp events")
			if event.Enable() {
				Expect(enablePTPEvent()).NotTo(HaveOccurred())
				ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(PtpLinuxDaemonNamespace).Get(context.Background(), ptpConfigOperatorName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(ptpConfig.Spec.EventConfig.EnableEventPublisher).Should(BeTrue(), "failed to enable ptp event")
			}
		})
		It("Should check whether PTP operator appropriate resource exists", func() {
			By("Getting list of available resources")
			rl, err := client.Client.ServerPreferredResources()
			Expect(err).ToNot(HaveOccurred())

			found := false
			By("Find appropriate resources")
			for _, g := range rl {
				if strings.Contains(g.GroupVersion, PtpResourcesGroupVersionPrefix) {
					for _, r := range g.APIResources {
						By("Search for resource " + PtpResourcesNameOperatorConfigs)
						if r.Name == PtpResourcesNameOperatorConfigs {
							found = true
						}
					}
				}
			}

			Expect(found).To(BeTrue(), fmt.Sprintf("resource %s not found", PtpResourcesNameOperatorConfigs))
		})
		// Setup verification
		It("Should check that all nodes are running at least one replica of linuxptp-daemon", func() {
			By("Getting ptp operator config")
			ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(PtpLinuxDaemonNamespace).Get(context.Background(), ptpConfigOperatorName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			listOptions := metav1.ListOptions{}
			if ptpConfig.Spec.DaemonNodeSelector != nil && len(ptpConfig.Spec.DaemonNodeSelector) != 0 {
				listOptions = metav1.ListOptions{LabelSelector: metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: ptpConfig.Spec.DaemonNodeSelector})}
			}

			By("Getting list of nodes")
			nodes, err := client.Client.CoreV1().Nodes().List(context.Background(), listOptions)
			Expect(err).NotTo(HaveOccurred())
			By("Checking number of nodes")
			Expect(len(nodes.Items)).To(BeNumerically(">", 0), "number of nodes should be more than 0")

			By("Get daemonsets collection for the namespace " + PtpLinuxDaemonNamespace)
			ds, err := client.Client.DaemonSets(PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(ds.Items)).To(BeNumerically(">", 0), "no damonsets found in the namespace "+PtpLinuxDaemonNamespace)
			By("Checking number of scheduled instances")
			Expect(ds.Items[0].Status.CurrentNumberScheduled).To(BeNumerically("==", len(nodes.Items)), "should be one instance per node")
		})
		// Setup verification
		It("Should check that operator is deployed", func() {
			By("Getting deployment " + PtpOperatorDeploymentName)
			dep, err := client.Client.Deployments(PtpLinuxDaemonNamespace).Get(context.Background(), PtpOperatorDeploymentName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			By("Checking availability of the deployment")
			for _, c := range dep.Status.Conditions {
				if c.Type == v1app.DeploymentAvailable {
					Expect(string(c.Status)).Should(Equal("True"), PtpOperatorDeploymentName+" deployment is not available")
				}
			}
		})
	})

	Describe("PTP e2e tests", Serial, func() {
		var ptpRunningPods []v1core.Pod
		var fifoPriorities map[string]int64
		var fullConfig testconfig.TestConfig

		execute.BeforeAll(func() {
			testconfig.CreatePtpConfigurations()
			fullConfig = testconfig.GetFullDiscoveredConfig(PtpLinuxDaemonNamespace, false)
			restartPtpDaemon()
		})

		Context("PTP Reboot discovery", func() {
			BeforeEach(func() {
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
			})

			It("The slave node is rebooted and discovered and in sync", func() {
				if testCaseEnabled(Reboot) {
					By("Slave node is rebooted", func() {
						rebootSlaveNode(fullConfig)
					})
				} else {
					Skip("Skipping the reboot test")
				}
			})
		})

		Context("PTP Interfaces discovery", func() {
			BeforeEach(func() {
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				ptpPods, err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")

				ptpSlaveRunningPods := []v1core.Pod{}
				ptpMasterRunningPods := []v1core.Pod{}

				for podIndex := range ptpPods.Items {
					if podRole(&ptpPods.Items[podIndex], fullConfig.DiscoveredSlavePtpConfig.Label) {
						waitUntilLogIsDetected(&ptpPods.Items[podIndex], timeoutIn3Minutes, "Profile Name:")
						ptpSlaveRunningPods = append(ptpSlaveRunningPods, ptpPods.Items[podIndex])
					} else if podRole(&ptpPods.Items[podIndex], fullConfig.DiscoveredMasterPtpConfig.Label) {
						waitUntilLogIsDetected(&ptpPods.Items[podIndex], timeoutIn3Minutes, "Profile Name:")
						ptpMasterRunningPods = append(ptpMasterRunningPods, ptpPods.Items[podIndex])
					}
				}
				if testconfig.GlobalConfig.ConfiguredMasterPresent {
					Expect(len(ptpMasterRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP master pods on Cluster")
					Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
				} else {
					Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
				}
				ptpRunningPods = append(ptpMasterRunningPods, ptpSlaveRunningPods...)
			})

			// 25729
			It("The interfaces support ptp can be discovered correctly", func() {
				for podIndex := range ptpRunningPods {
					ptpSupportedInt := getPtpMasterSlaveAttachedInterfaces(&ptpRunningPods[podIndex])
					Expect(len(ptpSupportedInt)).To(BeNumerically(">", 0), "Fail to detect PTP Supported interfaces on slave/master pods")
					ptpDiscoveredInterfaces := ptpDiscoveredInterfaceList(NodePtpDeviceAPIPath + ptpRunningPods[podIndex].Spec.NodeName)
					for _, intfc := range ptpSupportedInt {
						Expect(ptpDiscoveredInterfaces).To(ContainElement(intfc))
					}
				}
			})

			// 25730
			It("The virtual interfaces should be not discovered by ptp", func() {
				for podIndex := range ptpRunningPods {
					ptpNotSupportedInt := getNonPtpMasterSlaveAttachedInterfaces(&ptpRunningPods[podIndex])
					ptpDiscoveredInterfaces := ptpDiscoveredInterfaceList(NodePtpDeviceAPIPath + ptpRunningPods[podIndex].Spec.NodeName)
					for _, inter := range ptpNotSupportedInt {
						Expect(ptpDiscoveredInterfaces).ToNot(ContainElement(inter), "The interfaces discovered incorrectly. PTP non supported Interfaces in list")
					}
				}
			})

			// 25733
			It("PTP daemon apply match rule based on nodeLabel", func() {
				profileSlave := fmt.Sprintf("Profile Name: %s", fullConfig.DiscoveredSlavePtpConfig.Config.Name)
				profileMaster := ""
				if fullConfig.PtpModeDesired != testconfig.Discovery {
					profileMaster = fmt.Sprintf("Profile Name: %s", fullConfig.DiscoveredMasterPtpConfig.Config.Name)
				}

				for podIndex := range ptpRunningPods {
					podLogs, err := pods.GetLog(&ptpRunningPods[podIndex], PtpContainerName)
					Expect(err).NotTo(HaveOccurred(), "Error to find needed log due to %s", err)
					if podRole(&ptpRunningPods[podIndex], fullConfig.DiscoveredSlavePtpConfig.Label) {
						Expect(podLogs).Should(ContainSubstring(profileSlave),
							fmt.Sprintf("Profile %q not found in pod's log %s", profileSlave, ptpRunningPods[podIndex].Name))
					}
					if podRole(&ptpRunningPods[podIndex], fullConfig.DiscoveredMasterPtpConfig.Label) && fullConfig.PtpModeDesired != testconfig.Discovery {
						Expect(podLogs).Should(ContainSubstring(profileMaster),
							fmt.Sprintf("Profile %q not found in pod's log %s", profileSlave, ptpRunningPods[podIndex].Name))
					}
					if podRole(&ptpRunningPods[podIndex], fullConfig.DiscoveredMasterPtpConfig.Label) && fullConfig.PtpModeDesired != testconfig.Discovery {
						Expect(podLogs).Should(ContainSubstring(profileMaster),
							fmt.Sprintf("Profile %q not found in pod's log %s", profileSlave, ptpRunningPods[podIndex].Name))
					}
				}
			})

			// 25738
			It("Slave can sync to master", func() {
				var masterID string
				var slaveMasterID string
				grandMaster := "assuming the grand master role"
				for podIndex := range ptpRunningPods {
					if fullConfig.DiscoveredMasterPtpConfig.Label != "" &&
						podRole(&ptpRunningPods[podIndex], fullConfig.DiscoveredMasterPtpConfig.Label) {
						podLogs, err := pods.GetLog(&ptpRunningPods[podIndex], PtpContainerName)
						Expect(err).NotTo(HaveOccurred(), "Error to find needed log due to %s", err)
						Expect(podLogs).Should(ContainSubstring(grandMaster),
							fmt.Sprintf("Log message %q not found in pod's log %s", grandMaster, ptpRunningPods[podIndex].Name))
						for _, line := range strings.Split(podLogs, "\n") {
							if strings.Contains(line, "selected local clock") && strings.Contains(line, "as best master") {
								// Log example: ptp4l[10731.364]: [eno1] selected local clock 3448ed.fffe.f38e00 as best master
								masterID = strings.Split(line, " ")[5]
							}
						}
					}
					if podRole(&ptpRunningPods[podIndex], fullConfig.DiscoveredSlavePtpConfig.Label) {
						podLogs, err := pods.GetLog(&ptpRunningPods[podIndex], PtpContainerName)
						Expect(err).NotTo(HaveOccurred(), "Error to find needed log due to %s", err)

						for _, line := range strings.Split(podLogs, "\n") {
							if strings.Contains(line, "new foreign master") {
								// Log example: ptp4l[11292.467]: [eno1] port 1: new foreign master 3448ed.fffe.f38e00-1
								slaveMasterID = strings.Split(line, " ")[7]
							}
						}
					}
				}
				Expect(masterID).NotTo(BeNil())
				Expect(slaveMasterID).NotTo(BeNil())
				Expect(slaveMasterID).Should(HavePrefix(masterID), "Error match MasterID with the SlaveID. Slave connected to another Master")
			})
			// Checking that BCSlave can sync
			It("Downstream slave can sync to BC master", func() {
				isSingleNode, err := nodes.IsSingleNodeCluster()
				if err != nil {
					Skip("cannot determine if cluster is single node")
				}
				if fullConfig.PtpModeDiscovered != testconfig.BoundaryClock || isSingleNode {
					Skip("test only valid for Boundary clock in multi-node clusters")
				}
				if fullConfig.L2Config == nil {
					Skip("Boundary clock slave sync test is not performed in discovery mode")
				}
				if len(fullConfig.L2Config.BestClockConfig.BcSlave) == 0 {
					Skip("No enough nodes and segregated networks to test Boundary clock slave sync")
				}
				var masterID string
				var slaveMasterID string
				// grandMaster := "assuming the grand master role"
				for podIndex := range ptpRunningPods {
					if podRole(&ptpRunningPods[podIndex], PtpBCSlaveNodeLabel) {
						podLogs, err := pods.GetLog(&ptpRunningPods[podIndex], PtpContainerName)
						Expect(err).NotTo(HaveOccurred(), "Error to find needed log due to %s", err)

						for _, line := range strings.Split(podLogs, "\n") {
							if strings.Contains(line, "new foreign master") {
								// Log example: ptp4l[11292.467]: [eno1] port 1: new foreign master 3448ed.fffe.f38e00-1
								slaveMasterID = strings.Split(line, " ")[7]
							}
						}
					}
				}
				Expect(masterID).NotTo(BeNil())
				Expect(slaveMasterID).NotTo(BeNil())
				Expect(slaveMasterID).Should(HavePrefix(masterID), "Error match MasterID with the SlaveID. Slave connected to another Master")
			})
			// 25743
			It("Can provide a profile with higher priority", func() {
				var testPtpPod v1core.Pod
				if fullConfig.PtpModeDesired == testconfig.Discovery {
					Skip("Skipping because adding a different profile")
				}

				By("Creating a config with higher priority", func() {
					var policyName string
					switch fullConfig.PtpModeDiscovered {
					case testconfig.Discovery, testconfig.DualNICBoundaryClock, testconfig.None:
						Skip("Skipping because DualNICBoundaryClock is not supported yet for this test")
					case testconfig.OrdinaryClock:
						policyName = PtpSlavePolicyName
					case testconfig.BoundaryClock:
						policyName = PtpBcMasterPolicyName
					}
					ptpConfigSlave, err := client.Client.PtpV1Interface.PtpConfigs(PtpLinuxDaemonNamespace).Get(context.Background(), policyName, metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())
					nodes, err := client.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
						LabelSelector: fullConfig.DiscoveredSlavePtpConfig.Label,
					})
					Expect(err).NotTo(HaveOccurred())
					Expect(len(nodes.Items)).To(BeNumerically(">", 0),
						fmt.Sprintf("PTP Nodes with label %s are not deployed on cluster", fullConfig.DiscoveredSlavePtpConfig.Label))

					ptpConfigTest := mutateProfile(ptpConfigSlave, PtpSlavePolicyName, nodes.Items[0].Name)
					_, err = client.Client.PtpV1Interface.PtpConfigs(PtpLinuxDaemonNamespace).Create(context.Background(), ptpConfigTest, metav1.CreateOptions{})
					Expect(err).NotTo(HaveOccurred())

					testPtpPod, err = getPtpPodOnNode(nodes.Items[0].Name)
					Expect(err).NotTo(HaveOccurred())

					testPtpPod, err = replaceTestPod(&testPtpPod, time.Minute)
					Expect(err).NotTo(HaveOccurred())
				})

				By("Checking if Node has Profile", func() {
					waitUntilLogIsDetected(&testPtpPod, timeoutIn3Minutes, "Profile Name: test")
				})

				By("Deleting the test profile", func() {
					err := client.Client.PtpV1Interface.PtpConfigs(PtpLinuxDaemonNamespace).Delete(context.Background(), "test", metav1.DeleteOptions{})
					Expect(err).NotTo(HaveOccurred())
					Eventually(func() bool {
						_, err := client.Client.PtpV1Interface.PtpConfigs(PtpLinuxDaemonNamespace).Get(context.Background(), "test", metav1.GetOptions{})
						return kerrors.IsNotFound(err)
					}, 1*time.Minute, 1*time.Second).Should(BeTrue(), "Could not delete the test profile")
				})

				By("Checking the profile is reverted", func() {
					waitUntilLogIsDetected(&testPtpPod, timeoutIn3Minutes, "Profile Name: test-slave")
				})
			})

			It("Should retrieve the details of hardwares for the Ptp", func() {
				By("Getting the version of the OCP cluster")

				ocpVersion, err := getOCPVersion()
				Expect(err).ToNot(HaveOccurred())
				Expect(ocpVersion).ShouldNot(BeEmpty())

				By("Getting the version of the PTP operator")

				ptpOperatorVersion, err := getPtpOperatorVersion()
				Expect(err).ToNot(HaveOccurred())
				Expect(ptpOperatorVersion).ShouldNot(BeEmpty())

				By("Getting the NIC details of all the PTP enabled interfaces")

				var mapping = make(map[string]string)
				for _, pod := range ptpRunningPods {
					mapping = getNICInfo(pod)
					Expect(mapping).ShouldNot(BeEmpty())
				}

				By("Getting the interface details of the PTP config")

				ptpConfig := testconfig.GlobalConfig
				if ptpConfig.DiscoveredMasterPtpConfig != nil {
					printInterface(ptpConfig.DiscoveredMasterPtpConfig.Config, mapping)
				}
				if ptpConfig.DiscoveredSlavePtpConfig != nil {
					printInterface(ptpConfig.DiscoveredSlavePtpConfig.Config, mapping)
				}
				if ptpConfig.DiscoveredSlavePtpConfigSecondary != nil {
					printInterface(ptpConfig.DiscoveredSlavePtpConfigSecondary.Config, mapping)
				}

				By("Getting ptp config details")

				logrus.Infof("Discovered master ptp config %s", ptpConfig.DiscoveredMasterPtpConfig.String())
				logrus.Infof("Discovered slave ptp config %s", ptpConfig.DiscoveredSlavePtpConfig.String())
			})
		})

		Context("PTP metric is present", func() {
			BeforeEach(func() {
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				ptpPods, err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")

				ptpSlaveRunningPods := []v1core.Pod{}
				ptpMasterRunningPods := []v1core.Pod{}

				for podIndex := range ptpPods.Items {
					if podRole(&ptpPods.Items[podIndex], fullConfig.DiscoveredSlavePtpConfig.Label) {
						waitUntilLogIsDetected(&ptpPods.Items[podIndex], timeoutIn3Minutes, "Profile Name:")
						ptpSlaveRunningPods = append(ptpSlaveRunningPods, ptpPods.Items[podIndex])
					} else if podRole(&ptpPods.Items[podIndex], fullConfig.DiscoveredMasterPtpConfig.Label) {
						waitUntilLogIsDetected(&ptpPods.Items[podIndex], timeoutIn3Minutes, "Profile Name:")
						ptpMasterRunningPods = append(ptpMasterRunningPods, ptpPods.Items[podIndex])
					}
				}
				if testconfig.GlobalConfig.ConfiguredMasterPresent {
					Expect(len(ptpMasterRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP master pods on Cluster")
					Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
				} else {
					Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
				}
				ptpRunningPods = append(ptpMasterRunningPods, ptpSlaveRunningPods...)
			})

			// 27324
			It("on slave", func() {
				slavePodDetected := false
				for podIndex := range ptpRunningPods {
					if podRole(&ptpRunningPods[podIndex], fullConfig.DiscoveredSlavePtpConfig.Label) {
						Eventually(func() string {
							buf, _ := pods.ExecCommand(client.Client, &ptpRunningPods[podIndex], PtpContainerName, []string{"curl", metricsEndPoint})
							return buf.String()
						}, timeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("openshift_ptp_offset_ns"),
							"Time metrics are not detected")
						slavePodDetected = true
						break
					}
				}
				Expect(slavePodDetected).ToNot(BeFalse(), "No slave pods detected")
			})
		})

		Context("Running with event enabled", func() {
			ptpSlaveRunningPods := []v1core.Pod{}
			BeforeEach(func() {
				if !ptpEventEnabled() {
					Skip("Skipping, PTP events not enabled")
				}
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				ptpPods, err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")

				for podIndex := range ptpPods.Items {
					if podRole(&ptpPods.Items[podIndex], fullConfig.DiscoveredSlavePtpConfig.Label) {
						waitUntilLogIsDetected(&ptpPods.Items[podIndex], timeoutIn3Minutes, "Profile Name:")
						ptpSlaveRunningPods = append(ptpSlaveRunningPods, ptpPods.Items[podIndex])
					}
				}
				if fullConfig.PtpModeDesired == testconfig.Discovery {
					Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
				}
			})

			It("Should check for ptp events ", func() {
				By("Checking event side car is present")

				for podIndex := range ptpSlaveRunningPods {
					cloudProxyFound := false
					Expect(len(ptpSlaveRunningPods[podIndex].Spec.Containers)).To(BeNumerically("==", 3), "linuxptp-daemon is not deployed on cluster with cloud event proxy")
					for _, c := range ptpSlaveRunningPods[podIndex].Spec.Containers {
						if c.Name == EventProxyContainerName {
							cloudProxyFound = true
						}
					}
					Expect(cloudProxyFound).ToNot(BeFalse(), "No event pods detected")
				}

				By("Checking event metrics are present")
				for podIndex := range ptpSlaveRunningPods {
					Eventually(func() string {
						buf, _ := pods.ExecCommand(client.Client, &ptpSlaveRunningPods[podIndex], EventProxyContainerName, []string{"curl", metricsEndPoint})
						return buf.String()
					}, timeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("openshift_ptp_interface_role"),
						"Interface role metrics are not detected")

					Eventually(func() string {
						buf, _ := pods.ExecCommand(client.Client, &ptpSlaveRunningPods[podIndex], EventProxyContainerName, []string{"curl", metricsEndPoint})
						return buf.String()
					}, timeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("openshift_ptp_threshold"),
						"Threshold metrics are not detected")
				}

				By("Checking event api is healthy")
				for podIndex := range ptpSlaveRunningPods {
					Eventually(func() string {
						buf, _ := pods.ExecCommand(client.Client, &ptpSlaveRunningPods[podIndex], EventProxyContainerName, []string{"curl", "127.0.0.1:9085/api/cloudNotifications/v1/health"})
						return buf.String()
					}, timeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("OK"),
						"Event API is not in healthy state")
				}

				By("Checking ptp publisher is created")
				for podIndex := range ptpSlaveRunningPods {
					Eventually(func() string {
						buf, _ := pods.ExecCommand(client.Client, &ptpSlaveRunningPods[podIndex], EventProxyContainerName, []string{"curl", "127.0.0.1:9085/api/cloudNotifications/v1/publishers"})
						return buf.String()
					}, timeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("endpointUri"),
						"Event API  did not return publishers")
				}

				By("Checking events are generated")
				for podIndex := range ptpSlaveRunningPods {
					podLogs, err := pods.GetLog(&ptpSlaveRunningPods[podIndex], EventProxyContainerName)
					Expect(err).NotTo(HaveOccurred(), "Error to find needed log due to %s", err)
					Expect(podLogs).Should(ContainSubstring("Created publisher"),
						fmt.Sprintf("PTP event publisher was not created in pod %s", ptpSlaveRunningPods[podIndex].Name))
					Expect(podLogs).Should(ContainSubstring("event sent"),
						fmt.Sprintf("PTP event was not generated in the pod %s", ptpSlaveRunningPods[podIndex].Name))
				}
			})
		})
		Context("Running with fifo scheduling", func() {
			BeforeEach(func() {
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}

				masterConfigs, slaveConfigs := discoveryPTPConfiguration(PtpLinuxDaemonNamespace)
				ptpConfigs := append(masterConfigs, slaveConfigs...)

				fifoPriorities = make(map[string]int64)
				for _, config := range ptpConfigs {
					for _, profile := range config.Spec.Profile {
						if profile.PtpSchedulingPolicy != nil && *profile.PtpSchedulingPolicy == "SCHED_FIFO" {
							if profile.PtpSchedulingPriority != nil {
								fifoPriorities[*profile.Name] = *profile.PtpSchedulingPriority
							}
						}
					}
				}
				if len(fifoPriorities) == 0 {
					Skip("No SCHED_FIFO policies configured")
				}
			})
			It("Should check whether using fifo scheduling", func() {
				By("checking for chrt logs")
				ptpPods, err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")
				for name, priority := range fifoPriorities {
					ptp4lLog := fmt.Sprintf("/bin/chrt -f %d /usr/sbin/ptp4l", priority)
					for podIndex := range ptpPods.Items {
						logs, err := pods.GetLog(&ptpPods.Items[podIndex], PtpContainerName)
						Expect(err).NotTo(HaveOccurred())
						profileName := fmt.Sprintf("Profile Name: %s", name)
						if strings.Contains(logs, profileName) {
							Expect(logs).Should(ContainSubstring(ptp4lLog))
							delete(fifoPriorities, name)
						}
					}
				}
				Expect(fifoPriorities).To(HaveLen(0))
			})
		})
	})
})

func waitForNewForeignMaster(ptpSlaveNode *nodes.NodeTopology, slaveInterface string) (valid bool, err error) {
	const Timeout = 60
	for i := 0; i < Timeout; i++ {
		time.Sleep(1 * time.Second)
		ptpPods, err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).List(context.Background(),
			metav1.ListOptions{LabelSelector: "app=linuxptp-daemon", FieldSelector: fmt.Sprintf("spec.nodeName=%s", ptpSlaveNode.NodeName)})
		if err != nil {
			return false, errors.New("could not get ptp daemon pods")
		}
		if len(ptpPods.Items) != 1 {
			return false, fmt.Errorf("number of ptp daemon pods is not 1, pods number:%d", len(ptpPods.Items))
		}
		logs, err := pods.GetLog(&ptpPods.Items[0], PtpContainerName)
		if err != nil {
			return false, fmt.Errorf("%s", err)
		}
		if strings.Contains(logs, "new foreign master") {
			fmt.Printf("Foreign master found on, %s after %d seconds", slaveInterface, i)
			return true, nil
		}
	}
	fmt.Printf("Foreign master not found on, %s after %d seconds", slaveInterface, Timeout)
	return false, nil
}

func restartPtpDaemon() {
	ptpPods, err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	Expect(err).ToNot(HaveOccurred())
	for podIndex := range ptpPods.Items {
		err = client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).Delete(context.Background(), ptpPods.Items[podIndex].Name, metav1.DeleteOptions{GracePeriodSeconds: pointer.Int64Ptr(0)})
		Expect(err).ToNot(HaveOccurred())
	}

	waitForPtpDaemonToBeReady()
}

func waitForPtpDaemonToBeReady() int {
	daemonset, err := client.Client.DaemonSets(PtpLinuxDaemonNamespace).Get(context.Background(), PtpDaemonsetName, metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred())
	expectedNumber := daemonset.Status.DesiredNumberScheduled
	Eventually(func() int32 {
		daemonset, err = client.Client.DaemonSets(PtpLinuxDaemonNamespace).Get(context.Background(), PtpDaemonsetName, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		return daemonset.Status.NumberReady
	}, timeoutIn5Minutes, 2*time.Second).Should(Equal(expectedNumber))

	Eventually(func() int {
		ptpPods, err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
		Expect(err).ToNot(HaveOccurred())
		return len(ptpPods.Items)
	}, timeoutIn5Minutes, 2*time.Second).Should(Equal(int(expectedNumber)))
	return 0
}

// Returns the slave node label to be used in the test
func discoveryPTPConfiguration(namespace string) ([]*ptpv1.PtpConfig, []*ptpv1.PtpConfig) {
	var masters []*ptpv1.PtpConfig
	var slaves []*ptpv1.PtpConfig

	configList, err := client.Client.PtpConfigs(namespace).List(context.Background(), metav1.ListOptions{})
	Expect(err).ToNot(HaveOccurred())
	for configIndex := range configList.Items {
		for _, profile := range configList.Items[configIndex].Spec.Profile {
			if testconfig.IsPtpMaster(profile.Ptp4lOpts, profile.Phc2sysOpts) {
				masters = append(masters, &configList.Items[configIndex])
			}
			if testconfig.IsPtpSlave(profile.Ptp4lOpts, profile.Phc2sysOpts) {
				slaves = append(slaves, &configList.Items[configIndex])
			}
		}
	}

	return masters, slaves
}

func podRole(runningPod *v1core.Pod, role string) bool {
	nodeList, err := client.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
		LabelSelector: role,
	})
	Expect(err).NotTo(HaveOccurred())
	for NodeNumber := range nodeList.Items {
		if runningPod.Spec.NodeName == nodeList.Items[NodeNumber].Name {
			return true
		}
	}
	return false
}

// This function parses ethtool command output and detect interfaces which supports ptp protocol
func isPTPEnabled(ethToolOutput *bytes.Buffer) bool {
	var RxEnabled bool
	var TxEnabled bool
	var RawEnabled bool

	scanner := bufio.NewScanner(ethToolOutput)
	for scanner.Scan() {
		line := strings.TrimPrefix(scanner.Text(), "\t")
		parts := strings.Fields(line)
		if parts[0] == ETHTOOL_HARDWARE_RECEIVE_CAP {
			RxEnabled = true
		}
		if parts[0] == ETHTOOL_HARDWARE_TRANSMIT_CAP {
			TxEnabled = true
		}
		if parts[0] == ETHTOOL_HARDWARE_RAW_CLOCK_CAP {
			RawEnabled = true
		}
	}
	return RxEnabled && TxEnabled && RawEnabled
}

func ptpDiscoveredInterfaceList(path string) []string {
	var ptpInterfaces []string
	var nodePtpDevice ptpv1.NodePtpDevice
	fg, err := client.Client.CoreV1().RESTClient().Get().AbsPath(path).DoRaw(context.Background())
	Expect(err).ToNot(HaveOccurred())

	err = json.Unmarshal(fg, &nodePtpDevice)
	Expect(err).ToNot(HaveOccurred())

	for _, intConf := range nodePtpDevice.Status.Devices {
		ptpInterfaces = append(ptpInterfaces, intConf.Name)
	}
	return ptpInterfaces
}

func mutateProfile(profile *ptpv1.PtpConfig, profileName, nodeName string) *ptpv1.PtpConfig {
	mutatedConfig := profile.DeepCopy()
	priority := int64(0)
	mutatedConfig.ObjectMeta.Reset()
	mutatedConfig.ObjectMeta.Name = "test"
	mutatedConfig.ObjectMeta.Namespace = PtpLinuxDaemonNamespace
	mutatedConfig.Spec.Profile[0].Name = &profileName
	mutatedConfig.Spec.Recommend[0].Priority = &priority
	mutatedConfig.Spec.Recommend[0].Match[0].NodeLabel = nil
	mutatedConfig.Spec.Recommend[0].Match[0].NodeName = &nodeName
	mutatedConfig.Spec.Recommend[0].Profile = &profileName
	return mutatedConfig
}

func waitUntilLogIsDetected(pod *v1core.Pod, timeout time.Duration, neededLog string) {
	Eventually(func() string {
		logs, _ := pods.GetLog(pod, PtpContainerName)
		return logs
	}, timeout, 1*time.Second).Should(ContainSubstring(neededLog), fmt.Sprintf("Timeout to detect log %q in pod %q", neededLog, pod.Name))
}

func getPtpPodOnNode(nodeName string) (v1core.Pod, error) {
	runningPod, err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	Expect(err).NotTo(HaveOccurred(), "Error to get list of pods by label: app=linuxptp-daemon")
	Expect(len(runningPod.Items)).To(BeNumerically(">", 0), "PTP pods are  not deployed on cluster")
	for podIndex := range runningPod.Items {

		if runningPod.Items[podIndex].Spec.NodeName == nodeName {
			return runningPod.Items[podIndex], nil
		}
	}
	return v1core.Pod{}, errors.New("pod not found")
}

func getMasterSlaveAttachedInterfaces(pod *v1core.Pod) []string {
	var IntList []string
	Eventually(func() error {
		stdout, err := pods.ExecCommand(client.Client, pod, PtpContainerName, []string{"ls", "/sys/class/net/"})
		if err != nil {
			return err
		}

		if stdout.String() == "" {
			return errors.New("empty response from pod retrying")
		}

		IntList = strings.Split(strings.Join(strings.Fields(stdout.String()), " "), " ")
		if len(IntList) == 0 {
			return errors.New("no interface detected")
		}

		return nil
	}, timeoutIn3Minutes, 5*time.Second).Should(BeNil())

	return IntList
}

func getPtpMasterSlaveAttachedInterfaces(pod *v1core.Pod) []string {
	var ptpSupportedInterfaces []string
	var stdout bytes.Buffer

	intList := getMasterSlaveAttachedInterfaces(pod)
	for _, interf := range intList {
		skipInterface := false
		PCIAddr := ""
		var err error

		// Get readlink status
		Eventually(func() error {
			stdout, err = pods.ExecCommand(client.Client, pod, PtpContainerName, []string{"readlink", "-f", fmt.Sprintf("/sys/class/net/%s", interf)})
			if err != nil {
				return err
			}

			if stdout.String() == "" {
				return errors.New("empty response from pod retrying")
			}

			// Skip virtual interface
			if strings.Contains(stdout.String(), "devices/virtual/net") {
				skipInterface = true
				return nil
			}

			// sysfs address looks like: /sys/devices/pci0000:17/0000:17:02.0/0000:19:00.5/net/eno1
			pathSegments := strings.Split(stdout.String(), "/")
			if len(pathSegments) != 8 {
				skipInterface = true
				return nil
			}

			PCIAddr = pathSegments[5] // 0000:19:00.5
			return nil
		}, timeoutIn3Minutes, 5*time.Second).Should(BeNil())

		if skipInterface || PCIAddr == "" {
			continue
		}

		// Check if this is a virtual function
		Eventually(func() error {
			// If the physfn doesn't exist this means the interface is not a virtual function so we ca add it to the list
			stdout, err = pods.ExecCommand(client.Client, pod, PtpContainerName, []string{"ls", fmt.Sprintf("/sys/bus/pci/devices/%s/physfn", PCIAddr)})
			if err != nil {
				if strings.Contains(stdout.String(), "No such file or directory") {
					return nil
				}
				return err
			}

			if stdout.String() == "" {
				return errors.New("empty response from pod retrying")
			}

			// Virtual function
			skipInterface = true
			return nil
		}, 2*time.Minute, 1*time.Second).Should(BeNil())

		if skipInterface {
			continue
		}

		Eventually(func() error {
			stdout, err = pods.ExecCommand(client.Client, pod, PtpContainerName, []string{"ethtool", "-T", interf})
			if stdout.String() == "" {
				return errors.New("empty response from pod retrying")
			}

			if err != nil {
				if strings.Contains(stdout.String(), "No such device") {
					skipInterface = true
					return nil
				}
				return err
			}
			return nil
		}, 2*time.Minute, 1*time.Second).Should(BeNil())

		if skipInterface {
			continue
		}

		if isPTPEnabled(&stdout) {
			ptpSupportedInterfaces = append(ptpSupportedInterfaces, interf)
		}
	}
	return ptpSupportedInterfaces
}

func replaceTestPod(pod *v1core.Pod, timeout time.Duration) (v1core.Pod, error) {
	var newPod v1core.Pod

	err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{
		GracePeriodSeconds: pointer.Int64Ptr(0)})
	Expect(err).NotTo(HaveOccurred())

	Eventually(func() error {
		newPod, err = getPtpPodOnNode(pod.Spec.NodeName)

		if err == nil && newPod.Name != pod.Name && newPod.Status.Phase == "Running" {
			return nil
		}

		return errors.New("cannot replace PTP pod")
	}, timeout, 1*time.Second).Should(BeNil())

	return newPod, nil
}

func getNonPtpMasterSlaveAttachedInterfaces(pod *v1core.Pod) []string {
	var ptpSupportedInterfaces []string
	var err error
	var stdout bytes.Buffer

	intList := getMasterSlaveAttachedInterfaces(pod)
	for _, interf := range intList {
		Eventually(func() error {
			stdout, err = pods.ExecCommand(client.Client, pod, PtpContainerName, []string{"ethtool", "-T", interf})
			if err != nil && !strings.Contains(stdout.String(), "No such device") {
				return err
			}
			if stdout.String() == "" {
				return errors.New("empty response from pod retrying")
			}
			return nil
		}, timeoutIn3Minutes, 2*time.Second).Should(BeNil())

		if strings.Contains(stdout.String(), "No such device") {
			continue
		}

		if !isPTPEnabled(&stdout) {
			ptpSupportedInterfaces = append(ptpSupportedInterfaces, interf)
		}
	}
	return ptpSupportedInterfaces
}

func enablePTPEvent() error {
	ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(PtpLinuxDaemonNamespace).Get(context.Background(), ptpConfigOperatorName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	if ptpConfig.Spec.EventConfig == nil {
		ptpConfig.Spec.EventConfig = &ptpv1.PtpEventConfig{
			EnableEventPublisher: true,
			TransportHost:        "amqp://mock",
		}
	}
	if ptpConfig.Spec.EventConfig.TransportHost == "" {
		ptpConfig.Spec.EventConfig.TransportHost = "amqp://mock"
	}

	ptpConfig.Spec.EventConfig.EnableEventPublisher = true
	_, err = client.Client.PtpOperatorConfigs(PtpLinuxDaemonNamespace).Update(context.Background(), ptpConfig, metav1.UpdateOptions{})
	return err
}

func ptpEventEnabled() bool {
	ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(PtpLinuxDaemonNamespace).Get(context.Background(), ptpConfigOperatorName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	if ptpConfig.Spec.EventConfig == nil {
		return false
	}
	return ptpConfig.Spec.EventConfig.EnableEventPublisher
}

func getNICInfo(pod v1core.Pod) map[string]string {
	var ptpSupportedInterfaces []string = getPtpMasterSlaveAttachedInterfaces(&pod)
	var stdout bytes.Buffer

	var ptpInterfaceNicMapping = make(map[string]string)

	for _, interf := range ptpSupportedInterfaces {
		PCIAddr := ""
		var err error

		Eventually(func() error {
			stdout, err = pods.ExecCommand(client.Client, &pod, PtpContainerName, []string{"readlink", "-f", fmt.Sprintf("/sys/class/net/%s", interf)})
			if err != nil {
				return err
			}

			if stdout.String() == "" {
				return errors.New("empty response from pod retrying")
			}

			pathSegments := strings.Split(stdout.String(), "/")
			PCIAddr = pathSegments[5] // 0000:19:00.5
			return nil
		}, timeoutIn3Minutes, 5*time.Second).Should(BeNil())

		if PCIAddr == "" {
			continue
		}

		ptpInterfaceNicMapping[interf] = PCIAddr
	}

	return ptpInterfaceNicMapping
}

func getPtpOperatorVersion() (string, error) {

	const releaseVersionStr = "RELEASE_VERSION"

	var ptpOperatorVersion string

	deploy, err := client.Client.AppsV1Interface.Deployments(PtpLinuxDaemonNamespace).Get(context.TODO(), PtpOperatorDeploymentName, metav1.GetOptions{})

	if err != nil {
		logrus.Infof("PTP Operator version is not found: %v", err)
		return "", err
	}

	envs := deploy.Spec.Template.Spec.Containers[0].Env
	for _, env := range envs {

		if env.Name == releaseVersionStr {
			ptpOperatorVersion = env.Value
			ptpOperatorVersion = ptpOperatorVersion[1:]
		}
	}

	logrus.Infof("PTP operator version is %v", ptpOperatorVersion)

	return ptpOperatorVersion, err
}

func getOCPVersion() (string, error) {

	const OpenShiftAPIServer = "openshift-apiserver"

	ocpClient := client.Client.OcpClient
	clusterOperator, err := ocpClient.ClusterOperators().Get(context.TODO(), OpenShiftAPIServer, metav1.GetOptions{})

	var ocpVersion string
	if err != nil {
		switch {
		case kerrors.IsForbidden(err), kerrors.IsNotFound(err):
			logrus.Errorf("OpenShift Version not found (must be logged in to cluster as admin): %v", err)
			err = nil
		}
	}
	if clusterOperator != nil {
		for _, ver := range clusterOperator.Status.Versions {
			if ver.Name == OpenShiftAPIServer {
				ocpVersion = ver.Version
				break
			}
		}
	}
	logrus.Infof("OCP Version is %v", ocpVersion)

	return ocpVersion, err
}

func printInterface(config ptpv1.PtpConfig, interfaceDetailsMap map[string]string) {
	for _, profile := range config.Spec.Profile {
		if (ptpv1.PtpProfile{}) == profile {
			continue
		}
		if profile.Interface != nil {
			logrus.Infof("profile name = %s, interface name = %s, interface details = %s", *profile.Name, *profile.Interface, interfaceDetailsMap[*profile.Interface])
		}
	}
}

func rebootSlaveNode(fullConfig testconfig.TestConfig) {
	logrus.Info("Rebooting system starts ..............")

	const (
		imageWithVersion = "quay.io/testnetworkfunction/debug-partner:latest"
	)

	// 1. create a daemon set for the node reboot
	rebootDaemonSetRunningPods, err := daemonsets.CreateDaemonSet(rebootDaemonSetName, rebootDaemonSetNamespace, rebootDaemonSetContainerName, imageWithVersion, timeoutIn5Minutes)
	if err != nil {
		logrus.Errorf("error : +%v\n", err.Error())
	}
	nodeToPodMapping := make(map[string]v1core.Pod)
	for _, dsPod := range rebootDaemonSetRunningPods.Items {
		nodeToPodMapping[dsPod.Spec.NodeName] = dsPod
	}

	// 2. Get the slave config, restart the slave node
	slavePtpConfig := fullConfig.DiscoveredSlavePtpConfig.Config

	restartedNodes := make([]string, len(rebootDaemonSetRunningPods.Items))

	for _, recommend := range slavePtpConfig.Spec.Recommend {
		matches := recommend.Match

		for _, match := range matches {
			nodeLabel := *match.NodeLabel
			// Get all nodes with this node label
			nodes, err := client.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{LabelSelector: nodeLabel})
			Expect(err).NotTo(HaveOccurred())

			// Restart the node
			for _, node := range nodes.Items {
				rebootNode(nodeToPodMapping[node.Name], node)
				restartedNodes = append(restartedNodes, node.Name)
			}
		}
	}
	logrus.Printf("Restarted nodes %v", restartedNodes)

	// 3. Verify the setup of PTP
	verifyAfterRebootState(restartedNodes, fullConfig)

	// 4. Slave nodes can sync to master
	checkSlaveSyncWithMaster(fullConfig)

	logrus.Info("Rebooting system ends ..............")
}

func rebootNode(pod v1core.Pod, node v1core.Node) {
	checkRestart(pod)
	// Wait for the node to be unreachable
	const unrechable = false
	testnode.WaitForNodeReachability(&node, timeoutIn10Minutes, unrechable)

	// Wait for all nodes to be reachable now after the restart
	const reachable = true
	testnode.WaitForNodeReachability(&node, timeoutIn10Minutes, reachable)
}

func checkRestart(pod v1core.Pod) {
	logrus.Printf("Restarting the node %s that pod %s is running on", pod.Spec.NodeName, pod.Name)

	const (
		pollingInterval = 3 * time.Second
	)

	Eventually(func() error {
		_, err := pods.ExecCommand(client.Client, &pod, "container-00", []string{"chroot", "/host", "shutdown", "-r"})
		return err
	}, timeoutIn10Minutes, pollingInterval).Should(BeNil())
}

func verifyAfterRebootState(rebootedNodes []string, fullConfig testconfig.TestConfig) {
	By("Getting ptp operator config")
	ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(PtpLinuxDaemonNamespace).Get(context.Background(), ptpConfigOperatorName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	listOptions := metav1.ListOptions{}
	if ptpConfig.Spec.DaemonNodeSelector != nil && len(ptpConfig.Spec.DaemonNodeSelector) != 0 {
		listOptions = metav1.ListOptions{LabelSelector: metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: ptpConfig.Spec.DaemonNodeSelector})}
	}

	By("Getting list of nodes")
	nodes, err := client.Client.CoreV1().Nodes().List(context.Background(), listOptions)
	Expect(err).NotTo(HaveOccurred())
	By("Checking number of nodes")
	Expect(len(nodes.Items)).To(BeNumerically(">", 0), "number of nodes should be more than 0")

	By("Get daemonsets collection for the namespace " + PtpLinuxDaemonNamespace)
	ds, err := client.Client.DaemonSets(PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{})
	Expect(err).ToNot(HaveOccurred())
	Expect(len(ds.Items)).To(BeNumerically(">", 0), "no damonsets found in the namespace "+PtpLinuxDaemonNamespace)

	By("Checking number of scheduled instances")
	Expect(ds.Items[0].Status.CurrentNumberScheduled).To(BeNumerically("==", len(nodes.Items)), "should be one instance per node")

	By("Checking if the ptp offset metric is present")
	for _, slaveNode := range rebootedNodes {

		runningPods := getRebootDaemonsetPodsAt(slaveNode)

		// Testing for one pod is sufficient as these pods are running on the same node that restarted
		for _, pod := range runningPods.Items {
			Expect(podRole(&pod, fullConfig.DiscoveredSlavePtpConfig.Label)).To(BeTrue())

			logrus.Printf("Calling metrics endpoint for pod %s with status %s", pod.Name, pod.Status.Phase)

			time.Sleep(timeoutIn3Minutes)

			Eventually(func() string {
				commands := []string{
					"curl", "-s", metricsEndPoint,
					//"curl", "-s", `127.0.0.1:9091/metrics | grep  '^openshift_ptp_offset_ns' | cut -d ' ' -f 2`,
				}
				buf, err := pods.ExecCommand(client.Client, &pod, rebootDaemonSetContainerName, commands)
				Expect(err).NotTo(HaveOccurred())

				scanner := bufio.NewScanner(strings.NewReader(buf.String()))
				var lines []string = make([]string, 5)
				for scanner.Scan() {
					text := scanner.Text()
					if strings.Contains(text, "openshift_ptp_offset_ns{from=\"master\"") {
						logrus.Printf("Line obtained is %s", text)
						lines = append(lines, text)
					}
				}
				var offset string
				var offsetVal int
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					tokens := strings.Fields(line)
					len := len(tokens)

					if len > 0 {
						offset = tokens[len-1]
						if offset != "" {
							if val, err := strconv.Atoi(offset); err == nil {
								offsetVal = val
								logrus.Println("Offset value obtained", offsetVal)
								break
							}
						}
					}
				}
				Expect(buf.String()).NotTo(BeEmpty())
				Expect(offsetVal >= masterOffsetLowerBound && offsetVal < masterOffsetHigherBound).To(BeTrue())
				return buf.String()
			}, timeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("openshift_ptp_offset_ns"),
				"Time metrics are not detected")
			break
		}
	}
}

func getRebootDaemonsetPodsAt(node string) *v1core.PodList {

	rebootDaemonsetPodList, err := client.Client.CoreV1().Pods(rebootDaemonSetNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "name=" + rebootDaemonSetName, FieldSelector: fmt.Sprintf("spec.nodeName=%s", node)})
	Expect(err).ToNot(HaveOccurred())

	return rebootDaemonsetPodList
}

func checkSlaveSyncWithMaster(fullConfig testconfig.TestConfig) {
	By("Checking if slave nodes can sync with the master")

	ptpPods, err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	Expect(err).NotTo(HaveOccurred())
	Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")

	ptpSlaveRunningPods := []v1core.Pod{}
	ptpMasterRunningPods := []v1core.Pod{}

	for _, pod := range ptpPods.Items {
		if podRole(&pod, fullConfig.DiscoveredSlavePtpConfig.Label) {
			waitUntilLogIsDetected(&pod, timeoutIn5Minutes, "Profile Name:")
			ptpSlaveRunningPods = append(ptpSlaveRunningPods, pod)
		} else if podRole(&pod, fullConfig.DiscoveredMasterPtpConfig.Label) {
			waitUntilLogIsDetected(&pod, timeoutIn5Minutes, "Profile Name:")
			ptpMasterRunningPods = append(ptpMasterRunningPods, pod)
		}
	}
	if testconfig.GlobalConfig.ConfiguredMasterPresent {
		Expect(len(ptpMasterRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP master pods on Cluster")
		Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
	} else {
		Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
	}

	var masterID string
	var slaveMasterID string
	grandMaster := "assuming the grand master role"

	for _, pod := range ptpPods.Items {
		if fullConfig.DiscoveredMasterPtpConfig.Label != "" &&
			podRole(&pod, fullConfig.DiscoveredMasterPtpConfig.Label) {
			podLogs, err := pods.GetLog(&pod, PtpContainerName)
			Expect(err).NotTo(HaveOccurred(), "Error to find needed log due to %s", err)
			Expect(podLogs).Should(ContainSubstring(grandMaster),
				fmt.Sprintf("Log message \"%s\" not found in pod's log %s", grandMaster, pod.Name))
			for _, line := range strings.Split(podLogs, "\n") {
				if strings.Contains(line, "selected local clock") && strings.Contains(line, "as best master") {
					// Log example: ptp4l[10731.364]: [eno1] selected local clock 3448ed.fffe.f38e00 as best master
					masterID = strings.Split(line, " ")[5]
				}
			}
		}
		if podRole(&pod, fullConfig.DiscoveredSlavePtpConfig.Label) {
			podLogs, err := pods.GetLog(&pod, PtpContainerName)
			Expect(err).NotTo(HaveOccurred(), "Error to find needed log due to %s", err)

			for _, line := range strings.Split(podLogs, "\n") {
				if strings.Contains(line, "new foreign master") {
					// Log example: ptp4l[11292.467]: [eno1] port 1: new foreign master 3448ed.fffe.f38e00-1
					slaveMasterID = strings.Split(line, " ")[7]
				}
			}
		}
	}
	Expect(masterID).NotTo(BeNil())
	Expect(slaveMasterID).NotTo(BeNil())
	Expect(slaveMasterID).Should(HavePrefix(masterID), "Error match MasterID with the SlaveID. Slave connected to another Master")

	Expect(masterID).NotTo(BeNil())
	Expect(slaveMasterID).NotTo(BeNil())
	Expect(slaveMasterID).Should(HavePrefix(masterID), "Error match MasterID with the SlaveID. Slave connected to another Master")
}

func testCaseEnabled(testCase TestCase) bool {

	enabledTests, isSet := os.LookupEnv("ENABLE_TEST_CASE")

	if isSet {
		tokens := strings.Split(enabledTests, ",")
		for _, token := range tokens {
			token = strings.TrimSpace(token)
			if strings.Contains(token, string(testCase)) {
				return true
			}
		}
	}
	return false
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

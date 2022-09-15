package ptp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/ptp-operator/test/utils"
	"github.com/openshift/ptp-operator/test/utils/event"
	"github.com/openshift/ptp-operator/test/utils/l2discovery"
	"github.com/openshift/ptp-operator/test/utils/metrics"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/apps/v1"
	v1core "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"

	"github.com/openshift/ptp-operator/test/utils/client"
	"github.com/openshift/ptp-operator/test/utils/execute"
	"github.com/openshift/ptp-operator/test/utils/nodes"
	"github.com/openshift/ptp-operator/test/utils/pods"
	"github.com/openshift/ptp-operator/test/utils/testconfig"

	k8sPriviledgedDs "github.com/test-network-function/privileged-daemonset"
)

type TestCase string

const (
	Reboot TestCase = "reboot"

	masterOffsetLowerBound  = -100
	masterOffsetHigherBound = 100

	timeoutIn3Minutes  = 3 * time.Minute
	timeoutIn5Minutes  = 5 * time.Minute
	timeoutIn10Minutes = 10 * time.Minute
	timeout10Seconds   = 10 * time.Second

	metricsEndPoint       = "127.0.0.1:9091/metrics"
	ptpConfigOperatorName = "default"

	rebootDaemonSetNamespace     = "default"
	rebootDaemonSetName          = "ptp-reboot"
	rebootDaemonSetContainerName = "container-00"
)

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
				ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(utils.PtpLinuxDaemonNamespace).Get(context.Background(), ptpConfigOperatorName, metav1.GetOptions{})
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
				if strings.Contains(g.GroupVersion, utils.PtpResourcesGroupVersionPrefix) {
					for _, r := range g.APIResources {
						By("Search for resource " + utils.PtpResourcesNameOperatorConfigs)
						if r.Name == utils.PtpResourcesNameOperatorConfigs {
							found = true
						}
					}
				}
			}

			Expect(found).To(BeTrue(), fmt.Sprintf("resource %s not found", utils.PtpResourcesNameOperatorConfigs))
		})
		// Setup verification
		It("Should check that all nodes are running at least one replica of linuxptp-daemon", func() {
			By("Getting ptp operator config")

			ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(utils.PtpLinuxDaemonNamespace).Get(context.Background(), ptpConfigOperatorName, metav1.GetOptions{})
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

			By("Get daemonsets collection for the namespace " + utils.PtpLinuxDaemonNamespace)
			ds, err := client.Client.DaemonSets(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(ds.Items)).To(BeNumerically(">", 0), "no damonsets found in the namespace "+utils.PtpLinuxDaemonNamespace)
			By("Checking number of scheduled instances")
			Expect(ds.Items[0].Status.CurrentNumberScheduled).To(BeNumerically("==", len(nodes.Items)), "should be one instance per node")
		})
		// Setup verification
		It("Should check that operator is deployed", func() {
			By("Getting deployment " + utils.PtpOperatorDeploymentName)
			dep, err := client.Client.Deployments(utils.PtpLinuxDaemonNamespace).Get(context.Background(), utils.PtpOperatorDeploymentName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			By("Checking availability of the deployment")
			for _, c := range dep.Status.Conditions {
				if c.Type == v1.DeploymentAvailable {
					Expect(string(c.Status)).Should(Equal("True"), utils.PtpOperatorDeploymentName+" deployment is not available")
				}
			}
		})

	})

	Describe("PTP e2e tests", func() {
		var ptpRunningPods []*v1core.Pod
		var fifoPriorities map[string]int64
		var fullConfig testconfig.TestConfig
		execute.BeforeAll(func() {
			testconfig.CreatePtpConfigurations()
			fullConfig = testconfig.GetFullDiscoveredConfig(utils.PtpLinuxDaemonNamespace, false)
			if fullConfig.Status != testconfig.DiscoverySuccessStatus {
				fmt.Printf(`ptpconfigs were not properly discovered, Check:
- the ptpconfig has a %s label only in the recommend section (no node section)
- the node running the clock under test is label with: %s`, utils.PtpClockUnderTestNodeLabel, utils.PtpClockUnderTestNodeLabel)
				os.Exit(1)
			}
			if fullConfig.PtpModeDesired != testconfig.Discovery {
				restartPtpDaemon()
			}

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
				ptpPods, err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")

				ptpRunningPods = testPtpRunningPods(ptpPods)
			})

			// 25729
			It("The interfaces support ptp can be discovered correctly", func() {
				for podIndex := range ptpRunningPods {
					ptpSupportedInt := getPtpMasterSlaveAttachedInterfaces(ptpRunningPods[podIndex])
					Expect(len(ptpSupportedInt)).To(BeNumerically(">", 0), "Fail to detect PTP Supported interfaces on slave/master pods")
					ptpDiscoveredInterfaces := ptpDiscoveredInterfaceList(utils.NodePtpDeviceAPIPath + ptpRunningPods[podIndex].Spec.NodeName)
					for _, intfc := range ptpSupportedInt {
						Expect(ptpDiscoveredInterfaces).To(ContainElement(intfc))
					}
				}
			})

			// 25730
			It("The virtual interfaces should be not discovered by ptp", func() {
				for podIndex := range ptpRunningPods {
					ptpNotSupportedInt := getNonPtpMasterSlaveAttachedInterfaces(ptpRunningPods[podIndex])
					ptpDiscoveredInterfaces := ptpDiscoveredInterfaceList(utils.NodePtpDeviceAPIPath + ptpRunningPods[podIndex].Spec.NodeName)
					for _, inter := range ptpNotSupportedInt {
						Expect(ptpDiscoveredInterfaces).ToNot(ContainElement(inter), "The interfaces discovered incorrectly. PTP non supported Interfaces in list")
					}
				}
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
				if ptpConfig.DiscoveredGrandMasterPtpConfig != nil {
					printInterface(ptpv1.PtpConfig(*ptpConfig.DiscoveredGrandMasterPtpConfig), mapping)
				}
				if ptpConfig.DiscoveredClockUnderTestPtpConfig != nil {
					printInterface(ptpv1.PtpConfig(*ptpConfig.DiscoveredClockUnderTestPtpConfig), mapping)
				}
				if ptpConfig.DiscoveredClockUnderTestSecondaryPtpConfig != nil {
					printInterface(ptpv1.PtpConfig(*ptpConfig.DiscoveredClockUnderTestSecondaryPtpConfig), mapping)
				}

				By("Getting ptp config details")

				logrus.Infof("Discovered master ptp config %s", ptpConfig.DiscoveredGrandMasterPtpConfig.String())
				logrus.Infof("Discovered slave ptp config %s", ptpConfig.DiscoveredClockUnderTestPtpConfig.String())
			})
		})
		Context("PTP ClockSync", func() {
			err := metrics.InitEnvIntParamConfig("MAX_OFFSET_IN_NS", metrics.MaxOffsetDefaultNs, &metrics.MaxOffsetNs)
			err = metrics.InitEnvIntParamConfig("MIN_OFFSET_IN_NS", metrics.MinOffsetDefaultNs, &metrics.MinOffsetNs)
			Expect(err).NotTo(HaveOccurred(), "error getting max offset in nanoseconds %s", err)

			// 25733
			It("PTP daemon apply match rule based on nodeLabel", func() {

				if fullConfig.PtpModeDesired == testconfig.Discovery {
					Skip(fmt.Sprint("This test needs the ptp-daemon to be rebooted but it is not possible in discovery mode, skipping"))
				}
				profileSlave := fmt.Sprintf("Profile Name: %s", fullConfig.DiscoveredClockUnderTestPtpConfig.Name)
				profileMaster := ""
				if fullConfig.DiscoveredGrandMasterPtpConfig != nil {
					profileMaster = fmt.Sprintf("Profile Name: %s", fullConfig.DiscoveredGrandMasterPtpConfig.Name)
				}

				for podIndex := range ptpRunningPods {
					_, err := pods.GetLog(ptpRunningPods[podIndex], utils.PtpContainerName)
					Expect(err).NotTo(HaveOccurred(), "Error to find needed log due to %s", err)

					if isClockUnderTestPod(ptpRunningPods[podIndex]) {
						waitUntilLogIsDetected(ptpRunningPods[podIndex], timeoutIn3Minutes, profileSlave)
					} else if isGrandMasterPod(ptpRunningPods[podIndex]) && fullConfig.DiscoveredGrandMasterPtpConfig != nil {
						waitUntilLogIsDetected(ptpRunningPods[podIndex], timeoutIn3Minutes, profileMaster)
					}
				}
			})

			// Multinode clock sync test:
			// - waits for the foreign master to appear
			// - verifies that the foreign master has the expected grandmaster ID
			// - use metrics to verify that the offset is below threshold
			//
			// Single node clock sync test:
			// - waits for the foreign master to appear
			// - use metrics to verify that the offset is below threshold
			It("Slave can sync to master", func() {
				isSingleNode, err := nodes.IsSingleNodeCluster()
				if err != nil {
					Skip("cannot determine if cluster is single node")
				}
				var grandmasterID *string
				if fullConfig.L2Config != nil && !isSingleNode {
					aLabel := utils.PtpGrandmasterNodeLabel
					aString, err := getClockIDMaster(utils.PtpGrandMasterPolicyName, &aLabel, nil)
					grandmasterID = &aString
					Expect(err).To(BeNil())
				}
				BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID)

				if fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClock {
					BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestSecondaryPtpConfig), grandmasterID)
				}
			})

			// Multinode BCSlave clock sync
			// - waits for the BCSlave foreign master to appear (the boundary clock)
			// - verifies that the BCSlave foreign master has the expected boundary clock ID
			// - use metrics to verify that the offset with boundary clock is below threshold
			It("Downstream slave can sync to BC master", func() {
				isSingleNode, err := nodes.IsSingleNodeCluster()
				if err != nil {
					Skip("cannot determine if cluster is single node")
				}
				if fullConfig.L2Config == nil || isSingleNode {
					Skip("Boundary clock slave sync test is not performed in discovery or SNO mode")
				}
				if fullConfig.PtpModeDiscovered != testconfig.BoundaryClock &&
					fullConfig.PtpModeDiscovered != testconfig.DualNICBoundaryClock {
					Skip("test only valid for Boundary clock in multi-node clusters")
				}
				if (fullConfig.PtpModeDiscovered == testconfig.BoundaryClock &&
					len(fullConfig.L2Config.Solutions[l2discovery.AlgoBCWithSlaves]) == 0) ||
					(fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClock &&
						len(fullConfig.L2Config.Solutions[l2discovery.AlgoDualNicBCWithSlaves]) == 0) {
					Skip("test only valid for Boundary clock in multi-node clusters with slaves")
				}
				aLabel := utils.PtpClockUnderTestNodeLabel
				masterIDBc1, err := getClockIDMaster(utils.PtpBcMaster1PolicyName, &aLabel, nil)
				Expect(err).To(BeNil())
				BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredSlave1PtpConfig), &masterIDBc1)

				if fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClock &&
					len(fullConfig.L2Config.Solutions[l2discovery.AlgoDualNicBCWithSlaves]) != 0 {

					aLabel := utils.PtpClockUnderTestNodeLabel
					masterIDBc2, err := getClockIDMaster(utils.PtpBcMaster2PolicyName, &aLabel, nil)
					Expect(err).To(BeNil())
					BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredSlave2PtpConfig), &masterIDBc2)
				}

			})

			// 25743
			It("Can provide a profile with higher priority", func() {
				var testPtpPod v1core.Pod
				isSingleNode, err := nodes.IsSingleNodeCluster()
				if err != nil {
					Skip("cannot determine if cluster is single node")
				}
				if fullConfig.PtpModeDesired == testconfig.Discovery {
					Skip("Skipping because adding a different profile and no modifications are allowed in discovery mode")
				}
				var policyName string
				var modifiedPtpConfig *ptpv1.PtpConfig
				By("Creating a config with higher priority", func() {

					switch fullConfig.PtpModeDiscovered {
					case testconfig.Discovery, testconfig.None:
						Skip("Skipping because Discovery or None is not supported yet for this test")
					case testconfig.OrdinaryClock:
						policyName = utils.PtpSlave1PolicyName
					case testconfig.BoundaryClock:
						policyName = utils.PtpBcMaster1PolicyName
					case testconfig.DualNICBoundaryClock:
						policyName = utils.PtpBcMaster1PolicyName
					}
					ptpConfigToModify, err := client.Client.PtpV1Interface.PtpConfigs(utils.PtpLinuxDaemonNamespace).Get(context.Background(), policyName, metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())
					nodes, err := client.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
						LabelSelector: utils.PtpClockUnderTestNodeLabel,
					})
					Expect(err).NotTo(HaveOccurred())
					Expect(len(nodes.Items)).To(BeNumerically(">", 0),
						fmt.Sprintf("PTP Nodes with label %s are not deployed on cluster", utils.PtpClockUnderTestNodeLabel))

					ptpConfigTest := mutateProfile(ptpConfigToModify, utils.PtpTempPolicyName, nodes.Items[0].Name)
					modifiedPtpConfig, err = client.Client.PtpV1Interface.PtpConfigs(utils.PtpLinuxDaemonNamespace).Create(context.Background(), ptpConfigTest, metav1.CreateOptions{})
					Expect(err).NotTo(HaveOccurred())

					testPtpPod, err = getPtpPodOnNode(nodes.Items[0].Name)
					Expect(err).NotTo(HaveOccurred())

					testPtpPod, err = replaceTestPod(&testPtpPod, time.Minute)
					Expect(err).NotTo(HaveOccurred())
				})

				By("Checking if Node has Profile and check sync", func() {
					var grandmasterID *string
					if fullConfig.L2Config != nil && !isSingleNode {
						aLabel := utils.PtpGrandmasterNodeLabel
						aString, err := getClockIDMaster(utils.PtpGrandMasterPolicyName, &aLabel, nil)
						grandmasterID = &aString
						Expect(err).To(BeNil())
					}
					BasicClockSyncCheck(fullConfig, modifiedPtpConfig, grandmasterID)
				})

				By("Deleting the test profile", func() {
					err := client.Client.PtpV1Interface.PtpConfigs(utils.PtpLinuxDaemonNamespace).Delete(context.Background(), utils.PtpTempPolicyName, metav1.DeleteOptions{})
					Expect(err).NotTo(HaveOccurred())
					Eventually(func() bool {
						_, err := client.Client.PtpV1Interface.PtpConfigs(utils.PtpLinuxDaemonNamespace).Get(context.Background(), utils.PtpTempPolicyName, metav1.GetOptions{})
						return kerrors.IsNotFound(err)
					}, 1*time.Minute, 1*time.Second).Should(BeTrue(), "Could not delete the test profile")
				})

				By("Checking the profile is reverted", func() {
					waitUntilLogIsDetected(&testPtpPod, timeoutIn3Minutes, "Profile Name: "+policyName)
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
				if ptpConfig.DiscoveredGrandMasterPtpConfig != nil {
					printInterface(ptpv1.PtpConfig(*ptpConfig.DiscoveredGrandMasterPtpConfig), mapping)
				}
				if ptpConfig.DiscoveredClockUnderTestPtpConfig != nil {
					printInterface(ptpv1.PtpConfig(*ptpConfig.DiscoveredClockUnderTestPtpConfig), mapping)
				}
				if ptpConfig.DiscoveredClockUnderTestSecondaryPtpConfig != nil {
					printInterface(ptpv1.PtpConfig(*ptpConfig.DiscoveredClockUnderTestSecondaryPtpConfig), mapping)
				}

				By("Getting ptp config details")

				logrus.Infof("Discovered master ptp config %s", ptpConfig.DiscoveredGrandMasterPtpConfig.String())
				logrus.Infof("Discovered slave ptp config %s", ptpConfig.DiscoveredClockUnderTestPtpConfig.String())
			})
		})

		Context("PTP metric is present", func() {
			BeforeEach(func() {
				waitForPtpDaemonToBeReady()
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				ptpPods, err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")

				ptpRunningPods = testPtpRunningPods(ptpPods)
			})

			// 27324
			It("on slave", func() {
				slavePodDetected := false
				for podIndex := range ptpRunningPods {
					if isClockUnderTestPod(ptpRunningPods[podIndex]) {
						Eventually(func() string {
							buf, _ := pods.ExecCommand(client.Client, ptpRunningPods[podIndex], utils.PtpContainerName, []string{"curl", metricsEndPoint})
							return buf.String()
						}, timeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpOffsetNs),
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
				waitForPtpDaemonToBeReady()
				if !ptpEventEnabled() {
					Skip("Skipping, PTP events not enabled")
				}
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				ptpPods, err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")

				for podIndex := range ptpPods.Items {
					if role, _ := pods.PodRole(&ptpPods.Items[podIndex], utils.PtpClockUnderTestNodeLabel); role {
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
						if c.Name == utils.EventProxyContainerName {
							cloudProxyFound = true
						}
					}
					Expect(cloudProxyFound).ToNot(BeFalse(), "No event pods detected")
				}

				By("Checking event metrics are present")
				for podIndex := range ptpSlaveRunningPods {
					Eventually(func() string {
						buf, _ := pods.ExecCommand(client.Client, &ptpSlaveRunningPods[podIndex], utils.EventProxyContainerName, []string{"curl", metricsEndPoint})
						return buf.String()
					}, timeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpInterfaceRole),
						"Interface role metrics are not detected")

					Eventually(func() string {
						buf, _ := pods.ExecCommand(client.Client, &ptpSlaveRunningPods[podIndex], utils.EventProxyContainerName, []string{"curl", metricsEndPoint})
						return buf.String()
					}, timeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpThreshold),
						"Threshold metrics are not detected")
				}

				By("Checking event api is healthy")
				for podIndex := range ptpSlaveRunningPods {
					Eventually(func() string {
						buf, _ := pods.ExecCommand(client.Client, &ptpSlaveRunningPods[podIndex], utils.EventProxyContainerName, []string{"curl", "127.0.0.1:9085/api/cloudNotifications/v1/health"})
						return buf.String()
					}, timeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("OK"),
						"Event API is not in healthy state")
				}

				By("Checking ptp publisher is created")
				for podIndex := range ptpSlaveRunningPods {
					Eventually(func() string {
						buf, _ := pods.ExecCommand(client.Client, &ptpSlaveRunningPods[podIndex], utils.EventProxyContainerName, []string{"curl", "127.0.0.1:9085/api/cloudNotifications/v1/publishers"})
						return buf.String()
					}, timeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("endpointUri"),
						"Event API  did not return publishers")
				}

				By("Checking events are generated")
				for podIndex := range ptpSlaveRunningPods {
					podLogs, err := pods.GetLog(&ptpSlaveRunningPods[podIndex], utils.EventProxyContainerName)
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
				waitForPtpDaemonToBeReady()
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}

				masterConfigs, slaveConfigs := discoveryPTPConfiguration(utils.PtpLinuxDaemonNamespace)
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
				ptpPods, err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")
				for name, priority := range fifoPriorities {
					ptp4lLog := fmt.Sprintf("/bin/chrt -f %d /usr/sbin/ptp4l", priority)
					for podIndex := range ptpPods.Items {
						logs, err := pods.GetLog(&ptpPods.Items[podIndex], utils.PtpContainerName)
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

func restartPtpDaemon() {
	ptpPods, err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	Expect(err).ToNot(HaveOccurred())
	for podIndex := range ptpPods.Items {
		err = client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).Delete(context.Background(), ptpPods.Items[podIndex].Name, metav1.DeleteOptions{GracePeriodSeconds: pointer.Int64Ptr(0)})
		Expect(err).ToNot(HaveOccurred())
	}

	waitForPtpDaemonToBeReady()
}

func waitForPtpDaemonToBeReady() int {
	daemonset, err := client.Client.DaemonSets(utils.PtpLinuxDaemonNamespace).Get(context.Background(), utils.PtpDaemonsetName, metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred())
	expectedNumber := daemonset.Status.DesiredNumberScheduled
	Eventually(func() int32 {
		daemonset, err = client.Client.DaemonSets(utils.PtpLinuxDaemonNamespace).Get(context.Background(), utils.PtpDaemonsetName, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		return daemonset.Status.NumberReady
	}, timeoutIn5Minutes, 2*time.Second).Should(Equal(expectedNumber))

	Eventually(func() int {
		ptpPods, err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
		Expect(err).ToNot(HaveOccurred())
		return len(ptpPods.Items)
	}, timeoutIn5Minutes, 2*time.Second).Should(Equal(int(expectedNumber)))
	return 0
}

// Returns the slave node label to be used in the test
func discoveryPTPConfiguration(namespace string) (masters, slaves []*ptpv1.PtpConfig) {
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

// This function parses ethtool command output and detect interfaces which supports ptp protocol
func isPTPEnabled(ethToolOutput *bytes.Buffer) bool {
	var RxEnabled bool
	var TxEnabled bool
	var RawEnabled bool

	scanner := bufio.NewScanner(ethToolOutput)
	for scanner.Scan() {
		line := strings.TrimPrefix(scanner.Text(), "\t")
		parts := strings.Fields(line)
		if parts[0] == utils.ETHTOOL_HARDWARE_RECEIVE_CAP {
			RxEnabled = true
		}
		if parts[0] == utils.ETHTOOL_HARDWARE_TRANSMIT_CAP {
			TxEnabled = true
		}
		if parts[0] == utils.ETHTOOL_HARDWARE_RAW_CLOCK_CAP {
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
	mutatedConfig.ObjectMeta.Name = utils.PtpTempPolicyName
	mutatedConfig.ObjectMeta.Namespace = utils.PtpLinuxDaemonNamespace
	mutatedConfig.Spec.Profile[0].Name = &profileName
	mutatedConfig.Spec.Recommend[0].Priority = &priority
	mutatedConfig.Spec.Recommend[0].Match[0].NodeLabel = nil
	mutatedConfig.Spec.Recommend[0].Match[0].NodeName = &nodeName
	mutatedConfig.Spec.Recommend[0].Profile = &profileName
	return mutatedConfig
}

func waitUntilLogIsDetected(pod *v1core.Pod, timeout time.Duration, neededLog string) {
	Eventually(func() string {
		logs, _ := pods.GetLog(pod, utils.PtpContainerName)
		logrus.Debugf("wait for log = %s in pod=%s.%s", neededLog, pod.Namespace, pod.Name)
		return logs
	}, timeout, 1*time.Second).Should(ContainSubstring(neededLog), fmt.Sprintf("Timeout to detect log %q in pod %q", neededLog, pod.Name))
}

// looks for a given pattern in a pod's log and returns when found
func waitUntilLogIsDetectedRegex(pod *v1core.Pod, timeout time.Duration, regex string) string {
	var results []string
	Eventually(func() []string {
		podLogs, _ := pods.GetLog(pod, utils.PtpContainerName)
		logrus.Debugf("wait for log = %s in pod=%s.%s", regex, pod.Namespace, pod.Name)
		r := regexp.MustCompile(regex)
		var id string

		for _, submatches := range r.FindAllStringSubmatchIndex(podLogs, -1) {
			id = string(r.ExpandString([]byte{}, "$1", podLogs, submatches))
			results = append(results, id)
		}
		return results
	}, timeout, 5*time.Second).Should(Not(HaveLen(0)), fmt.Sprintf("Timeout to detect regex %q in pod %q", regex, pod.Name))
	if len(results) != 0 {
		return results[len(results)-1]
	}
	return ""
}

func getPtpPodOnNode(nodeName string) (v1core.Pod, error) {
	waitForPtpDaemonToBeReady()
	runningPod, err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
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
		stdout, err := pods.ExecCommand(client.Client, pod, utils.PtpContainerName, []string{"ls", "/sys/class/net/"})
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
			stdout, err = pods.ExecCommand(client.Client, pod, utils.PtpContainerName, []string{"readlink", "-f", fmt.Sprintf("/sys/class/net/%s", interf)})
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
			stdout, err = pods.ExecCommand(client.Client, pod, utils.PtpContainerName, []string{"ls", fmt.Sprintf("/sys/bus/pci/devices/%s/physfn", PCIAddr)})
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
			stdout, err = pods.ExecCommand(client.Client, pod, utils.PtpContainerName, []string{"ethtool", "-T", interf})
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
			logrus.Debugf("Append ptp interface=%s from node=%s", interf, pod.Spec.NodeName)
		}
	}
	return ptpSupportedInterfaces
}

func replaceTestPod(pod *v1core.Pod, timeout time.Duration) (v1core.Pod, error) {
	var newPod v1core.Pod

	err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{
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
			stdout, err = pods.ExecCommand(client.Client, pod, utils.PtpContainerName, []string{"ethtool", "-T", interf})
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
	ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(utils.PtpLinuxDaemonNamespace).Get(context.Background(), ptpConfigOperatorName, metav1.GetOptions{})
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
	_, err = client.Client.PtpOperatorConfigs(utils.PtpLinuxDaemonNamespace).Update(context.Background(), ptpConfig, metav1.UpdateOptions{})
	return err
}

func ptpEventEnabled() bool {
	ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(utils.PtpLinuxDaemonNamespace).Get(context.Background(), ptpConfigOperatorName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	if ptpConfig.Spec.EventConfig == nil {
		return false
	}
	return ptpConfig.Spec.EventConfig.EnableEventPublisher
}

func getNICInfo(pod *v1core.Pod) map[string]string {
	var ptpSupportedInterfaces []string = getPtpMasterSlaveAttachedInterfaces(pod)
	var stdout bytes.Buffer

	var ptpInterfaceNicMapping = make(map[string]string)

	for _, interf := range ptpSupportedInterfaces {
		PCIAddr := ""
		var err error

		Eventually(func() error {
			stdout, err = pods.ExecCommand(client.Client, pod, utils.PtpContainerName, []string{"readlink", "-f", fmt.Sprintf("/sys/class/net/%s", interf)})
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

	deploy, err := client.Client.AppsV1Interface.Deployments(utils.PtpLinuxDaemonNamespace).Get(context.TODO(), utils.PtpOperatorDeploymentName, metav1.GetOptions{})

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

	// Create the client of Priviledged Daemonset
	k8sPriviledgedDs.SetDaemonSetClient(client.Client.Interface)
	// 1. create a daemon set for the node reboot
	rebootDaemonSetRunningPods, err := k8sPriviledgedDs.CreateDaemonSet(rebootDaemonSetName, rebootDaemonSetNamespace, rebootDaemonSetContainerName, imageWithVersion, timeoutIn5Minutes)
	if err != nil {
		logrus.Errorf("error : +%v\n", err.Error())
	}
	nodeToPodMapping := make(map[string]v1core.Pod)
	for _, dsPod := range rebootDaemonSetRunningPods.Items {
		nodeToPodMapping[dsPod.Spec.NodeName] = dsPod
	}

	// 2. Get the slave config, restart the slave node
	slavePtpConfig := fullConfig.DiscoveredClockUnderTestPtpConfig
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
	nodes.WaitForNodeReachability(&node, timeoutIn10Minutes, unrechable)

	// Wait for all nodes to be reachable now after the restart
	const reachable = true
	nodes.WaitForNodeReachability(&node, timeoutIn10Minutes, reachable)
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
	ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(utils.PtpLinuxDaemonNamespace).Get(context.Background(), ptpConfigOperatorName, metav1.GetOptions{})
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

	By("Get daemonsets collection for the namespace " + utils.PtpLinuxDaemonNamespace)
	ds, err := client.Client.DaemonSets(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{})
	Expect(err).ToNot(HaveOccurred())
	Expect(len(ds.Items)).To(BeNumerically(">", 0), "no damonsets found in the namespace "+utils.PtpLinuxDaemonNamespace)

	By("Checking number of scheduled instances")
	Expect(ds.Items[0].Status.CurrentNumberScheduled).To(BeNumerically("==", len(nodes.Items)), "should be one instance per node")

	By("Checking if the ptp offset metric is present")
	for _, slaveNode := range rebootedNodes {

		runningPods := getRebootDaemonsetPodsAt(slaveNode)

		// Testing for one pod is sufficient as these pods are running on the same node that restarted
		for _, pod := range runningPods.Items {
			Expect(isClockUnderTestPod(&pod)).To(BeTrue())

			logrus.Printf("Calling metrics endpoint for pod %s with status %s", pod.Name, pod.Status.Phase)

			time.Sleep(timeoutIn3Minutes)

			Eventually(func() string {
				commands := []string{
					"curl", "-s", metricsEndPoint,
				}
				buf, err := pods.ExecCommand(client.Client, &pod, rebootDaemonSetContainerName, commands)
				Expect(err).NotTo(HaveOccurred())

				scanner := bufio.NewScanner(strings.NewReader(buf.String()))
				var lines []string = make([]string, 5)
				for scanner.Scan() {
					text := scanner.Text()
					if strings.Contains(text, metrics.OpenshiftPtpOffsetNs+"{from=\"master\"") {
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
			}, timeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpOffsetNs),
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

	ptpPods, err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	Expect(err).NotTo(HaveOccurred())
	Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")

	ptpSlaveRunningPods := []v1core.Pod{}
	ptpMasterRunningPods := []v1core.Pod{}

	for _, pod := range ptpPods.Items {
		if isClockUnderTestPod(&pod) {
			waitUntilLogIsDetected(&pod, timeoutIn5Minutes, "Profile Name:")
			ptpSlaveRunningPods = append(ptpSlaveRunningPods, pod)
		} else if isGrandMasterPod(&pod) {
			waitUntilLogIsDetected(&pod, timeoutIn5Minutes, "Profile Name:")
			ptpMasterRunningPods = append(ptpMasterRunningPods, pod)
		}
	}
	if testconfig.GlobalConfig.DiscoveredGrandMasterPtpConfig != nil {
		Expect(len(ptpMasterRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP master pods on Cluster")
		Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
	} else {
		Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
	}

	var masterID string
	var slaveMasterID string
	grandMaster := "assuming the grand master role"

	for _, pod := range ptpPods.Items {
		if utils.PtpGrandmasterNodeLabel != "" &&
			isGrandMasterPod(&pod) {
			podLogs, err := pods.GetLog(&pod, utils.PtpContainerName)
			Expect(err).NotTo(HaveOccurred(), "Error to find needed log due to %s", err)
			Expect(podLogs).Should(ContainSubstring(grandMaster),
				fmt.Sprintf("Log message %q not found in pod's log %s", grandMaster, pod.Name))
			for _, line := range strings.Split(podLogs, "\n") {
				if strings.Contains(line, "selected local clock") && strings.Contains(line, "as best master") {
					// Log example: ptp4l[10731.364]: [eno1] selected local clock 3448ed.fffe.f38e00 as best master
					masterID = strings.Split(line, " ")[5]
				}
			}
		}
		if isClockUnderTestPod(&pod) {
			podLogs, err := pods.GetLog(&pod, utils.PtpContainerName)
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

func getProfileLogID(ptpConfigName string, label, nodeName *string) (id string, err error) {
	ptpPods, err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	if err != nil {
		return id, err
	}
	for _, pod := range ptpPods.Items {
		isPodFound, err := pods.HasPodLabelOrNodeName(&pod, label, nodeName)
		if err != nil {
			logrus.Errorf("could not check %s pod role, err: %s", *label, err)
			Fail(fmt.Sprintf("could not check %s pod role, err: %s", *label, err))
		}

		if !isPodFound {
			continue
		}
		waitUntilLogIsDetected(&pod, timeoutIn10Minutes, `Ptp4lConf: #profile:`)

		podLogs, err := pods.GetLog(&pod, utils.PtpContainerName)
		if err != nil {
			return id, err
		}

		for _, line := range strings.Split(podLogs, " daemon.go") {
			if strings.Contains(line, `Ptp4lConf: #profile:`) && strings.Contains(line, ptpConfigName) {
				r := regexp.MustCompile(`(?m)message_tag \[(.*)\]`)
				for _, submatches := range r.FindAllStringSubmatchIndex(line, -1) {
					id = string(r.ExpandString([]byte{}, "$1", line, submatches))
					return id, nil
				}
			}
		}

	}
	return id, nil
}

func getClockIDMaster(ptpConfigName string, label, nodeName *string) (id string, err error) {
	logID, err := getProfileLogID(ptpConfigName, label, nodeName)
	if err != nil {
		return id, err
	}
	ptpPods, err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	if err != nil {
		return id, err
	}
	for _, pod := range ptpPods.Items {
		isPodFound, err := pods.HasPodLabelOrNodeName(&pod, label, nodeName)
		if err != nil {
			logrus.Errorf("could not check %s pod role, err: %s", label, err)
			Fail(fmt.Sprintf("could not check %s pod role, err: %s", label, err))
		}

		if !isPodFound {
			continue
		}

		return waitUntilLogIsDetectedRegex(&pod, timeoutIn10Minutes, `(?m)\[`+logID+`\] selected local clock (.*) as best master`), nil

	}
	return id, err
}

func getClockIDForeign(ptpConfigName string, label, nodeName *string) (id string, err error) {
	logID, err := getProfileLogID(ptpConfigName, label, nodeName)
	if err != nil {
		return id, err
	}
	var results []string
	ptpPods, err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	if err != nil {
		return id, err
	}
	for _, pod := range ptpPods.Items {

		isPodFound, err := pods.HasPodLabelOrNodeName(&pod, label, nodeName)
		if err != nil {
			logrus.Errorf("could not check %s pod role, err: %s", label, err)
			Fail(fmt.Sprintf("could not check %s pod role, err: %s", label, err))
		}

		if !isPodFound {
			continue
		}

		waitUntilLogIsDetected(&pod, timeoutIn10Minutes, "new foreign master")
		podLogs, err := pods.GetLog(&pod, utils.PtpContainerName)
		if err != nil {
			return id, err
		}

		r := regexp.MustCompile(`(?m)\[` + logID + `\].*new foreign master (.*)`)
		for _, submatches := range r.FindAllStringSubmatchIndex(podLogs, -1) {
			id = string(r.ExpandString([]byte{}, "$1", podLogs, submatches))
			results = append(results, id)
		}

		if len(results) == 0 {
			return id, fmt.Errorf("no match for last master clock ID")
		}
		return results[len(results)-1], nil
	}
	return id, err
}

// helper function for old interface discovery test
func testPtpRunningPods(ptpPods *v1core.PodList) (ptpRunningPods []*v1core.Pod) {
	ptpSlaveRunningPods := []*v1core.Pod{}
	ptpMasterRunningPods := []*v1core.Pod{}
	for podIndex := range ptpPods.Items {
		isClockUnderTestPod, err := pods.PodRole(&ptpPods.Items[podIndex], utils.PtpClockUnderTestNodeLabel)
		if err != nil {
			logrus.Errorf("could not check clock under test pod role, err: %s", err)
			Fail(fmt.Sprintf("could not check clock under test pod role, err: %s", err))
		}

		isGrandmaster, err := pods.PodRole(&ptpPods.Items[podIndex], utils.PtpGrandmasterNodeLabel)
		if err != nil {
			logrus.Errorf("could not check Grandmaster pod role, err: %s", err)
			Fail(fmt.Sprintf("could not check Grandmaster pod role, err: %s", err))
		}

		if isClockUnderTestPod {
			waitUntilLogIsDetected(&ptpPods.Items[podIndex], timeoutIn3Minutes, "Profile Name:")
			ptpSlaveRunningPods = append(ptpSlaveRunningPods, &ptpPods.Items[podIndex])
		} else if isGrandmaster {
			waitUntilLogIsDetected(&ptpPods.Items[podIndex], timeoutIn3Minutes, "Profile Name:")
			ptpMasterRunningPods = append(ptpMasterRunningPods, &ptpPods.Items[podIndex])
		}
	}
	if testconfig.GlobalConfig.DiscoveredGrandMasterPtpConfig != nil {
		Expect(len(ptpMasterRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP master pods on Cluster")
		Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
	} else {
		Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
	}
	ptpRunningPods = append(ptpRunningPods, ptpSlaveRunningPods...)
	ptpRunningPods = append(ptpRunningPods, ptpMasterRunningPods...)
	return ptpRunningPods
}

// returns true if the pod is running a grandmaster
func isGrandMasterPod(aPod *v1core.Pod) bool {

	result, err := pods.PodRole(aPod, utils.PtpGrandmasterNodeLabel)
	if err != nil {
		logrus.Errorf("could not check Grandmaster pod role, err: %s", err)
		Fail(fmt.Sprintf("could not check Grandmaster pod role, err: %s", err))
	}
	return result
}

// returns true if the pod is running the clock under test
func isClockUnderTestPod(aPod *v1core.Pod) bool {

	result, err := pods.PodRole(aPod, utils.PtpClockUnderTestNodeLabel)
	if err != nil {
		logrus.Errorf("could not check Clock under test pod role, err: %s", err)
		Fail(fmt.Sprintf("could not check Clock under test pod role, err: %s", err))
	}
	return result
}

// waits for the foreign master to appear in the logs and checks the clock accuracy
func BasicClockSyncCheck(fullConfig testconfig.TestConfig, ptpConfig *ptpv1.PtpConfig, gmID *string) {
	if gmID != nil {
		logrus.Infof("expected master=%s", *gmID)
	}
	profileName, errProfile := testconfig.GetProfileName(ptpConfig)

	if fullConfig.PtpModeDesired == testconfig.Discovery {
		if errProfile != nil {
			logrus.Infof("profile name not detected in log (probably because of log rollover)). Remote clock ID will not be printed")
		}
	} else {
		Expect(errProfile).To(BeNil())
	}

	label, err := metrics.GetLabel(ptpConfig)
	nodeName, err := metrics.GetFirstNode(ptpConfig)
	slaveMaster, err := getClockIDForeign(profileName, label, nodeName)
	if errProfile == nil {
		if fullConfig.PtpModeDesired == testconfig.Discovery {
			if err != nil {
				logrus.Infof("slave's Master not detected in log (probably because of log rollover))")
			} else {
				logrus.Infof("slave's Master=%s", slaveMaster)
			}
		} else {
			Expect(err).To(BeNil())
			Expect(slaveMaster).NotTo(BeNil())
			logrus.Infof("slave's Master=%s", slaveMaster)
		}
	}
	if gmID != nil {
		Expect(slaveMaster).Should(HavePrefix(*gmID), "Slave connected to another (incorrect) Master")
	}

	Eventually(func() error {
		err = metrics.CheckClockRoleAndOffset(ptpConfig, label, nodeName)
		if err != nil {
			logrus.Infof(fmt.Sprintf("CheckClockRoleAndOffset Failed because of err: %s", err))
		}
		return err
	}, timeoutIn3Minutes, timeout10Seconds).Should(BeNil(), fmt.Sprintf("Timeout to detect metrics for ptpconfig %s", ptpConfig.Name))

}

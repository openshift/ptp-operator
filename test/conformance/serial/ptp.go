package test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/ptp-operator/test/pkg"
	"github.com/openshift/ptp-operator/test/pkg/event"
	"github.com/openshift/ptp-operator/test/pkg/metrics"
	"github.com/openshift/ptp-operator/test/pkg/namespaces"
	"github.com/openshift/ptp-operator/test/pkg/ptphelper"
	"github.com/openshift/ptp-operator/test/pkg/ptptesthelper"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/apps/v1"
	v1core "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"

	"github.com/openshift/ptp-operator/test/pkg/pods"

	"github.com/openshift/ptp-operator/test/pkg/client"
	"github.com/openshift/ptp-operator/test/pkg/execute"

	"github.com/openshift/ptp-operator/test/pkg/testconfig"
)

type TestCase string

const (
	Reboot TestCase = "reboot"
)

var DesiredMode = testconfig.GetDesiredConfig(true).PtpModeDesired

var _ = Describe("["+strings.ToLower(DesiredMode.String())+"-serial]", Serial, func() {
	BeforeEach(func() {
		Expect(client.Client).NotTo(BeNil())
	})

	Context("PTP configuration verifications", func() {
		// Setup verification
		// if requested enabled  ptp events
		It("Should check whether PTP operator needs to enable PTP events", func() {
			By("Find if variable set to enable ptp events")
			if event.Enable() {
				apiVersion := ptphelper.GetDefaultApiVersion()
				err := ptphelper.EnablePTPEvent(apiVersion, "")
				Expect(err).To(BeNil(), "error when enable ptp event")
				ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
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
				if strings.Contains(g.GroupVersion, pkg.PtpResourcesGroupVersionPrefix) {
					for _, r := range g.APIResources {
						By("Search for resource " + pkg.PtpResourcesNameOperatorConfigs)
						if r.Name == pkg.PtpResourcesNameOperatorConfigs {
							found = true
						}
					}
				}
			}

			Expect(found).To(BeTrue(), fmt.Sprintf("resource %s not found", pkg.PtpResourcesNameOperatorConfigs))
		})
		// Setup verification
		It("Should check that all nodes are running at least one replica of linuxptp-daemon", func() {
			By("Getting ptp operator config")

			ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
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

			By("Get daemonsets collection for the namespace " + pkg.PtpLinuxDaemonNamespace)
			ds, err := client.Client.DaemonSets(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(ds.Items)).To(BeNumerically(">", 0), "no damonsets found in the namespace "+pkg.PtpLinuxDaemonNamespace)
			By("Checking number of scheduled instances")
			Expect(ds.Items[0].Status.CurrentNumberScheduled).To(BeNumerically("==", len(nodes.Items)), "should be one instance per node")
		})
		// Setup verification
		It("Should check that operator is deployed", func() {
			By("Getting deployment " + pkg.PtpOperatorDeploymentName)
			dep, err := client.Client.Deployments(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpOperatorDeploymentName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			By("Checking availability of the deployment")
			for _, c := range dep.Status.Conditions {
				if c.Type == v1.DeploymentAvailable {
					Expect(string(c.Status)).Should(Equal("True"), pkg.PtpOperatorDeploymentName+" deployment is not available")
				}
			}
		})

	})

	Describe("PTP e2e tests", func() {
		var ptpPods *v1core.PodList
		var fifoPriorities map[string]int64
		var fullConfig testconfig.TestConfig
		execute.BeforeAll(func() {
			err := testconfig.CreatePtpConfigurations()
			if err != nil {
				fullConfig.Status = testconfig.DiscoveryFailureStatus
				Fail(fmt.Sprintf("Could not create a ptp config, err=%s", err))
			}
			fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, false)
			if fullConfig.Status != testconfig.DiscoverySuccessStatus {
				logrus.Printf(`ptpconfigs were not properly discovered, Check:
- the ptpconfig has a %s label only in the recommend section (no node section)
- the node running the clock under test is label with: %s`, pkg.PtpClockUnderTestNodeLabel, pkg.PtpClockUnderTestNodeLabel)

				Fail("Failed to find a valid ptp slave configuration")

			}
			if fullConfig.PtpModeDesired != testconfig.Discovery {
				ptphelper.RestartPTPDaemon()
			}

		})

		Context("PTP Reboot discovery", func() {
			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToBeReady()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
			})

			It("The slave node is rebooted and discovered and in sync", func() {
				Skip("This is covered by QE")
				if testCaseEnabled(Reboot) {
					By("Slave node is rebooted", func() {
						ptptesthelper.RebootSlaveNode(fullConfig)
					})
				} else {
					Skip("Skipping the reboot test")
				}
			})
		})

		Context("PTP Interfaces discovery", func() {

			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToBeReady()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				var err error
				ptpPods, err = client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")

			})

			It("The interfaces supporting ptp can be discovered correctly", func() {
				for podIndex := range ptpPods.Items {
					ptpNodeIfacesDiscoveredByL2 := ptphelper.GetPtpInterfacePerNode(ptpPods.Items[podIndex].Spec.NodeName, fullConfig.L2Config.GetPtpIfListUnfiltered())
					lenPtpNodeIfacesDiscoveredByL2 := len(ptpNodeIfacesDiscoveredByL2)
					ptpNodeIfacesFromPtpApi := ptphelper.PtpDiscoveredInterfaceList(ptpPods.Items[podIndex].Spec.NodeName)
					lenPtpNodeIfacesFromPtpApi := len(ptpNodeIfacesFromPtpApi)
					sort.Strings(ptpNodeIfacesDiscoveredByL2)
					sort.Strings(ptpNodeIfacesFromPtpApi)
					logrus.Infof("Interfaces supporting ptp for node        %s: %v", ptpPods.Items[podIndex].Spec.NodeName, ptpNodeIfacesDiscoveredByL2)
					logrus.Infof("Interfaces discovered by ptp API for node %s: %v", ptpPods.Items[podIndex].Spec.NodeName, ptpNodeIfacesFromPtpApi)

					// The discovered PTP interfaces should match exactly the list of interfaces calculated by test
					Expect(lenPtpNodeIfacesDiscoveredByL2).To(Equal(lenPtpNodeIfacesFromPtpApi))
					for index := range ptpNodeIfacesDiscoveredByL2 {
						Expect(ptpNodeIfacesDiscoveredByL2[index]).To(Equal(ptpNodeIfacesFromPtpApi[index]))
					}
				}
			})

			It("Should retrieve the details of hardwares for the Ptp", func() {
				By("Getting the version of the OCP cluster")

				ocpVersion, err := getOCPVersion()
				Expect(err).ToNot(HaveOccurred())
				Expect(ocpVersion).ShouldNot(BeEmpty())

				By("Getting the version of the PTP operator")

				ptpOperatorVersion, err := ptphelper.GetPtpOperatorVersion()
				Expect(err).ToNot(HaveOccurred())
				Expect(ptpOperatorVersion).ShouldNot(BeEmpty())

				By("Getting the NIC details of all the PTP enabled interfaces")

				ptpInterfacesList := fullConfig.L2Config.GetPtpIfList()

				for _, ptpInterface := range ptpInterfacesList {
					ifaceHwDetails := fmt.Sprintf("Device: %s, Function: %s, Description: %s",
						ptpInterface.IfPci.Device, ptpInterface.IfPci.Function, ptpInterface.IfPci.Description)

					logrus.Debugf("Node: %s, Interface Name: %s, %s", ptpInterface.NodeName, ptpInterface.IfName, ifaceHwDetails)

					AddReportEntry(fmt.Sprintf("Node %s, Interface: %s", ptpInterface.NodeName, ptpInterface.IfName), ifaceHwDetails)
				}

				By("Getting ptp config details")
				ptpConfig := testconfig.GlobalConfig

				masterPtpConfigStr := ptpConfig.DiscoveredGrandMasterPtpConfig.String()
				slavePtpConfigStr := ptpConfig.DiscoveredClockUnderTestPtpConfig.String()

				logrus.Infof("Discovered master ptp config %s", masterPtpConfigStr)
				logrus.Infof("Discovered slave ptp config %s", slavePtpConfigStr)

				AddReportEntry("master-ptp-config", masterPtpConfigStr)
				AddReportEntry("slave-ptp-config", slavePtpConfigStr)
			})
		})

		Context("PTP ClockSync", func() {
			err := metrics.InitEnvIntParamConfig("MAX_OFFSET_IN_NS", metrics.MaxOffsetDefaultNs, &metrics.MaxOffsetNs)
			Expect(err).NotTo(HaveOccurred(), "error getting max offset in nanoseconds %s", err)
			err = metrics.InitEnvIntParamConfig("MIN_OFFSET_IN_NS", metrics.MinOffsetDefaultNs, &metrics.MinOffsetNs)
			Expect(err).NotTo(HaveOccurred(), "error getting min offset in nanoseconds %s", err)

			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToBeReady()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
			})
			// 25733
			It("PTP daemon apply match rule based on nodeLabel", func() {

				if fullConfig.PtpModeDesired == testconfig.Discovery {
					Skip("This test needs the ptp-daemon to be rebooted but it is not possible in discovery mode, skipping")
				}
				profileSlave := fmt.Sprintf("Profile Name: %s", fullConfig.DiscoveredClockUnderTestPtpConfig.Name)
				profileMaster := ""
				if fullConfig.DiscoveredGrandMasterPtpConfig != nil {
					profileMaster = fmt.Sprintf("Profile Name: %s", fullConfig.DiscoveredGrandMasterPtpConfig.Name)
				}

				for podIndex := range ptpPods.Items {
					isClockUnderTest, err := ptphelper.IsClockUnderTestPod(&ptpPods.Items[podIndex])
					if err != nil {
						Fail(fmt.Sprintf("check clock under test clock type, err=%s", err))
					}
					isGrandmaster, err := ptphelper.IsGrandMasterPod(&ptpPods.Items[podIndex])
					if err != nil {
						Fail(fmt.Sprintf("check Grandmaster clock type, err=%s", err))
					}
					if isClockUnderTest {
						_, err = pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							profileSlave, true, pkg.TimeoutIn3Minutes)
						if err != nil {
							Fail(fmt.Sprintf("could not get slave profile name, err=%s", err))
						}
					} else if isGrandmaster && fullConfig.DiscoveredGrandMasterPtpConfig != nil {
						_, err = pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							profileMaster, true, pkg.TimeoutIn5Minutes)
						if err != nil {
							Fail(fmt.Sprintf("could not get master profile name, err=%s", err))
						}
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
				if fullConfig.PtpModeDesired == testconfig.TelcoGrandMasterClock {
					Skip("Skipping as slave interface is not available with a WPC-GM profile")
				}
				isExternalMaster := ptphelper.IsExternalGM()
				var grandmasterID *string
				if fullConfig.L2Config != nil && !isExternalMaster {
					aLabel := pkg.PtpGrandmasterNodeLabel
					aString, err := ptphelper.GetClockIDMaster(pkg.PtpGrandMasterPolicyName, &aLabel, nil, true)
					grandmasterID = &aString
					Expect(err).To(BeNil())
				}
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID)
				Expect(err).To(BeNil())
				if fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClock {
					err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestSecondaryPtpConfig), grandmasterID)
					Expect(err).To(BeNil())
				}
			})

			// Multinode BCSlave clock sync
			// - waits for the BCSlave foreign master to appear (the boundary clock)
			// - verifies that the BCSlave foreign master has the expected boundary clock ID
			// - use metrics to verify that the offset with boundary clock is below threshold
			It("Downstream slave can sync to BC master", func() {
				if fullConfig.PtpModeDesired == testconfig.TelcoGrandMasterClock {
					Skip("test not valid for WPC GM testing only valid for BC config in multi-node cluster ")
				}

				if fullConfig.PtpModeDiscovered != testconfig.BoundaryClock &&
					fullConfig.PtpModeDiscovered != testconfig.DualNICBoundaryClock {
					Skip("test only valid for Boundary clock in multi-node clusters")
				}
				if !fullConfig.FoundSolutions[testconfig.AlgoBCWithSlavesString] &&
					!fullConfig.FoundSolutions[testconfig.AlgoDualNicBCWithSlavesString] &&
					!fullConfig.FoundSolutions[testconfig.AlgoBCWithSlavesExtGMString] &&
					!fullConfig.FoundSolutions[testconfig.AlgoDualNicBCWithSlavesExtGMString] {
					Skip("test only valid for Boundary clock in multi-node clusters with slaves")
				}
				aLabel := pkg.PtpClockUnderTestNodeLabel
				masterIDBc1, err := ptphelper.GetClockIDMaster(pkg.PtpBcMaster1PolicyName, &aLabel, nil, false)
				Expect(err).To(BeNil())
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredSlave1PtpConfig), &masterIDBc1)
				Expect(err).To(BeNil())

				if (fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClock) && (fullConfig.FoundSolutions[testconfig.AlgoDualNicBCWithSlavesExtGMString] ||
					fullConfig.FoundSolutions[testconfig.AlgoDualNicBCWithSlavesString]) {
					aLabel := pkg.PtpClockUnderTestNodeLabel
					masterIDBc2, err := ptphelper.GetClockIDMaster(pkg.PtpBcMaster2PolicyName, &aLabel, nil, false)
					Expect(err).To(BeNil())
					err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredSlave2PtpConfig), &masterIDBc2)
					Expect(err).To(BeNil())
				}

			})

			// 25743
			It("Can provide a profile with higher priority", func() {
				var testPtpPod v1core.Pod
				isExternalMaster := ptphelper.IsExternalGM()
				if fullConfig.PtpModeDesired == testconfig.Discovery {
					Skip("Skipping because adding a different profile and no modifications are allowed in discovery mode")
				}
				var policyName string
				var modifiedPtpConfig *ptpv1.PtpConfig
				By("Creating a config with higher priority", func() {
					if fullConfig.PtpModeDiscovered == testconfig.TelcoGrandMasterClock {
						Skip("WPC GM (T-GM) mode is not supported for this test")
					}
					switch fullConfig.PtpModeDiscovered {
					case testconfig.Discovery, testconfig.None:
						Skip("Skipping because Discovery or None is not supported yet for this test")
					case testconfig.OrdinaryClock:
						policyName = pkg.PtpSlave1PolicyName
					case testconfig.BoundaryClock:
						policyName = pkg.PtpBcMaster1PolicyName
					case testconfig.DualNICBoundaryClock:
						policyName = pkg.PtpBcMaster1PolicyName
					}
					ptpConfigToModify, err := client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), policyName, metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())
					nodes, err := client.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
						LabelSelector: pkg.PtpClockUnderTestNodeLabel,
					})
					Expect(err).NotTo(HaveOccurred())
					Expect(len(nodes.Items)).To(BeNumerically(">", 0),
						fmt.Sprintf("PTP Nodes with label %s are not deployed on cluster", pkg.PtpClockUnderTestNodeLabel))

					ptpConfigTest := ptphelper.MutateProfile(ptpConfigToModify, pkg.PtpTempPolicyName, nodes.Items[0].Name)
					modifiedPtpConfig, err = client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Create(context.Background(), ptpConfigTest, metav1.CreateOptions{})
					Expect(err).NotTo(HaveOccurred())

					testPtpPod, err = ptphelper.GetPtpPodOnNode(nodes.Items[0].Name)
					Expect(err).NotTo(HaveOccurred())

					testPtpPod, err = ptphelper.ReplaceTestPod(&testPtpPod, time.Minute)
					Expect(err).NotTo(HaveOccurred())
				})

				By("Checking if Node has Profile and check sync", func() {
					var grandmasterID *string
					if fullConfig.L2Config != nil && !isExternalMaster {
						aLabel := pkg.PtpGrandmasterNodeLabel
						aString, err := ptphelper.GetClockIDMaster(pkg.PtpGrandMasterPolicyName, &aLabel, nil, true)
						grandmasterID = &aString
						Expect(err).To(BeNil())
					}
					err = ptptesthelper.BasicClockSyncCheck(fullConfig, modifiedPtpConfig, grandmasterID)
					Expect(err).To(BeNil())
				})

				By("Deleting the test profile", func() {
					err := client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Delete(context.Background(), pkg.PtpTempPolicyName, metav1.DeleteOptions{})
					Expect(err).NotTo(HaveOccurred())
					Eventually(func() bool {
						_, err := client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpTempPolicyName, metav1.GetOptions{})
						return kerrors.IsNotFound(err)
					}, 1*time.Minute, 1*time.Second).Should(BeTrue(), "Could not delete the test profile")
				})

				By("Checking the profile is reverted", func() {
					_, err := pods.GetPodLogsRegex(testPtpPod.Namespace,
						testPtpPod.Name, pkg.PtpContainerName,
						"Profile Name: "+policyName, true, pkg.TimeoutIn3Minutes)
					if err != nil {
						Fail(fmt.Sprintf("could not get profile name, err=%s", err))
					}
				})
			})
		})

		Context("PTP metric is present", func() {
			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToBeReady()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				var err error

				_, err = pods.GetPodLogsRegex(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName,
					"Profile Name:", true, pkg.TimeoutIn3Minutes)
				if err != nil {
					Fail(fmt.Sprintf("could not get slave profile name, err=%s", err))
				}

			})

			// 27324
			It("on slave", func() {
				if fullConfig.PtpModeDiscovered == testconfig.TelcoGrandMasterClock {
					Skip("test not valid for WPC GM config")
				}
				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpOffsetNs),
					"Time metrics are not detected")
			})
		})

		Context("Running with event enabled", func() {
			BeforeEach(func() {
				if ptphelper.PtpEventEnabled() == 0 {
					Skip("Skipping, PTP events not enabled")
				}

				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToBeReady()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				})

				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				var err error
				ptpPods, err = client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")
			})

			It("Should check for ptp events ", func() {
				By("Checking event side car is present")
				apiVersion := ptphelper.PtpEventEnabled()
				var apiBase, endpointUri string
				if apiVersion == 1 {
					apiBase = event.ApiBaseV1
					endpointUri = "endpointUri"
				} else {
					apiBase = event.ApiBaseV2
					endpointUri = "EndpointUri"
				}
				cloudProxyFound := false
				Expect(len(fullConfig.DiscoveredClockUnderTestPod.Spec.Containers)).To(BeNumerically("==", 3), "linuxptp-daemon is not deployed on cluster with cloud event proxy")
				for _, c := range fullConfig.DiscoveredClockUnderTestPod.Spec.Containers {
					if c.Name == pkg.EventProxyContainerName {
						cloudProxyFound = true
					}
				}
				Expect(cloudProxyFound).ToNot(BeFalse(), "No event pods detected")

				By("Checking event api is healthy")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", path.Join(apiBase, "health")})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("OK"),
					"Event API is not in healthy state")

				By("Checking ptp publisher is created")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", path.Join(apiBase, "publishers")})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(endpointUri),
					"Event API  did not return publishers")

				By("Checking events are generated")

				_, err := pods.GetPodLogsRegex(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.EventProxyContainerName,
					"Created publisher", true, pkg.TimeoutIn3Minutes)
				if err != nil {
					Fail(fmt.Sprintf("PTP event publisher was not created in pod %s, err=%s", fullConfig.DiscoveredClockUnderTestPod.Name, err))
				}
				_, err = pods.GetPodLogsRegex(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.EventProxyContainerName,
					"event sent", true, pkg.TimeoutIn3Minutes)
				if err != nil {
					Fail(fmt.Sprintf("PTP event was not generated in the pod %s, err=%s", fullConfig.DiscoveredClockUnderTestPod.Name, err))
				}

				By("Checking event metrics are present")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", pkg.MetricsEndPoint})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpInterfaceRole),
					"Interface role metrics are not detected")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", pkg.MetricsEndPoint})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpThreshold),
					"Threshold metrics are not detected")
			})
		})

		Context("Running with event enabled, v1 regression", func() {
			BeforeEach(func() {
				if !ptphelper.IsV1EventRegressionNeeded() {
					Skip("Skipping, test PTP events v1 regression is for 4.16 and 4.17 only")
				}

				ptphelper.EnablePTPEvent("1.0", fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
				// wait for pod info updated
				time.Sleep(5 * time.Second)

				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToBeReady()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}

				var err error
				ptpPods, err = client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")
			})

			It("Should check for ptp events ", func() {
				By("Checking event side car is present")

				cloudProxyFound := false
				Expect(len(fullConfig.DiscoveredClockUnderTestPod.Spec.Containers)).To(BeNumerically("==", 3), "linuxptp-daemon is not deployed on cluster with cloud event proxy")
				for _, c := range fullConfig.DiscoveredClockUnderTestPod.Spec.Containers {
					if c.Name == pkg.EventProxyContainerName {
						cloudProxyFound = true
					}
				}
				Expect(cloudProxyFound).ToNot(BeFalse(), "No event pods detected")

				By("Checking event api is healthy")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", path.Join(event.ApiBaseV1, "health")})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("OK"),
					"Event API is not in healthy state")

				By("Checking ptp publisher is created")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", path.Join(event.ApiBaseV1, "publishers")})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("endpointUri"),
					"Event API  did not return publishers")

				By("Checking events are generated")

				_, err := pods.GetPodLogsRegex(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.EventProxyContainerName,
					"Created publisher", true, pkg.TimeoutIn3Minutes)
				if err != nil {
					Fail(fmt.Sprintf("PTP event publisher was not created in pod %s, err=%s", fullConfig.DiscoveredClockUnderTestPod.Name, err))
				}
				_, err = pods.GetPodLogsRegex(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.EventProxyContainerName,
					"event sent", true, pkg.TimeoutIn3Minutes)
				if err != nil {
					Fail(fmt.Sprintf("PTP event was not generated in the pod %s, err=%s", fullConfig.DiscoveredClockUnderTestPod.Name, err))
				}

				By("Checking event metrics are present")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", pkg.MetricsEndPoint})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpInterfaceRole),
					"Interface role metrics are not detected")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", pkg.MetricsEndPoint})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpThreshold),
					"Threshold metrics are not detected")
				// reset to v2
				ptphelper.EnablePTPEvent("2.0", fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
			})
		})

		Context("Running with reference plugin", func() {
			BeforeEach(func() {
				By("Enabling reference plugin", func() {
					Expect(ptphelper.EnablePTPReferencePlugin()).NotTo(HaveOccurred())
				})
			})
			AfterEach(func() {
				By("Disabling reference plugin", func() {
					Expect(ptphelper.DisablePTPReferencePlugin()).NotTo(HaveOccurred())
				})
			})
			XIt("Should check whether plugin is loaded", func() {
				By("checking for plugin logs")
				foundMatch := false
				for i := 0; i < 3 && !foundMatch; i++ {
					ptphelper.WaitForPtpDaemonToBeReady()
					ptpPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
					Expect(err).NotTo(HaveOccurred())
					Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")
					pluginLog := "Trying to register plugin: reference"
					for podIndex := range ptpPods.Items {
						_, err := pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							pluginLog, true, pkg.TimeoutIn3Minutes)
						if err != nil {
							logrus.Errorf(fmt.Sprintf("Reference plugin not loaded, err=%s", err))
							continue
						}
						foundMatch = true
					}
				}
				Expect(foundMatch).To(BeTrue())
			})
			XIt("Should check whether test plugin executes", func() {
				By("Find if required logs are found")
				Expect(ptphelper.EnablePTPReferencePlugin()).NotTo(HaveOccurred())
				pluginConfigExists := false
				pluginOpts := ""
				masterConfigs, slaveConfigs := ptphelper.DiscoveryPTPConfiguration(pkg.PtpLinuxDaemonNamespace)
				ptpConfigs := append(masterConfigs, slaveConfigs...)
				for _, config := range ptpConfigs {
					for _, profile := range config.Spec.Profile {
						if profile.Plugins != nil {
							for name, opts := range profile.Plugins {
								if name == "reference" {
									optsByteArray, _ := json.Marshal(opts)
									json.Unmarshal(optsByteArray, &pluginOpts)
									pluginConfigExists = true
								}
							}
						}
					}
				}
				if !pluginConfigExists {
					Skip("No plugin policies configured")
				}
				foundMatch := false
				for i := 0; i < 3 && !foundMatch; i++ {
					ptphelper.WaitForPtpDaemonToBeReady()
					ptpPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
					Expect(err).NotTo(HaveOccurred())
					Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")
					pluginLog := fmt.Sprintf("OnPTPConfigChangeGeneric: (%s)", pluginOpts)
					for podIndex := range ptpPods.Items {
						_, err := pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							pluginLog, true, pkg.TimeoutIn3Minutes)
						if err != nil {
							logrus.Errorf(fmt.Sprintf("Reference plugin not running OnPTPConfigChangeGeneric, err=%s", err))
							continue
						}
						foundMatch = true
					}
				}
				Expect(foundMatch).To(BeTrue())
			})
		})
		Context("Running with fifo scheduling", func() {
			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToBeReady()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}

				masterConfigs, slaveConfigs := ptphelper.DiscoveryPTPConfiguration(pkg.PtpLinuxDaemonNamespace)
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
				var err error
				ptpPods, err = client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
				Expect(err).NotTo(HaveOccurred())
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")
			})
			It("Should check whether using fifo scheduling", func() {
				By("checking for chrt logs")
				for name, priority := range fifoPriorities {
					ptp4lLog := fmt.Sprintf("/bin/chrt -f %d /usr/sbin/ptp4l", priority)
					for podIndex := range ptpPods.Items {
						profileName := fmt.Sprintf("Profile Name: %s", name)
						_, err := pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							profileName, true, pkg.TimeoutIn3Minutes)
						if err != nil {
							logrus.Errorf(fmt.Sprintf("error getting profile=%s, err=%s ", name, err))
							continue
						}
						_, err = pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							ptp4lLog, true, pkg.TimeoutIn3Minutes)
						if err != nil {
							logrus.Errorf(fmt.Sprintf("error getting ptp4l chrt line=%s, err=%s ", ptp4lLog, err))
							continue
						}
						delete(fifoPriorities, name)
					}
				}
				Expect(fifoPriorities).To(HaveLen(0))
			})
		})

		// old cnf-feature-deploy tests
		var _ = Describe("PTP socket sharing between pods", func() {
			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToBeReady()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				if fullConfig.PtpModeDesired == testconfig.Discovery {
					Skip("PTP socket test not supported in discovery mode")
				}
			})
			AfterEach(func() {
				err := namespaces.Clean(openshiftPtpNamespace, "testpod-", client.Client)
				Expect(err).ToNot(HaveOccurred())
			})
			var _ = Context("Negative - run pmc in a new unprivileged pod on the slave node", func() {
				It("Should not be able to use the uds", func() {
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, fullConfig.DiscoveredClockUnderTestPod.Spec.Containers[0].Name, []string{"pmc", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
						return buf.String()
					}, 1*time.Minute, 2*time.Second).ShouldNot(ContainSubstring("failed to open configuration file"), "ptp config file was not created")
					podDefinition := pods.DefinePodOnNode(pkg.PtpLinuxDaemonNamespace, fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
					hostPathDirectoryOrCreate := v1core.HostPathDirectoryOrCreate
					podDefinition.Spec.Volumes = []v1core.Volume{
						{
							Name: "socket-dir",
							VolumeSource: v1core.VolumeSource{
								HostPath: &v1core.HostPathVolumeSource{
									Path: "/var/run/ptp",
									Type: &hostPathDirectoryOrCreate,
								},
							},
						},
					}
					podDefinition.Spec.Containers[0].VolumeMounts = []v1core.VolumeMount{
						{
							Name:      "socket-dir",
							MountPath: "/var/run",
						},
						{
							Name:      "socket-dir",
							MountPath: "/host",
						},
					}
					pod, err := client.Client.Pods(pkg.PtpLinuxDaemonNamespace).Create(context.Background(), podDefinition, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())
					err = pods.WaitForCondition(client.Client, pod, v1core.ContainersReady, v1core.ConditionTrue, 3*time.Minute)
					Expect(err).ToNot(HaveOccurred())
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, pod, pod.Spec.Containers[0].Name, []string{"pmc", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
						return buf.String()
					}, 1*time.Minute, 2*time.Second).Should(ContainSubstring("Permission denied"), "unprivileged pod can access the uds socket")
				})
			})

			var _ = Context("Run pmc in a new pod on the slave node", func() {
				It("Should be able to sync using a uds", func() {

					Expect(fullConfig.DiscoveredClockUnderTestPod).ToNot(BeNil())
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, fullConfig.DiscoveredClockUnderTestPod.Spec.Containers[0].Name, []string{"pmc", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
						return buf.String()
					}, 1*time.Minute, 2*time.Second).ShouldNot(ContainSubstring("failed to open configuration file"), "ptp config file was not created")
					podDefinition, _ := pods.RedefineAsPrivileged(
						pods.DefinePodOnNode(pkg.PtpLinuxDaemonNamespace, fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName), "")
					hostPathDirectoryOrCreate := v1core.HostPathDirectoryOrCreate
					podDefinition.Spec.Volumes = []v1core.Volume{
						{
							Name: "socket-dir",
							VolumeSource: v1core.VolumeSource{
								HostPath: &v1core.HostPathVolumeSource{
									Path: "/var/run/ptp",
									Type: &hostPathDirectoryOrCreate,
								},
							},
						},
					}
					podDefinition.Spec.Containers[0].VolumeMounts = []v1core.VolumeMount{
						{
							Name:      "socket-dir",
							MountPath: "/var/run",
						},
						{
							Name:      "socket-dir",
							MountPath: "/host",
						},
					}
					pod, err := client.Client.Pods(pkg.PtpLinuxDaemonNamespace).Create(context.Background(), podDefinition, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())
					err = pods.WaitForCondition(client.Client, pod, v1core.ContainersReady, v1core.ConditionTrue, 3*time.Minute)
					Expect(err).ToNot(HaveOccurred())
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, pod, pod.Spec.Containers[0].Name, []string{"pmc", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
						return buf.String()
					}, 1*time.Minute, 2*time.Second).ShouldNot(ContainSubstring("failed to open configuration file"), "ptp config file is not shared between pods")

					Eventually(func() int {
						buf, _, _ := pods.ExecCommand(client.Client, pod, pod.Spec.Containers[0].Name, []string{"pmc", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
						return strings.Count(buf.String(), "offsetFromMaster")
					}, 3*time.Minute, 2*time.Second).Should(BeNumerically(">=", 1))
				})
			})
		})

		var _ = Describe("prometheus", func() {
			Context("Metrics reported by PTP pods", func() {
				It("Should all be reported by prometheus", func() {
					var err error
					ptpPods, err = client.Client.Pods(openshiftPtpNamespace).List(context.Background(), metav1.ListOptions{
						LabelSelector: "app=linuxptp-daemon",
					})
					Expect(err).ToNot(HaveOccurred())
					ptpMonitoredEntriesByPod, uniqueMetricKeys := collectPtpMetrics(ptpPods.Items)
					Eventually(func() error {
						podsPerPrometheusMetricKey := collectPrometheusMetrics(uniqueMetricKeys)
						return containSameMetrics(ptpMonitoredEntriesByPod, podsPerPrometheusMetricKey)
					}, 5*time.Minute, 2*time.Second).Should(Not(HaveOccurred()))

				})
			})
		})

		Context("PTP Outage recovery", func() {
			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToBeReady()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
			})

			It("The slave node network interface is taken down and up", func() {
				if fullConfig.PtpModeDiscovered == testconfig.TelcoGrandMasterClock {
					Skip("test not valid for WPC GM config")
				}
				By("toggling network interfaces and syncing", func() {
					skippedInterfacesStr, isSet := os.LookupEnv("SKIP_INTERFACES")

					if !isSet {
						Skip("Mandatory to provide skipped interface to avoid making a node disconnected from the cluster")
					} else {
						skipInterfaces := make(map[string]bool)
						separated := strings.Split(skippedInterfacesStr, ",")
						for _, val := range separated {
							skipInterfaces[val] = true
						}
						logrus.Info("skipINterfaces", skipInterfaces)
						ptptesthelper.RecoverySlaveNetworkOutage(fullConfig, skipInterfaces)
					}
				})
			})
		})

		Context("WPC GM Verification Tests", func() {
			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToBeReady()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
			})
			It("is verifying WPC GM state based on logs", func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for GM test config")
				}

				By("checking GM required processes status", func() {
					processesArr := [...]string{"phc2sys", "gpspipe", "ts2phc", "gpsd", "ptp4l", "dpll"}
					for _, val := range processesArr {
						logMatches, err := pods.GetPodLogsRegex(openshiftPtpNamespace, fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName, val, true, pkg.TimeoutIn1Minute)
						Expect(err).To(BeNil(), fmt.Sprintf("Error encountered looking for %s", val))
						Expect(logMatches).ToNot(BeEmpty(), fmt.Sprintf("Expected %s to be running for GM", val))
					}
				})

				By("checking clock class state is locked", func() {
					/*
						ptp4l[1726600400]:[ts2phc.0.config] CLOCK_CLASS_CHANGE 6
					*/
					clockClassPattern := `ptp4l(?m)\[.*?\]:\[(.*?)\] CLOCK_CLASS_CHANGE 6`
					clockClassRe := regexp.MustCompile(clockClassPattern)
					logMatches, err := pods.GetPodLogsRegex(openshiftPtpNamespace, fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName, clockClassRe.String(), false, pkg.TimeoutIn1Minute)
					Expect(err).To(BeNil(), "Error encountered looking for ptp4l clock class state")
					Expect(logMatches).NotTo(BeEmpty(), "Expected ptp4l clock class state to be Locked for GM")
					//TODO 2 Card Add loop to check ifaces

				})

				By("checking DPLL frequency and DPLL phase state to be locked", func() {
					/*
						dpll[1726600932]:[ts2phc.0.config] ens7f0 frequency_status 3 offset -1 phase_status 3 pps_status 1 s2
					*/
					dpllStatePattern := `dpll(?m).*?:\[(.*?)\] (.*?)frequency_status 3 offset (.*?) phase_status 3 pps_status (.*?) (.*?)`
					dpllStateRe := regexp.MustCompile(dpllStatePattern)
					logMatches, err := pods.GetPodLogsRegex(openshiftPtpNamespace, fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName, dpllStateRe.String(), false, pkg.TimeoutIn1Minute)
					Expect(err).To(BeNil(), "Error encountered looking for dpll frequency and phase state")
					Expect(logMatches).NotTo(BeEmpty(), "Expected dpll frequency and phase state to be locked for GM")
					//TODO 2 Card Add loop to check ifaces

				})

				By("checking GM clock state locked", func() {

					/*
						I0917 19:22:15.000310 2843504 event.go:430] dpll State s2, gnss State s2, tsphc state s2, gm state s2
						phc2sys[2355322.441]: [ptp4l.0.config:6] CLOCK_REALTIME phc offset       137 s2 freq   -7709 delay    514
					*/

					gmClockStatePattern := `(?m).*?dpll State s2, gnss State s2, tsphc state s2, gm state s2,`
					gmClockStateRe := regexp.MustCompile(gmClockStatePattern)
					logMatches, err := pods.GetPodLogsRegex(openshiftPtpNamespace, fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName, gmClockStateRe.String(), false, pkg.TimeoutIn1Minute)
					Expect(err).To(BeNil(), "Error encountered looking for dpll, gnss,ts2phc and GM clock state")
					Expect(logMatches).NotTo(BeEmpty(), "Expected dpll, gnss,ts2phc and GM clock state to be locked for GM")

					phc2sysPattern := `phc2sys(?m).*?: \[(.*?)\] CLOCK_REALTIME phc offset[ \t]+(.*?) s2 (.*?)`
					phc2sysRe := regexp.MustCompile(phc2sysPattern)
					logMatches, err = pods.GetPodLogsRegex(openshiftPtpNamespace, fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName, phc2sysRe.String(), false, pkg.TimeoutIn1Minute)
					Expect(err).To(BeNil(), "Error encountered looking for phc2sys clock state")
					Expect(logMatches).NotTo(BeEmpty(), "Expected phc2sys clock state to be locked for GM")
					//TODO 2 Card Add loop to check ifaces

				})

				By("checking PTP NMEA status for ts2phc", func() {
					/*
						# ts2phc[1726600506]:[ts2phc.0.config] ens7f0 nmea_status 1 offset 0 s2
					*/
					nmeaStatusPattern := `ts2phc(?m).*?:\[(.*?)\] (.*?) nmea_status 1 offset (.*?) (.*?)`
					nmeaStatusRe := regexp.MustCompile(nmeaStatusPattern)
					logMatches, err := pods.GetPodLogsRegex(openshiftPtpNamespace, fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName, nmeaStatusRe.String(), false, pkg.TimeoutIn1Minute)
					Expect(err).To(BeNil(), "Error encountered looking for phc2sys clock state")
					Expect(logMatches).NotTo(BeEmpty(), "Expected ts2phc nmea state to be available for GM")
					//TODO 2 Card Add loop to check ifaces
				})
			})

			//TODO: Use Focus Flag to separate GM tests from other tests
			It("is verifying WPC GM state based on metrics", func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for GM test config")
				}
				By("checking GM required processes status", func() {
					/*
						# TYPE openshift_ptp_process_status gauge
						openshift_ptp_process_status{config="ptp4l.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="phc2sys"} 1
						openshift_ptp_process_status{config="ptp4l.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="ptp4l"} 1
						openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="gpsd"} 1
						openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="gpspipe"}
						openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="gpspipe"} 1
						openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="ts2phc"} 1
					*/
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						return buf.String()
					}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpProcessStatus),
						"Process status metrics are not detected")

					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						return buf.String()
					}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("phc2sys"),
						"phc2ys process status not detected")

					time.Sleep(10 * time.Second)
					buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
					ret, err := processRunning(buf.String())
					Expect(err).To(BeNil())
					Expect(ret["phc2sys"]).To(BeTrue(), "Expected phc2sys to be running for GM")
					Expect(ret["gpspipe"]).To(BeTrue(), "Expected gpspipe to be running for GM")
					Expect(ret["ts2phc"]).To(BeTrue(), "Expected ts2phc to be running for GM")
					Expect(ret["gpsd"]).To(BeTrue(), "Expected gpsd to be running for GM")
					Expect(ret["ptp4l"]).To(BeTrue(), "Expected ptp4l to be running for GM")
					time.Sleep(1 * time.Minute)
				})

				By("checking clock class state is locked", func() {
					/*
						# TYPE openshift_ptp_clock_class gauge
						# openshift_ptp_clock_class{node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="ptp4l"} 6
					*/
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						return buf.String()
					}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpClockClass),
						"NMEA status metrics are not detected")
					buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
					clockClassPattern := `openshift_ptp_clock_class\{node="([^"]+)",process="([^"]+)"\} (\d+)`

					// Compile the regular expression
					clockClassRe := regexp.MustCompile(clockClassPattern)

					// Find matches
					clockClassMap := map[string]bool{"ptp4l": false}

					scanner := bufio.NewScanner(strings.NewReader(buf.String()))
					timeout := 10 * time.Second
					start := time.Now()
					for scanner.Scan() {
						t := time.Now()
						elapsed := t.Sub(start)
						if elapsed > timeout {
							Fail("Timedout reading input from metrics")
						}
						line := scanner.Text()
						if matches := clockClassRe.FindStringSubmatch(line); matches != nil {
							if _, ok := clockClassMap[matches[2]]; ok && matches[3] == "6" {
								clockClassMap[matches[2]] = true
								break
							}

						}
					}
					if err := scanner.Err(); err != nil {
						Fail(fmt.Sprintf("Error reading input: %s", err))
					}
					Expect(clockClassMap["ptp4l"]).To(BeTrue(), "Expected ptp4l clock class state to be Locked for GM")
				})

				By("checking DPLL frequency state locked", func() {
					/*
						# TODO: Revisit this for 2 card as each card will have its own dpll process
						# TYPE openshift_ptp_frequency_status gauge
						# openshift_ptp_frequency_status{from="dpll",iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 3
					*/
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						return buf.String()
					}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpFrequencyStatus),
						"frequency status metrics are not detected")

					buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
					freqStatusPattern := `openshift_ptp_frequency_status\{from="([^"]+)",iface="([^"]+)",node="([^"]+)",process="([^"]+)"\} (\d+)`

					// Compile the regular expression
					freqStatusRe := regexp.MustCompile(freqStatusPattern)

					// Find matches
					freqStatusMap := map[string]bool{"dpll": false}

					scanner := bufio.NewScanner(strings.NewReader(buf.String()))
					timeout := 10 * time.Second
					start := time.Now()
					for scanner.Scan() {
						t := time.Now()
						elapsed := t.Sub(start)
						if elapsed > timeout {
							Fail("Timedout reading input from metrics")
						}
						line := scanner.Text()
						if matches := freqStatusRe.FindStringSubmatch(line); matches != nil {
							if _, ok := freqStatusMap[matches[4]]; ok && matches[5] == "3" {
								freqStatusMap[matches[4]] = true
								break
							}

						}
					}
					if err := scanner.Err(); err != nil {
						Fail(fmt.Sprintf("Error reading input from metrics: %s", err))
					}
					Expect(freqStatusMap["dpll"]).To(BeTrue(), "Expected dpll frequency status to be Locked for GM")

				})

				By("checking DPLL phase state locked", func() {
					/*
						# TODO: Revisit this for 2 card as each card will have its own dpll process
						# TYPE openshift_ptp_phase_status gauge
						# openshift_ptp_phase_status{from="dpll",iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 3
					*/
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						return buf.String()
					}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpPhaseStatus),
						"frequency status metrics are not detected")

					buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
					phaseStatusPattern := `openshift_ptp_phase_status\{from="([^"]+)",iface="([^"]+)",node="([^"]+)",process="([^"]+)"\} (\d+)`

					// Compile the regular expression
					phaseStatusRe := regexp.MustCompile(phaseStatusPattern)

					// Find matches
					phaseStatusMap := map[string]bool{"dpll": false}

					scanner := bufio.NewScanner(strings.NewReader(buf.String()))
					timeout := 10 * time.Second
					start := time.Now()
					for scanner.Scan() {
						t := time.Now()
						elapsed := t.Sub(start)
						if elapsed > timeout {
							Fail("Timedout reading input from metrics")
						}
						line := scanner.Text()
						if matches := phaseStatusRe.FindStringSubmatch(line); matches != nil {
							if _, ok := phaseStatusMap[matches[4]]; ok && matches[5] == "3" {
								phaseStatusMap[matches[4]] = true
								break
							}

						}
					}
					if err := scanner.Err(); err != nil {
						Fail(fmt.Sprintf("Error reading input: %s", err))
					}
					Expect(phaseStatusMap["dpll"]).To(BeTrue(), "Expected dpll phase status to be Locked for GM")

				})

				By("checking GM clock state locked", func() {
					/*
						# TODO: Revisit this for 2 card as each card will have its own dpll and ts2phc processes
						# TYPE openshift_ptp_clock_state gauge
						openshift_ptp_clock_state{iface="CLOCK_REALTIME",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="phc2sys"} 1
						openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="GM"} 1
						openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 1
						openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="gnss"} 1
						openshift_ptp_clock_state{iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="ts2phc"} 1
					*/
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						return buf.String()
					}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpClockState),
						"Clock state metrics are not detected")

					buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
					ret, err := clockStateByProcesses(buf.String())
					Expect(err).To(BeNil())
					Expect(ret["phc2sys"]).To(BeTrue(), "Expected phc2sys clock state to be locked for GM")
					Expect(ret["GM"]).To(BeTrue(), "Expected GM clock state to be locked for GM")
					Expect(ret["dpll"]).To(BeTrue(), "Expected dpll clock state to be locked for GM")
					Expect(ret["ts2phc"]).To(BeTrue(), "Expected ts2phc clock state to be locked for GM")
					Expect(ret["gnss"]).To(BeTrue(), "Expected gnss clock state to be locked for GM")

				})

				By("checking PTP NMEA status for ts2phc", func() {
					/*
						# TYPE openshift_ptp_nmea_status gauge
						# openshift_ptp_nmea_status{from="ts2phc",iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="ts2phc"} 1
					*/
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						return buf.String()
					}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpNMEAStatus),
						"NMEA status metrics are not detected")
					buf, _, _ := pods.ExecCommand(client.Client, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
					nmeaStatusPattern := `openshift_ptp_nmea_status\{from="([^"]+)",iface="([^"]+)",node="([^"]+)",process="([^"]+)"\} (\d+)`

					// Compile the regular expression
					nmeaStatusRe := regexp.MustCompile(nmeaStatusPattern)

					// Find matches
					processNmeaState := map[string]bool{"ts2phc": false}

					scanner := bufio.NewScanner(strings.NewReader(buf.String()))
					timeout := 10 * time.Second
					start := time.Now()
					for scanner.Scan() {
						t := time.Now()
						elapsed := t.Sub(start)
						if elapsed > timeout {
							Fail("Timedout reading input from metrics")
						}
						line := scanner.Text()
						if matches := nmeaStatusRe.FindStringSubmatch(line); matches != nil {
							if _, ok := processNmeaState[matches[4]]; ok && matches[5] == "1" {
								processNmeaState[matches[4]] = true
								break
							}

						}
					}
					if err := scanner.Err(); err != nil {
						Fail(fmt.Sprintf("Error reading input from metrics: %s", err))
					}
					Expect(processNmeaState["ts2phc"]).To(BeTrue(), "Expected ts2phc nmea state to be available for GM")
				})
			})

		})
	})
})

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

func processRunning(input string) (map[string]bool, error) {
	// Regular expression pattern
	processStatusPattern := `openshift_ptp_process_status\{config="([^"]+)",node="([^"]+)",process="([^"]+)"\} (\d+)`

	// Compile the regular expression
	processStatusRe := regexp.MustCompile(processStatusPattern)

	// Find matches
	processRunning := map[string]bool{"phc2sys": false, "ptp4l": false, "ts2phc": false, "gpspipe": false, "gpsd": false}

	scanner := bufio.NewScanner(strings.NewReader(input))
	timeout := 10 * time.Second
	start := time.Now()
	for scanner.Scan() {
		t := time.Now()
		elapsed := t.Sub(start)
		if elapsed > timeout {
			fmt.Println("Timed out when reading metrics")
			break
		}
		line := scanner.Text()
		if matches := processStatusRe.FindStringSubmatch(line); matches != nil {
			if _, ok := processRunning[matches[3]]; ok && matches[4] == "1" {
				processRunning[matches[3]] = true
			}

		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Println("Error reading input:", err)
		return nil, err
	}
	return processRunning, nil
}

func clockStateByProcesses(input string) (map[string]bool, error) {
	// Regular expression pattern
	clockStatePattern := `openshift_ptp_clock_state\{iface="([^"]+)",node="([^"]+)",process="([^"]+)"\} (\d+)`

	// Compile the regular expression
	processStatusRe := regexp.MustCompile(clockStatePattern)

	// Find matches
	processClockState := map[string]bool{"phc2sys": false, "GM": false, "dpll": false, "ts2phc": false, "gnss": false}

	scanner := bufio.NewScanner(strings.NewReader(input))
	timeout := 10 * time.Second
	start := time.Now()
	for scanner.Scan() {
		t := time.Now()
		elapsed := t.Sub(start)
		if elapsed > timeout {
			fmt.Println("Timed out when reading metrics")
			break
		}
		line := scanner.Text()
		if matches := processStatusRe.FindStringSubmatch(line); matches != nil {
			if _, ok := processClockState[matches[3]]; ok && matches[4] == "1" {
				processClockState[matches[3]] = true
			}

		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Println("Error reading input:", err)
		return nil, err
	}
	return processClockState, nil
}

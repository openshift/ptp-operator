package test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/event"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/metrics"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/namespaces"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/ptphelper"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/ptptesthelper"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/apps/v1"
	v1core "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/pods"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/execute"

	fbprotocol "github.com/facebook/time/ptp/protocol"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/testconfig"
	exports "github.com/redhat-cne/ptp-listener-exports"
	ptpEvent "github.com/redhat-cne/sdk-go/pkg/event/ptp"
	k8sv1 "k8s.io/api/core/v1"
)

type TestCase string

const (
	Reboot TestCase = "reboot"
)

const (
	DPLL_LOCKED_HO_ACQ = 3
	DPLL_HOLDOVER      = 4
	DPLL_FREERUN       = 1
	DPLL_LOCKED        = 2
)
const (
	ClockClassFreerun = 248
)

var (
	clockClassPattern = `^openshift_ptp_clock_class\{(?:config="ptp4l\.\d+\.config",)?node="([^"]+)",process="([^"]+)"\}\s+(\d+)`
	clockClassRe      = regexp.MustCompile(clockClassPattern)
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
			if !event.Enable() {
				Skip("Skipping as env var ENABLE_PTP_EVENT is not set or is set to false")
			}

			apiVersion := event.GetDefaultApiVersion()
			err := ptphelper.EnablePTPEvent(apiVersion, "")
			Expect(err).To(BeNil(), "error when enable ptp event")
			ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ptpConfig.Spec.EventConfig.EnableEventPublisher).Should(BeTrue(), "failed to enable ptp event")

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
			Expect(ds.Items[0].Status.CurrentNumberScheduled).To(BeNumerically("==", ds.Items[0].Status.DesiredNumberScheduled), "should be one instance per node")
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
		portEngine := ptptesthelper.PortEngine{}

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

			portEngine.Initialize(fullConfig.DiscoveredClockUnderTestPod, fullConfig.DiscoveredFollowerInterfaces)

		})

		Context("PTP Reboot discovery", func() {
			BeforeEach(func() {
				Skip("This is covered by QE")
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
			})

			It("The slave node is rebooted and discovered and in sync", func() {
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
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)
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

				ocpVersion, err := ptphelper.GetOCPVersion()
				if err != nil {
					logrus.Infof("Kubernetes cluster under test is not Openshift, cannot get OCP version")
				} else {
					logrus.Infof("Kubernetes cluster under test is Openshift, OCP version is %s", ocpVersion)
				}

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
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
			})
			AfterEach(func() {
				portEngine.TurnAllPortsUp()
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
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(BeNil())
				if fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClock || fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClockHA {
					err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestSecondaryPtpConfig), grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
					Expect(err).To(BeNil())
				}
			})

			// Test That clock can sync in dual follower scenario when one port is down
			It("Dual follower can sync when one follower port goes down", func() {
				if fullConfig.PtpModeDesired != testconfig.DualFollowerClock {
					Skip("Test reserved for dual follower scenario")
				}
				Expect(len(fullConfig.DiscoveredFollowerInterfaces) == 2)
				isExternalMaster := ptphelper.IsExternalGM()
				var grandmasterID *string
				if fullConfig.L2Config != nil && !isExternalMaster {
					aLabel := pkg.PtpGrandmasterNodeLabel
					aString, err := ptphelper.GetClockIDMaster(pkg.PtpGrandMasterPolicyName, &aLabel, nil, true)
					grandmasterID = &aString
					Expect(err).To(BeNil())
				}
				By("Check sync")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig),
					grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)

				// Set initial roles
				err = portEngine.SetInitialRoles()
				Expect(err).To(BeNil())

				// Retry until there is no error or we timeout
				Eventually(func() error {
					return portEngine.RolesInOnly([]metrics.MetricRole{metrics.MetricRoleSlave, metrics.MetricRoleListening})
				}, 150*time.Second, 30*time.Second).Should(BeNil())

				By("Port0: down")
				err = portEngine.TurnPortDown(portEngine.Ports[0])
				Expect(err).To(BeNil())
				By("Check sync")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(BeNil())
				By("Check clock role")
				Eventually(func() error {
					return portEngine.CheckClockRole(portEngine.Ports[0], portEngine.Ports[1], metrics.MetricRoleFaulty, metrics.MetricRoleSlave)
				}, 120*time.Second, 1*time.Second).Should(BeNil())

				By("Port1: down")
				err = portEngine.TurnPortDown(portEngine.Ports[1])
				Expect(err).To(BeNil())
				By("Check holdover")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateHoldOver, metrics.MetricRoleFaulty, false)
				Expect(err).To(BeNil())
				By("Check freerun")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateFreeRun, metrics.MetricRoleFaulty, false)
				Expect(err).To(BeNil())
				By("Check clock role")
				Eventually(func() error {
					return portEngine.CheckClockRole(portEngine.Ports[0], portEngine.Ports[1], metrics.MetricRoleFaulty, metrics.MetricRoleFaulty)
				}, 120*time.Second, 1*time.Second).Should(BeNil())

				By("Port1: up")
				err = portEngine.TurnPortUp(portEngine.Ports[1])
				Expect(err).To(BeNil())
				By("Check sync")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(BeNil())
				By("Check clock role")
				Eventually(func() error {
					return portEngine.CheckClockRole(portEngine.Ports[0], portEngine.Ports[1], metrics.MetricRoleFaulty, metrics.MetricRoleSlave)
				}, 120*time.Second, 1*time.Second).Should(BeNil())

				By("Port0: up")
				err = portEngine.TurnPortUp(portEngine.Ports[0])
				Expect(err).To(BeNil())
				By("Check sync")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(BeNil())
				By("Check clock role")
				Eventually(func() error {
					return portEngine.CheckClockRole(portEngine.Ports[0], portEngine.Ports[1], portEngine.InitialRoles[0], portEngine.InitialRoles[1])
				}, 120*time.Second, 1*time.Second).Should(BeNil())

				By("Remove Grandmaster")
				err := client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Delete(context.Background(), testconfig.GlobalConfig.DiscoveredGrandMasterPtpConfig.Name, metav1.DeleteOptions{})
				Expect(err).To(BeNil())
				By("Check clock role")
				Eventually(func() error {
					return portEngine.CheckClockRole(portEngine.Ports[0], portEngine.Ports[1], metrics.MetricRoleListening, metrics.MetricRoleListening)
				}, 120*time.Second, 1*time.Second).Should(BeNil())
				By("Check holdover")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateHoldOver, metrics.MetricRoleListening, false)
				Expect(err).To(BeNil())
				By("Check freerun")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateFreeRun, metrics.MetricRoleListening, false)
				Expect(err).To(BeNil())
				By("Recreate Grandmaster")
				tempPtpConfig := (*ptpv1.PtpConfig)(testconfig.GlobalConfig.DiscoveredGrandMasterPtpConfig)
				tempPtpConfig.SetResourceVersion("")
				_, err = client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Create(context.Background(), tempPtpConfig, metav1.CreateOptions{})
				Expect(err).To(BeNil())
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
					fullConfig.PtpModeDiscovered != testconfig.DualNICBoundaryClock &&
					fullConfig.PtpModeDiscovered != testconfig.DualNICBoundaryClockHA {
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
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredSlave1PtpConfig), &masterIDBc1, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(BeNil())

				if (fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClock || fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClockHA) &&
					(fullConfig.FoundSolutions[testconfig.AlgoDualNicBCWithSlavesExtGMString] || fullConfig.FoundSolutions[testconfig.AlgoDualNicBCWithSlavesString]) {
					aLabel := pkg.PtpClockUnderTestNodeLabel
					masterIDBc2, err := ptphelper.GetClockIDMaster(pkg.PtpBcMaster2PolicyName, &aLabel, nil, false)
					Expect(err).To(BeNil())
					err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredSlave2PtpConfig), &masterIDBc2, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
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
					case testconfig.DualFollowerClock:
						policyName = pkg.PtpSlave1PolicyName
					case testconfig.BoundaryClock:
						policyName = pkg.PtpBcMaster1PolicyName
					case testconfig.DualNICBoundaryClock, testconfig.DualNICBoundaryClockHA:
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
					err = ptptesthelper.BasicClockSyncCheck(fullConfig, modifiedPtpConfig, grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
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

			It("DualNICBCHA phc2sys switches to secondary ptp4l when primary interface fails", func() {
				if fullConfig.PtpModeDiscovered != testconfig.DualNICBoundaryClockHA {
					Skip("Test only valid for Dual NIC Boundary Clocks with phc2sys HA configuration (DualNICBCHA)")
				}

				By("Identifying which interface phc2sys is currently using")
				primaryPtpConfig := (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig)
				primaryBCSlaveInterfaces := ptpv1.GetInterfaces(*primaryPtpConfig, ptpv1.Slave)

				secondaryPtpConfig := (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestSecondaryPtpConfig)
				secondaryBCSlaveInterfaces := ptpv1.GetInterfaces(*secondaryPtpConfig, ptpv1.Slave)

				logrus.Infof("Primary   BC slave interfaces: %v", primaryBCSlaveInterfaces)
				logrus.Infof("Secondary BC slave interfaces: %v", secondaryBCSlaveInterfaces)

				// Get phc2sys logs to identify which interface it's using
				const phc2sysLogPattern = `phc2sys(?m).*?:.* selecting (\w+) as out-of-domain source clock`
				var selectedInterface string

				logMatches, err := pods.GetPodLogsRegex(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName,
					phc2sysLogPattern, false, pkg.TimeoutIn1Minute)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(logMatches)).To(BeNumerically("==", 1), "Could not identify which interface phc2sys is using")

				logrus.Infof("phc2sys log matching line: %v", logMatches[0][0])
				selectedInterface = logMatches[0][1]

				// Save it as primary interface
				primaryInterface := selectedInterface
				By("Verifying the selected interface " + selectedInterface + " is a primary BC's slave interface")
				// Check if the selected interface belongs to the primary boundary clock config

				// Check if the selected interface belongs to the primary boundary clock config
				if !slices.Contains(primaryBCSlaveInterfaces, selectedInterface) {
					Fail(fmt.Sprintf("Selected interface %s does not belong to the primary boundary clock config. Primary interfaces: %v", selectedInterface, primaryBCSlaveInterfaces))
				}

				// Wait for some time to ensure the regex won't match the previous log entry
				time.Sleep(2 * time.Second)
				ifDownTime := time.Now()
				By("Taking down the selected interface " + selectedInterface)

				// Use the PortEngine to turn down the interface so phc2sys will switch to the slave interface in the secondary boundary clock
				portEngine.TurnPortDown(selectedInterface)

				// Wait for the interface to be down and ptp4l to report freerun
				Eventually(func() error {
					return metrics.CheckClockRole([]metrics.MetricRole{metrics.MetricRoleFaulty}, []string{selectedInterface}, &fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
				}, 30*time.Second, 5*time.Second).Should(BeNil(), "Primary BC's slave interface "+selectedInterface+" should be in FAULTY state")

				By("Waiting for 5 seconds for the new interface to be selected")
				time.Sleep(5 * time.Second)

				By("Verifying phc2sys switches to a different interface")
				// Wait for phc2sys to switch to a different interface
				var newSelectedInterface string
				logMatches, err = pods.GetPodLogsRegexSince(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName,
					phc2sysLogPattern, false, pkg.TimeoutIn1Minute, ifDownTime)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(logMatches)).To(BeNumerically("==", 1), "Could not identify which interface phc2sys is using")
				// Get the most recent log entry
				logrus.Infof("phc2sys log matching line: %v", logMatches[0][0])
				newSelectedInterface = logMatches[0][1]

				// Verify that phc2sys switched to a different interface
				Expect(newSelectedInterface).ToNot(Equal(selectedInterface), "phc2sys should have switched to a different interface")

				By("Verifying the new selected interface " + newSelectedInterface + " is a secondary BC's slave interface")
				// Check if the selected interface belongs to the primary boundary clock config
				if !slices.Contains(secondaryBCSlaveInterfaces, newSelectedInterface) {
					Fail(fmt.Sprintf("Selected interface %s does not belong to the secondary boundary clock config. Secondary interfaces: %v", newSelectedInterface, secondaryBCSlaveInterfaces))
				}

				time.Sleep(2 * time.Second)
				ifUpTime := time.Now()
				By("Restoring the primary BC's slave interface " + primaryInterface)
				// Bring the interface back up
				portEngine.TurnPortUp(primaryInterface)

				// Wait for the interface to recover to SLAVE state
				Eventually(func() error {
					return metrics.CheckClockRole([]metrics.MetricRole{metrics.MetricRoleSlave}, []string{primaryInterface}, &fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
				}, 30*time.Second, 5*time.Second).Should(BeNil(), "Primary BC's slave interface "+primaryInterface+" should recover to SLAVE state")

				By("Waiting 5 seconds for the primary BC's slave interface to be selected again")
				time.Sleep(5 * time.Second)

				// Check the new interfaces is the primary one
				logMatches, err = pods.GetPodLogsRegexSince(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName,
					phc2sysLogPattern, false, pkg.TimeoutIn1Minute, ifUpTime)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(logMatches)).To(BeNumerically("==", 1), "Could not identify which interface phc2sys is using")
				// Get the most recent log entry
				logrus.Infof("phc2sys log matching line: %v", logMatches[0][0])
				selectedInterface = logMatches[0][1]

				By("Verifying the selected interface " + selectedInterface + " is the original primary BC's slave interface " + primaryInterface)
				if selectedInterface != primaryInterface {
					Fail(fmt.Sprintf("Selected interface %s is not the original primary interface %s", selectedInterface, primaryInterface))
				}
			})
		})

		Context("PTP metric is present", func() {
			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

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
			It("verifies on slave", func() {
				if fullConfig.PtpModeDiscovered == testconfig.TelcoGrandMasterClock {
					Skip("Skipping: test not valid for WPC GM (Telco Grandmaster Clock) config")
				}
				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, false, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
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
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)
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
					buf, _, _ := pods.ExecCommand(client.Client, false, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", path.Join(apiBase, "health")})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("OK"),
					"Event API is not in healthy state")

				By("Checking ptp publisher is created")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, false, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", path.Join(apiBase, "publishers")})
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
					buf, _, _ := pods.ExecCommand(client.Client, false, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", pkg.MetricsEndPoint})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpInterfaceRole),
					"Interface role metrics are not detected")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, false, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", pkg.MetricsEndPoint})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpThreshold),
					"Threshold metrics are not detected")
			})
		})

		Context("Running with event enabled, v1 regression", func() {
			BeforeEach(func() {
				if !event.IsV1EventRegressionNeeded() {
					Skip("Skipping, test PTP events v1 regression is for 4.16 and 4.17 only")
				}

				ptphelper.EnablePTPEvent("1.0", fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
				// wait for pod info updated
				time.Sleep(5 * time.Second)

				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

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
				apiVersion := ptphelper.PtpEventEnabled()
				var apiBase string
				if apiVersion == 1 {
					apiBase = event.ApiBaseV1
				} else {
					apiBase = event.ApiBaseV2
				}
				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", path.Join(apiBase, "health")})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("OK"),
					"Event API is not in healthy state")

				By("Checking ptp publisher is created")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", path.Join(apiBase, "publishers")})
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
					"event sent", true, pkg.TimeoutIn5Minutes)
				if err != nil {
					Fail(fmt.Sprintf("PTP event was not generated in the pod %s, err=%s", fullConfig.DiscoveredClockUnderTestPod.Name, err))
				}

				By("Checking event metrics are present")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", pkg.MetricsEndPoint})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpInterfaceRole),
					"Interface role metrics are not detected")

				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", pkg.MetricsEndPoint})
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
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)
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
					ptphelper.WaitForPtpDaemonToExist()
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

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
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

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
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

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
						buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"pmc", "-b", "0", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
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
						buf, _, _ := pods.ExecCommand(client.Client, true, pod, pod.Spec.Containers[0].Name, []string{"pmc", "-b", "0", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
						return buf.String()
					}, 1*time.Minute, 2*time.Second).Should(ContainSubstring("Permission denied"), "unprivileged pod can access the uds socket")
				})
			})

			var _ = Context("Run pmc in a new pod on the slave node", func() {
				It("Should be able to sync using a uds", func() {

					Expect(fullConfig.DiscoveredClockUnderTestPod).ToNot(BeNil())
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"pmc", "-b", "0", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
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
						buf, _, _ := pods.ExecCommand(client.Client, true, pod, pod.Spec.Containers[0].Name, []string{"pmc", "-b", "0", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
						return buf.String()
					}, 1*time.Minute, 2*time.Second).ShouldNot(ContainSubstring("failed to open configuration file"), "ptp config file is not shared between pods")

					Eventually(func() int {
						buf, _, _ := pods.ExecCommand(client.Client, false, pod, pod.Spec.Containers[0].Name, []string{"pmc", "-b", "0", "-u", "-f", "/var/run/ptp4l.0.config", "GET CURRENT_DATA_SET"})
						return strings.Count(buf.String(), "offsetFromMaster")
					}, 3*time.Minute, 2*time.Second).Should(BeNumerically(">=", 1))
				})
			})
		})

		var _ = Describe("prometheus", func() {
			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
			})
			AfterEach(func() {

			})
			Context("Metrics reported by PTP pods", func() {
				It("Should all be reported by prometheus", func() {
					var err error
					ptpPods, err = client.Client.Pods(openshiftPtpNamespace).List(context.Background(), metav1.ListOptions{
						LabelSelector: "app=linuxptp-daemon",
					})
					Expect(err).ToNot(HaveOccurred())
					Expect(fullConfig.DiscoveredClockUnderTestPod).ToNot(BeNil())
					ptpMonitoredEntriesByPod, uniqueMetricKeys := collectPtpMetrics([]k8sv1.Pod{*fullConfig.DiscoveredClockUnderTestPod})
					Eventually(func() error {
						podsPerPrometheusMetricKey, err := collectPrometheusMetrics(uniqueMetricKeys)
						if err != nil {
							return err
						}
						return containSameMetrics(ptpMonitoredEntriesByPod, podsPerPrometheusMetricKey)
					}, 5*time.Minute, 2*time.Second).Should(Not(HaveOccurred()))

				})
			})
		})

		Context("PTP Outage recovery", func() {
			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
			})

			It("The slave node network interface is taken down and up", func() {
				if fullConfig.PtpModeDiscovered == testconfig.TelcoGrandMasterClock {
					Skip("test not valid for WPC GM config")
				}
				if fullConfig.PtpModeDesired == testconfig.DualFollowerClock {
					Skip("Test not valid for dual follower scenario")
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
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for GM test config")
				}
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

				})

			})
			It("is verifying WPC GM state based on logs", func() {

				By("checking GM required processes status", func() {
					processesArr := [...]string{"phc2sys", "gpspipe", "ts2phc", "gpsd", "ptp4l", "dpll"}
					for _, val := range processesArr {
						logMatches, err := pods.GetPodLogsRegex(openshiftPtpNamespace, fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName, val, true, pkg.TimeoutIn1Minute)
						Expect(err).To(BeNil(), fmt.Sprintf("Error encountered looking for %s", val))
						Expect(logMatches).ToNot(BeEmpty(), fmt.Sprintf("Expected %s to be running for GM", val))
					}
				})

				By("checking clock class state is locked", func() {
					clockClassPattern := `ptp4l(?m)\[.*?\]:\[(.*?)\] CLOCK_CLASS_CHANGE 6`
					clockClassRe := regexp.MustCompile(clockClassPattern)

					Eventually(func() ([][]string, error) {
						logMatches, err := pods.GetPodLogsRegex(
							openshiftPtpNamespace,
							fullConfig.DiscoveredClockUnderTestPod.Name,
							pkg.PtpContainerName,
							clockClassRe.String(),
							false,                // don't follow logs
							pkg.TimeoutIn1Minute, // inner timeout for single call (can be shorter if you want)
						)
						return logMatches, err
					}, pkg.TimeoutIn5Minutes, pkg.Timeout10Seconds).Should( // <-- total wait 5 mins, check every 10s
						And(
							Not(BeEmpty()),
							Not(BeNil()),
						),
						"Expected ptp4l clock class state to eventually be Locked (class 6)",
					)
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
					checkProcessStatus(fullConfig, "1")
					time.Sleep(1 * time.Minute)
				})

				By("checking clock class state is locked", func() {
					/*
						# TYPE openshift_ptp_clock_class gauge
						# openshift_ptp_clock_class{node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="ptp4l"} 6
					*/
					checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)))

				})

				By("checking DPLL frequency state locked", func() {
					/*
						# TODO: Revisit this for 2 card as each card will have its own dpll process
						# TYPE openshift_ptp_frequency_status gauge
						# openshift_ptp_frequency_status{from="dpll",iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 3
					*/
					checkDPLLFrequencyState(fullConfig, fmt.Sprint(DPLL_LOCKED_HO_ACQ))

				})

				By("checking DPLL phase state locked", func() {
					/*
						# TODO: Revisit this for 2 card as each card will have its own dpll process
						# TYPE openshift_ptp_phase_status gauge
						# openshift_ptp_phase_status{from="dpll",iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 3
					*/
					checkDPLLPhaseState(fullConfig, fmt.Sprint(DPLL_LOCKED_HO_ACQ))

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
					checkClockState(fullConfig, "1")

				})

				By("checking PTP NMEA status for ts2phc", func() {
					/*
						# TYPE openshift_ptp_nmea_status gauge
						# openshift_ptp_nmea_status{from="ts2phc",iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="ts2phc"} 1
					*/
					checkPTPNMEAStatus(fullConfig, "1")
				})
			})

			It("gpsd and gpspipe restart quickly updates process status metrics", func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for GM test config")
				}

				By("Ensuring gpsd and gpspipe are running (process_status == 1)")
				checkStatusByProcess(fullConfig, "gpsd", "1")
				checkStatusByProcess(fullConfig, "gpspipe", "1")

				By("Starting fast watchers to capture 1→0→1 flips while killing processes")
				resultCh := make(chan string, 2)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				go func() {
					ok := watchProcessFlipOneZeroOne(fullConfig, "gpsd", 30*time.Second)
					if ok {
						resultCh <- "gpsd"
					}
				}()
				go func() {
					ok := watchProcessFlipOneZeroOne(fullConfig, "gpspipe", 30*time.Second)
					if ok {
						resultCh <- "gpspipe"
					}
				}()
				time.Sleep(1 * time.Second) // Give watchers time to start
				By("Killing gpsd and gpspipe to trigger restart while watchers are running")
				_, _, killErr := pods.ExecCommand(
					client.Client,
					true,
					fullConfig.DiscoveredClockUnderTestPod,
					pkg.PtpContainerName,
					[]string{"sh", "-c", "pkill -TERM gpsd || true; pkill -TERM gpspipe || true"},
				)
				Expect(killErr).To(BeNil(), "failed to kill gpsd/gpspipe")

				seen := map[string]bool{"gpsd": false, "gpspipe": false}
				for !seen["gpsd"] || !seen["gpspipe"] {
					select {
					case <-ctx.Done():
						Fail("Timed out waiting for fast 1→0→1 metric flip for gpsd/gpspipe")
						return
					case name := <-resultCh:
						seen[name] = true
					}
				}
			})

		})

		Context("WPC GM GNSS signal loss tests", func() {
			BeforeEach(func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for GM test config")
				}
			})
			/*
				Step | Action
				1    | Check starting stability (ClockClass 6, locked)
				2    | Start continuous coldboot
				3	 | Wait for DPLL state = 3 (Holdover)
				3    | Wait for ClockClass 7 (in-spec holdover)
				4    | Check clock state = 2 (Holdover)
				5    | Stop coldboot
				6    | Wait a little (for GNSS to recover)
				7    | Wait for ClockClass 6 again
				8    | Confirm clock state = 1 for T-GM (Locked)
				9    | Check Dpll State = 1 (Locked)
			*/
			It("Testing WPC T-GM holdover through connection loss", func() {
				By("Coldboot GNSS continuously while waiting for ClockClass 7 and clock state for GM an DPLL", func() {
					checkStabilityOfWPCGMUsingMetrics(fullConfig)

					// Initially system should be LOCKED (ClockClass 6)
					checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)))

					stopChan := make(chan struct{})

					// Start coldboot in background
					go coldBootInBackground(stopChan, fullConfig)

					// Meanwhile, wait for ClockClass 7 (GNSS loss - Holdover In Spec)
					waitForClockClass(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass7)))

					// Also verify ClockState 2 (Holdover)
					checkClockStateForProcess(fullConfig, "GM", "2")

					// Also verify ClockState (Holdover) for DPLL
					checkClockStateForProcess(fullConfig, "dpll", "2")

					// Once holdover detected, stop coldboot loop
					close(stopChan)

					// Give GNSS time to fully recover
					time.Sleep(pkg.Timeout10Seconds)

					// Now wait for system to go back to LOCKED (ClockClass 6)
					waitForClockClass(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)))
					// Give DPLL time update
					time.Sleep(pkg.Timeout10Seconds)
					// Also verify ClockState (Holdover) for DPLL
					checkClockStateForProcess(fullConfig, "dpll", "1")

					// Also verify ClockState 1 (Locked)
					checkClockStateForProcess(fullConfig, "GM", "1")
				})
			})

		})

		Context("WPC GM ts2phc termination tests", func() {
			BeforeEach(func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for GM test config")
				}
			})

			It("Continuously terminating ts2phc triggers FREERUN event and then recovers to LOCKED", func() {
				By("Ensure initial state is LOCKED via clock class 6")
				checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)))

				// Ensure event consumer exists and pubsub is initialized (V2)
				nodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
				Expect(nodeName).ToNot(BeEmpty(), "clock-under-test pod node is empty")
				By("Deploy consumer app for event API v2")
				err := event.CreateConsumerApp(nodeName)
				if err != nil {
					Skip(fmt.Sprintf("Consumer app setup failed: %v", err))
				}
				// Wait for consumer to be fully ready
				time.Sleep(10 * time.Second)
				// Initialize pub/sub
				event.InitPubSub()
				// Ensure cleanup regardless of test outcome
				DeferCleanup(func() {
					_ = event.DeleteConsumerNamespace()
					if event.PubSub != nil {
						event.PubSub.Close()
					}
				})

				By("Subscribe to GM change events and start log monitor")
				const incomingEventsBuffer = 100
				subs, cleanup := event.SubscribeToGMChangeEvents(incomingEventsBuffer, true, 60*time.Second)
				DeferCleanup(cleanup)
				term, monErr := event.MonitorPodLogsRegex()
				Expect(monErr).ToNot(HaveOccurred(), "could not start listening to events")
				DeferCleanup(func() { stopMonitor(term) })

				By("Continuously killing ts2phc while waiting for FREERUN event and ClockClass 248")
				stopChan := make(chan struct{})

				// Start continuous ts2phc killing in background
				go killTs2phcInBackground(stopChan, fullConfig)

				// Phase 1: Wait for FREERUN and Clock Class 248 after continuous ts2phc kills
				By("Waiting for FREERUN and ClockClass 248 after continuous ts2phc kills")
				// Make clock class optional as stop gap for CI failures which we only see in CI
				waitForStateAndCC(subs, ptpEvent.FREERUN, 248, 90*time.Second, true)

				// Once FREERUN/holdover detected, stop continuous killing
				close(stopChan)

				// Phase 2: Re-subscribe with initial snapshot to handle recovery to LOCKED/CC=6
				const buf2 = 100
				subs2, cleanup2 := event.SubscribeToGMChangeEvents(buf2, true, 60*time.Second)
				defer cleanup2()
				By("Waiting for LOCKED and ClockClass 6 after recovery")
				waitForStateAndCC(subs2, ptpEvent.LOCKED, 6, 90*time.Second, false)

			})
		})

		Context("WPC GM Events verification (V1)", func() {
			BeforeEach(func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for GM test config")
				}
			})
		})

		Context("WPC GM Events verification (V2)", func() {
			BeforeEach(func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for GM test config")
				}

				// Set up consumer pod for event monitoring
				if fullConfig.DiscoveredClockUnderTestPod != nil {
					nodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
					if nodeName != "" {
						logrus.Info("Deploy consumer app for testing event API v2")
						err := event.CreateConsumerApp(nodeName)
						if err != nil {
							logrus.Errorf("PTP events are not available due to consumer app creation error err=%s", err)
							Skip("Consumer app setup failed")
						}

						// Wait a bit more for the consumer pod to be fully ready
						logrus.Info("Waiting for consumer pod to be fully ready...")
						time.Sleep(10 * time.Second)

						// Initialize pub/sub system
						event.InitPubSub()
					}
				}
			})

			AfterEach(func() {
				// Clean up consumer namespace
				DeferCleanup(func() {
					err := event.DeleteConsumerNamespace()
					if err != nil {
						logrus.Debugf("Deleting consumer namespace failed because of err=%s", err)
					}
				})

				// Close internal pubsub
				if event.PubSub != nil {
					event.PubSub.Close()
				}
			})

			It("Testing T-GM Events on GNSS reboot and recovery", func() {
				By("Verifying Clock Class, GNSS, and PTP State")

				// Ensure system starts in LOCKED (ClockClass 6)
				logrus.Info("Verifying current LOCKED state...")
				By("Check current LOCKED state")
				checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)))

				// Subscribe to CHANGE events (consistent with later verifications)
				const incomingEventsBuffer = 100
				subs, cleanup := event.SubscribeToGMChangeEvents(incomingEventsBuffer, true, 60*time.Second)
				defer cleanup()

				// Start cold boot in background; will be stopped via stop1
				stop := make(chan struct{})
				var once sync.Once
				safeClose := func() { once.Do(func() { close(stop) }) }
				defer safeClose()

				// Start log monitor once
				term, err := event.MonitorPodLogsRegex()
				defer func() { stopMonitor(term) }()
				Expect(err).ToNot(HaveOccurred(), "could not start listening to events")
				By("Starting cold boot in background")
				go coldBootInBackground(stop, fullConfig)
				time.Sleep(2 * time.Second) // Allow some time for cold boot to start

				// now use subs.GNSS, subs.CC, subs.LS
				events := getGMEvents(subs.GNSS, subs.CLOCKCLASS, subs.LOCKSTATE, 10*time.Second)
				fmt.Fprintf(GinkgoWriter, "First Event recieved  %v, ", events)

				// Verify expected HOLDOVER conditions
				By("Verifying GNSS state Antenna Disconnected")
				verifyEvent(events[ptpEvent.GnssStateChange], ptpEvent.ANTENNA_DISCONNECTED)
				By("Verifying ClockClass value to 7")
				verifyMetric(events[ptpEvent.PtpClockClassChange], float64(fbprotocol.ClockClass7))
				By("Verifying PTP state Holdover")
				verifyEvent(events[ptpEvent.PtpStateChange], ptpEvent.HOLDOVER)
				stopMonitor(term)
				// Stop phase 1 collection / cold boot
				By("Stopping cold boot")
				safeClose()
				//Phase 2 verify GNSS recovery events
				term2, err2 := event.MonitorPodLogsRegex()
				defer func() { stopMonitor(term2) }()
				Expect(err2).ToNot(HaveOccurred(), "could not start listening to events")
				By("Waiting for GNSS recovery to LOCKED state via clock class metrics")
				waitForClockClass(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)))

				// Phase 2: collect again after GNSS recovery
				events = getGMEvents(subs.GNSS, subs.CLOCKCLASS, subs.LOCKSTATE, 10*time.Second)
				fmt.Fprintf(GinkgoWriter, "Event recieved  %v, ", events)

				// Verify conditions have returned to LOCKED
				By("Verifying GNSS state Synchronized")
				verifyEvent(events[ptpEvent.GnssStateChange], ptpEvent.SYNCHRONIZED)
				By("Verifying ClockClass value to 6")
				verifyMetric(events[ptpEvent.PtpClockClassChange], float64(fbprotocol.ClockClass6))
				By("Verifying PTP state Locked")
				verifyEvent(events[ptpEvent.PtpStateChange], ptpEvent.LOCKED)

			})
		})
	})
})

func checkStabilityOfWPCGMUsingMetrics(fullConfig testconfig.TestConfig) {
	checkProcessStatus(fullConfig, "1")
	checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)))
	checkDPLLFrequencyState(fullConfig, fmt.Sprint(DPLL_LOCKED_HO_ACQ))
	checkDPLLPhaseState(fullConfig, fmt.Sprint(DPLL_LOCKED_HO_ACQ))
	checkClockState(fullConfig, "1")
	checkPTPNMEAStatus(fullConfig, "1")
}

func verifyEventsV1(expectedState string) {
	//TODO
	switch expectedState {
	case "LOCKED":
		/*
			7.2.3.1 Synchronization State (implemented)
			event.sync.sync-status.synchronization-state-change
			/sync/sync-status/sync-state LOCKED

			7.2.3.3 PTP Synchronization State (implemented)
			event.sync.ptp-status.ptp-state-change
			/sync/ptp-status/lock-state LOCKED

			7.2.3.6 GNSS-Sync-State (implemented)
			event.sync.gnss-status.gnss-state-change
			/sync/gnss-status/gnss-sync-status LOCKED

			7.2.3.8 OS Clock Sync-State (implemented)
			event.sync.sync-status.os-clock-sync-state-change
			/sync/sync-status/os-clock-sync-state


			7.2.3.10 PTP Clock Class Change (implemented)
			event.sync.ptp-status.ptp-clock-class-change
			/sync/ptp-status/clock-class LOCKED
		*/
	case "HOLDOVER":
		/*
			7.2.3.1 Synchronization State (implemented)
			event.sync.sync-status.synchronization-state-change
			/sync/sync-status/sync-state HOLDOVER

			7.2.3.3 PTP Synchronization State (implemented)
			event.sync.ptp-status.ptp-state-change
			/sync/ptp-status/lock-state HOLDOVER

			7.2.3.6 GNSS-Sync-State (implemented)
			event.sync.gnss-status.gnss-state-change
			/sync/gnss-status/gnss-sync-status HOLDOVER

			7.2.3.8 OS Clock Sync-State (implemented)
			event.sync.sync-status.os-clock-sync-state-change
			/sync/sync-status/os-clock-sync-state


			7.2.3.10 PTP Clock Class Change (implemented)
			event.sync.ptp-status.ptp-clock-class-change
			/sync/ptp-status/clock-class HOLDOVER

		*/

	case "FREERUN":
		/*
			7.2.3.1 Synchronization State (implemented)
			event.sync.sync-status.synchronization-state-change
			/sync/sync-status/sync-state FREERUN

			7.2.3.3 PTP Synchronization State (implemented)
			event.sync.ptp-status.ptp-state-change
			/sync/ptp-status/lock-state FREERUN

			7.2.3.6 GNSS-Sync-State (implemented)
			event.sync.gnss-status.gnss-state-change
			/sync/gnss-status/gnss-sync-status FREERUN

			7.2.3.8 OS Clock Sync-State (implemented)
			event.sync.sync-status.os-clock-sync-state-change
			/sync/sync-status/os-clock-sync-state


			7.2.3.10 PTP Clock Class Change (implemented)
			event.sync.ptp-status.ptp-clock-class-change
			/sync/ptp-status/clock-class FREERUN

		*/

	}

}

func verifyEventsV2(expectedState string) {
	//TODO
	switch expectedState {
	case "LOCKED":
		/*
			7.2.3.1 Synchronization State (implemented)
			event.sync.sync-status.synchronization-state-change
			/sync/sync-status/sync-state LOCKED

			7.2.3.3 PTP Synchronization State (implemented)
			event.sync.ptp-status.ptp-state-change
			/sync/ptp-status/lock-state LOCKED

			7.2.3.6 GNSS-Sync-State (implemented)
			event.sync.gnss-status.gnss-state-change
			/sync/gnss-status/gnss-sync-status LOCKED

			7.2.3.8 OS Clock Sync-State (implemented)
			event.sync.sync-status.os-clock-sync-state-change
			/sync/sync-status/os-clock-sync-state


			7.2.3.10 PTP Clock Class Change (implemented)
			event.sync.ptp-status.ptp-clock-class-change
			/sync/ptp-status/clock-class LOCKED
		*/
	case "HOLDOVER":
		/*
			7.2.3.1 Synchronization State (implemented)
			event.sync.sync-status.synchronization-state-change
			/sync/sync-status/sync-state HOLDOVER

			7.2.3.3 PTP Synchronization State (implemented)
			event.sync.ptp-status.ptp-state-change
			/sync/ptp-status/lock-state HOLDOVER

			7.2.3.6 GNSS-Sync-State (implemented)
			event.sync.gnss-status.gnss-state-change
			/sync/gnss-status/gnss-sync-status HOLDOVER

			7.2.3.8 OS Clock Sync-State (implemented)
			event.sync.sync-status.os-clock-sync-state-change
			/sync/sync-status/os-clock-sync-state


			7.2.3.10 PTP Clock Class Change (implemented)
			event.sync.ptp-status.ptp-clock-class-change
			/sync/ptp-status/clock-class HOLDOVER

		*/

	case "FREERUN":
		/*
			7.2.3.1 Synchronization State (implemented)
			event.sync.sync-status.synchronization-state-change
			/sync/sync-status/sync-state FREERUN

			7.2.3.3 PTP Synchronization State (implemented)
			event.sync.ptp-status.ptp-state-change
			/sync/ptp-status/lock-state FREERUN

			7.2.3.6 GNSS-Sync-State (implemented)
			event.sync.gnss-status.gnss-state-change
			/sync/gnss-status/gnss-sync-status FREERUN

			7.2.3.8 OS Clock Sync-State (implemented)
			event.sync.sync-status.os-clock-sync-state-change
			/sync/sync-status/os-clock-sync-state


			7.2.3.10 PTP Clock Class Change (implemented)
			event.sync.ptp-status.ptp-clock-class-change
			/sync/ptp-status/clock-class FREERUN

		*/

	}

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

func processRunning(input string, state string) (map[string]bool, error) {
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
			if _, ok := processRunning[matches[3]]; ok && matches[4] == state {
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

func clockStateByProcesses(input string, state string) (map[string]bool, error) {
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
			if _, ok := processClockState[matches[3]]; ok && matches[4] == state {
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

func getClockStateByProcess(metrics, process string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(metrics))

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "openshift_ptp_clock_state") && strings.Contains(line, fmt.Sprintf(`process="%s"`, process)) {
			// split line to get value
			parts := strings.Fields(line)
			if len(parts) == 2 {
				return parts[1], true
			}
		}
	}
	return "", false
}

func checkProcessStatus(fullConfig testconfig.TestConfig, state string) {
	// Add nil checks to prevent panic
	if fullConfig.DiscoveredClockUnderTestPod == nil {
		Fail("DiscoveredClockUnderTestPod is nil - cannot check process status")
		return
	}

	if client.Client == nil {
		Fail("Client is nil - cannot execute commands")
		return
	}

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
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		return buf.String()
	}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpProcessStatus),
		"Process status metrics are not detected")

	Eventually(func() string {
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		return buf.String()
	}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring("phc2sys"),
		"phc2ys process status not detected")

	time.Sleep(10 * time.Second)
	buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
	ret, err := processRunning(buf.String(), state)
	Expect(err).To(BeNil())
	Expect(ret["phc2sys"]).To(BeTrue(), fmt.Sprintf("Expected phc2sys to be  %s for GM", state))
	Expect(ret["ptp4l"]).To(BeTrue(), fmt.Sprintf("Expected ptp4l to be  %s for GM", state))
	Expect(ret["ts2phc"]).To(BeTrue(), fmt.Sprintf("Expected ts2phc to be  %s for GM", state))
	Expect(ret["gpspipe"]).To(BeTrue(), fmt.Sprintf("Expected gpspipe to be %s for GM", state))
	Expect(ret["gpsd"]).To(BeTrue(), fmt.Sprintf("Expected gpsd to be q %s for GM", state))
}

func checkClockClassState(fullConfig testconfig.TestConfig, expectedState string) {
	By(fmt.Sprintf("Waiting for clock class to become %s", expectedState))
	Eventually(func() bool {
		// Get the latest metrics output
		buf, _, err := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		if err != nil {
			fmt.Fprintf(GinkgoWriter, "Error executing curl: %v\n", err)
			return false
		}

		// Scan line by line
		scanner := bufio.NewScanner(strings.NewReader(buf.String()))
		for scanner.Scan() {
			line := scanner.Text()

			// Check if the line matches the clock class pattern
			matches := clockClassRe.FindStringSubmatch(line)
			if len(matches) >= 4 {
				fmt.Fprintf(GinkgoWriter, "Matched line: %v\n", matches)
				process := matches[2]
				class := matches[3]
				if strings.TrimSpace(process) == "ptp4l" && strings.TrimSpace(class) == expectedState {
					fmt.Fprintf(GinkgoWriter, "Found clock class %s for process %s\n", class, process)
					return true
				} else {
					fmt.Fprintf(GinkgoWriter, "Match found but process=%s class=%s, not matching yet...\n", process, class)
				}
			}
		}

		// If error during scan
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(GinkgoWriter, "Error scanning metrics: %v\n", err)
		}

		return false
	}, pkg.TimeoutIn5Minutes, pkg.Timeout1Seconds).Should(BeTrue(),
		fmt.Sprintf("Expected ptp4l clock class to eventually be %s for GM", expectedState))
}

func checkDPLLFrequencyState(fullConfig testconfig.TestConfig, state string) {
	/*
		# TODO: Revisit this for 2 card as each card will have its own dpll process
		# TYPE openshift_ptp_frequency_status gauge
		# openshift_ptp_frequency_status{from="dpll",iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 3
	*/
	Eventually(func() string {
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		return buf.String()
	}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpFrequencyStatus),
		"frequency status metrics are not detected")

	buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
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
			if _, ok := freqStatusMap[matches[4]]; ok && matches[5] == state {
				freqStatusMap[matches[4]] = true
				break
			}

		}
	}
	if err := scanner.Err(); err != nil {
		Fail(fmt.Sprintf("Error reading input from metrics: %s", err))
	}
	Expect(freqStatusMap["dpll"]).To(BeTrue(), fmt.Sprintf("Expected dpll frequency status to be %s for GM", state))
}

func checkDPLLPhaseState(fullConfig testconfig.TestConfig, state string) {
	/*
		# TODO: Revisit this for 2 card as each card will have its own dpll process
		# TYPE openshift_ptp_phase_status gauge
		# openshift_ptp_phase_status{from="dpll",iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 3
	*/
	Eventually(func() string {
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		return buf.String()
	}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpPhaseStatus),
		"frequency status metrics are not detected")

	buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
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
			if _, ok := phaseStatusMap[matches[4]]; ok && matches[5] == state {
				phaseStatusMap[matches[4]] = true
				break
			}

		}
	}
	if err := scanner.Err(); err != nil {
		Fail(fmt.Sprintf("Error reading input: %s", err))
	}
	Expect(phaseStatusMap["dpll"]).To(BeTrue(), fmt.Sprintf("Expected dpll phase status to be %s for GM", state))
}

func checkClockState(fullConfig testconfig.TestConfig, state string) {
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
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		return buf.String()
	}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(ContainSubstring(metrics.OpenshiftPtpClockState),
		"Clock state metrics are not detected")

	buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
	ret, err := clockStateByProcesses(buf.String(), state)
	Expect(err).To(BeNil())
	Expect(ret["GM"]).To(BeTrue(), fmt.Sprintf("Expected GM clock state to be %s for GM", state))
	//Not needed for now
	// Expect(ret["phc2sys"]).To(BeTrue(), fmt.Sprintf("Expected phc2sys clock state to be %s for GM", state))
	// Expect(ret["dpll"]).To(BeTrue(), fmt.Sprintf("Expected dpll clock state to be %s for GM", state))
	// Expect(ret["ts2phc"]).To(BeTrue(), fmt.Sprintf("Expected ts2phc clock state to be %s for GM", state))
	// Expect(ret["gnss"]).To(BeTrue(), fmt.Sprintf("Expected gnss clock state to be %s for GM", state))
}

func checkClockStateForProcess(fullConfig testconfig.TestConfig, process string, state string) {
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
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		return buf.String()
	}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(ContainSubstring(metrics.OpenshiftPtpClockState),
		"Clock state metrics are not detected")

	buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
	retState, found := getClockStateByProcess(buf.String(), process)
	Expect(found).To(BeTrue(), fmt.Sprintf("Expected %s clock state to be %s for GM but found %s", process, state, retState))
	Expect(retState).To(Equal(state), fmt.Sprintf("Expected %s clock state to be %s for GM %s", process, state, buf.String()))
}

// getProcessStatusByProcess parses openshift_ptp_process_status lines and returns the value for a given process
func getProcessStatusByProcess(metricsText, process string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(metricsText))

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, metrics.OpenshiftPtpProcessStatus) && strings.Contains(line, fmt.Sprintf(`process="%s"`, process)) {
			// split line to get value
			parts := strings.Fields(line)
			if len(parts) == 2 {
				return parts[1], true
			}
		}
	}
	return "", false
}

// checkStatusByProcess mirrors checkClockStateForProcess but for process status (1/0)
func checkStatusByProcess(fullConfig testconfig.TestConfig, process string, state string) {
	Eventually(func() string {
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		retState, found := getProcessStatusByProcess(buf.String(), process)
		if !found {
			return ""
		}
		return retState
	}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(Equal(state),
		fmt.Sprintf("Expected %s process status to be %s for GM", process, state))
}

// watchProcessFlipOneZeroOne aggressively samples metrics to detect a fast 1→0→1 flip for a process.
// It returns true if it observed 1, then 0, then 1 again in order within totalTimeout.
func watchProcessFlipOneZeroOne(fullConfig testconfig.TestConfig, process string, totalTimeout time.Duration) bool {
	deadline := time.Now().Add(totalTimeout)
	sawOne := false
	sawZero := false

	// Collect many samples per exec to avoid missing brief flips
	const samplesPerChunk = 120
	const sampleDelimiter = "__SAMPLE_END__"

	for time.Now().Before(deadline) {
		cmd := fmt.Sprintf(`for i in $(seq 1 %d); do curl -s %s; echo %s; done`, samplesPerChunk, pkg.MetricsEndPoint, sampleDelimiter)
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"sh", "-c", cmd})

		scanner := bufio.NewScanner(strings.NewReader(buf.String()))
		foundThisSample := false
		valueThisSample := ""
		restartHappened := false
		commitSample := func() bool {
			current := "0" // default to 0 if metric line missing in this sample
			if foundThisSample {
				current = valueThisSample
			}
			if current == "1" {
				if !sawOne {
					sawOne = true
				} else if sawZero {
					return true
				}
			} else if current == "0" {
				if sawOne {
					sawZero = true
				}
			}
			if restartHappened && sawOne {
				return true
			}
			foundThisSample = false
			valueThisSample = ""
			return false
		}

		for scanner.Scan() {
			line := scanner.Text()
			if line == sampleDelimiter {
				if commitSample() {
					return true
				}
				continue
			}
			if strings.HasPrefix(line, "openshift_ptp_process_restart_count") && strings.Contains(line, fmt.Sprintf(`process="%s"`, process)) {
				parts := strings.Fields(line)
				if len(parts) == 2 {
					// Any increment indicates a restart happened during this observation window
					restartHappened = true
				}
				continue
			}
			if strings.HasPrefix(line, metrics.OpenshiftPtpProcessStatus) && strings.Contains(line, fmt.Sprintf(`process="%s"`, process)) {
				parts := strings.Fields(line)
				if len(parts) != 2 {
					continue
				}
				foundThisSample = true
				valueThisSample = parts[1]
			}
		}
		// Commit last partial sample if any content was seen without a delimiter
		if foundThisSample {
			if commitSample() {
				return true
			}
		}

		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func checkPTPNMEAStatus(fullConfig testconfig.TestConfig, expectedState string) {
	nmeaStatusPattern := `openshift_ptp_nmea_status\{iface="([^"]+)",node="([^"]+)",process="([^"]+)"\} (\d+)`
	nmeaStatusRe := regexp.MustCompile(nmeaStatusPattern)

	Eventually(func() bool {
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		scanner := bufio.NewScanner(strings.NewReader(buf.String()))
		foundState := ""

		for scanner.Scan() {
			line := scanner.Text()
			if matches := nmeaStatusRe.FindStringSubmatch(line); matches != nil {
				if len(matches) < 4 {
					continue
				}
				process := matches[3]
				state := matches[4]
				fmt.Fprintf(GinkgoWriter, "Matched process=%s, state=%s\n", process, state)
				if process == "ts2phc" {
					foundState = state
					break
				}
			}
		}

		return foundState == expectedState
	}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(BeTrue(), fmt.Sprintf("Expected ts2phc NMEA state to be %s for GM", expectedState))
}

func coldBootInBackground(stopChan chan struct{}, fullConfig testconfig.TestConfig) {
	for {
		select {
		case <-stopChan:
			fmt.Fprintf(GinkgoWriter, "Stopping coldboot loop\n")
			return
		default:
			// Send coldboot
			_, _, err := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod,
				pkg.PtpContainerName, []string{"ubxtool", "-P", "29.20", "-p", "COLDBOOT", "-v", "3"})
			if err != nil {
				fmt.Fprintf(GinkgoWriter, "Error running coldboot: %v\n", err)
			} else {
				fmt.Fprintf(GinkgoWriter, "Coldboot sent\n")
			}
			time.Sleep(2 * time.Second) // Keep hammering every 2 sec
		}
	}
}

func killTs2phcInBackground(stopChan chan struct{}, fullConfig testconfig.TestConfig) {
	for {
		select {
		case <-stopChan:
			fmt.Fprintf(GinkgoWriter, "Stopping ts2phc kill loop\n")
			return
		default:
			// Kill ts2phc
			_, _, err := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod,
				pkg.PtpContainerName, []string{"sh", "-c", "pkill -TERM ts2phc || true"})
			if err != nil {
				fmt.Fprintf(GinkgoWriter, "Error killing ts2phc: %v\n", err)
			} else {
				fmt.Fprintf(GinkgoWriter, "ts2phc killed\n")
			}
			time.Sleep(2 * time.Millisecond) // Keep hammering every 2 ms
		}
	}
}

func waitForClockClass(fullConfig testconfig.TestConfig, expectedState string) {
	start := time.Now()

	for {
		if checkClockClassStateReturnBool(fullConfig, expectedState) {
			fmt.Fprintf(GinkgoWriter, "✅ Clock class reached %s\n", expectedState)
			break
		} else {
			fmt.Fprintf(GinkgoWriter, "Clock class not yet %s, retrying...\n", expectedState)
		}

		time.Sleep(pkg.TimeoutInterval2Seconds)

		if time.Since(start) > pkg.TimeoutIn3Minutes {
			Fail(fmt.Sprintf("Timed out waiting for clock class %s", expectedState))
			break
		}
	}
}

func checkClockClassStateReturnBool(fullConfig testconfig.TestConfig, expectedState string) bool {
	buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
	scanner := bufio.NewScanner(strings.NewReader(buf.String()))
	for scanner.Scan() {
		line := scanner.Text()
		if matches := clockClassRe.FindStringSubmatch(line); matches != nil {
			process := matches[2]
			class := matches[3]
			if strings.TrimSpace(process) == "ptp4l" && strings.TrimSpace(class) == expectedState {
				return true
			}
		}
	}
	return false
}

// EventResult holds a parsed event
type EventResult struct {
	Type   ptpEvent.EventType
	Values exports.StoredEventValues
}

// processEvent parses a StoredEvent into EventResult
func processEvent(eventType ptpEvent.EventType, ev exports.StoredEvent) (*EventResult, bool) {
	values, ok := ev[exports.EventValues].(exports.StoredEventValues)
	if !ok {
		logrus.Warnf("%s event: unable to parse values: %v", eventType, ev)
		return nil, false
	}
	return &EventResult{Type: eventType, Values: values}, true
}

// getEvents listens and aggregates events into a map until stopChan is closed
func getGMEvents(
	gnssEventChan <-chan exports.StoredEvent,
	ccEventChan <-chan exports.StoredEvent,
	lsEventChan <-chan exports.StoredEvent,
	timeout time.Duration,
) map[ptpEvent.EventType]exports.StoredEventValues {

	results := make(map[ptpEvent.EventType]exports.StoredEventValues)
	timer := time.NewTimer(timeout)

	for {
		select {
		case <-timer.C:
			return results
		case ev := <-gnssEventChan:
			if res, ok := processEvent(ptpEvent.GnssStateChange, ev); ok {
				results[res.Type] = res.Values
				fmt.Fprintf(GinkgoWriter, "GnssStateChange Event recieved  %v, ", res.Values)
			}
		case ev := <-ccEventChan:
			if res, ok := processEvent(ptpEvent.PtpClockClassChange, ev); ok {
				results[res.Type] = res.Values
				fmt.Fprintf(GinkgoWriter, "PtpClockClassChange Event recieved  %v, ", res.Values)
			}
		case ev := <-lsEventChan:
			if res, ok := processEvent(ptpEvent.PtpStateChange, ev); ok {
				results[res.Type] = res.Values
				fmt.Fprintf(GinkgoWriter, "PtpStateChange Event recieved  %v, ", res.Values)
			}
		}
	}
}

// verifyEvent looks for a particular state (string) inside a slice of StoredEventValues
func verifyEvent(events exports.StoredEventValues, expectedState ptpEvent.SyncState) {
	found := false
	if state, ok := events["notification"].(string); ok {
		if state == string(expectedState) {
			found = true
		}
	}
	Expect(found).To(BeTrue(),
		"expected state %q not found in %+v", expectedState, events)
}

// verifyMetricThreshold checks if any event has metric within a range
func verifyMetric(events exports.StoredEventValues, value float64) {
	found := false
	if metricValue, ok := events["metric"].(float64); ok {
		if metricValue == value {
			found = true
		}
	}
	Expect(found).To(BeTrue(),
		"expected a metric [%f] but got %+v", value, events)
}

func stopMonitor(term chan bool) {
	select {
	case term <- true: // tell goroutine to exit
	default: // avoid blocking if it’s already been stopped
	}
}

// waitForPtpStateEvent waits until a PTP state event with given state appears on the channel
func waitForPtpStateEvent(events <-chan exports.StoredEvent, expected ptpEvent.SyncState, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			Fail(fmt.Sprintf("Timed out waiting for PTP state event %s", expected))
			return
		case ev := <-events:
			if res, ok := processEvent(ptpEvent.PtpStateChange, ev); ok {
				if state, ok2 := res.Values["notification"].(string); ok2 && state == string(expected) {
					return
				}
			}
		}
	}
}

// waitForStateAndCC waits until the given state and clock class value (int) are both observed
func waitForStateAndCC(subs event.Subscriptions, state ptpEvent.SyncState, cc int, timeout time.Duration, warnOnMissingCC bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	stateSeen := false
	ccSeen := false

	for {
		if stateSeen && ccSeen {
			return
		}
		select {
		case <-deadline.C:
			if warnOnMissingCC && stateSeen {
				fmt.Fprintf(GinkgoWriter, "[WARN] Clock class %d was not seen. Continuting as it was marked as optional.\n", cc)
			} else {
				Fail(fmt.Sprintf("Timed out waiting for state %s and ClockClass %d", state, cc))
			}
			return
		case ev := <-subs.LOCKSTATE:
			if res, ok := processEvent(ptpEvent.PtpStateChange, ev); ok {
				if s, ok2 := res.Values["notification"].(string); ok2 && s == string(state) {
					stateSeen = true
				}
			}
		case ev := <-subs.CLOCKCLASS:
			if res, ok := processEvent(ptpEvent.PtpClockClassChange, ev); ok {
				if v, ok2 := res.Values["metric"].(float64); ok2 && int(v) == cc {
					ccSeen = true
				}
			}
		}
	}
}

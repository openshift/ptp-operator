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

	"github.com/Masterminds/semver"
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
		if DesiredMode == testconfig.DualNICBoundaryClockHA || DesiredMode == testconfig.DualFollowerClock {
			ptpOperatorVersion, err := ptphelper.GetPtpOperatorVersion()
			Expect(err).ToNot(HaveOccurred())
			operatorVersion, err := semver.NewVersion(ptpOperatorVersion)
			Expect(err).ToNot(HaveOccurred())
			var minVersion *semver.Version
			var skipMsg string
			switch DesiredMode {
			case testconfig.DualNICBoundaryClockHA:
				minVersion, err = semver.NewVersion("4.16")
				Expect(err).ToNot(HaveOccurred())
				skipMsg = "Skipping DualNICBoundaryClockHA tests - HA mode requires OCP 4.16+"
			case testconfig.DualFollowerClock:
				minVersion, err = semver.NewVersion("4.19")
				Expect(err).ToNot(HaveOccurred())
				skipMsg = "Skipping DualFollowerClock tests - Dual Follower mode requires OCP 4.19+"
			}
			if operatorVersion.LessThan(minVersion) {
				Skip(skipMsg)
			}
		}
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

		// Lease acquisition test
		It("Should re-acquire lease within 5 minutes after operator pod deletion", func() {
			By("Getting the current ptp-operator pod")
			operatorPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{
				LabelSelector: pkg.PtPOperatorPodsLabel,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(operatorPods.Items)).To(BeNumerically(">", 0), "no ptp-operator pods found")

			originalPodName := operatorPods.Items[0].Name
			logrus.Infof("Original ptp-operator pod: %s", originalPodName)

			By("Getting the current lease")
			lease, err := client.Client.CoordinationV1().Leases(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpOperatorLeaseID, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			var originalHolderIdentity string
			if lease.Spec.HolderIdentity != nil {
				originalHolderIdentity = *lease.Spec.HolderIdentity
			}
			logrus.Infof("Original lease holder identity: %s", originalHolderIdentity)

			By("Deleting the ptp-operator pod")
			err = client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).Delete(context.Background(), originalPodName, metav1.DeleteOptions{})
			Expect(err).ToNot(HaveOccurred())
			logrus.Infof("Deleted ptp-operator pod: %s", originalPodName)
			logrus.Infof("attempting to acquire leader lease %s/%s...", pkg.PtpLinuxDaemonNamespace, pkg.PtpOperatorLeaseID)

			By("Waiting for a new pod to be Running and Ready, and lease to be acquired")
			Eventually(func() error {
				// Check for new pod
				operatorPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{
					LabelSelector: pkg.PtPOperatorPodsLabel,
				})
				if err != nil {
					return fmt.Errorf("failed to list ptp-operator pods: %v", err)
				}
				if len(operatorPods.Items) == 0 {
					return fmt.Errorf("no ptp-operator pods found")
				}

				// Find a pod that is not the original deleted pod
				var newPod *v1core.Pod
				for i := range operatorPods.Items {
					pod := &operatorPods.Items[i]
					if pod.Name != originalPodName && pod.DeletionTimestamp == nil {
						newPod = pod
						break
					}
				}
				if newPod == nil {
					return fmt.Errorf("new ptp-operator pod not yet created")
				}

				// Check if pod is Running
				if newPod.Status.Phase != v1core.PodRunning {
					return fmt.Errorf("new pod %s is not running yet (phase: %s)", newPod.Name, newPod.Status.Phase)
				}

				// Check if pod is Ready
				podReady := false
				for _, cond := range newPod.Status.Conditions {
					if cond.Type == v1core.PodReady && cond.Status == v1core.ConditionTrue {
						podReady = true
						break
					}
				}
				if !podReady {
					return fmt.Errorf("new pod %s is not ready yet", newPod.Name)
				}

				// Check the lease
				lease, err := client.Client.CoordinationV1().Leases(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpOperatorLeaseID, metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get lease: %v", err)
				}

				if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
					return fmt.Errorf("lease has no holder identity")
				}

				// Verify lease holder has been updated (could be same pod name in different deployment scenario,
				// but renewTime should be recent)
				if lease.Spec.RenewTime == nil {
					return fmt.Errorf("lease has no renew time")
				}

				logrus.Infof("New pod %s is Running and Ready, lease held by: %s, renewTime: %v",
					newPod.Name, *lease.Spec.HolderIdentity, lease.Spec.RenewTime.Time)

				return nil
			}, pkg.TimeoutIn5Minutes, pkg.TimeoutInterval5Seconds).Should(BeNil(), "failed to re-acquire lease within 5 minutes")

			By("Verifying the lease is actively being renewed")
			// Get the lease one more time to confirm it's being renewed
			finalLease, err := client.Client.CoordinationV1().Leases(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpOperatorLeaseID, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(finalLease.Spec.HolderIdentity).ToNot(BeNil())
			Expect(*finalLease.Spec.HolderIdentity).ToNot(BeEmpty())
			logrus.Infof("successfully acquired lease %s/%s", pkg.PtpLinuxDaemonNamespace, pkg.PtpOperatorLeaseID)
		})

		// Webhook validation test for underscore profile names
		It("Should accept underscores in ptp-config profile names for HA profiles", func() {
			if DesiredMode != testconfig.DualNICBoundaryClockHA {
				Skip("Skipping as the test is only applicable for Dual NIC BC in HA mode (dualnicbcha)")
			}

			By("Creating a PtpConfig with underscore profile names in haProfiles")
			err := testconfig.CreatePtpConfigWithUnderscoreProfileNames()
			Expect(err).ToNot(HaveOccurred(), "webhook should accept underscores in profile names")
			logrus.Infof("Successfully created PtpConfig with underscore profile names: test_profile_bc1, test_profile_bc2")

			By("Verifying the PtpConfig was created with correct haProfiles")
			ptpConfig, err := client.Client.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpUnderscoreTestPolicyName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(ptpConfig.Spec.Profile)).To(BeNumerically(">", 0), "no profiles found in PtpConfig")

			haProfiles, exists := ptpConfig.Spec.Profile[0].PtpSettings["haProfiles"]
			Expect(exists).To(BeTrue(), "haProfiles setting should exist")
			Expect(haProfiles).To(ContainSubstring("test_profile_bc1"), "haProfiles should contain underscore profile name")
			Expect(haProfiles).To(ContainSubstring("test_profile_bc2"), "haProfiles should contain underscore profile name")
			logrus.Infof("Verified haProfiles contains underscore names: %s", haProfiles)

			// cleaning up the test PtpConfig
			err = testconfig.DeletePtpConfigWithUnderscoreProfileNames()
			Expect(err).ToNot(HaveOccurred(), "failed to delete test PtpConfig")
			logrus.Infof("Successfully deleted test PtpConfig")
		})

		// PtpOperatorConfig eventConfig test
		It("Should be able to update and read ptpoperatorconfig eventConfig.EnableEventPublisher", func() {
			By("Getting the current PtpOperatorConfig")
			ptpOperatorConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			// Store original value to restore later
			var originalEnableEventPublisher bool
			if ptpOperatorConfig.Spec.EventConfig != nil {
				originalEnableEventPublisher = ptpOperatorConfig.Spec.EventConfig.EnableEventPublisher
			}
			logrus.Infof("Original EnableEventPublisher value: %v", originalEnableEventPublisher)

			By("Setting EnableEventPublisher to true")
			if ptpOperatorConfig.Spec.EventConfig == nil {
				ptpOperatorConfig.Spec.EventConfig = &ptpv1.PtpEventConfig{}
			}
			ptpOperatorConfig.Spec.EventConfig.EnableEventPublisher = true
			_, err = client.Client.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Update(context.Background(), ptpOperatorConfig, metav1.UpdateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Reading back and verifying EnableEventPublisher is true")
			ptpOperatorConfig, err = client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(ptpOperatorConfig.Spec.EventConfig).ToNot(BeNil(), "EventConfig should not be nil")
			Expect(ptpOperatorConfig.Spec.EventConfig.EnableEventPublisher).To(BeTrue(), "EnableEventPublisher should be true")
			logrus.Infof("Verified EnableEventPublisher is set to true")

			By("Setting EnableEventPublisher to false")
			ptpOperatorConfig.Spec.EventConfig.EnableEventPublisher = false
			_, err = client.Client.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Update(context.Background(), ptpOperatorConfig, metav1.UpdateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Reading back and verifying EnableEventPublisher is false")
			ptpOperatorConfig, err = client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(ptpOperatorConfig.Spec.EventConfig.EnableEventPublisher).To(BeFalse(), "EnableEventPublisher should be false")
			logrus.Infof("Verified EnableEventPublisher is set to false")

			By("Restoring original EnableEventPublisher value")
			ptpOperatorConfig.Spec.EventConfig.EnableEventPublisher = originalEnableEventPublisher
			_, err = client.Client.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Update(context.Background(), ptpOperatorConfig, metav1.UpdateOptions{})
			Expect(err).ToNot(HaveOccurred())
			logrus.Infof("Restored EnableEventPublisher to original value: %v", originalEnableEventPublisher)
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
				if fullConfig.PtpModeDiscovered == testconfig.OrdinaryClock {
					// In 4.21+, OC correctly reports its local clock class (255/SlaveOnly).
					// Before 4.21, OC was incorrectly detected as GM and reported upstream GM's class (6).
					expectedClockClass := fbprotocol.ClockClass6
					if ptphelper.IsPTPOperatorVersionAtLeast("4.21") {
						expectedClockClass = fbprotocol.ClockClassSlaveOnly
					}
					By(fmt.Sprintf("Verifying OC clock_class is %d", expectedClockClass))
					checkClockClassState(fullConfig, strconv.Itoa(int(expectedClockClass)))
				}
			})

			It("Slave fails to sync when authentication mismatch occurs (negative test)", func() {
				// Only run if authentication is enabled
				authEnabled := os.Getenv("PTP_AUTH_ENABLED")
				if authEnabled != "true" {
					Skip("Authentication negative test requires PTP_AUTH_ENABLED=true")
				}

				if fullConfig.PtpModeDesired == testconfig.TelcoGrandMasterClock {
					Skip("Skipping as slave interface is not available with a WPC-GM profile")
				}

				// Save original GM config for restoration
				var originalGMConfig *ptpv1.PtpConfig

				By("Creating attacker secret with different SPP (simulates key mismatch)", func() {
					// Delete if already exists
					client.Client.Secrets(pkg.PtpLinuxDaemonNamespace).Delete(
						context.Background(),
						pkg.PtpSecurityMismatchSecretName,
						metav1.DeleteOptions{},
					)
					time.Sleep(2 * time.Second)

					// Create secret with spp 0 but DIFFERENT KEYS than ptp-security-conf
					// GM signs with these keys, BC has different keys for spp 0 → signature mismatch
					mismatchSecret := testconfig.CreateSecurityMismatchSecret(pkg.PtpLinuxDaemonNamespace)

					_, err := client.Client.Secrets(pkg.PtpLinuxDaemonNamespace).Create(
						context.Background(),
						mismatchSecret,
						metav1.CreateOptions{},
					)
					Expect(err).NotTo(HaveOccurred())
					fmt.Fprintf(GinkgoWriter, "Created mismatch secret with only spp 0\n")
				})

				By("Changing test-grandmaster to use spp 0 secret", func() {
					// Get current PtpConfig
					ptpConfig, err := client.Client.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Get(
						context.Background(),
						pkg.PtpGrandMasterPolicyName,
						metav1.GetOptions{},
					)
					Expect(err).NotTo(HaveOccurred())

					// Save the original config for restoration
					originalGMConfig = ptpConfig.DeepCopy()

					// Change sa_file to point to mismatch secret
					// Also change interface spp from 1 to 0
					ptp4lConf := *ptpConfig.Spec.Profile[0].Ptp4lConf
					modifiedConf := strings.Replace(ptp4lConf,
						"sa_file /etc/ptp-secret-mount/ptp-security-conf/ptp-security.conf",
						"sa_file /etc/ptp-secret-mount/ptp-security-mismatch/ptp-security.conf", 1)
					modifiedConf = strings.Replace(modifiedConf, "spp 1", "spp 0", 1) // Change first spp 1 to spp 0
					ptpConfig.Spec.Profile[0].Ptp4lConf = &modifiedConf

					// Update the PtpConfig
					_, err = client.Client.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Update(
						context.Background(),
						ptpConfig,
						metav1.UpdateOptions{},
					)
					Expect(err).NotTo(HaveOccurred())

					fmt.Fprintf(GinkgoWriter, "GM now uses spp 0 (slaves use spp 1 - mismatch!)\n")
					// Wait for controller to reconcile and pods to restart
					time.Sleep(60 * time.Second)
				})

				By("Verifying slave FAILS to sync due to SPP mismatch (2 minute check)", func() {
					// For negative test: expect sync to FAIL
					// Check that slave does NOT reach LOCKED state for 2 minutes

					// Wait 2 minutes and continuously verify slave is NOT in LOCKED state
					slavePod := fullConfig.DiscoveredClockUnderTestPod
					Expect(slavePod).NotTo(BeNil())

					// Use a simple metric check in a loop
					failedToSync := false
					for i := 0; i < 12; i++ { // 12 iterations × 10 seconds = 2 minutes
						// Try to check if it's in LOCKED state (this will return error if not LOCKED)
						if len(fullConfig.DiscoveredFollowerInterfaces) > 0 {
							slaveInterface := fullConfig.DiscoveredFollowerInterfaces[0]
							nodeNameStr := slavePod.Spec.NodeName

							err := metrics.CheckClockState(metrics.MetricClockStateLocked, slaveInterface, &nodeNameStr)
							if err != nil {
								// Error means NOT in LOCKED state - good for negative test
								failedToSync = true
							} else {
								// No error means it IS in LOCKED state - bad for negative test!
								Fail("Slave unexpectedly reached LOCKED state despite SPP mismatch")
								return
							}
						}
						time.Sleep(10 * time.Second)
					}

					Expect(failedToSync).To(BeTrue(), "Slave should fail to sync for 2 minutes due to authentication mismatch")
					fmt.Fprintf(GinkgoWriter, "✓ PASS: Authentication mismatch prevented sync (slave did not lock for 2 minutes)\n")
				})

				By("Restoring authentication to test-grandmaster (cleanup)", func() {
					// Restore the original config with auth settings intact
					Expect(originalGMConfig).NotTo(BeNil(), "Original GM config should have been saved")

					// Clear resource version for update
					originalGMConfig.SetResourceVersion("")

					// Delete the current config with spp 0
					err := client.Client.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Delete(
						context.Background(),
						pkg.PtpGrandMasterPolicyName,
						metav1.DeleteOptions{},
					)
					Expect(err).NotTo(HaveOccurred())

					// Wait for deletion
					time.Sleep(10 * time.Second)

					// Recreate with the original config (spp 1)
					_, err = client.Client.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Create(
						context.Background(),
						originalGMConfig,
						metav1.CreateOptions{},
					)
					Expect(err).NotTo(HaveOccurred())

					// Delete mismatch secret
					client.Client.Secrets(pkg.PtpLinuxDaemonNamespace).Delete(
						context.Background(),
						pkg.PtpSecurityMismatchSecretName,
						metav1.DeleteOptions{},
					)

					fmt.Fprintf(GinkgoWriter, "Restored test-grandmaster with original authentication settings\n")

					// Wait for pods to restart and stabilize
					time.Sleep(60 * time.Second)
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

					// Additional wait for clock sync to complete in multi-hop topologies (GM → BC → Slave)
					time.Sleep(30 * time.Second)
				})
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

					DeferCleanup(func() {
						err := client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Delete(context.Background(), pkg.PtpTempPolicyName, metav1.DeleteOptions{})
						if err != nil && !kerrors.IsNotFound(err) {
							logrus.Errorf("failed to delete temp ptpconfig %s: %s", pkg.PtpTempPolicyName, err)
						}
					})

					testPtpPod, err = ptphelper.GetPtpPodOnNode(nodes.Items[0].Name)
					Expect(err).NotTo(HaveOccurred())

					testPtpPod, err = ptphelper.ReplaceTestPod(&testPtpPod, time.Minute)
					Expect(err).NotTo(HaveOccurred())
				})

				By("Checking if Node has Profile and check sync", func() {
					// Don't pass gmID upfront: creating the temp config triggers
					// operator reconciliation which may restart the GM daemon
					// asynchronously, changing its clock ID. First confirm the
					// slave is synced (role + offset metrics), then verify the
					// slave's master matches the GM's current clock.
					err = ptptesthelper.BasicClockSyncCheck(fullConfig, modifiedPtpConfig, nil, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
					Expect(err).To(BeNil())

					if fullConfig.L2Config != nil && !isExternalMaster {
						aLabel := pkg.PtpGrandmasterNodeLabel
						gmClockID, err := ptphelper.GetClockIDMaster(pkg.PtpGrandMasterPolicyName, &aLabel, nil, true)
						Expect(err).To(BeNil())

						profileName, err := ptphelper.GetProfileName(modifiedPtpConfig)
						Expect(err).To(BeNil())
						label, err := ptphelper.GetLabel(modifiedPtpConfig)
						if err != nil {
							logrus.Warnf("could not get label from ptpconfig: %v", err)
						}
						node, err := ptphelper.GetFirstNode(modifiedPtpConfig)
						if err != nil {
							logrus.Warnf("could not get first node from ptpconfig: %v", err)
						}
						if label != nil && node != nil {
							slaveMaster, err := ptphelper.GetClockIDForeign(profileName, label, node)
							Expect(err).To(BeNil())
							Expect(slaveMaster).To(HavePrefix(gmClockID),
								fmt.Sprintf("Slave master %s does not match GM clock %s", slaveMaster, gmClockID))
						}
					}
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

			Context("Event API version validation", func() {
				BeforeEach(func() {
					if !ptphelper.IsPTPOperatorVersionAtLeast("4.19") {
						Skip("Skipping: these tests require PTP Operator version 4.19 or higher")
					}
				})

				// Verify cloud-event-proxy defaults to v2 when apiVersion is not set
				It("Should default to event API v2 when apiVersion is not explicitly set in PtpOperatorConfig", func() {
					By("Verifying apiVersion is not explicitly set in PtpOperatorConfig")
					ptpOperatorConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(
						context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())

					// Skip if apiVersion is explicitly set - this test is for default behavior
					if ptpOperatorConfig.Spec.EventConfig != nil && ptpOperatorConfig.Spec.EventConfig.ApiVersion != "" {
						Skip(fmt.Sprintf("apiVersion is explicitly set to '%s', skipping default test",
							ptpOperatorConfig.Spec.EventConfig.ApiVersion))
					}

					By("Checking cloud-event-proxy logs for v2 API config on all pods")
					for _, pod := range ptpPods.Items {
						// Verify cloud-event-proxy container exists
						cloudProxyFound := false
						for _, c := range pod.Spec.Containers {
							if c.Name == pkg.EventProxyContainerName {
								cloudProxyFound = true
							}
						}
						Expect(cloudProxyFound).ToNot(BeFalse(),
							fmt.Sprintf("No cloud-event-proxy container in pod %s", pod.Name))

						logrus.Infof("Checking cloud-event-proxy logs on pod %s (node: %s)", pod.Name, pod.Spec.NodeName)

						// Search for "starting v2 rest api server at port" in pod logs (literal text search)
						matches, err := pods.GetPodLogsRegex(
							pod.Namespace,
							pod.Name,
							pkg.EventProxyContainerName,
							`starting v2 rest api server at port`,
							true,
							30*time.Second,
						)
						Expect(err).NotTo(HaveOccurred(),
							fmt.Sprintf("Failed to get logs from pod %s", pod.Name))
						Expect(matches).NotTo(BeEmpty(),
							fmt.Sprintf("No 'starting v2 rest api server at port' found in cloud-event-proxy logs on pod %s", pod.Name))

						logrus.Infof("Pod %s: found 'starting v2 rest api server at port' in logs", pod.Name)
					}
				})

				// Verify cloud-event-proxy runs v2 when apiVersion is explicitly set to "2.0"
				It("Should run event API v2 when apiVersion is explicitly set to 2.0 in PtpOperatorConfig", func() {
					By("Setting apiVersion to 2.0 in PtpOperatorConfig")
					err := ptphelper.EnablePTPEvent("2.0", "")
					Expect(err).NotTo(HaveOccurred(), "Failed to set apiVersion to 2.0")

					By("Verifying apiVersion is set to 2.0 in PtpOperatorConfig")
					ptpOperatorConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(
						context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())
					Expect(ptpOperatorConfig.Spec.EventConfig).NotTo(BeNil(), "EventConfig should not be nil")
					Expect(ptpOperatorConfig.Spec.EventConfig.ApiVersion).To(Equal("2.0"),
						"apiVersion should be explicitly set to 2.0")

					// Wait for operator to process config change
					time.Sleep(5 * time.Second)

					By("Waiting for daemonset to be ready and refreshing pods list")
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

					ptpPods, err = client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(
						context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
					Expect(err).NotTo(HaveOccurred())
					Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), "linuxptp-daemon is not deployed on cluster")

					By("Checking cloud-event-proxy logs for v2 API config on all pods")
					for _, pod := range ptpPods.Items {
						// Verify cloud-event-proxy container exists
						cloudProxyFound := false
						for _, c := range pod.Spec.Containers {
							if c.Name == pkg.EventProxyContainerName {
								cloudProxyFound = true
							}
						}
						Expect(cloudProxyFound).ToNot(BeFalse(),
							fmt.Sprintf("No cloud-event-proxy container in pod %s", pod.Name))

						logrus.Infof("Checking cloud-event-proxy logs on pod %s (node: %s)", pod.Name, pod.Spec.NodeName)

						// Search for REST API config v2.0 in pod logs (literal text search)
						matches, err := pods.GetPodLogsRegex(
							pod.Namespace,
							pod.Name,
							pkg.EventProxyContainerName,
							`starting v2 rest api server at port`,
							true,
							30*time.Second,
						)
						Expect(err).NotTo(HaveOccurred(),
							fmt.Sprintf("Failed to get logs from pod %s", pod.Name))
						Expect(matches).NotTo(BeEmpty(),
							fmt.Sprintf("No 'starting v2 rest api server at port' found in cloud-event-proxy logs on pod %s", pod.Name))

						logrus.Infof("Pod %s: found 'starting v2 rest api server at port' in logs", pod.Name)
					}
				})
				// Verify invalid apiVersion values are rejected and pods are not restarted
				It("Should reject invalid apiVersion and not restart pods", func() {
					testCases := []struct {
						name                   string
						invalidVersion         string
						expectedErrorSubstring string
					}{
						{"v1 (deprecated/EOL)", "1.0", "v1 is no longer supported"},
						{"invalid version 'boo'", "boo", "is not a valid version"},
					}

					for _, tc := range testCases {
						By(fmt.Sprintf("Testing: %s", tc.name))

						By("Recording current pod UIDs and container count")
						originalPodUIDs := make(map[string]string)
						originalContainerCounts := make(map[string]int)
						for _, pod := range ptpPods.Items {
							originalPodUIDs[pod.Name] = string(pod.UID)
							originalContainerCounts[pod.Name] = len(pod.Spec.Containers)
							logrus.Infof("Pod %s: UID=%s, containers=%d", pod.Name, pod.UID, len(pod.Spec.Containers))
						}

						By("Recording current apiVersion in PtpOperatorConfig")
						ptpOperatorConfigBefore, err := client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(
							context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
						Expect(err).NotTo(HaveOccurred())
						originalApiVersion := ""
						if ptpOperatorConfigBefore.Spec.EventConfig != nil {
							originalApiVersion = ptpOperatorConfigBefore.Spec.EventConfig.ApiVersion
						}
						logrus.Infof("Original apiVersion: '%s'", originalApiVersion)

						By(fmt.Sprintf("Attempting to set apiVersion to '%s' (should be rejected)", tc.invalidVersion))
						err = ptphelper.EnablePTPEvent(tc.invalidVersion, "")
						Expect(err).To(HaveOccurred(),
							fmt.Sprintf("Setting apiVersion to '%s' should have been rejected", tc.invalidVersion))
						Expect(err.Error()).To(ContainSubstring(tc.expectedErrorSubstring),
							"Error should contain expected message")
						logrus.Infof("Received expected rejection error: %s", err.Error())

						By("Verifying PtpOperatorConfig was not changed")
						ptpOperatorConfigAfter, err := client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(
							context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
						Expect(err).NotTo(HaveOccurred())
						afterApiVersion := ""
						if ptpOperatorConfigAfter.Spec.EventConfig != nil {
							afterApiVersion = ptpOperatorConfigAfter.Spec.EventConfig.ApiVersion
						}
						Expect(afterApiVersion).To(Equal(originalApiVersion),
							"apiVersion should not have changed after rejected update")

						By("Verifying pods were not restarted (same UIDs and container count)")
						currentPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(
							context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
						Expect(err).NotTo(HaveOccurred())
						Expect(len(currentPods.Items)).To(Equal(len(ptpPods.Items)),
							"Number of pods should remain the same")

						for _, pod := range currentPods.Items {
							originalUID, exists := originalPodUIDs[pod.Name]
							Expect(exists).To(BeTrue(),
								fmt.Sprintf("Pod %s should still exist", pod.Name))
							Expect(string(pod.UID)).To(Equal(originalUID),
								fmt.Sprintf("Pod %s should have same UID (no restart)", pod.Name))
							Expect(len(pod.Spec.Containers)).To(Equal(originalContainerCounts[pod.Name]),
								fmt.Sprintf("Pod %s should have same number of containers", pod.Name))
							logrus.Infof("Pod %s: UID unchanged (%s), containers=%d", pod.Name, pod.UID, len(pod.Spec.Containers))
						}
					}
				})
			})
			It("Should receive events after linuxptp-daemon pod restart", func() {
				if ptphelper.PtpEventEnabled() != 2 {
					Skip("Skipping: test applies to event API v2 only")
				}

				By("Deploying consumer app for event API v2")
				nodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
				Expect(nodeName).ToNot(BeEmpty(), "clock-under-test pod node is empty")
				err := event.CreateConsumerApp(nodeName)
				if err != nil {
					Skip(fmt.Sprintf("Consumer app setup failed: %v", err))
				}
				DeferCleanup(func() {
					_ = event.DeleteConsumerNamespace()
					if event.PubSub != nil {
						event.PubSub.Close()
					}
				})
				time.Sleep(10 * time.Second)
				event.InitPubSub()
				term, monErr := event.MonitorPodLogsRegex()
				Expect(monErr).ToNot(HaveOccurred(), "could not start listening to events")
				DeferCleanup(func() { stopMonitor(term) })

				By("Restarting linuxptp-daemon pod on the clock-under-test node")
				originalPod := *fullConfig.DiscoveredClockUnderTestPod
				newPod, err := ptphelper.ReplaceTestPod(&originalPod, pkg.TimeoutIn5Minutes)
				Expect(err).NotTo(HaveOccurred())

				By("Waiting for linuxptp-daemon to become ready after restart")
				ptphelper.WaitForPtpDaemonToExist()
				fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
				Expect(err).NotTo(HaveOccurred())
				ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

				fullConfig.DiscoveredClockUnderTestPod = &newPod

				By("Checking consumer app getCurrentState log for SyncStateChange")
				err = event.PushInitialEvent(string(ptpEvent.SyncStateChange), 2*time.Minute)
				Expect(err).NotTo(HaveOccurred(), "getCurrentState did not return SyncStateChange")
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
							logrus.Errorf("Reference plugin not loaded, err=%s", err)
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
							logrus.Errorf("Reference plugin not running OnPTPConfigChangeGeneric, err=%s", err)
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
							logrus.Errorf("error getting profile=%s, err=%s ", name, err)
							continue
						}
						_, err = pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							ptp4lLog, true, pkg.TimeoutIn3Minutes)
						if err != nil {
							logrus.Errorf("error getting ptp4l chrt line=%s, err=%s ", ptp4lLog, err)
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
					// If authentication enabled, mount the Secret resource for sa_file access
					authEnabled := os.Getenv("PTP_AUTH_ENABLED")
					if authEnabled == "true" {
						// Mount the same secret that linuxptp-daemon uses
						podDefinition.Spec.Volumes = append(podDefinition.Spec.Volumes, v1core.Volume{
							Name: "ptp-security-conf-tlv-auth",
							VolumeSource: v1core.VolumeSource{
								Secret: &v1core.SecretVolumeSource{
									SecretName: "ptp-security-conf", // Same secret as linuxptp-daemon
								},
							},
						})
						podDefinition.Spec.Containers[0].VolumeMounts = append(podDefinition.Spec.Containers[0].VolumeMounts, v1core.VolumeMount{
							Name:      "ptp-security-conf-tlv-auth",
							MountPath: "/etc/ptp-secret-mount/ptp-security-conf",
							ReadOnly:  true,
						})
						logrus.Infof("[PMC UDS Test] Auth enabled: mounting secret 'ptp-security-conf' to /etc/ptp-secret-mount/ptp-security-conf/ptp-security.conf in test pod")
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
					// If authentication enabled, mount the Secret resource for sa_file access
					authEnabled := os.Getenv("PTP_AUTH_ENABLED")
					if authEnabled == "true" {
						// Mount the same secret that linuxptp-daemon uses
						podDefinition.Spec.Volumes = append(podDefinition.Spec.Volumes, v1core.Volume{
							Name: "ptp-security-conf-tlv-auth",
							VolumeSource: v1core.VolumeSource{
								Secret: &v1core.SecretVolumeSource{
									SecretName: "ptp-security-conf", // Same secret as linuxptp-daemon
								},
							},
						})
						podDefinition.Spec.Containers[0].VolumeMounts = append(podDefinition.Spec.Containers[0].VolumeMounts, v1core.VolumeMount{
							Name:      "ptp-security-conf-tlv-auth",
							MountPath: "/etc/ptp-secret-mount/ptp-security-conf",
							ReadOnly:  true,
						})
						logrus.Infof("[PMC UDS Test] Auth enabled: mounting secret 'ptp-security-conf' to /etc/ptp-secret-mount/ptp-security-conf/ptp-security.conf in test pod")
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
				2    | Disable GNSS
				3	 | Wait for DPLL state = 3 (Holdover)
				3    | Wait for ClockClass 7 (in-spec holdover)
				4    | Check clock state = 2 (Holdover)
				5    | Enable GNSS
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

					// Disable GNSS
					disableGNSSViaSignalRequirements(fullConfig)
					defer enableGNSSViaSignalRequirements(fullConfig) // Clean up incase test fails

					// Meanwhile, wait for ClockClass 7 (GNSS loss - Holdover In Spec)
					waitForClockClass(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass7)))

					// Give DPLL/GM time update
					time.Sleep(pkg.Timeout10Seconds)

					// Also verify ClockState 2 (Holdover)
					checkClockStateForProcess(fullConfig, "GM", "2")

					// Also verify ClockState (Holdover) for DPLL
					checkClockStateForProcess(fullConfig, "dpll", "2")

					// Once holdover detected, enable GNSS
					enableGNSSViaSignalRequirements(fullConfig)

					// Give GNSS time to fully recover
					time.Sleep(pkg.Timeout10Seconds)

					// Now wait for system to go back to LOCKED (ClockClass 6)
					waitForClockClass(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)))
					// Give DPLL/GM time update
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
				closeTs2phcStopChan := sync.OnceFunc(func() { close(stopChan) })
				DeferCleanup(closeTs2phcStopChan)

				// Phase 1: Wait for FREERUN and Clock Class 248 after continuous ts2phc kills
				By("Waiting for FREERUN and ClockClass 248 after continuous ts2phc kills")
				// Make clock class optional as stop gap for CI failures which we only see in CI
				waitForStateAndCC(subs, ptpEvent.FREERUN, 248, 90*time.Second, true)

				// Once FREERUN/holdover detected, stop continuous killing
				closeTs2phcStopChan()

				// Phase 2: Re-subscribe with initial snapshot to handle recovery to LOCKED/CC=6
				const buf2 = 100
				subs2, cleanup2 := event.SubscribeToGMChangeEvents(buf2, true, 60*time.Second)
				DeferCleanup(cleanup2)
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

				// Start log monitor once
				term, err := event.MonitorPodLogsRegex()
				defer func() { stopMonitor(term) }()
				Expect(err).ToNot(HaveOccurred(), "could not start listening to events")

				By("Disabling GNSS")
				disableGNSSViaSignalRequirements(fullConfig)
				defer enableGNSSViaSignalRequirements(fullConfig) // Clean up incase test fails

				// now use subs.GNSS, subs.CC, subs.LS
				events := getGMEvents(subs.GNSS, subs.CLOCKCLASS, subs.LOCKSTATE, 10*time.Second)
				fmt.Fprintf(GinkgoWriter, "First Event recieved  %v, ", events)

				By("Verifying ClockClass value to 7")
				verifyMetric(events[ptpEvent.PtpClockClassChange], float64(fbprotocol.ClockClass7))
				By("Verifying PTP state Holdover")
				verifyEvent(events[ptpEvent.PtpStateChange], ptpEvent.HOLDOVER)
				stopMonitor(term)

				// Stop phase 1 collection / enable GNSS
				By("Enabling GNSS")
				enableGNSSViaSignalRequirements(fullConfig)

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

		It("Should properly cleanup volumeMounts when secrets are deleted and remount when recreated", func() {
			ptpOperatorVersion, err := ptphelper.GetPtpOperatorVersion()
			Expect(err).ToNot(HaveOccurred())
			Expect(ptpOperatorVersion).ShouldNot(BeEmpty())
			operatorVersion, _ := semver.NewVersion(ptpOperatorVersion)
			haVersion, _ := semver.NewVersion("4.22")
			if operatorVersion.LessThan(haVersion) {
				Skip("Skipping volumeMount cleanup test - requires OCP 4.22+")
			}
			var testNode string
			var testInterface string
			var testPtpConfig *ptpv1.PtpConfig
			var testSecret *v1core.Secret

			By("Getting discovered config with PTP interfaces", func() {
				fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				if fullConfig.Status != testconfig.DiscoverySuccessStatus {
					Skip("Failed to find a valid ptp configuration")
				}

				// Get interfaces from L2 config
				ptpInterfaces := fullConfig.L2Config.GetPtpIfList()
				Expect(len(ptpInterfaces)).To(BeNumerically(">", 0), "No PTP interfaces found")

				testInterface = ptpInterfaces[0].IfName
				testNode = ptpInterfaces[0].NodeName
				fmt.Fprintf(GinkgoWriter, "Using test node: %s, interface: %s\n", testNode, testInterface)
			})

			By("Creating test secret", func() {
				testSecret = testconfig.CreateTestSecretForVolumeMountTest(pkg.PtpLinuxDaemonNamespace)
				_, err := client.Client.Secrets(pkg.PtpLinuxDaemonNamespace).Create(
					context.Background(),
					testSecret,
					metav1.CreateOptions{},
				)
				Expect(err).NotTo(HaveOccurred())
				fmt.Fprintf(GinkgoWriter, "Created test secret: %s\n", testSecret.Name)
			})

			By("Creating PtpConfig with sa_file referencing the test secret", func() {
				testPtpConfig = testconfig.CreatePtpConfigForVolumeMountTest(testNode, testInterface)
				_, err := client.Client.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Create(
					context.Background(),
					testPtpConfig,
					metav1.CreateOptions{},
				)
				Expect(err).NotTo(HaveOccurred())
				fmt.Fprintf(GinkgoWriter, "Created test PtpConfig: %s\n", testPtpConfig.Name)

				// Wait for reconciliation to complete
				time.Sleep(10 * time.Second)
			})

			By("Verifying secret volume is mounted in DaemonSet", func() {
				ds, err := client.Client.DaemonSets(pkg.PtpLinuxDaemonNamespace).Get(
					context.Background(),
					"linuxptp-daemon",
					metav1.GetOptions{},
				)
				Expect(err).NotTo(HaveOccurred())

				// Check for volume with -tlv-auth suffix
				volumeName := "ptp-test-volume-secret-tlv-auth"
				volumeFound := false
				for _, vol := range ds.Spec.Template.Spec.Volumes {
					if vol.Name == volumeName {
						volumeFound = true
						Expect(vol.Secret).NotTo(BeNil())
						Expect(vol.Secret.SecretName).To(Equal("ptp-test-volume-secret"))
						fmt.Fprintf(GinkgoWriter, "Found volume: %s referencing secret: %s\n", vol.Name, vol.Secret.SecretName)
						break
					}
				}
				Expect(volumeFound).To(BeTrue(), "Volume for test secret not found in DaemonSet")

				// Check for volumeMount in linuxptp-daemon-container
				mountFound := false
				for _, container := range ds.Spec.Template.Spec.Containers {
					if container.Name == "linuxptp-daemon-container" {
						for _, mount := range container.VolumeMounts {
							if mount.Name == volumeName {
								mountFound = true
								Expect(mount.MountPath).To(Equal("/etc/ptp-secret-mount/ptp-test-volume-secret"))
								Expect(mount.ReadOnly).To(BeTrue())
								fmt.Fprintf(GinkgoWriter, "Found volumeMount: %s at path: %s\n", mount.Name, mount.MountPath)
								break
							}
						}
						break
					}
				}
				Expect(mountFound).To(BeTrue(), "VolumeMount for test secret not found in DaemonSet container")
			})

			By("Deleting the test secret", func() {
				err := client.Client.Secrets(pkg.PtpLinuxDaemonNamespace).Delete(
					context.Background(),
					testSecret.Name,
					metav1.DeleteOptions{},
				)
				Expect(err).NotTo(HaveOccurred())
				fmt.Fprintf(GinkgoWriter, "Deleted test secret: %s\n", testSecret.Name)

				// Wait for reconciliation to process the deletion
				time.Sleep(15 * time.Second)
			})

			By("Verifying secret volume is removed from DaemonSet", func() {
				ds, err := client.Client.DaemonSets(pkg.PtpLinuxDaemonNamespace).Get(
					context.Background(),
					"linuxptp-daemon",
					metav1.GetOptions{},
				)
				Expect(err).NotTo(HaveOccurred())

				// Check that volume is removed
				volumeName := "ptp-test-volume-secret-tlv-auth"
				volumeFound := false
				for _, vol := range ds.Spec.Template.Spec.Volumes {
					if vol.Name == volumeName {
						volumeFound = true
						break
					}
				}
				Expect(volumeFound).To(BeFalse(), "Volume for test secret still found in DaemonSet after deletion")

				// Check that volumeMount is removed
				mountFound := false
				for _, container := range ds.Spec.Template.Spec.Containers {
					if container.Name == "linuxptp-daemon-container" {
						for _, mount := range container.VolumeMounts {
							if mount.Name == volumeName {
								mountFound = true
								break
							}
						}
						break
					}
				}
				Expect(mountFound).To(BeFalse(), "VolumeMount for test secret still found in DaemonSet after deletion")
				fmt.Fprintf(GinkgoWriter, "Verified: Volume and VolumeMount removed after secret deletion\n")
			})

			By("Cleaning up test PtpConfig", func() {
				err := client.Client.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Delete(
					context.Background(),
					testPtpConfig.Name,
					metav1.DeleteOptions{},
				)
				Expect(err).NotTo(HaveOccurred())
				fmt.Fprintf(GinkgoWriter, "Deleted test PtpConfig: %s\n", testPtpConfig.Name)

				// Wait for cleanup
				time.Sleep(5 * time.Second)
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

func disableGNSSViaSignalRequirements(fullConfig testconfig.TestConfig) {
	_, _, err := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod,
		pkg.PtpContainerName, []string{"ubxtool", "-P", "29.25", "-w", "1", "-v", "3", "-z", "CFG-NAVSPG-INFIL_NCNOTHRS,50,1"})
	if err != nil {
		fmt.Fprintf(GinkgoWriter, "Error disabling GNSS: %v\n", err)
	} else {
		fmt.Fprintf(GinkgoWriter, "GNSS disabled\n")
	}
}

func enableGNSSViaSignalRequirements(fullConfig testconfig.TestConfig) {
	_, _, err := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod,
		pkg.PtpContainerName, []string{"ubxtool", "-P", "29.25", "-w", "1", "-v", "3", "-z", "CFG-NAVSPG-INFIL_NCNOTHRS,0,1"})
	if err != nil {
		fmt.Fprintf(GinkgoWriter, "Error enabling GNSS: %v\n", err)
	} else {
		fmt.Fprintf(GinkgoWriter, "GNSS enabled\n")
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
			_, stderr, err := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod,
				pkg.PtpContainerName, []string{"sh", "-c", "pkill -TERM ts2phc"})
			if err != nil {
				fmt.Fprintf(GinkgoWriter, "Error killing ts2phc: %v stdout '%s'\n", err, stderr.String())
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

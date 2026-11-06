package test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/logging"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/metrics"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/namespaces"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/pmc"
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
			if len(ptpConfig.Spec.DaemonNodeSelector) != 0 {
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

			By("Checking if HA is available in this operator version")
			ptpOperatorVersion, err := ptphelper.GetPtpOperatorVersion()
			Expect(err).ToNot(HaveOccurred())
			Expect(ptpOperatorVersion).ShouldNot(BeEmpty())
			operatorVersion, err := semver.NewVersion(ptpOperatorVersion)
			Expect(err).ToNot(HaveOccurred())
			haVersion, _ := semver.NewVersion("4.16")
			if operatorVersion.LessThan(haVersion) {
				Skip("HA is not available in version < 4.16")
			}

			By("Creating a PtpConfig with underscore profile names in haProfiles")
			err = testconfig.CreatePtpConfigWithUnderscoreProfileNames()
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
			_, err = client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Update(context.Background(), ptpOperatorConfig, metav1.UpdateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Reading back and verifying EnableEventPublisher is true")
			ptpOperatorConfig, err = client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(ptpOperatorConfig.Spec.EventConfig).ToNot(BeNil(), "EventConfig should not be nil")
			Expect(ptpOperatorConfig.Spec.EventConfig.EnableEventPublisher).To(BeTrue(), "EnableEventPublisher should be true")
			logrus.Infof("Verified EnableEventPublisher is set to true")

			By("Setting EnableEventPublisher to false")
			ptpOperatorConfig.Spec.EventConfig.EnableEventPublisher = false
			_, err = client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Update(context.Background(), ptpOperatorConfig, metav1.UpdateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Reading back and verifying EnableEventPublisher is false")
			ptpOperatorConfig, err = client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(ptpOperatorConfig.Spec.EventConfig.EnableEventPublisher).To(BeFalse(), "EnableEventPublisher should be false")
			logrus.Infof("Verified EnableEventPublisher is set to false")

			By("Restoring original EnableEventPublisher value")
			ptpOperatorConfig.Spec.EventConfig.EnableEventPublisher = originalEnableEventPublisher
			_, err = client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Update(context.Background(), ptpOperatorConfig, metav1.UpdateOptions{})
			Expect(err).ToNot(HaveOccurred())
			logrus.Infof("Restored EnableEventPublisher to original value: %v", originalEnableEventPublisher)
		})

	})

	Describe("PTP e2e tests", func() {
		var ptpPods *v1core.PodList
		var fifoPriorities []struct {
			configName  string
			profileName string
			priority    int64
		}
		var fullConfig testconfig.TestConfig
		portEngine := ptptesthelper.PortEngine{}

		execute.BeforeAll(func() {
			err := testconfig.CreatePtpConfigurationsWithRetry(3)
			if err != nil {
				fullConfig.Status = testconfig.DiscoveryFailureStatus
				// Only topology "no … solution found" is a Skip. Operator/API/apply
				// failures must Fail so they cannot hide behind a suite Skip.
				if strings.Contains(err.Error(), "no solution found") ||
					strings.Contains(err.Error(), "no T-BC solution found") {
					Skip(fmt.Sprintf("Could not create a ptp config (insufficient topology), err=%s", err))
				}
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

			if fullConfig.DiscoveredClockUnderTestPod == nil {
				Fail("DiscoveredClockUnderTestPod is nil - check that the node is labeled with " + pkg.PtpClockUnderTestNodeLabel)
			}
			pod := fullConfig.DiscoveredClockUnderTestPod
			clockNodes, err := fullConfig.ClockNodeInfo()
			if err != nil {
				logrus.Warnf("Failed to collect clock node information: %v", err)
			}
			if err := logging.WriteNodeInfo(pod.Spec.NodeName, strings.ToLower(fullConfig.PtpModeDiscovered.String()), clockNodes); err != nil {
				logrus.Warnf("Failed to write node information file: %v", err)
			}
			portEngine.Initialize(fullConfig.DiscoveredClockUnderTestPod, fullConfig.DiscoveredFollowerInterfaces)

		})

		Context("PTP Reboot discovery", func() {
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
				Expect(fullConfig.DiscoveredClockUnderTestPod).NotTo(BeNil(),
					"clock-under-test pod missing after refresh; label node with "+pkg.PtpClockUnderTestNodeLabel)
				Expect(fullConfig.DiscoveredClockUnderTestPtpConfig).NotTo(BeNil(),
					"clock-under-test PtpConfig missing after refresh")
				Skip("This is covered by QE")
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
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}

				podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
				Expect(err).NotTo(HaveOccurred())
				ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

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

				if ptpConfig.DiscoveredGrandMasterPtpConfig != nil {
					masterPtpConfigStr := ptpConfig.DiscoveredGrandMasterPtpConfig.String()
					logrus.Infof("Discovered master ptp config %s", masterPtpConfigStr)
					AddReportEntry("master-ptp-config", masterPtpConfigStr)
				} else {
					logrus.Infof("No internal grandmaster PTP config discovered (external GM)")
				}
				slavePtpConfigStr := ptpConfig.DiscoveredClockUnderTestPtpConfig.String()
				logrus.Infof("Discovered slave ptp config %s", slavePtpConfigStr)
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
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
				Expect(err).NotTo(HaveOccurred())
				ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)
			})
			AfterEach(func() {
				portEngine.TurnAllPortsUp()
			})
			// 25733
			It("PTP daemon apply match rule based on nodeLabel", func() {

				if fullConfig.PtpModeDesired == testconfig.Discovery {
					Skip("This test needs the ptp-daemon to be rebooted but it is not possible in discovery mode, skipping")
				}
				clockUnderTestConfig := (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig)
				profileSlaveName, err := ptphelper.GetProfileName(clockUnderTestConfig, true)
				Expect(err).NotTo(HaveOccurred(), "could not get clock-under-test profile name")
				profileSlave := ptphelper.ProfileNameLogPattern(clockUnderTestConfig.Name, profileSlaveName)
				if fullConfig.PtpModeDesired == testconfig.TelcoBoundaryClock {
					profileSlave = `(?m)Profile Name: (?:` +
						ptphelper.ProfileNameMatchPattern(clockUnderTestConfig.Name, "tbc-tr") + "|" +
						ptphelper.ProfileNameMatchPattern(clockUnderTestConfig.Name, "tbc-tt") + ")"
				}
				profileMaster := ""
				if fullConfig.DiscoveredGrandMasterPtpConfig != nil {
					gmConfig := (*ptpv1.PtpConfig)(fullConfig.DiscoveredGrandMasterPtpConfig)
					gmProfileName, gmErr := ptphelper.GetProfileName(gmConfig, false)
					Expect(gmErr).NotTo(HaveOccurred(), "could not get grandmaster profile name")
					profileMaster = ptphelper.ProfileNameLogPattern(gmConfig.Name, gmProfileName)
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
					// In TGMBC the GM and BC share a node (both labels present);
					// check grandmaster first so we look for the correct profile.
					if isGrandmaster && fullConfig.DiscoveredGrandMasterPtpConfig != nil {
						_, err = pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							profileMaster, false, pkg.TimeoutIn5Minutes)
						if err != nil {
							Fail(fmt.Sprintf("could not get master profile name, err=%s", err))
						}
					} else if isClockUnderTest {
						_, err = pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							profileSlave, false, pkg.TimeoutIn3Minutes)
						if err != nil {
							Fail(fmt.Sprintf("could not get slave profile name, err=%s", err))
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
			// - use cloud events to verify the slave is locked
			// - use metrics to verify that the offset is below threshold
			It("Slave reports lock state LOCKED via cloud events", func() {
				if fullConfig.PtpModeDesired == testconfig.TelcoGrandMasterClock {
					Skip("Skipping as slave interface is not available with a WPC-GM profile")
				}
				Expect(fullConfig.DiscoveredClockUnderTestPod).NotTo(BeNil(),
					"clock-under-test pod missing; label node with "+pkg.PtpClockUnderTestNodeLabel)
				evCtx := setupBCClockClassEvents(fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
				if !evCtx.available {
					Skip("PTP events not available")
				}
				waitForLockStateViaEvent(evCtx.subs, fullConfig.DiscoveredFollowerInterfaces[0], ptpEvent.LOCKED, pkg.TimeoutIn5Minutes)
			})

			It("Slave can sync to master", func() {
				if fullConfig.PtpModeDiscovered == testconfig.TelcoGrandMasterClock {
					Skip("Skipping: clock-under-test is a standalone TGM profile")
				}
				isExternalMaster := ptphelper.IsExternalGM()
				var grandmasterID *string
				if fullConfig.L2Config != nil && !isExternalMaster {
					aLabel := pkg.PtpGrandmasterNodeLabel
					gmProfileName := pkg.PtpGrandMasterPolicyName
					if fullConfig.PtpModeDiscovered == testconfig.TelcoGMOC || fullConfig.PtpModeDiscovered == testconfig.TelcoGMBC {
						gmProfileName = pkg.PtpWPCGrandMasterPolicyName
					}
					var aString string
					Expect(fullConfig.DiscoveredGrandMasterPtpConfig).NotTo(BeNil(), "expected discovered grandmaster config")
					gmConfigName := (*ptpv1.PtpConfig)(fullConfig.DiscoveredGrandMasterPtpConfig).Name
					Eventually(func() error {
						var getErr error
						aString, getErr = ptphelper.GetClockIDMaster(gmConfigName, gmProfileName, &aLabel, nil, true)
						return getErr
					}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(BeNil(),
						"Timeout to get grandmaster clock ID")
					grandmasterID = &aString
				}

				waitForWPCGMReady(fullConfig)

				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(BeNil())
				if fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClock || fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClockHA {
					err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestSecondaryPtpConfig), grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
					Expect(err).To(BeNil())
				}
				if fullConfig.PtpModeDiscovered == testconfig.OrdinaryClock || fullConfig.PtpModeDiscovered == testconfig.TelcoGMOC {
					// In 4.21+, a single-iface OC follower (including TGMOC) reports its
					// local clock class (255/SlaveOnly). Before 4.21, OC was incorrectly
					// detected as GM and reported the upstream GM's class (6).
					expectedClockClass := fbprotocol.ClockClass6
					if ptphelper.IsPTPOperatorVersionAtLeast("4.21") {
						expectedClockClass = fbprotocol.ClockClassSlaveOnly
					}
					By(fmt.Sprintf("Verifying OC clock_class is %d", expectedClockClass))
					checkClockClassState(fullConfig, strconv.Itoa(int(expectedClockClass)), pkg.TimeoutIn5Minutes)
				}
			})

			It("Slave loses synchronization when its upstream authentication key is removed (negative test)", func() {
				if !testconfig.IsAuthEnabled() {
					Skip("Authentication negative test requires PTP_AUTH_ENABLED=true")
				}
				if fullConfig.PtpModeDesired != testconfig.OrdinaryClock {
					Skip(fmt.Sprintf("Authentication negative test supports OC only, mode is %v", fullConfig.PtpModeDesired))
				}
				if fullConfig.PtpModeDiscovered != testconfig.OrdinaryClock {
					Skip(fmt.Sprintf("Authentication negative test supports OC only, discovered mode is %v", fullConfig.PtpModeDiscovered))
				}
				if fullConfig.DiscoveredClockUnderTestSecondaryPtpConfig != nil {
					Skip("Authentication negative test does not support multiple clock-under-test profiles or HA")
				}

				discoveredConfig := (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig)
				Expect(discoveredConfig).NotTo(BeNil(), "clock-under-test PtpConfig is required")
				profileName, err := ptphelper.GetProfileName(discoveredConfig, true)
				Expect(err).NotTo(HaveOccurred(), "could not resolve the clock-under-test receiver profile")

				ptpConfig, err := client.Client.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Get(
					context.Background(), discoveredConfig.Name, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "could not get clock-under-test PtpConfig %s", discoveredConfig.Name)
				profile, err := profileByName(ptpConfig, profileName)
				Expect(err).NotTo(HaveOccurred(), "could not resolve receiver profile %s in PtpConfig %s", profileName, ptpConfig.Name)
				Expect(profile.Ptp4lConf).NotTo(BeNil(), "receiver profile %s has no ptp4lConf", profileName)
				Expect(ptpConfig.Spec.Profile).To(HaveLen(1), "Authentication negative test requires one ptp4l profile in PtpConfig %s", ptpConfig.Name)

				receiveInterfaces := ptpv1.GetInterfaces(*ptpConfig, ptpv1.Slave)
				Expect(receiveInterfaces).To(HaveLen(1), "Authentication negative test requires exactly one upstream receive interface in profile %s", profileName)
				slaveInterface := receiveInterfaces[0]

				targetPod, err := ptphelper.GetPTPPodWithPTPConfig(ptpConfig)
				Expect(err).NotTo(HaveOccurred(), "could not find the linuxptp-daemon pod for %s", ptpConfig.Name)
				nodeName := targetPod.Spec.NodeName
				Expect(nodeName).NotTo(BeEmpty(), "clock-under-test pod has no node identity")
				fullConfig.DiscoveredClockUnderTestPod = targetPod
				psOutput, err := listProcessesInPod(targetPod)
				Expect(err).NotTo(HaveOccurred(), "could not list processes on node %s", nodeName)
				Expect(ptp4lPIDsFromPS(psOutput, "")).To(HaveLen(1),
					"Authentication negative test requires exactly one ptp4l process on node %s", nodeName)

				var grandmasterID *string
				if fullConfig.L2Config != nil && !ptphelper.IsExternalGM() {
					Expect(fullConfig.DiscoveredGrandMasterPtpConfig).NotTo(BeNil(), "expected discovered grandmaster config")
					gmConfigName := (*ptpv1.PtpConfig)(fullConfig.DiscoveredGrandMasterPtpConfig).Name
					gmLabel := pkg.PtpGrandmasterNodeLabel
					var id string
					Eventually(func() error {
						var getErr error
						id, getErr = ptphelper.GetClockIDMaster(gmConfigName, pkg.PtpGrandMasterPolicyName, &gmLabel, nil, true)
						return getErr
					}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(Succeed(), "could not get the grandmaster clock ID")
					grandmasterID = &id
				}

				By("establishing authenticated synchronization before changing the receiver SAD")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, ptpConfig, grandmasterID,
					metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(Succeed(), "authenticated receiver baseline must be LOCKED/SLAVE with acceptable offset")

				configFile, err := ptptesthelper.DiscoverPtp4lConfigByProfile(ptpConfig.Name, profileName, nodeName)
				Expect(err).NotTo(HaveOccurred(), "could not resolve rendered ptp4l config for %s/%s", ptpConfig.Name, profileName)

				By("getting the original SAD (Security Association Database)")
				secret, err := client.Client.CoreV1().Secrets(pkg.PtpLinuxDaemonNamespace).Get(
					context.Background(), pkg.PtpSecurityConfSecretName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "could not get the ptp-security-conf Secret")
				originalSAD, exists := secret.Data["ptp-security.conf"]
				Expect(exists).To(BeTrue(), "ptp-security-conf is missing ptp-security.conf")
				Expect(originalSAD).NotTo(BeEmpty(), "ptp-security-conf/ptp-security.conf is empty")

				By("creating the mutated SAD with key 1 removed, keeping key 2 for the clock under test")
				mutatedSAD, err := testconfig.PrepareAuthNegativeSAD(string(originalSAD))
				Expect(err).NotTo(HaveOccurred(), "could not prepare the isolated SAD")

				By("preparing the clock under test ptp4l config pointing to the isolated SAD and key 2")
				secretEntryName := authNegativeSecretEntryName()
				originalSAFile := "/etc/ptp-secret-mount/ptp-security-conf/ptp-security.conf"
				isolatedSAFile := "/etc/ptp-secret-mount/ptp-security-conf/" + secretEntryName
				mutatedConf, transmitInterfaces, err := testconfig.PrepareAuthNegativePtp4lConf(
					*profile.Ptp4lConf, originalSAFile, isolatedSAFile, slaveInterface)
				Expect(err).NotTo(HaveOccurred(), "could not prepare receiver profile %s", profileName)

				By("creating the state object to hold before/after configuration")
				state := &authNegativeState{
					ptpConfigName:     ptpConfig.Name,
					profileName:       profileName,
					nodeName:          nodeName,
					secretEntryName:   secretEntryName,
					configFile:        configFile,
					originalSAFile:    originalSAFile,
					isolatedSAFile:    isolatedSAFile,
					originalSAD:       append([]byte(nil), originalSAD...),
					originalPtp4lConf: *profile.Ptp4lConf,
				}
				DeferCleanup(restoreAuthNegativeState, state)

				By("adding the isolated SAD as a second key in the existing shared secret to avoid a linuxptp-daemon restart")
				err = setAuthNegativeSecretEntry(secretEntryName, []byte(mutatedSAD))
				Expect(err).NotTo(HaveOccurred(), "could not add isolated SAD entry %s", secretEntryName)

				By("verifying the new secret key is propagated to the linuxptp-daemon pod file system")
				expectedSADHash := sha256.Sum256([]byte(mutatedSAD))
				expectedSADHashText := hex.EncodeToString(expectedSADHash[:])
				Eventually(func() error {
					output, execErr := execInDaemonPodOnNode(nodeName, []string{"sha256sum", isolatedSAFile})
					if execErr != nil {
						return execErr
					}
					if fields := strings.Fields(output); len(fields) == 0 || fields[0] != expectedSADHashText {
						return fmt.Errorf("mounted isolated SAD hash has not converged (expected sha256 %s)", expectedSADHashText)
					}
					return nil
				}, pkg.TimeoutIn5Minutes, pkg.Timeout10Seconds).Should(Succeed(), "isolated SAD was not projected to the receiver pod")

				By("updating the clock under test with the prepared isolated SAD and key 2")
				observationStart := time.Now()
				err = updateAuthNegativeProfileConf(ptpConfig.Name, profileName, mutatedConf)
				Expect(err).NotTo(HaveOccurred(), "could not update receiver profile %s", profileName)
				Eventually(func() error {
					output, execErr := execInDaemonPodOnNode(nodeName, []string{"cat", configFile})
					if execErr != nil {
						return execErr
					}
					return checkAuthNegativeAppliedConfig(output, isolatedSAFile, transmitInterfaces)
				}, pkg.TimeoutIn5Minutes, pkg.Timeout10Seconds).Should(Succeed(), "receiver profile did not apply the isolated SAD and keep key 2 active")

				By("checking for a fresh authentication rejection on the selected receive port")
				// Per-profile log-tag matching is unnecessary here: the test enforces one
				// profile and exactly one ptp4l process on the node, and the since-timestamp
				// scopes the log window to the mutation. The (?m) flag is required because
				// GetPodLogsRegexSince appends "\s*^" as its full-line match convention.
				authErrorPattern := `(?m)port \d+ \(` + regexp.QuoteMeta(slaveInterface) + `\): auth: bad message`
				pod, err := ptphelper.GetPtpPodOnNode(nodeName)
				Expect(err).NotTo(HaveOccurred(), "could not refresh the clock-under-test daemon pod")
				_, err = pods.GetPodLogsRegexSince(pod.Namespace, pod.Name, pkg.PtpContainerName,
					authErrorPattern, false, pkg.TimeoutIn5Minutes, observationStart)
				Expect(err).NotTo(HaveOccurred(), "no fresh auth: bad message log for interface %s", slaveInterface)

				By("checking that the receive interface stays LISTENING with a stable ptp4l process after authentication rejection")
				baselinePID := -1
				Eventually(func() error {
					pid, err := sampleListeningPtp4l(nodeName, configFile, slaveInterface)
					baselinePID = pid
					return err
				}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(Succeed(),
					fmt.Sprintf("receive interface %s did not settle into LISTENING with a single ptp4l process for config %s on node %s",
						slaveInterface, configFile, nodeName))
				logrus.Infof("authentication negative test ptp4l baseline: config=%s pid=%d node=%s", configFile, baselinePID, nodeName)
				Consistently(func() (int, error) {
					return sampleListeningPtp4l(nodeName, configFile, slaveInterface)
				}, pkg.TimeoutIn30Seconds, pkg.Timeout10Seconds).Should(Equal(baselinePID),
					fmt.Sprintf("receive interface %s with ptp4l process for config %s did not stay LISTENING and running on node %s",
						slaveInterface, configFile, nodeName))

				By("restoring the original receiver profile and authentication SAD")
				Expect(restoreAuthNegativeState(state)).To(Succeed(), "could not restore the authentication negative test receiver state")
				pod, err = ptphelper.GetPtpPodOnNode(nodeName)
				Expect(err).NotTo(HaveOccurred(), "could not reacquire the receiver daemon pod after cleanup")
				fullConfig.DiscoveredClockUnderTestPod = &pod
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, ptpConfig, grandmasterID,
					metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(Succeed(), "receiver did not recover LOCKED/SLAVE sync with acceptable offset")
			})

			// Test That clock can sync in dual follower scenario when one port is down
			It("Dual follower can sync when one follower port goes down", func() {
				if fullConfig.PtpModeDesired != testconfig.DualFollowerClock {
					Skip("Test reserved for dual follower scenario")
				}
				Expect(len(fullConfig.DiscoveredFollowerInterfaces)).To(Equal(2), "dual follower requires exactly 2 follower interfaces")
				isExternalMaster := ptphelper.IsExternalGM()
				var grandmasterID *string
				if fullConfig.L2Config != nil && !isExternalMaster {
					aLabel := pkg.PtpGrandmasterNodeLabel
					var aString string
					Expect(fullConfig.DiscoveredGrandMasterPtpConfig).NotTo(BeNil(), "expected discovered grandmaster config")
					gmConfigName := (*ptpv1.PtpConfig)(fullConfig.DiscoveredGrandMasterPtpConfig).Name
					Eventually(func() error {
						var getErr error
						aString, getErr = ptphelper.GetClockIDMaster(gmConfigName, pkg.PtpGrandMasterPolicyName, &aLabel, nil, true)
						return getErr
					}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(BeNil(),
						"Timeout to get grandmaster clock ID")
					grandmasterID = &aString
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

				nodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName

				By("Port0: down")
				portEngine.TurnOffAndWaitFaulty(portEngine.Ports[0], nodeName)
				By("Check sync")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(BeNil())
				By("Check clock role")
				Eventually(func() error {
					return portEngine.CheckClockRole(portEngine.Ports[0], portEngine.Ports[1], metrics.MetricRoleFaulty, metrics.MetricRoleSlave)
				}, 120*time.Second, 1*time.Second).Should(BeNil())

				By("Port1: down")
				portEngine.TurnOffAndWaitFaulty(portEngine.Ports[1], nodeName)
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
				portEngine.TurnOnAndWaitSlave(portEngine.Ports[1], nodeName)
				By("Check sync")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(BeNil())
				By("Check clock role")
				Eventually(func() error {
					return portEngine.CheckClockRole(portEngine.Ports[0], portEngine.Ports[1], metrics.MetricRoleFaulty, metrics.MetricRoleSlave)
				}, 120*time.Second, 1*time.Second).Should(BeNil())

				By("Port0: up")
				portEngine.TurnOnAndWaitSlave(portEngine.Ports[0], nodeName)
				By("Check sync")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), grandmasterID, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(BeNil())
				By("Check clock role")
				Eventually(func() error {
					return portEngine.CheckClockRole(portEngine.Ports[0], portEngine.Ports[1], portEngine.InitialRoles[0], portEngine.InitialRoles[1])
				}, 120*time.Second, 1*time.Second).Should(BeNil())

				if !isExternalMaster {
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
				} else {
					logrus.Infof("Skipping GM removal/recreation test phase (external GM)")
				}
			})

			// Multinode BCSlave clock sync
			// - waits for the BCSlave foreign master to appear (the boundary clock)
			// - verifies that the BCSlave foreign master has the expected boundary clock ID
			// - use metrics to verify that the offset with boundary clock is below threshold
			It("Downstream slave can sync to BC master", func() {
				if fullConfig.PtpModeDiscovered != testconfig.BoundaryClock &&
					fullConfig.PtpModeDiscovered != testconfig.DualNICBoundaryClock &&
					fullConfig.PtpModeDiscovered != testconfig.DualNICBoundaryClockHA &&
					fullConfig.PtpModeDiscovered != testconfig.TelcoBoundaryClock &&
					fullConfig.PtpModeDiscovered != testconfig.TelcoGMBC {
					Skip("test only valid for Boundary clock or TGMBC in multi-node clusters")
				}

				// FoundSolutions lists every solvable L2 topology, not the one that was
				// applied. DualNIC often falls back to DualNicBC (no OC slaves) while
				// BCWithSlaves is still "found" on the same graph — gate on the
				// discovered downstream slave config that this test actually uses.
				if fullConfig.DiscoveredSlave1PtpConfig == nil {
					Skip("test only valid when a downstream OC slave ptpconfig is configured")
				}
				waitForWPCGMReady(fullConfig)
				// TGMBC + simulation: GNSS steps the BC PHC to GPS time while the OC PHC
				// stays near zero. E2E delay then collapses into a negative ~1e18 path
				// delay and the OC never reaches LOCKED. Align the OC PHC to wall clock
				// and replace the daemon pod so ptp4l remeasures delay in the same epoch
				// as the BC. The underlying mock PHC epoch mismatch belongs in netdevsim-dkms;
				// this path is scoped to simulation only and must not hide command failures.
				if fullConfig.PtpModeDiscovered == testconfig.TelcoGMBC && ptphelper.UseGnssSimulation() {
					By("Aligning downstream OC PHC and replacing daemon pod after WPC GM/BC are locked")
					slaveNodes, listErr := client.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
						LabelSelector: pkg.PtpSlave1NodeLabel,
					})
					Expect(listErr).NotTo(HaveOccurred())
					Expect(slaveNodes.Items).NotTo(BeEmpty(), "no node with label %s", pkg.PtpSlave1NodeLabel)
					slavePod, podErr := ptphelper.GetPtpPodOnNode(slaveNodes.Items[0].Name)
					Expect(podErr).NotTo(HaveOccurred())
					slaveIfs := ptpv1.GetInterfaces(*(*ptpv1.PtpConfig)(fullConfig.DiscoveredSlave1PtpConfig), ptpv1.Slave)
					Expect(slaveIfs).NotTo(BeEmpty(), "downstream OC has no slave interface")
					beforeKill := time.Now()
					_, _, alignErr := pods.ExecCommand(client.Client, true, &slavePod, pkg.PtpContainerName,
						[]string{"sh", "-c", fmt.Sprintf("phc_ctl %s set", slaveIfs[0])})
					Expect(alignErr).NotTo(HaveOccurred(), "failed to align OC PHC with phc_ctl")
					newSlavePod, replaceErr := ptphelper.ReplaceTestPod(&slavePod, pkg.TimeoutIn5Minutes)
					Expect(replaceErr).NotTo(HaveOccurred(), "failed to replace downstream OC daemon pod after PHC align")
					slavePod = newSlavePod
					ptphelper.WaitForPtpDaemonToBeReady([]*v1core.Pod{&slavePod})
					// isLiteralText=true: GetPodLogsRegexSince appends `\s*^` for regex
					// patterns, which never matches mid-line ptp4l port events.
					Eventually(func() bool {
						matches, _ := pods.GetPodLogsRegexSince(
							slavePod.Namespace, slavePod.Name, pkg.PtpContainerName,
							"UNCALIBRATED to SLAVE on MASTER_CLOCK_SELECTED", true, pkg.Timeout10Seconds, beforeKill)
						return len(matches) > 0
					}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(BeTrue(),
						"downstream OC ptp4l did not re-select SLAVE after PHC align/pod replace")
				}
				aLabel := pkg.PtpClockUnderTestNodeLabel
				name := pkg.PtpBcMaster1PolicyName
				Expect(fullConfig.DiscoveredClockUnderTestPtpConfig).ToNot(BeNil(), "BC mode requires DiscoveredClockUnderTestPtpConfig")
				bc1ConfigName := (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig).Name
				if fullConfig.PtpModeDiscovered == testconfig.TelcoBoundaryClock {
					name = "tbc-tr"
				}
				var masterIDBc1 string
				Eventually(func() error {
					var getErr error
					masterIDBc1, getErr = ptphelper.GetClockIDMaster(bc1ConfigName, name, &aLabel, nil, false)
					return getErr
				}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(BeNil(),
					"Timeout to get BC master1 clock ID")
				err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredSlave1PtpConfig), &masterIDBc1, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
				Expect(err).To(BeNil())

				if (fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClock || fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClockHA) &&
					fullConfig.DiscoveredSlave2PtpConfig != nil {
					aLabel := pkg.PtpClockUnderTestNodeLabel
					var masterIDBc2 string
					bc2ConfigName := bc1ConfigName
					if fullConfig.DiscoveredClockUnderTestSecondaryPtpConfig != nil {
						bc2ConfigName = (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestSecondaryPtpConfig).Name
					}
					Eventually(func() error {
						var getErr error
						masterIDBc2, getErr = ptphelper.GetClockIDMaster(bc2ConfigName, pkg.PtpBcMaster2PolicyName, &aLabel, nil, false)
						return getErr
					}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(BeNil(),
						"Timeout to get BC master2 clock ID")
					err = ptptesthelper.BasicClockSyncCheck(fullConfig, (*ptpv1.PtpConfig)(fullConfig.DiscoveredSlave2PtpConfig), &masterIDBc2, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
					Expect(err).To(BeNil())
				}

			})

			// 25743
			Context("Can provide a profile with higher priority", Ordered, ContinueOnFailure, func() {
				var (
					testPtpPod        v1core.Pod
					policyName        string
					modifiedPtpConfig *ptpv1.PtpConfig
					isExternalMaster  bool
					evCtx             bcEventContext
				)

				BeforeAll(func() {
					isExternalMaster = ptphelper.IsExternalGM()
					if fullConfig.PtpModeDesired == testconfig.Discovery {
						Skip("Skipping because adding a different profile and no modifications are allowed in discovery mode")
					}
					if fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClockHA {
						Skip("DualNICBCHA: temp profile conflicts with phc2sys HA haProfiles reference, causing master port to stay LISTENING")
					}
					By("Creating a config with higher priority", func() {
						if fullConfig.PtpModeDiscovered == testconfig.TelcoGrandMasterClock {
							Skip("WPC GM (T-GM) mode is not supported for this test")
						}
						// TGMBC keeps GM and BC on different nodes (solver StepSameNode
						// negative). The temp profile targets the BC (CUT) node only, so
						// it does not override the GM — run the TelcoGMBC branch below.
						switch fullConfig.PtpModeDiscovered {
						case testconfig.Discovery, testconfig.None:
							Skip("Skipping because Discovery or None is not supported yet for this test")
						case testconfig.OrdinaryClock, testconfig.TelcoGMOC:
							policyName = pkg.PtpSlave1PolicyName
						case testconfig.DualFollowerClock:
							policyName = pkg.PtpSlave1PolicyName
						case testconfig.BoundaryClock, testconfig.TelcoGMBC:
							policyName = pkg.PtpBcMaster1PolicyName
						case testconfig.DualNICBoundaryClock, testconfig.DualNICBoundaryClockHA:
							policyName = pkg.PtpBcMaster1PolicyName
						case testconfig.TelcoBoundaryClock:
							policyName = pkg.PTPWPCTBCPolicyName
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

						// Wait for operator to reconcile the config map with the
						// temp profile. The daemon watches the configmap and will
						// hot-reload ptp4l internally — no pod restart needed.
						err = ptphelper.WaitForConfigMapProfile(nodes.Items[0].Name, pkg.PtpTempPolicyName, 2*time.Minute)
						Expect(err).NotTo(HaveOccurred(), "operator did not reconcile temp profile into configmap in time")

						testPtpPod, err = ptphelper.GetPtpPodOnNode(nodes.Items[0].Name)
						Expect(err).NotTo(HaveOccurred())
					})

					By("Waiting for daemon to load the temp profile", func() {
						_, err := pods.GetPodLogsRegex(testPtpPod.Namespace,
							testPtpPod.Name, pkg.PtpContainerName,
							ptphelper.ProfileNameLogPattern(pkg.PtpTempPolicyName, pkg.PtpTempPolicyName), false, pkg.TimeoutIn3Minutes)
						Expect(err).NotTo(HaveOccurred(), "daemon did not load temp profile in time")
					})

					evCtx = setupBCClockClassEvents(testPtpPod.Spec.NodeName)
				})

				It("reports lock state LOCKED via cloud events under the higher-priority profile", func() {
					if !evCtx.available {
						Skip("PTP events not available")
					}
					waitForLockStateViaEvent(evCtx.subs, fullConfig.DiscoveredFollowerInterfaces[0], ptpEvent.LOCKED, pkg.TimeoutIn5Minutes)
				})

				It("Can provide a profile with higher priority", func() {
					By("Checking if Node has Profile and check sync", func() {
						waitForWPCGMReady(fullConfig)
						// Don't pass gmID upfront: creating the temp config triggers
						// operator reconciliation which may restart the GM daemon
						// asynchronously, changing its clock ID. First confirm the
						// slave is synced (role + offset metrics), then verify the
						// slave's master matches the GM's current clock.
						err = ptptesthelper.BasicClockSyncCheck(fullConfig, modifiedPtpConfig, nil, metrics.MetricClockStateLocked, metrics.MetricRoleSlave, true)
						Expect(err).To(BeNil())

						if fullConfig.L2Config != nil && !isExternalMaster {
							aLabel := pkg.PtpGrandmasterNodeLabel
							gmProfileName := pkg.PtpGrandMasterPolicyName
							if fullConfig.PtpModeDiscovered == testconfig.TelcoGMOC || fullConfig.PtpModeDiscovered == testconfig.TelcoGMBC {
								gmProfileName = pkg.PtpWPCGrandMasterPolicyName
							}
							var gmClockID string
							Expect(fullConfig.DiscoveredGrandMasterPtpConfig).NotTo(BeNil(), "expected discovered grandmaster config")
							gmConfigName := (*ptpv1.PtpConfig)(fullConfig.DiscoveredGrandMasterPtpConfig).Name
							Eventually(func() error {
								var getErr error
								gmClockID, getErr = ptphelper.GetClockIDMaster(gmConfigName, gmProfileName, &aLabel, nil, true)
								return getErr
							}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(BeNil(),
								"Timeout to get grandmaster clock ID")

							profileName, err := ptphelper.GetProfileName(modifiedPtpConfig, true)
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
								var slaveMaster string
								Eventually(func() error {
									var getErr error
									slaveMaster, getErr = ptphelper.GetClockIDForeign(modifiedPtpConfig.Name, profileName, label, node)
									if getErr != nil {
										return getErr
									}
									if !strings.HasPrefix(slaveMaster, gmClockID) {
										return fmt.Errorf("slave master %s does not match GM clock %s", slaveMaster, gmClockID)
									}
									return nil
								}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(BeNil(),
									"Timeout waiting for slave to follow expected GM")
							}
						}
					})
				})

				It("reverts to the original profile after deleting the higher-priority profile", func() {
					By("Deleting the test profile", func() {
						err := client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Delete(context.Background(), pkg.PtpTempPolicyName, metav1.DeleteOptions{})
						Expect(err).NotTo(HaveOccurred())
						Eventually(func() bool {
							_, err := client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpTempPolicyName, metav1.GetOptions{})
							return kerrors.IsNotFound(err)
						}, 1*time.Minute, 1*time.Second).Should(BeTrue(), "Could not delete the test profile")
					})

					By("Checking the profile is reverted", func() {
						profileLogPattern := ptphelper.ProfileNameLogPattern(policyName, policyName)
						profileFilePattern := ptphelper.ProfileNameMatchPattern(policyName, policyName)
						if policyName == pkg.PTPWPCTBCPolicyName {
							tbcPattern := "(?:" +
								ptphelper.ProfileNameMatchPattern(policyName, "tbc-tr") + "|" +
								ptphelper.ProfileNameMatchPattern(policyName, "tbc-tt") + ")"
							profileLogPattern = `(?m)Profile Name: ` + tbcPattern
							profileFilePattern = tbcPattern
						}
						_, err := pods.GetPodLogsRegex(testPtpPod.Namespace,
							testPtpPod.Name, pkg.PtpContainerName,
							profileLogPattern, false, pkg.TimeoutIn3Minutes)
						if err != nil {
							// Fallback to file-based search to get the profile name
							// for when logs do not contain the profile.
							_, err = ptphelper.GetConfigForProfileFromVarRun(profileFilePattern, &testPtpPod, pkg.PtpContainerName)
							if err != nil {
								Fail(fmt.Sprintf("could not get profile name, err=%s", err))
							}
						}
					})
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

				// HA member profile matchers for openshift_ptp_ha_profile_status (ACTIVE
				// for the phc2sys-selected member, INACTIVE for the other).
				primaryProfile := regexp.MustCompile(`profile="` + ptphelper.ProfileNameMatchPattern(primaryPtpConfig.Name, *primaryPtpConfig.Spec.Profile[0].Name) + `"`)
				secondaryProfile := regexp.MustCompile(`profile="` + ptphelper.ProfileNameMatchPattern(secondaryPtpConfig.Name, *secondaryPtpConfig.Spec.Profile[0].Name) + `"`)
				haNodeName := &fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName

				// phc2sys is delayed until ptp4l synchronizes, so first wait for
				// the primary BC slave interface to reach SLAVE state, then give
				// phc2sys a short window to emit its initial interface selection.
				By("Waiting for ptp4l to synchronize on primary BC slave interface")
				slaveRoles := make([]metrics.MetricRole, len(primaryBCSlaveInterfaces))
				for i := range slaveRoles {
					slaveRoles[i] = metrics.MetricRoleSlave
				}
				Eventually(func() error {
					return metrics.CheckClockRole(slaveRoles, primaryBCSlaveInterfaces, &fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(BeNil(),
					"Primary BC slave interface must reach SLAVE state before phc2sys starts")

				checkStatusByProcess(fullConfig, "phc2sys", "1")

				// Get phc2sys logs to identify which interface it's using.
				const phc2sysLogPattern = `phc2sys(?m).*?:.* selecting (\w+) as out-of-domain source clock`
				var selectedInterface string

				logMatches, err := pods.GetPodLogsRegex(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName,
					phc2sysLogPattern, false, pkg.TimeoutIn1Minute)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(logMatches)).To(BeNumerically(">=", 1), "Could not identify which interface phc2sys is using")
				logrus.Infof("phc2sys log matching line: %v", logMatches[len(logMatches)-1][0])
				selectedInterface = logMatches[len(logMatches)-1][1]

				// Save it as primary interface
				primaryInterface := selectedInterface
				By("Verifying the selected interface " + selectedInterface + " is a primary BC's slave interface")
				// Check if the selected interface belongs to the primary boundary clock config
				if !slices.Contains(primaryBCSlaveInterfaces, selectedInterface) {
					Fail(fmt.Sprintf("Selected interface %s does not belong to the primary boundary clock config. Primary interfaces: %v", selectedInterface, primaryBCSlaveInterfaces))
				}

				By("Verifying ha_profile_status reports the primary profile ACTIVE and the secondary INACTIVE")
				Eventually(func() error {
					if err := metrics.CheckHAProfileStatus(primaryProfile, true, haNodeName); err != nil {
						return err
					}
					return metrics.CheckHAProfileStatus(secondaryProfile, false, haNodeName)
				}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(BeNil(),
					"primary HA profile must be ACTIVE and secondary INACTIVE while phc2sys uses the primary")

				// Wait for some time to ensure the regex won't match the previous log entry
				time.Sleep(2 * time.Second)
				ifDownTime := time.Now()
				By("Taking down the selected interface " + selectedInterface)

				nodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
				portEngine.TurnOffAndWaitFaulty(selectedInterface, nodeName)

				By("Waiting for 5 seconds for the new interface to be selected")
				time.Sleep(5 * time.Second)

				By("Verifying phc2sys switches to a different interface")
				var newSelectedInterface string
				logMatches, err = pods.GetPodLogsRegexSince(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName,
					phc2sysLogPattern, false, pkg.TimeoutIn1Minute, ifDownTime)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(logMatches)).To(BeNumerically(">=", 1), "Could not identify which interface phc2sys switched to after primary interface failed")
				Expect(ptptesthelper.CountPhc2sysTransitions(logMatches, selectedInterface)).To(Equal(1),
					fmt.Sprintf("phc2sys should have made exactly 1 transition (primary->secondary) after primary failed; port may be flapping. Sequence: %v",
						ptptesthelper.Phc2sysMatchedInterfaces(logMatches)))
				logrus.Infof("phc2sys log matching line: %v", logMatches[len(logMatches)-1][0])
				newSelectedInterface = logMatches[len(logMatches)-1][1]

				Expect(newSelectedInterface).ToNot(Equal(selectedInterface), "phc2sys should have switched to a different interface")
				By("Verifying the new selected interface " + newSelectedInterface + " is a secondary BC's slave interface")
				if !slices.Contains(secondaryBCSlaveInterfaces, newSelectedInterface) {
					Fail(fmt.Sprintf("Selected interface %s does not belong to the secondary boundary clock config. Secondary interfaces: %v", newSelectedInterface, secondaryBCSlaveInterfaces))
				}

				By("Verifying ha_profile_status flips to the secondary profile ACTIVE after failover")
				Eventually(func() error {
					if err := metrics.CheckHAProfileStatus(secondaryProfile, true, haNodeName); err != nil {
						return err
					}
					return metrics.CheckHAProfileStatus(primaryProfile, false, haNodeName)
				}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(BeNil(),
					"secondary HA profile must become ACTIVE and primary INACTIVE after failover")

				time.Sleep(2 * time.Second)
				ifUpTime := time.Now()
				By("Restoring the primary BC's slave interface " + primaryInterface)
				portEngine.TurnOnAndWaitSlave(primaryInterface, nodeName)

				By("Waiting 5 seconds for the primary BC's slave interface to be selected again")
				time.Sleep(5 * time.Second)

				// On recovery, ptp4l briefly promotes the interface to MASTER before a better master
				// is found and it transitions to SLAVE. This causes one phc2sys transition:
				// secondary (during brief MASTER state) -> primary (on SLAVE transition).
				// More than one transition indicates flapping (e.g. primary->secondary->primary).
				logMatches, err = pods.GetPodLogsRegexSince(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName,
					phc2sysLogPattern, false, pkg.TimeoutIn1Minute, ifUpTime)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(logMatches)).To(BeNumerically(">=", 1), "Could not identify which interface phc2sys switched to after primary interface recovered")
				Expect(ptptesthelper.CountPhc2sysTransitions(logMatches, newSelectedInterface)).To(Equal(1),
					fmt.Sprintf("phc2sys should have made exactly 1 transition (secondary->primary) since primary recovered; "+
						"0 means phc2sys never switched back, >1 indicates flapping. Sequence: %v",
						ptptesthelper.Phc2sysMatchedInterfaces(logMatches)))
				logrus.Infof("phc2sys log matching line: %v", logMatches[len(logMatches)-1][0])
				selectedInterface = logMatches[len(logMatches)-1][1]

				By("Verifying the selected interface " + selectedInterface + " is the original primary BC's slave interface " + primaryInterface)
				if selectedInterface != primaryInterface {
					Fail(fmt.Sprintf("Selected interface %s is not the original primary interface %s", selectedInterface, primaryInterface))
				}

				By("Verifying ha_profile_status returns to the primary profile ACTIVE after recovery")
				Eventually(func() error {
					if err := metrics.CheckHAProfileStatus(primaryProfile, true, haNodeName); err != nil {
						return err
					}
					return metrics.CheckHAProfileStatus(secondaryProfile, false, haNodeName)
				}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(BeNil(),
					"primary HA profile must be ACTIVE again and secondary INACTIVE after recovery")
			})

		})

		// OCPBUGS-66407 / OCPBUGS-59883: after locking the PTP source, the
		// clockClass reported via cloud events and Prometheus metrics must
		// match PMC.
		//
		// The test framework creates BC configs with gmCapable=0, but real partner
		// deployments default to gmCapable=1 (ptp4l default). Without gmCapable=1,
		// ptp4l won't promote itself to GM on sync loss and won't report clockClass
		// 248. BeforeAll edits the config in place (gmCapable=1 + clockClass 248)
		// and restores it via DeferCleanup.
		Context("clockClass when locking PTP source on single NIC boundary clock", Ordered, ContinueOnFailure, func() {
			const eventTimeout = pkg.TimeoutIn5Minutes
			freerun := ClockClassFreerun          // 248
			locked := int(fbprotocol.ClockClass6) // 6
			var (
				nic1     ptptesthelper.NICInfo
				nodeName string
				evCtx    bcEventContext
			)

			BeforeAll(func() {
				ptphelper.WaitForPtpDaemonToExist()
				fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				if fullConfig.PtpModeDiscovered != testconfig.BoundaryClock {
					Skip("Test only valid for single NIC Boundary Clock")
				}
				if fullConfig.PtpModeDesired == testconfig.Discovery {
					Skip("Test not valid in discovery mode")
				}
				podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
				Expect(err).NotTo(HaveOccurred())
				ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

				nodeName = fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
				nic1 = ptptesthelper.DiscoverNICInfo(*(*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), nodeName, "NIC-1")

				By("Updating BC config in-place: gmCapable 0 -> gmCapable 1 + clockClass 248")
				originalConf := ptptesthelper.EnableGMCapableInPlace(nic1.PtpConfigName)
				DeferCleanup(func() {
					ptptesthelper.RestorePtp4lConf(nic1.PtpConfigName, originalConf)
				})
				By("Waiting for PTP daemon to apply gmCapable=1 config")
				ptptesthelper.WaitForConfigContent(fullConfig, nic1.ConfigPath(), "gmCapable 1")

				evCtx = setupBCClockClassEvents(nodeName)
			})

			AfterAll(func() {
				if err := portEngine.TurnAllPortsUp(); err != nil {
					logrus.Warnf("TurnAllPortsUp during cleanup failed: %v", err)
				}
			})

			It("Step 1: initial state reports clockClass 6 via cloud events", func() {
				if !evCtx.available {
					Skip("PTP events not available")
				}
				waitForClockClassViaEvent(evCtx.subs, nic1.SlaveIf, locked, eventTimeout)
			})
			It("Step 1: initial state reports clockClass 6 via metrics", func() {
				ptptesthelper.VerifyNICClockClass(fullConfig, nic1, locked, false)
			})

			It("Step 2: locks NIC-1 PTP source by bringing slave interface down", func() {
				if evCtx.available {
					evCtx.subs.Drain()
				}
				By(fmt.Sprintf("Bringing down %s", nic1.SlaveIf))
				Expect(portEngine.TurnPortDown(nic1.SlaveIf)).To(Succeed())
			})
			It("Step 2: freerun state reports clockClass 248 via cloud events", func() {
				if !evCtx.available {
					Skip("PTP events not available")
				}
				waitForClockClassViaEvent(evCtx.subs, nic1.SlaveIf, freerun, eventTimeout)
			})
			It("Step 2: freerun state reports clockClass 248 via metrics", func() {
				ptptesthelper.VerifyNICClockClass(fullConfig, nic1, freerun, false)
			})

			It("Step 3: unlocks NIC-1 PTP source by bringing slave interface up", func() {
				if evCtx.available {
					evCtx.subs.Drain()
				}
				By(fmt.Sprintf("Bringing up %s", nic1.SlaveIf))
				Expect(portEngine.TurnPortUp(nic1.SlaveIf)).To(Succeed())
			})
			It("Step 3: recovered state reports clockClass 6 via cloud events", func() {
				if !evCtx.available {
					Skip("PTP events not available")
				}
				waitForClockClassViaEvent(evCtx.subs, nic1.SlaveIf, locked, eventTimeout)
			})
			It("Step 3: recovered state reports clockClass 6 via metrics", func() {
				ptptesthelper.VerifyNICClockClass(fullConfig, nic1, locked, false)
			})
		})

		// Dual-NIC boundary clock: after locking each PTP source, verify the
		// clockClass via cloud events (node-level) and per-NIC via metrics.
		Context("clockClass when locking PTP source on dual NIC boundary clock", Ordered, ContinueOnFailure, func() {
			const eventTimeout = pkg.TimeoutIn5Minutes
			freerun := ClockClassFreerun          // 248
			locked := int(fbprotocol.ClockClass6) // 6
			var (
				nic1, nic2 ptptesthelper.NICInfo
				nodeName   string
				evCtx      bcEventContext
			)

			BeforeAll(func() {
				ptphelper.WaitForPtpDaemonToExist()
				fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				if fullConfig.PtpModeDiscovered != testconfig.DualNICBoundaryClock &&
					fullConfig.PtpModeDiscovered != testconfig.DualNICBoundaryClockHA {
					Skip("Test only valid for dual NIC Boundary Clock configurations")
				}
				if fullConfig.PtpModeDesired == testconfig.Discovery {
					Skip("Test not valid in discovery mode")
				}
				podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
				Expect(err).NotTo(HaveOccurred())
				ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

				nodeName = fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
				nic1 = ptptesthelper.DiscoverNICInfo(*(*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig), nodeName, "NIC-1")
				secondaryPtpConfig := (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestSecondaryPtpConfig)
				Expect(secondaryPtpConfig).ToNot(BeNil(), "Secondary PtpConfig not found for dual NIC")
				nic2 = ptptesthelper.DiscoverNICInfo(*secondaryPtpConfig, nodeName, "NIC-2")

				By("Updating BC configs in-place: gmCapable 0 -> gmCapable 1 + clockClass 248")
				originalNic1Conf := ptptesthelper.EnableGMCapableInPlace(nic1.PtpConfigName)
				originalNic2Conf := ptptesthelper.EnableGMCapableInPlace(nic2.PtpConfigName)
				DeferCleanup(func() {
					logrus.Info("Restoring original BC configs")
					ptptesthelper.RestorePtp4lConf(nic1.PtpConfigName, originalNic1Conf)
					ptptesthelper.RestorePtp4lConf(nic2.PtpConfigName, originalNic2Conf)
				})
				By("Waiting for PTP daemon to apply gmCapable=1 config")
				ptptesthelper.WaitForConfigContent(fullConfig, nic1.ConfigPath(), "gmCapable 1")
				ptptesthelper.WaitForConfigContent(fullConfig, nic2.ConfigPath(), "gmCapable 1")

				evCtx = setupBCClockClassEvents(nodeName)
			})

			AfterAll(func() {
				if err := portEngine.TurnAllPortsUp(); err != nil {
					logrus.Warnf("TurnAllPortsUp during cleanup failed: %v", err)
				}
			})

			It("Step 1: all NICs report clockClass 6 via cloud events", func() {
				if !evCtx.available {
					Skip("PTP events not available")
				}
				waitForClockClassViaEvent(evCtx.subs, nic1.SlaveIf, locked, eventTimeout)
			})
			It("Step 1: NIC-1 reports clockClass 6 via metrics", func() {
				ptptesthelper.VerifyNICClockClass(fullConfig, nic1, locked, true)
			})
			It("Step 1: NIC-2 reports clockClass 6 via metrics", func() {
				ptptesthelper.VerifyNICClockClass(fullConfig, nic2, locked, true)
			})

			It("Step 2: locks NIC-2 PTP source by bringing slave interface down", func() {
				if evCtx.available {
					evCtx.subs.Drain()
				}
				By(fmt.Sprintf("Bringing down %s", nic2.SlaveIf))
				Expect(portEngine.TurnPortDown(nic2.SlaveIf)).To(Succeed())
			})
			It("Step 3: freerun clockClass 248 appears via cloud events", func() {
				if !evCtx.available {
					Skip("PTP events not available")
				}
				waitForClockClassViaEvent(evCtx.subs, nic2.SlaveIf, freerun, eventTimeout)
			})
			It("Step 3: NIC-1 still reports clockClass 6 via metrics", func() {
				ptptesthelper.VerifyNICClockClass(fullConfig, nic1, locked, true)
			})
			It("Step 3: NIC-2 reports freerun clockClass 248 via metrics", func() {
				ptptesthelper.VerifyNICClockClass(fullConfig, nic2, freerun, true)
			})

			It("Step 4: swaps sources — locks NIC-1 (down), unlocks NIC-2 (up)", func() {
				if evCtx.available {
					evCtx.subs.Drain()
				}
				By(fmt.Sprintf("Bringing down %s and up %s", nic1.SlaveIf, nic2.SlaveIf))
				Expect(portEngine.TurnPortDown(nic1.SlaveIf)).To(Succeed())
				Expect(portEngine.TurnPortUp(nic2.SlaveIf)).To(Succeed())
			})
			It("Step 5: freerun clockClass 248 appears via cloud events", func() {
				if !evCtx.available {
					Skip("PTP events not available")
				}
				waitForClockClassViaEvent(evCtx.subs, nic1.SlaveIf, freerun, eventTimeout)
			})
			It("Step 5: NIC-1 reports freerun clockClass 248 via metrics", func() {
				ptptesthelper.VerifyNICClockClass(fullConfig, nic1, freerun, true)
			})
			It("Step 5: NIC-2 reports recovered clockClass 6 via metrics", func() {
				ptptesthelper.VerifyNICClockClass(fullConfig, nic2, locked, true)
			})
		})

		Context("PTP metric is present", func() {
			BeforeEach(func() {
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				})
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
				Expect(err).NotTo(HaveOccurred())
				ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

				_, err = pods.GetPodLogsRegex(fullConfig.DiscoveredClockUnderTestPod.Namespace,
					fullConfig.DiscoveredClockUnderTestPod.Name, pkg.PtpContainerName,
					"Profile Name:", true, pkg.TimeoutIn3Minutes)
				if err != nil {
					// "Profile Name:" is printed once at daemon start. On a long
					// GNSS run kubelet rotates the container log (~10MB) and the
					// line is gone, even though the daemon is still serving the
					// profile. The match itself is unused; the metrics endpoint
					// is the same signal the following spec checks.
					metricsReady := false
					Eventually(func() bool {
						buf, _, mErr := pods.ExecCommand(client.Client, false, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						if mErr != nil {
							return false
						}
						metricsReady = strings.Contains(buf.String(), metrics.OpenshiftPtpOffsetNs)
						return metricsReady
					}, pkg.TimeoutIn1Minute, 5*time.Second).Should(BeTrue(),
						"could not get slave profile name and daemon metrics are not available: %v", err)
					logrus.Warnf("Profile Name line is not in the current container log; daemon metrics are present: %v", err)
				}

			})

			// 27324
			It("verifies on slave", func() {
				if fullConfig.PtpModeDiscovered == testconfig.TelcoGrandMasterClock {
					Skip("Skipping: clock-under-test is a standalone TGM profile")
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
					requireDiscoveryAfterRefresh(&fullConfig, testconfig.None)
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
				waitForWPCGMReady(fullConfig)
			})

			// Subscribe to PTP events and assert an event payload is delivered to
			// the consumer, exercising the O-RAN pub/sub path end to end (v1
			// sidecar transport or v2 in-daemon proxy).
			It("Should deliver ptp events to a subscribed consumer", func() {
				apiVersion := ptphelper.PtpEventEnabled()

				By("Checking the cloud-event-proxy sidecar is present")
				cloudProxyFound := false
				Expect(len(fullConfig.DiscoveredClockUnderTestPod.Spec.Containers)).To(BeNumerically("==", 3),
					"linuxptp-daemon is not deployed on cluster with cloud event proxy")
				for _, c := range fullConfig.DiscoveredClockUnderTestPod.Spec.Containers {
					if c.Name == pkg.EventProxyContainerName {
						cloudProxyFound = true
					}
				}
				Expect(cloudProxyFound).To(BeTrue(), "No cloud-event-proxy container detected")

				By("Deploying a consumer that subscribes to PTP events")
				nodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
				Expect(nodeName).ToNot(BeEmpty(), "clock-under-test pod node is empty")
				var err error
				if apiVersion == 1 {
					// v1 delivers events over the cloud-event-proxy sidecar.
					err = event.CreateConsumerAppWithSidecar(nodeName)
				} else {
					err = event.CreateConsumerApp(nodeName)
				}
				if err != nil {
					Skip(fmt.Sprintf("Consumer app setup failed: %v", err))
				}
				DeferCleanup(func() {
					_ = event.DeleteConsumerNamespace()
					if event.PubSub != nil {
						event.PubSub.Close()
					}
				})
				// v2 exposes a publisher health endpoint to poll; v1 readiness
				// was handled above.
				if apiVersion != 1 {
					if waitErr := event.WaitForConsumerReady(nodeName); waitErr != nil {
						Skip(fmt.Sprintf("Consumer app not ready: %v", waitErr))
					}
				}

				By("Listening for events delivered to the subscribed consumer")
				event.InitPubSub()
				term, monErr := event.MonitorPodLogsRegex()
				Expect(monErr).ToNot(HaveOccurred(), "could not start listening to events")
				DeferCleanup(func() { stopMonitor(term) })

				By("Verifying a PTP event is delivered to the consumer (subscribe + read)")
				Expect(event.PushInitialEvent(string(ptpEvent.SyncStateChange), 2*time.Minute)).
					To(Succeed(), "did not receive a SyncStateChange event via the subscribed consumer")
			})

			// The event pipeline also exports Prometheus metrics.
			It("Should expose ptp event metrics", func() {
				By("Checking interface role metrics are present")
				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, false, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", pkg.MetricsEndPoint})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpInterfaceRole),
					"Interface role metrics are not detected")

				By("Checking threshold metrics are present")
				Eventually(func() string {
					buf, _, _ := pods.ExecCommand(client.Client, false, fullConfig.DiscoveredClockUnderTestPod, pkg.EventProxyContainerName, []string{"curl", pkg.MetricsEndPoint})
					return buf.String()
				}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpThreshold),
					"Threshold metrics are not detected")
			})

			// clockClass recovery after a cloud-event-proxy crash. BeforeAll
			// crashes the proxy once; each It then verifies recovery via the
			// Event API, PMC, and metrics.
			Context("clockClass recovery after cloud-event-proxy crash", Ordered, ContinueOnFailure, func() {
				var (
					expectedClockClass    fbprotocol.ClockClass
					expectedClockClassStr string
					crashDone             bool
					basePushCount         int
				)

				BeforeAll(func() {
					if ptphelper.PtpEventEnabled() != 2 {
						Skip("Skipping: test applies to event API v2 only")
					}

					// Determine expected clock class based on PTP mode.
					// In 4.21+, a single-iface OC follower (including TGMOC's
					// downstream OC) reports its local clock class (255/SlaveOnly)
					// instead of the upstream GM's class (6). PMC gm.ClockClass
					// still reports 6.
					expectedClockClass = fbprotocol.ClockClass6
					cutIsOrdinaryFollower := fullConfig.PtpModeDiscovered == testconfig.OrdinaryClock ||
						fullConfig.PtpModeDiscovered == testconfig.TelcoGMOC
					if cutIsOrdinaryFollower && ptphelper.IsPTPOperatorVersionAtLeast("4.21") {
						expectedClockClass = fbprotocol.ClockClassSlaveOnly
					}
					expectedClockClassStr = strconv.Itoa(int(expectedClockClass))
					logrus.Infof("PtpModeDiscovered: %s, expected clockClass: %s", fullConfig.PtpModeDiscovered, expectedClockClassStr)

					By("Deploying consumer app for event API v2")
					nodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
					Expect(nodeName).ToNot(BeEmpty(), "clock-under-test pod node is empty")
					if err := event.CreateConsumerApp(nodeName); err != nil {
						Skip(fmt.Sprintf("Consumer app setup failed: %v", err))
					}
					if waitErr := event.WaitForConsumerReady(nodeName); waitErr != nil {
						Skip(fmt.Sprintf("Consumer app not ready: %v", waitErr))
					}
					event.InitPubSub()

					// Baseline via the metrics-independent paths: Event API and PMC.
					By(fmt.Sprintf("Verifying initial clockClass is %s via Event API", expectedClockClassStr))
					verifyClockClassCurrentState(int(expectedClockClass), 60*time.Second)

					// PMC gm.ClockClass always reports the upstream GM's class (6),
					// regardless of whether this node is BC or OC.
					By("Verifying initial gm.ClockClass is 6 via PMC")
					ptptesthelper.VerifyClockClassViaPMC(fullConfig, "/var/run/ptp4l.0.config", int(fbprotocol.ClockClass6))

					By("Killing cloud-event-proxy process in sidecar container")
					// Baseline count of pushed events delivered to the consumer
					// before the crash. After the restart, a strict increase proves
					// a fresh push was delivered to the still-subscribed consumer.
					// A count delta is immune to runner/node clock skew.
					var countErr error
					basePushCount, countErr = event.ConsumerPushedEventCount(time.Time{})
					Expect(countErr).NotTo(HaveOccurred(), "could not read baseline consumer pushed-event count")

					cepPod := refreshDaemonPod(fullConfig.DiscoveredClockUnderTestPod)
					killCloudEventProxy(cepPod)
					By("Waiting for cloud-event-proxy to be ready")
					Expect(event.WaitForCloudEventProxyReady(cepPod)).To(BeNil(),
						"cloud-event-proxy did not become ready after restart")
					fullConfig.DiscoveredClockUnderTestPod = refreshDaemonPod(cepPod)
					waitForWPCGMReady(fullConfig)

					// One CEP restart only: wait for metrics, then Event API CurrentState
					// within 2 minutes. Extra CEP bounces would let a failed first recovery
					// still go green on a later retry.
					By(fmt.Sprintf("Waiting for clockClass %s via metrics after CEP restart", expectedClockClassStr))
					checkClockClassState(fullConfig, expectedClockClassStr, pkg.TimeoutIn5Minutes)
					crashDone = true
				})

				AfterAll(func() {
					if event.PubSub != nil {
						event.PubSub.Close()
					}
					if err := event.DeleteConsumerNamespace(); err != nil {
						logrus.Debugf("Deleting consumer namespace failed: %s", err)
					}
				})

				It("recovers clockClass via Event API after cloud-event-proxy restart", func() {
					Expect(crashDone).To(BeTrue(), "cloud-event-proxy crash setup did not complete")
					By(fmt.Sprintf("Verifying clockClass is %s via Event API after restart", expectedClockClassStr))
					verifyClockClassCurrentState(int(expectedClockClass), 2*time.Minute)
				})

				It("recovers gm.ClockClass via PMC after cloud-event-proxy restart", func() {
					Expect(crashDone).To(BeTrue(), "cloud-event-proxy crash setup did not complete")
					By("Verifying gm.ClockClass remains 6 via PMC after restart")
					ptptesthelper.VerifyClockClassViaPMC(fullConfig, "/var/run/ptp4l.0.config", int(fbprotocol.ClockClass6))
				})

				It("recovers clockClass via metrics after cloud-event-proxy restart", func() {
					Expect(crashDone).To(BeTrue(), "cloud-event-proxy crash setup did not complete")
					By(fmt.Sprintf("Verifying clockClass remains %s via metrics after restart", expectedClockClassStr))
					checkClockClassState(fullConfig, expectedClockClassStr, pkg.TimeoutIn5Minutes)
				})

				// Proves subscriptions persist across cloud-event-proxy process runs
				It("delivers pushed events to the subscribed consumer after cloud-event-proxy restart", func() {
					if !event.IsCEPV2() {
						Skip("subscription persistence applies to the CEPv2 cloud-event-proxy only")
					}
					Expect(crashDone).To(BeTrue(), "cloud-event-proxy crash setup did not complete")
					By("Verifying a new event is pushed to the already-subscribed consumer after restart")
					Eventually(func() (int, error) {
						return event.ConsumerPushedEventCount(time.Time{})
					}, 3*time.Minute, pkg.TimeoutInterval5Seconds).Should(BeNumerically(">", basePushCount),
						"no new event was pushed to the subscribed consumer after the cloud-event-proxy restart")
				})
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

					By("Checking cloud-event-proxy v2 health endpoint on all pods")
					for _, pod := range ptpPods.Items {
						cloudProxyFound := false
						for _, c := range pod.Spec.Containers {
							if c.Name == pkg.EventProxyContainerName {
								cloudProxyFound = true
							}
						}
						if !cloudProxyFound {
							continue
						}

						logrus.Infof("Checking cloud-event-proxy v2 API on pod %s (node: %s)", pod.Name, pod.Spec.NodeName)

						Eventually(func() string {
							buf, _, _ := pods.ExecCommand(client.Client, true, &pod, pkg.EventProxyContainerName,
								[]string{"curl", "-s", "--max-time", "5", event.ApiBaseV2 + "/health"})
							return buf.String()
						}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(ContainSubstring("OK"),
							fmt.Sprintf("cloud-event-proxy v2 health endpoint not responding on pod %s", pod.Name))

						logrus.Infof("Pod %s: cloud-event-proxy v2 API is healthy", pod.Name)
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

					By("Checking cloud-event-proxy v2 health endpoint on all pods")
					for _, pod := range ptpPods.Items {
						cloudProxyFound := false
						for _, c := range pod.Spec.Containers {
							if c.Name == pkg.EventProxyContainerName {
								cloudProxyFound = true
							}
						}
						if !cloudProxyFound {
							continue
						}

						logrus.Infof("Checking cloud-event-proxy v2 API on pod %s (node: %s)", pod.Name, pod.Spec.NodeName)

						Eventually(func() string {
							buf, _, _ := pods.ExecCommand(client.Client, true, &pod, pkg.EventProxyContainerName,
								[]string{"curl", "-s", "--max-time", "5", event.ApiBaseV2 + "/health"})
							return buf.String()
						}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(ContainSubstring("OK"),
							fmt.Sprintf("cloud-event-proxy v2 health endpoint not responding on pod %s", pod.Name))

						logrus.Infof("Pod %s: cloud-event-proxy v2 API is healthy", pod.Name)
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

				fifoPriorities = nil
				for _, config := range ptpConfigs {
					for _, profile := range config.Spec.Profile {
						if profile.PtpSchedulingPolicy != nil && *profile.PtpSchedulingPolicy == "SCHED_FIFO" {
							if profile.Name != nil && profile.PtpSchedulingPriority != nil {
								fifoPriorities = append(fifoPriorities, struct {
									configName  string
									profileName string
									priority    int64
								}{config.Name, *profile.Name, *profile.PtpSchedulingPriority})
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
				for i := 0; i < len(fifoPriorities); {
					fp := fifoPriorities[i]
					ptp4lLog := fmt.Sprintf("/bin/chrt -f %d /usr/sbin/ptp4l", fp.priority)
					matched := false
					for podIndex := range ptpPods.Items {
						profileLog := ptphelper.ProfileNameLogPattern(fp.configName, fp.profileName)
						_, err := pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							profileLog, false, pkg.TimeoutIn3Minutes)
						if err != nil {
							logrus.Errorf("error getting profile=%s/%s, err=%s ", fp.configName, fp.profileName, err)
							continue
						}
						_, err = pods.GetPodLogsRegex(ptpPods.Items[podIndex].Namespace,
							ptpPods.Items[podIndex].Name, pkg.PtpContainerName,
							ptp4lLog, true, pkg.TimeoutIn3Minutes)
						if err != nil {
							logrus.Errorf("error getting ptp4l chrt line=%s, err=%s ", ptp4lLog, err)
							continue
						}
						matched = true
						break
					}
					if matched {
						fifoPriorities = append(fifoPriorities[:i], fifoPriorities[i+1:]...)
					} else {
						i++
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
				Expect(fullConfig.DiscoveredClockUnderTestPod).NotTo(BeNil(),
					"clock-under-test pod missing after refresh; label node with "+pkg.PtpClockUnderTestNodeLabel)
				Expect(fullConfig.DiscoveredClockUnderTestPtpConfig).NotTo(BeNil(),
					"clock-under-test PtpConfig missing after refresh")
				if fullConfig.PtpModeDiscovered == testconfig.TelcoGMBC {
					waitForWPCGMReady(fullConfig)
				}
			})

			It("The slave node network interface is taken down and up", func() {
				if fullConfig.PtpModeDiscovered == testconfig.TelcoGrandMasterClock {
					Skip("test not valid for standalone TGM config")
				}
				if fullConfig.PtpModeDesired == testconfig.DualFollowerClock {
					Skip("Test not valid for dual follower scenario")
				}

				skippedInterfacesStr, isSet := os.LookupEnv("SKIP_INTERFACES")
				if !isSet {
					Skip("Mandatory to provide skipped interface to avoid making a node disconnected from the cluster")
				}
				skipInterfaces := make(map[string]bool)
				for _, val := range strings.Split(skippedInterfacesStr, ",") {
					skipInterfaces[val] = true
				}

				slaveIf := ptpv1.GetInterfaces((ptpv1.PtpConfig)(*fullConfig.DiscoveredClockUnderTestPtpConfig), ptpv1.Slave)
				nodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName

				By("Taking all slave interfaces down")
				var toggledIfaces []string
				for _, iface := range slaveIf {
					if skipInterfaces[iface] {
						logrus.Infof("Skipping interface %s", iface)
						continue
					}
					logrus.Infof("Simulating PTP outage on interface %s", iface)
					portEngine.TurnOffAndWaitFaulty(iface, nodeName)
					toggledIfaces = append(toggledIfaces, iface)
				}

				By("Restoring all slave interfaces and verifying sync")
				for _, iface := range toggledIfaces {
					portEngine.TurnOnAndWaitSlave(iface, nodeName)

					Eventually(func() error {
						offsetVal, err := metrics.GetPtpOffeset(iface, &nodeName)
						if err != nil {
							return err
						}
						if offsetVal < metrics.MinOffsetNs || offsetVal >= metrics.MaxOffsetNs {
							return fmt.Errorf("offset %d out of bounds [%d, %d)", offsetVal, metrics.MinOffsetNs, metrics.MaxOffsetNs)
						}
						return nil
					}, 2*time.Minute, 5*time.Second).Should(Succeed(),
						fmt.Sprintf("offset for %s should be within bounds", iface))
				}
			})

			// OsClockSyncState goes FREERUN when BC upstream is lost (reverse phc2sys)
			// and recovers to LOCKED when the link is restored.
			//
			// Reproduces Case 04504543 / reverse-sync regression after OCPBUGS-88369:
			// with phc2sysOpts -a -r, losing the BC slave causes phc2sys to reverse
			// (PHC ← CLOCK_REALTIME) and stop emitting CLOCK_REALTIME phc offset.
			// cloud-event-proxy must publish os-clock-sync-state FREERUN.
			//
			// Run with: PTP_TEST_MODE=BC (or DualNICBC) ENABLE_PTP_EVENT=true SKIP_INTERFACES=<mgmt,...>
			// Requires a cloud-event-proxy build that includes reverse-sync FREERUN detection.
			// Skips when phc2sysOpts lacks -a -r or CLOCK_REALTIME metric is absent.
			// Kind/netdevsim: runs when VRT is present (DisableAllSlaveRTUpdate=false); otherwise skips.
			It("OsClockSyncState goes FREERUN on BC upstream loss and recovers to LOCKED", func() {
				if fullConfig.PtpModeDiscovered != testconfig.BoundaryClock &&
					fullConfig.PtpModeDiscovered != testconfig.DualNICBoundaryClock {
					Skip("test only valid for BC and DualNICBC (not DualNICBCHA HA)")
				}
				if !bcProfileHasNetworkDisciplinedOsClock((*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig)) {
					Skip("requires phc2sysOpts with -a -r for CLOCK_REALTIME reverse-sync; " +
						"on Kind/netdevsim ensure VRT is present so run-tests.sh sets " +
						"DisableAllSlaveRTUpdate=false")
				}

				skippedInterfacesStr, isSet := os.LookupEnv("SKIP_INTERFACES")
				if !isSet {
					Skip("Mandatory to provide skipped interface to avoid making a node disconnected from the cluster")
				}
				skipInterfaces := make(map[string]bool)
				for _, val := range strings.Split(skippedInterfacesStr, ",") {
					skipInterfaces[val] = true
				}

				slaveIf := ptpv1.GetInterfaces((ptpv1.PtpConfig)(*fullConfig.DiscoveredClockUnderTestPtpConfig), ptpv1.Slave)
				if fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClock {
					Expect(fullConfig.DiscoveredClockUnderTestSecondaryPtpConfig).NotTo(BeNil(),
						"secondary PtpConfig required for dual-NIC BC outage tests")
					secondarySlaveIf := ptpv1.GetInterfaces((ptpv1.PtpConfig)(*fullConfig.DiscoveredClockUnderTestSecondaryPtpConfig), ptpv1.Slave)
					slaveIf = append(slaveIf, secondarySlaveIf...)
				}
				Expect(slaveIf).ToNot(BeEmpty(), "no slave interfaces found in the clock-under-test PtpConfig(s)")

				nodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName

				By("Probing CLOCK_REALTIME/phc2sys metric availability")
				if !clockRealTimeMetricAvailable(nodeName, 30*time.Second) {
					Skip("openshift_ptp_clock_state{iface=CLOCK_REALTIME,process=phc2sys} not available; " +
						"on Kind/netdevsim enable VRT so each worker has a stand-in CLOCK_REALTIME PHC")
				}

				portEngine.Initialize(fullConfig.DiscoveredClockUnderTestPod, slaveIf)
				DeferCleanup(func() {
					ptptesthelper.DeletePtpTestPrivilegedDaemonSet(
						pkg.RecoveryNetworkOutageDaemonSetName,
						pkg.RecoveryNetworkOutageDaemonSetNamespace,
					)
				})

				evCtx := setupOsClockSyncStateEvents(nodeName)

				By("Checking initial CLOCK_REALTIME / OsClockSyncState is LOCKED")
				Eventually(func() error {
					return metrics.CheckClockRealTimeState(metrics.MetricClockStateLocked, &nodeName)
				}, pkg.TimeoutIn5Minutes, pkg.Timeout10Seconds).Should(Succeed(),
					"CLOCK_REALTIME must be LOCKED before outage")
				if evCtx.available {
					Expect(event.PushInitialEvent(string(ptpEvent.OsClockSyncStateChange), 30*time.Second)).To(Succeed())
					waitForOsClockSyncState(evCtx.ch, ptpEvent.LOCKED, pkg.TimeoutIn3Minutes)
				}

				By("Taking all BC slave interfaces down to force phc2sys reverse-sync")
				outageStart := time.Now()
				err := portEngine.TurnAllPortsDown(skipInterfaces)
				Expect(err).To(BeNil())
				DeferCleanup(func() {
					_ = portEngine.TurnAllPortsUp()
				})

				By("Correlating phc2sys reverse-sync log signals (best-effort)")
				correlatePhc2sysReverseSync(fullConfig.DiscoveredClockUnderTestPod, outageStart)

				By("Checking CLOCK_REALTIME metric is FREERUN after upstream loss")
				Eventually(func() error {
					return metrics.CheckClockRealTimeState(metrics.MetricClockStateFreeRun, &nodeName)
				}, pkg.TimeoutIn5Minutes, pkg.Timeout10Seconds).Should(Succeed(),
					"CLOCK_REALTIME must go FREERUN when BC upstream is lost (reverse phc2sys); "+
						"stuck LOCKED indicates missing reverse-sync detection in cloud-event-proxy")

				if evCtx.available {
					By("Checking OsClockSyncStateChange event is FREERUN")
					waitForOsClockSyncState(evCtx.ch, ptpEvent.FREERUN, pkg.TimeoutIn5Minutes)
				}

				By("Restoring all BC slave interfaces")
				err = portEngine.TurnAllPortsUp()
				Expect(err).To(BeNil())

				By("Checking CLOCK_REALTIME metric recovers to LOCKED")
				Eventually(func() error {
					return metrics.CheckClockRealTimeState(metrics.MetricClockStateLocked, &nodeName)
				}, pkg.TimeoutIn5Minutes, pkg.Timeout10Seconds).Should(Succeed(),
					"CLOCK_REALTIME must return to LOCKED after upstream recovers")

				if evCtx.available {
					By("Checking OsClockSyncStateChange event recovers to LOCKED")
					waitForOsClockSyncState(evCtx.ch, ptpEvent.LOCKED, pkg.TimeoutIn5Minutes)
				}

				logrus.Info("Successfully verified OsClockSyncState FREERUN/LOCKED around BC reverse-sync outage")
			})

			It("Should restart ptp4l with no socket errors after SIGTERM", func() {
				verifyProcessRestartNoSocketErrors(fullConfig, "ptp4l")
			})

			It("Should restart phc2sys with no socket errors after SIGTERM", func() {
				verifyProcessRestartNoSocketErrors(fullConfig, "phc2sys")
			})

			It("Should restart ts2phc with no socket errors after SIGTERM", func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("ts2phc only runs in GM configuration")
				}
				verifyProcessRestartNoSocketErrors(fullConfig, "ts2phc")
			})
		})

		// BC clock class recovery after an upstream link outage. The degraded
		// phase has no fixed clockClass to assert (gmCapable is not forced here,
		// only that the clock leaves Locked/6), so the two Locked states are
		// verified via cloud events and the degraded state via metrics.
		Context("BC clock class recovery after upstream link outage", Ordered, ContinueOnFailure, func() {
			const eventTimeout = pkg.TimeoutIn5Minutes
			lockedStr := strconv.Itoa(int(fbprotocol.ClockClass6))
			lockedCC := int(fbprotocol.ClockClass6)
			var (
				slaveIf          []string
				slavePodNodeName string
				faultyRoles      []metrics.MetricRole
				slaveRoles       []metrics.MetricRole
				evCtx            bcEventContext
			)

			BeforeAll(func() {
				ptphelper.WaitForPtpDaemonToExist()
				fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				if fullConfig.Status == testconfig.DiscoveryFailureStatus {
					Skip("Failed to find a valid ptp slave configuration")
				}
				if fullConfig.PtpModeDiscovered == testconfig.TelcoGrandMasterClock {
					Skip("test not valid for WPC GM config")
				}
				if fullConfig.PtpModeDesired == testconfig.DualFollowerClock {
					Skip("Test not valid for dual follower scenario")
				}
				if fullConfig.PtpModeDiscovered != testconfig.BoundaryClock &&
					fullConfig.PtpModeDiscovered != testconfig.DualNICBoundaryClock &&
					fullConfig.PtpModeDiscovered != testconfig.DualNICBoundaryClockHA {
					Skip("test only valid for Boundary Clock configurations")
				}
				if _, isSet := os.LookupEnv("SKIP_INTERFACES"); !isSet {
					Skip("Mandatory to provide skipped interface to avoid making a node disconnected from the cluster")
				}
				Expect(fullConfig.DiscoveredClockUnderTestPod).NotTo(BeNil(),
					"clock-under-test pod missing after refresh; label node with "+pkg.PtpClockUnderTestNodeLabel)
				Expect(fullConfig.DiscoveredClockUnderTestPtpConfig).NotTo(BeNil(),
					"clock-under-test PtpConfig missing after refresh")
				podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
				Expect(err).NotTo(HaveOccurred())
				ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)

				slaveIf = ptpv1.GetInterfaces((ptpv1.PtpConfig)(*fullConfig.DiscoveredClockUnderTestPtpConfig), ptpv1.Slave)
				if fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClock ||
					fullConfig.PtpModeDiscovered == testconfig.DualNICBoundaryClockHA {
					Expect(fullConfig.DiscoveredClockUnderTestSecondaryPtpConfig).NotTo(BeNil(),
						"secondary PtpConfig required for dual-NIC BC outage tests")
					secondarySlaveIf := ptpv1.GetInterfaces((ptpv1.PtpConfig)(*fullConfig.DiscoveredClockUnderTestSecondaryPtpConfig), ptpv1.Slave)
					logrus.Infof("Secondary BC slave interfaces are %+q", secondarySlaveIf)
					slaveIf = append(slaveIf, secondarySlaveIf...)
				}
				Expect(slaveIf).ToNot(BeEmpty(), "no slave interfaces found in the clock-under-test PtpConfig(s)")
				logrus.Infof("All slave interfaces are %+q", slaveIf)
				slavePodNodeName = fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName

				faultyRoles = make([]metrics.MetricRole, len(slaveIf))
				for i := range faultyRoles {
					faultyRoles[i] = metrics.MetricRoleFaulty
				}
				slaveRoles = make([]metrics.MetricRole, len(slaveIf))
				for i := range slaveRoles {
					slaveRoles[i] = metrics.MetricRoleSlave
				}

				portEngine.Initialize(fullConfig.DiscoveredClockUnderTestPod, slaveIf)
				DeferCleanup(func() {
					ptptesthelper.DeletePtpTestPrivilegedDaemonSet(
						pkg.RecoveryNetworkOutageDaemonSetName,
						pkg.RecoveryNetworkOutageDaemonSetNamespace,
					)
				})

				evCtx = setupBCClockClassEvents(slavePodNodeName)
			})

			AfterAll(func() {
				if err := portEngine.TurnAllPortsUp(); err != nil {
					logrus.Warnf("TurnAllPortsUp during cleanup failed: %v", err)
				}
			})

			It("initial state reports clock class Locked (6) via cloud events", func() {
				if !evCtx.available {
					Skip("PTP events not available")
				}
				waitForClockClassViaEvent(evCtx.subs, slaveIf[0], lockedCC, eventTimeout)
			})
			It("initial state reports clock class Locked (6) via metrics", func() {
				checkClockClassState(fullConfig, lockedStr, pkg.TimeoutIn5Minutes)
			})

			It("takes all slave interfaces down (simulate upstream outage)", func() {
				skippedInterfacesStr, isSet := os.LookupEnv("SKIP_INTERFACES")
				if !isSet {
					Skip("Mandatory to provide skipped interface to avoid making a node disconnected from the cluster")
				}
				skipInterfaces := make(map[string]bool)
				for _, val := range strings.Split(skippedInterfacesStr, ",") {
					skipInterfaces[val] = true
				}
				Expect(portEngine.TurnAllPortsDown(skipInterfaces)).To(BeNil())
			})
			It("degraded state: all slave port roles are FAULTY via metrics", func() {
				Eventually(func() error {
					return metrics.CheckClockRole(faultyRoles, slaveIf, &slavePodNodeName)
				}, 5*time.Minute, 10*time.Second).Should(BeNil())
			})
			It("degraded state: clock class leaves Locked (6) via metrics", func() {
				Eventually(func() bool {
					return !checkClockClassStateReturnBool(fullConfig, lockedStr)
				}, 5*time.Minute, 10*time.Second).Should(BeTrue(),
					"expected clock class to degrade from Locked (6) after upstream link loss")
			})

			It("brings all slave interfaces back up", func() {
				if evCtx.available {
					evCtx.subs.Drain()
				}
				Expect(portEngine.TurnAllPortsUp()).To(BeNil())
			})
			It("recovered state reports clock class Locked (6) via cloud events", func() {
				if !evCtx.available {
					Skip("PTP events not available")
				}
				waitForClockClassViaEvent(evCtx.subs, slaveIf[0], lockedCC, eventTimeout)
			})
			It("recovered state: all slave port roles are SLAVE via metrics", func() {
				Eventually(func() error {
					return metrics.CheckClockRole(slaveRoles, slaveIf, &slavePodNodeName)
				}, 5*time.Minute, 10*time.Second).Should(BeNil())
			})
			It("recovered state: clock class recovers to Locked (6) via metrics", func() {
				waitForClockClass(fullConfig, lockedStr)
			})
		})

		Context("WPC GM Verification Tests", func() {
			BeforeEach(func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for standalone TGM config")
				}
				if ptphelper.UseGnssSimulation() {
					Skip("WPC GM verification expects gpsd/hardware GNSS; gnss-sim PTY is in use")
				}
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					requireDiscoveryAfterRefresh(&fullConfig, testconfig.TelcoGrandMasterClock)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)
				})
			})
			It("is verifying WPC GM state based on logs", func() {
				checkStatusByProcess(fullConfig, "phc2sys", "1")

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
					Skip("test valid only for standalone TGM config")
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
					checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)), pkg.TimeoutIn5Minutes)

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
					Skip("test valid only for standalone TGM config")
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

			It("Verify ts2phc clock state is not LOCKED after GM PtpConfig is deleted", func() {
				By("Verifying ts2phc clock state is LOCKED")
				checkClockStateForProcess(fullConfig, "ts2phc", "1")

				Expect(testconfig.GlobalConfig.DiscoveredGrandMasterPtpConfig).NotTo(BeNil(),
					"DiscoveredGrandMasterPtpConfig must not be nil")
				gmConfigName := testconfig.GlobalConfig.DiscoveredGrandMasterPtpConfig.Name

				By(fmt.Sprintf("Deleting GM PtpConfig %s", gmConfigName))
				err := client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Delete(
					context.Background(), gmConfigName, metav1.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred())

				DeferCleanup(func() {
					By("Recreating GM PtpConfig after test")
					ensureGMPtpConfigRestored()
				})

				By("Waiting for ts2phc clock state metric to be removed")
				Eventually(func() bool {
					buf, _, _ := pods.ExecCommand(client.Client, true,
						fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName,
						[]string{"curl", pkg.MetricsEndPoint})
					metricsOutput := buf.String()
					if !strings.Contains(metricsOutput, "# HELP") {
						return false
					}
					_, found := getClockStateByProcess(metricsOutput, "ts2phc")
					return !found
				}, pkg.TimeoutIn5Minutes, pkg.Timeout10Seconds).Should(BeTrue(),
					"Expected ts2phc clock state metric to be removed after GM PtpConfig deletion")
			})

			It("Verify ts2phc metrics are cleaned up after GM PtpConfig delete and re-apply", func() {
				Expect(testconfig.GlobalConfig.DiscoveredGrandMasterPtpConfig).NotTo(BeNil(),
					"DiscoveredGrandMasterPtpConfig must not be nil")
				gmConfigName := testconfig.GlobalConfig.DiscoveredGrandMasterPtpConfig.Name

				By("Ensuring ts2phc is running and recording its config file name and restart count")
				var initialConfigName string
				var initialRestartCount int
				Eventually(func() bool {
					buf, _, _ := pods.ExecCommand(client.Client, true,
						fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName,
						[]string{"curl", pkg.MetricsEndPoint})
					initialConfigName, initialRestartCount, _ = getProcessConfigAndRestartCount(buf.String(), "ts2phc")
					return initialConfigName != ""
				}, pkg.TimeoutIn3Minutes, pkg.Timeout10Seconds).Should(BeTrue(),
					"ts2phc process_status with config name not found in metrics")
				fmt.Fprintf(GinkgoWriter, "Initial ts2phc config=%s, restart_count=%d\n",
					initialConfigName, initialRestartCount)

				By(fmt.Sprintf("Deleting GM PtpConfig %s", gmConfigName))
				err := client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Delete(
					context.Background(), gmConfigName, metav1.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred())

				DeferCleanup(func() {
					By("Ensuring GM PtpConfig is restored after test")
					ensureGMPtpConfigRestored()
				})

				By("Waiting for ts2phc process to stop after deletion")
				Eventually(func() bool {
					buf, _, _ := pods.ExecCommand(client.Client, true,
						fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName,
						[]string{"curl", pkg.MetricsEndPoint})
					metricsOutput := buf.String()
					if !strings.Contains(metricsOutput, "# HELP") {
						return false
					}
					configName, _, _ := getProcessConfigAndRestartCount(metricsOutput, "ts2phc")
					return configName == ""
				}, pkg.TimeoutIn5Minutes, pkg.Timeout10Seconds).Should(BeTrue(),
					"ts2phc process did not stop after GM PtpConfig deletion")

				By("Re-applying the same GM PtpConfig")
				tempPtpConfig := (*ptpv1.PtpConfig)(testconfig.GlobalConfig.DiscoveredGrandMasterPtpConfig)
				tempPtpConfig.SetResourceVersion("")
				_, err = client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Create(
					context.Background(), tempPtpConfig, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())

				By("Waiting for ts2phc to come back up after re-apply")
				var newConfigName string
				var newRestartCount int
				Eventually(func() bool {
					buf, _, _ := pods.ExecCommand(client.Client, true,
						fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName,
						[]string{"curl", pkg.MetricsEndPoint})
					newConfigName, newRestartCount, _ = getProcessConfigAndRestartCount(buf.String(), "ts2phc")
					return newConfigName != ""
				}, pkg.TimeoutIn5Minutes, pkg.Timeout10Seconds).Should(BeTrue(),
					"ts2phc did not come back up after GM PtpConfig re-apply")
				fmt.Fprintf(GinkgoWriter, "After re-apply: ts2phc config=%s, restart_count=%d\n",
					newConfigName, newRestartCount)

				By("Verifying config name is preserved and metrics are consistent after re-apply")
				Expect(newConfigName).To(Equal(initialConfigName),
					fmt.Sprintf("ts2phc config name should be preserved after re-apply, got %s (initial was %s)",
						newConfigName, initialConfigName))
				Expect(newRestartCount).To(BeNumerically(">=", initialRestartCount),
					fmt.Sprintf("ts2phc restart count should continue after re-apply (not reset), got %d (initial was %d)",
						newRestartCount, initialRestartCount))
			})

		})

		Context("WPC GM GNSS signal loss tests", func() {
			BeforeEach(func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for standalone TGM config")
				}
				if ptphelper.UseGnssSimulation() {
					Skip("ubxtool/GNSS loss tests require hardware GNSS; gnss-sim PTY is in use")
				}
				fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				requireDiscoveryAfterRefresh(&fullConfig, testconfig.TelcoGrandMasterClock)
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
					checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)), pkg.TimeoutIn5Minutes)

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
					Skip("test valid only for standalone TGM config")
				}
				if ptphelper.UseGnssSimulation() {
					Skip("ts2phc termination GM event tests target hardware GNSS path")
				}
				fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				requireDiscoveryAfterRefresh(&fullConfig, testconfig.TelcoGrandMasterClock)
			})

			It("Continuously terminating ts2phc triggers FREERUN event and then recovers to LOCKED", func() {
				By("Ensure initial state is LOCKED via clock class 6")
				checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)), pkg.TimeoutIn5Minutes)

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
					Skip("test valid only for standalone TGM config")
				}
				if ptphelper.UseGnssSimulation() {
					Skip("WPC GM v1 events expect hardware GNSS")
				}
			})
		})

		Context("WPC GM Events verification (V2)", func() {
			BeforeEach(func() {
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("test valid only for standalone TGM config")
				}
				if ptphelper.UseGnssSimulation() {
					Skip("WPC GM v2 GNSS reboot events expect ubxtool/hardware GNSS (use Simulated T-GM events when gnss-sim is active)")
				}
				fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
				requireDiscoveryAfterRefresh(&fullConfig, testconfig.TelcoGrandMasterClock)

				// Set up consumer pod for event monitoring
				if fullConfig.DiscoveredClockUnderTestPod != nil {
					nodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
					if nodeName != "" {
						logrus.Info("Deploy consumer app for testing event API v2")
						err := event.CreateConsumerApp(nodeName)
						Expect(err).ToNot(HaveOccurred(), "Consumer app setup failed")

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
				checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)), pkg.TimeoutIn5Minutes)

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
				By("Verifying GNSS state Locked")
				verifyEvent(events[ptpEvent.GnssStateChange], ptpEvent.LOCKED)
				By("Verifying ClockClass value to 6")
				verifyMetric(events[ptpEvent.PtpClockClassChange], float64(fbprotocol.ClockClass6))
				By("Verifying PTP state Locked")
				verifyEvent(events[ptpEvent.PtpStateChange], ptpEvent.LOCKED)

			})
		})

		Context("Simulated T-GM Verification Tests", func() {
			BeforeEach(func() {
				if !ptphelper.IsGnssSimulatedCI() {
					Skip("test valid only when gnss-sim PTY is used (hardware GNSS not present on cluster)")
				}
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("simulated T-GM config was not discovered as TelcoGrandMasterClock")
				}
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					requireDiscoveryAfterRefresh(&fullConfig, testconfig.TelcoGrandMasterClock)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)
				})
			})

			It("is verifying simulated T-GM process status (without gpsd/gpspipe)", func() {
				By("checking that gnss-sim is healthy", func() {
					Eventually(func() bool {
						return ptphelper.GNSSSimIsHealthy()
					}, pkg.TimeoutIn1Minute, 5*time.Second).Should(BeTrue(),
						"gnss-sim health check failed")
				})

				gmPod := getGMPod()

				By("checking sim GM required processes status (ts2phc, ptp4l, phc2sys)", func() {
					processesArr := [...]string{"ts2phc", "ptp4l", "phc2sys"}
					for _, val := range processesArr {
						logMatches, err := pods.GetPodLogsRegex(openshiftPtpNamespace, gmPod.Name, pkg.PtpContainerName, val, true, pkg.TimeoutIn1Minute)
						Expect(err).To(BeNil(), fmt.Sprintf("Error encountered looking for %s", val))
						Expect(logMatches).ToNot(BeEmpty(), fmt.Sprintf("Expected %s to be running for simulated GM", val))
					}
				})

				By("checking sim GM process metrics (skip gpsd/gpspipe; phc2sys -r is required)", func() {
					Eventually(func() string {
						buf, _, _ := pods.ExecCommand(client.Client, true, gmPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						return buf.String()
					}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(ContainSubstring(metrics.OpenshiftPtpProcessStatus),
						"Process status metrics are not detected")

					Eventually(func() bool {
						buf, _, _ := pods.ExecCommand(client.Client, true, gmPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						ret, err := processRunningSimGM(buf.String(), "1")
						if err != nil {
							return false
						}
						return ret["ptp4l"] && ret["ts2phc"] && ret["phc2sys"]
					}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(BeTrue(),
						"Expected ptp4l, ts2phc, and phc2sys to report process_status 1 for simulated GM")
				})

				By("checking CLOCK_REALTIME is LOCKED via phc2sys -r", func() {
					nodeName := gmPod.Spec.NodeName
					Eventually(func() error {
						return metrics.CheckClockRealTimeState(metrics.MetricClockStateLocked, &nodeName)
					}, pkg.TimeoutIn5Minutes, pkg.Timeout10Seconds).Should(Succeed(),
						"simulated T-GM CLOCK_REALTIME (phc2sys -r) must reach LOCKED")
				})
			})

			It("is verifying simulated T-GM clock state via metrics", func() {
				gmPod := getGMPod()

				By("checking clock class state is locked (CC6)", func() {
					Eventually(func() bool {
						buf, _, _ := pods.ExecCommand(client.Client, true, gmPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						return checkPtp4lClockClassInMetrics(buf.String(), strconv.Itoa(int(fbprotocol.ClockClass6)))
					}, pkg.TimeoutIn5Minutes, pkg.Timeout1Seconds).Should(BeTrue(),
						"Expected GM ptp4l clock class to eventually be 6 (LOCKED)")
				})

				By("checking PTP NMEA status for ts2phc", func() {
					nmeaStatusPattern := `openshift_ptp_nmea_status\{(?:from="[^"]+",)?iface="([^"]+)",node="([^"]+)",process="([^"]+)"\} (\d+)`
					nmeaStatusRe := regexp.MustCompile(nmeaStatusPattern)
					Eventually(func() bool {
						buf, _, _ := pods.ExecCommand(client.Client, true, gmPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						scanner := bufio.NewScanner(strings.NewReader(buf.String()))
						for scanner.Scan() {
							if matches := nmeaStatusRe.FindStringSubmatch(scanner.Text()); matches != nil {
								if strings.TrimSpace(matches[3]) == "ts2phc" && strings.TrimSpace(matches[4]) == "1" {
									return true
								}
							}
						}
						return false
					}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(BeTrue(),
						"Expected ts2phc nmea_status 1 on GM pod")
				})

				By("checking GM clock state locked via metrics", func() {
					Eventually(func() bool {
						buf, _, _ := pods.ExecCommand(client.Client, true, gmPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						ret, err := clockStateByProcesses(buf.String(), "1")
						if err != nil {
							return false
						}
						return ret["GM"]
					}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(BeTrue(),
						"Expected GM clock state to be 1 (LOCKED)")
				})
			})

			It("is verifying simulated T-GM DPLL state via gnss-sim API", func() {
				gmPod := getGMPod()

				By("checking DPLL is in LOCKED state via gnss-sim API", func() {
					Eventually(func() string {
						dpllState, err := ptphelper.GNSSSimGetDPLLState()
						if err != nil {
							logrus.Warnf("gnss-sim DPLL query failed: %v", err)
							return ""
						}
						return dpllState.State
					}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(Equal("LOCKED"),
						"Expected gnss-sim DPLL to be in LOCKED state")
				})

				By("verifying DPLL clock class is 6 via gnss-sim API", func() {
					dpllState, err := ptphelper.GNSSSimGetDPLLState()
					Expect(err).ToNot(HaveOccurred())
					Expect(dpllState.ClockClass).To(Equal(6), "Expected gnss-sim DPLL clock class 6")
					Expect(dpllState.FrequencyStatus).To(Equal(3), "Expected gnss-sim DPLL frequency_status 3 (LOCKED_HO_ACQ)")
					Expect(dpllState.PhaseStatus).To(Equal(3), "Expected gnss-sim DPLL phase_status 3 (LOCKED_HO_ACQ)")
					Expect(dpllState.PPSStatus).To(Equal(1), "Expected gnss-sim DPLL pps_status 1")
				})

				By("verifying daemon DPLL frequency_status and phase_status are LOCKED (3)", func() {
					Eventually(func() bool {
						buf, _, _ := pods.ExecCommand(client.Client, true, gmPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						return dpllMetricStatusLocked(buf.String(), "openshift_ptp_frequency_status") &&
							dpllMetricStatusLocked(buf.String(), "openshift_ptp_phase_status")
					}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(BeTrue(),
						"Expected openshift_ptp_frequency_status and phase_status process=dpll to be 3 (LOCKED)")
				})
			})
		})

		Context("Simulated T-GM GNSS signal loss tests", func() {
			BeforeEach(func() {
				if !ptphelper.IsGnssSimulatedCI() {
					Skip("test valid only when gnss-sim PTY is used (hardware GNSS not present on cluster)")
				}
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("simulated T-GM config was not discovered as TelcoGrandMasterClock")
				}
				waitForWPCGMReady(fullConfig)
			})

			It("Testing simulated T-GM holdover through GNSS signal loss via API", func() {
				gmPod := getGMPod()

				waitForGMClockClass := func(expected string) {
					Eventually(func() bool {
						buf, _, _ := pods.ExecCommand(client.Client, true, gmPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						return checkPtp4lClockClassInMetrics(buf.String(), expected)
					}, pkg.TimeoutIn3Minutes, 2*time.Second).Should(BeTrue(),
						fmt.Sprintf("Expected GM ptp4l clock class to reach %s", expected))
				}
				waitForGMClockState := func(expected string) {
					Eventually(func() bool {
						buf, _, _ := pods.ExecCommand(client.Client, true, gmPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
						ret, err := clockStateByProcesses(buf.String(), expected)
						return err == nil && ret["GM"]
					}, pkg.TimeoutIn3Minutes, 2*time.Second).Should(BeTrue(),
						fmt.Sprintf("Expected GM clock_state process=GM to be %s", expected))
				}

				By("Ensuring initial stability: clock class 6, DPLL LOCKED, GM clock_state LOCKED", func() {
					waitForGMClockClass(strconv.Itoa(int(fbprotocol.ClockClass6)))
					waitForGMClockState("1")
					Eventually(func() string {
						dpll, err := ptphelper.GNSSSimGetDPLLState()
						if err != nil {
							return ""
						}
						return dpll.State
					}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(Equal("LOCKED"))
				})

				By("Triggering GNSS signal loss via gnss-sim API", func() {
					err := ptphelper.GNSSSimSignalLoss()
					Expect(err).ToNot(HaveOccurred(), "failed to trigger signal loss on gnss-sim")
				})
				defer func() {
					_ = ptphelper.GNSSSimSignalRestore()
				}()

				By("Waiting for DPLL to transition to HOLDOVER via gnss-sim API", func() {
					Eventually(func() string {
						dpll, err := ptphelper.GNSSSimGetDPLLState()
						if err != nil {
							return ""
						}
						return dpll.State
					}, pkg.TimeoutIn3Minutes, 2*time.Second).Should(Equal("HOLDOVER"),
						"Expected DPLL to transition to HOLDOVER after signal loss")
				})

				By("Verifying DPLL clock class transitions to 7 (holdover in-spec)", func() {
					dpll, err := ptphelper.GNSSSimGetDPLLState()
					Expect(err).ToNot(HaveOccurred())
					Expect(dpll.ClockClass).To(Equal(7))
				})

				By("Waiting for ptp4l clock class 7 and GM clock_state HOLDOVER in Prometheus metrics", func() {
					waitForGMClockClass(strconv.Itoa(int(fbprotocol.ClockClass7)))
					waitForGMClockState("2")
				})

				By("Waiting for DPLL to transition to FREERUN after holdover timeout", func() {
					Eventually(func() string {
						dpll, err := ptphelper.GNSSSimGetDPLLState()
						if err != nil {
							return ""
						}
						return dpll.State
					}, pkg.TimeoutIn3Minutes, 2*time.Second).Should(Equal("FREERUN"),
						"Expected DPLL to transition to FREERUN after holdover timeout")
				})

				By("Verifying DPLL clock class transitions to 248 (freerun)", func() {
					dpll, err := ptphelper.GNSSSimGetDPLLState()
					Expect(err).ToNot(HaveOccurred())
					Expect(dpll.ClockClass).To(Equal(248))
				})

				By("Waiting for GM clock_state FREERUN in Prometheus metrics", func() {
					waitForGMClockState("0")
				})

				By("Restoring GNSS signal via gnss-sim API", func() {
					err := ptphelper.GNSSSimSignalRestore()
					Expect(err).ToNot(HaveOccurred(), "failed to restore signal on gnss-sim")
				})

				By("Waiting for DPLL to recover to LOCKED", func() {
					Eventually(func() string {
						dpll, err := ptphelper.GNSSSimGetDPLLState()
						if err != nil {
							return ""
						}
						return dpll.State
					}, pkg.TimeoutIn3Minutes, 2*time.Second).Should(Equal("LOCKED"),
						"Expected DPLL to recover to LOCKED after signal restore")
				})

				By("Verifying clock class returns to 6", func() {
					dpll, err := ptphelper.GNSSSimGetDPLLState()
					Expect(err).ToNot(HaveOccurred())
					Expect(dpll.ClockClass).To(Equal(6))
				})

				By("Waiting for ptp4l clock class 6 and GM clock_state LOCKED in Prometheus metrics", func() {
					waitForGMClockClass(strconv.Itoa(int(fbprotocol.ClockClass6)))
					waitForGMClockState("1")
				})
			})
		})

		Context("Simulated T-GM Events verification (V2)", func() {
			BeforeEach(func() {
				if !ptphelper.IsGnssSimulatedCI() {
					Skip("test valid only when gnss-sim PTY is used (hardware GNSS not present on cluster)")
				}
				if fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
					Skip("simulated T-GM config was not discovered as TelcoGrandMasterClock")
				}
				waitForWPCGMReady(fullConfig)

				gmPod := getGMPod()
				nodeName := gmPod.Spec.NodeName
				if nodeName != "" {
					logrus.Info("Deploy consumer app for simulated T-GM event API v2")
					err := event.CreateConsumerApp(nodeName)
					Expect(err).ToNot(HaveOccurred(), "Consumer app setup failed")
					Expect(event.WaitForConsumerReady(nodeName)).To(BeNil(), "Consumer app not ready")
					event.InitPubSub()
				}
			})

			AfterEach(func() {
				DeferCleanup(func() {
					err := event.DeleteConsumerNamespace()
					if err != nil {
						logrus.Debugf("Deleting consumer namespace failed: err=%s", err)
					}
				})
				if event.PubSub != nil {
					event.PubSub.Close()
				}
			})

			It("Testing simulated T-GM events on GNSS loss and recovery", func() {
				By("Ensuring initial LOCKED state via clock class 6")
				checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)), pkg.TimeoutIn5Minutes)

				const incomingEventsBuffer = 100
				subs, cleanup := event.SubscribeToGMChangeEvents(incomingEventsBuffer, true, 60*time.Second)
				defer cleanup()

				term, err := event.MonitorPodLogsRegex()
				defer func() { stopMonitor(term) }()
				Expect(err).ToNot(HaveOccurred(), "could not start listening to events")

				By("Triggering GNSS signal loss via gnss-sim API")
				simErr := ptphelper.GNSSSimSignalLoss()
				Expect(simErr).ToNot(HaveOccurred())
				defer func() { _ = ptphelper.GNSSSimSignalRestore() }()

				By("Waiting for GM clock class to degrade in metrics before collecting events")
				checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass7)), pkg.TimeoutIn5Minutes)

				events := getGMEvents(subs.GNSS, subs.CLOCKCLASS, subs.LOCKSTATE, 30*time.Second)
				fmt.Fprintf(GinkgoWriter, "Sim T-GM loss events: %v\n", events)

				By("Verifying ClockClass transitions to 7 (holdover)")
				verifyMetric(events[ptpEvent.PtpClockClassChange], float64(fbprotocol.ClockClass7))
				By("Verifying PTP state HOLDOVER")
				verifyEvent(events[ptpEvent.PtpStateChange], ptpEvent.HOLDOVER)
				stopMonitor(term)

				term2, err2 := event.MonitorPodLogsRegex()
				defer func() { stopMonitor(term2) }()
				Expect(err2).ToNot(HaveOccurred())

				By("Restoring GNSS signal via gnss-sim API")
				simErr = ptphelper.GNSSSimSignalRestore()
				Expect(simErr).ToNot(HaveOccurred())

				By("Waiting for GNSS recovery via clock class metrics")
				waitForClockClass(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)))

				events = getGMEvents(subs.GNSS, subs.CLOCKCLASS, subs.LOCKSTATE, 30*time.Second)
				fmt.Fprintf(GinkgoWriter, "Sim T-GM recovery events: %v\n", events)

				By("Verifying GNSS state Locked")
				verifyEvent(events[ptpEvent.GnssStateChange], ptpEvent.LOCKED)
				By("Verifying ClockClass returns to 6")
				verifyMetric(events[ptpEvent.PtpClockClassChange], float64(fbprotocol.ClockClass6))
				By("Verifying PTP state Locked")
				verifyEvent(events[ptpEvent.PtpStateChange], ptpEvent.LOCKED)
			})
		})

		// TGMOC dedicated tests removed: OC tests run for TGMOC since the
		// clock-under-test is the OC slave (swapped in testconfig.go).

		// TGMBC "Verifies WPC T-GM and downstream BC reach Locked clock class"
		// removed: now covered by Simulated T-GM Verification Tests (GM) and
		// "Downstream slave can sync to BC master" (BC).
		Context("TGMBC - Cascading holdover", func() {
			BeforeEach(func() {
				if fullConfig.PtpModeDesired != testconfig.TelcoGMBC {
					Skip("test valid only for TGMBC mode")
				}
				if !ptphelper.IsGnssSimulatedCI() {
					Skip("test valid only when gnss-sim PTY is used (hardware GNSS not present on cluster)")
				}
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)
				})
				waitForWPCGMReady(fullConfig)
			})

			It("Verifies cascading holdover on GNSS signal loss", func() {
				gmPod := getGMPod()

				bcPtpConfig := (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig)
				Expect(bcPtpConfig).ToNot(BeNil(), "BC PtpConfig was not discovered")
				bcPod, err := ptphelper.GetPTPPodWithPTPConfig(bcPtpConfig)
				Expect(err).ToNot(HaveOccurred())
				fullConfig.DiscoveredClockUnderTestPod = bcPod

				By("ensuring initial GM clock class is 6 (LOCKED)")
				Eventually(func() bool {
					buf, _, _ := pods.ExecCommand(client.Client, true, gmPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
					return checkPtp4lClockClassInMetrics(buf.String(), "6")
				}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(BeTrue())

				By("ensuring initial BC parent gm.ClockClass is 6 (LOCKED)")
				Eventually(func() int {
					cc, err := ptptesthelper.GetClockClassViaPMC(fullConfig, "/var/run/ptp4l.0.config")
					if err != nil {
						return -1
					}
					return cc
				}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(Equal(int(fbprotocol.ClockClass6)))

				By("triggering GNSS signal loss via gnss-sim API")
				simErr := ptphelper.GNSSSimSignalLoss()
				Expect(simErr).ToNot(HaveOccurred())
				defer func() { _ = ptphelper.GNSSSimSignalRestore() }()

				By("waiting for GM DPLL to transition to HOLDOVER after GNSS loss")
				Eventually(func() string {
					dpll, err := ptphelper.GNSSSimGetDPLLState()
					if err != nil {
						return ""
					}
					return dpll.State
				}, 5*time.Minute, 2*time.Second).Should(Equal("HOLDOVER"),
					"Expected GM DPLL to enter HOLDOVER after GNSS signal loss")

				By("waiting for GM clock class to reach CC7 (holdover) in metrics")
				Eventually(func() bool {
					buf, _, _ := pods.ExecCommand(client.Client, true, gmPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
					return checkPtp4lClockClassInMetrics(buf.String(), "7")
				}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(BeTrue(),
					"Expected GM ptp4l clock class to reach CC7 (holdover) in Prometheus metrics after DPLL HOLDOVER")

				By("waiting for BC parent gm.ClockClass to cascade to CC7 (holdover)")
				Eventually(func() bool {
					cc, err := ptptesthelper.GetClockClassViaPMC(fullConfig, "/var/run/ptp4l.0.config")
					return err == nil && cc == int(fbprotocol.ClockClass7)
				}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(BeTrue(),
					"Expected BC parent gm.ClockClass to cascade to CC7 after upstream GM GNSS loss")

				By("restoring GNSS signal via gnss-sim API")
				simErr = ptphelper.GNSSSimSignalRestore()
				Expect(simErr).ToNot(HaveOccurred())

				By("waiting for GM DPLL to recover to LOCKED")
				Eventually(func() string {
					dpll, err := ptphelper.GNSSSimGetDPLLState()
					if err != nil {
						return ""
					}
					return dpll.State
				}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(Equal("LOCKED"),
					"Expected GM DPLL to recover to LOCKED after signal restore")

				By("waiting for GM clock class to recover to Locked (6)")
				Eventually(func() bool {
					buf, _, _ := pods.ExecCommand(client.Client, true, gmPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
					return checkPtp4lClockClassInMetrics(buf.String(), "6")
				}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(BeTrue(),
					"Expected GM ptp4l clock class to recover to 6 before asserting BC cascade-recover")

				By("waiting for BC parent gm.ClockClass to cascade-recover to Locked (6)")
				Eventually(func() int {
					cc, err := ptptesthelper.GetClockClassViaPMC(fullConfig, "/var/run/ptp4l.0.config")
					if err != nil {
						return -1
					}
					return cc
				}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(Equal(int(fbprotocol.ClockClass6)),
					"Expected BC parent gm.ClockClass to cascade-recover to 6")

				logrus.Info("Successfully verified T-GM -> BC cascading holdover and recovery")
			})
		})

		Context("TGMBC - Events verification (V2)", func() {
			BeforeEach(func() {
				if fullConfig.PtpModeDesired != testconfig.TelcoGMBC {
					Skip("test valid only for TGMBC mode")
				}
				if !ptphelper.IsGnssSimulatedCI() {
					Skip("test valid only when gnss-sim PTY is used (hardware GNSS not present on cluster)")
				}
				By("Refreshing configuration", func() {
					ptphelper.WaitForPtpDaemonToExist()
					fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
					podsRunningPTP4l, err := testconfig.GetPodsRunningPTP4l(&fullConfig)
					Expect(err).NotTo(HaveOccurred())
					ptphelper.WaitForPtpDaemonToBeReady(podsRunningPTP4l)
				})
				waitForWPCGMReady(fullConfig)

				gmPod := getGMPod()
				nodeName := gmPod.Spec.NodeName
				if nodeName != "" {
					logrus.Info("Deploy consumer app for TGMBC event API v2")
					err := event.CreateConsumerApp(nodeName)
					Expect(err).ToNot(HaveOccurred(), "Consumer app setup failed")
					Expect(event.WaitForConsumerReady(nodeName)).To(BeNil(), "Consumer app not ready")
					event.InitPubSub()
				}
			})

			AfterEach(func() {
				DeferCleanup(func() {
					err := event.DeleteConsumerNamespace()
					if err != nil {
						logrus.Debugf("Deleting consumer namespace failed: err=%s", err)
					}
				})
				if event.PubSub != nil {
					event.PubSub.Close()
				}
			})

			It("Verifies cascading events on GNSS loss and recovery", func() {
				gmPod := getGMPod()

				bcPtpConfig := (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig)
				Expect(bcPtpConfig).ToNot(BeNil(), "BC PtpConfig was not discovered")
				bcPod, err := ptphelper.GetPTPPodWithPTPConfig(bcPtpConfig)
				Expect(err).ToNot(HaveOccurred())
				fullConfig.DiscoveredClockUnderTestPod = bcPod

				By("Ensuring initial LOCKED state: GM metrics CC6 and BC parent gm.ClockClass 6")
				Eventually(func() bool {
					buf, _, _ := pods.ExecCommand(client.Client, true, gmPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
					return checkPtp4lClockClassInMetrics(buf.String(), strconv.Itoa(int(fbprotocol.ClockClass6)))
				}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(BeTrue(),
					"Expected GM ptp4l clock class 6 before event test")
				Eventually(func() int {
					cc, err := ptptesthelper.GetClockClassViaPMC(fullConfig, "/var/run/ptp4l.0.config")
					if err != nil {
						return -1
					}
					return cc
				}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(Equal(int(fbprotocol.ClockClass6)),
					"Expected BC parent gm.ClockClass 6 before event test")

				const incomingEventsBuffer = 100
				subs, cleanup := event.SubscribeToGMChangeEvents(incomingEventsBuffer, true, 60*time.Second)
				defer cleanup()

				term, err := event.MonitorPodLogsRegex()
				defer func() { stopMonitor(term) }()
				Expect(err).ToNot(HaveOccurred(), "could not start listening to events")

				By("Triggering GNSS signal loss via gnss-sim API")
				simErr := ptphelper.GNSSSimSignalLoss()
				Expect(simErr).ToNot(HaveOccurred())
				defer func() { _ = ptphelper.GNSSSimSignalRestore() }()

				// Assert holdover cascade immediately. A long event-collection wait
				// after this lets gnss-sim leave HOLDOVER (CC7) for FREERUN (CC248),
				// so a later CC7-only PMC check would miss the holdover window.
				By("Waiting for GM clock class to reach CC7 (holdover) in metrics")
				Eventually(func() bool {
					buf, _, _ := pods.ExecCommand(client.Client, true, gmPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
					return checkPtp4lClockClassInMetrics(buf.String(), "7")
				}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(BeTrue(),
					"Expected GM ptp4l clock class to reach CC7 (holdover) before collecting events")

				By("Verifying BC parent gm.ClockClass cascades to CC7 via PMC while still in holdover")
				Eventually(func() bool {
					cc, err := ptptesthelper.GetClockClassViaPMC(fullConfig, "/var/run/ptp4l.0.config")
					return err == nil && cc == int(fbprotocol.ClockClass7)
				}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(BeTrue(),
					"Expected BC parent gm.ClockClass to cascade to CC7 after GM holdover")

				events := getGMEvents(subs.GNSS, subs.CLOCKCLASS, subs.LOCKSTATE, 30*time.Second)
				fmt.Fprintf(GinkgoWriter, "TGMBC GM loss events: %v\n", events)

				By("Verifying GM ClockClass transitions to 7 (holdover)")
				verifyMetric(events[ptpEvent.PtpClockClassChange], float64(fbprotocol.ClockClass7))
				By("Verifying GM PTP state HOLDOVER")
				verifyEvent(events[ptpEvent.PtpStateChange], ptpEvent.HOLDOVER)
				stopMonitor(term)

				term2, err2 := event.MonitorPodLogsRegex()
				defer func() { stopMonitor(term2) }()
				Expect(err2).ToNot(HaveOccurred())

				By("Restoring GNSS signal via gnss-sim API")
				simErr = ptphelper.GNSSSimSignalRestore()
				Expect(simErr).ToNot(HaveOccurred())

				By("Waiting for GM clock class recovery to CC6")
				Eventually(func() bool {
					buf, _, _ := pods.ExecCommand(client.Client, true, gmPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
					return checkPtp4lClockClassInMetrics(buf.String(), strconv.Itoa(int(fbprotocol.ClockClass6)))
				}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(BeTrue(),
					"Expected GM ptp4l clock class to recover to 6 before collecting recovery events")

				events = getGMEvents(subs.GNSS, subs.CLOCKCLASS, subs.LOCKSTATE, 30*time.Second)
				fmt.Fprintf(GinkgoWriter, "TGMBC GM recovery events: %v\n", events)

				By("Verifying GM GNSS state Locked")
				verifyEvent(events[ptpEvent.GnssStateChange], ptpEvent.LOCKED)
				By("Verifying GM ClockClass returns to 6")
				verifyMetric(events[ptpEvent.PtpClockClassChange], float64(fbprotocol.ClockClass6))
				By("Verifying GM PTP state Locked")
				verifyEvent(events[ptpEvent.PtpStateChange], ptpEvent.LOCKED)

				By("Verifying BC parent gm.ClockClass cascades back to CC6 via PMC")
				Eventually(func() int {
					cc, err := ptptesthelper.GetClockClassViaPMC(fullConfig, "/var/run/ptp4l.0.config")
					if err != nil {
						return -1
					}
					return cc
				}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(Equal(int(fbprotocol.ClockClass6)),
					"Expected BC parent gm.ClockClass to cascade-recover to CC6 after GM recovery event")

				logrus.Info("Successfully verified TGMBC cascading events on GNSS loss and recovery")
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

		It("Check GM announcements in locked mode and holdover transitions", func() {
			ptpOperatorVersion, err := ptphelper.GetPtpOperatorVersion()
			Expect(err).ToNot(HaveOccurred())
			operatorVersion, err := semver.NewVersion(ptpOperatorVersion)
			Expect(err).ToNot(HaveOccurred())
			minVersion, err := semver.NewVersion("4.20")
			Expect(err).ToNot(HaveOccurred())
			if operatorVersion.LessThan(minVersion) {
				Skip("T-BC is not available below 4.20")
			}

			if fullConfig.PtpModeDiscovered != testconfig.TelcoBoundaryClock {
				Skip("test valid only for T-BC test config")
			}

			// 0. Modify E810 plugin settings, restart pod, and register cleanup
			originalPtpConfig := (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig)
			restoreConfig, err := ptphelper.ApplyE810PluginMutation(
				fullConfig.DiscoveredClockUnderTestPod,
				originalPtpConfig,
				*originalPtpConfig.Spec.Profile[0].Name,
				fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName,
				fullConfig.L2Config,
				500, // LocalMaxHoldoverOffSet: 500ns (DPLL offset threshold for freerun/class 248, ~250s at ~2ns/s drift)
				300, // LocalHoldoverTimeout: 300s (safety net, offset should cross 500ns first)
				100, // MaxInSpecOffset: 100ns (DPLL offset threshold for out-of-spec/class 165, ~50s at ~2ns/s drift)
			)
			Expect(err).NotTo(HaveOccurred())

			// Discover TT config (for PMC) and TR config (daemon publishes clock_class metric under receiver)
			nodeName := fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
			cfgName, err := ptphelper.GetConfigForProfile(originalPtpConfig.Name, "tbc-tt", nil, &nodeName)
			Expect(err).NotTo(HaveOccurred())
			trCfgName, err := ptphelper.GetConfigForProfile(originalPtpConfig.Name, "tbc-tr", nil, &nodeName)
			Expect(err).NotTo(HaveOccurred())
			ttNIC := ptptesthelper.NICInfo{
				ConfigName:    strings.TrimPrefix(trCfgName, "/var/run/"),
				PMCConfigName: strings.TrimPrefix(cfgName, "/var/run/"),
			}

			pmcMonitor, err := pmc.NewMonitor(fullConfig.DiscoveredClockUnderTestPod, cfgName, 5*time.Second)
			Expect(err).NotTo(HaveOccurred())
			pmcMonitor.Start()

			DeferCleanup(func() {
				pmcMonitor.Stop()
				restoreConfig()
			})

			// Track clock class transitions (only record when value changes)
			var clockClassTransitions []int
			lastRecordedClass := -1

			// 1. Wait for locked state (class 6) after E810 config update
			By("Waiting for locked state after E810 config update")
			logging.WriteStep("Waiting for locked state after E810 config update")
			lockStart := time.Now()
			Eventually(func() bool {
				select {
				case data := <-pmcMonitor.DataChan():
					if data == nil {
						return false
					}
					if data.GrandmasterClockQuality.ClockClass != lastRecordedClass {
						clockClassTransitions = append(clockClassTransitions, data.GrandmasterClockQuality.ClockClass)
						lastRecordedClass = data.GrandmasterClockQuality.ClockClass
					}
					logrus.Warnf("Current Clock class: %v (expected: 6)", data.GrandmasterClockQuality.ClockClass)
					if data.GrandmasterClockQuality.ClockClass == 6 {
						return true
					}
				case err := <-pmcMonitor.ErrorChan():
					logrus.Warnf("PMC monitor error: %v", err)
				default:
					// No data available, return false and Eventually will retry
				}
				return false
			}, 30*time.Minute, 1*time.Second).Should(BeTrue(), "Clock should reach locked state (class 6)")
			fmt.Fprintf(GinkgoWriter, "Locked to class 6 in %s (startup transitions: %v)\n", time.Since(lockStart).Truncate(time.Second), clockClassTransitions)
			clockClassTransitions = []int{6}
			lastRecordedClass = 6

			ptptesthelper.VerifyNICClockClass(fullConfig, ttNIC, 6, false)

			// 2. Verify GM announcements are passed downstream in locked mode
			By("Verifying all announcement fields match between receiver and transmitter")
			logging.WriteStep("Verifying all announcement fields match between receiver and transmitter")
			pmcTR, err := pmc.Query(fullConfig.DiscoveredClockUnderTestPod, trCfgName)
			Expect(err).NotTo(HaveOccurred())
			Expect(pmcTR).ToNot(BeNil(), "tbc-tr announce data should not be nil")

			pmcTT, err := pmc.Query(fullConfig.DiscoveredClockUnderTestPod, cfgName)
			Expect(err).NotTo(HaveOccurred())
			Expect(pmcTT).ToNot(BeNil(), "tbc-tt announce data should not be nil")

			pmcTR.MeanPathDelay = pmcTT.MeanPathDelay
			pmcTR.OffsetFromMaster = pmcTT.OffsetFromMaster
			pmcTR.ParentPortIdentity = pmcTT.ParentPortIdentity
			pmcTR.GrandmasterPriority1 = pmcTT.GrandmasterPriority1
			pmcTR.GrandmasterPriority2 = pmcTT.GrandmasterPriority2

			err = pmcTT.CheckSame(pmcTR)
			Expect(err).NotTo(HaveOccurred(), "Transmitter should propagate exact GM information from receiver: %v", err)
			err = pmcTR.CheckSame(pmcTT)
			Expect(err).NotTo(HaveOccurred())

			// 3. Trigger holdover by disabling upstream port
			By("Triggering holdover by disabling upstream port")
			logging.WriteStep("Triggering holdover by disabling upstream port")
			skippedInterfacesStr, isSet := os.LookupEnv("SKIP_INTERFACES")

			if !isSet {
				Skip("Mandatory to provide skipped interface to avoid making a node disconnected from the cluster")
			}

			skipInterfaces := make(map[string]bool)
			separated := strings.Split(skippedInterfacesStr, ",")
			for _, val := range separated {
				skipInterfaces[val] = true
			}

			// Drain buffered PMC data collected during the link-down period so the
			// holdover transition check below only observes fresh samples.
			for len(pmcMonitor.DataChan()) > 0 {
				<-pmcMonitor.DataChan()
			}

			slaveIf := ptpv1.GetInterfaces((ptpv1.PtpConfig)(*fullConfig.DiscoveredClockUnderTestPtpConfig), ptpv1.Slave)
			nodeName = fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName
			var toggledIfaces []string
			for _, iface := range slaveIf {
				if skipInterfaces[iface] {
					logrus.Infof("Skipping interface %s", iface)
					continue
				}
				logrus.Infof("Disabling upstream interface %s", iface)
				portEngine.TurnOffAndWaitFaulty(iface, nodeName)
				toggledIfaces = append(toggledIfaces, iface)
			}
			restoreInterfacesOnce := sync.OnceFunc(func() {
				for _, iface := range toggledIfaces {
					portEngine.TurnOnAndWaitSlave(iface, nodeName)
				}
			})
			defer restoreInterfacesOnce()

			// Transition timer — tracks elapsed time between each phase
			lastTransitionTime := time.Now()

			// Look up expected priorities from the TT profile's ptp4l.conf
			priorityRegex := regexp.MustCompile(`priority(\d)\s+(\d+)`)
			var expectedPriority1, expectedPriority2 int
			ptpConfig := (*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig)
			for _, profile := range ptpConfig.Spec.Profile {
				if profile.Name != nil && ptphelper.QualifyProfileName(ptpConfig.Name, *profile.Name) == ptphelper.QualifyProfileName(ptpConfig.Name, "tbc-tt") && profile.Ptp4lConf != nil {
					for _, match := range priorityRegex.FindAllStringSubmatch(*profile.Ptp4lConf, -1) {
						v, err := strconv.Atoi(match[2])
						Expect(err).NotTo(HaveOccurred())
						switch match[1] {
						case "1":
							expectedPriority1 = v
						case "2":
							expectedPriority2 = v
						}
					}
					break
				}
			}
			logrus.Infof("Expected TT priorities from ptp4l.conf: priority1=%d, priority2=%d", expectedPriority1, expectedPriority2)

			// Calculate the TT clock identity from the leading interface's MAC address.
			// The leading interface (with PTP pins) is found in the tbc-tr profile's
			// e810 plugin config. The clock identity uses the standard EUI-48 → EUI-64
			// conversion (insert fffe in the middle of the MAC).
			expectedClockID, err := ptphelper.GetLocalClockID(ptpConfig, "tbc-tr",
				fullConfig.L2Config, fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
			Expect(err).NotTo(HaveOccurred(), "Should be able to calculate TT clock identity from MAC")
			logrus.Infof("Expected TT clock identity: %s", expectedClockID)

			// 3. Wait for holdover state (class 135) - in-spec holdover
			// The comparison is inside the Eventually loop so the PMC monitor keeps
			// querying until both the clock class AND the announced data match.
			By("Waiting for holdover state (class 135) and verifying announced data")
			logging.WriteStep("Waiting for holdover state (class 135) and verifying announced data")
			Eventually(func() bool {
				select {
				case data := <-pmcMonitor.DataChan():

					if data == nil {
						return false
					}
					currentClass := data.GrandmasterClockQuality.ClockClass
					logrus.Warnf("Current Clock class: %v (expected: 135)", currentClass)
					if currentClass != lastRecordedClass {
						clockClassTransitions = append(clockClassTransitions, currentClass)
						lastRecordedClass = currentClass
					}
					if currentClass != 135 {
						return false
					}
					// Clock class matches; now verify the announced data
					expectedInSpec := pmc.AnnounceData{
						GrandmasterClockQuality: pmc.ClockQuality{
							ClockClass:              135,
							ClockAccuracy:           data.GrandmasterClockQuality.ClockAccuracy, // Is calculated based on ramp so lets set it
							OffsetScaledLogVariance: "0xFFFF",
						},
						Leap61:                false,
						Leap59:                false,
						CurrentUtcOffsetValid: true,
						PtpTimescale:          true,
						TimeTraceable:         true,
						FrequencyTraceable:    data.FrequencyTraceable, // Can be either so we can just set it //based on traceability to Cat 1 frequency source
						CurrentUtcOffset:      data.CurrentUtcOffset,   // Comes from leap seconds file
						GrandmasterPriority1:  expectedPriority1,       // From TT ptp4l.conf
						GrandmasterPriority2:  expectedPriority2,       // From TT ptp4l.conf
						GrandmasterIdentity:   expectedClockID,         // TT's own clock identity in holdover
						StepsRemoved:          0,
						TimeSource:            "0xA0",
						ParentPortIdentity:    data.ParentPortIdentity, // Checked separately below
					}
					if checkErr := data.CheckSame(&expectedInSpec); checkErr != nil {
						fmt.Fprintf(GinkgoWriter, "Class 135 data not yet matching expected, retrying: %v\n", checkErr)
						return false
					}
					// Verify ParentPortIdentity references the TT's own clock (port number may vary)
					Expect(data.ParentPortIdentity).To(HavePrefix(expectedClockID),
						"ParentPortIdentity should reference the T-BC's own clock identity in holdover")
					return true
				case err := <-pmcMonitor.ErrorChan():
					logrus.Warnf("PMC monitor error: %v", err)
				default:
					// No data available, return false and Eventually will retry
				}
				return false
			}, 5*time.Minute, 1*time.Second).Should(BeTrue(),
				fmt.Sprintf("Clock should reach holdover (class 135) with matching data. transitions=%v", clockClassTransitions))
			now := time.Now()
			fmt.Fprintf(GinkgoWriter, "Reached class 135  (%s)\n", now.Sub(lastTransitionTime).Truncate(time.Second))
			lastTransitionTime = now

			ptptesthelper.VerifyNICClockClass(fullConfig, ttNIC, 135, false)

			// 5. Wait for out-of-spec state (class 165) - triggered by low MaxInSpecOffset
			// The comparison is inside the Eventually loop so the PMC monitor keeps
			// querying until both the clock class AND the announced data match.
			By("Waiting for out-of-spec state (class 165) and verifying announced data")
			logging.WriteStep("Waiting for out-of-spec state (class 165) and verifying announced data")
			Eventually(func() bool {
				select {
				case data := <-pmcMonitor.DataChan():
					if data == nil {
						return false
					}
					currentClass := data.GrandmasterClockQuality.ClockClass
					logrus.Warnf("Current Clock class: %v (expected: 165)", currentClass)
					if currentClass != lastRecordedClass {
						clockClassTransitions = append(clockClassTransitions, currentClass)
						lastRecordedClass = currentClass
					}
					if currentClass != 165 {
						return false
					}
					// Clock class matches; now verify the announced data
					expectedOutOfSpec := pmc.AnnounceData{
						GrandmasterClockQuality: pmc.ClockQuality{
							ClockClass:              165,
							ClockAccuracy:           "0xFE",
							OffsetScaledLogVariance: "0xFFFF",
						},
						Leap61:                false,
						Leap59:                false,
						CurrentUtcOffsetValid: true,
						PtpTimescale:          true,
						TimeTraceable:         false,
						FrequencyTraceable:    data.FrequencyTraceable, // Can be either so we can just set it //based on traceability to Cat 1 frequency source
						CurrentUtcOffset:      data.CurrentUtcOffset,   // Comes from leap seconds file
						GrandmasterPriority1:  expectedPriority1,       // From TT ptp4l.conf
						GrandmasterPriority2:  expectedPriority2,       // From TT ptp4l.conf
						GrandmasterIdentity:   expectedClockID,         // TT's own clock identity in holdover
						StepsRemoved:          0,
						TimeSource:            "0xA0",
						ParentPortIdentity:    data.ParentPortIdentity, // Checked separately below
					}
					if checkErr := data.CheckSame(&expectedOutOfSpec); checkErr != nil {
						fmt.Fprintf(GinkgoWriter, "Class 165 data not yet matching expected, retrying: %v\n", checkErr)
						return false
					}
					// Verify ParentPortIdentity references the TT's own clock (port number may vary)
					Expect(data.ParentPortIdentity).To(HavePrefix(expectedClockID),
						"ParentPortIdentity should reference the T-BC's own clock identity in out-of-spec holdover")
					return true
				case err := <-pmcMonitor.ErrorChan():
					logrus.Warnf("PMC monitor error: %v", err)
				default:
					// No data available, return false and Eventually will retry
				}
				return false
			}, 5*time.Minute, 1*time.Second).Should(BeTrue(),
				fmt.Sprintf("Clock should reach out-of-spec (class 165) with matching data. transitions=%v", clockClassTransitions))
			now = time.Now()
			fmt.Fprintf(GinkgoWriter, "Reached class 165  (%s)\n", now.Sub(lastTransitionTime).Truncate(time.Second))
			lastTransitionTime = now

			ptptesthelper.VerifyNICClockClass(fullConfig, ttNIC, 165, false)

			// 6. Wait for freerun state (class 248) - holdover timeout expires
			By("Waiting for freerun state (class 248)")
			logging.WriteStep("Waiting for freerun state (class 248)")
			Eventually(func() bool {
				select {
				case data := <-pmcMonitor.DataChan():
					if data == nil {
						return false
					}
					currentClass := data.GrandmasterClockQuality.ClockClass
					logrus.Warnf("Current Clock class: %v (expected: 248)", currentClass)
					if currentClass != lastRecordedClass {
						clockClassTransitions = append(clockClassTransitions, currentClass)
						lastRecordedClass = currentClass
					}
					return currentClass == 248
				case err := <-pmcMonitor.ErrorChan():
					logrus.Warnf("PMC monitor error: %v", err)
				default:
				}
				return false
			}, 5*time.Minute, 1*time.Second).Should(BeTrue(),
				fmt.Sprintf("Clock should reach freerun (class 248). transitions=%v", clockClassTransitions))
			now = time.Now()
			fmt.Fprintf(GinkgoWriter, "Reached class 248  (%s)\n", now.Sub(lastTransitionTime).Truncate(time.Second))
			lastTransitionTime = now

			ptptesthelper.VerifyNICClockClass(fullConfig, ttNIC, 248, false)

			// 7. Restore upstream interfaces and wait for re-lock (class 6)
			By("Restoring upstream interfaces to trigger re-lock")
			logging.WriteStep("Restoring upstream interfaces to trigger re-lock")
			restoreInterfacesOnce()

			By("Waiting for re-locked state (class 6)")
			logging.WriteStep("Waiting for re-locked state (class 6)")
			Eventually(func() bool {
				select {
				case data := <-pmcMonitor.DataChan():
					if data != nil && data.GrandmasterClockQuality.ClockClass != lastRecordedClass {
						clockClassTransitions = append(clockClassTransitions, data.GrandmasterClockQuality.ClockClass)
						lastRecordedClass = data.GrandmasterClockQuality.ClockClass
					}
					if data == nil || data.GrandmasterClockQuality.ClockClass != 6 {
						return false
					}
					return true
				case err := <-pmcMonitor.ErrorChan():
					logrus.Warnf("PMC monitor error: %v", err)
				default:
				}
				return false
			}, 5*time.Minute, 1*time.Second).Should(BeTrue(), "Clock should re-lock (class 6)")
			now = time.Now()
			fmt.Fprintf(GinkgoWriter, "Reached class 6 (re-lock)  (%s)\n", now.Sub(lastTransitionTime).Truncate(time.Second))

			ptptesthelper.VerifyNICClockClass(fullConfig, ttNIC, 6, false)

			// Verify the clock class transition history matches the expected order exactly.
			// An optional leading 248 (freerun at startup) is allowed and stripped.
			By("Verifying clock class transition history")
			logging.WriteStep("Verifying clock class transition history")
			actual := clockClassTransitions
			if len(actual) > 0 && actual[0] == 248 {
				actual = actual[1:]
			}
			expectedTransitions := []int{6, 135, 165, 248, 6}
			fmt.Fprintf(GinkgoWriter, "Clock class transitions: %v (expected %v)\n", clockClassTransitions, expectedTransitions)
			Expect(actual).To(Equal(expectedTransitions),
				"Clock class transitions did not match expected order (raw: %v)", clockClassTransitions)
		})
	})
})

func checkStabilityOfWPCGMUsingMetrics(fullConfig testconfig.TestConfig) {
	checkProcessStatus(fullConfig, "1")
	checkClockClassState(fullConfig, strconv.Itoa(int(fbprotocol.ClockClass6)), pkg.TimeoutIn5Minutes)
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

// processRunningSimGM is like processRunning but only checks processes that
// exist in the simulated T-GM environment (no gpsd or gpspipe — gnss-sim
// owns /dev/gnssN). phc2sys -r still runs so CLOCK_REALTIME is disciplined.
func processRunningSimGM(input string, state string) (map[string]bool, error) {
	processStatusPattern := `openshift_ptp_process_status\{config="([^"]+)",node="([^"]+)",process="([^"]+)"\} (\d+)`
	processStatusRe := regexp.MustCompile(processStatusPattern)

	result := map[string]bool{"ptp4l": false, "ts2phc": false, "phc2sys": false}

	scanner := bufio.NewScanner(strings.NewReader(input))
	timeout := 10 * time.Second
	start := time.Now()
	for scanner.Scan() {
		if time.Since(start) > timeout {
			fmt.Println("Timed out when reading metrics")
			break
		}
		line := scanner.Text()
		if matches := processStatusRe.FindStringSubmatch(line); matches != nil {
			if _, ok := result[matches[3]]; ok && matches[4] == state {
				result[matches[3]] = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
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

func refreshPodOnNotFound(pod *v1core.Pod, err error) {
	if err == nil || pod == nil {
		return
	}
	if !kerrors.IsNotFound(err) {
		return
	}
	nodeName := pod.Spec.NodeName
	fmt.Fprintf(GinkgoWriter, "Pod %s not found, refreshing pod reference for node %s\n", pod.Name, nodeName)
	newPod, refreshErr := ptphelper.GetPtpPodOnNode(nodeName)
	if refreshErr != nil {
		fmt.Fprintf(GinkgoWriter, "Failed to refresh pod for node %s: %v\n", nodeName, refreshErr)
		return
	}
	*pod = newPod
	fmt.Fprintf(GinkgoWriter, "Refreshed pod reference to %s\n", pod.Name)
}

func checkProcessStatus(fullConfig testconfig.TestConfig, state string) {
	if fullConfig.DiscoveredClockUnderTestPod == nil {
		Fail("DiscoveredClockUnderTestPod is nil - cannot check process status")
		return
	}

	if client.Client == nil {
		Fail("Client is nil - cannot execute commands")
		return
	}

	logging.WriteStep(fmt.Sprintf("Waiting for process status to become %s", state))
	/*
		# TYPE openshift_ptp_process_status gauge
		openshift_ptp_process_status{config="ptp4l.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="phc2sys"} 1
		openshift_ptp_process_status{config="ptp4l.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="ptp4l"} 1
		openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="gpsd"} 1
		openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="gpspipe"}
		openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="gpspipe"} 1
		openshift_ptp_process_status{config="ts2phc.0.config",node="cnfde22.ptp.lab.eng.bos.redhat.com",process="ts2phc"} 1
	*/
	timeout := pkg.TimeoutIn5Minutes
	if isProcessDelayed(fullConfig, "phc2sys") {
		timeout = pkg.TimeoutIn10Minutes
	}
	Eventually(func() ([]string, error) {
		buf, _, err := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		if err != nil {
			refreshPodOnNotFound(fullConfig.DiscoveredClockUnderTestPod, err)
			return nil, err
		}
		status, err := processRunning(buf.String(), state)
		if err != nil {
			return nil, err
		}
		required := []string{"ptp4l", "ts2phc", "gpspipe", "gpsd"}
		if !ptphelper.IsGnssSimConfigured() {
			required = append([]string{"phc2sys"}, required...)
		}
		var failing []string
		for _, process := range required {
			if !status[process] {
				failing = append(failing, process)
			}
		}
		return failing, nil
	}, timeout, 5*time.Second).Should(BeEmpty(), "Processes missing or not reporting status %s", state)
}

func checkClockClassState(fullConfig testconfig.TestConfig, expectedState string, timeout time.Duration) {
	By(fmt.Sprintf("Waiting for clock class to become %s", expectedState))
	logging.WriteStep(fmt.Sprintf("Waiting for clock class to become %s", expectedState))
	Eventually(func() bool {
		// Get the latest metrics output
		buf, _, err := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		if err != nil {
			fmt.Fprintf(GinkgoWriter, "Error executing curl: %v\n", err)
			refreshPodOnNotFound(fullConfig.DiscoveredClockUnderTestPod, err)
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
	}, timeout, pkg.Timeout1Seconds).Should(BeTrue(),
		fmt.Sprintf("Expected ptp4l clock class to eventually be %s for GM", expectedState))
}

func checkDPLLFrequencyState(fullConfig testconfig.TestConfig, state string) {
	/*
		# TODO: Revisit this for 2 card as each card will have its own dpll process
		# TYPE openshift_ptp_frequency_status gauge
		# openshift_ptp_frequency_status{from="dpll",iface="ens7fx",node="cnfdg32.ptp.eng.rdu2.dc.redhat.com",process="dpll"} 3
	*/
	Eventually(func() string {
		buf, _, err := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		if err != nil {
			refreshPodOnNotFound(fullConfig.DiscoveredClockUnderTestPod, err)
			return ""
		}
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
		buf, _, err := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		if err != nil {
			refreshPodOnNotFound(fullConfig.DiscoveredClockUnderTestPod, err)
			return ""
		}
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

// ensureGMPtpConfigRestored is a DeferCleanup helper that recreates the GM PtpConfig
// if it was deleted during a test, ensuring subsequent tests have a valid configuration.
func ensureGMPtpConfigRestored() {
	if testconfig.GlobalConfig.DiscoveredGrandMasterPtpConfig == nil {
		logrus.Warn("ensureGMPtpConfigRestored: DiscoveredGrandMasterPtpConfig is nil; cannot recreate GM PtpConfig")
		return
	}
	tempPtpConfig := (*ptpv1.PtpConfig)(testconfig.GlobalConfig.DiscoveredGrandMasterPtpConfig)
	tempPtpConfig.SetResourceVersion("")
	_, err := client.Client.PtpV1Interface.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Create(
		context.Background(), tempPtpConfig, metav1.CreateOptions{})
	if err != nil && !kerrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

// requireDiscoveryAfterRefresh fails when a forced rediscovery lost PtpConfigs or
// the clock-under-test pod (e.g. wiped mid-run). Soft Skip hid those regressions.
func requireDiscoveryAfterRefresh(cfg *testconfig.TestConfig, wantMode testconfig.PTPMode) {
	Expect(cfg.Status).To(Equal(testconfig.DiscoverySuccessStatus),
		fmt.Sprintf("PTP discovery failed after refresh (status=%s, mode=%s); "+
			"PtpConfigs may have been wiped by a concurrent suite",
			cfg.Status, cfg.PtpModeDiscovered))
	Expect(cfg.DiscoveredClockUnderTestPod).NotTo(BeNil(),
		"DiscoveredClockUnderTestPod is nil after refresh")
	if wantMode != testconfig.None {
		Expect(cfg.PtpModeDiscovered).To(Equal(wantMode),
			fmt.Sprintf("discovered mode %s != required %s after refresh",
				cfg.PtpModeDiscovered, wantMode))
	}
}

// getProcessConfigAndRestartCount parses the metrics output for a given process and returns
// its config file name (from process_status with value 1) and restart count.
func getProcessConfigAndRestartCount(metricsText, process string) (configName string, restartCount int, found bool) {
	processFilter := fmt.Sprintf(`process="%s"`, process)
	scanner := bufio.NewScanner(strings.NewReader(metricsText))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, metrics.OpenshiftPtpProcessStatus) && strings.Contains(line, processFilter) {
			parts := strings.Fields(line)
			if len(parts) == 2 && parts[1] == "1" {
				start := strings.Index(line, `config="`) + len(`config="`)
				end := strings.Index(line[start:], `"`)
				if end > 0 {
					configName = line[start : start+end]
					found = true
				}
			}
		}
		if strings.HasPrefix(line, metrics.OpenshiftPtpProcessRestartCount) && strings.Contains(line, processFilter) {
			parts := strings.Fields(line)
			if len(parts) == 2 {
				var parseErr error
				restartCount, parseErr = strconv.Atoi(parts[1])
				if parseErr != nil {
					fmt.Fprintf(GinkgoWriter, "WARN: could not parse restart_count %q for process %s: %v\n", parts[1], process, parseErr)
				}
			}
		}
	}
	return configName, restartCount, found
}

// verifyProcessRestartNoSocketErrors sends SIGTERM to the given PTP process, waits for a
// 1→0→1 metric restart cycle, and asserts no event-socket errors appear in the daemon logs.
func verifyProcessRestartNoSocketErrors(fullConfig testconfig.TestConfig, process string) {
	By(fmt.Sprintf("Verifying %s is running (process status == 1)", process))
	checkStatusByProcess(fullConfig, process, "1")

	beforeKill := time.Now()
	time.Sleep(1 * time.Second)

	By(fmt.Sprintf("Starting metric watcher and killing %s", process))
	watchDone := make(chan bool, 1)
	go func() {
		watchDone <- watchProcessFlipOneZeroOne(fullConfig, process, 60*time.Second)
	}()
	time.Sleep(1 * time.Second)

	_, _, err := pods.ExecCommand(client.Client, true,
		fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName,
		[]string{"sh", "-c", fmt.Sprintf("pkill -TERM %s || true", process)})
	Expect(err).To(BeNil(), fmt.Sprintf("failed to kill %s", process))

	By(fmt.Sprintf("Waiting for %s process_status 1→0→1 metric flip", process))
	Eventually(watchDone, 60*time.Second, 1*time.Second).Should(Receive(BeTrue()),
		fmt.Sprintf("%s did not complete 1→0→1 restart cycle", process))

	By("Checking daemon logs for socket errors after the kill")
	time.Sleep(5 * time.Second)
	socketErrMatches, _ := pods.GetPodLogsRegexSince(
		openshiftPtpNamespace,
		fullConfig.DiscoveredClockUnderTestPod.Name,
		pkg.PtpContainerName,
		`error trying to connect to event socket|Failed to reconnect to event socket|Reconnect failed after write error|Write error for|Write failed again after reconnect`,
		false,
		2*time.Second,
		beforeKill,
	)
	Expect(socketErrMatches).To(BeEmpty(),
		fmt.Sprintf("unexpected socket errors in logs after %s kill: %v", process, socketErrMatches))
}

// isProcessDelayed checks if a process has delayed startup in the daemon logs
func isProcessDelayed(fullConfig testconfig.TestConfig, process string) bool {
	delayedMatches, err := pods.GetPodLogsRegex(
		openshiftPtpNamespace,
		fullConfig.DiscoveredClockUnderTestPod.Name,
		pkg.PtpContainerName,
		fmt.Sprintf(`Delaying %s startup`, process),
		true, // fixed log text
		2*time.Second,
	)
	if err != nil {
		logrus.Warnf("Could not confirm delayed %s startup from pod logs; using 3-minute timeout: %v", process, err)
	}
	return len(delayedMatches) > 0
}

// checkStatusByProcess mirrors checkClockStateForProcess but for process status (1/0)
func checkStatusByProcess(fullConfig testconfig.TestConfig, process string, state string) {
	// Determine timeout based on whether process has delayed startup
	timeout := pkg.TimeoutIn3Minutes
	if isProcessDelayed(fullConfig, process) {
		By(fmt.Sprintf("%s has delayed startup, using extended timeout of 10 minutes", process))
		timeout = 10 * time.Minute
	}

	Eventually(func() string {
		buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		retState, found := getProcessStatusByProcess(buf.String(), process)
		if !found {
			return ""
		}
		return retState
	}, timeout, pkg.Timeout10Seconds).Should(Equal(state),
		fmt.Sprintf("Expected %s process status to be %s", process, state))
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
	buf, _, err := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
	if err != nil {
		refreshPodOnNotFound(fullConfig.DiscoveredClockUnderTestPod, err)
		return false
	}
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

// anyClockClassDifferent returns true when at least one ptp4l instance on the
// clock-under-test pod reports a clock class other than excludedClass. This is
// needed for TGMBC where the pod runs two ptp4l processes (GM + BC); checking
// !checkClockClassStateReturnBool would fail because the GM always reports 6.
func anyClockClassDifferent(fullConfig testconfig.TestConfig, excludedClass string) bool {
	buf, _, _ := pods.ExecCommand(client.Client, true, fullConfig.DiscoveredClockUnderTestPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
	scanner := bufio.NewScanner(strings.NewReader(buf.String()))
	for scanner.Scan() {
		line := scanner.Text()
		if matches := clockClassRe.FindStringSubmatch(line); matches != nil {
			process := matches[2]
			class := matches[3]
			if strings.TrimSpace(process) == "ptp4l" && strings.TrimSpace(class) != excludedClass {
				return true
			}
		}
	}
	return false
}

// killCloudEventProxy kills the cloud-event-proxy process in the sidecar.
// Uses /proc/PID/comm — portable across minimal images that lack pgrep/pkill.
// "cloud-event-proxy" truncated to 15 chars (Linux TASK_COMM_LEN) is
// "cloud-event-pro". The exec may error if killing PID 1 crashes the container;
// that is expected and ignored.
func killCloudEventProxy(pod *v1core.Pod) {
	killCmd := `for pid in $(ls /proc/ | grep '^[0-9]'); do ` +
		`if [ "$(cat /proc/$pid/comm 2>/dev/null)" = "cloud-event-pro" ]; then ` +
		`kill -9 $pid 2>/dev/null; fi; done`
	_, _, _ = pods.ExecCommand(
		client.Client,
		true,
		pod,
		pkg.EventProxyContainerName,
		[]string{"sh", "-c", killCmd},
	)
}

// refreshDaemonPod returns a live GET of the daemon pod. CEP kill/wait must
// use a fresh object so RestartCount baselines are not stale across bounces.
func refreshDaemonPod(pod *v1core.Pod) *v1core.Pod {
	fresh, err := client.Client.CoreV1().Pods(pod.Namespace).Get(
		context.Background(), pod.Name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "refreshing daemon pod %s/%s", pod.Namespace, pod.Name)
	return fresh
}

// waitForWPCGMReady blocks until the WPC T-GM advertises clock class 6 and
// CLOCK_REALTIME is LOCKED. The WPC GM does not hardcode clockClass; it
// converges dynamically through the GNSS→ts2phc→DPLL→PMC pipeline.
// Downstream OC/BC profiles that still use the default clock_class_threshold
// of 7 reject GMs with class > 7, so we must wait before any slave sync
// assertion. (TGMBC BC configs raise the threshold so cascading holdover can
// observe CC7.)
// The function is a no-op when there is no WPC GM in the topology.
func waitForWPCGMReady(fullConfig testconfig.TestConfig) {
	if fullConfig.PtpModeDiscovered != testconfig.TelcoGMOC &&
		fullConfig.PtpModeDiscovered != testconfig.TelcoGMBC &&
		fullConfig.PtpModeDiscovered != testconfig.TelcoGrandMasterClock {
		return
	}
	By("Waiting for WPC T-GM to reach clock class 6 (LOCKED)")
	gmPod := getGMPod()
	Eventually(func() bool {
		buf, _, _ := pods.ExecCommand(client.Client, true, gmPod, pkg.PtpContainerName, []string{"curl", pkg.MetricsEndPoint})
		return checkPtp4lClockClassInMetrics(buf.String(), strconv.Itoa(int(fbprotocol.ClockClass6)))
	}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(BeTrue(),
		"WPC T-GM did not reach clock class 6 — downstream clocks cannot leave LISTENING state")
	By("Waiting for WPC T-GM CLOCK_REALTIME (phc2sys -r) to reach LOCKED")
	nodeName := gmPod.Spec.NodeName
	Eventually(func() error {
		return metrics.CheckClockRealTimeState(metrics.MetricClockStateLocked, &nodeName)
	}, pkg.TimeoutIn5Minutes, 5*time.Second).Should(Succeed(),
		"WPC T-GM CLOCK_REALTIME did not reach LOCKED — keep phc2sys -r (VRT per-node CLOCK_REALTIME)")
}

// getGMPod returns the linuxptp-daemon pod running on the node labeled
// ptp/test-grandmaster. Used by tests that need the GM pod directly
// (e.g. TGMBC cascading holdover) regardless of which clock is under test.
func getGMPod() *v1core.Pod {
	nodeList, err := client.Client.Nodes().List(context.Background(),
		metav1.ListOptions{LabelSelector: fmt.Sprintf("%s=", pkg.PtpGrandmasterNodeLabel)})
	ExpectWithOffset(1, err).ToNot(HaveOccurred(), "failed to list nodes with GM label")
	ExpectWithOffset(1, nodeList.Items).ToNot(BeEmpty(), "no node with ptp/test-grandmaster label found")
	gmNodeName := nodeList.Items[0].Name

	ptpPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(),
		metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	for i := range ptpPods.Items {
		if ptpPods.Items[i].Spec.NodeName == gmNodeName {
			return &ptpPods.Items[i]
		}
	}
	Fail(fmt.Sprintf("no linuxptp-daemon pod found on GM node %s", gmNodeName))
	return nil
}

// checkPtp4lClockClassInMetrics requires process="ptp4l", matching main's
// checkClockClassStateReturnBool. A GM can update ts2phc to CC7 while ptp4l
// still announces CC6; accepting ts2phc alone would hide that gap.
func checkPtp4lClockClassInMetrics(metricsOutput string, expectedState string) bool {
	scanner := bufio.NewScanner(strings.NewReader(metricsOutput))
	for scanner.Scan() {
		line := scanner.Text()
		if matches := clockClassRe.FindStringSubmatch(line); matches != nil {
			process := strings.TrimSpace(matches[2])
			class := strings.TrimSpace(matches[3])
			if process == "ptp4l" && class == expectedState {
				return true
			}
		}
	}
	return false
}

// dpllMetricStatusLocked reports whether metrics contain process="dpll" with
// value 3 (LOCKED_HO_ACQ) for the given metric name prefix.
func dpllMetricStatusLocked(metricsOutput, metricName string) bool {
	prefix := metricName + "{"
	scanner := bufio.NewScanner(strings.NewReader(metricsOutput))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, prefix) || !strings.Contains(line, `process="dpll"`) {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == "3" {
			return true
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

// getEvents listens and collects all events per type. It always waits for
// the full timeout so that rapid state transitions (e.g. HOLDOVER→FREERUN
// within 5s) are captured rather than overwritten. All events are kept so
// verification can check whether a particular state was ever observed.
func getGMEvents(
	gnssEventChan <-chan exports.StoredEvent,
	ccEventChan <-chan exports.StoredEvent,
	lsEventChan <-chan exports.StoredEvent,
	timeout time.Duration,
) map[ptpEvent.EventType][]exports.StoredEventValues {

	results := make(map[ptpEvent.EventType][]exports.StoredEventValues)
	timer := time.NewTimer(timeout)

	for {
		select {
		case <-timer.C:
			return results
		case ev := <-gnssEventChan:
			if res, ok := processEvent(ptpEvent.GnssStateChange, ev); ok {
				fmt.Fprintf(GinkgoWriter, "GnssStateChange Event recieved  %v, ", res.Values)
				results[res.Type] = append(results[res.Type], res.Values)
			}
		case ev := <-ccEventChan:
			if res, ok := processEvent(ptpEvent.PtpClockClassChange, ev); ok {
				fmt.Fprintf(GinkgoWriter, "PtpClockClassChange Event recieved  %v, ", res.Values)
				results[res.Type] = append(results[res.Type], res.Values)
			}
		case ev := <-lsEventChan:
			if res, ok := processEvent(ptpEvent.PtpStateChange, ev); ok {
				fmt.Fprintf(GinkgoWriter, "PtpStateChange Event recieved  %v, ", res.Values)
				results[res.Type] = append(results[res.Type], res.Values)
			}
		}
	}
}

// eventValueMatchesString reports whether v equals want. v2 event storage
// keys values by resource path, so callers must scan all map entries.
func eventValueMatchesString(v interface{}, want string) bool {
	s, ok := v.(string)
	return ok && s == want
}

// eventValueMatchesFloat reports whether v equals want. JSON numbers are
// usually float64; also accept int and numeric strings for robustness.
func eventValueMatchesFloat(v interface{}, want float64) bool {
	switch n := v.(type) {
	case float64:
		return n == want
	case float32:
		return float64(n) == want
	case int:
		return float64(n) == want
	case int32:
		return float64(n) == want
	case int64:
		return float64(n) == want
	case json.Number:
		f, err := n.Float64()
		return err == nil && f == want
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return err == nil && f == want
	default:
		return false
	}
}

// verifyEvent checks that at least one collected event carries the expected
// notification state. Multiple events may arrive during the collection window
// (e.g. HOLDOVER followed by FREERUN), so we scan all of them.
// ValueByDataType matches legacy "notification" keys and v2 ".../notification"
// resource-path keys without accepting unrelated data types.
func verifyEvent(allEvents []exports.StoredEventValues, expectedState ptpEvent.SyncState) {
	want := string(expectedState)
	for _, ev := range allEvents {
		if stateVal, ok := event.ValueByDataType(ev, "notification"); ok {
			if eventValueMatchesString(stateVal, want) {
				return
			}
		}
	}
	Fail(fmt.Sprintf("expected state %q not found in %d events: %+v", expectedState, len(allEvents), allEvents))
}

// verifyMetric checks that at least one collected event carries the expected
// metric value. Scans all events since rapid transitions may produce multiple values.
// ValueByDataType matches legacy "metric" keys and v2 ".../metric" resource-path keys.
func verifyMetric(allEvents []exports.StoredEventValues, value float64) {
	for _, ev := range allEvents {
		if metricVal, ok := event.ValueByDataType(ev, "metric"); ok {
			if eventValueMatchesFloat(metricVal, value) {
				return
			}
		}
	}
	Fail(fmt.Sprintf("expected metric [%f] not found in %d events: %+v", value, len(allEvents), allEvents))
}

func stopMonitor(term chan bool) {
	select {
	case term <- true: // tell goroutine to eit(
	default: // avoid blocking if it’s already been stopped
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
				want := string(state)
				if raw, ok2 := event.ValueByDataType(res.Values, "notification"); ok2 {
					if s, ok3 := raw.(string); ok3 && s == want {
						stateSeen = true
					}
				} else if s, ok2 := res.Values["notification"].(string); ok2 && s == want {
					stateSeen = true
				} else {
					for _, val := range res.Values {
						if s, ok2 := val.(string); ok2 && s == want {
							stateSeen = true
							break
						}
					}
				}
			}
		case ev := <-subs.CLOCKCLASS:
			if res, ok := processEvent(ptpEvent.PtpClockClassChange, ev); ok {
				if raw, ok2 := event.ValueByDataType(res.Values, "metric"); ok2 {
					if v, ok3 := raw.(float64); ok3 && int(v) == cc {
						ccSeen = true
					}
				} else if v, ok2 := res.Values["metric"].(float64); ok2 && int(v) == cc {
					ccSeen = true
				} else {
					for _, val := range res.Values {
						if v, ok2 := val.(float64); ok2 && int(v) == cc {
							ccSeen = true
							break
						}
					}
				}
			}
		// Drain GNSS (not needed here) so its buffer can't fill and stall the
		// single blocking PubSub.Publish goroutine (see waitForClockClassViaEvent).
		case <-subs.GNSS:
		}
	}
}

// waitForClockClassViaEvent blocks until a clock-class change event carrying the
// expected class is observed on the cloud-event CLOCKCLASS channel, or fails at
// timeout. It reads only the cloud-event pipeline (no metrics).
func waitForClockClassViaEvent(subs event.Subscriptions, iface string, cc int, timeout time.Duration) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case <-deadline.C:
			Fail(fmt.Sprintf("Timed out waiting for clockClass %d on %s via cloud events", cc, iface))
			return
		case ev := <-subs.CLOCKCLASS:
			if res, ok := processEvent(ptpEvent.PtpClockClassChange, ev); ok {
				if raw, ok2 := event.ValueByInterfaceAndDataType(res.Values, iface, "metric"); ok2 {
					if v, ok3 := raw.(float64); ok3 && int(v) == cc {
						fmt.Fprintf(GinkgoWriter, "ClockClass %d on %s observed via cloud events\n", cc, iface)
						return
					}
				}
			}
		// Drain the channels we don't need. PubSub.Publish sends blockingly from a
		// single monitor goroutine, so an undrained GNSS/LOCKSTATE channel would
		// fill its buffer and stall publishing of the CLOCKCLASS events we wait for.
		case <-subs.GNSS:
		case <-subs.LOCKSTATE:
		}
	}
}

// waitForLockStateViaEvent blocks until a lock-state change event carrying the
// expected sync state is observed on the cloud-event LOCKSTATE channel, or fails
// at timeout. It reads only the cloud-event pipeline (no metrics).
func waitForLockStateViaEvent(subs event.Subscriptions, iface string, state ptpEvent.SyncState, timeout time.Duration) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case <-deadline.C:
			Fail(fmt.Sprintf("Timed out waiting for lock state %s on %s via cloud events", state, iface))
			return
		case ev := <-subs.LOCKSTATE:
			if res, ok := processEvent(ptpEvent.PtpStateChange, ev); ok {
				if raw, ok2 := event.ValueByInterfaceAndDataType(res.Values, iface, "notification"); ok2 {
					if s, ok3 := raw.(string); ok3 && s == string(state) {
						fmt.Fprintf(GinkgoWriter, "Lock state %s on %s observed via cloud events\n", state, iface)
						return
					}
				}
			}
		// Drain the channels we don't need so their buffers can't fill and stall
		// the single blocking PubSub.Publish goroutine (see waitForClockClassViaEvent).
		case <-subs.GNSS:
		case <-subs.CLOCKCLASS:
		}
	}
}

// bcEventContext bundles event subscriptions with an availability flag so
// callers can pass a single value to event-verification helpers.
type bcEventContext struct {
	subs      event.Subscriptions
	available bool
}

// bcProfileHasNetworkDisciplinedOsClock reports whether a BC PtpConfig runs
// phc2sys in automatic mode with CLOCK_REALTIME updates (-a -r). Without VRT,
// Kind/netdevsim CI sets DisableAllSlaveRTUpdate and replaces those opts with "-v".
func bcProfileHasNetworkDisciplinedOsClock(ptpConfig *ptpv1.PtpConfig) bool {
	if ptpConfig == nil {
		return false
	}
	for i := range ptpConfig.Spec.Profile {
		opts := ptpConfig.Spec.Profile[i].Phc2sysOpts
		if opts == nil || *opts == "" {
			continue
		}
		// Field-split so "-r" matches a real flag, not a substring of another opt.
		hasA, hasR := false, false
		for _, f := range strings.Fields(*opts) {
			switch f {
			case "-a":
				hasA = true
			case "-r":
				hasR = true
			}
		}
		if hasA && hasR {
			return true
		}
	}
	return false
}

// clockRealTimeMetricAvailable returns true if CLOCK_REALTIME/phc2sys clock_state
// is present with any recognized state within timeout.
func clockRealTimeMetricAvailable(nodeName string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	states := []metrics.MetricClockState{
		metrics.MetricClockStateLocked,
		metrics.MetricClockStateFreeRun,
		metrics.MetricClockStateHoldOver,
	}
	for time.Now().Before(deadline) {
		for _, st := range states {
			if err := metrics.CheckClockRealTimeState(st, &nodeName); err == nil {
				return true
			}
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

// osClockEventContext holds an OsClockSyncStateChange subscription for BC
// reverse-sync outage tests.
type osClockEventContext struct {
	ch        <-chan exports.StoredEvent
	available bool
}

// setupOsClockSyncStateEvents deploys the consumer, subscribes to
// OsClockSyncStateChange, and starts log monitoring. When ENABLE_PTP_EVENT
// is unset or setup fails, available is false and callers rely on metrics.
func setupOsClockSyncStateEvents(nodeName string) osClockEventContext {
	ctx := osClockEventContext{}
	if !event.Enable() {
		logrus.Info("ENABLE_PTP_EVENT not set; OsClockSyncStateChange checks will be skipped (metric asserts still run)")
		return ctx
	}
	logrus.Info("Deploy consumer app for OsClockSyncStateChange monitoring")
	if createErr := event.CreateConsumerApp(nodeName); createErr != nil {
		logrus.Warnf("PTP events not available: %s; skipping OsClockSyncStateChange checks", createErr)
		return ctx
	}
	if waitErr := event.WaitForConsumerReady(nodeName); waitErr != nil {
		logrus.Warnf("Consumer app not ready: %s; skipping OsClockSyncStateChange checks", waitErr)
		return ctx
	}
	event.InitPubSub()
	const incomingEventsBuffer = 100
	ch, subscriberID := event.PubSub.Subscribe(string(ptpEvent.OsClockSyncStateChange), incomingEventsBuffer)
	termMonitor, monErr := event.MonitorPodLogsRegex()
	if monErr != nil {
		logrus.Warnf("Could not start event monitoring: %s; skipping OsClockSyncStateChange checks", monErr)
		event.PubSub.Unsubscribe(string(ptpEvent.OsClockSyncStateChange), subscriberID)
		event.PubSub.Close()
		if deleteErr := event.DeleteConsumerNamespace(); deleteErr != nil {
			logrus.Debugf("Deleting consumer namespace failed: %s", deleteErr)
		}
		return ctx
	}
	ctx.ch = ch
	ctx.available = true
	DeferCleanup(func() {
		stopMonitor(termMonitor)
		event.PubSub.Unsubscribe(string(ptpEvent.OsClockSyncStateChange), subscriberID)
		event.PubSub.Close()
		if deleteErr := event.DeleteConsumerNamespace(); deleteErr != nil {
			logrus.Debugf("Deleting consumer namespace failed: %s", deleteErr)
		}
	})
	return ctx
}

// waitForOsClockSyncState waits until an OsClockSyncStateChange notification
// matches the expected sync state.
func waitForOsClockSyncState(ch <-chan exports.StoredEvent, expected ptpEvent.SyncState, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			Fail(fmt.Sprintf("Timed out waiting for OsClockSyncStateChange %s", expected))
			return
		case ev := <-ch:
			res, ok := processEvent(ptpEvent.OsClockSyncStateChange, ev)
			if !ok {
				continue
			}
			stateVal, ok := event.ValueByDataType(res.Values, "notification")
			if !ok {
				continue
			}
			state, ok := stateVal.(string)
			if ok && state == string(expected) {
				fmt.Fprintf(GinkgoWriter, "OsClockSyncStateChange %s verified via Event API\n", expected)
				return
			}
		}
	}
}

// correlatePhc2sysReverseSync best-effort checks daemon logs for reverse-sync
// signals after an outage. Hard assertions remain on CLOCK_REALTIME metrics/events.
func correlatePhc2sysReverseSync(pod *v1core.Pod, since time.Time) {
	if pod == nil {
		return
	}
	const reverseSyncPattern = `phc2sys(?m).*?(?:reconfiguring after port state change|sys offset)`
	matches, err := pods.GetPodLogsRegexSince(
		pod.Namespace,
		pod.Name,
		pkg.PtpContainerName,
		reverseSyncPattern,
		false,
		pkg.TimeoutIn1Minute,
		since,
	)
	if err != nil || len(matches) == 0 {
		logrus.Warnf("phc2sys reverse-sync log correlate: no reconfiguring/sys offset lines yet (logReduce may hide them): err=%v", err)
		return
	}
	logrus.Infof("phc2sys reverse-sync correlate: saw %d matching log line(s), last=%q",
		len(matches), matches[len(matches)-1][0])
}

// setupBCClockClassEvents creates the consumer app, initializes PubSub,
// subscribes to GM change events, starts the log monitor, and registers
// DeferCleanup for all resources. Returns a bcEventContext; if any setup
// step fails the available flag is false and event checks become no-ops.
func setupBCClockClassEvents(nodeName string) bcEventContext {
	ctx := bcEventContext{}
	if !event.Enable() {
		return ctx
	}
	logrus.Info("Deploy consumer app for BC clock class event monitoring")
	if createErr := event.CreateConsumerApp(nodeName); createErr != nil {
		logrus.Warnf("PTP events not available: %s; skipping event checks", createErr)
		return ctx
	}
	if waitErr := event.WaitForConsumerReady(nodeName); waitErr != nil {
		logrus.Warnf("Consumer app not ready: %s; skipping event checks", waitErr)
		return ctx
	}
	event.InitPubSub()
	var eventCleanup func()
	ctx.subs, eventCleanup = event.SubscribeToGMChangeEvents(100, true, 60*time.Second)
	termMonitor, monErr := event.MonitorPodLogsRegex()
	if monErr != nil {
		logrus.Warnf("Could not start event monitoring: %s; skipping event checks", monErr)
	} else {
		ctx.available = true
	}
	DeferCleanup(func() {
		if termMonitor != nil {
			stopMonitor(termMonitor)
		}
		eventCleanup()
		event.PubSub.Close()
		if deleteErr := event.DeleteConsumerNamespace(); deleteErr != nil {
			logrus.Debugf("Deleting consumer namespace failed: %s", deleteErr)
		}
	})
	return ctx
}

// verifyClockClassViaEvent drains the clock-class event channel and asserts the
// expected value. No-op when events are not available.
func verifyClockClassViaEvent(evCtx bcEventContext, expectedClass int) {
	if !evCtx.available {
		return
	}
	events := getGMEvents(evCtx.subs.GNSS, evCtx.subs.CLOCKCLASS, evCtx.subs.LOCKSTATE, 10*time.Second)
	verifyMetric(events[ptpEvent.PtpClockClassChange], float64(expectedClass))
}

// verifyClockClassCurrentState re-queries Event API getCurrentState until any
// clock-class value matches. After a cloud-event-proxy restart the first
// CurrentState can be 255/248 (BC master port or unpopulated stats) while
// ptp4l metrics already show 6; a single PushInitialEvent would accept that
// first event and then wait for a CLOCK_CLASS_CHANGE that never comes.
func verifyClockClassCurrentState(expectedClockClass int, timeout time.Duration) {
	const incomingEventsBuffer = 100
	const pushTimeout = 15 * time.Second
	ccTopic := string(ptpEvent.PtpClockClassChange)
	deadline := time.Now().Add(timeout)
	want := float64(expectedClockClass)

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			Fail(fmt.Sprintf("Timed out waiting for clockClass %d via Event API", expectedClockClass))
			return
		}
		ccCh, cID := event.PubSub.Subscribe(ccTopic, incomingEventsBuffer)
		thisPush := pushTimeout
		if remaining < thisPush {
			thisPush = remaining
		}
		_ = event.PushInitialEvent(ccTopic, thisPush)
		matched := false
	drain:
		for {
			select {
			case ev := <-ccCh:
				res, ok := processEvent(ptpEvent.PtpClockClassChange, ev)
				if !ok {
					continue
				}
				for _, val := range res.Values {
					if eventValueMatchesFloat(val, want) {
						matched = true
						break drain
					}
				}
				fmt.Fprintf(GinkgoWriter, "ClockClass CurrentState did not match %d: %v\n", expectedClockClass, res.Values)
			default:
				break drain
			}
		}
		event.PubSub.Unsubscribe(ccTopic, cID)
		if matched {
			fmt.Fprintf(GinkgoWriter, "ClockClass %d verified via Event API\n", expectedClockClass)
			return
		}
		time.Sleep(2 * time.Second)
	}
}

type authNegativeState struct {
	ptpConfigName     string
	profileName       string
	nodeName          string
	secretEntryName   string
	configFile        string
	originalSAFile    string
	isolatedSAFile    string
	originalSAD       []byte
	originalPtp4lConf string
	cleaned           bool
}

func profileByName(ptpConfig *ptpv1.PtpConfig, profileName string) (*ptpv1.PtpProfile, error) {
	if ptpConfig == nil {
		return nil, fmt.Errorf("PtpConfig is nil")
	}
	var match *ptpv1.PtpProfile
	for i := range ptpConfig.Spec.Profile {
		profile := &ptpConfig.Spec.Profile[i]
		if profile.Name == nil || *profile.Name != profileName {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("PtpConfig %s has more than one profile named %q", ptpConfig.Name, profileName)
		}
		match = profile
	}
	if match == nil {
		return nil, fmt.Errorf("PtpConfig %s has no profile named %q", ptpConfig.Name, profileName)
	}
	return match, nil
}

func authNegativeSecretEntryName() string {
	return "auth-negative-" + time.Now().UTC().Format("20060102T150405.000000000") + ".conf"
}

func listProcessesInPod(pod *v1core.Pod) (string, error) {
	if pod == nil {
		return "", fmt.Errorf("clock-under-test pod is nil")
	}
	output, _, err := pods.ExecCommand(client.Client, false, pod, pkg.PtpContainerName,
		[]string{"ps", "-eo", "pid=,args="})
	if err != nil {
		return "", fmt.Errorf("list processes in pod %s/%s: %w", pod.Namespace, pod.Name, err)
	}
	return output.String(), nil
}

// ptp4lPIDsFromPS parses `ps -eo pid=,args=` output and returns the
// PIDs of ptp4l processes whose -f argument is configFile; an empty configFile
// selects every ptp4l process. Rows that do not parse or belong to other
// executables (for example shell wrappers) are skipped, so a single unrelated
// odd process row cannot fail the check.
func ptp4lPIDsFromPS(psOutput, configFile string) []int {
	var pids []int
	for _, line := range strings.Split(psOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 || path.Base(fields[1]) != "ptp4l" {
			continue
		}
		if configFile == "" {
			pids = append(pids, pid)
			continue
		}
		for i := 2; i+1 < len(fields); i++ {
			if fields[i] == "-f" && fields[i+1] == configFile {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids
}

// ptp4lPIDsOnNode lists ptp4l PIDs on the daemon pod running on nodeName
// whose -f argument is configFile. It composes listProcessesInPod (which owns the ps
// invocation) with ptp4lPIDsFromPS (which parses its output).
func ptp4lPIDsOnNode(nodeName, configFile string) ([]int, error) {
	pod, err := ptphelper.GetPtpPodOnNode(nodeName)
	if err != nil {
		return nil, err
	}
	psOutput, err := listProcessesInPod(&pod)
	if err != nil {
		return nil, err
	}
	return ptp4lPIDsFromPS(psOutput, configFile), nil
}

// sampleListeningPtp4l returns the single live ptp4l PID for configFile on
// nodeName, requiring slaveInterface to be LISTENING. A crash-restart loop
// still shows one live process at every sample, but never the same PID.
func sampleListeningPtp4l(nodeName, configFile, slaveInterface string) (int, error) {
	pids, err := ptp4lPIDsOnNode(nodeName, configFile)
	if err != nil {
		return 0, err
	}
	if len(pids) != 1 {
		return 0, fmt.Errorf("expected exactly one live ptp4l process for %s on node %s, found %v", configFile, nodeName, pids)
	}
	if roleErr := metrics.CheckClockRole([]metrics.MetricRole{metrics.MetricRoleListening},
		[]string{slaveInterface}, &nodeName); roleErr != nil {
		return pids[0], fmt.Errorf("receive interface %s is not LISTENING: %w", slaveInterface, roleErr)
	}
	return pids[0], nil
}

func updateAuthNegativeProfileConf(configName, profileName, desired string) error {
	ptpConfig, err := client.Client.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Get(
		context.Background(), configName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get PtpConfig %s: %w", configName, err)
	}
	profile, err := profileByName(ptpConfig, profileName)
	if err != nil {
		return err
	}
	updated := desired
	profile.Ptp4lConf = &updated
	if _, err = client.Client.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Update(
		context.Background(), ptpConfig, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update PtpConfig %s: %w", configName, err)
	}
	return nil
}

func setAuthNegativeSecretEntry(entryName string, data []byte) error {
	secret, err := client.Client.CoreV1().Secrets(pkg.PtpLinuxDaemonNamespace).Get(
		context.Background(), pkg.PtpSecurityConfSecretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get Secret %s: %w", pkg.PtpSecurityConfSecretName, err)
	}
	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data[entryName] = append([]byte(nil), data...)
	if _, err = client.Client.CoreV1().Secrets(pkg.PtpLinuxDaemonNamespace).Update(
		context.Background(), secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("set Secret %s entry %s: %w", pkg.PtpSecurityConfSecretName, entryName, err)
	}
	return nil
}

func removeAuthNegativeSecretEntry(state *authNegativeState) error {
	ptpConfig, err := client.Client.PtpConfigs(pkg.PtpLinuxDaemonNamespace).Get(
		context.Background(), state.ptpConfigName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get PtpConfig %s before removing isolated SAD: %w", state.ptpConfigName, err)
	}
	profile, err := profileByName(ptpConfig, state.profileName)
	if err != nil {
		return err
	}
	if profile.Ptp4lConf != nil && strings.Contains(*profile.Ptp4lConf, "sa_file "+state.isolatedSAFile) {
		return fmt.Errorf("profile %s still references isolated SAD %s", state.profileName, state.secretEntryName)
	}
	secret, err := client.Client.CoreV1().Secrets(pkg.PtpLinuxDaemonNamespace).Get(
		context.Background(), pkg.PtpSecurityConfSecretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get Secret %s before removing isolated SAD: %w", pkg.PtpSecurityConfSecretName, err)
	}
	if _, exists := secret.Data[state.secretEntryName]; !exists {
		return nil
	}
	delete(secret.Data, state.secretEntryName)
	if _, err = client.Client.CoreV1().Secrets(pkg.PtpLinuxDaemonNamespace).Update(
		context.Background(), secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("remove Secret %s entry %s: %w", pkg.PtpSecurityConfSecretName, state.secretEntryName, err)
	}
	return nil
}

func restoreAuthNegativeState(state *authNegativeState) error {
	if state == nil || state.cleaned {
		return nil
	}
	err := updateAuthNegativeProfileConf(state.ptpConfigName, state.profileName, state.originalPtp4lConf)
	if err == nil {
		err = waitAuthNegativeRenderedConfig(state, state.originalSAFile)
	}
	if err != nil {
		// Single safety repair: if anything still references the isolated
		// entry, make it a copy of the original SAD so the process can
		// authenticate again.
		if repairErr := setAuthNegativeSecretEntry(state.secretEntryName, state.originalSAD); repairErr != nil {
			return fmt.Errorf("restore failed (%v), and repairing isolated SAD failed: %w", err, repairErr)
		}
		return err
	}
	if err := removeAuthNegativeSecretEntry(state); err != nil {
		return fmt.Errorf("remove isolated SAD entry %s: %w", state.secretEntryName, err)
	}
	state.cleaned = true
	return nil
}

func execInDaemonPodOnNode(nodeName string, command []string) (string, error) {
	pod, err := ptphelper.GetPtpPodOnNode(nodeName)
	if err != nil {
		return "", err
	}
	output, _, err := pods.ExecCommand(client.Client, false, &pod, pkg.PtpContainerName, command)
	if err != nil {
		return "", err
	}
	return output.String(), nil
}

func waitAuthNegativeRenderedConfig(state *authNegativeState, expectedSAFile string) error {
	deadline := time.Now().Add(pkg.TimeoutIn5Minutes)
	var lastErr error
	for {
		output, err := execInDaemonPodOnNode(state.nodeName, []string{"cat", state.configFile})
		if err != nil {
			lastErr = err
		} else if observed := parsePtp4lConf(output)["global"]["sa_file"]; observed != expectedSAFile {
			lastErr = fmt.Errorf("rendered global sa_file is %q, expected %q", observed, expectedSAFile)
		} else {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("timed out waiting for rendered config at %s: %w", state.configFile, lastErr)
		}
		time.Sleep(pkg.Timeout10Seconds)
	}
}

// parsePtp4lConf parses a rendered ptp4l config into a map of section
// name to option name/value pairs.
func parsePtp4lConf(config string) map[string]map[string]string {
	options := make(map[string]map[string]string)
	currentSection := ""
	for _, rawLine := range strings.Split(config, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			continue
		}
		fields := strings.Fields(line)
		if currentSection != "" && len(fields) == 2 {
			if options[currentSection] == nil {
				options[currentSection] = make(map[string]string)
			}
			options[currentSection][fields[0]] = fields[1]
		}
	}
	return options
}

func checkAuthNegativeAppliedConfig(config, expectedSAFile string, transmitInterfaces []string) error {
	options := parsePtp4lConf(config)
	if options["global"]["sa_file"] != expectedSAFile {
		return fmt.Errorf("rendered global sa_file is %q, expected %q", options["global"]["sa_file"], expectedSAFile)
	}
	if options["global"]["spp"] != "-1" || options["global"]["logging_level"] != "6" {
		return fmt.Errorf("rendered global authentication defaults or logging level have not converged")
	}
	for _, interfaceName := range transmitInterfaces {
		if options[interfaceName]["spp"] != "1" || options[interfaceName]["active_key_id"] != "2" {
			return fmt.Errorf("rendered signing key has not converged to key 2 for interface %q", interfaceName)
		}
	}
	return nil
}

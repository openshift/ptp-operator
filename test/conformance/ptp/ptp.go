package ptp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/ptp-operator/test/utils/event"

	v1 "k8s.io/api/apps/v1"
	v1core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"

	. "github.com/openshift/ptp-operator/test/utils"
	"github.com/openshift/ptp-operator/test/utils/clean"
	"github.com/openshift/ptp-operator/test/utils/client"
	testclient "github.com/openshift/ptp-operator/test/utils/client"
	"github.com/openshift/ptp-operator/test/utils/execute"
	"github.com/openshift/ptp-operator/test/utils/nodes"
	"github.com/openshift/ptp-operator/test/utils/pods"
	"github.com/openshift/ptp-operator/test/utils/testconfig"
)

var _ = Describe("[ptp]", func() {
	BeforeEach(func() {
		Expect(testclient.Client).NotTo(BeNil())
	})

	Context("PTP configuration verifications", func() {
		// Setup verification
		// if requested enabled  ptp events
		It("Should check whether PTP operator needs to enable PTP events", func() {
			By("Find if variable set to enable ptp events")
			if event.Enable() {
				Expect(enablePTPEvent()).NotTo(HaveOccurred())
				ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(PtpLinuxDaemonNamespace).Get(context.Background(), "default", metav1.GetOptions{})
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

			ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(PtpLinuxDaemonNamespace).Get(context.Background(), "default", metav1.GetOptions{})
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
				if c.Type == v1.DeploymentAvailable {
					Expect(string(c.Status)).Should(Equal("True"), PtpOperatorDeploymentName+" deployment is not available")
				}
			}
		})

	})

	Describe("PTP e2e tests", func() {
		var ptpRunningPods []v1core.Pod
		var fifoPriorities map[string]int64
		var fullConfig testconfig.TestConfig
		execute.BeforeAll(func() {

			configurePTP()
			fullConfig = testconfig.GetFullDiscoveredConfig(PtpLinuxDaemonNamespace, false)

			//waitForPtpDaemonToBeReady()
			restartPtpDaemon()

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

				for _, pod := range ptpPods.Items {
					if podRole(pod, fullConfig.DiscoveredSlavePtpConfig.Label) {
						waitUntilLogIsDetected(pod, 3*time.Minute, "Profile Name:")
						ptpSlaveRunningPods = append(ptpSlaveRunningPods, pod)
					} else if podRole(pod, fullConfig.DiscoveredMasterPtpConfig.Label) {
						waitUntilLogIsDetected(pod, 3*time.Minute, "Profile Name:")
						ptpMasterRunningPods = append(ptpMasterRunningPods, pod)
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
				for _, pod := range ptpRunningPods {
					ptpSupportedInt := getPtpMasterSlaveAttachedInterfaces(pod)
					Expect(len(ptpSupportedInt)).To(BeNumerically(">", 0), "Fail to detect PTP Supported interfaces on slave/master pods")
					ptpDiscoveredInterfaces := ptpDiscoveredInterfaceList(NodePtpDeviceAPIPath + pod.Spec.NodeName)
					for _, intfc := range ptpSupportedInt {
						Expect(ptpDiscoveredInterfaces).To(ContainElement(intfc))
					}
				}
			})

			// 25730
			It("The virtual interfaces should be not discovered by ptp", func() {
				for _, pod := range ptpRunningPods {
					ptpNotSupportedInt := getNonPtpMasterSlaveAttachedInterfaces(pod)
					ptpDiscoveredInterfaces := ptpDiscoveredInterfaceList(NodePtpDeviceAPIPath + pod.Spec.NodeName)
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

				for _, pod := range ptpRunningPods {
					podLogs, err := pods.GetLog(&pod, PtpContainerName)
					Expect(err).NotTo(HaveOccurred(), "Error to find needed log due to %s", err)
					if podRole(pod, fullConfig.DiscoveredSlavePtpConfig.Label) {
						Expect(podLogs).Should(ContainSubstring(profileSlave),
							fmt.Sprintf("Profile \"%s\" not found in pod's log %s", profileSlave, pod.Name))
					}
					if podRole(pod, fullConfig.DiscoveredMasterPtpConfig.Label) && fullConfig.PtpModeDesired != testconfig.Discovery {
						Expect(podLogs).Should(ContainSubstring(profileMaster),
							fmt.Sprintf("Profile \"%s\" not found in pod's log %s", profileSlave, pod.Name))
					}
				}
			})

			// 25738
			It("Slave can sync to master", func() {
				var masterID string
				var slaveMasterID string
				grandMaster := "assuming the grand master role"
				for _, pod := range ptpRunningPods {
					if fullConfig.DiscoveredMasterPtpConfig.Label != "" &&
						podRole(pod, fullConfig.DiscoveredMasterPtpConfig.Label) {
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
					if podRole(pod, fullConfig.DiscoveredSlavePtpConfig.Label) {
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
			})

			//25743
			It("Can provide a profile with higher priority", func() {
				var testPtpPod v1core.Pod
				if fullConfig.PtpModeDesired == testconfig.Discovery {
					Skip("Skipping because adding a different profile")
				}

				By("Creating a config with higher priority", func() {
					ptpConfigSlave, err := client.Client.PtpV1Interface.PtpConfigs("openshift-ptp").Get(context.Background(), PtpSlavePolicyName, metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())
					nodes, err := client.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
						LabelSelector: fullConfig.DiscoveredSlavePtpConfig.Label,
					})
					Expect(err).NotTo(HaveOccurred())
					Expect(len(nodes.Items)).To(BeNumerically(">", 0),
						fmt.Sprintf("PTP Nodes with label %s are not deployed on cluster", fullConfig.DiscoveredSlavePtpConfig.Label))

					ptpConfigTest := mutateProfile(*ptpConfigSlave, PtpSlavePolicyName, nodes.Items[0].Name)
					_, err = client.Client.PtpV1Interface.PtpConfigs("openshift-ptp").Create(context.Background(), ptpConfigTest, metav1.CreateOptions{})
					Expect(err).NotTo(HaveOccurred())

					testPtpPod, err = getPtpPodOnNode(nodes.Items[0].Name)
					Expect(err).NotTo(HaveOccurred())

					testPtpPod, err = replaceTestPod(testPtpPod, time.Minute)
					Expect(err).NotTo(HaveOccurred())
				})

				By("Checking if Node has Profile", func() {
					waitUntilLogIsDetected(testPtpPod, 3*time.Minute, "Profile Name: test")
				})

				By("Deleting the test profile", func() {
					err := client.Client.PtpV1Interface.PtpConfigs("openshift-ptp").Delete(context.Background(), "test", metav1.DeleteOptions{})
					Expect(err).NotTo(HaveOccurred())
					Eventually(func() bool {
						_, err := client.Client.PtpV1Interface.PtpConfigs("openshift-ptp").Get(context.Background(), "test", metav1.GetOptions{})
						return errors.IsNotFound(err)
					}, 1*time.Minute, 1*time.Second).Should(BeTrue(), "Could not delete the test profile")
				})

				By("Checking the profile is reverted", func() {
					waitUntilLogIsDetected(testPtpPod, 3*time.Minute, "Profile Name: test-slave")
				})
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

				for _, pod := range ptpPods.Items {
					if podRole(pod, fullConfig.DiscoveredSlavePtpConfig.Label) {
						waitUntilLogIsDetected(pod, 3*time.Minute, "Profile Name:")
						ptpSlaveRunningPods = append(ptpSlaveRunningPods, pod)
					} else if podRole(pod, fullConfig.DiscoveredMasterPtpConfig.Label) {
						waitUntilLogIsDetected(pod, 3*time.Minute, "Profile Name:")
						ptpMasterRunningPods = append(ptpMasterRunningPods, pod)
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
				for _, pod := range ptpRunningPods {
					if podRole(pod, fullConfig.DiscoveredSlavePtpConfig.Label) {
						Eventually(func() string {
							buf, _ := pods.ExecCommand(client.Client, pod, PtpContainerName, []string{"curl", "127.0.0.1:9091/metrics"})
							return buf.String()
						}, 5*time.Minute, 5*time.Second).Should(ContainSubstring("openshift_ptp_offset_ns"),
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

				for _, pod := range ptpPods.Items {
					if podRole(pod, fullConfig.DiscoveredSlavePtpConfig.Label) {
						waitUntilLogIsDetected(pod, 3*time.Minute, "Profile Name:")
						ptpSlaveRunningPods = append(ptpSlaveRunningPods, pod)
					}
				}
				if fullConfig.PtpModeDesired == testconfig.Discovery {
					Expect(len(ptpSlaveRunningPods)).To(BeNumerically(">=", 1), "Fail to detect PTP slave pods on Cluster")
				}
			})

			It("Should check for ptp events ", func() {
				By("Checking event side car is present")

				for _, pod := range ptpSlaveRunningPods {
					cloudProxyFound := false
					Expect(len(pod.Spec.Containers)).To(BeNumerically("==", 3), "linuxptp-daemon is not deployed on cluster with cloud event proxy")
					for _, c := range pod.Spec.Containers {
						if c.Name == EventProxyContainerName {
							cloudProxyFound = true
						}
					}
					Expect(cloudProxyFound).ToNot(BeFalse(), "No event pods detected")
				}

				By("Checking event metrics are present")
				for _, pod := range ptpSlaveRunningPods {
					Eventually(func() string {
						buf, _ := pods.ExecCommand(client.Client, pod, EventProxyContainerName, []string{"curl", "127.0.0.1:9091/metrics"})
						return buf.String()
					}, 5*time.Minute, 5*time.Second).Should(ContainSubstring("openshift_ptp_interface_role"),
						"Interface role metrics are not detected")

					Eventually(func() string {
						buf, _ := pods.ExecCommand(client.Client, pod, EventProxyContainerName, []string{"curl", "127.0.0.1:9091/metrics"})
						return buf.String()
					}, 5*time.Minute, 5*time.Second).Should(ContainSubstring("openshift_ptp_threshold"),
						"Threshold metrics are not detected")
				}

				By("Checking event api is healthy")
				for _, pod := range ptpSlaveRunningPods {
					Eventually(func() string {
						buf, _ := pods.ExecCommand(client.Client, pod, EventProxyContainerName, []string{"curl", "127.0.0.1:9085/api/cloudNotifications/v1/health"})
						return buf.String()
					}, 5*time.Minute, 5*time.Second).Should(ContainSubstring("OK"),
						"Event API is not in healthy state")
				}

				By("Checking ptp publisher is created")
				for _, pod := range ptpSlaveRunningPods {
					Eventually(func() string {
						buf, _ := pods.ExecCommand(client.Client, pod, EventProxyContainerName, []string{"curl", "127.0.0.1:9085/api/cloudNotifications/v1/publishers"})
						return buf.String()
					}, 5*time.Minute, 5*time.Second).Should(ContainSubstring("endpointUri"),
						"Event API  did not return publishers")
				}

				By("Checking events are generated")
				for _, pod := range ptpSlaveRunningPods {
					podLogs, err := pods.GetLog(&pod, EventProxyContainerName)
					Expect(err).NotTo(HaveOccurred(), "Error to find needed log due to %s", err)
					Expect(podLogs).Should(ContainSubstring("Created publisher"),
						fmt.Sprintf("PTP event publisher was not created in pod %s", pod.Name))
					Expect(podLogs).Should(ContainSubstring("event sent"),
						fmt.Sprintf("PTP event was not generated in the pod %s", pod.Name))
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
				Expect(len(ptpPods.Items)).To(BeNumerically(">", 0), fmt.Sprint("linuxptp-daemon is not deployed on cluster"))
				for name, priority := range fifoPriorities {
					ptp4lLog := fmt.Sprintf("/bin/chrt -f %d /usr/sbin/ptp4l", priority)
					for _, pod := range ptpPods.Items {
						logs, err := pods.GetLog(&pod, PtpContainerName)
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

func configurePTP() {
	ptpNodes, err := nodes.PtpEnabled(client.Client)
	Expect(err).ToNot(HaveOccurred())
	if testconfig.GetDesiredConfig(false).PtpModeDesired == testconfig.OrdinaryClock {
		err := clean.All()
		Expect(err).ToNot(HaveOccurred())
		if len(ptpNodes) > 1 {
			testconfig.GlobalConfig.ConfiguredMasterPresent = true
			configurePTPMasterSlave(ptpNodes)
			return
		}
		if len(ptpNodes) > 0 {
			testconfig.GlobalConfig.ConfiguredMasterPresent = false
			configurePTPSlaveOnly(ptpNodes)
			return
		}
	}
}

func configurePTPMasterSlave(ptpNodes []*nodes.NodeTopology) {

	Expect(len(ptpNodes)).To(BeNumerically(">", 1), "need at least two nodes with ptp capable nics")

	By("Labeling the grandmaster node")
	ptpGrandMasterNode := ptpNodes[0]
	var err error
	ptpGrandMasterNode.NodeObject, err = nodes.LabelNode(ptpGrandMasterNode.NodeName, PtpGrandmasterNodeLabel, "")
	Expect(err).ToNot(HaveOccurred())

	By("Labeling the slave node")
	ptpSlaveNode := ptpNodes[1]
	ptpSlaveNode.NodeObject, err = nodes.LabelNode(ptpSlaveNode.NodeName, PtpSlaveNodeLabel, "")
	Expect(err).ToNot(HaveOccurred())

	ptpSchedulingPolicy := "SCHED_OTHER"
	configureFifo, err := strconv.ParseBool(os.Getenv("CONFIGURE_FIFO"))
	if err == nil && configureFifo {
		ptpSchedulingPolicy = "SCHED_FIFO"
	}
	for _, gmInterface := range ptpGrandMasterNode.InterfaceList {
		for _, slaveInterface := range ptpSlaveNode.InterfaceList {
			clean.Configs()
			fmt.Printf("Validating interface %s for grandmaster, %s for slave\n", gmInterface, slaveInterface)
			By("Creating the policy for the grandmaster node")
			err = createConfig(PtpGrandMasterPolicyName,
				gmInterface,
				"-2",
				testconfig.BasePtp4lConfig,
				"-a -r -r",
				PtpGrandmasterNodeLabel,
				pointer.Int64Ptr(5),
				ptpSchedulingPolicy,
				pointer.Int64Ptr(65))
			Expect(err).ToNot(HaveOccurred())

			By("Creating the policy for the slave node")
			err = createConfig(PtpSlavePolicyName,
				slaveInterface,
				"-s -2",
				testconfig.BasePtp4lConfig,
				"-a -r",
				PtpSlaveNodeLabel,
				pointer.Int64Ptr(5),
				ptpSchedulingPolicy,
				pointer.Int64Ptr(65))
			Expect(err).ToNot(HaveOccurred())

			By("Restart the linuxptp-daemon pods")
			restartPtpDaemon()
			valid, err := waitForNewForeignMaster(ptpSlaveNode, slaveInterface)
			if err == nil && valid {
				return
			}
		}
	}
	fmt.Println("Did not find a valid PTP configuration")

	// no valid config found, removing last tested ptpconfig
	clean.All()
}
func configurePTPSlaveOnly(ptpNodes []*nodes.NodeTopology) {
	Expect(len(ptpNodes)).To(BeNumerically(">", 0), "need at least one node with ptp capable nic")
	By("Labeling the slave node")
	ptpSlaveNode := ptpNodes[0]
	var err error
	ptpSlaveNode.NodeObject, err = nodes.LabelNode(ptpSlaveNode.NodeName, PtpSlaveNodeLabel, "")
	Expect(err).ToNot(HaveOccurred())

	ptpSchedulingPolicy := "SCHED_OTHER"
	configureFifo, err := strconv.ParseBool(os.Getenv("CONFIGURE_FIFO"))
	if err == nil && configureFifo {
		ptpSchedulingPolicy = "SCHED_FIFO"
	}
	for _, slaveInterface := range ptpSlaveNode.InterfaceList {
		clean.Configs()
		fmt.Printf("Validating interface %s for slave and external grandmaster\n", slaveInterface)

		By("Creating the policy for the slave node")
		err = createConfig(PtpSlavePolicyName,
			slaveInterface,
			"-s -2",
			testconfig.BasePtp4lConfig,
			"-a -r",
			PtpSlaveNodeLabel,
			pointer.Int64Ptr(5),
			ptpSchedulingPolicy,
			pointer.Int64Ptr(65))
		Expect(err).ToNot(HaveOccurred())

		By("Restart the linuxptp-daemon pods")
		restartPtpDaemon()
		valid, err := waitForNewForeignMaster(ptpSlaveNode, slaveInterface)
		if err == nil && valid {
			return
		}
	}
	fmt.Println("Did not find a valid PTP configuration")

	// no valid config found, removing last tested ptpconfig
	clean.All()
}

func waitForNewForeignMaster(ptpSlaveNode *nodes.NodeTopology, slaveInterface string) (valid bool, err error) {
	const Timeout = 60
	for i := 0; i < Timeout; i++ {
		time.Sleep(1 * time.Second)
		ptpPods, err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).List(context.Background(),
			metav1.ListOptions{LabelSelector: "app=linuxptp-daemon", FieldSelector: fmt.Sprintf("spec.nodeName=%s", ptpSlaveNode.NodeName)})
		if err != nil {
			return false, fmt.Errorf("could not get ptp daemon pods")
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
	for _, pod := range ptpPods.Items {
		err = client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{GracePeriodSeconds: pointer.Int64Ptr(0)})
		Expect(err).ToNot(HaveOccurred())
	}

	waitForPtpDaemonToBeReady()
}

func waitForPtpDaemonToBeReady() {
	daemonset, err := client.Client.DaemonSets(PtpLinuxDaemonNamespace).Get(context.Background(), PtpDaemonsetName, metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred())
	expectedNumber := daemonset.Status.DesiredNumberScheduled
	Eventually(func() int32 {
		daemonset, err = client.Client.DaemonSets(PtpLinuxDaemonNamespace).Get(context.Background(), PtpDaemonsetName, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		return daemonset.Status.NumberReady
	}, 2*time.Minute, 2*time.Second).Should(Equal(expectedNumber))

	Eventually(func() int {
		ptpPods, err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
		Expect(err).ToNot(HaveOccurred())
		return len(ptpPods.Items)
	}, 2*time.Minute, 2*time.Second).Should(Equal(int(expectedNumber)))
}

// Returns the slave node label to be used in the test
func discoveryPTPConfiguration(namespace string) ([]ptpv1.PtpConfig, []ptpv1.PtpConfig) {
	var masters []ptpv1.PtpConfig
	var slaves []ptpv1.PtpConfig

	configList, err := client.Client.PtpConfigs(namespace).List(context.Background(), metav1.ListOptions{})
	Expect(err).ToNot(HaveOccurred())
	for _, config := range configList.Items {
		for _, profile := range config.Spec.Profile {
			if testconfig.IsPtpMaster(profile.Ptp4lOpts, profile.Phc2sysOpts) {
				masters = append(masters, config)
			}
			if testconfig.IsPtpSlave(profile.Ptp4lOpts, profile.Phc2sysOpts) {
				slaves = append(slaves, config)
			}
		}
	}

	return masters, slaves
}

func podRole(runningPod v1core.Pod, role string) bool {
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

func mutateProfile(profile ptpv1.PtpConfig, profileName string, nodeName string) *ptpv1.PtpConfig {
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

func waitUntilLogIsDetected(pod v1core.Pod, timeout time.Duration, neededLog string) {
	Eventually(func() string {
		logs, _ := pods.GetLog(&pod, PtpContainerName)
		return logs
	}, timeout, 1*time.Second).Should(ContainSubstring(neededLog), fmt.Sprintf("Timeout to detect log \"%s\" in pod \"%s\"", neededLog, pod.Name))
}

func getPtpPodOnNode(nodeName string) (v1core.Pod, error) {
	runningPod, err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	Expect(err).NotTo(HaveOccurred(), "Error to get list of pods by label: app=linuxptp-daemon")
	Expect(len(runningPod.Items)).To(BeNumerically(">", 0), "PTP pods are  not deployed on cluster")
	for _, pod := range runningPod.Items {

		if pod.Spec.NodeName == nodeName {
			return pod, nil
		}
	}
	return v1core.Pod{}, fmt.Errorf("Pod not found")
}

func getMasterSlaveAttachedInterfaces(pod v1core.Pod) []string {
	var IntList []string
	Eventually(func() error {
		stdout, err := pods.ExecCommand(client.Client, pod, PtpContainerName, []string{"ls", "/sys/class/net/"})
		if err != nil {
			return err
		}

		if stdout.String() == "" {
			return fmt.Errorf("empty response from pod retrying")
		}

		IntList = strings.Split(strings.Join(strings.Fields(stdout.String()), " "), " ")
		if len(IntList) == 0 {
			return fmt.Errorf("No interface detected")
		}

		return nil
	}, 3*time.Minute, 5*time.Second).Should(BeNil())

	return IntList
}

func getPtpMasterSlaveAttachedInterfaces(pod v1core.Pod) []string {
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
				return fmt.Errorf("empty response from pod retrying")
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
		}, 3*time.Minute, 5*time.Second).Should(BeNil())

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
				return fmt.Errorf("empty response from pod retrying")
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
				return fmt.Errorf("empty response from pod retrying")
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

func replaceTestPod(pod v1core.Pod, timeout time.Duration) (v1core.Pod, error) {
	var newPod v1core.Pod

	err := client.Client.CoreV1().Pods(PtpLinuxDaemonNamespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{
		GracePeriodSeconds: pointer.Int64Ptr(0)})
	Expect(err).NotTo(HaveOccurred())

	Eventually(func() error {
		newPod, err = getPtpPodOnNode(pod.Spec.NodeName)

		if err == nil && newPod.Name != pod.Name && newPod.Status.Phase == "Running" {
			return nil
		}

		return fmt.Errorf("Can not replace PTP pod")
	}, timeout, 1*time.Second).Should(BeNil())

	return newPod, nil
}

func getNonPtpMasterSlaveAttachedInterfaces(pod v1core.Pod) []string {
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
				return fmt.Errorf("empty response from pod retrying")
			}
			return nil
		}, 3*time.Minute, 2*time.Second).Should(BeNil())

		if strings.Contains(stdout.String(), "No such device") {
			continue
		}

		if !isPTPEnabled(&stdout) {
			ptpSupportedInterfaces = append(ptpSupportedInterfaces, interf)
		}
	}
	return ptpSupportedInterfaces
}

func createConfig(profileName, ifaceName, ptp4lOpts, ptp4lConfig, phc2sysOpts, nodeLabel string, priority *int64, ptpSchedulingPolicy string, ptpSchedulingPriority *int64) error {
	ptpProfile := ptpv1.PtpProfile{Name: &profileName, Interface: &ifaceName, Phc2sysOpts: &phc2sysOpts, Ptp4lOpts: &ptp4lOpts, PtpSchedulingPolicy: &ptpSchedulingPolicy, PtpSchedulingPriority: ptpSchedulingPriority}
	if ptp4lConfig != "" {
		ptpProfile.Ptp4lConf = &ptp4lConfig
	}
	matchRule := ptpv1.MatchRule{NodeLabel: &nodeLabel}
	ptpRecommend := ptpv1.PtpRecommend{Profile: &profileName, Priority: priority, Match: []ptpv1.MatchRule{matchRule}}
	policy := ptpv1.PtpConfig{ObjectMeta: metav1.ObjectMeta{Name: profileName, Namespace: PtpLinuxDaemonNamespace},
		Spec: ptpv1.PtpConfigSpec{Profile: []ptpv1.PtpProfile{ptpProfile}, Recommend: []ptpv1.PtpRecommend{ptpRecommend}}}

	_, err := client.Client.PtpConfigs(PtpLinuxDaemonNamespace).Create(context.Background(), &policy, metav1.CreateOptions{})
	return err
}

func enablePTPEvent() error {
	ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(PtpLinuxDaemonNamespace).Get(context.Background(), "default", metav1.GetOptions{})
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
	ptpConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(PtpLinuxDaemonNamespace).Get(context.Background(), "default", metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	if ptpConfig.Spec.EventConfig == nil {
		return false
	}
	return ptpConfig.Spec.EventConfig.EnableEventPublisher
}

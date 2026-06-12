package ptphelper

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	. "github.com/onsi/gomega"
	"github.com/openshift/library-go/pkg/config/clusterstatus"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	v1core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/nodes"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/pods"

	configv1 "github.com/openshift/api/config/v1"
	l2exports "github.com/redhat-cne/l2discovery-lib/exports"
	ctrl "sigs.k8s.io/controller-runtime"
)

func GetPodWithLabel(label *string, nodeName *string) ([]*corev1.Pod, error) {
	res := make([]*corev1.Pod, 0)
	ptpPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	if err != nil {
		return res, err
	}
	for i := range ptpPods.Items {
		pod := &ptpPods.Items[i]
		isPodFound, err := pods.HasPodLabelOrNodeName(pod, label, nodeName)
		if err != nil {
			return res, fmt.Errorf("could not check pod role (label=%q, node=%q): %w", pkg.PtrStringOrDefault(label, "<nil>"), pkg.PtrStringOrDefault(nodeName, "<nil>"), err)
		}
		if !isPodFound {
			continue
		}
		res = append(res, pod)
	}
	return res, nil
}

func findMatchingPod(label *string, nodeName *string) (*corev1.Pod, error) {
	var lastErr error
	deadline := time.Now().Add(pkg.TimeoutIn3Minutes)
	for attempt := 1; ; attempt++ {
		foundPods, err := GetPodWithLabel(label, nodeName)
		if err != nil {
			lastErr = err
		} else if len(foundPods) == 0 {
			lastErr = fmt.Errorf("no PTP pods found for label=%q node=%q", pkg.PtrStringOrDefault(label, "<nil>"), pkg.PtrStringOrDefault(nodeName, "<nil>"))
		} else {
			return foundPods[0], nil
		}

		if time.Now().After(deadline) {
			return nil, lastErr
		}
		logrus.Infof("findMatchingPod attempt %d failed: %v, retrying...", attempt, lastErr)
		time.Sleep(pkg.Timeout10Seconds)
	}
}

func GetProfileLogID(ptpConfigName string, label *string, nodeName *string) (string, error) {
	const logIDRegex = `(?m).*?Ptp4lConf: #profile: %s(.|\n)*?message_tag \[(.*)\]`
	const logIDIndex = 2

	renderedRegex := fmt.Sprintf(logIDRegex, ptpConfigName)
	var lastErr error
	deadline := time.Now().Add(pkg.TimeoutIn3Minutes)
	for attempt := 1; ; attempt++ {
		pod, err := findMatchingPod(label, nodeName)
		if err != nil {
			lastErr = fmt.Errorf("finding pod for %s: %w", ptpConfigName, err)
		} else {
			matches, err := pods.GetPodLogsRegex(pod.Namespace,
				pod.Name, pkg.PtpContainerName,
				renderedRegex, false, pkg.TimeoutIn1Minute)
			if err != nil {
				lastErr = fmt.Errorf("could not get any profile line, err=%s", err)
			} else if len(matches) == 0 || len(matches[len(matches)-1]) <= logIDIndex {
				lastErr = fmt.Errorf("profile log id not found for %s in pod %s/%s", ptpConfigName, pod.Namespace, pod.Name)
			} else {
				id := matches[len(matches)-1][logIDIndex]
				if id == "" {
					lastErr = fmt.Errorf("empty profile log id for %s in pod %s/%s", ptpConfigName, pod.Namespace, pod.Name)
				} else {
					return id, nil
				}
			}
		}

		if time.Now().After(deadline) {
			return "", lastErr
		}
		logrus.Infof("GetProfileLogID attempt %d failed for %s: %v, retrying...", attempt, ptpConfigName, lastErr)
		time.Sleep(pkg.Timeout10Seconds)
	}
}

// configFileFromLogID derives the ptp4l config file path from a profile log ID.
// The log ID may contain a level suffix (e.g. "ptp4l.0.config:{level}"); only
// the part before the colon is used as the filename.
func configFileFromLogID(logID string) string {
	if idx := strings.Index(logID, ":"); idx != -1 {
		logID = logID[:idx]
	}
	return "/var/run/" + logID
}

// getClockIDViaPMC runs "pmc GET PARENT_DATA_SET" against the given ptp4l config
// file inside the linuxptp-daemon pod and returns the value of the requested
// field (e.g. "grandmasterIdentity" or "parentPortIdentity.clockIdentity").
func getClockIDViaPMC(pod *corev1.Pod, configFile, field string) (string, error) {
	re := regexp.MustCompile(`(?m)` + regexp.QuoteMeta(field) + `\s+(\S+)`)
	buf, _, err := pods.ExecCommand(client.Client, true, pod,
		pkg.PtpContainerName, []string{"pmc", "-b", "0", "-u", "-f", configFile, "GET", "PARENT_DATA_SET"})
	if err != nil {
		return "", fmt.Errorf("pmc GET PARENT_DATA_SET on %s: %v", configFile, err)
	}
	matches := re.FindStringSubmatch(buf.String())
	if len(matches) < 2 {
		return "", fmt.Errorf("%s not found in pmc output for %s: %s", field, configFile, buf.String())
	}
	return matches[1], nil
}

func GetClockIDMaster(ptpConfigName string, label *string, nodeName *string, isGM bool) (string, error) {
	const clockIDGMRegex = `(?m)\[%s\] selected local clock (.*) as best master`
	const clockIDBCRegex = `(?m)\[%s\] selected best master clock (.*)`
	const clockIDIndex = 1
	clockIDRegex := clockIDBCRegex
	if isGM {
		clockIDRegex = clockIDGMRegex
	}
	logID, err := GetProfileLogID(ptpConfigName, label, nodeName)
	if err != nil {
		return "", err
	}
	configFile := configFileFromLogID(logID)
	if strings.Contains(logID, "level") {
		logID = strings.Replace(logID, "{level}", "\\d+", 1)
	}
	pod, err := findMatchingPod(label, nodeName)
	if err != nil {
		return "", err
	}
	renderedRegex := fmt.Sprintf(clockIDRegex, logID)
	matches, err := pods.GetPodLogsRegex(pod.Namespace,
		pod.Name, pkg.PtpContainerName,
		renderedRegex, false, pkg.TimeoutIn1Minute)
	if err == nil && len(matches) > 0 && len(matches[len(matches)-1]) > clockIDIndex {
		return matches[len(matches)-1][clockIDIndex], nil
	}
	logrus.Infof("GetClockIDMaster: log parsing failed for %s (isGM=%v), falling back to pmc: %v", ptpConfigName, isGM, err)
	return getClockIDViaPMC(pod, configFile, "grandmasterIdentity")
}

func GetClockIDForeign(ptpConfigName string, label *string, nodeName *string) (string, error) {
	const clockIDForeignRegex = `(?m)\[%s\].* selected best master clock (.*)`
	const clockIDForeignIndex = 1
	logID, err := GetProfileLogID(ptpConfigName, label, nodeName)
	if err != nil {
		return "", err
	}
	configFile := configFileFromLogID(logID)
	if strings.Contains(logID, "level") {
		logID = strings.Replace(logID, "{level}", "\\d+", 1)
	}
	pod, err := findMatchingPod(label, nodeName)
	if err != nil {
		return "", err
	}
	renderedRegex := fmt.Sprintf(clockIDForeignRegex, logID)
	matches, err := pods.GetPodLogsRegex(pod.Namespace,
		pod.Name, pkg.PtpContainerName,
		renderedRegex, false, pkg.TimeoutIn1Minute)
	if err == nil && len(matches) > 0 && len(matches[len(matches)-1]) > clockIDForeignIndex {
		return matches[len(matches)-1][clockIDForeignIndex], nil
	}
	logrus.Infof("GetClockIDForeign: log parsing failed for %s, falling back to pmc: %v", ptpConfigName, err)
	return getClockIDViaPMC(pod, configFile, "parentPortIdentity.clockIdentity")
}

// WaitForClockIDForeign searches the slave's log stream for a specific expected
// GM clock ID. Unlike GetClockIDForeign (which returns whatever master is in
// the logs), this waits for the expected master to appear — handling the case
// where the GM restarted and the slave hasn't re-synced yet.
func WaitForClockIDForeign(ptpConfigName string, label *string, nodeName *string, expectedGMID string) error {
	logID, err := GetProfileLogID(ptpConfigName, label, nodeName)
	if err != nil {
		return fmt.Errorf("could not get profile log ID: %w", err)
	}
	if strings.Contains(logID, "level") {
		logID = strings.Replace(logID, "{level}", "\\d+", 1)
	}
	pod, err := findMatchingPod(label, nodeName)
	if err != nil {
		return fmt.Errorf("no matching pod found for profile %s: %w", ptpConfigName, err)
	}
	expectedMasterRegex := fmt.Sprintf(`(?m)\[%s\].* selected best master clock %s`, logID, expectedGMID)
	_, err = pods.GetPodLogsRegex(pod.Namespace, pod.Name, pkg.PtpContainerName,
		expectedMasterRegex, false, pkg.TimeoutIn10Minutes)
	if err != nil {
		return fmt.Errorf("expected master %s not found in logs: %w", expectedGMID, err)
	}
	logrus.Infof("slave's Master=%s (matched expected GM)", expectedGMID)
	return nil
}

// returns true if the pod is running a grandmaster
func IsGrandMasterPod(aPod *v1core.Pod) (result bool, err error) {
	result, err = pods.PodRole(aPod, pkg.PtpGrandmasterNodeLabel)
	if err != nil {
		return false, fmt.Errorf("could not check Grandmaster pod role, err: %s", err)
	}
	return result, nil
}

// returns true if the pod is running the clock under test
func IsClockUnderTestPod(aPod *v1core.Pod) (result bool, err error) {
	result, err = pods.PodRole(aPod, pkg.PtpClockUnderTestNodeLabel)
	if err != nil {
		return false, fmt.Errorf("could not check Clock under test pod role, err: %s", err)
	}
	return result, nil
}

// Returns the slave node label to be used in the test, empty string label cound not be found
func GetPTPConfigs(namespace string) ([]ptpv1.PtpConfig, []ptpv1.PtpConfig) {
	var masters []ptpv1.PtpConfig
	var slaves []ptpv1.PtpConfig

	configList, err := client.Client.PtpConfigs(namespace).List(context.Background(), metav1.ListOptions{})
	Expect(err).ToNot(HaveOccurred())
	for _, config := range configList.Items {
		for _, profile := range config.Spec.Profile {
			if IsPtpSlave(profile.Ptp4lOpts, profile.Phc2sysOpts) {
				slaves = append(slaves, config)
			}
		}
	}
	return masters, slaves
}
func GetPtpPodOnNode(nodeName string) (v1core.Pod, error) {
	WaitForPtpDaemonToExist()
	runningPod, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	Expect(err).NotTo(HaveOccurred(), "Error to get list of pods by label: app=linuxptp-daemon")
	Expect(len(runningPod.Items)).To(BeNumerically(">", 0), "PTP pods are  not deployed on cluster")
	for podIndex := range runningPod.Items {
		if runningPod.Items[podIndex].Spec.NodeName == nodeName {
			return runningPod.Items[podIndex], nil
		}
	}
	return v1core.Pod{}, errors.New("pod not found")
}

func GetMasterSlaveAttachedInterfaces(pod *v1core.Pod) []string {
	var IntList []string
	Eventually(func() error {
		stdout, _, err := pods.ExecCommand(client.Client, true, pod, pkg.PtpContainerName, []string{"ls", "/sys/class/net/"})
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
	}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(BeNil())

	return IntList
}

func GetPtpMasterSlaveAttachedInterfaces(pod *v1core.Pod) []string {
	var ptpSupportedInterfaces []string
	var stdout bytes.Buffer

	intList := GetMasterSlaveAttachedInterfaces(pod)
	for _, interf := range intList {
		skipInterface := false
		PCIAddr := ""
		var err error

		// Get readlink status
		Eventually(func() error {
			stdout, _, err = pods.ExecCommand(client.Client, true, pod, pkg.PtpContainerName, []string{"readlink", "-f", fmt.Sprintf("/sys/class/net/%s", interf)})
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
		}, pkg.TimeoutIn3Minutes, 5*time.Second).Should(BeNil())

		if skipInterface || PCIAddr == "" {
			continue
		}

		// Check if this is a virtual function
		Eventually(func() error {
			// If the physfn doesn't exist this means the interface is not a virtual function so we ca add it to the list
			stdout, _, err = pods.ExecCommand(client.Client, true, pod, pkg.PtpContainerName, []string{"ls", fmt.Sprintf("/sys/bus/pci/devices/%s/physfn", PCIAddr)})
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
			stdout, _, err = pods.ExecCommand(client.Client, true, pod, pkg.PtpContainerName, []string{"ethtool", "-T", interf})
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

		if IsPTPEnabled(&stdout) {
			ptpSupportedInterfaces = append(ptpSupportedInterfaces, interf)
			logrus.Debugf("Append ptp interface=%s from node=%s", interf, pod.Spec.NodeName)
		}
	}
	return ptpSupportedInterfaces
}

// This function parses ethtool command output and detect interfaces which supports ptp protocol
func IsPTPEnabled(ethToolOutput *bytes.Buffer) bool {
	var RxEnabled bool
	var TxEnabled bool
	var RawEnabled bool

	scanner := bufio.NewScanner(ethToolOutput)
	for scanner.Scan() {
		line := strings.TrimPrefix(scanner.Text(), "\t")
		parts := strings.Fields(line)
		if parts[0] == pkg.ETHTOOL_HARDWARE_RECEIVE_CAP {
			RxEnabled = true
		}
		if parts[0] == pkg.ETHTOOL_HARDWARE_TRANSMIT_CAP {
			TxEnabled = true
		}
		if parts[0] == pkg.ETHTOOL_HARDWARE_RAW_CLOCK_CAP {
			RawEnabled = true
		}
	}
	return RxEnabled && TxEnabled && RawEnabled
}

func PtpDiscoveredInterfaceList(nodeName string) []string {
	var ptpInterfaces []string
	var nodePtpDevice ptpv1.NodePtpDevice
	fg, err := client.Client.PtpV1Interface.NodePtpDevices(nodePtpDevice.Namespace).List(context.Background(), metav1.ListOptions{})
	Expect(err).ToNot(HaveOccurred())
	for _, aNodePtpDevice := range fg.Items {
		if aNodePtpDevice.Name == nodeName {
			for _, aIfName := range aNodePtpDevice.Status.Devices {
				ptpInterfaces = append(ptpInterfaces, aIfName.Name)
			}
		}
	}
	return ptpInterfaces
}

// WaitForConfigMapProfile polls the ptp-configmap until the given node's entry
// contains the expected profile name (qualified or unqualified substring).
// This ensures the operator has reconciled and the daemon will see the new
// config when it starts or detects the change.
func WaitForConfigMapProfile(nodeName, profileSubstring string, timeout time.Duration) error {
	var lastErr error
	Eventually(func() error {
		cm, err := client.Client.CoreV1().ConfigMaps(pkg.PtpLinuxDaemonNamespace).Get(
			context.Background(), "ptp-configmap", metav1.GetOptions{})
		if err != nil {
			lastErr = fmt.Errorf("could not get configmap: %v", err)
			return lastErr
		}
		data, ok := cm.Data[nodeName]
		if !ok {
			lastErr = fmt.Errorf("node %s not found in configmap", nodeName)
			return lastErr
		}
		if !strings.Contains(data, profileSubstring) {
			lastErr = fmt.Errorf("profile %q not found in configmap for node %s", profileSubstring, nodeName)
			return lastErr
		}
		return nil
	}, timeout, 2*time.Second).Should(BeNil(),
		fmt.Sprintf("timed out waiting for profile %q in configmap for node %s: %v",
			profileSubstring, nodeName, lastErr))
	return nil
}

func MutateProfile(profile *ptpv1.PtpConfig, profileName, nodeName string) *ptpv1.PtpConfig {
	mutatedConfig := profile.DeepCopy()
	priority := int64(0)
	mutatedConfig.ObjectMeta.Reset()
	mutatedConfig.ObjectMeta.Name = pkg.PtpTempPolicyName
	mutatedConfig.ObjectMeta.Namespace = pkg.PtpLinuxDaemonNamespace
	mutatedConfig.Spec.Profile[0].Name = &profileName
	mutatedConfig.Spec.Recommend[0].Priority = &priority
	mutatedConfig.Spec.Recommend[0].Match[0].NodeLabel = nil
	mutatedConfig.Spec.Recommend[0].Match[0].NodeName = &nodeName
	mutatedConfig.Spec.Recommend[0].Profile = &profileName
	return mutatedConfig
}

func ReplaceTestPod(pod *v1core.Pod, timeout time.Duration) (v1core.Pod, error) {
	var newPod v1core.Pod

	err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{
		GracePeriodSeconds: pointer.Int64Ptr(0)})
	if err != nil && !apierrors.IsNotFound(err) {
		return newPod, fmt.Errorf("failed to delete pod %s: %w", pod.Name, err)
	}
	if apierrors.IsNotFound(err) {
		logrus.Infof("Pod %s already gone (likely replaced by DaemonSet rolling update), waiting for new pod", pod.Name)
	}

	Eventually(func() error {
		newPod, err = GetPtpPodOnNode(pod.Spec.NodeName)

		if err == nil && newPod.Name != pod.Name && newPod.Status.Phase == "Running" {
			return nil
		}

		return errors.New("cannot replace PTP pod")
	}, timeout, 1*time.Second).Should(BeNil())

	return newPod, nil
}

func RestartPTPDaemon() {
	err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).DeleteCollection(
		context.Background(),
		metav1.DeleteOptions{GracePeriodSeconds: pointer.Int64Ptr(0)},
		metav1.ListOptions{LabelSelector: pkg.PtpLinuxDaemonPodsLabel},
	)
	Expect(err).ToNot(HaveOccurred())

	WaitForPtpDaemonToExist()
}

func CheckLeaseDuration(namespace string, leaseDurationDefault int32, leaseDurationSNO int32) int {
	expectedLeaseDurationSeconds := leaseDurationDefault
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	if infra, err := clusterstatus.GetClusterInfraStatus(ctx, ctrl.GetConfigOrDie()); err == nil && infra != nil {
		if infra.ControlPlaneTopology == configv1.SingleReplicaTopologyMode {
			expectedLeaseDurationSeconds = leaseDurationSNO
		}
	}

	le, err := client.Client.CoordinationV1().Leases(namespace).Get(context.Background(), "ptp.openshift.io", metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred())
	Expect(*le.Spec.LeaseDurationSeconds).To(Equal(expectedLeaseDurationSeconds))
	return 0
}

func WaitForPtpDaemonToExist() int {
	var expectedNumber int32
	Eventually(func() error {
		daemonset, err := client.Client.DaemonSets(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpDaemonsetName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("linuxptp-daemon DaemonSet not created yet: %w", err)
			}
			return err
		}
		expectedNumber = daemonset.Status.DesiredNumberScheduled
		return nil
	}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(Succeed(), "linuxptp-daemon DaemonSet must exist before readiness wait")

	Eventually(func() (int32, error) {
		daemonset, err := client.Client.DaemonSets(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpDaemonsetName, metav1.GetOptions{})
		if err != nil {
			return 0, err
		}
		return daemonset.Status.NumberReady, nil
	}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(Equal(expectedNumber),
		daemonsetNotReadyMessage(expectedNumber))

	Eventually(func() (int, error) {
		ptpPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
		if err != nil {
			return 0, err
		}
		return len(ptpPods.Items), nil
	}, pkg.TimeoutIn5Minutes, 2*time.Second).Should(Equal(int(expectedNumber)),
		fmt.Sprintf("expected %d linuxptp-daemon pods, check DaemonSet status", expectedNumber))

	return 0
}

func daemonsetNotReadyMessage(expectedNumber int32) string {
	pods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(
		context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	if err != nil {
		return fmt.Sprintf("linuxptp-daemon NumberReady != %d (failed to list pods: %v)", expectedNumber, err)
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("linuxptp-daemon DaemonSet: expected %d ready pods, found %d pods total", expectedNumber, len(pods.Items)))
	for i := range pods.Items {
		pod := &pods.Items[i]
		phase := string(pod.Status.Phase)
		var containerIssues []string
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Ready {
				continue
			}
			issue := fmt.Sprintf("%s: ready=false", cs.Name)
			if cs.State.Waiting != nil {
				issue += fmt.Sprintf(" (%s: %s)", cs.State.Waiting.Reason, cs.State.Waiting.Message)
			}
			if cs.State.Terminated != nil {
				issue += fmt.Sprintf(" (terminated: %s exit=%d)", cs.State.Terminated.Reason, cs.State.Terminated.ExitCode)
			}
			containerIssues = append(containerIssues, issue)
		}
		detail := fmt.Sprintf("  pod %s on %s: phase=%s", pod.Name, pod.Spec.NodeName, phase)
		if len(containerIssues) > 0 {
			detail += " | " + strings.Join(containerIssues, "; ")
		}
		lines = append(lines, detail)
	}
	return strings.Join(lines, "\n")
}

func WaitForPtpDaemonToBeReady(podList []*v1core.Pod) int {
	Eventually(func() error {
		ptpVersion, err := GetPtpOperatorVersionFromDeployment()
		if err != nil {
			logrus.Infof("Unable to get PTP operator version, skipping readiness check: %v", err)
			return nil
		}

		ptpVer, err := semver.NewVersion(ptpVersion)
		if err != nil {
			logrus.Infof("Unable to parse PTP operator version %s, skipping readiness check: %v", ptpVersion, err)
			return nil
		}

		ptp419, _ := semver.NewVersion("4.19")
		if ptpVer.LessThan(ptp419) {
			return nil
		}

		for _, pod := range podList {
			err = CheckReadiness(pod)
			if err != nil {
				return fmt.Errorf("Not Ready: %v", err)
			}
		}
		return nil
	}, 2*time.Minute, 2*time.Second).Should(Not(HaveOccurred()))

	return 0
}

// Returns the slave node label to be used in the test
func DiscoveryPTPConfiguration(namespace string) (masters, slaves []*ptpv1.PtpConfig) {
	configList, err := client.Client.PtpConfigs(namespace).List(context.Background(), metav1.ListOptions{})
	Expect(err).ToNot(HaveOccurred())
	for configIndex := range configList.Items {
		for _, profile := range configList.Items[configIndex].Spec.Profile {
			if IsPtpMaster(profile.Ptp4lOpts, profile.Phc2sysOpts) {
				masters = append(masters, &configList.Items[configIndex])
			}
			if IsPtpSlave(profile.Ptp4lOpts, profile.Phc2sysOpts) {
				slaves = append(slaves, &configList.Items[configIndex])
			} else {
				slaves = append(slaves, &configList.Items[configIndex])

			}
		}
	}

	return masters, slaves
}

// EnablePTPEvent: if configMapName is passed, clean up the configMap when version changed
func EnablePTPEvent(apiVersion, configMapName string) error {
	var ptpConfig *ptpv1.PtpOperatorConfig
	Eventually(func() error {
		var e error
		ptpConfig, e = client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(
			context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
		return e
	}, pkg.TimeoutIn5Minutes, 3*time.Second).Should(Succeed(), "PtpOperatorConfig must exist to enable events")

	var currentApiVersion string
	if ptpConfig.Spec.EventConfig == nil {
		ptpConfig.Spec.EventConfig = &ptpv1.PtpEventConfig{
			EnableEventPublisher: true,
		}
	} else {
		currentApiVersion = ptpConfig.Spec.EventConfig.ApiVersion
	}

	ptpConfig.Spec.EventConfig.EnableEventPublisher = true
	ptpConfig.Spec.EventConfig.ApiVersion = apiVersion

	// clean up configMap for subscription if update to a different version
	var err error
	if currentApiVersion != "" && currentApiVersion != apiVersion && configMapName != "" {
		// Check if the ConfigMap exists
		configMap, getErr := client.Client.CoreV1().ConfigMaps(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), configMapName, metav1.GetOptions{})
		if getErr != nil {
			logrus.Infof("ConfigMap %s does not exist: %v", configMapName, getErr)
		} else {
			// Empty the ConfigMap
			configMap.Data = map[string]string{}
			configMap.BinaryData = map[string][]byte{}

			// Update the ConfigMap
			_, err = client.Client.CoreV1().ConfigMaps(pkg.PtpLinuxDaemonNamespace).Update(context.Background(), configMap, metav1.UpdateOptions{})
			if err != nil {
				logrus.Errorf("Error updating ConfigMap: %v", err)
			}

			logrus.Infof("ConfigMap %s emptied successfully\n", configMapName)
		}
	}
	_, err = client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Update(context.Background(), ptpConfig, metav1.UpdateOptions{})
	return err
}

// PtpEventEnabled returns 0 if event is not enabled, 1 for v1 API, 2 for v2 O-RAN Compliant API
func PtpEventEnabled() int {
	ptpVersion, err := GetPtpOperatorVersionFromDeployment()
	eventsVersionDefault := 0

	if err == nil {
		ptpVer, err := semver.NewVersion(ptpVersion)
		if err == nil {
			ptp418, _ := semver.NewVersion("4.18")
			if ptpVer.LessThan(ptp418) {
				eventsVersionDefault = 1
			} else {
				eventsVersionDefault = 2
			}
		}
	}

	var ptpConfig *ptpv1.PtpOperatorConfig
	Eventually(func() error {
		var e error
		ptpConfig, e = client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(
			context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
		return e
	}, pkg.TimeoutIn5Minutes, 3*time.Second).Should(Succeed(), "PtpOperatorConfig must exist for PtpEventEnabled check")
	if ptpConfig.Spec.EventConfig == nil {
		return 0
	}
	if !ptpConfig.Spec.EventConfig.EnableEventPublisher {
		return 0
	}

	if ptpConfig.Spec.EventConfig.ApiVersion == "" {
		return eventsVersionDefault
	}

	if IsV1Api(ptpConfig.Spec.EventConfig.ApiVersion) {
		return 1
	}
	return 2
}

func EnablePTPReferencePlugin() error {
	ptpOperatorConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	var plugindata apiextensions.JSON
	plugindata.Raw = []byte("1")
	if ptpOperatorConfig.Spec.EnabledPlugins != nil {
		(*ptpOperatorConfig.Spec.EnabledPlugins)["reference"] = &plugindata
	}

	_, err = client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Update(context.Background(), ptpOperatorConfig, metav1.UpdateOptions{})
	return err
}

func DisablePTPReferencePlugin() error {
	ptpOperatorConfig, err := client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Get(context.Background(), pkg.PtpConfigOperatorName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	(*ptpOperatorConfig.Spec.EnabledPlugins)["reference"] = nil

	_, err = client.Client.PtpV1Interface.PtpOperatorConfigs(pkg.PtpLinuxDaemonNamespace).Update(context.Background(), ptpOperatorConfig, metav1.UpdateOptions{})
	return err
}

func GetPtpOperatorVersion() (string, error) {
	const releaseVersionStr = "RELEASE_VERSION"

	var ptpOperatorVersion string

	deploy, err := client.Client.AppsV1Interface.Deployments(pkg.PtpLinuxDaemonNamespace).Get(context.TODO(), pkg.PtpOperatorDeploymentName, metav1.GetOptions{})

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

func GetPtpOperatorVersionFromDeployment() (string, error) {
	const releaseVersionStr = "RELEASE_VERSION"

	// Return cached if available to avoid repeated logs and API calls
	if cachedReleaseVersion != "" {
		return cachedReleaseVersion, nil
	}

	deploy, err := client.Client.AppsV1Interface.Deployments(pkg.PtpLinuxDaemonNamespace).Get(context.TODO(), pkg.PtpOperatorDeploymentName, metav1.GetOptions{})
	if err != nil {
		logrus.Infof("PTP Operator deployment not found: %v", err)
		return "", err
	}

	// Get the RELEASE_VERSION from the first container's environment variables
	if len(deploy.Spec.Template.Spec.Containers) == 0 {
		return "", fmt.Errorf("no containers found in PTP operator deployment")
	}

	envs := deploy.Spec.Template.Spec.Containers[0].Env
	for _, env := range envs {
		if env.Name == releaseVersionStr {
			ptpOperatorVersion := strings.TrimPrefix(env.Value, "v")
			cachedReleaseVersion = ptpOperatorVersion
			if !releaseVersionLogged {
				logrus.Infof("PTP operator version from RELEASE_VERSION: %s", ptpOperatorVersion)
				releaseVersionLogged = true
			}
			return cachedReleaseVersion, nil
		}
	}

	return "", fmt.Errorf("RELEASE_VERSION environment variable not found in PTP operator deployment")
}

// Checks for DualNIC BC
func IsSecondaryBc(config *ptpv1.PtpConfig) bool {
	for _, profile := range config.Spec.Profile {
		if profile.Phc2sysOpts != nil {
			return false
		}
	}
	return true
}

// Checks if the ptpSettings has more than one HA profile
func hasHaProfiles(ptpSettings map[string]string) bool {
	logrus.Infof("Checking if ptpSettings %v has more than one HA profile", ptpSettings)
	return ptpSettings != nil && ptpSettings["haProfiles"] != "" && len(strings.Split(ptpSettings["haProfiles"], ",")) > 1
}

// Checks for DualNIC BC HA
func ConfigIsPhc2SysHa(config *ptpv1.PtpConfig) bool {
	logrus.Infof("Checking if config %s is Phc2Sys HA", config.Name)
	for _, profile := range config.Spec.Profile {
		if profile.Phc2sysOpts != nil && profile.Ptp4lOpts != nil && *profile.Ptp4lOpts == "" && hasHaProfiles(profile.PtpSettings) {
			logrus.Infof("Config %s is Phc2Sys HA", config.Name)
			return true
		}
	}
	return false
}

// Checks for OC
func IsPtpSlave(ptp4lOpts, phc2sysOpts *string) bool {
	return /*strings.Contains(*ptp4lOpts, "-s") &&*/ ((phc2sysOpts != nil && (strings.Count(*phc2sysOpts, "-a") == 1 && strings.Count(*phc2sysOpts, "-r") == 1)) ||
		phc2sysOpts == nil)
}

// Checks for Grand master
func IsPtpMaster(ptp4lOpts, phc2sysOpts *string) bool {
	return ptp4lOpts != nil && phc2sysOpts != nil && !strings.Contains(*ptp4lOpts, "-s ") && strings.Count(*phc2sysOpts, "-a") == 1 && strings.Count(*phc2sysOpts, "-r") == 2
}

// Checks for DualNIC BC
func GetProfileName(config *ptpv1.PtpConfig) (string, error) {
	if config == nil {
		return "", fmt.Errorf("ptp config is nil")
	}
	for _, profile := range config.Spec.Profile {
		if profile.Name == nil {
			continue
		}
		switch *profile.Name {
		case pkg.PtpGrandMasterPolicyName,
			pkg.PtpBcMaster1PolicyName,
			pkg.PtpBcMaster2PolicyName,
			pkg.PtpSlave1PolicyName,
			pkg.PtpSlave2PolicyName,
			pkg.PtpTempPolicyName:
			return *profile.Name, nil
		}
	}
	return "", fmt.Errorf("cannot find valid test profile name")
}
func RetrievePTPProfileLabels(configs []ptpv1.PtpConfig) string {
	for _, config := range configs {
		for _, recommend := range config.Spec.Recommend {
			for _, match := range recommend.Match {
				label := *match.NodeLabel
				nodeCount, err := nodes.LabeledNodesCount(label)
				Expect(err).ToNot(HaveOccurred())
				if nodeCount > 0 {
					return label
				}
			}
		}
	}
	return ""
}

func GetPTPPodWithPTPConfig(ptpConfig *ptpv1.PtpConfig) (aPtpPod *v1core.Pod, err error) {
	ptpPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	if err != nil {
		return aPtpPod, err
	}

	label, err := GetLabel(ptpConfig)
	if err != nil {
		logrus.Debugf("GetLabel %s", err)
	}

	nodeName, err := GetFirstNode(ptpConfig)
	if err != nil {
		logrus.Debugf("GetFirstNode %s", err)
	}

	for _, pod := range ptpPods.Items {

		isPodFound, err := pods.HasPodLabelOrNodeName(&pod, label, nodeName)
		if err != nil {
			logrus.Errorf("could not check %q pod role, err: %s", pkg.PtrStringOrDefault(label, "<nil>"), err)
		}

		if isPodFound {
			aPtpPod = &pod
			break
		}
	}
	if aPtpPod == nil {
		return nil, fmt.Errorf("no linuxptp-daemon pod found matching ptpconfig %s (label=%q, nodeName=%q)",
			ptpConfig.Name, pkg.PtrStringOrDefault(label, "<nil>"), pkg.PtrStringOrDefault(nodeName, "<nil>"))
	}
	return aPtpPod, nil
}

// Gets the first label configured in the ptpconfig->spec->recommend
func GetLabel(ptpConfig *ptpv1.PtpConfig) (*string, error) {
	for _, r := range ptpConfig.Spec.Recommend {
		for _, m := range r.Match {
			if m.NodeLabel == nil {
				continue
			}
			aLabel := ""
			switch *m.NodeLabel {
			case pkg.PtpClockUnderTestNodeLabel:
				aLabel = pkg.PtpClockUnderTestNodeLabel
			case pkg.PtpGrandmasterNodeLabel:
				aLabel = pkg.PtpGrandmasterNodeLabel
			case pkg.PtpSlave1NodeLabel:
				aLabel = pkg.PtpSlave1NodeLabel
			case pkg.PtpSlave2NodeLabel:
				aLabel = pkg.PtpSlave2NodeLabel
			}
			return &aLabel, nil
		}
	}
	return nil, fmt.Errorf("label not found")
}

// gets the first nodename configured in the ptpconfig->spec->recommend
func GetFirstNode(ptpConfig *ptpv1.PtpConfig) (*string, error) {
	for _, r := range ptpConfig.Spec.Recommend {
		for _, m := range r.Match {
			if m.NodeName == nil {
				continue
			}
			return m.NodeName, nil
		}
	}
	return nil, fmt.Errorf("nodeName not found")
}

func GetPtpInterfacePerNode(nodeName string, ifList map[string]*l2exports.PtpIf) (out []string) {
	for _, aIf := range ifList {
		if aIf.NodeName == nodeName {
			out = append(out, aIf.IfName)
		}
	}
	return out
}

var mu sync.RWMutex

// cache for operator version logging
var cachedReleaseVersion string
var releaseVersionLogged bool

// saves events to file
func SaveStoreEventsToFile(allEvents, filename string) {
	mu.Lock()
	err := os.WriteFile(filename, []byte(allEvents), 0644)
	if err != nil {
		logrus.Errorf("could not write events to file, err: %s", err)
	}
	mu.Unlock()
}

func IsExternalGM() (out bool) {
	value, isSet := os.LookupEnv("EXTERNAL_GM")
	value = strings.ToLower(value)
	out = isSet && !strings.Contains(value, "false")
	logrus.Infof("EXTERNAL_GM=%t", out)
	return out
}

func execPodCommand(nodeName string, cmd []string) (stdoutBuf, stderrBuf bytes.Buffer, err error) {

	WaitForPtpDaemonToExist()
	pod, err := GetPtpPodOnNode(nodeName)
	so, se := bytes.Buffer{}, bytes.Buffer{}
	if err != nil {
		logrus.Errorf("Could not get ptp pod from node due to err: %s, nodeName: %s", err, nodeName)
		return so, se, err
	}
	so, se, err = pods.ExecCommand(client.Client, true, &pod, pkg.PtpContainerName, cmd)
	if err != nil {
		logrus.Errorf("Could not run command %s on pod because of err: %s \n stderr: %s", cmd, err, se.String())
	}
	return so, se, err
}

// GetMajorVersion returns major version
func GetMajorVersion(version string) (int, error) {
	if version == "" {
		return 1, nil
	}
	version = strings.TrimPrefix(version, "v")
	version = strings.TrimPrefix(version, "V")
	v := strings.Split(version, ".")
	majorVersion, err := strconv.Atoi(v[0])
	if err != nil {
		logrus.Errorf("Error parsing major version from %s, %v", version, err)
		return 1, err
	}
	return majorVersion, nil
}

// IsV1Api ...
func IsV1Api(version string) bool {
	if majorVersion, err := GetMajorVersion(version); err == nil {
		if majorVersion >= 2 {
			return false
		}
	}
	// by default use V1
	return true
}

func CheckReadiness(pod *corev1.Pod) (err error) {
	stdout, stderr, err := pods.ExecCommand(
		client.Client,
		false,
		pod,
		pod.Spec.Containers[0].Name,
		[]string{"curl", "-v", "localhost:8081/ready"},
	)
	if err != nil {
		return fmt.Errorf("error getting readiness, err: %v", err)
	}
	if !strings.Contains(stdout.String()+stderr.String(), "HTTP/1.1 200 OK") {
		return fmt.Errorf("pod not ready with, err: %s", stdout.String()+stderr.String())
	}

	return nil
}

func GetOCPVersion() (ocpVersion string, err error) {
	const OpenShiftAPIServer = "openshift-apiserver"

	ocpClient := client.Client.OcpClient
	var clusterOperator *configv1.ClusterOperator
	clusterOperator, err = ocpClient.ClusterOperators().Get(context.TODO(), OpenShiftAPIServer, metav1.GetOptions{})

	if err != nil {
		return ocpVersion, fmt.Errorf("This cluster is not an openshift cluster or the user does not have admin rights, err: %v", err)
	}

	for _, ver := range clusterOperator.Status.Versions {
		if ver.Name == OpenShiftAPIServer {
			ocpVersion = ver.Version
			break
		}
	}
	return ocpVersion, err
}

// IsPTPOperatorVersionAtLeast checks if the PTP Operator version is >= the specified minimum version
// Returns true if version cannot be determined (to allow tests to run)
func IsPTPOperatorVersionAtLeast(minVersion string) bool {
	foundVersion, err := GetPtpOperatorVersionFromDeployment()
	if err != nil {
		logrus.Infof("Could not get PTP Operator version, assuming version check passes: %v", err)
		return true
	}
	logrus.Infof("Found version %s; checking %s <= %s", foundVersion, foundVersion, minVersion)
	ver, err := semver.NewVersion(foundVersion)
	if err != nil {
		logrus.Infof("Could not parse PTP Operator version %s, assuming version check passes: %v", foundVersion, err)
		return true
	}

	minVer, err := semver.NewVersion(minVersion)
	if err != nil {
		logrus.Infof("Could not parse min version %s, assuming version check passes: %v", minVersion, err)
		return true
	}

	return !ver.LessThan(minVer)
}

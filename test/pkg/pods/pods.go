package pods

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	testclient "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/images"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/utils/pointer"
)

// ExecCommand runs command in the pod and returns buffer output
// If mergeOutput is true, stderr will be merged into stdout, otherwise they are separate
func ExecCommand(cs *testclient.ClientSet, mergeOutput bool, pod *corev1.Pod, containerName string, command []string) (stdoutBuf, stderrBuf bytes.Buffer, err error) {
	req := testclient.Client.CoreV1().RESTClient().
		Post().
		Namespace(pod.Namespace).
		Resource("pods").
		Name(pod.Name).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   command,
			Stdin:     false, // Disable stdin for non-interactive commands
			Stdout:    true,
			Stderr:    true,
			TTY:       false, // Always disable TTY
		}, scheme.ParameterCodec)

	// Note: Using SPDY executor (deprecated but still functional)
	// TODO: Upgrade to WebSocket executor when client-go version supports it
	exec, err := remotecommand.NewSPDYExecutor(cs.Config, "POST", req.URL())
	if err != nil {
		return stdoutBuf, stderrBuf, err
	}

	// Always stream to separate buffers first
	err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
		Stdin:  nil, // No stdin for non-interactive commands
		Stdout: &stdoutBuf,
		Stderr: &stderrBuf,
		Tty:    false, // Always disable TTY
	})

	// If mergeOutput is true, append stderr content to stdout buffer
	if mergeOutput {
		stdoutBuf.Write(stderrBuf.Bytes())
	}

	logrus.Tracef("ExecCommand podName=%s containerName=%s command=%v stdout=%s stderr=%s err=%s", pod.Name, containerName, command, stdoutBuf.String(), stderrBuf.String(), err)
	if err != nil {
		return stdoutBuf, stderrBuf, fmt.Errorf("exec.StreamWithContext failure. Stdout: %s, Stderr: %s, Err: %w", stdoutBuf.String(), stderrBuf.String(), err)
	}

	return stdoutBuf, stderrBuf, nil
}

// returns true if the pod passed as paremeter is running on the node selected by the label passed as a parameter.
// the label represent a ptp conformance test role such as: grandmaster, clock under test, slave1, slave2
func PodRole(runningPod *corev1.Pod, label string) (bool, error) {
	nodeList, err := testclient.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
		LabelSelector: label,
	})
	if err != nil {
		return false, fmt.Errorf("error getting node list")
	}
	for NodeNumber := range nodeList.Items {
		if runningPod.Spec.NodeName == nodeList.Items[NodeNumber].Name {
			return true, nil
		}
	}
	return false, nil
}

// returns true if a pod has a given label or node name
func HasPodLabelOrNodeName(pod *corev1.Pod, label *string, nodeName *string) (result bool, err error) {
	if label == nil && nodeName == nil {
		return result, fmt.Errorf("label and nodeName are nil")
	}
	// node name might be present and will be superseded by label
	/*if label != nil && nodeName != nil {
		return result, fmt.Errorf("label or nodeName must be nil")
	}*/
	if label != nil {
		result, err = PodRole(pod, *label)
		if err != nil {
			return result, fmt.Errorf("could not check %s pod role, err: %s", *label, err)
		}
	}
	if nodeName != nil {
		result = pod.Spec.NodeName == *nodeName
	}
	return result, nil
}

// WaitForCondition waits until the pod will have specified condition type with the expected status
func WaitForCondition(cs *testclient.ClientSet, pod *corev1.Pod, conditionType corev1.PodConditionType, conditionStatus corev1.ConditionStatus, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		updatePod, err := cs.Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		for _, c := range updatePod.Status.Conditions {
			if c.Type == conditionType && c.Status == conditionStatus {
				return true, nil
			}
		}
		return false, nil
	})
}

// WaitForPhase waits until the pod will be in specified phase
func WaitForPhase(cs *testclient.ClientSet, pod *corev1.Pod, phaseType corev1.PodPhase, timeout time.Duration) error {
	return wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		updatePod, err := cs.Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		return updatePod.Status.Phase == phaseType, nil
	})
}

func findRegexInStream(stream io.ReadCloser, r *regexp.Regexp, timeout time.Duration) (matches [][]string, err error) {
	logContent := ""
	buf := make([]byte, 2000)

	for start := time.Now(); time.Since(start) <= timeout && len(matches) == 0; {
		numBytes, err := stream.Read(buf)
		if err != nil {
			if err == io.EOF {
				logContent += string(buf[:numBytes])
				matches = r.FindAllStringSubmatch(logContent, -1)
				break
			} else {
				return nil, fmt.Errorf("error reading from stream: %s", err)
			}
		}

		if numBytes == 0 {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		logContent += string(buf[:numBytes])
		matches = r.FindAllStringSubmatch(logContent, -1)
	}

	if len(matches) == 0 {
		return matches, errors.New("timedout waiting for matches")
	}

	return matches, nil
}

// returns last Regex match in the logs for a given pod
func GetPodLogsRegexSince(namespace string, podName string, containerName, regex string, isLiteralText bool, timeout time.Duration, since time.Time) (matches [][]string, err error) {
	const matchOnlyFullLines = `\s*^`
	if isLiteralText {
		regex = regexp.QuoteMeta(regex)
	} else {
		regex += matchOnlyFullLines
	}

	r := regexp.MustCompile(regex)

	podLogOptions := corev1.PodLogOptions{
		Container: containerName,
		Follow:    true,
		SinceTime: &metav1.Time{Time: since},
	}

	podLogRequest := testclient.Client.CoreV1().Pods(namespace).GetLogs(podName, &podLogOptions)

	stream, err := podLogRequest.Stream(context.TODO())
	if err != nil {
		return matches, fmt.Errorf("failed to open log streamn for %s/%s container=%s, err=%s", namespace, podName, containerName, err)
	}
	defer stream.Close()

	matches, err = findRegexInStream(stream, r, timeout)
	if err != nil {
		return matches, fmt.Errorf("could not find regex in log stream for %s/%s container=%s, err=%s", namespace, podName, containerName, err)
	}

	return matches, nil
}

// returns last Regex match in the logs for a given pod
func GetPodLogsRegex(namespace string, podName string, containerName, regex string, isLiteralText bool, timeout time.Duration) (matches [][]string, err error) {
	const matchOnlyFullLines = `\s*^`
	if isLiteralText {
		regex = regexp.QuoteMeta(regex)
	} else {
		regex += matchOnlyFullLines
	}

	r := regexp.MustCompile(regex)

	podLogOptions := corev1.PodLogOptions{
		Container: containerName,
		Follow:    true,
	}

	podLogRequest := testclient.Client.CoreV1().Pods(namespace).GetLogs(podName, &podLogOptions)

	stream, err := podLogRequest.Stream(context.TODO())
	if err != nil {
		return matches, fmt.Errorf("failed to open log streamn for %s/%s container=%s, err=%s", namespace, podName, containerName, err)
	}
	defer stream.Close()

	matches, err = findRegexInStream(stream, r, timeout)
	if err != nil {
		return matches, fmt.Errorf("could not find regex in log stream for %s/%s container=%s, err=%s", namespace, podName, containerName, err)
	}

	return matches, nil
}

func ExecutePtpInterfaceCommand(pod corev1.Pod, interfaceName string, command string) {
	const (
		pollingInterval = 3 * time.Second
	)
	gomega.Eventually(func() error {
		_, _, err := ExecCommand(client.Client, true, &pod, "container-00", []string{"sh", "-c", command})
		return err
	}, pkg.TimeoutIn10Minutes, pollingInterval).Should(gomega.BeNil())
}

func CheckRestart(pod corev1.Pod) {
	logrus.Printf("Restarting the node %s that pod %s is running on", pod.Spec.NodeName, pod.Name)

	const (
		pollingInterval = 3 * time.Second
	)

	gomega.Eventually(func() error {
		_, _, err := ExecCommand(client.Client, true, &pod, "container-00", []string{"chroot", "/host", "shutdown", "-r"})
		return err
	}, pkg.TimeoutIn10Minutes, pollingInterval).Should(gomega.BeNil())
}

func GetRebootDaemonsetPodsAt(node string) *corev1.PodList {

	rebootDaemonsetPodList, err := client.Client.CoreV1().Pods(pkg.RebootDaemonSetNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "name=" + pkg.RebootDaemonSetName, FieldSelector: fmt.Sprintf("spec.nodeName=%s", node)})
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	return rebootDaemonsetPodList
}

func getDefinition(namespace string) *corev1.Pod {
	podObject := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "testpod-",
			Namespace:    namespace},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: pointer.Int64Ptr(0),
			Containers: []corev1.Container{{Name: "test",
				Image:   images.For(images.TestUtils),
				Command: []string{"/bin/bash", "-c", "sleep INF"}}}}}

	return podObject
}

// DefinePodOnNode creates the pod defintion with a node selector
func DefinePodOnNode(namespace string, nodeName string) *corev1.Pod {
	pod := getDefinition(namespace)
	pod.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": nodeName}
	return pod
}

// RedefineAsPrivileged updates the pod definition to be privileged
func RedefineAsPrivileged(pod *corev1.Pod, containerName string) (*corev1.Pod, error) {
	c := containerByName(pod, containerName)
	if c == nil {
		return pod, fmt.Errorf("container with name: %s not found in pod", containerName)
	}
	if c.SecurityContext == nil {
		c.SecurityContext = &corev1.SecurityContext{}
	}
	c.SecurityContext.Privileged = pointer.BoolPtr(true)

	return pod, nil
}

func containerByName(pod *corev1.Pod, containerName string) *corev1.Container {
	if containerName == "" {
		return &pod.Spec.Containers[0]
	}

	for _, c := range pod.Spec.Containers {
		if c.Name == containerName {
			return &c
		}
	}

	return nil
}

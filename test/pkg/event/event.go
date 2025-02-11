package event

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	testclient "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/namespaces"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/pods"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/ptphelper"
	chanpubsub "github.com/redhat-cne/channel-pubsub"
	exports "github.com/redhat-cne/ptp-listener-exports"
	cneevent "github.com/redhat-cne/sdk-go/pkg/event"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

const (
	ConsumerNamespace             = "ptp-event-framework-test"
	ProducerNamespace             = "openshift-ptp"
	ConsumerPodName               = "consumer"
	ConsumerSidecarContainerName  = "sidecar"
	ConsumerSaName                = "consumer-sa"
	ConsumerServiceName           = "consumer-events-subscription-service"
	TestSidecarSuccessLogString   = "rest service returned healthy status"
	ConsumerContainerName         = "consumer"
	CustomerCloudEventProxyPort   = 8089
	ProviderCloudEventProxyPort   = 9085
	sidecarNamespaceDeleteTimeout = time.Minute * 2
	ApiBaseV1                     = "127.0.0.1:9085/api/ocloudNotifications/v1/"
	ApiBaseV2                     = "127.0.0.1:9043/api/ocloudNotifications/v2"
)

var (
	PubSub *chanpubsub.Pubsub
)

func InitPubSub() {
	PubSub = chanpubsub.NewPubsub()
}

// enables event if ptp event is required
func Enable() bool {
	eventMode, _ := strconv.ParseBool(os.Getenv("ENABLE_PTP_EVENT"))
	return eventMode
}

// IsV1RegressionNeeded returns true when we need to test both v1 and v2 event API
func IsV1EventRegressionNeeded() bool {
	value, _ := strconv.ParseBool(os.Getenv("ENABLE_V1_REGRESSION"))
	return value
}

// returns 2.0 if EVENT_API_VERSION is invalid or not set
func GetDefaultApiVersion() string {
	value, isSet := os.LookupEnv("EVENT_API_VERSION")
	if isSet {
		if _, err := strconv.ParseFloat(value, 64); err == nil {
			return value
		} else {
			logrus.Warnf("EVENT_API_VERSION %s is not valid", value)
		}
	}
	return "2.0"
}

func CreateConsumerAppWithSidecar(nodeNameFull string) (err error) {
	nodeName := nodeNameFull
	// using the first component of the node name before the first dot (master2.example.com -> master2)
	if nodeNameFull != "" && strings.Contains(nodeNameFull, ".") {
		nodeName = strings.Split(nodeName, ".")[0]
	}
	testSidecarImageName, err := GetCloudEventProxyImageFromPod()
	if err != nil {
		return fmt.Errorf("could not get test sidecar image, err=%s", err)
	}
	rootUser := pointer.Int64(0)
	aPod := corev1.Pod{
		TypeMeta: v1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      ConsumerPodName,
			Namespace: ConsumerNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "consumer",
			},
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "pubsubstore",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			ServiceAccountName: ConsumerSaName,
			Containers: []corev1.Container{
				{Name: ConsumerSidecarContainerName,
					Image: testSidecarImageName,
					SecurityContext: &corev1.SecurityContext{
						Privileged: pointer.Bool(true),
						RunAsUser:  rootUser,
					},

					Args: []string{"--metrics-addr=127.0.0.1:9091",
						"--store-path=/store",
						"--transport-host=consumer-events-subscription-service." + ConsumerNamespace + ".svc.cluster.local:9043",
						"--http-event-publishers=ptp-event-publisher-service-NODE_NAME." + ProducerNamespace + ".svc.cluster.local:9043",
						"--api-port=8089"},
					Env: []corev1.EnvVar{{Name: "NODE_NAME", Value: nodeNameFull}},
					Ports: []corev1.ContainerPort{{Name: "sub-port", ContainerPort: 9043},
						{Name: "metrics-port", ContainerPort: 9091}},
					VolumeMounts: []corev1.VolumeMount{{MountPath: "/store", Name: "pubsubstore"}},
				},
				{Name: ConsumerContainerName,
					Image: "quay.io/redhat-cne/cloud-event-consumer:latest",
					SecurityContext: &corev1.SecurityContext{
						Privileged: pointer.Bool(true),
						RunAsUser:  rootUser,
					},

					Args: []string{"--local-api-addr=127.0.0.1:9089",
						"--api-path=/api/ocloudNotifications/v1/",
						"--api-addr=127.0.0.1:8089",
						"--http-event-publishers=ptp-event-publisher-service-NODE_NAME." + ProducerNamespace + ".svc.cluster.local:9043"},

					Env: []corev1.EnvVar{{Name: "NODE_NAME", Value: nodeNameFull},
						{Name: "CONSUMER_TYPE", Value: "PTP"},
						{Name: "ENABLE_STATUS_CHECK", Value: "true"},
					},
				},
			},
		},
	}

	// delete namespace if present
	if namespaces.IsPresent(ConsumerNamespace, client.Client) {
		err := namespaces.Delete(ConsumerNamespace, client.Client)
		if err != nil {
			logrus.Warnf("could not delete namespace=%s, err=%s", ConsumerNamespace, err)
		}
		// wait for the namespace to be deleted
		err = namespaces.WaitForDeletion(client.Client, ConsumerNamespace, sidecarNamespaceDeleteTimeout)
		if err != nil {
			return err
		}
		logrus.Infof("namespace %s deleted", ConsumerNamespace)
	}

	labels := map[string]string{
		"security.openshift.io/scc.podSecurityLabelSync": "false",
		"pod-security.kubernetes.io/audit":               "privileged",
		"pod-security.kubernetes.io/enforce":             "privileged",
		"pod-security.kubernetes.io/warn":                "privileged",
		"openshift.io/cluster-monitoring":                "true",
	}
	// create sidecar namespace
	err = namespaces.Create(ConsumerNamespace, client.Client, labels)
	if err != nil {
		return fmt.Errorf("could not create namespace=%s, err=%s", ConsumerNamespace, err)
	}

	err = CreateConsumerServiceAccount()
	if err != nil {
		return err
	}

	err = ConfigureRoles()
	if err != nil {
		return err
	}

	err = CreateConsumerService()
	if err != nil {
		return err
	}

	// create sidecar pod
	podInstance, err := client.Client.Pods(ConsumerNamespace).Create(context.TODO(), &aPod, v1.CreateOptions{})
	if err != nil {
		return err
	}

	//wait for pod to be ready
	err = pods.WaitForCondition(client.Client, podInstance, corev1.ContainersReady, corev1.ConditionTrue, 5*time.Minute)
	if err != nil {
		return err
	}

	//wait for success logs to appear in sidecar pod
	_, err = pods.GetPodLogsRegex(ConsumerNamespace, ConsumerPodName, ConsumerSidecarContainerName, TestSidecarSuccessLogString, true, time.Minute*3)
	if err == nil {
		logrus.Infof("PTP events test consumer pod is up and running")
	}
	return err
}

// create consumer app without sidecar
func CreateConsumerApp(nodeNameFull string) (err error) {
	rootUser := pointer.Int64(0)
	aPod := corev1.Pod{
		TypeMeta: v1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      ConsumerPodName,
			Namespace: ConsumerNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "consumer",
			},
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "pubsubstore",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			ServiceAccountName: ConsumerSaName,
			Containers: []corev1.Container{
				{Name: ConsumerContainerName,
					Image: "quay.io/redhat-cne/cloud-event-consumer:latest",
					SecurityContext: &corev1.SecurityContext{
						Privileged: pointer.Bool(true),
						RunAsUser:  rootUser,
					},

					Args: []string{"--local-api-addr=consumer-events-subscription-service." + ConsumerNamespace + ".svc.cluster.local:9043",
						"--api-path=/api/ocloudNotifications/v2/",
						"--api-addr=127.0.0.1:8089",
						"--api-version=2.0",
						"--http-event-publishers=ptp-event-publisher-service-NODE_NAME." + ProducerNamespace + ".svc.cluster.local:9043"},

					Env: []corev1.EnvVar{{Name: "NODE_NAME", Value: nodeNameFull},
						{Name: "CONSUMER_TYPE", Value: "PTP"},
						{Name: "ENABLE_STATUS_CHECK", Value: "true"},
					},
				},
			},
		},
	}

	// delete namespace if present
	if namespaces.IsPresent(ConsumerNamespace, client.Client) {
		err := namespaces.Delete(ConsumerNamespace, client.Client)
		if err != nil {
			logrus.Warnf("could not delete namespace=%s, err=%s", ConsumerNamespace, err)
		}
		// wait for the namespace to be deleted
		err = namespaces.WaitForDeletion(client.Client, ConsumerNamespace, sidecarNamespaceDeleteTimeout)
		if err != nil {
			return err
		}
		logrus.Infof("namespace %s deleted", ConsumerNamespace)
	}

	labels := map[string]string{
		"security.openshift.io/scc.podSecurityLabelSync": "false",
		"pod-security.kubernetes.io/audit":               "privileged",
		"pod-security.kubernetes.io/enforce":             "privileged",
		"pod-security.kubernetes.io/warn":                "privileged",
		"openshift.io/cluster-monitoring":                "true",
	}

	err = namespaces.Create(ConsumerNamespace, client.Client, labels)
	if err != nil {
		return fmt.Errorf("could not create namespace=%s, err=%s", ConsumerNamespace, err)
	}

	err = CreateConsumerServiceAccount()
	if err != nil {
		return err
	}

	err = CreateConsumerService()
	if err != nil {
		return err
	}

	podInstance, err := client.Client.Pods(ConsumerNamespace).Create(context.TODO(), &aPod, v1.CreateOptions{})
	if err != nil {
		return err
	}

	//wait for pod to be ready
	err = pods.WaitForCondition(client.Client, podInstance, corev1.ContainersReady, corev1.ConditionTrue, 5*time.Minute)
	if err != nil {
		return err
	}

	logrus.Infof("PTP events test consumer pod is up and running")
	return nil
}

const roleName = "use-privileged"
const roleBindingName = "sidecar-rolebinding"

func ConfigureRoles() error {
	aRole := rbacv1.Role{
		TypeMeta: v1.TypeMeta{
			Kind:       "Role",
			APIVersion: "v1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      roleName,
			Namespace: ConsumerNamespace,
		},
		Rules: []rbacv1.PolicyRule{{
			APIGroups:     []string{"security.openshift.io"},
			Resources:     []string{"securitycontextconstraints"},
			ResourceNames: []string{"privileged"},
			Verbs:         []string{"use"},
		},
		},
	}

	aRoleBinding := rbacv1.RoleBinding{
		TypeMeta: v1.TypeMeta{
			Kind:       "RoleBinding",
			APIVersion: "v1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      roleBindingName,
			Namespace: ConsumerNamespace,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      ConsumerSaName,
			Namespace: ConsumerNamespace,
		}},
		RoleRef: rbacv1.RoleRef{
			Kind:     "Role",
			Name:     roleName,
			APIGroup: "rbac.authorization.k8s.io",
		},
	}

	_, err := client.Client.RbacV1().Roles(ConsumerNamespace).Create(context.TODO(), &aRole, v1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error creating role, err=%s", err)
	}

	_, err = client.Client.RbacV1().RoleBindings(ConsumerNamespace).Create(context.TODO(), &aRoleBinding, v1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error creating role bindings, err=%s", err)
	}
	return nil
}

func CreateConsumerServiceAccount() error {
	aServiceAccount := corev1.ServiceAccount{
		TypeMeta: v1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      ConsumerSaName,
			Namespace: ConsumerNamespace,
		},
	}

	_, err := client.Client.ServiceAccounts(ConsumerNamespace).Create(context.TODO(), &aServiceAccount, v1.CreateOptions{})
	return err
}

func CreateConsumerService() error {
	aService := corev1.Service{
		TypeMeta: v1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      ConsumerServiceName,
			Namespace: ConsumerNamespace,
		},
		Spec: corev1.ServiceSpec{
			Ports:    []corev1.ServicePort{{Name: "sub-port", Port: 9043}},
			Selector: map[string]string{"app.kubernetes.io/name": "consumer"},
			Type:     corev1.ServiceTypeClusterIP,
		},
	}
	// Create the new service
	_, err := client.Client.Services(ConsumerNamespace).Create(context.TODO(), &aService, v1.CreateOptions{})
	return err
}

// delete consumer sidecar pod and associated service
func DeleteConsumerNamespace() error {
	// delete namespace if present
	if namespaces.IsPresent(ConsumerNamespace, client.Client) {
		err := namespaces.Delete(ConsumerNamespace, client.Client)
		if err != nil {
			return fmt.Errorf("could not delete namespace=%s, err=%s", ConsumerNamespace, err)
		}
	}
	return nil
}

// delete Service
func DeleteService(namespace, name string) {
	// Delete service
	foregroundDelete := v1.DeletePropagationForeground
	err := client.Client.CoreV1().Services(namespace).Delete(context.TODO(), name, v1.DeleteOptions{PropagationPolicy: &foregroundDelete})
	if err != nil {
		logrus.Warnf("error deleting ns=%s service=%s err: %v", namespace, name, err)
	}
}

const DeleteBackground = "deleteBackground"
const DeleteForeground = "deleteForeground"

// gets the cloud-event-proxy sidecar image from the ptp-operator pods
func GetCloudEventProxyImageFromPod() (image string, err error) {
	ptpPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	if err != nil {
		return image, fmt.Errorf("could not get list of linux-ptp-daemon pods, err=%s", err)
	}
	for podIndex := range ptpPods.Items {
		for _, c := range ptpPods.Items[podIndex].Spec.Containers {
			if strings.Contains(c.Name, "cloud-event-proxy") {
				return c.Image, nil
			}

		}
	}
	return image, fmt.Errorf("could find a cloud-event-proxy sidecar in ptp-daemon pods, cannot get cloud-event-proxy image")
}

// returns last Regex match in the logs for a given pod
func MonitorPodLogsRegex() (term chan bool, err error) {
	namespace := ConsumerNamespace
	podName := ConsumerPodName
	containerName := ConsumerContainerName
	regex := `received event ({.*})`
	count := int64(0)
	podLogOptions := corev1.PodLogOptions{
		Container: containerName,
		Follow:    true,
		TailLines: &count,
	}
	term = make(chan bool)
	go func() {
		podLogRequest := testclient.Client.CoreV1().Pods(namespace).GetLogs(podName, &podLogOptions)
		stream, err := podLogRequest.Stream(context.TODO())
		if err != nil {
			logrus.Errorf("could not retrieve log in ns=%s pod=%s, err=%s", namespace, podName, err)
		}
		defer stream.Close()
		scanner := bufio.NewScanner(stream)
		for scanner.Scan() {

			select {
			case <-term:
				logrus.Infof("received term signal, exiting MonitorPodLogsRegex")
				return

			default:
				line := scanner.Text()
				logrus.Trace(line)

				r := regexp.MustCompile(regex)
				matches := r.FindAllStringSubmatch(line, -1)
				if len(matches) > 0 {
					aStoredEvent, eType, err := createStoredEvent([]byte(matches[0][1]))
					if err == nil {
						PubSub.Publish(eType, aStoredEvent)
					}
				}
			}
		}
		// Check for any scanner errors
		if err := scanner.Err(); err != nil {
			logrus.Errorf("Error reading input:%s", err)
		}
	}()

	return term, nil
}

// returns last Regex match in the logs for a given pod
func PushInitialEvent(eventType string, timeout time.Duration) (err error) {
	namespace := ConsumerNamespace
	podName := ConsumerPodName
	containerName := ConsumerContainerName
	regex := `Got CurrentState: ({.*})`

	count := int64(0)
	podLogOptions := corev1.PodLogOptions{
		Container: containerName,
		Follow:    true,
		TailLines: &count,
	}

	podLogRequest := testclient.Client.CoreV1().Pods(namespace).GetLogs(podName, &podLogOptions)
	stream, err := podLogRequest.Stream(context.TODO())
	if err != nil {
		return fmt.Errorf("could not retrieve log in ns=%s pod=%s, err=%s", namespace, podName, err)
	}
	defer stream.Close()
	start := time.Now()
	for {
		scanner := bufio.NewScanner(stream)
		for scanner.Scan() {
			t := time.Now()
			elapsed := t.Sub(start)
			if elapsed > timeout {
				return fmt.Errorf("timedout PushInitialValue, waiting for log in ns=%s pod=%s, looking for = %s", namespace, podName, regex)
			}
			line := scanner.Text()
			logrus.Trace(line)

			r := regexp.MustCompile(regex)
			matches := r.FindAllStringSubmatch(line, -1)
			if len(matches) > 0 {
				aStoredEvent, eType, err := createStoredEvent([]byte(matches[0][1]))
				if err != nil {
					return err
				}
				if eType == eventType {
					PubSub.Publish(eType, aStoredEvent)
					return nil
				}
			}
		}

	}
}
func createStoredEvent(data []byte) (aStoredEvent exports.StoredEvent, aType string, err error) {
	apiVersion := ptphelper.PtpEventEnabled()
	if apiVersion == 1 {
		var e cneevent.Event
		tmpData := strings.ReplaceAll(string(data), `\`, ``)
		err = json.Unmarshal([]byte(tmpData), &e)
		if err != nil {
			return aStoredEvent, aType, err
		}

		if !isEventValid(&e) {
			return aStoredEvent, aType, fmt.Errorf("parsed invalid event event=%+v", e)
		}
		logrus.Debug(e)

		latency := time.Now().UnixMilli() - e.GetTime().UnixMilli()
		// set log to Info level for performance measurement
		logrus.Debugf("Latency for the event: %d ms\n", latency)

		values := exports.StoredEventValues{}
		for _, v := range e.Data.Values {
			dataType := string(v.DataType)
			values[dataType] = v.Value
		}
		return exports.StoredEvent{exports.EventTimeStamp: e.Time, exports.EventType: e.Type, exports.EventSource: e.Source, exports.EventValues: values}, e.Type, nil
	}
	var e ce.Event
	tmpData := strings.ReplaceAll(string(data), `\`, ``)
	err = json.Unmarshal([]byte(tmpData), &e)
	if err != nil {
		return aStoredEvent, aType, err
	}

	if !isEventValidV2(&e) {
		return aStoredEvent, aType, fmt.Errorf("parsed invalid event event=%+v", e)
	}
	logrus.Debug(e)

	latency := time.Now().UnixMilli() - e.Context.GetTime().UnixMilli()
	// set log to Info level for performance measurement
	logrus.Debugf("Latency for the event: %d ms\n", latency)

	var d cneevent.Data
	err = json.Unmarshal(e.Data(), &d)
	if err != nil {
		logrus.Errorf("Failed to unmarshal data for v2 event: %s\n", string(e.Data()))
		return aStoredEvent, aType, err
	}
	values := exports.StoredEventValues{}
	for _, v := range d.Values {
		dataType := string(v.DataType)
		values[dataType] = v.Value
	}
	aType = e.Context.GetType()
	return exports.StoredEvent{exports.EventTimeStamp: e.Context.GetTime(), exports.EventType: aType, exports.EventSource: e.Context.GetSource(), exports.EventValues: values}, aType, nil
}

func isEventValid(aEvent *cneevent.Event) bool {
	if aEvent.Time == nil || aEvent.Data == nil {
		return false
	}
	return true
}

func isEventValidV2(aEvent *ce.Event) bool {
	if aEvent.Context == nil || aEvent.Context.GetTime().IsZero() || aEvent.Data() == nil {
		return false
	}
	return true
}

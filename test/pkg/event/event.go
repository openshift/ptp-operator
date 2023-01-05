package event

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/openshift/ptp-operator/test/pkg"
	"github.com/openshift/ptp-operator/test/pkg/client"
	"github.com/openshift/ptp-operator/test/pkg/namespaces"
	"github.com/openshift/ptp-operator/test/pkg/pods"
	"github.com/openshift/ptp-operator/test/pkg/testconfig"
	lib "github.com/redhat-cne/ptp-listener-lib"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

const (
	ConsumerSidecarTestNamespace  = "ptp-event-framework-test"
	ProviderSidecarTestNamespace  = "openshift-ptp"
	TestSidecarName               = "cloud-proxy-sidecar"
	TestSidecarContainerName      = "sidecar"
	TestSidecarSaName             = "sidecar-sa"
	TestServiceName               = "consumer-events-subscription-service"
	TestSidecarSuccessLogString   = "rest service returned healthy status"
	CustomerCloudEventProxyPort   = 8089
	ProviderCloudEventProxyPort   = 9085
	sidecarNamespaceDeleteTimeout = time.Minute * 2
)

// enables event if ptp event is required
func Enable() bool {
	eventMode, _ := strconv.ParseBool(os.Getenv("ENABLE_PTP_EVENT"))
	return eventMode
}

// create ptp-events sidecar
func CreateEventProxySidecar(nodeName string) (err error) {
	// using the first component of the node name before the first dot (master2.example.com -> master2)
	if nodeName != "" && strings.Contains(nodeName, ".") {
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
			Name:      "cloud-proxy-sidecar",
			Namespace: ConsumerSidecarTestNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "proxy",
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
			ServiceAccountName: TestSidecarSaName,
			Containers: []corev1.Container{
				{Name: TestSidecarContainerName,
					Image: testSidecarImageName,
					SecurityContext: &corev1.SecurityContext{
						Privileged: pointer.Bool(true),
						RunAsUser:  rootUser,
					},

					Args: []string{"--metrics-addr=127.0.0.1:9091",
						"--store-path=/store",
						"--transport-host=consumer-events-subscription-service." + ConsumerSidecarTestNamespace + ".svc.cluster.local:9043",
						"--http-event-publishers=ptp-event-publisher-service-" + nodeName + "." + ProviderSidecarTestNamespace + ".svc.cluster.local:9043",
						"--api-port=8089"},
					Env: []corev1.EnvVar{{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"}}},
						{Name: "NODE_IP", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.hostIP"}}}},
					Ports: []corev1.ContainerPort{{Name: "sub-port", ContainerPort: 9043},
						{Name: "metrics-port", ContainerPort: 9091}},
					VolumeMounts: []corev1.VolumeMount{{MountPath: "/store", Name: "pubsubstore"}},
				},
			},
		},
	}

	// delete namespace if present
	if namespaces.IsPresent(ConsumerSidecarTestNamespace, client.Client) {
		err := namespaces.Delete(ConsumerSidecarTestNamespace, client.Client)
		if err != nil {
			logrus.Warnf("could not delete namespace=%s, err=%s", ConsumerSidecarTestNamespace, err)
		}
		// wait for the namespace to be deleted
		err = namespaces.WaitForDeletion(client.Client, ConsumerSidecarTestNamespace, sidecarNamespaceDeleteTimeout)
		if err != nil {
			return err
		}
		logrus.Infof("namespace %s deleted", ConsumerSidecarTestNamespace)
	}

	// create sidecar namespace
	err = namespaces.Create(ConsumerSidecarTestNamespace, client.Client)
	if err != nil {
		return fmt.Errorf("could not create namespace=%s, err=%s", ConsumerSidecarTestNamespace, err)
	}

	// create service account
	err = ConfigurePrivilegedServiceAccount()
	if err != nil {
		return fmt.Errorf("could not configure privileged rights, err=%s", err)
	}

	// create sidecar pod
	podInstance, err := client.Client.Pods(ConsumerSidecarTestNamespace).Create(context.TODO(), &aPod, v1.CreateOptions{})
	if err != nil {
		return err
	}

	//wait for pod to be ready
	err = pods.WaitForCondition(client.Client, podInstance, corev1.ContainersReady, corev1.ConditionTrue, 5*time.Minute)
	if err != nil {
		return err
	}

	// create sidecar service
	err = CreateServiceSidecar()
	if err != nil {
		return err
	}

	//wait for success logs to appear in sidecar pod
	_, err = pods.GetPodLogsRegex(ConsumerSidecarTestNamespace, TestSidecarName, TestSidecarContainerName, TestSidecarSuccessLogString, true, time.Minute*3)
	if err != nil {
		logrus.Infof("PTP events test sidecar pod is up and running")
	}
	return err
}

const roleName = "use-privileged"
const roleBindingName = "sidecar-rolebinding"

func ConfigurePrivilegedServiceAccount() error {
	aRole := rbacv1.Role{
		TypeMeta: v1.TypeMeta{
			Kind:       "Role",
			APIVersion: "v1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      roleName,
			Namespace: ConsumerSidecarTestNamespace,
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
			Namespace: ConsumerSidecarTestNamespace,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      TestSidecarSaName,
			Namespace: ConsumerSidecarTestNamespace,
		}},
		RoleRef: rbacv1.RoleRef{
			Kind:     "Role",
			Name:     roleName,
			APIGroup: "rbac.authorization.k8s.io",
		},
	}

	aServiceAccount := corev1.ServiceAccount{
		TypeMeta: v1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      TestSidecarSaName,
			Namespace: ConsumerSidecarTestNamespace,
		},
	}

	// created role
	_, err := client.Client.RbacV1().Roles(ConsumerSidecarTestNamespace).Create(context.TODO(), &aRole, v1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error creating role, err=%s", err)
	}
	// delete any previous rolebindings
	/*err = client.Client.RbacV1().RoleBindings(ConsumerSidecarTestNamespace).Delete(context.TODO(), roleBindingName, v1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("error deleting previous rolebinding, err=%s", err)
	}*/
	// create rolebinding
	_, err = client.Client.RbacV1().RoleBindings(ConsumerSidecarTestNamespace).Create(context.TODO(), &aRoleBinding, v1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error creating role bindings, err=%s", err)
	}
	// create service account
	_, err = client.Client.ServiceAccounts(ConsumerSidecarTestNamespace).Create(context.TODO(), &aServiceAccount, v1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error creating service account, err=%s", err)
	}
	return nil
}

// create ptp events consumer service
func CreateServiceSidecar() error {
	aService := corev1.Service{
		TypeMeta: v1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      TestServiceName,
			Namespace: ConsumerSidecarTestNamespace,
		},
		Spec: corev1.ServiceSpec{
			Ports:    []corev1.ServicePort{{Name: "sub-port", Port: 9043}},
			Selector: map[string]string{"app.kubernetes.io/name": "proxy"},
			Type:     corev1.ServiceTypeClusterIP,
		},
	}
	// Create the new service
	_, err := client.Client.Services(ConsumerSidecarTestNamespace).Create(context.TODO(), &aService, v1.CreateOptions{})
	return err
}

// delete consumer sidecar pod and associated service
func DeleteTestSidecarNamespace() error {
	// delete namespace if present
	if namespaces.IsPresent(ConsumerSidecarTestNamespace, client.Client) {
		err := namespaces.Delete(ConsumerSidecarTestNamespace, client.Client)
		if err != nil {
			return fmt.Errorf("could not delete namespace=%s, err=%s", ConsumerSidecarTestNamespace, err)
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

func IsDeployConsumerSidecar() (deployConsumerSidecar bool) {
	value, isSet := os.LookupEnv("USE_PTP_EVENT_CONSUMER_SIDECAR")
	value = strings.ToLower(value)
	deployConsumerSidecar = !isSet || (isSet && strings.Contains(value, "true"))
	logrus.Infof("deployConsumerSidecar=%t", deployConsumerSidecar)
	return deployConsumerSidecar
}

const localHttpServerPort = 8989

// initialized the event listening framework to start listening for all supported events
func InitEvents(fullConfig *testconfig.TestConfig, localHttpServerPort, localPortFoward int, useSideCar bool) {
	// initializes internal channel-pubsub object
	lib.InitPubSub()
	defer logrus.Infof("Init Events Fullconfig=%v", fullConfig)
	const waitForPortUp = time.Second * 20
	time.Sleep(waitForPortUp)
	ptpEventsPodName := fullConfig.DiscoveredClockUnderTestPod.Name
	ptpEventsPodPort := ProviderCloudEventProxyPort
	eventNamespace := ProviderSidecarTestNamespace
	if useSideCar {
		ptpEventsPodName = TestSidecarName
		ptpEventsPodPort = CustomerCloudEventProxyPort
		eventNamespace = ConsumerSidecarTestNamespace
	}
	err := lib.StartListening(
		localPortFoward,  // this is the local end of the port forwarding tunnel
		ptpEventsPodPort, // this is the remote end of the port forwarding tunnel (port)
		localHttpServerPort,
		ptpEventsPodName, // this is the remote end of the port forwarding tunnel (pod's name)
		fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName, // this is the clock under test node name
		eventNamespace,               // this is the remote end of the port forwarding tunnel (pod's namespace)
		client.Client.KubeConfigPath, // this is the remote end of the port forwarding tunnel (pod's kubeconfig)
		client.Client.Config.Host,    // this is the remote end of the port forwarding tunnel (pod's kubernetes api IP)
	)

	if err != nil {
		logrus.Errorf("PTP events are not available due to err=%s", err)
	}
}

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
	return image, fmt.Errorf("could find a cloud-event-proxy sidecar in ptp-daemon pods, cannot get cloud-events-proxy image")
}

package privilegeddaemonset

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	v1core "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	pointer "k8s.io/utils/ptr"
)

const (
	tolerationsPeriodSecs = 300
)

type DaemonSetClient struct {
	K8sClient kubernetes.Interface
}

var daemonsetClient = DaemonSetClient{}

func SetDaemonSetClient(aK8sClient kubernetes.Interface) {
	daemonsetClient.K8sClient = aK8sClient
}

const (
	roleSaName             = "privileged-ds"
	waitingTime            = 5 * time.Second
	namespaceDeleteTimeout = time.Minute * 2
)

//nolint:funlen
func createDaemonSetsTemplate(dsName, namespace, containerName, imageWithVersion string, labelsMap map[string]string, cpuReq, cpuLim, memReq, memLim string) *appsv1.DaemonSet {
	dsAnnotations := make(map[string]string)
	dsAnnotations["debug.openshift.io/source-container"] = containerName
	dsAnnotations["openshift.io/scc"] = "node-exporter"

	matchLabels := make(map[string]string)
	matchLabels["name"] = dsName

	for key, value := range labelsMap {
		matchLabels[key] = value
	}

	rootUser := pointer.To(int64(0))

	container := v1core.Container{
		Name:            containerName,
		Image:           imageWithVersion,
		ImagePullPolicy: "IfNotPresent",
		SecurityContext: &v1core.SecurityContext{
			Privileged: pointer.To(true),
			RunAsUser:  rootUser,
		},
		Stdin:                  true,
		StdinOnce:              true,
		TerminationMessagePath: "/dev/termination-log",
		TTY:                    true,
		VolumeMounts: []v1core.VolumeMount{
			{
				MountPath: "/host",
				Name:      "host",
			},
		},
	}
	// setting CPU and memory request/limits
	container.Resources.Requests = v1core.ResourceList{}
	container.Resources.Limits = v1core.ResourceList{}
	container.Resources.Requests[v1core.ResourceCPU] = resource.MustParse(cpuReq)
	container.Resources.Limits[v1core.ResourceCPU] = resource.MustParse(cpuLim)
	container.Resources.Requests[v1core.ResourceMemory] = resource.MustParse(memReq)
	container.Resources.Limits[v1core.ResourceMemory] = resource.MustParse(memLim)
	preemptPolicyLowPrio := v1core.PreemptLowerPriority
	hostPathTypeDir := v1core.HostPathDirectory
	tolerationsSeconds := pointer.To(int64(tolerationsPeriodSecs))

	return &appsv1.DaemonSet{

		ObjectMeta: metav1.ObjectMeta{
			Name:        dsName,
			Namespace:   namespace,
			Annotations: dsAnnotations,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: matchLabels,
			},
			Template: v1core.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: matchLabels,
				},
				Spec: v1core.PodSpec{
					ServiceAccountName: roleSaName,
					Containers:         []v1core.Container{container},
					PreemptionPolicy:   &preemptPolicyLowPrio,
					Priority:           pointer.To(int32(0)),
					HostNetwork:        true,
					HostIPC:            true,
					HostPID:            true,
					Tolerations: []v1core.Toleration{
						{
							Effect:            "NoExecute",
							Key:               "node.kubernetes.io/not-ready",
							Operator:          "Exists",
							TolerationSeconds: tolerationsSeconds,
						},
						{
							Effect:            "NoExecute",
							Key:               "node.kubernetes.io/unreachable",
							Operator:          "Exists",
							TolerationSeconds: tolerationsSeconds,
						},
						{
							Effect: "NoSchedule",
							Key:    "node-role.kubernetes.io/master",
						},
						{
							Effect: "NoSchedule",
							Key:    "node-role.kubernetes.io/control-plane",
						},
					},
					Volumes: []v1core.Volume{
						{
							Name: "host",
							VolumeSource: v1core.VolumeSource{
								HostPath: &v1core.HostPathVolumeSource{
									Path: "/",
									Type: &hostPathTypeDir,
								},
							},
						},
					},
				},
			},
		},
	}
}

// This method is used to delete a daemonset specified by the name at a specified namespace
func DeleteDaemonSet(daemonSetName, namespace string) error {
	const (
		Timeout = 5 * time.Minute
	)

	deletePolicy := metav1.DeletePropagationForeground
	err := daemonsetClient.K8sClient.AppsV1().DaemonSets(namespace).Delete(context.TODO(), daemonSetName, metav1.DeleteOptions{PropagationPolicy: &deletePolicy})
	if err != nil {
		return fmt.Errorf("daemonset %q deletion failed, err: %v", daemonSetName, err)
	}
	dsDeleted := false
	start := time.Now()
	for time.Since(start) < Timeout {
		if !doesDaemonSetExist(daemonSetName, namespace) {
			dsDeleted = true
			break
		}
		time.Sleep(waitingTime)
	}

	if !dsDeleted {
		return fmt.Errorf("timeout waiting for daemonset %q to be deleted", daemonSetName)
	}

	return nil
}

// Check if the daemonset exists
func doesDaemonSetExist(daemonSetName, namespace string) bool {
	_, err := daemonsetClient.K8sClient.AppsV1().DaemonSets(namespace).Get(context.TODO(), daemonSetName, metav1.GetOptions{})
	return err == nil
}

func IsDaemonSetReady(daemonSetName, namespace, image string) bool {
	const hoursPerWeek = 168 // 7 days

	// The daemonset will be considered not ready if it does not exist
	ds, err := daemonsetClient.K8sClient.AppsV1().DaemonSets(namespace).Get(context.TODO(), daemonSetName, metav1.GetOptions{})
	if err != nil {
		return false
	}

	// Or if it's been running for more than a week
	if time.Since(ds.CreationTimestamp.Time).Hours() > hoursPerWeek {
		return false
	}

	// Or if the container image do not match the desired one
	if ds.Spec.Template.Spec.Containers[0].Image != image {
		return false
	}

	// Or if it's not healthy
	return isDaemonSetReady(&ds.Status)
}

// This function is used to create a daemonset with the specified name, namespace, container name and image with the timeout to check
// if the deployment is ready and all daemonset pods are running fine
func CreateDaemonSet(daemonSetName, namespace, containerName, imageWithVersion string, labels map[string]string, timeout time.Duration, cpuReq, cpuLim, memReq, memLim string) (aPodList *v1core.PodList, err error) {
	// first, initialize the namespace
	err = initNamespace(namespace)
	if err != nil {
		return aPodList, fmt.Errorf("failed to initialize the privileged daemonset namespace, err: %v", err)
	}

	daemonSet := createDaemonSetsTemplate(daemonSetName, namespace, containerName, imageWithVersion, labels, cpuReq, cpuLim, memReq, memLim)

	if doesDaemonSetExist(daemonSetName, namespace) {
		err = DeleteDaemonSet(daemonSetName, namespace)
		if err != nil {
			return aPodList, fmt.Errorf("failed to delete daemonset %q, err: %v", daemonSetName, err)
		}
	}

	_, err = daemonsetClient.K8sClient.AppsV1().DaemonSets(namespace).Create(context.TODO(), daemonSet, metav1.CreateOptions{})
	if err != nil {
		return aPodList, err
	}

	err = WaitDaemonsetReady(namespace, daemonSetName, timeout)
	if err != nil {
		return aPodList, err
	}

	aPodList, err = daemonsetClient.K8sClient.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "name=" + daemonSetName})
	if err != nil {
		return aPodList, err
	}

	return aPodList, nil
}

// This function is used to wait until daemonset is ready
func WaitDaemonsetReady(namespace, name string, timeout time.Duration) error {
	nodes, err := daemonsetClient.K8sClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node list, err:%s", err)
	}

	nodesCount := int32(len(nodes.Items))
	isReady := false
	for start := time.Now(); !isReady && time.Since(start) < timeout; {
		daemonSet, err := daemonsetClient.K8sClient.AppsV1().DaemonSets(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get daemonset %q (ns %q), err: %v", name, namespace, err)
		}

		if daemonSet.Status.DesiredNumberScheduled == nodesCount {
			if isDaemonSetReady(&daemonSet.Status) {
				isReady = true
				break
			}
		}

		time.Sleep(waitingTime)
	}

	if !isReady {
		return fmt.Errorf("daemonset %q (ns %q) could not be deployed (timed out)", name, namespace)
	}

	return nil
}

func isDaemonSetReady(status *appsv1.DaemonSetStatus) bool {
	//nolint:gocritic
	return status.DesiredNumberScheduled == status.CurrentNumberScheduled &&
		status.DesiredNumberScheduled == status.NumberAvailable &&
		status.DesiredNumberScheduled == status.NumberReady &&
		status.NumberMisscheduled == 0
}

//nolint:funlen
func ConfigurePrivilegedServiceAccount(namespace string) error {
	aRole := rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleSaName,
			Namespace: namespace,
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
		TypeMeta: metav1.TypeMeta{
			Kind:       "RoleBinding",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleSaName,
			Namespace: namespace,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      roleSaName,
			Namespace: namespace,
		}},
		RoleRef: rbacv1.RoleRef{
			Kind:     "Role",
			Name:     roleSaName,
			APIGroup: "rbac.authorization.k8s.io",
		},
	}

	aServiceAccount := v1core.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleSaName,
			Namespace: namespace,
		},
	}

	// create role
	_, err := daemonsetClient.K8sClient.RbacV1().Roles(namespace).Create(context.TODO(), &aRole, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error creating role, err=%s", err)
	}

	// create rolebinding
	_, err = daemonsetClient.K8sClient.RbacV1().RoleBindings(namespace).Create(context.TODO(), &aRoleBinding, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error creating role bindings, err=%s", err)
	}
	// create service account
	_, err = daemonsetClient.K8sClient.CoreV1().ServiceAccounts(namespace).Create(context.TODO(), &aServiceAccount, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error creating service account, err=%s", err)
	}
	return nil
}

func initNamespace(namespace string) (err error) {
	err =
		DeleteNamespaceIfPresent(namespace)
	if err != nil {
		return fmt.Errorf("could not delete (if present) namespace=%s, err=%s", namespace, err)
	}

	// create namespace
	err = namespaceCreate(namespace)
	if err != nil {
		return fmt.Errorf("could not create namespace=%s, err=%s", namespace, err)
	}

	// create service account
	err = ConfigurePrivilegedServiceAccount(namespace)
	if err != nil {
		return fmt.Errorf("could not configure privileged rights, err=%s", err)
	}
	return nil
}

// WaitForCondition waits until the pod will have specified condition type with the expected status
func namespaceIsPresent(namespace string) bool {
	_, err := daemonsetClient.K8sClient.CoreV1().Namespaces().Get(context.Background(), namespace, metav1.GetOptions{})
	return err == nil
}

// WaitForDeletion waits until the namespace will be removed from the cluster
func namespaceWaitForDeletion(nsName string, timeout time.Duration) error {
	//nolint:revive
	return wait.PollUntilContextTimeout(context.TODO(), time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		_, err := daemonsetClient.K8sClient.CoreV1().Namespaces().Get(context.Background(), nsName, metav1.GetOptions{})
		if k8serrors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
}

// Create creates a new namespace with the given name.
// If the namespace exists, it returns.
func namespaceCreate(namespace string) error {
	_, err := daemonsetClient.K8sClient.CoreV1().Namespaces().Create(context.Background(), &v1core.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		}},
		metav1.CreateOptions{},
	)

	if k8serrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func DeleteNamespaceIfPresent(namespace string) (err error) {
	// delete namespace if present
	if !namespaceIsPresent(namespace) {
		return nil
	}
	err = daemonsetClient.K8sClient.CoreV1().Namespaces().Delete(context.Background(), namespace, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("could not delete namespace %q, err: %v", namespace, err)
	}
	// wait for the namespace to be deleted
	err = namespaceWaitForDeletion(namespace, namespaceDeleteTimeout)
	if err != nil {
		return fmt.Errorf("failed waiting for namespace %q to be deleted, err: %v", namespace, err)
	}

	return nil
}

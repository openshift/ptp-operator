package ptpcfg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/golang/glog"
	"github.com/openshift/ptp-operator/pkg/apply"
	"github.com/openshift/ptp-operator/pkg/names"
	"github.com/openshift/ptp-operator/pkg/render"
	ptpv1 "github.com/openshift/ptp-operator/pkg/apis/ptp/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_ptpcfg")

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new PtpCfg Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcilePtpCfg{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("ptpcfg-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource PtpCfg
	err = c.Watch(&source.Kind{Type: &ptpv1.PtpCfg{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner PtpCfg
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &ptpv1.PtpCfg{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcilePtpCfg implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcilePtpCfg{}

// ReconcilePtpCfg reconciles a PtpCfg object
type ReconcilePtpCfg struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a PtpCfg object and makes changes based on the state read
// and what is in the PtpCfg.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcilePtpCfg) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling PtpCfg")

	// Fetch the PtpCfg instance
	defaultCfg := &ptpv1.PtpCfg{}
	err := r.client.Get(context.TODO(), types.NamespacedName{
		Name: names.DefaultCfgName, Namespace: names.Namespace}, defaultCfg)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			defaultCfg.SetNamespace(names.Namespace)
			defaultCfg.SetName(names.DefaultCfgName)
			defaultCfg.Spec = ptpv1.PtpCfgSpec{
				Profile: []ptpv1.PtpProfile{},
				Recommend: []ptpv1.PtpRecommend{},
			}
			if err = r.client.Create(context.TODO(), defaultCfg); err != nil {
				reqLogger.Error(err, "failed to create default ptp config",
					"Namespace", names.Namespace, "Name", names.DefaultCfgName)
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	instances := &ptpv1.PtpCfgList{}
	err = r.client.List(context.TODO(), &client.ListOptions{}, instances)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	nodeList := &corev1.NodeList{}
	err = r.client.List(context.TODO(), &client.ListOptions{}, nodeList)
	if err != nil {
		glog.Errorf("failed to list nodes")
		return reconcile.Result{}, err
	}

	if err = r.syncLinuxptpDaemon(defaultCfg); err != nil {
		return reconcile.Result{}, err
	}

	if err = r.syncNodePtpDevice(nodeList); err != nil {
		return reconcile.Result{}, err
	}

	if err = r.syncPtpCfg(instances, nodeList); err != nil {
		return reconcile.Result{}, err
	}

	// Define a new Pod object
	pod := newPodForCR(defaultCfg)

	// Set PtpCfg instance as the owner and controller
	if err := controllerutil.SetControllerReference(defaultCfg, pod, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	// Check if this Pod already exists
	found := &corev1.Pod{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new Pod", "Pod.Namespace", pod.Namespace, "Pod.Name", pod.Name)
		err = r.client.Create(context.TODO(), pod)
		if err != nil {
			return reconcile.Result{}, err
		}

		// Pod created successfully - don't requeue
		return reconcile.Result{}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}

	// Pod already exists - don't requeue
	reqLogger.Info("Skip reconcile: Pod already exists", "Pod.Namespace", found.Namespace, "Pod.Name", found.Name)
	return reconcile.Result{}, nil
}

// syncLinuxptpDaemon synchronizes Linuxptp DaemonSet
func (r *ReconcilePtpCfg) syncLinuxptpDaemon(defaultCfg *ptpv1.PtpCfg) error {
	var err error
	objs := []*uns.Unstructured{}

	data := render.MakeRenderData()
	data.Data["Image"] = os.Getenv("LINUXPTP_DAEMON_IMAGE")
	data.Data["Namespace"] = names.Namespace
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	objs, err = render.RenderDir(filepath.Join(names.ManifestDir, "linuxptp"), &data)
	if err != nil {
		return fmt.Errorf("failed to render linuxptp daemon manifest: %v", err)
	}

	for _, obj := range objs {
		if err = controllerutil.SetControllerReference(defaultCfg, obj, r.scheme); err != nil {
			return fmt.Errorf("failed to set owner reference: %v", err)
		}
		if err = apply.ApplyObject(context.TODO(), r.client, obj); err != nil {
			return fmt.Errorf("failed to apply object %v with err: %v", obj, err)
		}
	}
	return nil
}

// syncNodePtpDevice synchronizes NodePtpDevice CR for each node
func (r *ReconcilePtpCfg) syncNodePtpDevice(nodeList *corev1.NodeList) error {
	for _, node := range nodeList.Items {
		found := &ptpv1.NodePtpDevice{}
		err := r.client.Get(context.TODO(), types.NamespacedName{
			Namespace: names.Namespace, Name: node.Name}, found)
		if err != nil {
			if errors.IsNotFound(err) {
				ptpDev := &ptpv1.NodePtpDevice{}
				ptpDev.Name = node.Name
				ptpDev.Namespace = names.Namespace
				err = r.client.Create(context.TODO(), ptpDev)
				if err != nil {
					return fmt.Errorf("failed to create NodePtpDevice for node: %v", node.Name)
				}
				glog.Infof("create NodePtpDevice successfully for node: %v", node.Name)
			} else {
				return fmt.Errorf("failed to get NodePtpDevice for node: %v", node.Name)
			}
		} else {
			glog.Infof("NodePtpDevice exists for node: %v, skipping", node.Name)
		}
	}
	return nil
}

// syncPtpCfg synchronizes PtpCfg CR
func (r *ReconcilePtpCfg) syncPtpCfg(ptpCfgList *ptpv1.PtpCfgList, nodeList *corev1.NodeList) error {
	for _, node := range nodeList.Items {
		nodePtpProfile, err := getRecommendNodePtpProfile(ptpCfgList, node)
		if err != nil {
			return fmt.Errorf("failed to get recommended node PtpCfg: %v", err)
		}

		cm := &corev1.ConfigMap{}
		err = r.client.Get(context.TODO(), types.NamespacedName{
			Namespace: names.Namespace, Name: "ptp-configmap-" + node.Name}, cm)
		if err != nil {
			if errors.IsNotFound(err) {
				nodePtpConfigMap := &corev1.ConfigMap{}
				nodePtpConfigMap.Name = "ptp-configmap-" + node.Name
				nodePtpConfigMap.Namespace = names.Namespace
				data, err := json.Marshal(nodePtpProfile)
				if err != nil {
					return fmt.Errorf("failed to Marshal nodePtpProfile: %v", err)
				}
				nodePtpConfigMap.Data = map[string]string{"node-ptp-profile": string(data)}

				err = r.client.Create(context.TODO(), nodePtpConfigMap)
				if err != nil {
					return fmt.Errorf("failed to create node ptp config map: %v", err)
				}
				glog.Infof("create node ptp config map successfully for node: %v", node.Name)
			} else {
				return fmt.Errorf("failed to get node ptp config map: %v", err)
			}
		} else {
			glog.Infof("node ptp config map already exists, updating")
			data, err := json.Marshal(nodePtpProfile)
			if err != nil {
				return fmt.Errorf("failed to Marshal nodePtpProfile: %v", err)
			}
			cm.Data["node-ptp-profile"] = string(data)
			err = r.client.Update(context.TODO(), cm)
			if err != nil {
				return fmt.Errorf("failed to update node ptp config map: %v", err)
			}
		}
	}
	return nil
}

// newPodForCR returns a busybox pod with the same name/namespace as the cr
func newPodForCR(cr *ptpv1.PtpCfg) *corev1.Pod {
	labels := map[string]string{
		"app": cr.Name,
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-pod",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "busybox",
					Image:   "busybox",
					Command: []string{"sleep", "3600"},
				},
			},
		},
	}
}

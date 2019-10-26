package ptpcfg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/golang/glog"
	"github.com/openshift/ptp-operator/pkg/names"
	ptpv1 "github.com/openshift/ptp-operator/pkg/apis/ptp/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
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

	instances := &ptpv1.PtpCfgList{}
	err := r.client.List(context.TODO(), &client.ListOptions{}, instances)
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

	if err = r.syncPtpCfg(instances, nodeList); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// syncPtpCfg synchronizes PtpCfg CR
func (r *ReconcilePtpCfg) syncPtpCfg(ptpCfgList *ptpv1.PtpCfgList, nodeList *corev1.NodeList) error {
	var err error

	nodePtpConfigMap := &corev1.ConfigMap{}
	nodePtpConfigMap.Name = names.DefaultPTPConfigMapName
	nodePtpConfigMap.Namespace = names.Namespace
	nodePtpConfigMap.Data = make(map[string]string)

	for _, node := range nodeList.Items {
		nodePtpProfile, err := getRecommendNodePtpProfile(ptpCfgList, node)
		if err != nil {
			return fmt.Errorf("failed to get recommended node PtpCfg: %v", err)
		}

		data, err := json.Marshal(nodePtpProfile)
		if err != nil {
			return fmt.Errorf("failed to Marshal nodePtpProfile: %v", err)
		}
		nodePtpConfigMap.Data[node.Name] = string(data)
	}

	cm := &corev1.ConfigMap{}
	err = r.client.Get(context.TODO(), types.NamespacedName{
		Namespace: names.Namespace, Name: names.DefaultPTPConfigMapName}, cm)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), nodePtpConfigMap)
			if err != nil {
				return fmt.Errorf("failed to create node ptp config map: %v", err)
			}
			glog.Infof("create node ptp config map successfully")
		} else {
			return fmt.Errorf("failed to get node ptp config map: %v", err)
		}
	} else {
		glog.Infof("node ptp config map already exists, updating")
		cm.Data = nodePtpConfigMap.Data
		err = r.client.Update(context.TODO(), cm)
		if err != nil {
			return fmt.Errorf("failed to update node ptp config map: %v", err)
		}
	}
	return nil
}

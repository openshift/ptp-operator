/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"
	"github.com/golang/glog"
	ptpv1 "github.com/openshift/ptp-operator/api/v1"
	"github.com/openshift/ptp-operator/pkg/apply"
	"github.com/openshift/ptp-operator/pkg/names"
	"github.com/openshift/ptp-operator/pkg/render"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// PtpOperatorConfigReconciler reconciles a PtpOperatorConfig object
type PtpOperatorConfigReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

const (
	ResyncPeriod = 2 * time.Minute
)

//+kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpoperatorconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpoperatorconfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpoperatorconfigs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PtpOperatorConfig object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.2/pkg/reconcile
func (r *PtpOperatorConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (reconcile.Result, error) {
	reqLogger := r.Log.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name)
	reqLogger.Info("Reconciling PtpOperatorConfig")

	// Fetch the PtpOperatorConfig instance
	defaultCfg := &ptpv1.PtpOperatorConfig{}
	err := r.Get(context.TODO(), types.NamespacedName{
		Name: names.DefaultOperatorConfigName, Namespace: names.Namespace}, defaultCfg)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			defaultCfg.SetNamespace(names.Namespace)
			defaultCfg.SetName(names.DefaultOperatorConfigName)
			defaultCfg.Spec = ptpv1.PtpOperatorConfigSpec{
				DaemonNodeSelector: map[string]string{},
			}
			if err = r.Create(context.TODO(), defaultCfg); err != nil {
				reqLogger.Error(err, "failed to create default ptp config",
					"Namespace", names.Namespace, "Name", names.DefaultOperatorConfigName)
				return reconcile.Result{}, err
			}
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	nodeList := &corev1.NodeList{}
	err = r.List(context.TODO(), nodeList, &client.ListOptions{})
	if err != nil {
		glog.Errorf("failed to list nodes")
		return reconcile.Result{}, err
	}

	if err = r.syncNodePtpDevice(nodeList); err != nil {
		glog.Errorf("failed to sync node ptp device: %v", err)
		return reconcile.Result{}, err
	}

	if err = r.createPTPConfigMap(defaultCfg); err != nil {
		glog.Errorf("failed to create ptp config map node: %v", err)
		return reconcile.Result{}, err
	}

	if err = r.syncLinuxptpDaemon(defaultCfg); err != nil {
		glog.Errorf("failed to sync linux ptp daemon: %v", err)
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: ResyncPeriod}, nil
}

// createPTPConfigMap creates PTP config map
func (r *PtpOperatorConfigReconciler) createPTPConfigMap(defaultCfg *ptpv1.PtpOperatorConfig) error {
	var err error

	cm := &corev1.ConfigMap{}
	err = r.Get(context.TODO(), types.NamespacedName{
		Namespace: names.Namespace, Name: names.DefaultPTPConfigMapName}, cm)
	if err != nil {
		if errors.IsNotFound(err) {
			cm.Name = names.DefaultPTPConfigMapName
			cm.Namespace = names.Namespace
			cm.Data = make(map[string]string)

			if err = controllerutil.SetControllerReference(defaultCfg, cm, r.Scheme); err != nil {
				return fmt.Errorf("failed to set owner reference: %v", err)
			}

			err = r.Create(context.TODO(), cm)
			if err != nil {
				return fmt.Errorf("failed to create ptp config map: %v", err)
			}
			glog.Infof("create ptp config map successfully")
		} else {
			return fmt.Errorf("failed to node ptp config map: %v", err)
		}
	}
	return nil
}

// setDaemonNodeSelector synchronizes Linuxptp DaemonSet
func (r *PtpOperatorConfigReconciler) setDaemonNodeSelector(
	defaultCfg *ptpv1.PtpOperatorConfig,
	obj *uns.Unstructured,
) (*uns.Unstructured, error) {
	var err error
	if obj.GetKind() == "DaemonSet" && len(defaultCfg.Spec.DaemonNodeSelector) > 0 {
		scheme := kscheme.Scheme
		ds := &appsv1.DaemonSet{}
		err = scheme.Convert(obj, ds, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to convert linuxptp obj to appsv1.DaemonSet: %v", err)
		}
		ds.Spec.Template.Spec.NodeSelector = defaultCfg.Spec.DaemonNodeSelector
		err = scheme.Convert(ds, obj, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to convert appsv1.DaemonSet to linuxptp obj: %v", err)
		}
	}
	return obj, nil
}

// syncLinuxptpDaemon synchronizes Linuxptp DaemonSet
func (r *PtpOperatorConfigReconciler) syncLinuxptpDaemon(defaultCfg *ptpv1.PtpOperatorConfig) error {
	var err error
	objs := []*uns.Unstructured{}

	data := render.MakeRenderData()
	data.Data["Image"] = os.Getenv("LINUXPTP_DAEMON_IMAGE")
	data.Data["Namespace"] = names.Namespace
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	data.Data["KubeRbacProxy"] = os.Getenv("KUBE_RBAC_PROXY_IMAGE")
	objs, err = render.RenderDir(filepath.Join(names.ManifestDir, "linuxptp"), &data)
	if err != nil {
		return fmt.Errorf("failed to render linuxptp daemon manifest: %v", err)
	}

	for _, obj := range objs {
		obj, err = r.setDaemonNodeSelector(defaultCfg, obj)
		if err != nil {
			return err
		}
		if err = controllerutil.SetControllerReference(defaultCfg, obj, r.Scheme); err != nil {
			return fmt.Errorf("failed to set owner reference: %v", err)
		}
		if err = apply.ApplyObject(context.TODO(), r.Client, obj); err != nil {
			return fmt.Errorf("failed to apply object %v with err: %v", obj, err)
		}
	}
	return nil
}

// syncNodePtpDevice synchronizes NodePtpDevice CR for each node
func (r *PtpOperatorConfigReconciler) syncNodePtpDevice(nodeList *corev1.NodeList) error {
	for _, node := range nodeList.Items {
		found := &ptpv1.NodePtpDevice{}
		err := r.Get(context.TODO(), types.NamespacedName{
			Namespace: names.Namespace, Name: node.Name}, found)
		if err != nil {
			if errors.IsNotFound(err) {
				ptpDev := &ptpv1.NodePtpDevice{}
				ptpDev.Name = node.Name
				ptpDev.Namespace = names.Namespace
				err = r.Create(context.TODO(), ptpDev)
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

func (r *PtpOperatorConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ptpv1.PtpOperatorConfig{}).
		Owns(&appsv1.DaemonSet{}).
		Complete(r)
}

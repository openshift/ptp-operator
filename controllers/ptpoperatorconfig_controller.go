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
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/golang/glog"
	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/apply"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/names"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/render"
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
	// IsInitialSync tracks if it is initial run of syncLinuxptpDaemon
	// and only retry checking amq status during the initial sync
	IsInitialSync       bool
	TransportHostStatus *EventTransportHostStatus
}

// EventTransportHostStatus ... identify transport host status ( for amq only)
type EventTransportHostStatus struct {
	TransportHostRetryCount int
	LastTransportHostValue  string
	Success                 bool
}

const (
	ResyncPeriod = 2 * time.Minute
	// AmqReadyStateError Event related transport protocol const
	AmqReadyStateError        = "AMQ not ready"
	TransportRetryMaxCount    = 12 // max 3 minutes
	RsyncTransportRetryPeriod = 10 * time.Second
	AmqScheme                 = "amqp"
	AmqDefaultHostName        = "nohup"
	AmqDefaultHost            = AmqScheme + "://" + AmqDefaultHostName
	DefaultTransportHost      = "http://ptp-event-publisher-service-NODE_NAME.openshift-ptp.svc.cluster.local:9043"
	DefaultStorageType        = "emptyDir"
	PVCNamePrefix             = "cloud-event-proxy-store"
)

//+kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpoperatorconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpoperatorconfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpoperatorconfigs/finalizers,verbs=update
//+kubebuilder:rbac:groups=config.openshift.io,resources=infrastructures,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;delete

func (r *PtpOperatorConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (reconcile.Result, error) {
	reqLogger := r.Log.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name)
	reqLogger.Info("Reconciling PtpOperatorConfig")

	// Fetch the PtpOperatorConfig instance
	defaultCfg := &ptpv1.PtpOperatorConfig{}
	err := r.Get(ctx, types.NamespacedName{
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
			if err = r.Create(ctx, defaultCfg); err != nil {
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
	err = r.List(ctx, nodeList, &client.ListOptions{})
	if err != nil {
		glog.Errorf("failed to list nodes")
		return reconcile.Result{}, err
	}

	if err = r.syncNodePtpDevice(ctx, nodeList); err != nil {
		glog.Errorf("failed to sync node ptp device: %v", err)
		return reconcile.Result{}, err
	}

	if err = r.createPTPConfigMap(ctx, defaultCfg); err != nil {
		glog.Errorf("failed to create ptp config map node: %v", err)
		return reconcile.Result{}, err
	}

	if err = r.syncLinuxptpDaemon(ctx, defaultCfg, nodeList); err != nil {
		glog.Errorf("failed to sync linux ptp daemon: %v", err)
		if err.Error() == AmqReadyStateError {
			return reconcile.Result{RequeueAfter: RsyncTransportRetryPeriod}, nil
		}
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: ResyncPeriod}, nil
}

// createPTPConfigMap creates PTP config map
func (r *PtpOperatorConfigReconciler) createPTPConfigMap(ctx context.Context, defaultCfg *ptpv1.PtpOperatorConfig) error {
	var err error

	cm := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{
		Namespace: names.Namespace, Name: names.DefaultPTPConfigMapName}, cm)
	if err != nil {
		if errors.IsNotFound(err) {
			cm.Name = names.DefaultPTPConfigMapName
			cm.Namespace = names.Namespace
			cm.Data = make(map[string]string)

			if err = controllerutil.SetControllerReference(defaultCfg, cm, r.Scheme); err != nil {
				return fmt.Errorf("failed to set owner reference: %v", err)
			}

			err = r.Create(ctx, cm)
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

// syncPvc update PersistentVolumeClaim
func (r *PtpOperatorConfigReconciler) syncPvc(ctx context.Context, obj *uns.Unstructured) (*uns.Unstructured, error) {
	var err error
	scheme := kscheme.Scheme
	pvc := &corev1.PersistentVolumeClaim{}
	err = scheme.Convert(obj, pvc, nil)

	if err != nil {
		return nil, fmt.Errorf("failed to convert obj to corev1.PersistentVolumeClaim: %v", err)
	}

	// update the VolumeName of PVC when the PVC is bound to a PV
	if pvcDeployed := r.getPvc(obj.GetName(), names.Namespace); pvcDeployed != nil {
		if pvcDeployed.Spec.VolumeName != pvc.Spec.VolumeName && pvc.Spec.VolumeName == "" {
			log.Printf("pvc %s is Bound, updating VolumeName to %s", obj.GetName(), pvcDeployed.Spec.VolumeName)
			pvc.Spec.VolumeName = pvcDeployed.Spec.VolumeName
		}
	}

	err = scheme.Convert(pvc, obj, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to convert corev1.PersistentVolumeClaim to obj: %v", err)
	}

	return obj, nil
}

// cleanupPvc clean up obsolete PVCs not mounted to current lixnuxptp pod
func (r *PtpOperatorConfigReconciler) cleanupPvc(ctx context.Context, storageType string) error {
	var err error
	pvcList := &corev1.PersistentVolumeClaimList{}
	opts := []client.ListOption{
		client.InNamespace(names.Namespace),
	}
	err = r.List(context.TODO(), pvcList, opts...)
	if err != nil {
		return err
	}

	pvcName := fmt.Sprintf("%s-%s", PVCNamePrefix, storageType)

	for _, p := range pvcList.Items {
		if strings.HasPrefix(p.ObjectMeta.Name, PVCNamePrefix) {
			if p.ObjectMeta.Name != pvcName || storageType == DefaultStorageType {
				if err := r.Client.Delete(ctx, &p); err != nil {
					log.Printf("fail to delete obsolete pvc %s err: %v", p.ObjectMeta.Name, err)
				} else {
					log.Printf("garbage collection: successfully deleted obsolete pvc %s", p.ObjectMeta.Name)
				}
			}
		}

	}
	return nil
}

// syncLinuxptpDaemon synchronizes Linuxptp DaemonSet
func (r *PtpOperatorConfigReconciler) syncLinuxptpDaemon(ctx context.Context, defaultCfg *ptpv1.PtpOperatorConfig, nodeList *corev1.NodeList) error {
	var err error
	objs := []*uns.Unstructured{}

	data := render.MakeRenderData()
	data.Data["Image"] = os.Getenv("LINUXPTP_DAEMON_IMAGE")
	data.Data["Namespace"] = names.Namespace
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	data.Data["KubeRbacProxy"] = os.Getenv("KUBE_RBAC_PROXY_IMAGE")
	data.Data["SideCar"] = os.Getenv("SIDECAR_EVENT_IMAGE")
	data.Data["NodeName"] = os.Getenv("NODE_NAME")
	data.Data["StorageType"] = DefaultStorageType
	// configure EventConfig
	if defaultCfg.Spec.EventConfig == nil {
		data.Data["EnableEventPublisher"] = false
	} else {
		data.Data["EnableEventPublisher"] = defaultCfg.Spec.EventConfig.EnableEventPublisher
		data.Data["EventTransportHost"] = defaultCfg.Spec.EventConfig.TransportHost

		if defaultCfg.Spec.EventConfig.EnableEventPublisher {
			transportHost, e := r.EventTransportHostAvailabilityCheck(defaultCfg.Spec.EventConfig.TransportHost)
			if e != nil {
				return e
			}
			data.Data["EventTransportHost"] = transportHost
			if defaultCfg.Spec.EventConfig.StorageType != "" {
				data.Data["StorageType"] = defaultCfg.Spec.EventConfig.StorageType
			}
		}
	}

	var pluginList []string

	if defaultCfg.Spec.EnabledPlugins != nil {
		for k := range *defaultCfg.Spec.EnabledPlugins {
			pluginList = append(pluginList, k)
		}
	} else {
		pluginList = []string{"e810"} // Enable e810 by default if plugins not specified
	}

	enabledPlugins := strings.Join(pluginList, ",")
	data.Data["EnabledPlugins"] = enabledPlugins
	if enabledPlugins != "" {
		glog.Infof("ptp operator enabled plugins: %s", enabledPlugins)
	}

	objs, err = render.RenderTemplate(filepath.Join(names.ManifestDir, "linuxptp/ptp-daemon.yaml"), &data)
	if err != nil {
		return fmt.Errorf("failed to render linuxptp daemon manifest: %v", err)
	}

	err = r.cleanupPvc(ctx, fmt.Sprintf("%s", data.Data["StorageType"]))
	if err != nil {
		return err
	}

	for _, obj := range objs {
		obj, err = r.setDaemonNodeSelector(defaultCfg, obj)
		if err != nil {
			return err
		}

		if obj.GetKind() == "PersistentVolumeClaim" {
			obj, err = r.syncPvc(ctx, obj)
			if err != nil {
				return err
			}
		}

		if err = controllerutil.SetControllerReference(defaultCfg, obj, r.Scheme); err != nil {
			return fmt.Errorf("failed to set owner reference for daemon: %v", err)
		}
		if err = apply.ApplyObject(ctx, r.Client, obj); err != nil {
			return fmt.Errorf("failed to apply object %v with err: %v", obj, err)
		}
	}

	if defaultCfg.Spec.EventConfig == nil {
		return nil
	}

	for _, node := range nodeList.Items {
		data.Data["NodeName"] = strings.Split(node.Name, ".")[0]
		objs, err = render.RenderTemplate(filepath.Join(names.ManifestDir, "linuxptp/event-service.yaml"), &data)
		if err != nil {
			return fmt.Errorf("failed to render event service manifest: %v", err)
		}
		for _, obj := range objs {
			if err = apply.ApplyObject(ctx, r.Client, obj); err != nil {
				return fmt.Errorf("failed to apply service object %v with err: %v", obj, err)
			}
		}
	}
	return nil
}

// syncNodePtpDevice synchronizes NodePtpDevice CR for each node
func (r *PtpOperatorConfigReconciler) syncNodePtpDevice(ctx context.Context, nodeList *corev1.NodeList) error {
	for _, node := range nodeList.Items {
		found := &ptpv1.NodePtpDevice{}
		err := r.Get(ctx, types.NamespacedName{
			Namespace: names.Namespace, Name: node.Name}, found)
		if err != nil {
			if errors.IsNotFound(err) {
				ptpDev := &ptpv1.NodePtpDevice{}
				ptpDev.Name = node.Name
				ptpDev.Namespace = names.Namespace
				err = r.Create(ctx, ptpDev)
				if err != nil {
					return fmt.Errorf("failed to create NodePtpDevice for node %v: %v", node.Name, err)
				}
				glog.Infof("create NodePtpDevice successfully for node: %v", node.Name)
			} else {
				return fmt.Errorf("failed to get NodePtpDevice for node %v: %v", node.Name, err)
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

func (r *PtpOperatorConfigReconciler) getAmqStatus(svcName string, namespace string) bool {

	podList := &corev1.PodList{}
	opts := []client.ListOption{
		client.InNamespace(namespace),
	}
	err := r.List(context.TODO(), podList, opts...)
	if err != nil {
		return false
	}
	for _, p := range podList.Items {
		for _, c := range p.Spec.Containers {
			if c.Name == svcName && p.Status.Phase == "Running" {
				return true
			}
		}

	}
	return false
}

// EventTransportHostAvailabilityCheck ... check availability for transporthost (for amq only )
func (r *PtpOperatorConfigReconciler) EventTransportHostAvailabilityCheck(transportHost string) (string, error) {
	var transportUrl *url.URL
	if transportHost == "" {
		glog.Warningf("ptp operator config Spec, ptpEventConfig.transportHost=%v is not valid, proceed as %s",
			transportHost, DefaultTransportHost)
		return DefaultTransportHost, nil
	}

	// if new transport has been applied reset everything
	r.TransportHostStatus.ResetOnChange(transportHost)

	// example transportHost: "amqp://amq-router.amq-router.svc.cluster.local"
	// if transportHost is "amqp://nohup", ignore any validation and print events to log
	transportUrl, err := url.Parse(transportHost)
	if err != nil {
		glog.Warningf("ptp operator config Spec, ptpEventConfig.transportHost=%v is not valid, proceed as %s",
			transportHost, DefaultTransportHost)
		return DefaultTransportHost, nil
	} else if r.TransportHostStatus.RetryThisHost(transportUrl.Scheme, transportUrl.Host) { // not set to nohup and last try was not success
		if r.TransportHostStatus.RetryOnCount() {
			if amq := strings.Split(transportUrl.Host, "."); len(amq) > 1 { // check for availability if its amq
				glog.Infof("check AMQP service %v in namespace %v before deploying linuxptp daemon (%d of %d attempts)", amq[0], amq[1],
					r.TransportHostStatus.TransportHostRetryCount+1, TransportRetryMaxCount)
				if !r.getAmqStatus(amq[0], amq[1]) {
					r.TransportHostStatus.Inc()
					return transportHost, fmt.Errorf("%s", AmqReadyStateError) // failed then try again
				}
			} // else continue as it is
		} else {
			glog.Infof("Max retry reached for amqp connection. Set transport host to %s", AmqDefaultHost)
			return AmqDefaultHost, nil
		}
	} // else not checking for amq://localhost:5672 or http
	r.TransportHostStatus.Exit()
	return transportHost, nil
}

func (r *PtpOperatorConfigReconciler) getPvc(pvcName string, namespace string) *corev1.PersistentVolumeClaim {

	pvcList := &corev1.PersistentVolumeClaimList{}
	opts := []client.ListOption{
		client.InNamespace(namespace),
	}
	err := r.List(context.TODO(), pvcList, opts...)
	if err != nil {
		return nil
	}
	for _, p := range pvcList.Items {
		if p.ObjectMeta.Name == pvcName {
			return &p
		}

	}
	return nil
}

// Inc count
func (e *EventTransportHostStatus) Inc() {
	e.TransportHostRetryCount++
	e.Success = false
}

// ResetOnChange count
func (e *EventTransportHostStatus) ResetOnChange(newHost string) {
	if e.LastTransportHostValue != newHost {
		e.TransportHostRetryCount = 0
		e.Success = false
		e.LastTransportHostValue = newHost
	}
}

// RetryOnCount connecting to transport host
func (e *EventTransportHostStatus) RetryOnCount() bool {
	if e.TransportHostRetryCount >= TransportRetryMaxCount {
		return false
	}
	return true
}

// Exit from retry
func (e *EventTransportHostStatus) Exit() {
	e.Success = true
	e.TransportHostRetryCount = 0
}

// RetryThisHost ... check if retry  needed for this host
func (e *EventTransportHostStatus) RetryThisHost(scheme, host string) bool {
	if scheme == AmqScheme &&
		host != AmqDefaultHostName &&
		!e.Success {
		return true
	}
	return false

}

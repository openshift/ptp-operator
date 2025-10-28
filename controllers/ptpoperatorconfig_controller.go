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
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
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
	ResyncPeriod         = 2 * time.Minute
	DefaultTransportHost = "http://ptp-event-publisher-service-{{.NodeName}}.openshift-ptp.svc.cluster.local:9043"
	DefaultStorageType   = "emptyDir"
	DefaultApiVersion    = "2.0"
)

// +kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpoperatorconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpoperatorconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpoperatorconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups=config.openshift.io,resources=infrastructures,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete

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

	if err = r.createLeapConfigMap(ctx, defaultCfg); err != nil {
		glog.Errorf("failed to create leap config map: %v", err)
		return reconcile.Result{}, err
	}

	if err = r.applyNetworkPoliciesFromYaml(ctx, filepath.Join(names.ManifestDir, "linuxptp/network-policy.yaml"), defaultCfg); err != nil {
		glog.Errorf("failed to apply NetworkPolicy %v", err)
		return reconcile.Result{}, err
	}

	if err = r.syncLinuxptpDaemon(ctx, defaultCfg, nodeList); err != nil {
		glog.Errorf("failed to sync linux ptp daemon: %v", err)
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: ResyncPeriod}, nil
}

// createLeapConfigMap creates an empty leap second config map
func (r *PtpOperatorConfigReconciler) createLeapConfigMap(ctx context.Context, defaultCfg *ptpv1.PtpOperatorConfig) error {
	var err error

	cm := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{
		Namespace: names.Namespace, Name: names.DefaultLeapConfigMapName}, cm)
	if err != nil {
		if errors.IsNotFound(err) {
			cm.Name = names.DefaultLeapConfigMapName
			cm.Namespace = names.Namespace
			cm.Data = make(map[string]string)

			if err = controllerutil.SetControllerReference(defaultCfg, cm, r.Scheme); err != nil {
				return fmt.Errorf("failed to set owner reference: %v", err)
			}
			err = r.Create(ctx, cm)
			if err != nil {
				return fmt.Errorf("failed to create leap config map: %v", err)
			}
			glog.Infof("leap config map created successfully")
		} else {
			return fmt.Errorf("failed to create leap config map: %v", err)
		}
	}
	return nil
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
	data.Data["EventApiVersion"] = DefaultApiVersion
	// Get cluster name from environment variable with fallback
	clusterName := os.Getenv("CLUSTER_NAME")
	if clusterName == "" {
		clusterName = "openshift.local" // Default fallback
	}
	data.Data["ClusterName"] = clusterName
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
			if defaultCfg.Spec.EventConfig.ApiVersion != data.Data["EventApiVersion"] {
				glog.Infof("Event API version is '%s', using version %s.",
					defaultCfg.Spec.EventConfig.ApiVersion,
					data.Data["EventApiVersion"])
			}
		}
	}

	var pluginList []string

	if defaultCfg.Spec.EnabledPlugins != nil {
		for k := range *defaultCfg.Spec.EnabledPlugins {
			pluginList = append(pluginList, k)
		}
	} else {
		pluginList = []string{"e810", "ntpfailover"} // Enable e810 by default if plugins not specified
	}
	sort.Strings(pluginList)
	enabledPlugins := strings.Join(pluginList, ",")
	data.Data["EnabledPlugins"] = enabledPlugins
	if enabledPlugins != "" {
		glog.Infof("ptp operator enabled plugins: %s", enabledPlugins)
	}

	objs, err = render.RenderTemplate(filepath.Join(names.ManifestDir, "linuxptp/ptp-daemon.yaml"), &data)
	if err != nil {
		return fmt.Errorf("failed to render linuxptp daemon manifest: %v", err)
	}

	// Process auth-config.yaml if event publisher is enabled
	if defaultCfg.Spec.EventConfig != nil && defaultCfg.Spec.EventConfig.EnableEventPublisher {
		authObjs, err := render.RenderTemplate(filepath.Join(names.ManifestDir, "linuxptp/auth-config.yaml"), &data)
		if err != nil {
			return fmt.Errorf("failed to render auth config manifest: %v", err)
		}
		objs = append(objs, authObjs...)
	}

	for _, obj := range objs {
		obj, err = r.setDaemonNodeSelector(defaultCfg, obj)
		if err != nil {
			return err
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

	if defaultCfg.Spec.EventConfig.EnableEventPublisher {
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
	}

	return nil
}

// applyEventNetworkPolicy applies the NetworkPolicy for cloud-event-proxy
// applyNetworkPolicyFromYAML reads and applies the NetworkPolicy YAML directly
// applyNetworkPoliciesFromYaml reads a multi-document YAML file and applies each NetworkPolicy
func (r *PtpOperatorConfigReconciler) applyNetworkPoliciesFromYaml(
	ctx context.Context,
	path string,
	defaultCfg *ptpv1.PtpOperatorConfig,
) error {
	glog.Infof("Applying network policies from YAML file: %s", path)

	// Read the YAML file
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read YAML file: %v", err)
	}

	// Split YAML into separate documents
	yamlDocs := strings.Split(string(data), "---")
	var jsonBytes []byte
	for _, doc := range yamlDocs {
		trimmedDoc := strings.TrimSpace(doc)
		if len(trimmedDoc) == 0 {
			continue
		}

		jsonBytes, err = k8syaml.ToJSON([]byte(trimmedDoc))
		if err != nil {
			return fmt.Errorf("failed to convert YAML to JSON: %v", err)
		}

		// Unmarshal into an Unstructured object
		np := &uns.Unstructured{}
		if err = json.Unmarshal(jsonBytes, np); err != nil {
			return fmt.Errorf("failed to unmarshal JSON into Unstructured: %v", err)
		}

		// Process only NetworkPolicy objects
		if np.GetKind() != "NetworkPolicy" {
			glog.Infof("Ignoring network policy from YAML document: %s", trimmedDoc)
			continue
		}

		// Convert unstructured to typed object
		var typedNP networkingv1.NetworkPolicy
		if err = runtime.DefaultUnstructuredConverter.FromUnstructured(np.Object, &typedNP); err != nil {
			glog.Errorf("Failed to convert to typed NetworkPolicy: %v", err)
			return fmt.Errorf("failed to convert to typed NetworkPolicy: %v", err)
		}

		// Check if the object already exists
		found := &networkingv1.NetworkPolicy{}
		err = r.Get(ctx, types.NamespacedName{
			Namespace: typedNP.Namespace,
			Name:      typedNP.Name,
		}, found)

		if err != nil {
			if errors.IsNotFound(err) {
				glog.Infof("NetworkPolicy %s/%s not found, creating it", typedNP.Namespace, typedNP.Name)

				if err = controllerutil.SetControllerReference(defaultCfg, &typedNP, r.Scheme); err != nil {
					glog.Errorf("Failed to set owner reference for NetworkPolicy: %v", err)
					return fmt.Errorf("failed to set owner reference: %v", err)
				}

				if err = r.Create(ctx, &typedNP); err != nil {
					glog.Errorf("Failed to create NetworkPolicy: %v", err)
					return fmt.Errorf("failed to create NetworkPolicy: %v", err)
				}
				glog.Infof("Successfully created NetworkPolicy: %s/%s", typedNP.Namespace, typedNP.Name)
			} else {
				glog.Errorf("Failed to get NetworkPolicy: %v", err)
				return fmt.Errorf("failed to get NetworkPolicy: %v", err)
			}
		} else {
			glog.Infof("NetworkPolicy %s/%s already exists. Skipping creation.", typedNP.Namespace, typedNP.Name)
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

// EventTransportHostAvailabilityCheck ... check availability for transporthost
func (r *PtpOperatorConfigReconciler) EventTransportHostAvailabilityCheck(transportHost string) (string, error) {
	if transportHost == "" {
		glog.Warningf("ptp operator config Spec, ptpEventConfig.transportHost=%v is not valid, proceed as %s",
			transportHost, DefaultTransportHost)
		return DefaultTransportHost, nil
	}

	_, err := url.Parse(transportHost)
	if err != nil {
		glog.Warningf("ptp operator config Spec, ptpEventConfig.transportHost=%v is not valid, proceed as %s",
			transportHost, DefaultTransportHost)
		return DefaultTransportHost, nil
	}
	return transportHost, nil
}

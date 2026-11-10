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
	configv1 "github.com/openshift/api/config/v1"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// PtpOperatorConfigReconciler reconciles a PtpOperatorConfig object
type PtpOperatorConfigReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
	// TLSProfileSpec is the cluster-wide TLS profile to apply to kube-rbac-proxy.
	// When nil, legacy hardcoded cipher suites are used (pre-TLS adherence behavior).
	TLSProfileSpec *configv1.TLSProfileSpec
}

func chronydNetworkPolicy() *networkingv1.NetworkPolicy {
	ports := []networkingv1.NetworkPolicyPort{}
	for _, protocol := range []corev1.Protocol{corev1.ProtocolUDP, corev1.ProtocolTCP} {
		p := protocol
		port := intstr.FromInt32(123)
		ports = append(ports, networkingv1.NetworkPolicyPort{Protocol: &p, Port: &port})
	}
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "13-linuxptp-daemon-egress-ntp-optional", Namespace: names.Namespace},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "linuxptp-daemon"}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Ingress:     []networkingv1.NetworkPolicyIngressRule{{Ports: ports}},
			Egress:      []networkingv1.NetworkPolicyEgressRule{{Ports: ports}},
		},
	}
}

func DefaultTransportHost() string {
	return "http://ptp-event-publisher-service-NODE_NAME." + names.Namespace + ".svc.cluster.local:9043"
}

const (
	ResyncPeriod       = 2 * time.Minute
	DefaultStorageType = "emptyDir"
	DefaultApiVersion  = "2.0"
)

// +kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpoperatorconfigs,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpoperatorconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups=config.openshift.io,resources=infrastructures,verbs=get;list;watch
// +kubebuilder:rbac:groups=config.openshift.io,resources=apiservers,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=create;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,resourceNames=linuxptp-daemon-auth-delegator,verbs=get;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,resourceNames="system:auth-delegator",verbs=bind

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
			// Owned objects are automatically garbage collected. The event-publisher
			// authentication resources, however, are not owned by the PtpOperatorConfig -
			// the ClusterRoleBinding is cluster-scoped (and grants system:auth-delegator
			// access) and the other objects carry no owner reference - so they would be
			// left orphaned. Tear them down explicitly before recreating the default
			// config so no stale auth resources or TokenReview access survive a delete.
			if err = r.teardownEventAuth(ctx); err != nil {
				reqLogger.Error(err, "failed to tear down event auth on config deletion")
				return reconcile.Result{}, err
			}
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
	data.Data["ImagePullPolicy"] = "IfNotPresent"
	if overridePolicy, ok := os.LookupEnv("IMAGE_PULL_POLICY"); ok {
		data.Data["ImagePullPolicy"] = overridePolicy
	}
	data.Data["Namespace"] = names.Namespace
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	data.Data["KubeRbacProxy"] = os.Getenv("KUBE_RBAC_PROXY_IMAGE")
	data.Data["SideCar"] = os.Getenv("SIDECAR_EVENT_IMAGE")
	data.Data["NodeName"] = os.Getenv("NODE_NAME")
	data.Data["StorageType"] = DefaultStorageType
	data.Data["EventApiVersion"] = DefaultApiVersion
	// EnableEventAuth turns on mTLS + TokenReview on the event publisher APIs. It
	// depends on the OpenShift Service CA operator to mint serving certificates
	// and inject the CA bundle, so it is opt-in (default off) to keep the
	// operator usable on clusters without Service CA. Secured deployments set
	// ENABLE_EVENT_AUTH=true explicitly.
	data.Data["EnableEventAuth"] = os.Getenv("ENABLE_EVENT_AUTH") == "true"
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

	enabledPlugins := enabledPluginNames(defaultCfg)
	data.Data["EnabledPlugins"] = enabledPlugins
	if enabledPlugins != "" {
		glog.Infof("ptp operator enabled plugins: %s", enabledPlugins)
	}

	r.setTLSTemplateData(&data)

	objs, err = render.RenderTemplate(filepath.Join(names.ManifestDir, "linuxptp/ptp-daemon.yaml"), &data)
	if err != nil {
		return fmt.Errorf("failed to render linuxptp daemon manifest: %v", err)
	}

	// Set context to indicate this update is from PtpOperatorConfig controller
	// This allows the merge logic to preserve security volumes from current DaemonSet
	ctxWithSource := context.WithValue(ctx, apply.ControllerSourceKey, apply.SourcePtpOperatorConfig)

	for _, obj := range objs {
		obj, err = r.setDaemonNodeSelector(defaultCfg, obj)
		if err != nil {
			return err
		}

		if err = controllerutil.SetControllerReference(defaultCfg, obj, r.Scheme); err != nil {
			return fmt.Errorf("failed to set owner reference for daemon: %v", err)
		}
		if err = apply.ApplyObject(ctxWithSource, r.Client, obj); err != nil {
			return fmt.Errorf("failed to apply object %v with err: %v", obj, err)
		}
	}

	// CEPv1 is deployed only when event publishing is enabled in 5.0. Reconcile
	// the shared auth resources once, and tear them down whenever either switch
	// is off so no TokenReview permission or serving-cert resources are orphaned.
	authEnabled := data.Data["EnableEventPublisher"] == true && data.Data["EnableEventAuth"] == true
	if err = r.syncEventAuth(ctx, &data, authEnabled); err != nil {
		return err
	}

	if defaultCfg.Spec.EventConfig != nil && defaultCfg.Spec.EventConfig.EnableEventPublisher {
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

func enabledPluginNames(defaultCfg *ptpv1.PtpOperatorConfig) string {
	var pluginList []string

	if defaultCfg.Spec.EnabledPlugins != nil {
		for name := range *defaultCfg.Spec.EnabledPlugins {
			pluginList = append(pluginList, name)
		}
	} else {
		pluginList = []string{"e810", "e825", "e830", "ntpfailover", "phc-first-step"}
	}

	sort.Strings(pluginList)
	return strings.Join(pluginList, ",")
}

// syncEventAuth reconciles the event-publisher authentication manifest (mTLS
// serving-cert Service, injected CA bundle, auth-config ConfigMap and the
// TokenReview ClusterRoleBinding). It renders the manifest unconditionally so
// that when authentication is disabled - either the publisher is off or auth
// was turned off - any resources left over from a previously-enabled state are
// torn down. Otherwise the cluster-scoped ClusterRoleBinding and the
// serving-cert Service would be orphaned after a user turns authentication off.
func (r *PtpOperatorConfigReconciler) syncEventAuth(ctx context.Context, data *render.RenderData, enabled bool) error {
	if !enabled {
		return r.teardownEventAuth(ctx)
	}
	authObjs, err := render.RenderTemplate(filepath.Join(names.ManifestDir, "linuxptp/auth-config.yaml"), data)
	if err != nil {
		return fmt.Errorf("failed to render event auth-config manifest: %v", err)
	}
	for _, obj := range authObjs {
		if err = apply.ApplyObject(ctx, r.Client, obj); err != nil {
			return fmt.Errorf("failed to apply auth-config object %v with err: %v", obj, err)
		}
	}
	if err = r.syncEventCABundle(ctx); err != nil {
		return err
	}
	return nil
}

// syncEventCABundle maintains the combined trust anchor the event publisher uses
// to verify mTLS peer certificates. The ptp-event-publisher-service-ca ConfigMap
// carries the service.beta.openshift.io/inject-cabundle annotation, so the
// OpenShift Service CA operator writes service-ca.crt into it (trusting the
// server certificate and any Service CA-signed client certs). A consumer that
// presents a client certificate signed by its own CA - e.g. an out-of-cluster
// test consumer - cannot use a Service CA-issued client cert (Service CA serving
// certs are serverAuth-only), so its CA must also be trusted. That CA is
// published out-of-band into the optional ptp-event-publisher-client-ca
// ConfigMap; this function folds it together with service-ca.crt into the
// operator-owned ca-bundle.crt key that cloud-event-proxy loads via caCertPath.
//
// The Service CA input and derived output deliberately use different ConfigMaps.
// This gives each controller exclusive ownership and prevents the Service CA
// injection from triggering an endless operator reconciliation loop.
func (r *PtpOperatorConfigReconciler) syncEventCABundle(ctx context.Context) error {
	serviceCABundle := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: names.Namespace, Name: names.EventPublisherServiceCAConfigMapName}, serviceCABundle); err != nil {
		if errors.IsNotFound(err) {
			// Created by the auth manifest earlier in the same reconcile; it is
			// populated on a later reconcile (the ConfigMap watch fires once the
			// Service CA operator injects service-ca.crt).
			return nil
		}
		return fmt.Errorf("failed to get event CA bundle configmap: %v", err)
	}

	serviceCA := strings.TrimSpace(serviceCABundle.Data[names.ServiceCAKey])
	if serviceCA == "" {
		// Service CA operator has not injected the CA yet; wait for the watch to
		// re-trigger reconciliation once service-ca.crt appears.
		glog.Infof("event CA bundle: waiting for Service CA injection of %q", names.ServiceCAKey)
		return nil
	}

	combined := serviceCA + "\n"
	bundle := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: names.Namespace, Name: names.EventPublisherCABundleConfigMapName}, bundle); err != nil {
		return fmt.Errorf("failed to get event CA bundle output configmap: %v", err)
	}

	// Fold in an optional externally-supplied client CA (e.g. a test consumer's
	// clientAuth CA). Absent -> mTLS still works for Service CA-signed clients.
	clientCA := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: names.Namespace, Name: names.EventPublisherClientCAConfigMapName}, clientCA)
	switch {
	case err == nil:
		for _, k := range sortedStringKeys(clientCA.Data) {
			v := strings.TrimSpace(clientCA.Data[k])
			if v == "" {
				continue
			}
			combined += v + "\n"
		}
	case errors.IsNotFound(err):
		// No extra client CA to trust.
	default:
		return fmt.Errorf("failed to get event client CA configmap: %v", err)
	}

	if bundle.Data[names.EventPublisherCABundleKey] == combined && bundle.Data[names.ServiceCAKey] == "" {
		return nil // already up to date
	}
	if bundle.Data == nil {
		bundle.Data = map[string]string{}
	}
	bundle.Data[names.EventPublisherCABundleKey] = combined
	// Remove data left by the pre-split layout during an upgrade. The Service CA
	// controller now owns only EventPublisherServiceCAConfigMapName.
	delete(bundle.Data, names.ServiceCAKey)
	if err := r.Update(ctx, bundle); err != nil {
		return fmt.Errorf("failed to update event CA bundle configmap: %v", err)
	}
	glog.Infof("event CA bundle %q updated (%d bytes)", names.EventPublisherCABundleKey, len(combined))
	return nil
}

// sortedStringKeys returns the keys of m in deterministic order so the derived
// CA bundle is stable across reconciles (avoids spurious updates).
func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// teardownEventAuth deletes every event-publisher authentication resource
// (mTLS serving-cert Service, injected CA bundle, auth-config ConfigMap and the
// TokenReview ClusterRoleBinding). It renders the auth manifest with a minimal
// render context - only the object names and namespace matter for a
// delete-by-name - so it can be called even when no PtpOperatorConfig exists
// (e.g. the config was just deleted). Deletion is idempotent: NotFound is
// ignored. This guarantees the cluster-scoped ClusterRoleBinding granting
// system:auth-delegator and the serving-cert Service are never left orphaned,
// whether authentication is reconciled off or the config itself is removed.
func (r *PtpOperatorConfigReconciler) teardownEventAuth(ctx context.Context) error {
	data := render.MakeRenderData()
	data.Data["Namespace"] = names.Namespace
	// The TLS fields only affect ConfigMap payloads, which are irrelevant for a
	// delete-by-name, but must be present so template rendering succeeds.
	r.setTLSTemplateData(&data)
	authObjs, err := render.RenderTemplate(filepath.Join(names.ManifestDir, "linuxptp/auth-config.yaml"), &data)
	if err != nil {
		return fmt.Errorf("failed to render event auth-config manifest: %v", err)
	}
	for _, obj := range authObjs {
		if err = r.Delete(ctx, obj); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete auth-config object %s/%s with err: %v",
				obj.GetKind(), obj.GetName(), err)
		}
	}
	return nil
}

// applyNetworkPoliciesFromYaml reconciles the operand and operator NetworkPolicy
// objects declared in the multi-document YAML at path. The host-networked daemon
// is not restricted by the daemon policies (06-13); the normal-networked
// operator-pod policies (01-05) are enforced by OVN-Kubernetes.
func (r *PtpOperatorConfigReconciler) applyNetworkPoliciesFromYaml(
	ctx context.Context,
	path string,
	defaultCfg *ptpv1.PtpOperatorConfig,
) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read operand policies: %w", err)
	}

	// The API server egress rules are port-based (TCP 6443) with no peer, because
	// the API server host IP is not known ahead of time. A ClusterIP-based rule is
	// not reliable: in-cluster clients reach the API server through a path where
	// the policy sees the backend/host address on the target port 6443.
	desired := make(map[string]*networkingv1.NetworkPolicy)
	for _, doc := range strings.Split(string(data), "---") {
		trimmedDoc := strings.TrimSpace(doc)
		if trimmedDoc == "" {
			continue
		}
		jsonBytes, err := k8syaml.ToJSON([]byte(trimmedDoc))
		if err != nil {
			return fmt.Errorf("decode operand policy YAML: %w", err)
		}
		np := &networkingv1.NetworkPolicy{}
		if err := json.Unmarshal(jsonBytes, np); err != nil {
			return fmt.Errorf("decode operand policy: %w", err)
		}
		switch np.Name {
		case "06-linuxptp-daemon-default-deny", "07-linuxptp-daemon-egress-dns",
			"08-linuxptp-daemon-egress-api-server", "09-linuxptp-daemon-ptp-traffic",
			"11-linuxptp-daemon-ingress-metrics",
			"01-ptp-operator-default-deny", "02-ptp-operator-ingress-webhook",
			"03-ptp-operator-ingress-metrics", "04-ptp-operator-egress-dns",
			"05-ptp-operator-egress-api-server":
		default:
			return fmt.Errorf("unexpected operand policy %q", np.Name)
		}
		np.Namespace = names.Namespace
		desired[np.Name] = np
	}

	configs := &ptpv1.PtpConfigList{}
	if err := r.List(ctx, configs, client.InNamespace(names.Namespace)); err != nil {
		return fmt.Errorf("list PtpConfigs for chronyd policy: %w", err)
	}
	for _, config := range configs.Items {
		for _, profile := range config.Spec.Profile {
			if profile.ChronydConf != nil || profile.ChronydOpts != nil {
				desired["13-linuxptp-daemon-egress-ntp-optional"] = chronydNetworkPolicy()
			}
		}
	}

	// Create allows before the default-deny declarations. Operator-pod policies
	// are created before daemon policies because the operator pod is enforced.
	for _, name := range []string{
		"02-ptp-operator-ingress-webhook", "03-ptp-operator-ingress-metrics",
		"04-ptp-operator-egress-dns", "05-ptp-operator-egress-api-server",
		"07-linuxptp-daemon-egress-dns", "08-linuxptp-daemon-egress-api-server",
		"09-linuxptp-daemon-ptp-traffic", "11-linuxptp-daemon-ingress-metrics",
		"13-linuxptp-daemon-egress-ntp-optional",
		"01-ptp-operator-default-deny", "06-linuxptp-daemon-default-deny",
	} {
		np, ok := desired[name]
		if !ok {
			continue
		}
		if err := controllerutil.SetControllerReference(defaultCfg, np, r.Scheme); err != nil {
			return fmt.Errorf("set policy %s owner: %w", name, err)
		}
		current := &networkingv1.NetworkPolicy{}
		err := r.Get(ctx, types.NamespacedName{Namespace: names.Namespace, Name: name}, current)
		if errors.IsNotFound(err) {
			if err := r.Create(ctx, np); err != nil {
				return fmt.Errorf("create policy %s: %w", name, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("get policy %s: %w", name, err)
		}
		if !metav1.IsControlledBy(current, defaultCfg) {
			return fmt.Errorf("policy %s exists but is not owned by PtpOperatorConfig", name)
		}
		if !equality.Semantic.DeepEqual(current.Spec, np.Spec) {
			current.Spec = np.Spec
			if err := r.Update(ctx, current); err != nil {
				return fmt.Errorf("update policy %s: %w", name, err)
			}
		}
	}
	policies := &networkingv1.NetworkPolicyList{}
	if err := r.List(ctx, policies, client.InNamespace(names.Namespace)); err != nil {
		return fmt.Errorf("list managed policies: %w", err)
	}
	for i := range policies.Items {
		policy := &policies.Items[i]
		if !metav1.IsControlledBy(policy, defaultCfg) {
			continue
		}
		if _, ok := desired[policy.Name]; ok {
			continue
		}
		// Renamed or stale owned policies are removed. The historical unnumbered
		// operator-pod policies are renamed to the numbered 01-05 scheme here.
		switch policy.Name {
		case "06-linuxptp-daemon-default-deny", "07-linuxptp-daemon-egress-dns",
			"08-linuxptp-daemon-egress-api-server", "09-linuxptp-daemon-ptp-traffic",
			"11-linuxptp-daemon-ingress-metrics", "12-operator-to-daemon-management",
			"13-linuxptp-daemon-egress-ntp-optional",
			"01-ptp-operator-default-deny", "02-ptp-operator-ingress-webhook",
			"03-ptp-operator-ingress-metrics", "04-ptp-operator-egress-dns",
			"05-ptp-operator-egress-api-server",
			"allow-webhook-traffic", "allow-egress-to-api-server", "allow-metrics-traffic":
			if err := r.Delete(ctx, policy, client.Preconditions{
				UID:             &policy.UID,
				ResourceVersion: &policy.ResourceVersion,
			}); err != nil && !errors.IsNotFound(err) {
				return fmt.Errorf("delete stale policy %s: %w", policy.Name, err)
			}
		default:
			// Unrelated policies, and historical objects such as the namespace-wide
			// default-deny-all, are never adopted or deleted; leave them for a
			// separately validated migration.
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

// legacyCipherSuites are the hardcoded cipher suites used by kube-rbac-proxy
// before cluster TLS profile support was added (PR #189).
const legacyCipherSuites = "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256," +
	"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256," +
	"TLS_RSA_WITH_AES_128_CBC_SHA256," +
	"TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256," +
	"TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256"

// setTLSTemplateData populates TLS settings for the kube-rbac-proxy container
// in the daemon DaemonSet template. When TLSProfileSpec is set (strict adherence),
// the cluster-wide TLS profile is used. When nil (legacy), the hardcoded cipher
// suites from before cluster TLS profile support (PR #189) are used.
func (r *PtpOperatorConfigReconciler) setTLSTemplateData(data *render.RenderData) {
	if r.TLSProfileSpec != nil {
		ianaCiphers := libgocrypto.OpenSSLToIANACipherSuites(r.TLSProfileSpec.Ciphers)
		data.Data["TLSMinVersion"] = string(r.TLSProfileSpec.MinTLSVersion)
		data.Data["TLSCipherSuites"] = strings.Join(ianaCiphers, ",")
		data.Data["TLSCipherSuitesJSON"] = ciphersToJSONArray(ianaCiphers)
		// TODO: pass TLSGroups to kube-rbac-proxy once it supports --tls-curve-preferences
		// (upstream: https://github.com/kube-rbac-proxy/kube-rbac-proxy/issues/414)
	} else {
		data.Data["TLSMinVersion"] = ""
		data.Data["TLSCipherSuites"] = legacyCipherSuites
		data.Data["TLSCipherSuitesJSON"] = ciphersToJSONArray(strings.Split(legacyCipherSuites, ","))
	}
}

// ciphersToJSONArray renders IANA cipher-suite names as a JSON array literal for
// embedding in the cloud-event-proxy auth-config template.
func ciphersToJSONArray(ciphers []string) string {
	quoted := make([]string, 0, len(ciphers))
	for _, c := range ciphers {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		quoted = append(quoted, fmt.Sprintf("%q", c))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func (r *PtpOperatorConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ptpv1.PtpOperatorConfig{}).
		Owns(&appsv1.DaemonSet{}).
		// The event-publisher CA bundle is assembled from input ConfigMaps the
		// operator does not own - the Service CA operator injects service-ca.crt
		// and an optional client CA is published out-of-band - so watch them to
		// rebuild the bundle promptly instead of waiting for the resync period.
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.mapCAConfigMapToRequest)).
		Complete(r)
}

// mapCAConfigMapToRequest enqueues a reconcile of the default PtpOperatorConfig
// when one of the CA-bundle input ConfigMaps changes.
func (r *PtpOperatorConfigReconciler) mapCAConfigMapToRequest(ctx context.Context, obj client.Object) []reconcile.Request {
	if obj.GetNamespace() != names.Namespace {
		return nil
	}
	switch obj.GetName() {
	case names.EventPublisherServiceCAConfigMapName, names.EventPublisherClientCAConfigMapName:
		return []reconcile.Request{{NamespacedName: types.NamespacedName{
			Namespace: names.Namespace, Name: names.DefaultOperatorConfigName}}}
	}
	return nil
}

// EventTransportHostAvailabilityCheck ... check availability for transporthost
func (r *PtpOperatorConfigReconciler) EventTransportHostAvailabilityCheck(transportHost string) (string, error) {
	if transportHost == "" {
		glog.Warningf("ptp operator config Spec, ptpEventConfig.transportHost=%v is not valid, proceed as %s",
			transportHost, DefaultTransportHost())
		return DefaultTransportHost(), nil
	}

	_, err := url.Parse(transportHost)
	if err != nil {
		glog.Warningf("ptp operator config Spec, ptpEventConfig.transportHost=%v is not valid, proceed as %s",
			transportHost, DefaultTransportHost())
		return DefaultTransportHost(), nil
	}
	return transportHost, nil
}

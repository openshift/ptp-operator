package controllers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/names"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/render"
	configv1 "github.com/openshift/api/config/v1"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// authTestNamespace matches makeTestRenderData's Namespace.
const authTestNamespace = "openshift-ptp"

// newAuthTestReconciler builds a reconciler backed by a fake client preloaded
// with the given objects.
func newAuthTestReconciler(t *testing.T, objs ...client.Object) *PtpOperatorConfigReconciler {
	t.Helper()
	// syncEventAuth renders via names.ManifestDir ("./bindata"), which is
	// relative to the repo root; tests run from ./controllers.
	t.Chdir("..")
	scheme := runtime.NewScheme()
	assert.NoError(t, clientgoscheme.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return &PtpOperatorConfigReconciler{Client: c, Scheme: scheme}
}

// authRenderData returns render data with the fields the auth-config manifest
// needs to render.
func authRenderData() *render.RenderData {
	data := makeTestRenderData()
	data.Data["TLSMinVersion"] = ""
	data.Data["TLSCipherSuitesJSON"] = "[]"
	return data
}

// authResourceExists reports whether the named object is present in the client.
func authResourceExists(t *testing.T, r *PtpOperatorConfigReconciler, obj client.Object, name, namespace string) bool {
	t.Helper()
	err := r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, obj)
	if err == nil {
		return true
	}
	if apierrors.IsNotFound(err) {
		return false
	}
	t.Fatalf("unexpected error getting %s/%s: %v", namespace, name, err)
	return false
}

// TestSyncEventAuthDisabledDeletesResources verifies that when authentication is
// disabled the previously-created auth resources - including the cluster-scoped
// ClusterRoleBinding - are torn down rather than orphaned.
func TestSyncEventAuthDisabledDeletesResources(t *testing.T) {
	seed := []client.Object{
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "ptp-event-publisher-service-ca", Namespace: authTestNamespace}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "ptp-event-publisher-ca-bundle", Namespace: authTestNamespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "ptp-event-publisher-server", Namespace: authTestNamespace}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "ptp-event-publisher-auth", Namespace: authTestNamespace}},
		&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "linuxptp-daemon-auth-delegator"}},
	}
	r := newAuthTestReconciler(t, seed...)

	assert.NoError(t, r.syncEventAuth(context.Background(), authRenderData(), false))

	assert.False(t, authResourceExists(t, r, &corev1.ConfigMap{}, "ptp-event-publisher-service-ca", authTestNamespace))
	assert.False(t, authResourceExists(t, r, &corev1.ConfigMap{}, "ptp-event-publisher-ca-bundle", authTestNamespace))
	assert.False(t, authResourceExists(t, r, &corev1.Service{}, "ptp-event-publisher-server", authTestNamespace))
	assert.False(t, authResourceExists(t, r, &corev1.ConfigMap{}, "ptp-event-publisher-auth", authTestNamespace))
	assert.False(t, authResourceExists(t, r, &rbacv1.ClusterRoleBinding{}, "linuxptp-daemon-auth-delegator", ""))

	// Deleting again when nothing is present must be a no-op (NotFound tolerated).
	assert.NoError(t, r.syncEventAuth(context.Background(), authRenderData(), false))
}

// TestSyncEventAuthEnabledCreatesResources verifies the enabled path creates the
// full set of auth resources.
func TestSyncEventAuthEnabledCreatesResources(t *testing.T) {
	r := newAuthTestReconciler(t)

	assert.NoError(t, r.syncEventAuth(context.Background(), authRenderData(), true))

	assert.True(t, authResourceExists(t, r, &corev1.ConfigMap{}, "ptp-event-publisher-service-ca", authTestNamespace))
	assert.True(t, authResourceExists(t, r, &corev1.ConfigMap{}, "ptp-event-publisher-ca-bundle", authTestNamespace))
	assert.True(t, authResourceExists(t, r, &corev1.Service{}, "ptp-event-publisher-server", authTestNamespace))
	assert.True(t, authResourceExists(t, r, &corev1.ConfigMap{}, "ptp-event-publisher-auth", authTestNamespace))
	assert.True(t, authResourceExists(t, r, &rbacv1.ClusterRoleBinding{}, "linuxptp-daemon-auth-delegator", ""))
}

// seedAuthResources returns the full set of event-auth objects as they exist
// once authentication has been enabled.
func seedAuthResources() []client.Object {
	return []client.Object{
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "ptp-event-publisher-service-ca", Namespace: authTestNamespace}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "ptp-event-publisher-ca-bundle", Namespace: authTestNamespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "ptp-event-publisher-server", Namespace: authTestNamespace}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "ptp-event-publisher-auth", Namespace: authTestNamespace}},
		&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "linuxptp-daemon-auth-delegator"}},
	}
}

// assertAuthResourcesGone asserts none of the event-auth objects remain.
func assertAuthResourcesGone(t *testing.T, r *PtpOperatorConfigReconciler) {
	t.Helper()
	assert.False(t, authResourceExists(t, r, &corev1.ConfigMap{}, "ptp-event-publisher-service-ca", authTestNamespace))
	assert.False(t, authResourceExists(t, r, &corev1.ConfigMap{}, "ptp-event-publisher-ca-bundle", authTestNamespace))
	assert.False(t, authResourceExists(t, r, &corev1.Service{}, "ptp-event-publisher-server", authTestNamespace))
	assert.False(t, authResourceExists(t, r, &corev1.ConfigMap{}, "ptp-event-publisher-auth", authTestNamespace))
	assert.False(t, authResourceExists(t, r, &rbacv1.ClusterRoleBinding{}, "linuxptp-daemon-auth-delegator", ""))
}

// TestTeardownEventAuthRemovesResources verifies the shared teardown helper
// deletes every auth resource and is idempotent when they are already gone.
func TestTeardownEventAuthRemovesResources(t *testing.T) {
	r := newAuthTestReconciler(t, seedAuthResources()...)

	assert.NoError(t, r.teardownEventAuth(context.Background()))
	assertAuthResourcesGone(t, r)

	// Idempotent: a second teardown with nothing present must not error.
	assert.NoError(t, r.teardownEventAuth(context.Background()))
}

// TestReconcileDeletedConfigTearsDownAuth is the delete/recreate regression:
// when the PtpOperatorConfig is deleted, Reconcile hits the NotFound path,
// which must tear down the orphan-prone auth resources (notably the
// cluster-scoped auth-delegator ClusterRoleBinding) before recreating the
// default config. Without the explicit teardown these would survive the delete.
func TestReconcileDeletedConfigTearsDownAuth(t *testing.T) {
	scheme := runtime.NewScheme()
	assert.NoError(t, clientgoscheme.AddToScheme(scheme))
	assert.NoError(t, ptpv1.AddToScheme(scheme))

	// syncEventAuth/teardownEventAuth render from names.ManifestDir ("./bindata"),
	// which is relative to the repo root; tests run from ./controllers.
	t.Chdir("..")

	// Seed the auth resources but NOT the PtpOperatorConfig, simulating a config
	// that has just been deleted.
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(seedAuthResources()...).Build()
	r := &PtpOperatorConfigReconciler{Client: c, Scheme: scheme, Log: logr.Discard()}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: names.DefaultOperatorConfigName, Namespace: names.Namespace},
	})
	assert.NoError(t, err)

	// Auth resources must be gone...
	assertAuthResourcesGone(t, r)

	// ...and the default config must have been recreated.
	assert.True(t, authResourceExists(t, r, &ptpv1.PtpOperatorConfig{}, names.DefaultOperatorConfigName, names.Namespace))
}

// getConfigMapData returns the .data of the named ConfigMap.
func getConfigMapData(t *testing.T, r *PtpOperatorConfigReconciler, name string) map[string]string {
	t.Helper()
	cm := &corev1.ConfigMap{}
	assert.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: authTestNamespace}, cm))
	return cm.Data
}

// TestSyncEventCABundleCombinesCAs verifies the operator folds the injected
// Service CA and an out-of-band client CA into the operator-owned ca-bundle.crt
// key, which is what removes the scale-operator-to-0 workaround.
func TestSyncEventCABundleCombinesCAs(t *testing.T) {
	r := newAuthTestReconciler(t,
		// Service CA input after injection.
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: names.EventPublisherServiceCAConfigMapName, Namespace: authTestNamespace},
			Data:       map[string]string{names.ServiceCAKey: "SERVICE-CA-PEM"},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: names.EventPublisherCABundleConfigMapName, Namespace: authTestNamespace},
			Data:       map[string]string{names.ServiceCAKey: "STALE-PRE-SPLIT-CA"},
		},
		// Out-of-band client CA published by e.g. make deploy-consumer.
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: names.EventPublisherClientCAConfigMapName, Namespace: authTestNamespace},
			Data:       map[string]string{"client-ca.crt": "CLIENT-CA-PEM"},
		},
	)

	assert.NoError(t, r.syncEventCABundle(context.Background()))

	data := getConfigMapData(t, r, names.EventPublisherCABundleConfigMapName)
	assert.Equal(t, "SERVICE-CA-PEM\nCLIENT-CA-PEM\n", data[names.EventPublisherCABundleKey])
	assert.NotContains(t, data, names.ServiceCAKey, "legacy injected key must be removed from the output")
	assert.Equal(t, map[string]string{names.ServiceCAKey: "SERVICE-CA-PEM"},
		getConfigMapData(t, r, names.EventPublisherServiceCAConfigMapName))
}

// TestSyncEventCABundleServiceCAOnly verifies that without a client CA the bundle
// is just the injected Service CA (mTLS still works for Service CA-signed peers).
func TestSyncEventCABundleServiceCAOnly(t *testing.T) {
	r := newAuthTestReconciler(t,
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: names.EventPublisherServiceCAConfigMapName, Namespace: authTestNamespace},
			Data:       map[string]string{names.ServiceCAKey: "SERVICE-CA-PEM"},
		},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: names.EventPublisherCABundleConfigMapName, Namespace: authTestNamespace}},
	)

	assert.NoError(t, r.syncEventCABundle(context.Background()))

	data := getConfigMapData(t, r, names.EventPublisherCABundleConfigMapName)
	assert.Equal(t, "SERVICE-CA-PEM\n", data[names.EventPublisherCABundleKey])
}

// TestSyncEventCABundleWaitsForServiceCA verifies the bundle is not written until
// the Service CA operator has injected service-ca.crt (avoids an empty trust file
// that would make cloud-event-proxy reject all clients).
func TestSyncEventCABundleWaitsForServiceCA(t *testing.T) {
	r := newAuthTestReconciler(t,
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: names.EventPublisherServiceCAConfigMapName, Namespace: authTestNamespace},
		},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: names.EventPublisherCABundleConfigMapName, Namespace: authTestNamespace}},
	)

	assert.NoError(t, r.syncEventCABundle(context.Background()))

	data := getConfigMapData(t, r, names.EventPublisherCABundleConfigMapName)
	_, present := data[names.EventPublisherCABundleKey]
	assert.False(t, present, "ca-bundle.crt must not be written before Service CA injection")
}

// TestMapCAConfigMapToRequest watches only external inputs. Watching the
// operator-owned output would make every bundle update enqueue itself forever.
func TestMapCAConfigMapToRequest(t *testing.T) {
	r := &PtpOperatorConfigReconciler{}

	for _, name := range []string{
		names.EventPublisherServiceCAConfigMapName,
		names.EventPublisherClientCAConfigMapName,
	} {
		requests := r.mapCAConfigMapToRequest(context.Background(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: names.Namespace},
		})
		assert.Len(t, requests, 1, "input ConfigMap %s must trigger reconciliation", name)
	}

	requests := r.mapCAConfigMapToRequest(context.Background(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: names.EventPublisherCABundleConfigMapName, Namespace: names.Namespace},
	})
	assert.Empty(t, requests, "operator-owned output must not trigger reconciliation")
}

// daemonSidecarArgs returns the args of the cloud-event-proxy container from a
// rendered ptp-daemon.yaml DaemonSet object.
func daemonSidecarArgs(t *testing.T, objs []*unstructured.Unstructured) []string {
	t.Helper()
	for _, obj := range objs {
		if obj.GetKind() != "DaemonSet" {
			continue
		}
		containers, found, err := unstructuredContainers(obj.Object)
		assert.NoError(t, err)
		assert.True(t, found)
		for _, c := range containers {
			container := c.(map[string]interface{})
			if container["name"] == "cloud-event-proxy" {
				var args []string
				if raw, ok := container["args"].([]interface{}); ok {
					for _, a := range raw {
						args = append(args, a.(string))
					}
				}
				return args
			}
		}
	}
	return nil
}

// TestEventAuthDisabled verifies the sidecar carries no auth wiring when
// ENABLE_EVENT_AUTH is off, so the operator stays usable without Service CA.
func TestEventAuthDisabled(t *testing.T) {
	data := makeTestRenderData()
	data.Data["EnableEventPublisher"] = true
	data.Data["SideCarV2"] = ""
	data.Data["EventTransportHost"] = "http://ptp-event-publisher-service-NODE_NAME.openshift-ptp.svc.cluster.local:9043"
	data.Data["TLSMinVersion"] = ""
	data.Data["TLSCipherSuites"] = ""
	data.Data["EnableEventAuth"] = false

	objs, err := render.RenderTemplate("../bindata/linuxptp/ptp-daemon.yaml", data)
	assert.NoError(t, err)
	args := daemonSidecarArgs(t, objs)
	for _, a := range args {
		assert.NotContains(t, a, "--auth-config", "auth-config must not be set when auth disabled")
	}
}

// TestEventAuthRequiresPublisher verifies the 5.0 CEPv1 gate: enabling the
// operator environment switch alone must not add auth volumes or deploy the
// cloud-event-proxy when EventConfig does not enable the publisher.
func TestEventAuthRequiresPublisher(t *testing.T) {
	data := makeTestRenderData()
	data.Data["EnableEventPublisher"] = false
	data.Data["EnableEventAuth"] = true
	data.Data["TLSMinVersion"] = ""
	data.Data["TLSCipherSuites"] = ""

	objs, err := render.RenderTemplate("../bindata/linuxptp/ptp-daemon.yaml", data)
	assert.NoError(t, err)
	assert.Nil(t, daemonSidecarArgs(t, objs), "cloud-event-proxy must remain disabled")
	assert.Nil(t, daemonVolume(t, objs, "server-certs"), "auth volumes must remain disabled")
	assert.Nil(t, daemonVolume(t, objs, "auth-config"), "auth config must remain disabled")
}

// TestEventAuthEnabled verifies the sidecar gets the auth-config flag and the
// auth manifest renders the expected resources with a valid, TLS-profiled JSON.
func TestEventAuthEnabled(t *testing.T) {
	profile := *configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	ianaCiphers := libgocrypto.OpenSSLToIANACipherSuites(profile.Ciphers)

	data := makeTestRenderData()
	data.Data["Namespace"] = "custom-ns"
	data.Data["EnableEventPublisher"] = true
	data.Data["SideCarV2"] = ""
	data.Data["EventTransportHost"] = "http://ptp-event-publisher-service-NODE_NAME.custom-ns.svc.cluster.local:9043"
	data.Data["TLSMinVersion"] = string(profile.MinTLSVersion)
	data.Data["TLSCipherSuites"] = strings.Join(ianaCiphers, ",")
	data.Data["TLSCipherSuitesJSON"] = ciphersToJSONArray(ianaCiphers)
	data.Data["EnableEventAuth"] = true

	// Sidecar must reference the auth config.
	objs, err := render.RenderTemplate("../bindata/linuxptp/ptp-daemon.yaml", data)
	assert.NoError(t, err)
	args := daemonSidecarArgs(t, objs)
	assert.Contains(t, args, "--auth-config=/etc/cloud-event-proxy/auth/config.json")

	// Auth manifest must produce the CA bundle, serving-cert service, auth
	// ConfigMap and the auth-delegator ClusterRoleBinding.
	authObjs, err := render.RenderTemplate("../bindata/linuxptp/auth-config.yaml", data)
	assert.NoError(t, err)
	kinds := map[string]bool{}
	var cfgJSON string
	for _, o := range authObjs {
		kinds[o.GetKind()+"/"+o.GetName()] = true
		if o.GetKind() == "ConfigMap" && o.GetName() == "ptp-event-publisher-auth" {
			d, ok := o.Object["data"].(map[string]interface{})
			assert.True(t, ok)
			cfgJSON = d["config.json"].(string)
		}
	}
	assert.True(t, kinds["ConfigMap/ptp-event-publisher-ca-bundle"])
	assert.True(t, kinds["ConfigMap/ptp-event-publisher-service-ca"])
	assert.True(t, kinds["Service/ptp-event-publisher-server"])
	assert.True(t, kinds["ConfigMap/ptp-event-publisher-auth"])
	assert.True(t, kinds["ClusterRoleBinding/linuxptp-daemon-auth-delegator"])

	for _, o := range authObjs {
		if o.GetKind() == "ConfigMap" && o.GetName() == names.EventPublisherCABundleConfigMapName {
			assert.Equal(t, "false", o.GetAnnotations()["service.beta.openshift.io/inject-cabundle"])
		}
	}

	// config.json must be valid and carry the cluster TLS profile.
	var cfg map[string]interface{}
	assert.NoError(t, json.Unmarshal([]byte(cfgJSON), &cfg))
	assert.Equal(t, true, cfg["enableMTLS"])
	assert.Equal(t, true, cfg["enableOAuth"])
	assert.Equal(t, string(profile.MinTLSVersion), cfg["tlsMinVersion"])
	suites, ok := cfg["tlsCipherSuites"].([]interface{})
	assert.True(t, ok)
	assert.NotEmpty(t, suites)

	// The outbound push leg must be wired for client auth: the config must point
	// at the clientAuth keypair and the ServiceAccount token.
	assert.Equal(t, "/etc/cloud-event-proxy/client-certs/tls.crt", cfg["clientCertPath"])
	assert.Equal(t, "/etc/cloud-event-proxy/client-certs/tls.key", cfg["clientKeyPath"])
	assert.Equal(t, "/var/run/secrets/kubernetes.io/serviceaccount/token", cfg["serviceAccountToken"])

	// The daemonset must mount the clientAuth cert optionally so the daemon still
	// starts when the out-of-band ptp-event-publisher-client-tls secret is absent.
	vol := daemonVolume(t, objs, "client-certs")
	assert.NotNil(t, vol, "client-certs volume must be rendered")
	secret, ok := vol["secret"].(map[string]interface{})
	assert.True(t, ok, "client-certs volume must be backed by a secret")
	assert.Equal(t, "ptp-event-publisher-client-tls", secret["secretName"])
	assert.Equal(t, true, secret["optional"], "client-certs secret mount must be optional")
}

// daemonVolume returns the named volume map from the rendered DaemonSet, or nil.
func daemonVolume(t *testing.T, objs []*unstructured.Unstructured, name string) map[string]interface{} {
	t.Helper()
	for _, obj := range objs {
		if obj.GetKind() != "DaemonSet" {
			continue
		}
		vols, found, err := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "volumes")
		assert.NoError(t, err)
		assert.True(t, found)
		for _, v := range vols {
			vol := v.(map[string]interface{})
			if vol["name"] == name {
				return vol
			}
		}
	}
	return nil
}

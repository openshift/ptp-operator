package controllers

import (
	"context"
	"path/filepath"
	"testing"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/names"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type policyTestClient struct {
	client.Client
	objects     map[string]client.Object
	updateCount int
}

func policyKey(obj client.Object) string { return obj.GetNamespace() + "/" + obj.GetName() }

func (c *policyTestClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	stored, ok := c.objects[key.Namespace+"/"+key.Name]
	if !ok {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "test"}, key.Name)
	}
	switch target := obj.(type) {
	case *networkingv1.NetworkPolicy:
		*target = *stored.(*networkingv1.NetworkPolicy).DeepCopy()
	default:
		panic("unexpected Get type")
	}
	return nil
}

func (c *policyTestClient) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	switch target := list.(type) {
	case *ptpv1.PtpConfigList:
		for _, obj := range c.objects {
			if config, ok := obj.(*ptpv1.PtpConfig); ok {
				target.Items = append(target.Items, *config.DeepCopy())
			}
		}
	case *networkingv1.NetworkPolicyList:
		for _, obj := range c.objects {
			if policy, ok := obj.(*networkingv1.NetworkPolicy); ok {
				target.Items = append(target.Items, *policy.DeepCopy())
			}
		}
	default:
		panic("unexpected List type")
	}
	return nil
}

func (c *policyTestClient) Create(_ context.Context, obj client.Object, _ ...client.CreateOption) error {
	c.objects[policyKey(obj)] = obj.DeepCopyObject().(client.Object)
	return nil
}

func (c *policyTestClient) Update(_ context.Context, obj client.Object, _ ...client.UpdateOption) error {
	c.updateCount++
	return c.Create(context.Background(), obj)
}

func (c *policyTestClient) Delete(_ context.Context, obj client.Object, _ ...client.DeleteOption) error {
	delete(c.objects, policyKey(obj))
	return nil
}

// apiEgressPortOnly asserts that a policy allows egress to the API server using
// a port-based rule (TCP 6443) with no destination peer.
func apiEgressPortOnly(t *testing.T, policy *networkingv1.NetworkPolicy) {
	t.Helper()
	if len(policy.Spec.Egress) != 1 || len(policy.Spec.Egress[0].To) != 0 ||
		len(policy.Spec.Egress[0].Ports) != 1 ||
		policy.Spec.Egress[0].Ports[0].Port.IntVal != 6443 ||
		*policy.Spec.Egress[0].Ports[0].Protocol != corev1.ProtocolTCP {
		t.Fatalf("API egress must be port-only TCP 6443: %+v", policy.Spec.Egress)
	}
}

func TestOperandNetworkPolicies(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	for _, register := range []func(*runtime.Scheme) error{corev1.AddToScheme, networkingv1.AddToScheme, ptpv1.AddToScheme} {
		if err := register(scheme); err != nil {
			t.Fatal(err)
		}
	}
	owner := &ptpv1.PtpOperatorConfig{ObjectMeta: metav1.ObjectMeta{Name: names.DefaultOperatorConfigName, Namespace: names.Namespace, UID: types.UID("policy-owner")}}
	c := &policyTestClient{objects: map[string]client.Object{policyKey(owner): owner}}
	r := &PtpOperatorConfigReconciler{Client: c, Scheme: scheme}
	path := filepath.Join("..", "bindata", "linuxptp", "network-policy.yaml")
	apply := func() {
		t.Helper()
		if err := r.applyNetworkPoliciesFromYaml(ctx, path, owner); err != nil {
			t.Fatal(err)
		}
	}
	get := func(name string) *networkingv1.NetworkPolicy {
		t.Helper()
		policy := &networkingv1.NetworkPolicy{}
		if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: names.Namespace}, policy); err != nil {
			t.Fatal(err)
		}
		return policy
	}

	apply()
	api := get("08-linuxptp-daemon-egress-api-server")
	if !metav1.IsControlledBy(api, owner) {
		t.Fatalf("daemon API egress policy is not owned by the config")
	}
	apiEgressPortOnly(t, api)
	for _, name := range []string{
		"06-linuxptp-daemon-default-deny", "07-linuxptp-daemon-egress-dns",
		"08-linuxptp-daemon-egress-api-server", "09-linuxptp-daemon-ptp-traffic",
		"11-linuxptp-daemon-ingress-metrics",
	} {
		policy := get(name)
		if !metav1.IsControlledBy(policy, owner) || policy.Spec.PodSelector.MatchLabels["app"] != "linuxptp-daemon" {
			t.Fatalf("incorrect daemon selector or ownership for %s", name)
		}
	}
	deny := get("06-linuxptp-daemon-default-deny")
	if len(deny.Spec.PolicyTypes) != 2 || len(deny.Spec.Ingress) != 0 || len(deny.Spec.Egress) != 0 {
		t.Fatalf("unexpected daemon default deny: %+v", deny.Spec)
	}
	ptp := get("09-linuxptp-daemon-ptp-traffic")
	for _, ports := range [][]networkingv1.NetworkPolicyPort{ptp.Spec.Ingress[0].Ports, ptp.Spec.Egress[0].Ports} {
		if len(ports) != 2 || ports[0].Port.IntVal != 319 || ports[1].Port.IntVal != 320 ||
			*ports[0].Protocol != corev1.ProtocolUDP || *ports[1].Protocol != corev1.ProtocolUDP {
			t.Fatalf("IP PTP must use only UDP 319/320: %+v", ports)
		}
	}
	metrics := get("11-linuxptp-daemon-ingress-metrics")
	if len(metrics.Spec.Ingress) != 1 || metrics.Spec.Ingress[0].Ports[0].Port.IntVal != 8443 {
		t.Fatalf("incorrect daemon metrics policy: %+v", metrics.Spec)
	}

	operatorDeny := get("01-ptp-operator-default-deny")
	if !metav1.IsControlledBy(operatorDeny, owner) || operatorDeny.Spec.PodSelector.MatchLabels["name"] != "ptp-operator" ||
		len(operatorDeny.Spec.PolicyTypes) != 2 || len(operatorDeny.Spec.Ingress) != 0 || len(operatorDeny.Spec.Egress) != 0 {
		t.Fatalf("incorrect operator default deny: %+v", operatorDeny.Spec)
	}
	operatorWebhook := get("02-ptp-operator-ingress-webhook")
	if !metav1.IsControlledBy(operatorWebhook, owner) ||
		operatorWebhook.Spec.PodSelector.MatchLabels["name"] != "ptp-operator" ||
		len(operatorWebhook.Spec.Ingress) != 1 || operatorWebhook.Spec.Ingress[0].Ports[0].Port.IntVal != 9443 {
		t.Fatalf("incorrect operator webhook policy: %+v", operatorWebhook.Spec)
	}
	operatorMetrics := get("03-ptp-operator-ingress-metrics")
	if !metav1.IsControlledBy(operatorMetrics, owner) ||
		operatorMetrics.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "openshift-monitoring" ||
		operatorMetrics.Spec.Ingress[0].Ports[0].Port.IntVal != 8443 {
		t.Fatalf("incorrect operator metrics policy: %+v", operatorMetrics.Spec)
	}
	operatorDNS := get("04-ptp-operator-egress-dns")
	if !metav1.IsControlledBy(operatorDNS, owner) || len(operatorDNS.Spec.Egress) != 1 ||
		operatorDNS.Spec.Egress[0].Ports[0].Port.IntVal != 5353 {
		t.Fatalf("incorrect operator DNS policy: %+v", operatorDNS.Spec)
	}
	operatorAPI := get("05-ptp-operator-egress-api-server")
	if !metav1.IsControlledBy(operatorAPI, owner) ||
		operatorAPI.Spec.PodSelector.MatchLabels["name"] != "ptp-operator" {
		t.Fatalf("incorrect operator API egress policy: %+v", operatorAPI.Spec)
	}
	apiEgressPortOnly(t, operatorAPI)

	if err := c.Get(ctx, types.NamespacedName{Name: "default-deny-all", Namespace: names.Namespace}, &networkingv1.NetworkPolicy{}); err == nil {
		t.Fatal("namespace-wide operator policy must not be created by operand controller")
	}
	for _, name := range []string{"10-linuxptp-daemon-ingress-health", "12-operator-to-daemon-management"} {
		if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: names.Namespace}, &networkingv1.NetworkPolicy{}); err == nil {
			t.Fatalf("unexpected policy without verified endpoint: %s", name)
		}
	}

	apply() // an unchanged reconcile must be a no-op
	if c.updateCount != 0 {
		t.Fatalf("unchanged policies were updated %d times", c.updateCount)
	}
	if len(c.objects) != 11 { // owner, five operator policies, and five unconditional daemon policies
		t.Fatalf("unexpected policy inventory: %d objects", len(c.objects))
	}

	profile := ptpv1.PtpProfile{ChronydOpts: new(string)}
	config := &ptpv1.PtpConfig{ObjectMeta: metav1.ObjectMeta{Name: "ntp", Namespace: names.Namespace}, Spec: ptpv1.PtpConfigSpec{Profile: []ptpv1.PtpProfile{profile}}}
	if err := c.Create(ctx, config); err != nil {
		t.Fatal(err)
	}
	apply()
	ntp := get("13-linuxptp-daemon-egress-ntp-optional")
	if !metav1.IsControlledBy(ntp, owner) || len(ntp.Spec.Ingress[0].Ports) != 2 || len(ntp.Spec.Egress[0].Ports) != 2 {
		t.Fatalf("incorrect conditional NTP policy: %+v", ntp)
	}
	if err := c.Delete(ctx, config); err != nil {
		t.Fatal(err)
	}
	apply()
	if err := c.Get(ctx, types.NamespacedName{Name: ntp.Name, Namespace: names.Namespace}, &networkingv1.NetworkPolicy{}); err == nil {
		t.Fatal("disabled chronyd policy was not removed")
	}

	legacy := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "default-deny-all", Namespace: names.Namespace}}
	if err := controllerutil.SetControllerReference(owner, legacy, scheme); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	staleOperator := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "allow-webhook-traffic", Namespace: names.Namespace}}
	if err := controllerutil.SetControllerReference(owner, staleOperator, scheme); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, staleOperator); err != nil {
		t.Fatal(err)
	}
	stale := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "12-operator-to-daemon-management", Namespace: names.Namespace}}
	if err := controllerutil.SetControllerReference(owner, stale, scheme); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, stale); err != nil {
		t.Fatal(err)
	}
	other := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: names.Namespace}}
	if err := c.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	apply()
	get(other.Name)  // unrelated policies are not cleaned up
	get(legacy.Name) // namespace-wide legacy policy is not deleted
	for _, name := range []string{"12-operator-to-daemon-management", "allow-webhook-traffic"} {
		if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: names.Namespace}, &networkingv1.NetworkPolicy{}); err == nil {
			t.Fatalf("stale owned policy was not removed: %s", name)
		}
	}

	api = get(api.Name)
	api.OwnerReferences = nil
	if err := c.Update(ctx, api); err != nil {
		t.Fatal(err)
	}
	if err := r.applyNetworkPoliciesFromYaml(ctx, path, owner); err == nil {
		t.Fatal("controller adopted a policy it did not own")
	}
}

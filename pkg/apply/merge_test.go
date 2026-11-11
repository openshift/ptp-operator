package apply

import (
	"bytes"
	"context"
	"testing"

	. "github.com/onsi/gomega"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// TestReconcileNamespace makes sure that namespace
// annotations are merged, and everything else is overwritten
// Namespaces use the "generic" logic; deployments and services
// have custom logic
func TestMergeNamespace(t *testing.T) {
	g := NewGomegaWithT(t)

	cur := UnstructuredFromYaml(t, `
apiVersion: v1
kind: Namespace
metadata:
  name: ns1
  labels:
    a: cur
    b: cur
  annotations:
    a: cur
    b: cur`)

	upd := UnstructuredFromYaml(t, `
apiVersion: v1
kind: Namespace
metadata:
  name: ns1
  labels:
    a: upd
    c: upd
  annotations:
    a: upd
    c: upd`)

	// this mutates updated
	err := MergeObjectForUpdate(context.Background(), cur, upd)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(upd.GetLabels()).To(Equal(map[string]string{
		"a": "upd",
		"b": "cur",
		"c": "upd",
	}))

	g.Expect(upd.GetAnnotations()).To(Equal(map[string]string{
		"a": "upd",
		"b": "cur",
		"c": "upd",
	}))
}

func TestMergeDeployment(t *testing.T) {
	g := NewGomegaWithT(t)

	cur := UnstructuredFromYaml(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: d1
  labels:
    a: cur
    b: cur
  annotations:
    deployment.kubernetes.io/revision: cur
    a: cur
    b: cur`)

	upd := UnstructuredFromYaml(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: d1
  labels:
    a: upd
    c: upd
  annotations:
    deployment.kubernetes.io/revision: upd
    a: upd
    c: upd`)

	// this mutates updated
	err := MergeObjectForUpdate(context.Background(), cur, upd)
	g.Expect(err).NotTo(HaveOccurred())

	// labels are not merged
	g.Expect(upd.GetLabels()).To(Equal(map[string]string{
		"a": "upd",
		"b": "cur",
		"c": "upd",
	}))

	// annotations are merged
	g.Expect(upd.GetAnnotations()).To(Equal(map[string]string{
		"a": "upd",
		"b": "cur",
		"c": "upd",

		"deployment.kubernetes.io/revision": "cur",
	}))
}

func TestMergeNilCur(t *testing.T) {
	g := NewGomegaWithT(t)

	cur := UnstructuredFromYaml(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: d1`)

	upd := UnstructuredFromYaml(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: d1
  labels:
    a: upd
    c: upd
  annotations:
    a: upd
    c: upd`)

	// this mutates updated
	err := MergeObjectForUpdate(context.Background(), cur, upd)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(upd.GetLabels()).To(Equal(map[string]string{
		"a": "upd",
		"c": "upd",
	}))

	g.Expect(upd.GetAnnotations()).To(Equal(map[string]string{
		"a": "upd",
		"c": "upd",
	}))
}

func TestMergeNilMeta(t *testing.T) {
	g := NewGomegaWithT(t)

	cur := UnstructuredFromYaml(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: d1`)

	upd := UnstructuredFromYaml(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: d1`)

	// this mutates updated
	err := MergeObjectForUpdate(context.Background(), cur, upd)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(upd.GetLabels()).To(BeEmpty())
}

func TestMergeNilUpd(t *testing.T) {
	g := NewGomegaWithT(t)

	cur := UnstructuredFromYaml(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: d1
  labels:
    a: cur
    b: cur
  annotations:
    a: cur
    b: cur`)

	upd := UnstructuredFromYaml(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: d1`)

	// this mutates updated
	err := MergeObjectForUpdate(context.Background(), cur, upd)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(upd.GetLabels()).To(Equal(map[string]string{
		"a": "cur",
		"b": "cur",
	}))

	g.Expect(upd.GetAnnotations()).To(Equal(map[string]string{
		"a": "cur",
		"b": "cur",
	}))
}

func TestMergeService(t *testing.T) {
	g := NewGomegaWithT(t)

	cur := UnstructuredFromYaml(t, `
apiVersion: v1
kind: Service
metadata:
  name: d1
spec:
  clusterIP: cur`)

	upd := UnstructuredFromYaml(t, `
apiVersion: v1
kind: Service
metadata:
  name: d1
spec:
  clusterIP: upd`)

	err := MergeObjectForUpdate(context.Background(), cur, upd)
	g.Expect(err).NotTo(HaveOccurred())

	ip, _, err := uns.NestedString(upd.Object, "spec", "clusterIP")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ip).To(Equal("cur"))
}

// TestMergeConfigMapPreservesDerivedData verifies the event-publisher CA bundle
// keeps the operator-derived output when its data-less template is re-applied.
func TestMergeConfigMapPreservesDerivedData(t *testing.T) {
	g := NewGomegaWithT(t)

	cur := UnstructuredFromYaml(t, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: ptp-event-publisher-ca-bundle
data:
  ca-bundle.crt: DERIVED`)

	upd := UnstructuredFromYaml(t, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: ptp-event-publisher-ca-bundle`)

	err := MergeObjectForUpdate(context.Background(), cur, upd)
	g.Expect(err).NotTo(HaveOccurred())

	data, ok, err := uns.NestedStringMap(upd.Object, "data")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(BeTrue())
	g.Expect(data).To(HaveKeyWithValue("ca-bundle.crt", "DERIVED"))
}

// TestMergeConfigMapPreservesInjectedServiceCA verifies the data-less Service
// CA input template does not clear service-ca.crt after injection. Clearing it
// would cause Service CA and ptp-operator to update the object forever.
func TestMergeConfigMapPreservesInjectedServiceCA(t *testing.T) {
	g := NewGomegaWithT(t)

	cur := UnstructuredFromYaml(t, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: ptp-event-publisher-service-ca
data:
  service-ca.crt: INJECTED`)

	upd := UnstructuredFromYaml(t, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: ptp-event-publisher-service-ca`)

	err := MergeObjectForUpdate(context.Background(), cur, upd)
	g.Expect(err).NotTo(HaveOccurred())

	data, ok, err := uns.NestedStringMap(upd.Object, "data")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(BeTrue())
	g.Expect(data).To(HaveKeyWithValue("service-ca.crt", "INJECTED"))
}

// TestMergeConfigMapUnscopedUntouched verifies the merge only guards the CA
// bundle ConfigMap; other ConfigMaps (e.g. ptp-configmap) are fully owned by the
// operator and must not have stale keys preserved.
func TestMergeConfigMapUnscopedUntouched(t *testing.T) {
	g := NewGomegaWithT(t)

	cur := UnstructuredFromYaml(t, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: ptp-configmap
data:
  stale: old`)

	upd := UnstructuredFromYaml(t, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: ptp-configmap`)

	err := MergeObjectForUpdate(context.Background(), cur, upd)
	g.Expect(err).NotTo(HaveOccurred())

	_, ok, err := uns.NestedStringMap(upd.Object, "data")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(BeFalse(), "unrelated ConfigMap data must not be preserved")
}

func TestMergeServiceAccount(t *testing.T) {
	g := NewGomegaWithT(t)

	cur := UnstructuredFromYaml(t, `
apiVersion: v1
kind: ServiceAccount
metadata:
  name: d1
  annotations:
    a: cur
secrets:
- foo`)

	upd := UnstructuredFromYaml(t, `
apiVersion: v1
kind: ServiceAccount
metadata:
  name: d1
  annotations:
    b: upd`)

	err := IsObjectSupported(cur)
	g.Expect(err).To(MatchError(ContainSubstring("cannot create ServiceAccount with secrets")))

	err = MergeObjectForUpdate(context.Background(), cur, upd)
	g.Expect(err).NotTo(HaveOccurred())

	s, ok, err := uns.NestedSlice(upd.Object, "secrets")
	g.Expect(ok).To(BeTrue())
	g.Expect(s).To(ConsistOf("foo"))
}

// UnstructuredFromYaml creates an unstructured object from a raw yaml string
func UnstructuredFromYaml(t *testing.T, obj string) *uns.Unstructured {
	t.Helper()
	buf := bytes.NewBufferString(obj)
	decoder := yaml.NewYAMLOrJSONDecoder(buf, 4096)

	u := uns.Unstructured{}
	err := decoder.Decode(&u)
	if err != nil {
		t.Fatalf("failed to parse test yaml: %v", err)
	}

	return &u
}

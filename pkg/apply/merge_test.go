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

func TestMergeDaemonSetPreservesVerbosityArgs(t *testing.T) {
	g := NewGomegaWithT(t)

	cur := UnstructuredFromYaml(t, `
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: linuxptp-daemon
spec:
  template:
    spec:
      volumes:
      - name: ptp4l-conf-tlv-auth
        configMap:
          name: ptp4l-conf
      containers:
      - name: linuxptp-daemon-container
        command: ["/bin/sh"]
        args: ["-c", "/usr/local/bin/ptp --alsologtostderr -v 10"]`)

	upd := UnstructuredFromYaml(t, `
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: linuxptp-daemon
spec:
  template:
    spec:
      containers:
      - name: linuxptp-daemon-container
        command: ["/bin/sh"]
        args: ["-c", "/usr/local/bin/ptp --alsologtostderr -v 7"]
        volumeMounts:
        - name: config-volume
          mountPath: /etc/linuxptp`)

	ctx := context.WithValue(context.Background(), ControllerSourceKey, SourcePtpOperatorConfig)
	err := MergeDaemonSetForUpdate(ctx, cur, upd)
	g.Expect(err).NotTo(HaveOccurred())

	containers, found, err := uns.NestedSlice(upd.Object,
		"spec", "template", "spec", "containers")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(found).To(BeTrue())
	container, ok := containers[0].(map[string]interface{})
	g.Expect(ok).To(BeTrue())
	containerArgs, _, err := uns.NestedStringSlice(container, "args")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(containerArgs).To(ContainElement(ContainSubstring("-v 7")),
		"rendered -v verbosity args must be preserved across the merge path")

	volumes, ok, err := uns.NestedSlice(upd.Object, "spec", "template", "spec", "volumes")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(BeTrue())
	g.Expect(volumes).To(ContainElement(HaveKey("name")),
		"security volumes should still be merged")
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

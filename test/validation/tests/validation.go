package validation_tests

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	goclient "sigs.k8s.io/controller-runtime/pkg/client"

	testutils "github.com/openshift/ptp-operator/test/utils"
	testclient "github.com/openshift/ptp-operator/test/utils/client"
)

var _ = Describe("validation", func() {
	Context("ptp", func() {
		It("should have the ptp namespace", func() {
			_, err := testclient.Client.Namespaces().Get(context.Background(), testutils.PtpNamespace, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should have the ptp operator deployment in running state", func() {
			deploy, err := testclient.Client.Deployments(testutils.PtpNamespace).Get(context.Background(), testutils.PtpOperatorDeploymentName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(deploy.Status.Replicas).To(Equal(deploy.Status.ReadyReplicas))

			pods, err := testclient.Client.Pods(testutils.PtpNamespace).List(context.Background(), metav1.ListOptions{
				LabelSelector: fmt.Sprintf("name=%s", testutils.PtpOperatorDeploymentName)})
			Expect(err).ToNot(HaveOccurred())

			Expect(len(pods.Items)).To(Equal(1))
			Expect(pods.Items[0].Status.Phase).To(Equal(corev1.PodRunning))
		})

		It("should have the linuxptp daemonset in running state", func() {
			daemonset, err := testclient.Client.DaemonSets(testutils.PtpNamespace).Get(context.Background(), testutils.PtpDaemonsetName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(daemonset.Status.NumberReady).To(Equal(daemonset.Status.DesiredNumberScheduled))
		})

		It("should have the ptp CRDs available in the cluster", func() {
			crd := &apiext.CustomResourceDefinition{}
			err := testclient.Client.Get(context.TODO(), goclient.ObjectKey{Name: testutils.NodePtpDevicesCRD}, crd)
			Expect(err).ToNot(HaveOccurred())

			err = testclient.Client.Get(context.TODO(), goclient.ObjectKey{Name: testutils.PtpConfigsCRD}, crd)
			Expect(err).ToNot(HaveOccurred())

			err = testclient.Client.Get(context.TODO(), goclient.ObjectKey{Name: testutils.PtpOperatorConfigsCRD}, crd)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})

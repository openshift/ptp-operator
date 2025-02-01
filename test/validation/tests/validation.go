package validation_tests

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	goclient "sigs.k8s.io/controller-runtime/pkg/client"

	testutils "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	testclient "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/ptphelper"
)

var _ = Describe("validation", func() {
	Context("ptp", func() {
		It("should have the ptp namespace", func() {
			_, err := testclient.Client.CoreV1().Namespaces().Get(context.Background(), testutils.PtpNamespace, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should have the ptp operator deployment in running state", func() {
			Eventually(func() error {
				deploy, err := testclient.Client.Deployments(testutils.PtpNamespace).Get(context.Background(), testutils.PtpOperatorDeploymentName, metav1.GetOptions{})
				if err != nil {
					return err
				}

				pods, err := testclient.Client.CoreV1().Pods(testutils.PtpNamespace).List(context.Background(), metav1.ListOptions{
					LabelSelector: fmt.Sprintf("name=%s", testutils.PtpOperatorDeploymentName)})
				if err != nil {
					return err
				}

				if len(pods.Items) != int(deploy.Status.Replicas) {
					return fmt.Errorf("deployment %s pods are not ready, expected %d replicas got %d pods", testutils.PtpOperatorDeploymentName, deploy.Status.Replicas, len(pods.Items))
				}

				for _, pod := range pods.Items {
					if pod.Status.Phase != corev1.PodRunning {
						return fmt.Errorf("deployment %s pod %s is not running, expected status %s got %s", testutils.PtpOperatorDeploymentName, pod.Name, corev1.PodRunning, pod.Status.Phase)
					}
				}

				return nil
			}, testutils.TimeoutIn10Minutes, testutils.TimeoutInterval2Seconds).ShouldNot(HaveOccurred())
		})

		It("should set the lease duration to 270 seconds for SNO, 137 seconds otherwise", func() {
			ptphelper.CheckLeaseDuration(testutils.PtpNamespace, 137, 270)
		})

		It("should have the linuxptp daemonset in running state", func() {
			ptphelper.WaitForPtpDaemonToBeReady()
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

//go:build !unittests
// +build !unittests

package test_test

import (
	"context"
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	"k8s.io/utils/pointer"

	"github.com/openshift/ptp-operator/test/conformance/ptp"
	_ "github.com/openshift/ptp-operator/test/conformance/ptp"
	"github.com/openshift/ptp-operator/test/utils"
	"github.com/openshift/ptp-operator/test/utils/clean"
	"github.com/openshift/ptp-operator/test/utils/client"
	testclient "github.com/openshift/ptp-operator/test/utils/client"
	"github.com/openshift/ptp-operator/test/utils/testconfig"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var junitPath *string
var DeletePtpConfig bool

var fullConfig testconfig.TestConfig
var testParameters ptp.Configuration

func init() {
	junitPath = flag.String("junit", "junit.xml", "the path for the junit format report")
}

func InitDeletePtpConfig() {
	value, isSet := os.LookupEnv("KEEP_PTPCONFIG")
	value = strings.ToLower(value)
	DeletePtpConfig = !isSet || strings.Contains(value, "false")
	logrus.Infof("DeletePtpConfig=%t", DeletePtpConfig)
}

func TestTest(t *testing.T) {
	RegisterFailHandler(Fail)
	InitDeletePtpConfig()
	RunSpecs(t, "PTP e2e integration tests")
}

var _ = BeforeSuite(func() {
	logrus.Info("Executed from BeforeSuite")
	testclient.Client = testclient.New("")
	Expect(testclient.Client).NotTo(BeNil())

	testconfig.CreatePtpConfigurations()
	fullConfig = testconfig.GetFullDiscoveredConfig(utils.PtpLinuxDaemonNamespace, false)
	restartPtpDaemon()
})

var _ = AfterSuite(func() {
	if DeletePtpConfig {
		clean.All()
	}
})

func restartPtpDaemon() {
	ptpPods, err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	Expect(err).ToNot(HaveOccurred())
	for podIndex := range ptpPods.Items {
		err = client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).Delete(context.Background(), ptpPods.Items[podIndex].Name, metav1.DeleteOptions{GracePeriodSeconds: pointer.Int64Ptr(0)})
		Expect(err).ToNot(HaveOccurred())
	}

	waitForPtpDaemonToBeReady()
}

func waitForPtpDaemonToBeReady() int {
	daemonset, err := client.Client.DaemonSets(utils.PtpLinuxDaemonNamespace).Get(context.Background(), utils.PtpDaemonsetName, metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred())
	expectedNumber := daemonset.Status.DesiredNumberScheduled
	Eventually(func() int32 {
		daemonset, err = client.Client.DaemonSets(utils.PtpLinuxDaemonNamespace).Get(context.Background(), utils.PtpDaemonsetName, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		return daemonset.Status.NumberReady
	}, timeoutIn5Minutes, 2*time.Second).Should(Equal(expectedNumber))

	Eventually(func() int {
		ptpPods, err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
		Expect(err).ToNot(HaveOccurred())
		return len(ptpPods.Items)
	}, timeoutIn5Minutes, 2*time.Second).Should(Equal(int(expectedNumber)))
	return 0
}

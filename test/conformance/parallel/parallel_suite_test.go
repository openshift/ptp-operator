//go:build !unittests
// +build !unittests

package test

import (
	"flag"
	"fmt"
	"strings"

	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"

	ptptestconfig "github.com/k8snetworkplumbingwg/ptp-operator/test/conformance/config"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/clean"
	testclient "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/event"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/logging"

	ptphelper "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/ptphelper"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/testconfig"
)

var junitPath *string
var DeletePtpConfig bool

func init() {
	junitPath = flag.String("junit", "junit.xml", "the path for the junit format report")
}

func TestTest(t *testing.T) {
	logging.InitLogLevel()
	RegisterFailHandler(Fail)
	RunSpecs(t, "PTP e2e tests : Parallel")
}

var _ = SynchronizedBeforeSuite(func() []byte {
	// Get the test config parameters
	testParameters, err := ptptestconfig.GetPtpTestConfig()
	Expect(err).To(BeNil(), "Failed to get Test Config")

	if testParameters.SoakTestConfig.DisableSoakTest {
		Skip("Soak testing is disabled at the configuration file. Hence, skipping!")
	}

	// Run on all Ginkgo nodes
	logrus.Info("Executed from parallel suite")
	testclient.Client = testclient.New("")
	Expect(testclient.Client).NotTo(BeNil())

	// discovers valid ptp configurations based on clock type
	err = testconfig.CreatePtpConfigurations()
	Expect(err).To(BeNil(), "Could not create a ptp config")

	By("Refreshing configuration", func() {
		ptphelper.WaitForPtpDaemonToBeReady()
		fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
	})
	ptphelper.RestartPTPDaemon()

	isConsumerReady := true
	apiVersion := event.GetDefaultApiVersion()
	err = ptphelper.EnablePTPEvent(apiVersion, fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
	Expect(err).To(BeNil(), "Error when enable ptp event")
	if apiVersion == "1.0" {
		logrus.Info("Deploy consumer app with sidecar for testing event API v1")
		err = event.CreateConsumerAppWithSidecar(fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
		if err != nil {
			logrus.Errorf("PTP events are not available due to consumer app/sidecar creation error err=%s", err)
			isConsumerReady = false
		}
	} else {
		logrus.Info("Deploy consumer app without sidecar for testing event API v2")
		err = event.CreateConsumerApp(fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
		if err != nil {
			logrus.Errorf("PTP events are not available due to consumer app creation error err=%s", err)
			isConsumerReady = false
		}
	}
	// stops the event listening framework
	DeferCleanup(func() {
		err = event.DeleteConsumerNamespace()
		if err != nil {
			logrus.Debugf("Deleting consumer namespace failed because of err=%s", err)
		}
	})

	logrus.Debugf("lib.Ps=%v", event.PubSub)
	return []byte(fmt.Sprintf("%t,%p", isConsumerReady, event.PubSub))
}, func(data []byte) {
	values := strings.Split(string(data), ",")
	testclient.Client = testclient.New("")
	isConsumerReady := false
	if string(values[0]) == "true" {
		isConsumerReady = true
	}

	// this is executed once per thread/test
	By("Refreshing configuration", func() {
		ptphelper.WaitForPtpDaemonToBeReady()
		fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
		fullConfig.PtpEventsIsConsumerReady = isConsumerReady
	})
})
var _ = AfterSuite(func() {
	if DeletePtpConfig {
		clean.All()
	}
})

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
	lib "github.com/redhat-cne/ptp-listener-lib"
	"github.com/sirupsen/logrus"

	ptptestconfig "github.com/openshift/ptp-operator/test/conformance/config"
	"github.com/openshift/ptp-operator/test/pkg"
	"github.com/openshift/ptp-operator/test/pkg/clean"
	testclient "github.com/openshift/ptp-operator/test/pkg/client"
	"github.com/openshift/ptp-operator/test/pkg/event"
	"github.com/openshift/ptp-operator/test/pkg/logging"

	ptphelper "github.com/openshift/ptp-operator/test/pkg/ptphelper"
	"github.com/openshift/ptp-operator/test/pkg/testconfig"
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
	testParameters := ptptestconfig.GetPtpTestConfig()
	if testParameters.SoakTestConfig.DisableSoakTest {
		Skip("Soak testing is disabled at the configuration file. Hence, skipping!")
	}

	// Run on all Ginkgo nodes
	logrus.Info("Executed from parallel suite")
	testclient.Client = testclient.New("")
	Expect(testclient.Client).NotTo(BeNil())

	// discovers valid ptp configurations based on clock type
	err := testconfig.CreatePtpConfigurations()
	if err != nil {
		Fail(fmt.Sprintf("Could not create a ptp config, err=%s", err))
	}
	By("Refreshing configuration", func() {
		ptphelper.WaitForPtpDaemonToBeReady()
		fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
	})
	ptphelper.RestartPTPDaemon()

	isSideCarReady := false
	if event.IsDeployConsumerSidecar() {
		err := event.CreateEventProxySidecar(fullConfig.DiscoveredClockUnderTestPod.Spec.NodeName)
		if err != nil {
			logrus.Errorf("PTP events are not available due to Sidecar creation error err=%s", err)
		}
		isSideCarReady = true
	}

	// stops the event listening framework
	DeferCleanup(func() {

		//delete the sidecar
		err = event.DeleteTestSidecarNamespace()
		if err != nil {
			logrus.Debugf("Deleting test sidecar failed because of err=%s", err)
		}
	})

	logrus.Debugf("lib.Ps=%v", lib.Ps)
	return []byte(fmt.Sprintf("%t,%p", isSideCarReady, lib.Ps))
}, func(data []byte) {
	values := strings.Split(string(data), ",")
	testclient.Client = testclient.New("")
	isSideCarReady := false
	if string(values[0]) == "true" {
		isSideCarReady = true
	}

	// this is executed once per thread/test
	By("Refreshing configuration", func() {
		ptphelper.WaitForPtpDaemonToBeReady()
		fullConfig = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
		fullConfig.PtpEventsIsSidecarReady = isSideCarReady
	})
})
var _ = AfterSuite(func() {
	if DeletePtpConfig {
		clean.All()
	}
})

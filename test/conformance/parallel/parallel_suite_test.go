//go:build !unittests
// +build !unittests

package test

import (
	"flag"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ptp-operator/test/pkg"
	"github.com/openshift/ptp-operator/test/pkg/clean"
	testclient "github.com/openshift/ptp-operator/test/pkg/client"
	ptphelper "github.com/openshift/ptp-operator/test/pkg/ptphelper"
	"github.com/openshift/ptp-operator/test/pkg/testconfig"

	ptptestconfig "github.com/openshift/ptp-operator/test/conformance/config"
)

var junitPath *string
var DeletePtpConfig bool

func init() {
	junitPath = flag.String("junit", "junit.xml", "the path for the junit format report")
}

func TestTest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PTP e2e tests : Parallel")
}

var _ = SynchronizedBeforeSuite(func() []byte {
	// Get the test config parameters
	testParameters := ptptestconfig.GetPtpTestConfig()
	if !testParameters.SoakTestConfig.EnableSoakTest {
		Skip("Soak testing is disabled at the configuration file. Hence, skipping!")
	}

	// Run on all Ginkgo nodes
	logrus.Info("Executed from parallel suite")
	testclient.Client = testclient.New("")
	Expect(testclient.Client).NotTo(BeNil())

	testconfig.CreatePtpConfigurations()
	ptphelper.RestartPTPDaemon()
	_ = testconfig.GetFullDiscoveredConfig(pkg.PtpLinuxDaemonNamespace, true)
	// _ = GetPtpTestConfig()
	return []byte("ok")
}, func(config []byte) {

})
var _ = AfterSuite(func() {
	if DeletePtpConfig {
		clean.All()
	}
})

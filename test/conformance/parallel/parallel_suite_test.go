//go:build !unittests
// +build !unittests

package test

import (
	"flag"
	"fmt"
	"path"
	"strings"

	"testing"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/reporters"
	"github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"
	lib "github.com/redhat-cne/ptp-listener-lib"
	"github.com/sirupsen/logrus"

	kniK8sReporter "github.com/openshift-kni/k8sreporter"
	ptptestconfig "github.com/openshift/ptp-operator/test/conformance/config"
	"github.com/openshift/ptp-operator/test/pkg"
	"github.com/openshift/ptp-operator/test/pkg/clean"
	testclient "github.com/openshift/ptp-operator/test/pkg/client"
	"github.com/openshift/ptp-operator/test/pkg/event"
	"github.com/openshift/ptp-operator/test/pkg/k8sreporter"
	"github.com/openshift/ptp-operator/test/pkg/logging"
	stringsutil "github.com/openshift/ptp-operator/test/pkg/strings"

	ptphelper "github.com/openshift/ptp-operator/test/pkg/ptphelper"
	"github.com/openshift/ptp-operator/test/pkg/testconfig"
)

var (
	reportPath      *string
	junitPath       *string
	DeletePtpConfig bool
	reporter        *kniK8sReporter.KubernetesReporter
)

func init() {
	junitPath = flag.String("junit", "", "the path for the junit format report")
	reportPath = flag.String("report", "", "the path of the report file containing details for failed tests")
}

func TestTest(t *testing.T) {
	logging.InitLogLevel()
	RegisterFailHandler(Fail)
	if *reportPath != "" {
		reporter = k8sreporter.New("", *reportPath, pkg.PtpNamespace)
	}
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

var _ = ReportAfterSuite("PTP parallel e2e integration tests", func(report types.Report) {
	if *junitPath != "" {
		junitFile := path.Join(*junitPath, "ptp_parallel_junit.xml")
		reporters.GenerateJUnitReportWithConfig(report, junitFile, reporters.JunitReportConfig{
			OmitTimelinesForSpecState: types.SpecStatePassed | types.SpecStateSkipped,
			OmitLeafNodeType:          true,
			OmitSuiteSetupNodes:       true,
		})
	}
})

var _ = ReportAfterEach(func(specReport types.SpecReport) {
	if specReport.Failed() == false {
		return
	}

	if *reportPath != "" {
		dumpSubPath := stringsutil.CleanDirName(specReport.LeafNodeText)
		reporter.Dump(pkg.LogsFetchDuration, dumpSubPath)
	}
})

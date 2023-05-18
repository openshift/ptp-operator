//go:build !unittests
// +build !unittests

package test

import (
	"flag"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/openshift/ptp-operator/test/pkg/clean"
	testclient "github.com/openshift/ptp-operator/test/pkg/client"
	"github.com/openshift/ptp-operator/test/pkg/logging"
	"github.com/openshift/ptp-operator/test/pkg/testconfig"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/reporters"
	"github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
)

var (
	junitPath       *string
	DeletePtpConfig bool
)

func init() {
	junitPath = flag.String("junit", "", "the path for the junit format report")
}

func InitDeletePtpConfig() {
	value, isSet := os.LookupEnv("KEEP_PTPCONFIG")
	value = strings.ToLower(value)
	DeletePtpConfig = !isSet || strings.Contains(value, "false")
	logrus.Infof("DeletePtpConfig=%t", DeletePtpConfig)
}

func TestTest(t *testing.T) {
	logging.InitLogLevel()
	RegisterFailHandler(Fail)
	InitDeletePtpConfig()
	RunSpecs(t, "PTP e2e integration tests")
}

var _ = BeforeSuite(func() {
	logrus.Info("Executed from serial suite")
	testclient.Client = testclient.New("")
	Expect(testclient.Client).NotTo(BeNil())
})

var _ = AfterSuite(func() {

	if DeletePtpConfig && testconfig.GetDesiredConfig(false).PtpModeDesired != testconfig.Discovery {
		clean.All()
	}
})

var _ = ReportAfterSuite("PTP serial e2e integration tests", func(report types.Report) {
	if *junitPath != "" {
		junitFile := path.Join(*junitPath, "ptp_serial_junit.xml")
		reporters.GenerateJUnitReportWithConfig(report, junitFile, reporters.JunitReportConfig{
			OmitTimelinesForSpecState: types.SpecStatePassed | types.SpecStateSkipped,
			OmitLeafNodeType:          true,
			OmitSuiteSetupNodes:       true,
		})
	}
})

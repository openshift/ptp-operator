//go:build !unittests
// +build !unittests

package test

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/clean"
	testclient "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/logging"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/testconfig"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
)

var junitPath *string
var DeletePtpConfig bool

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

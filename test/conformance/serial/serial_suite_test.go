//go:build !unittests
// +build !unittests

package test

import (
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ptp-operator/test/conformance/ptp"
	_ "github.com/openshift/ptp-operator/test/conformance/ptp"
	"github.com/openshift/ptp-operator/test/utils/clean"
	testclient "github.com/openshift/ptp-operator/test/utils/client"
	"github.com/openshift/ptp-operator/test/utils/testconfig"
)

var junitPath *string
var DeletePtpConfig bool

var fullConfig testconfig.TestConfig
var testParameters ptp.Configuration

const (
	TimeoutIn3Minutes  = 3 * time.Minute
	TimeoutIn5Minutes  = 5 * time.Minute
	TimeoutIn10Minutes = 10 * time.Minute
)

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
	logrus.Info("Executed from serial suite")
	testclient.Client = testclient.New("")
	Expect(testclient.Client).NotTo(BeNil())
	testParameters = ptp.GetConfiguration()
})

var _ = AfterSuite(func() {
	if DeletePtpConfig {
		clean.All()
	}
})

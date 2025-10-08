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
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/event"
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

var _ = SynchronizedBeforeSuite(func() []byte {
	// This runs ONCE on node 1
	logrus.Info("Executed from serial suite")
	testclient.Client = testclient.New("")
	Expect(testclient.Client).NotTo(BeNil())

	// Initialize the pub/sub system for event handling
	event.InitPubSub()

	// Start log collection if enabled
	err := logging.StartLogCollection("serial")
	if err != nil {
		logrus.Errorf("Failed to start log collection: %v", err)
	}

	return nil
}, func(data []byte) {
	// This runs on ALL parallel nodes (including node 1)
	testclient.Client = testclient.New("")
	Expect(testclient.Client).NotTo(BeNil())
})

var _ = SynchronizedAfterSuite(func() {
	// This runs on all parallel nodes
	if DeletePtpConfig && testconfig.GetDesiredConfig(false).PtpModeDesired != testconfig.Discovery {
		clean.All()
	}
}, func() {
	// This runs ONCE on node 1 (after all other nodes finish)
	// Stop log collection
	logging.StopLogCollection()
})

var _ = ReportBeforeEach(func(report SpecReport) {
	// Write test start marker to all log files
	logging.WriteTestStart(report)
})

var _ = ReportAfterEach(func(report SpecReport) {
	// Write test end marker to all log files
	logging.WriteTestEnd(report)
})

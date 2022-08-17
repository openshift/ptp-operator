//go:build !unittests
// +build !unittests

package test_test

import (
	"flag"
	"os"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/reporters"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"

	_ "github.com/openshift/ptp-operator/test/conformance/ptp"
	"github.com/openshift/ptp-operator/test/utils/clean"
	testclient "github.com/openshift/ptp-operator/test/utils/client"
)

// TODO: we should refactor tests to use client from controller-runtime package
// see - https://github.com/openshift/cluster-api-actuator-pkg/blob/master/pkg/e2e/framework/framework.go

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
	RegisterFailHandler(Fail)

	rr := []Reporter{}
	if junitPath != nil {
		rr = append(rr, reporters.NewJUnitReporter(*junitPath))
	}
	InitDeletePtpConfig()
	RunSpecsWithDefaultAndCustomReporters(t, "PTP e2e integration tests", rr)
}

var _ = BeforeSuite(func() {
	testclient.Client = testclient.New("")
	Expect(testclient.Client).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	if DeletePtpConfig {
		clean.All()
	}
})

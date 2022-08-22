//go:build !unittests
// +build !unittests

package test

import (
	"flag"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ptp-operator/test/utils/clean"
	testclient "github.com/openshift/ptp-operator/test/utils/client"
)

var junitPath *string
var DeletePtpConfig bool

const (
	TimeoutIn3Minutes  = 3 * time.Minute
	TimeoutIn5Minutes  = 5 * time.Minute
	TimeoutIn10Minutes = 10 * time.Minute
)

func init() {
	junitPath = flag.String("junit", "junit.xml", "the path for the junit format report")
}

func TestTest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PTP e2e tests : Parallel")
}

var _ = BeforeSuite(func() {
	logrus.Info("Executed from parallel suite")
	testclient.Client = testclient.New("")
	Expect(testclient.Client).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	if DeletePtpConfig {
		clean.All()
	}
})

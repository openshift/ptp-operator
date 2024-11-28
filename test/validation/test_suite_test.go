package validation

import (
	"flag"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	testclient "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	_ "github.com/k8snetworkplumbingwg/ptp-operator/test/validation/tests"
)

var junitPath *string

func init() {
	junitPath = flag.String("junit", "junit.xml", "the path for the junit format report")
}

func TestTest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PTP Operator validation tests")
}

var _ = BeforeSuite(func() {
	testclient.Client = testclient.New("")
	Expect(testclient.Client).NotTo(BeNil())
})

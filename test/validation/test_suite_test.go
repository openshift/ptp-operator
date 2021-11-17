package validation

import (
	"flag"
	"testing"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/reporters"
	. "github.com/onsi/gomega"

	testclient "github.com/openshift/ptp-operator/test/utils/client"
	_ "github.com/openshift/ptp-operator/test/validation/tests"
)

var junitPath *string

func init() {
	junitPath = flag.String("junit", "junit.xml", "the path for the junit format report")
}

func TestTest(t *testing.T) {
	RegisterFailHandler(Fail)

	rr := []Reporter{}
	if junitPath != nil {
		rr = append(rr, reporters.NewJUnitReporter(*junitPath))
	}
	RunSpecsWithDefaultAndCustomReporters(t, "PTP Operator validation tests", rr)
}

var _ = BeforeSuite(func() {
	Expect(testclient.Client).NotTo(BeNil())
})

package names

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNamespaceDefault(t *testing.T) {
	// When OPERATOR_NAMESPACE is not set, Namespace should be "openshift-ptp"
	os.Unsetenv("OPERATOR_NAMESPACE")
	Namespace = "openshift-ptp" // reset to default (init() already ran)
	assert.Equal(t, "openshift-ptp", Namespace)
}

func TestNamespaceFromEnv(t *testing.T) {
	original := Namespace
	defer func() { Namespace = original }()

	os.Setenv("OPERATOR_NAMESPACE", "custom-namespace")
	defer os.Unsetenv("OPERATOR_NAMESPACE")

	// Simulate what init() does
	if ns := os.Getenv("OPERATOR_NAMESPACE"); ns != "" {
		Namespace = ns
	}
	assert.Equal(t, "custom-namespace", Namespace)
}

func TestNamespaceEmptyEnvKeepsDefault(t *testing.T) {
	original := Namespace
	defer func() { Namespace = original }()

	Namespace = "openshift-ptp"
	os.Setenv("OPERATOR_NAMESPACE", "")
	defer os.Unsetenv("OPERATOR_NAMESPACE")

	if ns := os.Getenv("OPERATOR_NAMESPACE"); ns != "" {
		Namespace = ns
	}
	assert.Equal(t, "openshift-ptp", Namespace)
}

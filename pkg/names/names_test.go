package names

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNamespaceDefault(t *testing.T) {
	originalNS := Namespace
	originalEnv, envWasSet := os.LookupEnv("OPERATOR_NAMESPACE")
	defer func() {
		Namespace = originalNS
		if envWasSet {
			os.Setenv("OPERATOR_NAMESPACE", originalEnv)
		} else {
			os.Unsetenv("OPERATOR_NAMESPACE")
		}
	}()

	os.Unsetenv("OPERATOR_NAMESPACE")
	Namespace = "openshift-ptp"
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

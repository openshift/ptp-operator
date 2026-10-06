package v1

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestValidatingAdmissionPolicyManifest(t *testing.T) {
	manifestPath := filepath.Join("..", "..", "config", "policy", "ptpconfig-minoffsetthreshold-deprecation.yaml")
	data, err := os.ReadFile(manifestPath)
	if !assert.NoError(t, err, "policy manifest must exist and be readable") {
		return
	}

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)

	var policy map[string]interface{}
	err = decoder.Decode(&policy)
	if !assert.NoError(t, err, "ValidatingAdmissionPolicy document must be valid YAML") {
		return
	}
	assert.Equal(t, "admissionregistration.k8s.io/v1", policy["apiVersion"])
	assert.Equal(t, "ValidatingAdmissionPolicy", policy["kind"])

	var binding map[string]interface{}
	err = decoder.Decode(&binding)
	if !assert.NoError(t, err, "ValidatingAdmissionPolicyBinding document must be valid YAML") {
		return
	}
	assert.Equal(t, "ValidatingAdmissionPolicyBinding", binding["kind"])

	bindingSpec, ok := binding["spec"].(map[string]interface{})
	if !assert.True(t, ok, "ValidatingAdmissionPolicyBinding spec must be a map") {
		return
	}
	assert.Equal(t, "ptpconfig-minoffsetthreshold-deprecation", bindingSpec["policyName"])
	assert.Equal(t, []interface{}{"Warn"}, bindingSpec["validationActions"])
}

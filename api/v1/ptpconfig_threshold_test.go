package v1

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// ptpClockThresholdFromCRD loads the generated PtpConfig CRD manifest and returns the
// OpenAPI schema node for the profile's ptpClockThreshold object.
func ptpClockThresholdFromCRD(t *testing.T) map[string]interface{} {
	t.Helper()
	manifestPath := filepath.Join("..", "..", "config", "crd", "bases", "ptp.openshift.io_ptpconfigs.yaml")
	data, err := os.ReadFile(manifestPath)
	if !assert.NoError(t, err, "CRD manifest must exist and be readable") {
		return nil
	}

	var crd map[string]interface{}
	err = yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096).Decode(&crd)
	if !assert.NoError(t, err, "CRD manifest must be valid YAML") {
		return nil
	}

	spec, _ := crd["spec"].(map[string]interface{})
	versions, _ := spec["versions"].([]interface{})
	if !assert.NotEmpty(t, versions, "CRD must declare at least one version") {
		return nil
	}
	version, _ := versions[0].(map[string]interface{})
	schema, _ := version["schema"].(map[string]interface{})
	openAPI, _ := schema["openAPIV3Schema"].(map[string]interface{})
	rootProps, _ := openAPI["properties"].(map[string]interface{})
	specNode, _ := rootProps["spec"].(map[string]interface{})
	specProps, _ := specNode["properties"].(map[string]interface{})
	profileNode, _ := specProps["profile"].(map[string]interface{})
	profileItems, _ := profileNode["items"].(map[string]interface{})
	profileProps, _ := profileItems["properties"].(map[string]interface{})
	thresholdNode, _ := profileProps["ptpClockThreshold"].(map[string]interface{})
	props, _ := thresholdNode["properties"].(map[string]interface{})

	if !assert.Contains(t, props, "ptpSourceQualifiedThreshold", "CRD must expose ptpSourceQualifiedThreshold") {
		return nil
	}
	return props
}

func TestPtpClockThresholdCRDSourceQualificationFields(t *testing.T) {
	props := ptpClockThresholdFromCRD(t)
	if props == nil {
		return
	}

	tests := []struct {
		name        string
		jsonName    string
		hasDefault  bool
		wantDefault interface{}
		wantMinimum int
	}{
		{
			name:        "qualified threshold",
			jsonName:    "ptpSourceQualifiedThreshold",
			hasDefault:  false,
			wantMinimum: 1,
		},
		{
			name:        "disqualified threshold",
			jsonName:    "ptpSourceDisqualifiedThreshold",
			hasDefault:  false,
			wantMinimum: 1,
		},
		{
			name:        "qualified samples",
			jsonName:    "ptpSourceQualifiedSamples",
			hasDefault:  false,
			wantMinimum: 1,
		},
		{
			name:        "disqualified samples",
			jsonName:    "ptpSourceDisqualifiedSamples",
			hasDefault:  false,
			wantMinimum: 1,
		},
		{
			name:        "use S3",
			jsonName:    "ptpSourceUseS3",
			hasDefault:  true,
			wantDefault: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, ok := props[tt.jsonName].(map[string]interface{})
			if !assert.True(t, ok, "%s must be an object property", tt.jsonName) {
				return
			}
			if tt.hasDefault {
				assert.Equal(t, tt.wantDefault, node["default"], "%s default must be set", tt.jsonName)
			} else {
				_, hasDefault := node["default"]
				assert.False(t, hasDefault, "%s must not have a CRD default; omitted field relies on the daemon fallback", tt.jsonName)
				if tt.wantMinimum > 0 {
					minimum, ok := node["minimum"].(float64)
					if assert.True(t, ok, "%s must declare a numeric minimum", tt.jsonName) {
						assert.Equal(t, float64(tt.wantMinimum), minimum, "%s must enforce minimum %d", tt.jsonName, tt.wantMinimum)
					}
				}
			}
		})
	}
}

func TestPtpClockThresholdCRDSourceQualificationCrossFieldRule(t *testing.T) {
	manifestPath := filepath.Join("..", "..", "config", "crd", "bases", "ptp.openshift.io_ptpconfigs.yaml")
	data, err := os.ReadFile(manifestPath)
	if !assert.NoError(t, err, "CRD manifest must exist and be readable") {
		return
	}

	var crd map[string]interface{}
	err = yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096).Decode(&crd)
	if !assert.NoError(t, err, "CRD manifest must be valid YAML") {
		return
	}

	spec, _ := crd["spec"].(map[string]interface{})
	versions, _ := spec["versions"].([]interface{})
	version, _ := versions[0].(map[string]interface{})
	schema, _ := version["schema"].(map[string]interface{})
	openAPI, _ := schema["openAPIV3Schema"].(map[string]interface{})
	rootProps, _ := openAPI["properties"].(map[string]interface{})
	specNode, _ := rootProps["spec"].(map[string]interface{})
	specProps, _ := specNode["properties"].(map[string]interface{})
	profileNode, _ := specProps["profile"].(map[string]interface{})
	profileItems, _ := profileNode["items"].(map[string]interface{})
	profileProps, _ := profileItems["properties"].(map[string]interface{})
	thresholdNode, _ := profileProps["ptpClockThreshold"].(map[string]interface{})

	validations, ok := thresholdNode["x-kubernetes-validations"].([]interface{})
	if !assert.True(t, ok, "ptpClockThreshold must carry a cross-field x-kubernetes-validations rule") {
		return
	}
	found := false
	for _, v := range validations {
		validation, _ := v.(map[string]interface{})
		rule, _ := validation["rule"].(string)
		if len(rule) > 0 && strings.Contains(rule, "ptpSourceDisqualifiedThreshold") && strings.Contains(rule, "ptpSourceQualifiedThreshold") {
			found = true
		}
	}
	assert.True(t, found, "cross-field rule must reference both ptpSource thresholds")
}

func TestPtpClockThresholdSourceQualificationJSONRoundTrip(t *testing.T) {
	qualifiedThreshold := int64(100)
	disqualifiedThreshold := int64(200)
	qualifiedSamples := int64(5)
	disqualifiedSamples := int64(6)
	in := &PtpClockThreshold{
		PtpSourceQualifiedThreshold:    &qualifiedThreshold,
		PtpSourceDisqualifiedThreshold: &disqualifiedThreshold,
		PtpSourceQualifiedSamples:      &qualifiedSamples,
		PtpSourceDisqualifiedSamples:   &disqualifiedSamples,
		PtpSourceUseS3:                 false,
	}

	data, err := json.Marshal(in)
	if !assert.NoError(t, err, "PtpClockThreshold must marshal") {
		return
	}

	var out map[string]interface{}
	err = json.Unmarshal(data, &out)
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, float64(100), out["ptpSourceQualifiedThreshold"])
	assert.Equal(t, float64(200), out["ptpSourceDisqualifiedThreshold"])
	assert.Equal(t, float64(5), out["ptpSourceQualifiedSamples"])
	assert.Equal(t, float64(6), out["ptpSourceDisqualifiedSamples"])
	// ptpSourceUseS3 is false and omitempty, so it is omitted from JSON.
	_, hasUseS3 := out["ptpSourceUseS3"]
	assert.False(t, hasUseS3, "false ptpSourceUseS3 must be omitted (omitempty)")

	// Round-trip decode keeps values.
	decoded := &PtpClockThreshold{}
	err = json.Unmarshal(data, decoded)
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, in, decoded)
}

func TestPtpClockThresholdZeroValuesOmittedForBackwardCompat(t *testing.T) {
	// A zero-value PtpClockThreshold (fields unset) must serialize without the new
	// fields so that existing consumers and CRD pruning are unaffected.
	in := &PtpClockThreshold{HoldOverTimeout: 5, MaxOffsetThreshold: 100}

	data, err := json.Marshal(in)
	if !assert.NoError(t, err) {
		return
	}

	var out map[string]interface{}
	err = json.Unmarshal(data, &out)
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, float64(5), out["holdOverTimeout"])
	assert.Equal(t, float64(100), out["maxOffsetThreshold"])
	_, hasQualified := out["ptpSourceQualifiedThreshold"]
	_, hasDisqualified := out["ptpSourceDisqualifiedThreshold"]
	_, hasQualifiedSamples := out["ptpSourceQualifiedSamples"]
	_, hasDisqualifiedSamples := out["ptpSourceDisqualifiedSamples"]
	assert.False(t, hasQualified, "zero ptpSourceQualifiedThreshold must be omitted (omitempty)")
	assert.False(t, hasDisqualified, "zero ptpSourceDisqualifiedThreshold must be omitted (omitempty)")
	assert.False(t, hasQualifiedSamples, "zero ptpSourceQualifiedSamples must be omitted (omitempty)")
	assert.False(t, hasDisqualifiedSamples, "zero ptpSourceDisqualifiedSamples must be omitted (omitempty)")
}

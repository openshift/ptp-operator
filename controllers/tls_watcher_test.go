package controllers

import (
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"
	"github.com/stretchr/testify/assert"

	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/render"
)

func TestSetTLSTemplateData_WithProfile(t *testing.T) {
	profile := *configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	r := &PtpOperatorConfigReconciler{
		TLSProfileSpec: &profile,
	}
	data := render.MakeRenderData()
	r.setTLSTemplateData(&data)

	expectedCiphers := strings.Join(libgocrypto.OpenSSLToIANACipherSuites(profile.Ciphers), ",")
	assert.Equal(t, string(profile.MinTLSVersion), data.Data["TLSMinVersion"])
	assert.Equal(t, expectedCiphers, data.Data["TLSCipherSuites"])
}

func TestSetTLSTemplateData_NilProfileUsesLegacy(t *testing.T) {
	r := &PtpOperatorConfigReconciler{
		TLSProfileSpec: nil,
	}
	data := render.MakeRenderData()
	r.setTLSTemplateData(&data)

	assert.Equal(t, "", data.Data["TLSMinVersion"],
		"TLSMinVersion should be empty when TLSProfileSpec is nil")
	assert.Equal(t, legacyCipherSuites, data.Data["TLSCipherSuites"],
		"TLSCipherSuites should use hardcoded legacy ciphers when TLSProfileSpec is nil")
}

func TestSetTLSTemplateData_ModernProfile(t *testing.T) {
	profile := *configv1.TLSProfiles[configv1.TLSProfileModernType]
	r := &PtpOperatorConfigReconciler{
		TLSProfileSpec: &profile,
	}
	data := render.MakeRenderData()
	r.setTLSTemplateData(&data)

	assert.Equal(t, "VersionTLS13", data.Data["TLSMinVersion"])
	// TLS 1.3 ciphers are not configurable in Go, so cipher list may be empty

	// Modern profile should include groups with PQC hybrid
	if len(profile.Groups) > 0 {
		groups, ok := data.Data["TLSGroups"].(string)
		assert.True(t, ok, "TLSGroups should be set for Modern profile")
		assert.Contains(t, groups, "X25519MLKEM768",
			"Modern profile should include PQC hybrid group")
	}
}

func TestSetTLSTemplateData_WithGroups(t *testing.T) {
	profile := configv1.TLSProfileSpec{
		Ciphers:       configv1.TLSProfiles[configv1.TLSProfileIntermediateType].Ciphers,
		MinTLSVersion: configv1.VersionTLS12,
		Groups: []configv1.TLSGroup{
			configv1.TLSGroupX25519,
			configv1.TLSGroupSecP256r1,
			configv1.TLSGroupSecP384r1,
		},
	}
	r := &PtpOperatorConfigReconciler{
		TLSProfileSpec: &profile,
	}
	data := render.MakeRenderData()
	r.setTLSTemplateData(&data)

	groups, ok := data.Data["TLSGroups"].(string)
	assert.True(t, ok, "TLSGroups should be set")
	assert.Equal(t, "X25519,secp256r1,secp384r1", groups)
}

func TestSetTLSTemplateData_NilProfileNoGroups(t *testing.T) {
	r := &PtpOperatorConfigReconciler{
		TLSProfileSpec: nil,
	}
	data := render.MakeRenderData()
	r.setTLSTemplateData(&data)

	groups, _ := data.Data["TLSGroups"]
	assert.Equal(t, "", groups, "TLSGroups should be empty in legacy mode")
}

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
}

package controllers

import (
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"
	"github.com/stretchr/testify/assert"

	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/render"
)

const legacyCipherSuites = "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256," +
	"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256," +
	"TLS_RSA_WITH_AES_128_CBC_SHA256," +
	"TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256," +
	"TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256"

func TestSetTLSTemplateData_StrictMode(t *testing.T) {
	profile := *configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	r := &PtpOperatorConfigReconciler{
		TLSProfileSpec:     profile,
		TLSAdherencePolicy: configv1.TLSAdherencePolicyStrictAllComponents,
	}
	data := render.MakeRenderData()
	r.setTLSTemplateData(&data)

	expectedCiphers := strings.Join(libgocrypto.OpenSSLToIANACipherSuites(profile.Ciphers), ",")
	assert.Equal(t, string(profile.MinTLSVersion), data.Data["TLSMinVersion"])
	assert.Equal(t, expectedCiphers, data.Data["TLSCipherSuites"])
}

func TestSetTLSTemplateData_LegacyMode(t *testing.T) {
	r := &PtpOperatorConfigReconciler{
		TLSAdherencePolicy: configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly,
	}
	data := render.MakeRenderData()
	r.setTLSTemplateData(&data)

	assert.Equal(t, "", data.Data["TLSMinVersion"],
		"TLSMinVersion should be empty in legacy mode")
	assert.Equal(t, legacyCipherSuites, data.Data["TLSCipherSuites"],
		"TLSCipherSuites should use hardcoded legacy ciphers")
}

func TestSetTLSTemplateData_NoOpinionDefaultsToLegacy(t *testing.T) {
	r := &PtpOperatorConfigReconciler{
		TLSAdherencePolicy: configv1.TLSAdherencePolicyNoOpinion,
	}
	data := render.MakeRenderData()
	r.setTLSTemplateData(&data)

	assert.Equal(t, "", data.Data["TLSMinVersion"],
		"TLSMinVersion should be empty when adherence is unset")
	assert.Equal(t, legacyCipherSuites, data.Data["TLSCipherSuites"],
		"TLSCipherSuites should use hardcoded legacy ciphers when adherence is unset")
}

func TestSetTLSTemplateData_StrictModernProfile(t *testing.T) {
	profile := *configv1.TLSProfiles[configv1.TLSProfileModernType]
	r := &PtpOperatorConfigReconciler{
		TLSProfileSpec:     profile,
		TLSAdherencePolicy: configv1.TLSAdherencePolicyStrictAllComponents,
	}
	data := render.MakeRenderData()
	r.setTLSTemplateData(&data)

	assert.Equal(t, "VersionTLS13", data.Data["TLSMinVersion"])
	// TLS 1.3 ciphers are not configurable in Go, so cipher list may be empty
}

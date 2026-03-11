package controllers

import (
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"
	"github.com/stretchr/testify/assert"

	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/render"
)

func TestTLSProfileTemplateData(t *testing.T) {
	tests := []struct {
		name                string
		profileType         configv1.TLSProfileType
		expectMinVersion    string
		expectCipherContain string
		expectCipherExclude string
	}{
		{
			name:                "Intermediate profile",
			profileType:         configv1.TLSProfileIntermediateType,
			expectMinVersion:    "VersionTLS12",
			expectCipherContain: "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
			expectCipherExclude: "TLS_RSA_WITH_3DES_EDE_CBC_SHA",
		},
		{
			name:                "Old profile",
			profileType:         configv1.TLSProfileOldType,
			expectMinVersion:    "VersionTLS10",
			expectCipherContain: "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
		},
		{
			name:             "Modern profile",
			profileType:      configv1.TLSProfileModernType,
			expectMinVersion: "VersionTLS13",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := *configv1.TLSProfiles[tt.profileType]

			ianaCiphers := libgocrypto.OpenSSLToIANACipherSuites(profile.Ciphers)
			tlsMinVersion := string(profile.MinTLSVersion)
			tlsCipherSuites := strings.Join(ianaCiphers, ",")

			assert.Equal(t, tt.expectMinVersion, tlsMinVersion)

			if tt.expectCipherContain != "" {
				assert.Contains(t, tlsCipherSuites, tt.expectCipherContain,
					"expected cipher suite not found in converted list")
			}
			if tt.expectCipherExclude != "" {
				assert.NotContains(t, tlsCipherSuites, tt.expectCipherExclude,
					"unexpected cipher suite found in converted list")
			}
		})
	}
}

func TestTLSProfileTemplateRendering(t *testing.T) {
	profile := *configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	ianaCiphers := libgocrypto.OpenSSLToIANACipherSuites(profile.Ciphers)

	data := render.MakeRenderData()
	data.Data["TLSMinVersion"] = string(profile.MinTLSVersion)
	data.Data["TLSCipherSuites"] = strings.Join(ianaCiphers, ",")
	// Set all required template variables
	data.Data["Image"] = "test-image"
	data.Data["ImagePullPolicy"] = "IfNotPresent"
	data.Data["Namespace"] = "openshift-ptp"
	data.Data["ReleaseVersion"] = "4.22.0"
	data.Data["KubeRbacProxy"] = "test-rbac-proxy"
	data.Data["SideCar"] = "test-sidecar"
	data.Data["NodeName"] = "test-node"
	data.Data["EnableEventPublisher"] = false
	data.Data["EnabledPlugins"] = "e810"
	data.Data["StorageType"] = "emptyDir"
	data.Data["EventApiVersion"] = "2.0"

	objs, err := render.RenderTemplate("../bindata/linuxptp/ptp-daemon.yaml", &data)
	assert.NoError(t, err)
	assert.NotEmpty(t, objs)

	// Find the DaemonSet
	var dsFound bool
	for _, obj := range objs {
		if obj.GetKind() != "DaemonSet" {
			continue
		}
		dsFound = true

		// Extract kube-rbac-proxy container args
		containers, found, err := unstructuredContainers(obj.Object)
		assert.NoError(t, err)
		assert.True(t, found)

		var rbacProxyArgs []string
		for _, c := range containers {
			container := c.(map[string]interface{})
			if container["name"] == "kube-rbac-proxy" {
				args := container["args"].([]interface{})
				for _, a := range args {
					rbacProxyArgs = append(rbacProxyArgs, a.(string))
				}
				break
			}
		}

		assert.NotEmpty(t, rbacProxyArgs, "kube-rbac-proxy container not found")

		// Verify TLS args are present with correct values
		expectedCiphers := "--tls-cipher-suites=" + strings.Join(ianaCiphers, ",")
		expectedMinVersion := "--tls-min-version=VersionTLS12"
		assert.Contains(t, rbacProxyArgs, expectedCiphers)
		assert.Contains(t, rbacProxyArgs, expectedMinVersion)
	}
	assert.True(t, dsFound, "DaemonSet not found in rendered objects")
}

func unstructuredContainers(obj map[string]interface{}) ([]interface{}, bool, error) {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil, false, nil
	}
	template, ok := spec["template"].(map[string]interface{})
	if !ok {
		return nil, false, nil
	}
	podSpec, ok := template["spec"].(map[string]interface{})
	if !ok {
		return nil, false, nil
	}
	containers, ok := podSpec["containers"].([]interface{})
	if !ok {
		return nil, false, nil
	}
	return containers, true, nil
}

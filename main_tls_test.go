package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	openshifttls "github.com/openshift/controller-runtime-common/pkg/tls"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// Exercise the TLS option used by both the webhook and metrics servers against
// real clients, so dependency updates cannot silently drop profile restrictions.
func TestTLSProfileNegotiation(t *testing.T) {
	tests := []struct {
		name        string
		group       configv1.TLSGroup
		clientCurve tls.CurveID
		minVersion  configv1.TLSProtocolVersion
		maxVersion  uint16
		wantError   bool
	}{
		{"TLS12 P256", configv1.TLSGroupSecP256r1, tls.CurveP256, configv1.VersionTLS12, tls.VersionTLS12, false},
		{"TLS13 P384", configv1.TLSGroupSecP384r1, tls.CurveP384, configv1.VersionTLS13, tls.VersionTLS13, false},
		{"TLS13 P521", configv1.TLSGroupSecP521r1, tls.CurveP521, configv1.VersionTLS13, tls.VersionTLS13, false},
		{"TLS13 X25519", configv1.TLSGroupX25519, tls.X25519, configv1.VersionTLS13, tls.VersionTLS13, false},
		{"TLS13 X25519MLKEM768", configv1.TLSGroupX25519MLKEM768, tls.X25519MLKEM768, configv1.VersionTLS13, tls.VersionTLS13, false},
		{"TLS13 SecP256r1MLKEM768", configv1.TLSGroupSecP256r1MLKEM768, tls.SecP256r1MLKEM768, configv1.VersionTLS13, tls.VersionTLS13, false},
		{"TLS13 SecP384r1MLKEM1024", configv1.TLSGroupSecP384r1MLKEM1024, tls.SecP384r1MLKEM1024, configv1.VersionTLS13, tls.VersionTLS13, false},
		{"empty groups use Go defaults", "", tls.CurveP256, configv1.VersionTLS12, tls.VersionTLS12, false},
		{"reject curve outside profile", configv1.TLSGroupSecP256r1, tls.CurveP384, configv1.VersionTLS13, tls.VersionTLS13, true},
		{"reject version below profile", configv1.TLSGroupSecP256r1, tls.CurveP256, configv1.VersionTLS13, tls.VersionTLS12, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := configv1.TLSProfileSpec{
				MinTLSVersion: tt.minVersion,
				Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
			}
			if tt.group != "" {
				profile.Groups = []configv1.TLSGroup{tt.group}
			}
			tlsOption, unsupported := openshifttls.NewTLSConfigFromProfile(profile)
			require.Empty(t, unsupported)

			server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			server.Config.ErrorLog = log.New(io.Discard, "", 0)
			server.TLS = &tls.Config{}
			tlsOption(server.TLS)
			server.StartTLS()
			defer server.Close()

			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", server.Listener.Addr().String(), &tls.Config{
				RootCAs:          roots,
				MinVersion:       tls.VersionTLS12,
				MaxVersion:       tt.maxVersion,
				CurvePreferences: []tls.CurveID{tt.clientCurve},
				CipherSuites:     []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256},
			})
			if tt.wantError {
				if conn != nil {
					conn.Close()
				}
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			defer conn.Close()
			require.Equal(t, tt.maxVersion, conn.ConnectionState().Version)
			if tt.maxVersion == tls.VersionTLS12 {
				require.Equal(t, uint16(tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256), conn.ConnectionState().CipherSuite)
			}
		})
	}
}

func TestFetchTLSConfig(t *testing.T) {
	expectedProfile := configv1.TLSProfileSpec{
		MinTLSVersion: configv1.VersionTLS13,
		Ciphers:       []string{"TLS_AES_128_GCM_SHA256"},
		Groups:        []configv1.TLSGroup{configv1.TLSGroupSecP256r1MLKEM768, configv1.TLSGroupSecP256r1},
	}
	apiServer := configv1.APIServer{
		TypeMeta: metav1.TypeMeta{APIVersion: configv1.GroupVersion.String(), Kind: "APIServer"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{TLSProfileSpec: expectedProfile},
			},
		},
	}

	resourceRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var response any
		switch r.URL.Path {
		case "/api":
			response = metav1.APIVersions{
				TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "APIVersions"},
				Versions: []string{"v1"},
			}
		case "/apis":
			groupVersion := metav1.GroupVersionForDiscovery{GroupVersion: configv1.GroupVersion.String(), Version: "v1"}
			response = metav1.APIGroupList{
				TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "APIGroupList"},
				Groups: []metav1.APIGroup{{
					Name:             configv1.GroupName,
					Versions:         []metav1.GroupVersionForDiscovery{groupVersion},
					PreferredVersion: groupVersion,
				}},
			}
		case "/apis/config.openshift.io/v1":
			response = metav1.APIResourceList{
				GroupVersion: configv1.GroupVersion.String(),
				APIResources: []metav1.APIResource{{
					Name: "apiservers", SingularName: "apiserver", Kind: "APIServer", Verbs: metav1.Verbs{"get"},
				}},
			}
		case "/apis/config.openshift.io/v1/apiservers/cluster":
			resourceRequests++
			response = apiServer
		default:
			http.NotFound(w, r)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(response))
	}))
	defer server.Close()

	profile, adherence, err := fetchTLSConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	require.Equal(t, expectedProfile, profile)
	require.Equal(t, configv1.TLSAdherencePolicyStrictAllComponents, adherence)
	require.Equal(t, 2, resourceRequests)
}

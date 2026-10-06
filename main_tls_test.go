package main

import (
	"crypto/tls"
	"crypto/x509"
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

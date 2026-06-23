package main

import (
	"crypto/tls"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/assert"
)

func TestTlsGroupsToCurveIDs(t *testing.T) {
	tests := []struct {
		name                string
		groups              []configv1.TLSGroup
		expectedCurves      []tls.CurveID
		expectedUnsupported []string
	}{
		{
			name:           "nil input",
			groups:         nil,
			expectedCurves: nil,
		},
		{
			name:           "empty input",
			groups:         []configv1.TLSGroup{},
			expectedCurves: nil,
		},
		{
			name: "all known groups",
			groups: []configv1.TLSGroup{
				configv1.TLSGroupX25519,
				configv1.TLSGroupSecP256r1,
				configv1.TLSGroupSecP384r1,
				configv1.TLSGroupSecP521r1,
				configv1.TLSGroupX25519MLKEM768,
			},
			expectedCurves: []tls.CurveID{
				tls.X25519,
				tls.CurveP256,
				tls.CurveP384,
				tls.CurveP521,
				tls.X25519MLKEM768,
			},
		},
		{
			name:                "unsupported group",
			groups:              []configv1.TLSGroup{"FutureGroup"},
			expectedCurves:      nil,
			expectedUnsupported: []string{"FutureGroup"},
		},
		{
			name: "mixed known and unknown",
			groups: []configv1.TLSGroup{
				configv1.TLSGroupX25519,
				"UnknownGroup",
				configv1.TLSGroupSecP384r1,
			},
			expectedCurves:      []tls.CurveID{tls.X25519, tls.CurveP384},
			expectedUnsupported: []string{"UnknownGroup"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			curves, unsupported := tlsGroupsToCurveIDs(tt.groups)
			assert.Equal(t, tt.expectedCurves, curves)
			assert.Equal(t, tt.expectedUnsupported, unsupported)
		})
	}
}

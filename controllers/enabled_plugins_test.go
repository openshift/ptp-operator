package controllers

import (
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
)

func TestEnabledPluginNames(t *testing.T) {
	explicitPlugins := map[string]*apiextensions.JSON{
		"custom-plugin": {Raw: []byte(`{"enabled":true}`)},
		"e825":          nil,
	}
	emptyPlugins := map[string]*apiextensions.JSON{}

	tests := []struct {
		name    string
		plugins *map[string]*apiextensions.JSON
		want    string
	}{
		{
			name: "default plugins include phc-first-step",
			want: "e810,e825,e830,ntpfailover,phc-first-step",
		},
		{
			name:    "explicit plugin names replace defaults and pass through generically",
			plugins: &explicitPlugins,
			want:    "custom-plugin,e825",
		},
		{
			name:    "explicit empty map disables defaults",
			plugins: &emptyPlugins,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ptpv1.PtpOperatorConfig{
				Spec: ptpv1.PtpOperatorConfigSpec{EnabledPlugins: tt.plugins},
			}
			if got := enabledPluginNames(cfg); got != tt.want {
				t.Errorf("enabledPluginNames() = %q, want %q", got, tt.want)
			}
		})
	}
}

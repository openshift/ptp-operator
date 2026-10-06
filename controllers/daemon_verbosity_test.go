package controllers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/render"
)

func TestResolveDaemonVerbosity(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     string
		wantErr  bool
	}{
		{
			name:     "unset uses default",
			envValue: "",
			want:     "10",
			wantErr:  false,
		},
		{
			name:     "zero passes through",
			envValue: "0",
			want:     "0",
			wantErr:  false,
		},
		{
			name:     "mid-range passes through",
			envValue: "5",
			want:     "5",
			wantErr:  false,
		},
		{
			name:     "default value passes through",
			envValue: "10",
			want:     "10",
			wantErr:  false,
		},
		{
			name:     "max passes through",
			envValue: "14",
			want:     "14",
			wantErr:  false,
		},
		{
			name:     "non-numeric falls back to default",
			envValue: "abc",
			want:     "10",
			wantErr:  true,
		},
		{
			name:     "negative falls back to default",
			envValue: "-1",
			want:     "10",
			wantErr:  true,
		},
		{
			name:     "above max falls back to default",
			envValue: "15",
			want:     "10",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveDaemonVerbosity(tt.envValue)
			assert.Equal(t, tt.want, got)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDaemonVerbosityFromEnv(t *testing.T) {
	t.Setenv("LINUXPTP_VERBOSITY", "")
	got, err := daemonVerbosityFromEnv()
	assert.NoError(t, err)
	assert.Equal(t, "10", got)

	t.Setenv("LINUXPTP_VERBOSITY", "6")
	got, err = daemonVerbosityFromEnv()
	assert.NoError(t, err)
	assert.Equal(t, "6", got)

	t.Setenv("LINUXPTP_VERBOSITY", "abc")
	got, err = daemonVerbosityFromEnv()
	assert.Error(t, err)
	assert.Equal(t, "10", got)
}

func daemonContainerArgs(t *testing.T, data *render.RenderData) []string {
	t.Helper()
	data.Data["TLSMinVersion"] = "VersionTLS12"
	data.Data["TLSCipherSuites"] = "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"
	objs, err := render.RenderTemplate("../bindata/linuxptp/ptp-daemon.yaml", data)
	assert.NoError(t, err)
	assert.NotEmpty(t, objs)

	for _, obj := range objs {
		if obj.GetKind() != "DaemonSet" {
			continue
		}
		containers, found, err := unstructuredContainers(obj.Object)
		assert.NoError(t, err)
		assert.True(t, found)
		for _, c := range containers {
			container := c.(map[string]interface{})
			if container["name"] != "linuxptp-daemon-container" {
				continue
			}
			args := container["args"].([]interface{})
			var out []string
			for _, a := range args {
				out = append(out, a.(string))
			}
			assert.NotEmpty(t, out)
			return out
		}
	}
	t.Fatal("linuxptp-daemon-container not found in rendered objects")
	return nil
}

func TestDaemonVerbosityDefaultRendering(t *testing.T) {
	data := makeTestRenderData()
	data.Data["Verbosity"] = "10"

	args := daemonContainerArgs(t, data)
	assert.Contains(t, args[0], "-v 10")
}

func TestDaemonVerbosityCustomRendering(t *testing.T) {
	data := makeTestRenderData()
	data.Data["Verbosity"] = "6"

	args := daemonContainerArgs(t, data)
	assert.Contains(t, args[0], "-v 6")

	data = makeTestRenderData()
	data.Data["Verbosity"] = "6"
	data.Data["EnableEventPublisher"] = true

	args = daemonContainerArgs(t, data)
	assert.Contains(t, args[0], "cloud event proxy to start")
	assert.Contains(t, args[0], "-v 6")
}

func TestControllerInvalidVerbosityRendersDefault(t *testing.T) {
	t.Setenv("LINUXPTP_VERBOSITY", "abc")
	verbosity, err := daemonVerbosityFromEnv()
	assert.Error(t, err)

	data := makeTestRenderData()
	data.Data["Verbosity"] = verbosity

	args := daemonContainerArgs(t, data)
	assert.Contains(t, args[0], "-v 10")
}

func TestDaemonVerbosityDoesNotAlterProtocolArgs(t *testing.T) {
	dataLow := makeTestRenderData()
	dataLow.Data["Verbosity"] = "0"
	low := daemonContainerArgs(t, dataLow)[0]

	dataHigh := makeTestRenderData()
	dataHigh.Data["Verbosity"] = "10"
	high := daemonContainerArgs(t, dataHigh)[0]

	stripLevel := func(cmd string) string {
		return strings.ReplaceAll(strings.ReplaceAll(cmd, " -v 0", ""), " -v 10", "")
	}
	assert.Equal(t, stripLevel(low), stripLevel(high))
	assert.Contains(t, low, "-v 0")
	assert.Contains(t, high, "-v 10")
}

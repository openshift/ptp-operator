package pods

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	testclient "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
)

const phc2sysSelectionRegex = `phc2sys(?m).*?:.* selecting (\w+) as out-of-domain source clock`

func withLogServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	clientset, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)

	previous := testclient.Client
	testclient.Client = &testclient.ClientSet{Interface: clientset}
	t.Cleanup(func() { testclient.Client = previous })
}

func TestGetPodLogsRegexSinceReturnsAllExistingMatches(t *testing.T) {
	var followCalls atomic.Int32
	withLogServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("follow") == "true" {
			followCalls.Add(1)
		}
		fmt.Fprint(w,
			"2026-10-23T15:26:39Z phc2sys[832.876]: [phc2sys.2.config:6] selecting ens2f0 as out-of-domain source clock\n",
			"2026-10-23T15:26:40Z phc2sys[840.878]: [phc2sys.2.config:6] selecting ens1f0 as out-of-domain source clock\n",
		)
	})

	matches, err := GetPodLogsRegexSince(
		"test-ns", "test-pod", "linuxptp-daemon-container",
		phc2sysSelectionRegex, false, 2*time.Second, time.Now().Add(-time.Minute))

	require.NoError(t, err)
	require.Len(t, matches, 2, "both selections already present in the logs must be returned")
	assert.Equal(t, "ens2f0", matches[0][1])
	assert.Equal(t, "ens1f0", matches[1][1])
	assert.Zero(t, followCalls.Load(), "existing logs must be read without following")
}

func TestGetPodLogsRegexSinceFallsBackToFollow(t *testing.T) {
	var followCalls atomic.Int32
	withLogServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("follow") != "true" {
			return
		}
		followCalls.Add(1)
		fmt.Fprint(w, "2026-10-23T15:26:40Z phc2sys[840.878]: [phc2sys.2.config:6] selecting ens1f0 as out-of-domain source clock\n")
	})

	matches, err := GetPodLogsRegexSince(
		"test-ns", "test-pod", "linuxptp-daemon-container",
		phc2sysSelectionRegex, false, 2*time.Second, time.Now())

	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, "ens1f0", matches[0][1])
	assert.Equal(t, int32(1), followCalls.Load(), "must follow the stream when no existing match is found")
}

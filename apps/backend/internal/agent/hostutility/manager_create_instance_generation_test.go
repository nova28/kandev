package hostutility

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agent/registry"
	agentctlclient "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/stretchr/testify/require"
)

// blockingCreateInstanceControlServer answers /health checks against created
// instances with 200 OK, but blocks every POST /api/v1/instances until the
// test releases it, so a test can pause createInstance mid-flight and
// publish a relaunch while the request is still outstanding on the original
// server.
func newBlockingCreateInstanceControlServer(t *testing.T, instancePort int) (server *httptest.Server, received, release chan struct{}) {
	t.Helper()
	received = make(chan struct{})
	release = make(chan struct{})
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/instances":
			close(received)
			<-release
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			err := json.NewEncoder(w).Encode(agentctlclient.CreateInstanceResponse{
				ID:   "fresh-instance",
				Port: instancePort,
			})
			require.NoError(t, err)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, received, release
}

// TestCreateInstanceUsesIdentityCapturedBeforeRequest covers RV2-2:
// createInstance dispatched via m.controlClient (resolving the endpoint
// internally, per request) and then read m.endpoint.Current() a second,
// independent time to stamp generation -- two reads separated by however
// long the create-instance round trip takes. A relaunch landing between them
// recorded the new (non-stale) generation on an instance whose client still
// pointed at the old, now-replaced server's port, so staleInstanceCause's
// generation comparison would never flag it as stale. Capturing one identity
// before dispatching the request and reusing it for both the request and the
// recorded generation closes that window.
func TestCreateInstanceUsesIdentityCapturedBeforeRequest(t *testing.T) {
	log := newTestLogger(t)
	reg := registry.NewRegistry(log)

	instanceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer instanceServer.Close()
	_, instancePort := serverHostPort(t, instanceServer)

	controlServer, received, release := newBlockingCreateInstanceControlServer(t, instancePort)
	controlHost, controlPort := serverHostPort(t, controlServer)

	ep := agentctlclient.NewEndpoint()
	ep.Publish(controlHost, controlPort, "token-gen1", 111) // generation 1

	mgr := NewManager(reg, ep, agentctlclient.NewControlClient(controlHost, controlPort, log), log)
	mgr.parentTmpDir = t.TempDir()

	type result struct {
		inst *instance
		err  error
	}
	done := make(chan result, 1)
	go func() {
		inst, err := mgr.createInstance(context.Background(), "gen-acp")
		done <- result{inst, err}
	}()

	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the create-instance request to reach the original server")
	}

	// Simulate a relaunch completing while the request above is still in
	// flight: a new generation is published pointing at a different (here,
	// unreachable) server. The request already in flight keeps talking to
	// the original server and must be attributed to it, not to this new
	// generation.
	ep.Publish("127.0.0.1", 1, "token-gen2", 222)

	close(release)

	var res result
	select {
	case res = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for createInstance to return")
	}
	require.NoError(t, res.err)
	require.Equal(t, uint64(1), res.inst.generation,
		"generation must match the identity that actually served the create-instance request, not whatever was current when it returned")
}

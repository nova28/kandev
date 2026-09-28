package hostutility

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kandev/kandev/internal/agent/registry"
	agentctlclient "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	agentctlutil "github.com/kandev/kandev/internal/agentctl/server/utility"
	"github.com/stretchr/testify/require"
)

// TestStaleInstanceCauseDetectsGenerationMismatchWithoutHealthCheck proves a
// cached instance bound to an older standalone agentctl generation is
// recognized stale from the generation alone. This matters because a
// relaunched agentctl process can still answer a generic /health check on
// its new endpoint with 200 OK even though it has never heard of the old
// instance ID -- a plain health check alone would report the dead instance
// as healthy.
func TestStaleInstanceCauseDetectsGenerationMismatchWithoutHealthCheck(t *testing.T) {
	log := newTestLogger(t)
	reg := registry.NewRegistry(log)

	var healthCalls atomic.Int32
	instanceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		healthCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer instanceServer.Close()
	instanceHost, instancePort := serverHostPort(t, instanceServer)

	ep := agentctlclient.NewEndpoint()
	ep.Publish(instanceHost, instancePort, "", 0)

	mgr := NewManager(reg, ep, agentctlclient.NewControlClient(instanceHost, 0, log), log)

	inst := &instance{
		agentType:  "gen-acp",
		instanceID: "stale-gen-instance",
		client: agentctlclient.NewClient(instanceHost, instancePort, log,
			agentctlclient.WithExecutionID("stale-gen-instance")),
		generation: 1,
	}

	// A relaunch republishes a new generation on the (possibly identical)
	// host and port.
	ep.Publish(instanceHost, instancePort, "", 0)

	err := mgr.staleInstanceCause(context.Background(), inst)
	require.Error(t, err)
	require.Contains(t, err.Error(), "generation")
	require.Equal(t, int32(0), healthCalls.Load(),
		"a generation mismatch must short-circuit before the health round trip")
}

// TestGetInstanceRecreatesAfterAgentctlRelaunch is the end-to-end companion:
// a cached instance bound to generation 1 must be dropped and replaced the
// first time it is used after the endpoint observes generation 2, even
// though the instance's own client would still report healthy against
// whatever now answers on its cached host/port.
func TestGetInstanceRecreatesAfterAgentctlRelaunch(t *testing.T) {
	log := newTestLogger(t)
	reg := registry.NewRegistry(log)
	const agentType = "relaunch-acp"
	require.NoError(t, reg.Register(&installedInferenceAgent{id: agentType}))

	instanceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			// Answers 200 unconditionally, including for the stale instance's
			// client -- proving the generation check, not a failed health
			// check, is what forces the recreate.
			w.WriteHeader(http.StatusOK)
		case "/api/v1/inference/prompt":
			if got := r.Header.Get("X-Instance-ID"); got != "fresh-relaunch-instance" {
				http.Error(w, "wrong instance id", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			err := json.NewEncoder(w).Encode(agentctlutil.PromptResponse{
				Success:  true,
				Response: "summary",
				Model:    "test-model",
			})
			require.NoError(t, err)
		default:
			http.NotFound(w, r)
		}
	}))
	defer instanceServer.Close()
	instanceHost, instancePort := serverHostPort(t, instanceServer)

	var createCalls atomic.Int32
	var deleteCalls atomic.Int32
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/instances":
			createCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			err := json.NewEncoder(w).Encode(agentctlclient.CreateInstanceResponse{
				ID:   "fresh-relaunch-instance",
				Port: instancePort,
			})
			require.NoError(t, err)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/instances/"):
			deleteCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controlServer.Close()
	controlHost, controlPort := serverHostPort(t, controlServer)

	ep := agentctlclient.NewEndpoint()
	ep.Publish(controlHost, controlPort, "", 0) // generation 1

	mgr := NewManager(reg, ep, agentctlclient.NewControlClient(controlHost, controlPort, log), log)
	mgr.parentTmpDir = t.TempDir()

	staleWorkDir := t.TempDir()
	mgr.instances[agentType] = &instance{
		agentType:  agentType,
		instanceID: "stale-relaunch-instance",
		workDir:    staleWorkDir,
		client: agentctlclient.NewClient(instanceHost, instancePort, log,
			agentctlclient.WithExecutionID("stale-relaunch-instance")),
		generation: 1,
	}

	ep.Publish(controlHost, controlPort, "", 0) // relaunch: generation 2

	result, err := mgr.ExecutePrompt(context.Background(), agentType, "test-model", "", "Summarize this")
	require.NoError(t, err)
	require.Equal(t, "summary", result.Response)
	require.Equal(t, int32(1), createCalls.Load(), "stale-generation instance must be recreated, not reused")
	require.Equal(t, int32(1), deleteCalls.Load(), "stale-generation instance must be deleted from agentctl")

	mgr.mu.RLock()
	fresh := mgr.instances[agentType]
	mgr.mu.RUnlock()
	require.NotNil(t, fresh)
	require.Equal(t, "fresh-relaunch-instance", fresh.instanceID)
	require.Equal(t, uint64(2), fresh.generation)

	mgr.Stop(context.Background())
	require.Equal(t, int32(2), deleteCalls.Load())
}

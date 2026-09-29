package lifecycle

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	agentctlclient "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/task/models"
)

// blockingListInstancesServer answers /health immediately but blocks every
// GET /api/v1/instances until the test releases it, so a test can pause
// enumeration mid-flight and publish a relaunch while the request is still
// outstanding on the original server.
type blockingListInstancesServer struct {
	server    *httptest.Server
	received  chan struct{}
	release   chan struct{}
	port      int
	instances []*agentctlclient.InstanceInfo
}

func newBlockingListInstancesServer(t *testing.T) *blockingListInstancesServer {
	t.Helper()
	s := &blockingListInstancesServer{
		received: make(chan struct{}),
		release:  make(chan struct{}),
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case r.URL.Path == "/api/v1/instances" && r.Method == http.MethodGet:
			close(s.received)
			<-s.release
			_ = json.NewEncoder(w).Encode(struct {
				Instances []*agentctlclient.InstanceInfo `json:"instances"`
			}{Instances: s.instances})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.server.Close)

	parsed, err := url.Parse(s.server.URL)
	if err != nil {
		t.Fatalf("parse control URL: %v", err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatalf("parse control port: %v", err)
	}
	s.port = port
	return s
}

func (s *blockingListInstancesServer) host() string {
	parsed, _ := url.Parse(s.server.URL)
	return parsed.Hostname()
}

// TestStandaloneExecutorRecoverInstancesUsesIdentityCapturedBeforeEnumeration
// covers RV2-1: RecoverInstances enumerated instances once via
// listInstancesWithRetry, fixing each instance's Port at that moment, then
// buildRecoveredInstances read r.endpoint.Current() a second, independent
// time to stamp Generation -- two reads separated by the
// correlation/stop-dispatch window. A relaunch landing between them recorded
// the new (non-stale) generation on an ExecutorInstance whose StandalonePort
// still belonged to the old, now-replaced server, defeating ExecutionStore's
// stale-generation retirement. Capturing one identity before enumeration and
// reusing it for both the enumeration request and the recorded Generation
// closes that window.
func TestStandaloneExecutorRecoverInstancesUsesIdentityCapturedBeforeEnumeration(t *testing.T) {
	original := newBlockingListInstancesServer(t)
	original.instances = []*agentctlclient.InstanceInfo{
		{ID: "std-1", Port: original.port, SessionID: "session-1", Status: "running"},
	}

	endpoint := agentctlclient.NewEndpoint()
	endpoint.Publish(original.host(), original.port, "token-gen1", 111)

	ctl := agentctlclient.NewControlClientFromEndpoint(endpoint, newTestLogger())
	exec := NewStandaloneExecutor(ctl, endpoint, newTestLogger())

	records := []*models.ExecutorRunning{
		{SessionID: "session-1", TaskID: "task-1", WorktreePath: "/workspace", AgentExecutionID: "std-1"},
	}

	type result struct {
		instances []*ExecutorInstance
		err       error
	}
	done := make(chan result, 1)
	go func() {
		instances, err := exec.RecoverInstances(context.Background(), records)
		done <- result{instances, err}
	}()

	select {
	case <-original.received:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the enumeration request to reach the original server")
	}

	// Simulate a relaunch completing while enumeration above is still in
	// flight: a new generation is published pointing at a different (here,
	// unreachable) server. The enumeration already in flight keeps talking
	// to the original server and must be attributed to it, not to this new
	// generation.
	endpoint.Publish("127.0.0.1", 1, "token-gen2", 222)

	close(original.release)

	var res result
	select {
	case res = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for RecoverInstances to return")
	}
	if res.err != nil {
		t.Fatalf("RecoverInstances: %v", res.err)
	}
	if len(res.instances) != 1 {
		t.Fatalf("len(instances) = %d, want 1", len(res.instances))
	}

	got := res.instances[0]
	if got.Generation != 1 {
		t.Fatalf("Generation = %d, want 1 (the generation that actually served the enumeration)", got.Generation)
	}
	if got.StandalonePort != original.port {
		t.Fatalf("StandalonePort = %d, want %d (the original server's response)", got.StandalonePort, original.port)
	}
}

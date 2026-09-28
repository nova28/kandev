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
)

// blockingCreateInstanceServer answers /health immediately but blocks every
// POST /api/v1/instances until the test releases it, so a test can pause
// CreateInstance mid-flight and publish a relaunch while the request is
// still outstanding on the original server.
type blockingCreateInstanceServer struct {
	server   *httptest.Server
	received chan struct{}
	release  chan struct{}
	port     int
}

func newBlockingCreateInstanceServer(t *testing.T) *blockingCreateInstanceServer {
	t.Helper()
	s := &blockingCreateInstanceServer{
		received: make(chan struct{}),
		release:  make(chan struct{}),
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case r.URL.Path == "/api/v1/instances" && r.Method == http.MethodPost:
			close(s.received)
			<-s.release
			_ = json.NewEncoder(w).Encode(agentctlclient.CreateInstanceResponse{ID: "std-1", Port: s.port})
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

func (s *blockingCreateInstanceServer) host() string {
	parsed, _ := url.Parse(s.server.URL)
	return parsed.Hostname()
}

// TestStandaloneExecutorCreateInstanceUsesIdentityCapturedBeforeRequest
// covers RV1-2: CreateInstance resolved r.ctl's request identity and
// r.endpoint.Current() at two independent points, so a relaunch landing
// between them recorded the new (non-stale) generation on an
// ExecutorInstance whose actual port still belongs to the old, now-replaced
// server -- defeating ExecutionStore's stale-generation retirement, since
// the misattributed execution looks current. Capturing one identity before
// dispatching the request and using it for both the request and the
// recorded Generation closes that window: the instance must be attributed
// to the server that actually served it, generation 1, not the generation
// published while the request was still in flight.
func TestStandaloneExecutorCreateInstanceUsesIdentityCapturedBeforeRequest(t *testing.T) {
	original := newBlockingCreateInstanceServer(t)

	endpoint := agentctlclient.NewEndpoint()
	endpoint.Publish(original.host(), original.port, "token-gen1", 111)

	ctl := agentctlclient.NewControlClientFromEndpoint(endpoint, newTestLogger())
	exec := NewStandaloneExecutor(ctl, endpoint, newTestLogger())

	type result struct {
		instance *ExecutorInstance
		err      error
	}
	done := make(chan result, 1)
	go func() {
		instance, err := exec.CreateInstance(context.Background(), &ExecutorCreateRequest{
			InstanceID:    "instance-1",
			TaskID:        "task-1",
			SessionID:     "session-1",
			WorkspacePath: "/workspace",
		})
		done <- result{instance, err}
	}()

	select {
	case <-original.received:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the create-instance request to reach the original server")
	}

	// Simulate a relaunch completing while the request above is still
	// in flight: a new generation is published pointing at a different
	// (here, unreachable) server. The request already in flight keeps
	// talking to the original server and must be attributed to it.
	endpoint.Publish("127.0.0.1", 1, "token-gen2", 222)

	close(original.release)

	var res result
	select {
	case res = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for CreateInstance to return")
	}
	if res.err != nil {
		t.Fatalf("CreateInstance: %v", res.err)
	}

	if res.instance.Generation != 1 {
		t.Fatalf("Generation = %d, want 1 (the generation that actually served the request)", res.instance.Generation)
	}
	if res.instance.StandalonePort != original.port {
		t.Fatalf("StandalonePort = %d, want %d (the original server's response)", res.instance.StandalonePort, original.port)
	}
}

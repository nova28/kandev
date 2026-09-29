package websocket

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// TestVscodeResolveProxyDoesNotDeadlockOnStaleGenerationRetirement covers
// RV2-3: resolveProxy previously held h.mu across the whole function,
// including the call to GetExecutionBySessionID. That call can synchronously
// retire a generation-stale execution and invoke the execution-retired hook
// -- wired in production to InvalidateProxy on this same handler. Because
// sync.Mutex is not reentrant, the hook re-acquiring h.mu from inside the
// call resolveProxy is still blocked on, on the same goroutine, deadlocked
// every request through this handler permanently. Moving the resolution work
// outside h.mu (reacquiring only to install the resolved cache entry) breaks
// the cycle.
func TestVscodeResolveProxyDoesNotDeadlockOnStaleGenerationRetirement(t *testing.T) {
	log, _ := observedTerminalLogger(t)
	upstream := newFakeCodeServer(t, "running")

	manager := newProxyTestManager(t, log)
	manager.SetAgentctlGeneration(1)
	execution := addExecutionForURL(t, manager, "sess-stale", upstream.server.URL, "", log)
	execution.SetGenerationForTesting(1)

	handler := NewVscodeProxyHandler(manager, log)
	manager.SetExecutionRetiredHook(handler.InvalidateProxy)

	// Simulate a relaunch: the execution above is now bound to a dead
	// generation, so the next accessor to observe it retires it and
	// synchronously invokes the hook just installed.
	manager.SetAgentctlGeneration(2)

	type result struct {
		status int
		err    error
	}
	done := make(chan result, 1)
	go func() {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/vscode/sess-stale/", nil)
		_, err := handler.resolveProxy(c, "sess-stale")
		done <- result{status: recorder.Code, err: err}
	}()

	select {
	case res := <-done:
		if res.err == nil {
			t.Fatalf("resolveProxy err = nil, want an error for a retired/stale execution")
		}
		if res.status != http.StatusNotFound {
			t.Fatalf("status = %d, want %d (the stale execution must be reported not found)", res.status, http.StatusNotFound)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out: resolveProxy deadlocked retiring a stale-generation execution while holding its own cache lock")
	}
}

// TestPortProxyResolveProxyDoesNotDeadlockOnStaleGenerationRetirement is the
// PortProxyHandler counterpart of the vscode-proxy deadlock regression above:
// the same lock-held-across-retirement shape existed byte-for-byte in
// PortProxyHandler.resolveProxy, invalidated via InvalidateSession.
func TestPortProxyResolveProxyDoesNotDeadlockOnStaleGenerationRetirement(t *testing.T) {
	log, _ := observedTerminalLogger(t)
	upstream := newFakeAgentctl(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	manager := newProxyTestManager(t, log)
	manager.SetAgentctlGeneration(1)
	execution := addExecutionForURL(t, manager, "sess-stale", upstream.server.URL, "", log)
	execution.SetGenerationForTesting(1)

	handler := NewPortProxyHandler(manager, log)
	manager.SetExecutionRetiredHook(handler.InvalidateSession)

	// Simulate a relaunch: the execution above is now bound to a dead
	// generation, so the next accessor to observe it retires it and
	// synchronously invokes the hook just installed.
	manager.SetAgentctlGeneration(2)

	type result struct {
		status int
		err    error
	}
	done := make(chan result, 1)
	go func() {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/port-proxy/sess-stale/3000/", nil)
		_, err := handler.resolveProxy(c, "sess-stale", 3000)
		done <- result{status: recorder.Code, err: err}
	}()

	select {
	case res := <-done:
		if res.err == nil {
			t.Fatalf("resolveProxy err = nil, want an error for a retired/stale execution")
		}
		if res.status != http.StatusNotFound {
			t.Fatalf("status = %d, want %d (the stale execution must be reported not found)", res.status, http.StatusNotFound)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out: resolveProxy deadlocked retiring a stale-generation execution while holding its own cache lock")
	}
}

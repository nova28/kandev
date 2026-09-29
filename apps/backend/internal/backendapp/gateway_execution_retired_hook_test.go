package backendapp

import (
	"reflect"
	"testing"
)

// TestExecutionRetiredInvalidationHookInvalidatesAllThreeCaches pins the
// wiring gateway.go's provideGateway installs on
// lifecycle.Manager.SetExecutionRetiredHook (RV2-10): a single retirement for
// one session ID must invalidate the vscode proxy, port proxy, and port
// tunnel caches, all with that same session ID. A relaunch's replacement
// session reuses the retired execution's session ID, so missing any one of
// the three would leave a stale cache entry serving traffic into the dead
// process.
func TestExecutionRetiredInvalidationHookInvalidatesAllThreeCaches(t *testing.T) {
	var calls []string
	hook := executionRetiredInvalidationHook(
		func(sessionID string) { calls = append(calls, "proxy:"+sessionID) },
		func(sessionID string) { calls = append(calls, "port:"+sessionID) },
		func(sessionID string) { calls = append(calls, "tunnel:"+sessionID) },
	)

	hook("session-1")

	want := []string{"proxy:session-1", "port:session-1", "tunnel:session-1"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

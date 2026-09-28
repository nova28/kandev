package lifecycle

import "testing"

// TestManagerRetireStaleExecutionRunsHookAndCleansUp exercises WO2's
// Manager-level wiring end to end: NewManager builds executionStore with a
// generation source and retire callback that close over the not-yet-built
// Manager (see NewManager's var mgr *Manager pattern); once
// SetAgentctlGeneration reports a newer generation than an execution's own,
// the next accessor to observe it must retire it and invoke the
// execution-retired hook with the session ID, so gateway caches keyed on
// that session (VS Code proxy, port proxy/tunnel) get invalidated.
func TestManagerRetireStaleExecutionRunsHookAndCleansUp(t *testing.T) {
	log := newTestRegistryLogger()
	mgr := NewManager(nil, nil, nil, nil, nil, nil, ExecutorFallbackWarn, "", log)

	var retiredSessions []string
	mgr.SetExecutionRetiredHook(func(sessionID string) {
		retiredSessions = append(retiredSessions, sessionID)
	})

	mgr.SetAgentctlGeneration(1)
	exec := &AgentExecution{ID: "exec-1", SessionID: "session-1", generation: 1}
	if err := mgr.executionStore.Add(exec); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Still current generation: visible, hook not called.
	if _, ok := mgr.executionStore.Get("exec-1"); !ok {
		t.Fatal("Get: execution unexpectedly hidden while generation is current")
	}
	if len(retiredSessions) != 0 {
		t.Fatalf("retiredSessions = %v, want none before a relaunch", retiredSessions)
	}

	// Simulate a relaunch: the endpoint subscribe callback in backendapp
	// calls this on every published identity, including the new generation.
	mgr.SetAgentctlGeneration(2)

	if _, ok := mgr.executionStore.Get("exec-1"); ok {
		t.Fatal("Get: execution bound to the dead generation must be hidden")
	}
	if _, ok := mgr.executionStore.Get("exec-1"); ok {
		t.Fatal("Get (second call): retired execution must stay hidden")
	}

	if want := []string{"session-1"}; len(retiredSessions) != len(want) || retiredSessions[0] != want[0] {
		t.Fatalf("retiredSessions = %v, want exactly one retirement of session-1", retiredSessions)
	}
}

// TestManagerSetAgentctlGenerationZeroValueDisablesRetirement pins that a
// Manager built without ever observing a published generation (tests, or a
// build before the endpoint subscribe callback fires) never retires
// generation-bound executions -- current generation 0 disables the check,
// matching ExecutionStore's own nil-source contract for pre-existing callers.
func TestManagerSetAgentctlGenerationZeroValueDisablesRetirement(t *testing.T) {
	log := newTestRegistryLogger()
	mgr := NewManager(nil, nil, nil, nil, nil, nil, ExecutorFallbackWarn, "", log)

	exec := &AgentExecution{ID: "exec-1", SessionID: "session-1", generation: 7}
	if err := mgr.executionStore.Add(exec); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if _, ok := mgr.executionStore.Get("exec-1"); !ok {
		t.Fatal("Get: execution must remain visible before any generation is observed")
	}
}

package lifecycle

import (
	"testing"
	"testing/synctest"
	"time"
)

// TestManagerRetireStaleExecutionRunsHookAndCleansUp exercises WO2's
// Manager-level wiring end to end: NewManager builds executionStore with a
// generation source and retire callback that close over the not-yet-built
// Manager (see NewManager's var mgr *Manager pattern); once
// SetAgentctlGeneration reports a newer generation than an execution's own,
// the next accessor to observe it must retire it and invoke the
// execution-retired hook with the session ID, so gateway caches keyed on
// that session (VS Code proxy, port proxy/tunnel) get invalidated.
func TestManagerRetireStaleExecutionRunsHookAndCleansUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := newTestRegistryLogger()
		mgr := NewManager(nil, nil, nil, nil, nil, nil, ExecutorFallbackWarn, "", log)

		var retiredSessions []string
		mgr.SetExecutionRetiredHook(func(sessionID string) {
			retiredSessions = append(retiredSessions, sessionID)
		})

		mgr.SetAgentctlGeneration(1)
		synctest.Wait()
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
		// calls this on every published identity, including the new
		// generation. The sweep it schedules runs on its own goroutine (see
		// RV3-2/SetAgentctlGeneration), so synctest.Wait lets every assertion
		// below observe its result deterministically instead of racing it.
		mgr.SetAgentctlGeneration(2)
		synctest.Wait()

		if _, ok := mgr.executionStore.Get("exec-1"); ok {
			t.Fatal("Get: execution bound to the dead generation must be hidden")
		}
		if _, ok := mgr.executionStore.Get("exec-1"); ok {
			t.Fatal("Get (second call): retired execution must stay hidden")
		}

		if want := []string{"session-1"}; len(retiredSessions) != len(want) || retiredSessions[0] != want[0] {
			t.Fatalf("retiredSessions = %v, want exactly one retirement of session-1", retiredSessions)
		}
	})
}

// TestSetAgentctlGenerationEagerlyRetiresStaleExecutionsWithoutAnAccessor
// covers RV2-4: before the eager sweep, executionStore only ever retired a
// stale-generation execution (and invoked the retired hook that invalidates
// gateway caches) as a side effect of some other accessor -- Get,
// GetBySessionID, List -- happening to touch that exact execution again.
// A relaunch that bumped the generation while nothing else queried the
// affected session left its VS Code/port proxy cache pointed at the dead
// server indefinitely. This test bumps the generation and asserts the retire
// hook already fired, with no accessor call in between.
func TestSetAgentctlGenerationEagerlyRetiresStaleExecutionsWithoutAnAccessor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := newTestRegistryLogger()
		mgr := NewManager(nil, nil, nil, nil, nil, nil, ExecutorFallbackWarn, "", log)

		var retiredSessions []string
		mgr.SetExecutionRetiredHook(func(sessionID string) {
			retiredSessions = append(retiredSessions, sessionID)
		})

		mgr.SetAgentctlGeneration(1)
		synctest.Wait()
		exec := &AgentExecution{ID: "exec-1", SessionID: "session-1", generation: 1}
		if err := mgr.executionStore.Add(exec); err != nil {
			t.Fatalf("Add: %v", err)
		}

		// Relaunch: bump the generation without ever calling Get,
		// GetBySessionID, or List for this execution again. The sweep this
		// schedules runs on its own goroutine (RV3-2); synctest.Wait blocks
		// until it has run to completion before the assertion below.
		mgr.SetAgentctlGeneration(2)
		synctest.Wait()

		if want := []string{"session-1"}; len(retiredSessions) != len(want) || retiredSessions[0] != want[0] {
			t.Fatalf("retiredSessions = %v, want exactly one immediate retirement of session-1", retiredSessions)
		}
	})
}

// TestSetAgentctlGenerationDoesNotBlockOnRetireCallback pins RV3-2's core
// contract: SetAgentctlGeneration is wired as the agentctl Endpoint's
// Subscribe observer (backendapp/agents.go), and Endpoint.Subscribe
// documents that an observer "must not block". Before the fix, the sweep
// (and every retirement's cleanup + retired hook) ran inline, so a slow or
// blocked hook would have blocked the observer -- and, during a relaunch,
// everything else waiting on Endpoint.mu or agentctlSupervisor.mu. This
// drives a retire hook that blocks until released and asserts
// SetAgentctlGeneration itself still returns promptly.
func TestSetAgentctlGenerationDoesNotBlockOnRetireCallback(t *testing.T) {
	log := newTestRegistryLogger()
	mgr := NewManager(nil, nil, nil, nil, nil, nil, ExecutorFallbackWarn, "", log)

	hookStarted := make(chan struct{})
	release := make(chan struct{})
	mgr.SetExecutionRetiredHook(func(sessionID string) {
		close(hookStarted)
		<-release
	})

	mgr.SetAgentctlGeneration(1)
	exec := &AgentExecution{ID: "exec-1", SessionID: "session-1", generation: 1}
	if err := mgr.executionStore.Add(exec); err != nil {
		t.Fatalf("Add: %v", err)
	}

	callReturned := make(chan struct{})
	go func() {
		mgr.SetAgentctlGeneration(2)
		close(callReturned)
	}()

	select {
	case <-callReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("SetAgentctlGeneration blocked on the retire callback instead of scheduling it asynchronously")
	}

	select {
	case <-hookStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("retire hook never started running on its own goroutine")
	}
	close(release)
}

// TestSetAgentctlGenerationRecoversPanicInSweep pins the other half of
// RV3-2: a bug anywhere in the retirement cascade (cleanup, the retired
// hook) must not propagate out of the sweep goroutine and crash the backend
// process. A later, independent generation bump with a healthy hook must
// still retire normally, proving the manager survives the earlier panic.
func TestSetAgentctlGenerationRecoversPanicInSweep(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := newTestRegistryLogger()
		mgr := NewManager(nil, nil, nil, nil, nil, nil, ExecutorFallbackWarn, "", log)

		mgr.SetExecutionRetiredHook(func(sessionID string) {
			panic("boom")
		})

		mgr.SetAgentctlGeneration(1)
		synctest.Wait()
		exec := &AgentExecution{ID: "exec-1", SessionID: "session-1", generation: 1}
		if err := mgr.executionStore.Add(exec); err != nil {
			t.Fatalf("Add: %v", err)
		}

		// Would crash the test binary (and, in production, the backend
		// process) without the sweep goroutine's own recover().
		mgr.SetAgentctlGeneration(2)
		synctest.Wait()

		var retiredSessions []string
		mgr.SetExecutionRetiredHook(func(sessionID string) {
			retiredSessions = append(retiredSessions, sessionID)
		})
		exec2 := &AgentExecution{ID: "exec-2", SessionID: "session-2", generation: 2}
		if err := mgr.executionStore.Add(exec2); err != nil {
			t.Fatalf("Add: %v", err)
		}
		mgr.SetAgentctlGeneration(3)
		synctest.Wait()

		if want := []string{"session-2"}; len(retiredSessions) != len(want) || retiredSessions[0] != want[0] {
			t.Fatalf("retiredSessions = %v, want exactly one retirement of session-2 after recovering from the earlier panic", retiredSessions)
		}
	})
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

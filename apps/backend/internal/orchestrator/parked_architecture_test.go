package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	"github.com/kandev/kandev/internal/agentctl/types/streams"
	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/task/models"
)

// TestParkedRecogniserUsesNeutralPredicate (AC-69a) verifies that the
// observedDetached recogniser gates only on IsDetachedBackgroundLaunch() and
// Kind==BackgroundWorkKindShell — never on the payload AgentID field.
// Injecting "mock-agent" as the AgentID and a shell-detached normalized payload
// must still set observedDetached=true, proving the predicate is agent-neutral.
// (Relabeled 2026-08-09: this test's own content is agent-name independence,
// AC-69a's territory, not AC-35's flag-independence claim — see
// TestParkedFeatureNeverReferencesTheHandoffFlag and
// TestComputeParked_IdenticalWithFlagForcedOnOrOff below for the actual AC-35
// coverage.)
func TestParkedRecogniserUsesNeutralPredicate(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const sessionID = "session-ac35"

	// Build a NormalizedPayload that satisfies both predicate terms:
	//   1. IsDetachedBackgroundLaunch() == true  (detached=true, ended=false)
	//   2. backgroundWorkKind(n) == BackgroundWorkKindShell
	normalized := &streams.NormalizedPayload{}
	normalized.SetBackgroundWorkIdentity(streams.BackgroundWorkKindShell, "", true, false)

	// Use "mock-agent" as AgentID — this is NOT "claude-agent-acp" and would be
	// rejected by the normalizer's stampBackgroundShellWork. But since the
	// orchestrator recogniser reads only the already-stamped normalized payload
	// (not AgentID), the recogniser must accept it.
	svc.handleAgentStreamEvent(context.Background(), &lifecycle.AgentStreamEventPayload{
		TaskID:    "t-ac35",
		SessionID: sessionID,
		AgentID:   "mock-agent", // explicitly NOT claude-agent-acp
		Data: &lifecycle.AgentStreamEventData{
			Type:       "tool_call",
			ToolCallID: "tc-1",
			Normalized: normalized,
		},
	})

	ps := svc.parkedStateFor(sessionID)
	if ps == nil {
		t.Fatal("expected parked state to be created for session")
	}
	if !ps.observedDetached {
		t.Error("expected observedDetached=true: recogniser must use neutral predicate (Kind+IsDetachedBackgroundLaunch), not AgentID")
	}
}

// TestParkedDoesNotAffectNotifications (AC-76) verifies that the parked hook
// fires AFTER publishTaskSessionStateChanged — i.e., the state-change event
// (which triggers session.turn_finished notifications) is not deferred or
// suppressed by the parked projection.
//
// Structural guard: updateTaskSessionStateWithHook calls
// publishTaskSessionStateChanged before calling onSessionParkedHook. This test
// confirms the DB row reflects the new state BEFORE the parked hook can observe it,
// proving notification dispatch (which reads DB state) is not delayed.
func TestParkedDoesNotAffectNotifications(t *testing.T) {
	repo := setupTestRepo(t)
	seedSession(t, repo, "t-ac76", "s-ac76", "")

	probe := &fakeProbe{result: probeResultLive}
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	svc.SetBackgroundProbe(probe)
	svc.getOrCreateParkedState("s-ac76").observedDetached = true

	// Transition to WAITING_FOR_INPUT via the hook-bearing path.
	// The session starts in RUNNING (seeded by seedSession), so nextState=WAITING_FOR_INPUT.
	_, ok := svc.updateTaskSessionStateWithHook(
		context.Background(), "t-ac76", "s-ac76",
		models.TaskSessionStateWaitingForInput,
		"",    // errorMessage
		false, // allowWakeFromWaiting
		nil,   // onChanged
	)
	if !ok {
		t.Fatal("expected state transition to succeed")
	}

	// The session row must reflect the new state (i.e., the DB write happened
	// before or independently of the parked hook).
	session, err := repo.GetTaskSession(context.Background(), "s-ac76")
	if err != nil {
		t.Fatalf("GetTaskSession: %v", err)
	}
	if session.State != models.TaskSessionStateWaitingForInput {
		t.Errorf("expected session state=%s in DB, got %s — state change must precede parked hook",
			models.TaskSessionStateWaitingForInput, session.State)
	}

	// The parked projection must have fired (probe returned "live").
	ps := svc.parkedStateFor("s-ac76")
	if ps == nil || !ps.parked {
		t.Error("expected parked=true after WAITING_FOR_INPUT with live probe — hook did not suppress state change")
	}

	t.Cleanup(func() { svc.evictParkedState(context.Background(), "t-ac76", "s-ac76", true) })
}

// TestParkedDoesNotAffectNotifications_AllCases strengthens the AC-76
// structural guard above across the four cases the spec names — parked,
// un-parked, unknown-probe, and no-recogniser — by asserting on the actual
// published events.TaskSessionStateChanged frame (the event notification
// dispatch reads), not just on the DB row and the parked-state bookkeeping.
// It proves the parked hook is a pure observer of the transition: exactly
// one state-changed event is published per transition, and its
// notification-relevant fields (task_id, session_id, old_state, new_state,
// error_message) are identical in shape across all four cases — the parked
// projection neither adds, removes, delays, nor duplicates this publish,
// regardless of what the probe or the recogniser conclude. (Review round 7,
// test-honesty: the prior AC-76 test asserted only DB/in-memory state, never
// the actual notification-triggering publish.)
func TestParkedDoesNotAffectNotifications_AllCases(t *testing.T) {
	cases := []struct {
		name             string
		observedDetached bool // recogniser attested a detached launch this turn
		probeResult      string
	}{
		{"parked: attested + live probe", true, probeResultLive},
		{"un-parked: attested + settled probe", true, probeResultSettled},
		{"unknown-probe: attested but probe can't tell", true, probeResultUnknown},
		{"no-recogniser: never attested, never probed", false, probeResultLive},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := setupTestRepo(t)
			taskID, sessionID := "t-ac76-"+tc.name, "s-ac76-"+tc.name
			seedSession(t, repo, taskID, sessionID, "")

			recorded := &recordingEventBus{}
			svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
			svc.eventBus = recorded
			probe := &fakeProbe{result: tc.probeResult}
			svc.SetBackgroundProbe(probe)
			if tc.observedDetached {
				svc.getOrCreateParkedState(sessionID).observedDetached = true
			}
			t.Cleanup(func() { svc.evictParkedState(context.Background(), taskID, sessionID, true) })

			_, ok := svc.updateTaskSessionStateWithHook(
				context.Background(), taskID, sessionID,
				models.TaskSessionStateWaitingForInput,
				"", false, nil,
			)
			if !ok {
				t.Fatal("expected state transition to succeed")
			}

			var stateChanged []recordedEvent
			for _, e := range recorded.events {
				if e.subject == events.TaskSessionStateChanged {
					stateChanged = append(stateChanged, e)
				}
			}
			if len(stateChanged) != 1 {
				t.Fatalf("expected exactly 1 events.TaskSessionStateChanged publish, got %d — the parked hook must neither suppress nor duplicate it", len(stateChanged))
			}

			data, ok := stateChanged[0].event.Data.(map[string]interface{})
			if !ok {
				t.Fatalf("expected event.Data to be a map, got %T", stateChanged[0].event.Data)
			}
			if data[metaKeyTaskID] != taskID {
				t.Errorf("task_id = %v, want %v", data[metaKeyTaskID], taskID)
			}
			if data[metaKeySessionID] != sessionID {
				t.Errorf("session_id = %v, want %v", data[metaKeySessionID], sessionID)
			}
			if data[metaKeyNewState] != string(models.TaskSessionStateWaitingForInput) {
				t.Errorf("new_state = %v, want %v", data[metaKeyNewState], models.TaskSessionStateWaitingForInput)
			}
			if data["error_message"] != "" {
				t.Errorf("error_message = %v, want empty — the parked hook must not attach an error", data["error_message"])
			}
		})
	}
}

// TestClearObservedDetachedOnTurnStarted_OwnershipFilter is AC-79a's
// ownership-filter clauses, which TestParkedFIFOOrdering does not exercise:
// its turn_started events carry no ExecutionID/PromptGeneration, so they
// never enter cancellationOwnsStreamEvent's identity check at all (Review
// round 7, test-honesty). This drives handleAgentStreamEvent directly with
// a claimed cancellation identity so the filter is actually in play.
func TestClearObservedDetachedOnTurnStarted_OwnershipFilter(t *testing.T) {
	newTurnStartedPayload := func(taskID, sessionID, executionID string, promptGeneration uint64) *lifecycle.AgentStreamEventPayload {
		return &lifecycle.AgentStreamEventPayload{
			TaskID:      taskID,
			SessionID:   sessionID,
			ExecutionID: executionID,
			Data: &lifecycle.AgentStreamEventData{
				Type:             streams.EventTypeTurnStarted,
				PromptGeneration: promptGeneration,
			},
		}
	}

	t.Run("a turn_started from a SUPERSEDED execution is rejected", func(t *testing.T) {
		svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
		const sessionID = "session-superseded"
		svc.markObservedDetached(sessionID)
		ps := svc.parkedStateFor(sessionID)
		baselineMarker := ps.turnMarker

		operation, owner := svc.claimCancellation(sessionID, cancellationKindExplicit)
		if !owner {
			t.Fatal("expected to claim cancellation ownership")
		}
		svc.setCancellationIdentity(sessionID, operation, cancellationIdentity{
			executionID:      "execution-owner",
			promptGeneration: 7,
		})

		svc.handleAgentStreamEvent(context.Background(), newTurnStartedPayload("task-1", sessionID, "execution-superseded", 7))

		if ps.turnMarker != baselineMarker {
			t.Fatalf("expected turnMarker unchanged (event rejected by the ownership filter), got %d -> %d", baselineMarker, ps.turnMarker)
		}
		if !ps.observedDetached {
			t.Fatal("expected observedDetached to remain true — the superseded event must never reach clearObservedDetachedOnTurnStarted")
		}
	})

	t.Run("a turn_started from the OWNING execution is admitted", func(t *testing.T) {
		svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
		const sessionID = "session-owning"
		svc.markObservedDetached(sessionID)
		ps := svc.parkedStateFor(sessionID)
		baselineMarker := ps.turnMarker

		operation, owner := svc.claimCancellation(sessionID, cancellationKindExplicit)
		if !owner {
			t.Fatal("expected to claim cancellation ownership")
		}
		svc.setCancellationIdentity(sessionID, operation, cancellationIdentity{
			executionID:      "execution-owner",
			promptGeneration: 7,
		})

		svc.handleAgentStreamEvent(context.Background(), newTurnStartedPayload("task-1", sessionID, "execution-owner", 7))

		if ps.turnMarker != baselineMarker+1 {
			t.Fatalf("expected turnMarker to increment by 1 (event admitted), got %d -> %d", baselineMarker, ps.turnMarker)
		}
		if ps.observedDetached {
			t.Fatal("expected observedDetached=false: the admitted turn_started must clear it")
		}
	})

	t.Run("a turn_started carrying prompt_generation:0 (synthetic wakeup) is admitted", func(t *testing.T) {
		svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
		const sessionID = "session-wakeup"
		svc.markObservedDetached(sessionID)
		ps := svc.parkedStateFor(sessionID)
		baselineMarker := ps.turnMarker

		operation, owner := svc.claimCancellation(sessionID, cancellationKindExplicit)
		if !owner {
			t.Fatal("expected to claim cancellation ownership")
		}
		svc.setCancellationIdentity(sessionID, operation, cancellationIdentity{
			executionID:      "execution-owner",
			promptGeneration: 7,
		})

		// PromptGeneration:0 bypasses the generation half of the ownership
		// check (cancellationOwnsStreamEvent) — the synthetic wakeup path
		// (fireWakeup) carries no generation of its own. ExecutionID still
		// matches the owner here; a 0 generation against a mismatched
		// ExecutionID would correctly still be rejected by the identity half.
		svc.handleAgentStreamEvent(context.Background(), newTurnStartedPayload("task-1", sessionID, "execution-owner", 0))

		if ps.turnMarker != baselineMarker+1 {
			t.Fatalf("expected turnMarker to increment by 1 (synthetic wakeup admitted), got %d -> %d", baselineMarker, ps.turnMarker)
		}
		if ps.observedDetached {
			t.Fatal("expected observedDetached=false: the admitted synthetic-wakeup turn_started must clear it")
		}
	})

	t.Run("a turn_started from an execution already marked completed is dropped", func(t *testing.T) {
		svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
		const sessionID, executionID = "session-tombstoned", "execution-done"
		svc.markObservedDetached(sessionID)
		ps := svc.parkedStateFor(sessionID)
		baselineMarker := ps.turnMarker

		svc.markExecutionCompleted(sessionID, executionID)

		svc.handleAgentStreamEvent(context.Background(), newTurnStartedPayload("task-1", sessionID, executionID, 1))

		if ps.turnMarker != baselineMarker {
			t.Fatalf("expected turnMarker unchanged (event dropped as a completed-execution tombstone), got %d -> %d", baselineMarker, ps.turnMarker)
		}
		if !ps.observedDetached {
			t.Fatal("expected observedDetached to remain true — a tombstoned execution's turn_started must never reach clearObservedDetachedOnTurnStarted")
		}
	})
}

// TestParkedEpochRestartSurvivable (AC-77 / AC-78) verifies that after a
// simulated backend restart (parkedEpoch incremented, session reset to parked=false,
// revision=0), the snapshot returns the new epoch and the boot state.
func TestParkedEpochRestartSurvivable(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())

	const sessionID = "session-epoch"
	// Simulate "before restart": parked=true with epoch E and revision R.
	epochBefore := int64(1_000_000)
	svc.parkedEpoch = epochBefore
	ps := svc.getOrCreateParkedState(sessionID)
	ps.parked = true
	ps.revision = 5

	// Verify snapshot before restart.
	parked, epoch, revision := svc.ParkedProjectionSnapshot(sessionID)
	if !parked || epoch != epochBefore || revision != 5 {
		t.Fatalf("pre-restart: want parked=true epoch=%d revision=5, got parked=%v epoch=%d revision=%d",
			epochBefore, parked, epoch, revision)
	}

	// Simulate backend restart: new epoch, state reset.
	epochAfter := int64(2_000_000)
	svc.parkedEpoch = epochAfter
	svc.evictParkedState(context.Background(), "", sessionID, true) // session cleared on restart

	// The snapshot for the session returns boot state (parked=false, revision=0)
	// but the new epoch — clients use the epoch change as a discard signal.
	parked, epoch, revision = svc.ParkedProjectionSnapshot(sessionID)
	if parked {
		t.Error("post-restart: expected parked=false for unknown session")
	}
	if epoch != epochAfter {
		t.Errorf("post-restart: expected epoch=%d (new), got %d", epochAfter, epoch)
	}
	if revision != 0 {
		t.Errorf("post-restart: expected revision=0, got %d", revision)
	}
}

// TestParkedFIFOOrdering (AC-79, AC-79a) verifies that the orchestrator
// processes a detached-shell tool_call event BEFORE the subsequent
// turn_started event, so the attestation is visible when the turn-completion
// frame is handled (AC-79) — and that turn_started then clears it for the
// *next* turn, never leaving turn N's attestation to leak into turn N+1
// (AC-79a: "observed_detached is set for turn N and then cleared by turn
// N+1 — never the reverse").
func TestParkedFIFOOrdering(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const sessionID = "session-fifo"

	// Step 1: tool_call with detached shell attestation arrives.
	normalized := &streams.NormalizedPayload{}
	normalized.SetBackgroundWorkIdentity(streams.BackgroundWorkKindShell, "", true, false)
	svc.handleAgentStreamEvent(context.Background(), &lifecycle.AgentStreamEventPayload{
		TaskID:    "t-fifo",
		SessionID: sessionID,
		Data: &lifecycle.AgentStreamEventData{
			Type:       "tool_call",
			ToolCallID: "tc-fifo",
			Normalized: normalized,
		},
	})

	// After tool_call: observedDetached must be set, turnMarker must still be 0.
	ps := svc.parkedStateFor(sessionID)
	if ps == nil || !ps.observedDetached {
		t.Fatal("expected observedDetached=true after tool_call with detached shell attestation")
	}
	markerAfterToolCall := ps.turnMarker

	// Step 2: turn_started arrives immediately after (simulating FIFO ordering).
	svc.handleAgentStreamEvent(context.Background(), &lifecycle.AgentStreamEventPayload{
		TaskID:    "t-fifo",
		SessionID: sessionID,
		Data: &lifecycle.AgentStreamEventData{
			Type: streams.EventTypeTurnStarted,
		},
	})

	ps = svc.parkedStateFor(sessionID)
	if ps.turnMarker <= markerAfterToolCall {
		t.Errorf("expected turnMarker to increment after turn_started, got %d (was %d)",
			ps.turnMarker, markerAfterToolCall)
	}
	// D3/AC-79a: observed_detached was set for turn N (the tool_call frame
	// preceded turn_started, proving AC-79's ordering) and is then cleared by
	// turn_started for turn N+1 — never the reverse. A stale attestation must
	// not survive the boundary it names.
	if ps.observedDetached {
		t.Error("observedDetached must be cleared by turn_started (D3/AC-79a: set for turn N, cleared by turn N+1)")
	}
}

// parkedFeatureSourceFiles lists the source files (excluding tests) that hold
// the symbols AC-35 names: the parked projection and its revision accessors,
// the probe port and its production implementation, the sampling loop, the
// recogniser registry, and the settle hook. Both parked_state.go and
// parked_projection.go are dedicated to this feature end to end and can be
// scanned wholesale; the mixed files below are scanned only for the specific
// declarations AC-35 names, via TestParkedFeatureNeverReferencesTheHandoffFlag.
var parkedFeatureSourceFiles = []string{
	"parked_state.go",
	"parked_projection.go",
}

// parkedFeatureForbiddenIdentifiers are the flag key, config field, and
// accessor names AC-35 forbids the parked-projection symbols from
// referencing, so the feature stays independent of
// features.claudeBackgroundPromptHandoff, which remains off and unread.
var parkedFeatureForbiddenIdentifiers = []string{
	"claudeBackgroundPromptHandoff",
	"ClaudeBackgroundPromptHandoff",
	"claudeBackgroundPromptHandoffEnabled",
	"claudeBackgroundPromptHandoffEnabledForSession",
}

// TestParkedFeatureNeverReferencesTheHandoffFlag is AC-35's architecture
// test: none of the symbols this feature introduces may reference the
// claudeBackgroundPromptHandoff flag key, the Features.ClaudeBackgroundPromptHandoff
// config field, or the claudeBackgroundPromptHandoffEnabled(ForSession)
// accessors. Scoped to symbol/file granularity per the spec's correction —
// package orchestrator legitimately references the flag elsewhere (the
// unrelated ForegroundActivity gate in service.go and turn_activity.go), so a
// package-wide check would be false by construction.
func TestParkedFeatureNeverReferencesTheHandoffFlag(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(sourceFile)

	var violations []string
	for _, name := range parkedFeatureSourceFiles {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(data)
		for _, forbidden := range parkedFeatureForbiddenIdentifiers {
			if strings.Contains(src, forbidden) {
				violations = append(violations, fmt.Sprintf("%s references forbidden identifier %q", name, forbidden))
			}
		}
	}
	if len(violations) != 0 {
		sort.Strings(violations)
		t.Fatalf("parked-projection source reads the claudeBackgroundPromptHandoff flag (AC-35):\n%s",
			strings.Join(violations, "\n"))
	}
}

// TestComputeParked_IdenticalWithFlagForcedOnOrOff is AC-35's behavioural
// clause: a parked session's projection is computed identically with the
// flag forced on and forced off, for the same inputs.
func TestComputeParked_IdenticalWithFlagForcedOnOrOff(t *testing.T) {
	repo := setupTestRepo(t)
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	probe := &fakeProbe{result: probeResultLive}
	svc.SetBackgroundProbe(probe)

	const taskID, sessionID = "task-ac35", "session-ac35"
	parkedTestSeedSession(t, repo, taskID, sessionID, models.TaskSessionStateWaitingForInput)

	for _, flag := range []bool{true, false} {
		svc.config.ClaudeBackgroundPromptHandoff = flag
		svc.getOrCreateParkedState(sessionID).observedDetached = true

		svc.onSessionParkedHook(context.Background(), taskID, sessionID)

		ps := svc.parkedStateFor(sessionID)
		if !ps.parked {
			t.Fatalf("flag=%v: expected parked=true, got false", flag)
		}
		t.Cleanup(func() { svc.evictParkedState(context.Background(), taskID, sessionID, true) })
	}
}

package orchestrator

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/orchestrator/watcher"
	"github.com/kandev/kandev/internal/task/models"
)

// stateLeftDuringProbe is a test double for backgroundProbePort that
// simulates the session leaving WAITING_FOR_INPUT via a path that does NOT
// touch turnMarker/observedDetached — e.g. straight to a terminal state,
// with no accompanying turn_started (spec's Entry lifecycle: "a session's
// execution can end... while the session is still sitting at
// WAITING_FOR_INPUT") — by calling unparkOnStateLeave from inside the probe
// call itself before returning a result. Review round 7, F6.
type stateLeftDuringProbe struct {
	svc       *Service
	taskID    string
	sessionID string
	result    string
	calls     int
}

func (t *stateLeftDuringProbe) ProbeBackgroundWorkloads(_ context.Context, _ string) (string, error) {
	t.calls++
	t.svc.unparkOnStateLeave(context.Background(), t.taskID, t.sessionID)
	return t.result, nil
}

// TestOnSessionParkedHook_DiscardsSampleWhenSessionLeavesStateDuringProbe
// verifies the F6 fix: before it, onSessionParkedHook read sessionState
// OUTSIDE parkedStatesMu and never revalidated it, so a probe racing a
// concurrent unparkOnStateLeave (a transition this test simulates via a
// path that bumps neither turnMarker nor observedDetached nor the
// eviction-only generation counter) could re-park a session that had
// already settled elsewhere, with no sampler left running to correct it.
// stateGeneration closes that gap.
func TestOnSessionParkedHook_DiscardsSampleWhenSessionLeavesStateDuringProbe(t *testing.T) {
	repo := setupTestRepo(t)
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-stateleave", "session-stateleave"
	svc.getOrCreateParkedState(sessionID).observedDetached = true
	parkedTestSeedSession(t, repo, taskID, sessionID, models.TaskSessionStateWaitingForInput)

	probe := &stateLeftDuringProbe{svc: svc, taskID: taskID, sessionID: sessionID, result: probeResultLive}
	svc.SetBackgroundProbe(probe)

	svc.onSessionParkedHook(context.Background(), taskID, sessionID)

	if probe.calls != 1 {
		t.Fatalf("expected 1 probe call, got %d", probe.calls)
	}
	ps := svc.parkedStateFor(sessionID)
	if ps.parked {
		t.Fatal("expected parked=false: a sample racing a concurrent state-leave must be discarded, not applied — even though the repository still reads WAITING_FOR_INPUT at sample-completion time")
	}
	if ps.lastSample != "" {
		t.Fatalf("expected lastSample unset (discard writes nothing), got %q", ps.lastSample)
	}
}

// TestSampleAndPublishParked_DiscardsSampleWhenSessionLeavesStateDuringProbe
// is the periodic-sampler counterpart of the settle-hook test above.
func TestSampleAndPublishParked_DiscardsSampleWhenSessionLeavesStateDuringProbe(t *testing.T) {
	repo := setupTestRepo(t)
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-stateleave2", "session-stateleave2"
	ps := svc.getOrCreateParkedState(sessionID)
	ps.observedDetached = true
	ps.parked = true // already parked; the sampler tick is a re-sample, not the first one.
	parkedTestSeedSession(t, repo, taskID, sessionID, models.TaskSessionStateWaitingForInput)

	probe := &stateLeftDuringProbe{svc: svc, taskID: taskID, sessionID: sessionID, result: probeResultLive}
	svc.SetBackgroundProbe(probe)

	keepRunning := svc.sampleAndPublishParked(context.Background(), taskID, sessionID)

	if probe.calls != 1 {
		t.Fatalf("expected 1 probe call, got %d", probe.calls)
	}
	// A discard returns keepRunning=true (mirrors TestSampleAndPublishParked_DiscardsStaleSample
	// in parked_projection_test.go) — harmless in practice, since
	// unparkOnStateLeave already cancelled this sampler's context, so
	// runParkingSampler's loop exits on ctx.Done() regardless of this return
	// value before ever reaching another tick.
	if !keepRunning {
		t.Fatal("expected a discarded sample to report keepRunning=true")
	}
	got := svc.parkedStateFor(sessionID)
	if got.parked {
		t.Fatal("expected parked=false: unparkOnStateLeave already unparked the session, and the racing sample must not undo it")
	}
}

// TestEvictParkedState_RemovesTaskMember verifies the fix for the unbounded
// growth defect (Review round 7, F2): eviction must DELETE the session's
// entry from the task's members map, not merely set it to false. A
// tombstone entry left at false would grow ts.members by one key per
// session ever parked, for the life of the process — even after the
// session and its task are both long gone. Spec (Task-level projection):
// "A session's entry is removed from members when its parkedState row is
// evicted... under the same per-task lock."
func TestEvictParkedState_RemovesTaskMember(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-evict3", "session-evict3"

	// Session parks, which populates ts.members[sessionID] = true via the
	// ordinary transition path. sessionRevision=0 matches the parkedState
	// row's actual (never-incremented) revision at this point.
	svc.updateTaskParkedState(context.Background(), taskID, sessionID, true, 0)
	if parked, _, _ := svc.TaskParkedProjectionSnapshot(taskID); !parked {
		t.Fatal("setup: expected task parked=true before eviction")
	}

	ps := svc.getOrCreateParkedState(sessionID)
	ps.parked = true

	svc.evictParkedState(context.Background(), taskID, sessionID, false)

	// Read the row directly, without get-or-create — getOrCreateTaskParkedState
	// would silently recreate an empty row and mask a real deletion.
	svc.taskParkedStatesMu.Lock()
	ts, taskRowSurvives := svc.taskParkedStates[taskID]
	svc.taskParkedStatesMu.Unlock()

	if taskRowSurvives {
		ts.mu.Lock()
		_, stillMember := ts.members[sessionID]
		ts.mu.Unlock()
		if stillMember {
			t.Fatal("expected the session's entry to be DELETED from ts.members on eviction, not merely set to false")
		}
		// This was the task's only ever-parked session, so its members map is
		// now empty and the task-level row itself should have been dropped
		// rather than left behind as a permanent, empty entry in
		// s.taskParkedStates.
		t.Fatal("expected the task-level row to be dropped once its members map is empty")
	}

	if parked, _, _ := svc.TaskParkedProjectionSnapshot(taskID); parked {
		t.Fatal("expected task parked=false after its only parked session was evicted")
	}
}

// TestEvictParkedState_RemovesTaskMemberButKeepsRowWithOtherMembers verifies
// a multi-session task keeps its row (and the other session's membership)
// when only one session is evicted.
func TestEvictParkedState_RemovesTaskMemberButKeepsRowWithOtherMembers(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionA, sessionB = "task-evict4", "session-evict4a", "session-evict4b"

	svc.updateTaskParkedState(context.Background(), taskID, sessionA, true, 0)
	svc.updateTaskParkedState(context.Background(), taskID, sessionB, false, 0)

	svc.getOrCreateParkedState(sessionA).parked = true
	svc.evictParkedState(context.Background(), taskID, sessionA, true)

	ts := svc.getOrCreateTaskParkedState(taskID)
	ts.mu.Lock()
	_, aStillMember := ts.members[sessionA]
	_, bStillMember := ts.members[sessionB]
	ts.mu.Unlock()
	if aStillMember {
		t.Fatal("expected the evicted session's entry to be removed")
	}
	if !bStillMember {
		t.Fatal("expected the other session's entry to survive an unrelated eviction")
	}

	svc.taskParkedStatesMu.Lock()
	_, taskRowSurvives := svc.taskParkedStates[taskID]
	svc.taskParkedStatesMu.Unlock()
	if !taskRowSurvives {
		t.Fatal("expected the task-level row to survive while it still has a member")
	}
}

// TestHandleTaskDeleted_RemovesTaskParkedState verifies the backstop GC:
// deleting a task drops its taskParkedStates row unconditionally, even if
// per-session eviction did not already empty it (e.g. task deletion racing
// ahead of, or bypassing, individual session eviction).
func TestHandleTaskDeleted_RemovesTaskParkedState(t *testing.T) {
	svc := createTestServiceWithScheduler(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo(), &mockAgentManager{})
	const taskID = "task-deleted-1"

	svc.updateTaskParkedState(context.Background(), taskID, "session-x", true, 1)
	svc.taskParkedStatesMu.Lock()
	_, exists := svc.taskParkedStates[taskID]
	svc.taskParkedStatesMu.Unlock()
	if !exists {
		t.Fatal("setup: expected a taskParkedStates row before deletion")
	}

	svc.handleTaskDeleted(context.Background(), watcher.TaskEventData{TaskID: taskID})

	svc.taskParkedStatesMu.Lock()
	_, stillExists := svc.taskParkedStates[taskID]
	svc.taskParkedStatesMu.Unlock()
	if stillExists {
		t.Fatal("expected handleTaskDeleted to drop the task's taskParkedStates row")
	}
}

// TestUpdateTaskParkedState_DiscardsOutOfOrderWrite is Review round 7's F7:
// two session-level transitions for the SAME session can call
// updateTaskParkedState out of causal order (the older goroutine
// descheduled between releasing parkedStatesMu and reaching this call,
// landing after the newer transition's call already did). Without the
// sessionRevision ordering guard, the stale write would silently overwrite
// the newer one and leave the task-level OR wrong until an unrelated later
// transition happened to correct it. This drives the calls directly, in the
// reverse-of-causal order a real race would produce, rather than relying on
// goroutine scheduling luck to reproduce it.
func TestUpdateTaskParkedState_DiscardsOutOfOrderWrite(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-order", "session-order"

	// Newer transition (session revision 2: the session re-parked) applies
	// FIRST, as it would if the older goroutine below got descheduled.
	svc.updateTaskParkedState(context.Background(), taskID, sessionID, true, 2)
	if parked, _, _ := svc.TaskParkedProjectionSnapshot(taskID); !parked {
		t.Fatal("setup: expected task parked=true after the newer (revision 2) write")
	}

	// Older transition (session revision 1: an earlier, now-superseded
	// unpark) arrives SECOND, carrying a lower sessionRevision. It must be
	// discarded, not applied — the task must stay parked=true.
	svc.updateTaskParkedState(context.Background(), taskID, sessionID, false, 1)

	parked, _, revision := svc.TaskParkedProjectionSnapshot(taskID)
	if !parked {
		t.Fatal("expected task to remain parked=true: the stale, out-of-order write must be discarded")
	}
	if revision != 1 {
		t.Fatalf("expected revision to stay at 1 (only the first, newer write counted), got %d", revision)
	}
}

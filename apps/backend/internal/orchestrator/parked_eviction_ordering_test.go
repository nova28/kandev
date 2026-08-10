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

// TestUpdateTaskParkedState_ConcurrentWithSiblingEviction is Review round 8's
// regression guard for the CRITICAL lock-scope race: updateTaskParkedState
// used to fetch its *taskParkedState pointer via getOrCreateTaskParkedState,
// which released taskParkedStatesMu before returning, then separately locked
// ts.mu — NOT the same critical section as the map lookup. Meanwhile
// removeTaskParkedMember (called by evictParkedState on every eviction
// cause) held taskParkedStatesMu for its entire body, including deleting the
// task's row from taskParkedStates once its last member was evicted. Session
// B parking while sibling session A's execution ends and gets evicted could
// interleave so B's write landed on the row AFTER it had already been
// deleted from the map — orphaning B's parked=true write.
//
// The window between resolving the row and mutating it is only a couple of
// instructions wide, far too narrow to reproduce reliably through ordinary
// goroutine scheduling even under -race and many iterations (this was
// verified: a plain gated-goroutine version of this test passed 300/300
// iterations against the unfixed code without ever reproducing the bug). So
// this test uses updateTaskParkedStateTestHook to deterministically pause
// session B's update exactly inside that window, run session A's eviction to
// completion, and only then let B resume — reproducing the exact interleaving
// on every run rather than hoping for it.
func TestUpdateTaskParkedState_ConcurrentWithSiblingEviction(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionA, sessionB = "task-sibling-evict", "session-sibling-a", "session-sibling-b"

	// Session A parks first — it is the task's only parked member, so its
	// eviction below empties ts.members and triggers the row-drop path.
	svc.updateTaskParkedState(context.Background(), taskID, sessionA, true, 1)
	if parked, _, _ := svc.TaskParkedProjectionSnapshot(taskID); !parked {
		t.Fatal("setup: expected task parked=true after session A parks")
	}
	ps := svc.getOrCreateParkedState(sessionA)
	ps.parked = true

	reachedHook := make(chan struct{})
	releaseHook := make(chan struct{})
	updateTaskParkedStateTestHook = func() {
		close(reachedHook)
		<-releaseHook
	}
	t.Cleanup(func() { updateTaskParkedStateTestHook = nil })

	updateDone := make(chan struct{})
	go func() {
		defer close(updateDone)
		// Session B settles parked=true — pauses at the hook, mid-operation.
		svc.updateTaskParkedState(context.Background(), taskID, sessionB, true, 1)
	}()
	<-reachedHook

	// Session A's execution ends and its parked row is evicted while B is
	// still paused mid-operation — the task's only OTHER parked member goes
	// away. This runs in its own goroutine, not inline: with the fix in
	// place, evictParkedState blocks on taskParkedStatesMu until B's paused
	// critical section releases it (it can no longer interleave into the
	// gap), so calling it synchronously here would deadlock against
	// releaseHook below.
	evictDone := make(chan struct{})
	go func() {
		defer close(evictDone)
		svc.evictParkedState(context.Background(), taskID, sessionA, false)
	}()

	close(releaseHook)
	<-updateDone
	<-evictDone

	parked, _, revision := svc.TaskParkedProjectionSnapshot(taskID)
	if !parked {
		t.Fatal("expected task parked=true — session B's write must not be lost to a sibling eviction that raced it")
	}
	if revision == 0 {
		t.Fatal("expected a nonzero revision for session B's applied write, got 0 (orphaned-write symptom)")
	}
}

// TestUpdateTaskParkedState_RevisionNeverRegressesAcrossRowDrop is Review
// round 8's regression guard for a design issue codex (cross-vendor)
// surfaced alongside the primary race: dropping a task's taskParkedState row
// when its last member is evicted, then recreating a fresh one on the next
// park, used to reset ts.revision to 0. A client that had already applied a
// higher revision from before the drop would then discard the newer
// parked=true update under the (parked_epoch, parked_revision) discard rule,
// leaving the UI stuck reporting not-parked.
func TestUpdateTaskParkedState_RevisionNeverRegressesAcrossRowDrop(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionA, sessionB = "task-revision-floor", "session-floor-a", "session-floor-b"

	// Session A parks and unparks, running the row's revision up past 1.
	svc.updateTaskParkedState(context.Background(), taskID, sessionA, true, 1)
	svc.updateTaskParkedState(context.Background(), taskID, sessionA, false, 2)
	svc.updateTaskParkedState(context.Background(), taskID, sessionA, true, 3)
	_, _, revisionBeforeDrop := svc.TaskParkedProjectionSnapshot(taskID)
	if revisionBeforeDrop < 2 {
		t.Fatalf("setup: expected at least 2 revision bumps before drop, got %d", revisionBeforeDrop)
	}

	// Session A's row is evicted — it was the task's only member, so the
	// task-level row is dropped. TestEvictParkedState_RemovesTaskMember pins
	// this drop-on-empty behavior; it stays intentional and unchanged.
	// ps.revision must be at least the last member-revision recorded above
	// (3) — evictParkedState increments it once more, and removeTaskParkedMember
	// rejects an eviction whose sessionRevision looks stale relative to the
	// last recorded write for this session.
	ps := svc.getOrCreateParkedState(sessionA)
	ps.parked = true
	ps.revision = 3
	svc.evictParkedState(context.Background(), taskID, sessionA, false)
	if parked, _, _ := svc.TaskParkedProjectionSnapshot(taskID); parked {
		t.Fatal("setup: expected task parked=false after its only session was evicted")
	}

	// A different session parks later, recreating the row from scratch.
	svc.updateTaskParkedState(context.Background(), taskID, sessionB, true, 1)

	parked, _, revisionAfterRecreate := svc.TaskParkedProjectionSnapshot(taskID)
	if !parked {
		t.Fatal("expected task parked=true after session B parks on the recreated row")
	}
	if revisionAfterRecreate <= revisionBeforeDrop {
		t.Fatalf("expected the recreated row's revision (%d) to exceed the last revision published before the drop (%d) — a client that already applied %d would otherwise discard this genuinely newer parked=true update",
			revisionAfterRecreate, revisionBeforeDrop, revisionBeforeDrop)
	}
}

// TestRemoveTaskParkedMember_RetainsRevisionTombstoneForStaleWrites is
// Review round 8's regression guard for codex's secondary finding: eviction
// used to delete ts.memberRevision[sessionID] along with
// ts.members[sessionID], erasing the ordering guard F7 (Review round 7)
// relies on. A delayed, pre-eviction updateTaskParkedState call for the SAME
// session — descheduled before the eviction and only resuming after — would
// then find no recorded revision, pass the "ok==false" branch
// unconditionally, and resurrect a stale parked value for a session that has
// already been evicted.
func TestRemoveTaskParkedMember_RetainsRevisionTombstoneForStaleWrites(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionA, sessionB = "task-tombstone", "session-tombstone-a", "session-tombstone-b"

	// Session B is a permanent member keeping the task row alive across A's
	// eviction below. Session A parks at member-revision 5.
	svc.updateTaskParkedState(context.Background(), taskID, sessionB, true, 1)
	svc.updateTaskParkedState(context.Background(), taskID, sessionA, true, 5)

	// A's eviction observes session-level revision 6 (one increment past the
	// 5 already recorded in ts.memberRevision above).
	ps := svc.getOrCreateParkedState(sessionA)
	ps.parked = true
	ps.revision = 5
	svc.evictParkedState(context.Background(), taskID, sessionA, false)

	// A delayed, pre-eviction write for session A arrives late, carrying a
	// sessionRevision (3) strictly less than the one the eviction recorded
	// (6). It must be rejected, not resurrect A's membership.
	svc.updateTaskParkedState(context.Background(), taskID, sessionA, true, 3)

	svc.taskParkedStatesMu.Lock()
	ts := svc.taskParkedStates[taskID]
	ts.mu.Lock()
	_, aIsMember := ts.members[sessionA]
	ts.mu.Unlock()
	svc.taskParkedStatesMu.Unlock()

	if aIsMember {
		t.Fatal("expected the stale, pre-eviction write to be rejected — session A must not be resurrected as a member after eviction")
	}
}

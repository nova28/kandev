package orchestrator

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

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
// TestUpdateTaskParkedState_ConcurrentWithSiblingEviction is Review round 8's
// regression guard for the CRITICAL lock-scope race — REWRITTEN in Review
// round 9 (test-supervisor finding): the original version paused session B's
// write at the hook and then spawned session A's eviction expecting it to
// interleave, but never proved the eviction actually reached the row-drop
// while B was paused. Since both operations serialize on the SAME
// taskParkedStatesMu, spawning the eviction and immediately releasing the
// hook let B's write simply finish first every time — the test passed
// 60/60 against a scratch reconstruction of the PRE-FIX code specifically
// because it never forced the interleaving it claims to reproduce.
//
// This version actively PROVES serialization inside the hook itself: it
// spawns the eviction, then asserts (via a bounded wait) that the eviction
// does NOT complete while B's write is still paused holding
// taskParkedStatesMu. If it does complete, the lock-scope fix has
// regressed and the two operations are interleaving again — the exact
// symptom that let A's eviction drop the row out from under B's write.
// Only after that assertion does it release the hook and let both
// operations run to their real completion, then checks the final state.
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
	evictDone := make(chan struct{})
	updateTaskParkedStateTestHook = func() {
		close(reachedHook)
		// Spawn session A's eviction only now, while this write is still
		// paused holding taskParkedStatesMu (acquired before this hook
		// runs — see updateTaskParkedState). Prove it cannot complete: it
		// needs the same mutex to even look up the row.
		go func() {
			svc.evictParkedState(context.Background(), taskID, sessionA, false)
			close(evictDone)
		}()
		select {
		case <-evictDone:
			t.Error("session A's eviction completed while session B's write was still paused mid-critical-section — taskParkedStatesMu is no longer serializing them")
		case <-time.After(50 * time.Millisecond):
			// Expected: the eviction is blocked on taskParkedStatesMu.
		}
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

	close(releaseHook)
	<-updateDone
	select {
	case <-evictDone:
	case <-time.After(2 * time.Second):
		t.Fatal("session A's eviction never completed after the hook released taskParkedStatesMu")
	}

	parked, _, revision := svc.TaskParkedProjectionSnapshot(taskID)
	if !parked {
		t.Fatal("expected task parked=true — session B's write must not be lost to a sibling eviction that raced it")
	}
	if revision == 0 {
		t.Fatal("expected a nonzero revision for session B's applied write, got 0 (orphaned-write symptom)")
	}
}

// TestUpdateTaskParkedState_SameSessionRaceAgainstOwnEviction is Review
// round 9's CRITICAL regression guard: round 11's fix closed the
// CROSS-session race above (a sibling's eviction dropping the row out from
// under a concurrent write) but reopened an equivalent SAME-session race
// through the identical row-drop mechanism. removeTaskParkedMember deletes
// the ENTIRE *taskParkedState struct — memberRevision map included — once a
// session's removal empties ts.members (the common single-session-task
// case). getOrCreateTaskParkedStateLocked then recreated the row (before
// this round's fix) with a FRESH, EMPTY memberRevision map, so
// updateTaskParkedState's staleness guard always misses for the very
// session that was just evicted, and a delayed, pre-eviction write for that
// SAME session is wrongly accepted — resurrecting a stale parked=true with
// nothing left to correct it, since eviction already cancelled that
// session's sampler.
//
// Unlike the goroutine-interleaving test above, this defect is fully
// deterministic and needs no concurrency to reproduce: it is purely a
// question of whether the guard's memory survives a row drop-and-recreate
// cycle, so this drives the calls sequentially in exactly the order a real
// race would produce them.
func TestUpdateTaskParkedState_SameSessionRaceAgainstOwnEviction(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-own-race", "session-own-race"

	// Session parks — the task's only member, memberRevision[session]=5.
	svc.updateTaskParkedState(context.Background(), taskID, sessionID, true, 5)
	if parked, _, _ := svc.TaskParkedProjectionSnapshot(taskID); !parked {
		t.Fatal("setup: expected task parked=true after session parks")
	}

	// The session is evicted at a NEWER session-level revision (6) — it is
	// the task's only member, so this empties ts.members and drops the row.
	svc.removeTaskParkedMember(context.Background(), taskID, sessionID, 6)
	if parked, _, _ := svc.TaskParkedProjectionSnapshot(taskID); parked {
		t.Fatal("setup: expected task parked=false after its only session was evicted")
	}

	// A delayed, PRE-eviction write for the SAME session arrives late,
	// carrying a sessionRevision (3) strictly less than the one the
	// eviction already recorded (6). On the buggy row-drop path this is
	// wrongly accepted because the recreated row's memberRevision map has
	// no entry for this session; it must be rejected instead, exactly as
	// TestRemoveTaskParkedMember_RetainsRevisionTombstoneForStaleWrites
	// already pins for the multi-session case that keeps the row alive.
	svc.updateTaskParkedState(context.Background(), taskID, sessionID, true, 3)

	parked, _, _ := svc.TaskParkedProjectionSnapshot(taskID)
	if parked {
		t.Fatal("expected task parked=false: a stale write for the just-evicted session must not resurrect it after the row was dropped and recreated")
	}
}

// TestTaskParkedProjectionSnapshot_RetainsRevisionAfterMembersEmptyDrop is
// Review round 9's regression guard for a second defect the same row-drop
// path causes: TaskParkedProjectionSnapshot returned a hardcoded revision=0
// for an absent row, with no fallback to taskParkedRevisionFloor. Because
// this method is what the task-activity publisher calls to build the
// OUTGOING task.updated frame — synchronously, immediately after
// removeTaskParkedMember drops the row — every ORDINARY last-session
// eviction (no race required at all) published a parked=false frame
// carrying revision=0 instead of the real, just-incremented revision. A
// client already holding a higher revision for this task discards that
// frame under the (parked_epoch, parked_revision) rule and is left stuck
// showing parked=true forever — exactly the "card stuck" failure class the
// whole revision/epoch mechanism exists to prevent.
func TestTaskParkedProjectionSnapshot_RetainsRevisionAfterMembersEmptyDrop(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-snapshot-after-drop", "session-snapshot-after-drop"

	svc.updateTaskParkedState(context.Background(), taskID, sessionID, true, 1)
	_, _, revisionBeforeEviction := svc.TaskParkedProjectionSnapshot(taskID)
	if revisionBeforeEviction == 0 {
		t.Fatal("setup: expected a nonzero revision after the task parks")
	}

	// Evicting the task's only member flips the OR to false and drops the
	// row (TestEvictParkedState_RemovesTaskMember already pins the drop).
	svc.removeTaskParkedMember(context.Background(), taskID, sessionID, 2)

	parked, _, revisionAfterEviction := svc.TaskParkedProjectionSnapshot(taskID)
	if parked {
		t.Fatal("expected task parked=false after its only session was evicted")
	}
	if revisionAfterEviction <= revisionBeforeEviction {
		t.Fatalf("expected the snapshot immediately after eviction to carry a revision strictly greater than the pre-eviction value (%d), got %d — a revision of 0 (or unchanged) is what a client already holding %d would discard under the (parked_epoch, parked_revision) rule, leaving it stuck showing parked=true",
			revisionBeforeEviction, revisionAfterEviction, revisionBeforeEviction)
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

// TestUpdateTaskParkedState_SameSessionRaceAgainstEvictionWhileRowAbsent is
// Review round 10's regression guard for a residual of round 9's CRITICAL
// finding: removeTaskParkedMember's row-absent early return (the "!ok"
// branch) never records sessionRevision into s.taskMemberRevisionFloor, so
// an eviction that lands while the task-level row happens to already be
// absent loses its tombstone entirely — unlike the row-PRESENT case round
// 12 fixed, where the shared memberRevision map retains it across the drop.
//
// Reproduces the exact sequence: session S parks and is reduced (row
// drops, floor retains revision 2); a re-park for S is decided
// (session-level revision 3) but its updateTaskParkedState call is
// descheduled before running (the same F7 interleaving
// TestUpdateTaskParkedState_SameSessionRaceAgainstOwnEviction reproduces);
// S is then deleted outright, which evicts at session-level revision 4 —
// but the task row is STILL absent (nothing recreated it), so this
// eviction's revision 4 is silently discarded instead of updating the
// floor; the delayed write at revision 3 then resumes, recreates the row
// from the STALE floor value (2), passes the guard (3 > 2), and wrongly
// resurrects parked=true for a session that no longer exists.
func TestUpdateTaskParkedState_SameSessionRaceAgainstEvictionWhileRowAbsent(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-race-row-absent", "session-race-row-absent"

	// Session parks — the task's only member, memberRevision[session]=2.
	svc.updateTaskParkedState(context.Background(), taskID, sessionID, true, 2)

	// The session is reduced (not deleted) at the same revision — it is
	// the task's only member, so this empties ts.members and drops the row,
	// leaving the floor at {session: 2}.
	svc.removeTaskParkedMember(context.Background(), taskID, sessionID, 2)
	if parked, _, _ := svc.TaskParkedProjectionSnapshot(taskID); parked {
		t.Fatal("setup: expected task parked=false after the row was reduced")
	}

	// The session is deleted outright while the task row is STILL absent
	// (nothing has recreated it yet). Its eviction observes revision 4 —
	// strictly newer than the 2 already recorded — but removeTaskParkedMember
	// hits the row-absent early return and silently drops it instead of
	// recording it into the floor. This is the defect under test.
	svc.removeTaskParkedMember(context.Background(), taskID, sessionID, 4)

	// A delayed, pre-deletion write for the SAME session arrives late,
	// carrying a sessionRevision (3) that is strictly less than the one the
	// deletion already recorded (4), but strictly greater than the stale
	// floor value (2) the row-absent eviction failed to update. It must
	// still be rejected — the session no longer exists.
	svc.updateTaskParkedState(context.Background(), taskID, sessionID, true, 3)

	parked, _, _ := svc.TaskParkedProjectionSnapshot(taskID)
	if parked {
		t.Fatal("expected task parked=false: a stale write for a since-deleted session must not resurrect it, even when its eviction landed while the task row was absent")
	}
}

// TestRemoveTaskParkedMember_DoesNotResurrectFloorAfterTaskDeleted is Review
// round 11's regression guard for SEC-001: after handleTaskDeleted has
// synchronously torn down a task's parked-projection state — the ordinary,
// unprivileged task-deletion path, where deleteTaskWithReasonAndDBDelete
// publishes task.deleted synchronously and only afterward starts async
// session-stop cleanup in a goroutine — a delayed removeTaskParkedMember
// call for one of that task's sessions must not recreate
// s.taskMemberRevisionFloor[taskID]. That call is reached when the async
// cleanup goroutine stops an execution whose session ever attested a
// detached launch: retireExecutionActivityAndPublish unconditionally calls
// evictParkedState -> removeTaskParkedMember on the last execution's
// retirement, regardless of whether the session was ever actually
// parked=true. Before this fix, Build round 13's row-absent (!ok) branch
// always recreated the floor entry here — and nothing ever cleared it
// again, since handleTaskDeleted fires exactly once per task and task IDs
// are never reused, leaking one map entry per deleted task with a
// background-attesting session for the life of the process.
func TestRemoveTaskParkedMember_DoesNotResurrectFloorAfterTaskDeleted(t *testing.T) {
	svc := createTestServiceWithScheduler(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo(), &mockAgentManager{})
	const taskID, sessionID = "task-deleted-tombstone", "session-deleted-tombstone"

	// Session parks — creates the task-level row.
	svc.updateTaskParkedState(context.Background(), taskID, sessionID, true, 2)

	// The task is deleted. handleTaskDeleted runs synchronously (mirroring
	// deleteTaskWithReasonAndDBDelete's "publish event (sync, fast)" step,
	// which always precedes its "stop agents... in the background" step),
	// tearing down taskParkedStates/taskParkedRevisionFloor/
	// taskMemberRevisionFloor for this task.
	svc.handleTaskDeleted(context.Background(), watcher.TaskEventData{TaskID: taskID})

	// The async task-cleanup goroutine now stops the session's execution,
	// which — because the session ever attested a detached launch — calls
	// removeTaskParkedMember. The row is absent (handleTaskDeleted just
	// dropped it), so this lands in the !ok branch.
	svc.removeTaskParkedMember(context.Background(), taskID, sessionID, 3)

	svc.taskParkedStatesMu.Lock()
	_, floorRecreated := svc.taskMemberRevisionFloor[taskID]
	svc.taskParkedStatesMu.Unlock()

	if floorRecreated {
		t.Fatal("expected taskMemberRevisionFloor[taskID] to stay absent after the task was deleted — recreating it here leaks it for the life of the process, since handleTaskDeleted only fires once per task and task IDs are never reused")
	}
}

// TestRemoveTaskParkedMember_RowAbsentFloorNeverRegresses proves the
// row-absent branch's "!recorded || sessionRevision >= lastRevision" guard
// actually rejects an older, out-of-order revision rather than merely
// accepting a newer one. Review round 11's test-supervisor leg found the
// guard survived mutation to an unconditional write with every existing test
// still green — a pre-existing gap the row-present sibling guard (:554) also
// has, closed here for the row-absent side while the file is already open.
func TestRemoveTaskParkedMember_RowAbsentFloorNeverRegresses(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-row-absent-no-regress", "session-row-absent-no-regress"

	// First row-absent eviction records revision 5 into the floor.
	svc.removeTaskParkedMember(context.Background(), taskID, sessionID, 5)

	// A second, OLDER row-absent eviction for the same session arrives late
	// (out-of-order delivery). It must not regress the floor.
	svc.removeTaskParkedMember(context.Background(), taskID, sessionID, 2)

	svc.taskParkedStatesMu.Lock()
	got := svc.taskMemberRevisionFloor[taskID][sessionID]
	svc.taskParkedStatesMu.Unlock()

	if got != 5 {
		t.Fatalf("expected the floor to stay at the higher revision 5, got %d — an older, out-of-order eviction must never regress it", got)
	}
}

// TestRemoveTaskParkedMember_TombstoneSurvivesDurableRetryHorizon is Review
// round 12's regression guard for COR-001: round 11's fix sized
// taskDeletedTombstoneRetention (10 minutes) against runTaskCleanup's 60s
// FAST-path timeout, but the standard production task-deletion path runs the
// DURABLE, retried cleanup job instead (internal/task/service's
// resourceCleanups is unconditionally wired), whose retry delays are
// 1m, 5m, 15m, 1h, 3h, 6h, 12h — so a session-stop attempt that transiently
// fails and retries can call removeTaskParkedMember tens of minutes to hours
// after handleTaskDeleted ran, long past the old 10-minute window. By then an
// unrelated task's deletion has already swept this task's tombstone (the
// sweep is lazy and wall-clock-only), and the delayed call silently
// recreates taskMemberRevisionFloor[taskID] — reopening the exact leak
// SEC-001 was raised to close.
//
// This reproduces the 3rd-retry case (21 minutes: 1m + 5m + 15m), which is
// already past the old 10-minute window but nowhere near the durable job's
// real worst case. Uses testing/synctest (per apps/backend/AGENTS.md's
// stated preference over time.Sleep) so the 21-minute advance is instant.
func TestRemoveTaskParkedMember_TombstoneSurvivesDurableRetryHorizon(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := createTestServiceWithScheduler(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo(), &mockAgentManager{})
		const taskID, otherTaskID, sessionID = "task-durable-retry", "task-unrelated-sweep-trigger", "session-durable-retry"

		// Session parks — creates the task-level row and the shared
		// memberRevision map that becomes taskMemberRevisionFloor[taskID].
		svc.updateTaskParkedState(context.Background(), taskID, sessionID, true, 2)

		// The task is deleted. handleTaskDeleted tears down its
		// parked-projection state and records a tombstone for it.
		svc.handleTaskDeleted(context.Background(), watcher.TaskEventData{TaskID: taskID})

		// The durable cleanup job's 3rd retry attempt lands 21 minutes later
		// — past the OLD, incorrectly-derived 10-minute tombstone window, but
		// well inside the real worst-case retry horizon.
		time.Sleep(21 * time.Minute)

		// An unrelated task's deletion runs the lazy sweep — the only place
		// taskDeletedTombstones entries are ever evicted.
		svc.handleTaskDeleted(context.Background(), watcher.TaskEventData{TaskID: otherTaskID})

		// The delayed removeTaskParkedMember call for the original,
		// already-deleted task must still find its tombstone and skip the
		// floor write.
		svc.removeTaskParkedMember(context.Background(), taskID, sessionID, 3)

		svc.taskParkedStatesMu.Lock()
		_, floorRecreated := svc.taskMemberRevisionFloor[taskID]
		svc.taskParkedStatesMu.Unlock()

		if floorRecreated {
			t.Fatal("expected taskMemberRevisionFloor[taskID] to stay absent 21 minutes after task deletion — the durable cleanup job's retry schedule can legitimately take far longer than the old 10-minute tombstone window, and a call landing after that window silently reopened SEC-001 (Review round 12, COR-001)")
		}
	})
}

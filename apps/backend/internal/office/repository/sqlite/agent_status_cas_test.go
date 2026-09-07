package sqlite_test

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/office/models"
)

// TestUpdateAgentStatusIfCurrent_MatchesExpected is the ordinary CAS write:
// the row is still in the status the caller expected, so the write lands.
func TestUpdateAgentStatusIfCurrent_MatchesExpected(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	workingStatusAgent(t, repo, "agent-cas-match", "paused")

	changed, err := repo.UpdateAgentStatusIfCurrent(ctx, "agent-cas-match", "paused", "idle", "")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true: expected status matched the row")
	}
	if got := statusOf(t, repo, "agent-cas-match"); got != models.AgentStatusIdle {
		t.Fatalf("status = %q, want idle", got)
	}
}

// TestUpdateAgentStatusIfCurrent_RefusesStaleExpected is the CAS refusal
// this card exists for: a concurrent writer (an operator's manual stop)
// moves the row before this call's write lands, so the write must be a
// no-op rather than clobbering the concurrent writer's status.
func TestUpdateAgentStatusIfCurrent_RefusesStaleExpected(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	workingStatusAgent(t, repo, "agent-cas-stale", "paused")

	// A concurrent writer moves the row before this call's write lands.
	if err := repo.UpdateAgentStatusFields(ctx, "agent-cas-stale", "stopped", ""); err != nil {
		t.Fatalf("seed concurrent stop: %v", err)
	}

	changed, err := repo.UpdateAgentStatusIfCurrent(ctx, "agent-cas-stale", "paused", "idle", "")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if changed {
		t.Fatal("changed = true, want false: expected status no longer matches the row")
	}
	if got := statusOf(t, repo, "agent-cas-stale"); got != models.AgentStatusStopped {
		t.Fatalf("status = %q, want stopped — the stale write must not have clobbered it", got)
	}
}

// TestUpdateAgentStatusIfCurrent_PersistsPauseReasonOnMatch pins that a
// successful CAS also carries the pause reason, not just the status.
func TestUpdateAgentStatusIfCurrent_PersistsPauseReasonOnMatch(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	workingStatusAgent(t, repo, "agent-cas-reason", "idle")

	changed, err := repo.UpdateAgentStatusIfCurrent(ctx, "agent-cas-reason", "idle", "paused", "manual pause")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	agent, err := repo.GetAgentInstance(ctx, "agent-cas-reason")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if agent.PauseReason != "manual pause" {
		t.Fatalf("pause reason = %q, want %q", agent.PauseReason, "manual pause")
	}
}

// TestUpdateAgentStatusIfCurrent_ClearsWorkingRunIDWhenTargetNotWorking pins
// that a write targeting a non-`working` status clears working_run_id, since
// leaving the agent's session is what retires the run's claim on the agent.
func TestUpdateAgentStatusIfCurrent_ClearsWorkingRunIDWhenTargetNotWorking(t *testing.T) {
	repo, db := newTestRepoWithDB(t)
	ctx := context.Background()
	workingStatusAgent(t, repo, "agent-cas-working", "working")
	setWorkingRunID(t, db, "agent-cas-working", "run-1")

	changed, err := repo.UpdateAgentStatusIfCurrent(ctx, "agent-cas-working", "working", "idle", "")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	var workingRunID string
	if err := db.Get(&workingRunID,
		`SELECT working_run_id FROM agent_profiles WHERE id = ?`, "agent-cas-working"); err != nil {
		t.Fatalf("read working_run_id: %v", err)
	}
	if workingRunID != "" {
		t.Fatalf("working_run_id = %q, want cleared", workingRunID)
	}
}

// TestUpdateAgentStatusIfCurrent_PreservesWorkingRunIDWhenTargetIsWorking
// pins the Blocker 1 fix: validateStatusTransition treats `from == to` as a
// no-op transition, so a `working -> working` write (e.g. a stale retry of
// UpdateAgentStatus, or MarkAgentPausedFixed's own final pause-reason clear
// landing after the scheduler claimed a requeued run) reaches this CAS. It
// must not blank out the working_run_id a live run still owns — doing so
// would make ClearAgentWorking's `working_run_id = ?` match forever fail,
// stranding the agent in "working" until a backend restart.
func TestUpdateAgentStatusIfCurrent_PreservesWorkingRunIDWhenTargetIsWorking(t *testing.T) {
	repo, db := newTestRepoWithDB(t)
	ctx := context.Background()
	workingStatusAgent(t, repo, "agent-cas-working-noop", "working")
	setWorkingRunID(t, db, "agent-cas-working-noop", "run-live")

	changed, err := repo.UpdateAgentStatusIfCurrent(ctx, "agent-cas-working-noop", "working", "working", "")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true: expected status matched the row")
	}
	var workingRunID string
	if err := db.Get(&workingRunID,
		`SELECT working_run_id FROM agent_profiles WHERE id = ?`, "agent-cas-working-noop"); err != nil {
		t.Fatalf("read working_run_id: %v", err)
	}
	if workingRunID != "run-live" {
		t.Fatalf("working_run_id = %q, want preserved as %q", workingRunID, "run-live")
	}
}

// TestClearAgentPauseReasonIfCurrent_MatchesExpected is the ordinary CAS
// write: pause_reason is still what the caller expected.
func TestClearAgentPauseReasonIfCurrent_MatchesExpected(t *testing.T) {
	repo, db := newTestRepoWithDB(t)
	ctx := context.Background()
	workingStatusAgent(t, repo, "agent-cas-pause-match", "idle")
	setPauseReason(t, db, "agent-cas-pause-match", "Auto-paused: test")

	changed, err := repo.ClearAgentPauseReasonIfCurrent(ctx, "agent-cas-pause-match", "Auto-paused: test")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	agent, err := repo.GetAgentInstance(ctx, "agent-cas-pause-match")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if agent.PauseReason != "" {
		t.Fatalf("pause reason = %q, want cleared", agent.PauseReason)
	}
}

// TestClearAgentPauseReasonIfCurrent_RefusesStaleExpected pins the CAS
// refusal: a concurrent writer changed the pause reason first, so this
// call must not blindly clear whatever reason is there now.
func TestClearAgentPauseReasonIfCurrent_RefusesStaleExpected(t *testing.T) {
	repo, db := newTestRepoWithDB(t)
	ctx := context.Background()
	workingStatusAgent(t, repo, "agent-cas-pause-stale", "idle")
	setPauseReason(t, db, "agent-cas-pause-stale", "Auto-paused: new reason")

	changed, err := repo.ClearAgentPauseReasonIfCurrent(ctx, "agent-cas-pause-stale", "Auto-paused: stale reason")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if changed {
		t.Fatal("changed = true, want false: expected pause reason no longer matches the row")
	}
	agent, err := repo.GetAgentInstance(ctx, "agent-cas-pause-stale")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if agent.PauseReason != "Auto-paused: new reason" {
		t.Fatalf("pause reason = %q, want unchanged", agent.PauseReason)
	}
}

// TestClearAgentPauseReasonIfCurrent_IgnoresStatus pins the Blocker 2 fix:
// this CAS guards on pause_reason alone, not status, so it succeeds — and
// leaves status untouched — even when the agent has moved on to "working"
// since the caller last observed its status. A status-keyed CAS would
// wrongly refuse this write.
func TestClearAgentPauseReasonIfCurrent_IgnoresStatus(t *testing.T) {
	repo, db := newTestRepoWithDB(t)
	ctx := context.Background()
	workingStatusAgent(t, repo, "agent-cas-pause-working", "working")
	setPauseReason(t, db, "agent-cas-pause-working", "Auto-paused: test")

	changed, err := repo.ClearAgentPauseReasonIfCurrent(ctx, "agent-cas-pause-working", "Auto-paused: test")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true: a status CAS would have refused this, a pause-reason CAS must not")
	}
	agent, err := repo.GetAgentInstance(ctx, "agent-cas-pause-working")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if agent.Status != models.AgentStatusWorking {
		t.Fatalf("status = %q, want unchanged (working)", agent.Status)
	}
	if agent.PauseReason != "" {
		t.Fatalf("pause reason = %q, want cleared", agent.PauseReason)
	}
}

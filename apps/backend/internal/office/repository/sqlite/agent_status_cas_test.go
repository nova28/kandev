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

// TestUpdateAgentStatusIfCurrent_ClearsWorkingRunID pins that this
// primitive always clears working_run_id on a successful write — unlike
// UpdateAgentStatusFields, it carries no `working` no-op case, because it
// is not a "working" writer (MarkAgentWorking/ClearAgentWorking own that
// transition's own CAS in agent_working_status.go).
func TestUpdateAgentStatusIfCurrent_ClearsWorkingRunID(t *testing.T) {
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

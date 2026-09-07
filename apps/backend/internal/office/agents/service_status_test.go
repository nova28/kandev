package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/office/models"
)

// TestUpdateAgentStatusIfCurrent_RefusesConcurrentStop is the agents
// package's regression pin for the same interleaving service/failure_test.go
// closes for the office service: an actor that read status="paused" (T1)
// must not have its write-back succeed once a concurrent stop (T2) has
// already moved the row. AgentService.UpdateAgentStatus shares the exact
// repository this package depends on (`Identical treatment` in the Triage
// card), so this pins that the CAS primitive it now routes through refuses
// a stale write here too, not just when called from internal/office/service.
//
// AgentService.UpdateAgentStatus itself always re-reads status fresh before
// validating and writing, so its own read-then-write pair cannot be made
// stale from outside a single call without genuine concurrency — and a
// concurrency test can't discriminate a real clobber from a second,
// legitimate call simply continuing the state machine forward (both look
// identical from the outside). The CAS primitive underneath it is what
// carries the actual guarantee, and is what this test exercises directly.
func TestUpdateAgentStatusIfCurrent_RefusesConcurrentStop(t *testing.T) {
	svc, repo := newTestAgentService(t)
	agent := &models.AgentInstance{WorkspaceID: "ws-1", Name: "Racer", Role: models.AgentRoleWorker}
	stored := createAndGetAgent(t, svc, repo, agent)

	if _, err := svc.UpdateAgentStatus(context.Background(), stored.ID, models.AgentStatusPaused, "manual pause"); err != nil {
		t.Fatalf("pause: %v", err)
	}

	// T1 observes status=paused.
	observed, err := repo.GetAgentInstance(context.Background(), stored.ID)
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if observed.Status != models.AgentStatusPaused {
		t.Fatalf("precondition: status = %q, want paused", observed.Status)
	}

	// T2: a concurrent stop lands before T1's write-back runs.
	if changed, err := repo.UpdateAgentStatusIfCurrent(
		context.Background(), stored.ID, string(models.AgentStatusPaused), string(models.AgentStatusStopped), "",
	); err != nil || !changed {
		t.Fatalf("seed concurrent stop: changed=%v err=%v", changed, err)
	}

	// T1's write-back asserts the status it originally observed.
	changed, err := repo.UpdateAgentStatusIfCurrent(
		context.Background(), stored.ID, string(observed.Status), string(models.AgentStatusIdle), "",
	)
	if err != nil {
		t.Fatalf("write-back: %v", err)
	}
	if changed {
		t.Fatal("changed = true, want false: T1's stale write-back must not clobber the concurrent stop")
	}

	got, err := repo.GetAgentInstance(context.Background(), stored.ID)
	if err != nil {
		t.Fatalf("get agent after: %v", err)
	}
	if got.Status != models.AgentStatusStopped {
		t.Fatalf("status = %q, want stopped", got.Status)
	}
}

// TestUpdateAgentStatus_PersistsValidTransition exercises
// AgentService.UpdateAgentStatus itself, not the repository primitive
// underneath it: a valid transition is validated, persisted via the CAS,
// and the returned agent reflects the new state.
func TestUpdateAgentStatus_PersistsValidTransition(t *testing.T) {
	svc, repo := newTestAgentService(t)
	agent := &models.AgentInstance{WorkspaceID: "ws-1", Name: "Direct", Role: models.AgentRoleWorker}
	stored := createAndGetAgent(t, svc, repo, agent)

	updated, err := svc.UpdateAgentStatus(context.Background(), stored.ID, models.AgentStatusPaused, "manual pause")
	if err != nil {
		t.Fatalf("UpdateAgentStatus: %v", err)
	}
	if updated.Status != models.AgentStatusPaused || updated.PauseReason != "manual pause" {
		t.Fatalf("returned agent status=%q pause_reason=%q, want paused/%q",
			updated.Status, updated.PauseReason, "manual pause")
	}
	got, err := repo.GetAgentInstance(context.Background(), stored.ID)
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if got.Status != models.AgentStatusPaused || got.PauseReason != "manual pause" {
		t.Fatalf("persisted status=%q pause_reason=%q, want paused/%q",
			got.Status, got.PauseReason, "manual pause")
	}
}

// TestUpdateAgentStatus_RejectsInvalidTransition pins that
// AgentService.UpdateAgentStatus validates before it ever reaches the
// CAS: a transition absent from allowedTransitions is rejected and
// nothing is written.
func TestUpdateAgentStatus_RejectsInvalidTransition(t *testing.T) {
	svc, repo := newTestAgentService(t)
	agent := &models.AgentInstance{WorkspaceID: "ws-1", Name: "Invalid", Role: models.AgentRoleWorker}
	stored := createAndGetAgent(t, svc, repo, agent)

	// idle -> working is not in allowedTransitions[idle]; only
	// MarkAgentWorking (agent_working_status.go) owns that transition.
	_, err := svc.UpdateAgentStatus(context.Background(), stored.ID, models.AgentStatusWorking, "")
	if !errors.Is(err, ErrAgentStatusTransition) {
		t.Fatalf("err = %v, want ErrAgentStatusTransition", err)
	}
	got, err := repo.GetAgentInstance(context.Background(), stored.ID)
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if got.Status != models.AgentStatusIdle {
		t.Fatalf("status = %q, want unchanged idle", got.Status)
	}
}

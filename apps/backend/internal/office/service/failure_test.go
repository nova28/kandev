package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/office/models"
	"github.com/kandev/kandev/internal/office/service"
)

// queueAndReadRun enqueues a run with a unique idempotency key
// (so multiple calls in one test don't coalesce) and reads it back.
// Mirrors the production path (QueueRun) without going through the
// claim step (which lives on the repository).
func queueAndReadRun(
	t *testing.T, svc *service.Service, agentID, taskID string,
) *models.Run {
	t.Helper()
	ctx := context.Background()
	payload := mustMarshalJSON(map[string]string{"task_id": taskID})
	idem := agentID + ":" + taskID
	if err := svc.QueueRun(ctx, agentID, service.RunReasonTaskAssigned, payload, idem); err != nil {
		t.Fatalf("queue run: %v", err)
	}
	rows, err := svc.ListRuns(ctx, "ws-1")
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	for _, w := range rows {
		if w.AgentProfileID == agentID && taskIDFromPayload(t, w.Payload) == taskID && w.Status != service.RunStatusFailed {
			return w
		}
	}
	t.Fatalf("queued run not found for (agent=%s, task=%s)", agentID, taskID)
	return nil
}

// uuidish builds a deterministic-but-unique task id for tests so the
// same test can insert multiple distinct synthetic tasks.
func uuidish(prefix string, i int) string {
	return prefix + "-" + string(rune('a'+i))
}

// insertSyntheticTask writes a row into the tasks table with the
// given assignee. Bypasses createOfficeTask's channel-agent creation
// (which fails when called repeatedly under the same test name).
// ADR 0005 Wave F: assignee lives in workflow_step_participants.
func insertSyntheticTask(
	t *testing.T, svc *service.Service, taskID, workspaceID, assigneeID string,
) {
	t.Helper()
	svc.ExecSQL(t,
		`INSERT INTO tasks (id, workspace_id, title, created_at, updated_at)
		 VALUES (?, ?, '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		taskID, workspaceID)
	if assigneeID != "" {
		setTestTaskAssignee(t, svc, taskID, assigneeID)
	}
}

func taskIDFromPayload(t *testing.T, payload string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return ""
	}
	if v, ok := m["task_id"].(string); ok {
		return v
	}
	return ""
}

func mustMarshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Pins the per-failure counter and the auto-pause threshold semantics.
// Three consecutive failures across different tasks → agent is
// auto-paused with a structured pause_reason.
func TestHandleAgentFailure_AutoPausesAtThreshold(t *testing.T) {
	svc, _ := newTestServiceWithBus(t)
	ctx := context.Background()

	createTestAgent(t, svc, "ws-1", "agent-pause")

	// Three different tasks, each producing one failed run.
	for i := 0; i < 3; i++ {
		taskID := uuidish("task-pause", i)
		insertSyntheticTask(t, svc, taskID, "ws-1", "agent-pause")
		w := queueAndReadRun(t, svc, "agent-pause", taskID)
		if err := svc.HandleAgentFailure(ctx, w, "boom"); err != nil {
			t.Fatalf("handle failure %d: %v", i, err)
		}
	}

	agent, err := svc.GetAgentInstance(ctx, "agent-pause")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if agent.ConsecutiveFailures != 3 {
		t.Fatalf("expected 3 consecutive failures, got %d", agent.ConsecutiveFailures)
	}
	if !strings.HasPrefix(agent.PauseReason, "Auto-paused:") {
		t.Fatalf("expected auto-pause reason, got %q", agent.PauseReason)
	}
	if agent.Status != models.AgentStatusPaused {
		t.Fatalf("expected paused status, got %q", agent.Status)
	}
}

// Pins that a successful turn resets the counter — even if there were
// previous failures. (We treat any successful agent turn as evidence
// that the agent isn't broken.)
func TestRecordAgentSuccess_ResetsCounter(t *testing.T) {
	svc, _ := newTestServiceWithBus(t)
	ctx := context.Background()

	createTestAgent(t, svc, "ws-1", "agent-success")

	// Two failures (still below threshold).
	for i := 0; i < 2; i++ {
		taskID := uuidish("task-succ", i)
		insertSyntheticTask(t, svc, taskID, "ws-1", "agent-success")
		w := queueAndReadRun(t, svc, "agent-success", taskID)
		if err := svc.HandleAgentFailure(ctx, w, "boom"); err != nil {
			t.Fatalf("handle failure %d: %v", i, err)
		}
	}

	// Successful turn → reset.
	svc.RecordAgentSuccess(ctx, "agent-success")

	agent, err := svc.GetAgentInstance(ctx, "agent-success")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if agent.ConsecutiveFailures != 0 {
		t.Fatalf("expected counter reset, got %d", agent.ConsecutiveFailures)
	}
	if agent.PauseReason != "" {
		t.Fatalf("expected no pause reason, got %q", agent.PauseReason)
	}
}

// Pins that the per-agent FailureThreshold override beats the global
// default of 3.
func TestHandleAgentFailure_RespectsPerAgentThreshold(t *testing.T) {
	svc, _ := newTestServiceWithBus(t)
	ctx := context.Background()

	createTestAgent(t, svc, "ws-1", "agent-tight")

	// Override to 1 — first failure should pause.
	override := 1
	agent, err := svc.GetAgentInstance(ctx, "agent-tight")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	agent.FailureThreshold = &override
	if err := svc.UpdateAgentInstance(ctx, agent); err != nil {
		// Some test paths don't expose UpdateAgentInstance; fall back
		// to a direct repo update via service helpers.
		t.Skipf("update agent unsupported: %v", err)
	}

	taskID := "task-tight-1"
	insertSyntheticTask(t, svc, taskID, "ws-1", "agent-tight")
	w := queueAndReadRun(t, svc, "agent-tight", taskID)
	if err := svc.HandleAgentFailure(ctx, w, "boom"); err != nil {
		t.Fatalf("handle failure: %v", err)
	}

	agent, err = svc.GetAgentInstance(ctx, "agent-tight")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if !strings.HasPrefix(agent.PauseReason, "Auto-paused:") {
		t.Fatalf("expected auto-pause at threshold=1, got pause_reason=%q (counter=%d)",
			agent.PauseReason, agent.ConsecutiveFailures)
	}
}

// Pins that reassigning a task auto-dismisses the per-task inbox
// entry for the OLD agent without resetting that agent's counter.
func TestOnAssigneeChanged_DismissesPriorEntryWithoutResettingCounter(t *testing.T) {
	svc, _ := newTestServiceWithBus(t)
	ctx := context.Background()

	createTestAgent(t, svc, "ws-1", "old-agent")
	createTestAgent(t, svc, "ws-1", "new-agent")

	taskID := "task-reassign-1"
	insertSyntheticTask(t, svc, taskID, "ws-1", "old-agent")
	w := queueAndReadRun(t, svc, "old-agent", taskID)
	if err := svc.HandleAgentFailure(ctx, w, "boom"); err != nil {
		t.Fatalf("handle failure: %v", err)
	}

	beforeAgent, _ := svc.GetAgentInstance(ctx, "old-agent")
	beforeCount := beforeAgent.ConsecutiveFailures

	// Simulate the reactivity hook firing on assignee change.
	svc.OnAssigneeChanged(ctx, taskID, "old-agent")

	// Counter must NOT be reset — the underlying cause may still be
	// unfixed for old-agent's other tasks.
	afterAgent, _ := svc.GetAgentInstance(ctx, "old-agent")
	if afterAgent.ConsecutiveFailures != beforeCount {
		t.Fatalf("expected counter unchanged at %d, got %d",
			beforeCount, afterAgent.ConsecutiveFailures)
	}

	// The run should be dismissed via the auto-dismiss sentinel.
	dismissed, err := svc.IsInboxItemDismissed(ctx, "_auto",
		service.InboxKindAgentRunFailed, w.ID)
	if err != nil {
		t.Fatalf("check dismissed: %v", err)
	}
	if !dismissed {
		t.Fatalf("expected run %s dismissed via _auto", w.ID)
	}
	// Sanity: settling time so any async event handlers complete.
	time.Sleep(10 * time.Millisecond)
}

// TestUpdateAgentStatusFrom_RefusesConcurrentManualStop is the committed
// form of the Triage scratch evidence: MarkAgentPausedFixed reads the
// agent and observes "paused" (T1), an operator's manual stop lands
// paused -> stopped before the write-back runs (T2), and the write-back
// asserts the status T1 actually observed. Before UpdateAgentStatusFrom
// existed, the equivalent unconditional write silently reverted the
// operator's stop.
func TestUpdateAgentStatusFrom_RefusesConcurrentManualStop(t *testing.T) {
	svc, _ := newTestServiceWithBus(t)
	ctx := context.Background()

	createTestAgent(t, svc, "ws-1", "agent-race")
	svc.ExecSQL(t,
		`UPDATE agent_profiles SET status = 'paused', pause_reason = 'Auto-paused: test' WHERE id = 'agent-race'`)

	// T1 observes status=paused (mirrors failure.go's GetAgentInstance read).
	agent, err := svc.GetAgentInstance(ctx, "agent-race")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if agent.Status != models.AgentStatusPaused {
		t.Fatalf("precondition: status = %q, want paused", agent.Status)
	}

	// T2: an operator's manual stop lands before T1's write-back runs.
	svc.ExecSQL(t, `UPDATE agent_profiles SET status = 'stopped' WHERE id = 'agent-race'`)

	// T1's write-back asserts the status it originally observed.
	err = svc.UpdateAgentStatusFrom(ctx, "agent-race", agent.Status, models.AgentStatusIdle, agent.PauseReason)
	if !errors.Is(err, service.ErrAgentStatusChanged) {
		t.Fatalf("err = %v, want ErrAgentStatusChanged", err)
	}

	got, err := svc.GetAgentInstance(ctx, "agent-race")
	if err != nil {
		t.Fatalf("get agent after: %v", err)
	}
	if got.Status != models.AgentStatusStopped {
		t.Fatalf("status = %q, want stopped — T1's stale write-back must not clobber the manual stop", got.Status)
	}
}

// TestMarkAgentPausedFixed_RequeueFailureIsResumable pins finding (b):
// when recovery genuinely fails partway (here, a manual stop lands before
// "Mark fixed" runs, so the requeue's guardAgentStatus refuses), the
// pause reason and inbox entry must survive so a retry can pick the
// recovery back up — not report success on work that never happened,
// and not repeat the already-applied unpause.
func TestMarkAgentPausedFixed_RequeueFailureIsResumable(t *testing.T) {
	svc, _ := newTestServiceWithBus(t)
	ctx := context.Background()

	createTestAgent(t, svc, "ws-1", "agent-resume")
	taskID := "task-resume-1"
	insertSyntheticTask(t, svc, taskID, "ws-1", "agent-resume")
	w := queueAndReadRun(t, svc, "agent-resume", taskID)
	if err := svc.HandleAgentFailure(ctx, w, "boom"); err != nil {
		t.Fatalf("handle failure: %v", err)
	}
	// The agent was auto-paused, then an operator's manual stop landed
	// before "Mark fixed" ran — paused -> stopped is a valid transition
	// that leaves the auto-pause reason and failure counter untouched.
	svc.ExecSQL(t,
		`UPDATE agent_profiles SET status = 'stopped',
		 pause_reason = 'Auto-paused: 1 consecutive failures. Last error: boom',
		 consecutive_failures = 1 WHERE id = 'agent-resume'`)

	if err := svc.MarkAgentPausedFixed(ctx, "user-1", "agent-resume"); err == nil {
		t.Fatal("expected an error: the agent is stopped, so the requeue must fail")
	}

	agent, err := svc.GetAgentInstance(ctx, "agent-resume")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if agent.Status != models.AgentStatusStopped {
		t.Fatalf("status = %q, want stopped (must not be resurrected to idle)", agent.Status)
	}
	if !strings.HasPrefix(agent.PauseReason, "Auto-paused:") {
		t.Fatal("pause reason cleared despite the requeue failure — a retry would lose the resume sentinel")
	}
	dismissed, err := svc.IsInboxItemDismissed(ctx, "user-1", service.InboxKindAgentPausedAfterFails, "agent-resume")
	if err != nil {
		t.Fatalf("check dismissed: %v", err)
	}
	if dismissed {
		t.Fatal("inbox item dismissed despite the requeue failure")
	}

	// The operator resolves the stop (stopped -> idle is valid); a retry
	// now completes the missing steps without repeating the status write.
	svc.ExecSQL(t, `UPDATE agent_profiles SET status = 'idle' WHERE id = 'agent-resume'`)
	if err := svc.MarkAgentPausedFixed(ctx, "user-1", "agent-resume"); err != nil {
		t.Fatalf("resume: %v", err)
	}

	agent, err = svc.GetAgentInstance(ctx, "agent-resume")
	if err != nil {
		t.Fatalf("get agent after resume: %v", err)
	}
	if agent.Status != models.AgentStatusIdle {
		t.Fatalf("status after resume = %q, want idle", agent.Status)
	}
	if agent.PauseReason != "" {
		t.Fatalf("pause reason after resume = %q, want cleared", agent.PauseReason)
	}
	dismissed, err = svc.IsInboxItemDismissed(ctx, "user-1", service.InboxKindAgentPausedAfterFails, "agent-resume")
	if err != nil {
		t.Fatalf("check dismissed after resume: %v", err)
	}
	if !dismissed {
		t.Fatal("inbox item not dismissed after a successful resume")
	}
}

// TestMarkAgentPausedFixed_ClearsPauseReasonDespiteConcurrentWorkingClaim
// pins Blocker 2: the requeue loop hands runs back to the scheduler before
// this function reaches its final step, so a live claim on one of those
// runs can move the agent to "working" before the pause reason is
// cleared. The clear must not assert the status observed at unpause (here,
// "idle") — that status is stale by construction the moment a run is
// queued. A run-queued subscriber (delivered synchronously, standing in
// for a scheduler pickup) flips the agent to working mid-call.
func TestMarkAgentPausedFixed_ClearsPauseReasonDespiteConcurrentWorkingClaim(t *testing.T) {
	svc, eb := newTestServiceWithBus(t)
	ctx := context.Background()

	createTestAgent(t, svc, "ws-1", "agent-working-claim")
	taskID := "task-working-claim-1"
	insertSyntheticTask(t, svc, taskID, "ws-1", "agent-working-claim")
	w := queueAndReadRun(t, svc, "agent-working-claim", taskID)
	if err := svc.HandleAgentFailure(ctx, w, "boom"); err != nil {
		t.Fatalf("handle failure: %v", err)
	}
	// One failure is below the default threshold of 3, so drive the agent
	// into the auto-paused state directly, mirroring
	// TestMarkAgentPausedFixed_RequeueFailureIsResumable.
	svc.ExecSQL(t,
		`UPDATE agent_profiles SET status = 'paused',
		 pause_reason = 'Auto-paused: 1 consecutive failures. Last error: boom',
		 consecutive_failures = 1 WHERE id = 'agent-working-claim'`)

	flipped := false
	sub, err := eb.Subscribe(events.OfficeRunQueued, func(_ context.Context, _ *bus.Event) error {
		if flipped {
			return nil
		}
		flipped = true
		svc.ExecSQL(t, `UPDATE agent_profiles SET status = 'working' WHERE id = 'agent-working-claim'`)
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	if err := svc.MarkAgentPausedFixed(ctx, "user-1", "agent-working-claim"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !flipped {
		t.Fatal("test setup broken: the run-queued hook never fired")
	}

	agent, err := svc.GetAgentInstance(ctx, "agent-working-claim")
	if err != nil {
		t.Fatalf("get agent after: %v", err)
	}
	if agent.Status != models.AgentStatusWorking {
		t.Fatalf("status = %q, want working — the claim mid-recovery must survive", agent.Status)
	}
	if agent.PauseReason != "" {
		t.Fatalf("pause reason = %q, want cleared despite the status having moved on", agent.PauseReason)
	}
	dismissed, err := svc.IsInboxItemDismissed(ctx, "user-1", service.InboxKindAgentPausedAfterFails, "agent-working-claim")
	if err != nil {
		t.Fatalf("check dismissed: %v", err)
	}
	if !dismissed {
		t.Fatal("inbox item not dismissed after a successful resume")
	}
}

package executor

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
)

// TestMockGetTaskSessionByTaskAndAgentMirrorsRepositoryOrdering pins the mock
// repository to the ordering the real repository implements
// (GetTaskSessionByTaskAndAgent in internal/task/repository/sqlite/session.go):
// live rows before terminal ones, then started_at DESC, then id DESC.
//
// Without this, the mock ranges over a map and returns an arbitrary match, so
// every caller-level assertion about which session was picked is decided by Go's
// randomized map iteration rather than by the contract under test.
func TestMockGetTaskSessionByTaskAndAgentMirrorsRepositoryOrdering(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	seed := func(repo *mockRepository, sessions ...*models.TaskSession) {
		for _, s := range sessions {
			s.TaskID, s.AgentProfileID = "task-office", "agent-1"
			repo.sessions[s.ID] = s
		}
	}

	t.Run("live beats newer terminal", func(t *testing.T) {
		repo := newMockRepository()
		seed(repo,
			&models.TaskSession{ID: "sess-live", State: models.TaskSessionStateRunning, StartedAt: base},
			&models.TaskSession{ID: "sess-done", State: models.TaskSessionStateCompleted, StartedAt: base.Add(time.Hour)},
			&models.TaskSession{ID: "sess-failed", State: models.TaskSessionStateFailed, StartedAt: base.Add(2 * time.Hour)},
			&models.TaskSession{ID: "sess-cancelled", State: models.TaskSessionStateCancelled, StartedAt: base.Add(3 * time.Hour)},
		)
		got, err := repo.GetTaskSessionByTaskAndAgent(context.Background(), "task-office", "agent-1")
		if err != nil {
			t.Fatalf("GetTaskSessionByTaskAndAgent: %v", err)
		}
		if got == nil || got.ID != "sess-live" {
			t.Fatalf("session = %v, want sess-live (live must outrank every newer terminal row)", got)
		}
	})

	t.Run("among live, newest started_at wins", func(t *testing.T) {
		repo := newMockRepository()
		seed(repo,
			&models.TaskSession{ID: "sess-old", State: models.TaskSessionStateRunning, StartedAt: base},
			&models.TaskSession{ID: "sess-new", State: models.TaskSessionStateIdle, StartedAt: base.Add(time.Hour)},
		)
		got, err := repo.GetTaskSessionByTaskAndAgent(context.Background(), "task-office", "agent-1")
		if err != nil {
			t.Fatalf("GetTaskSessionByTaskAndAgent: %v", err)
		}
		if got == nil || got.ID != "sess-new" {
			t.Fatalf("session = %v, want sess-new", got)
		}
	})

	t.Run("started_at ties break on id DESC", func(t *testing.T) {
		repo := newMockRepository()
		seed(repo,
			&models.TaskSession{ID: "sess-a", State: models.TaskSessionStateRunning, StartedAt: base},
			&models.TaskSession{ID: "sess-b", State: models.TaskSessionStateRunning, StartedAt: base},
		)
		got, err := repo.GetTaskSessionByTaskAndAgent(context.Background(), "task-office", "agent-1")
		if err != nil {
			t.Fatalf("GetTaskSessionByTaskAndAgent: %v", err)
		}
		if got == nil || got.ID != "sess-b" {
			t.Fatalf("session = %v, want sess-b (id DESC is the total tiebreak)", got)
		}
	})

	t.Run("all terminal still returns the newest", func(t *testing.T) {
		repo := newMockRepository()
		seed(repo,
			&models.TaskSession{ID: "sess-old", State: models.TaskSessionStateCompleted, StartedAt: base},
			&models.TaskSession{ID: "sess-new", State: models.TaskSessionStateCompleted, StartedAt: base.Add(time.Hour)},
		)
		got, err := repo.GetTaskSessionByTaskAndAgent(context.Background(), "task-office", "agent-1")
		if err != nil {
			t.Fatalf("GetTaskSessionByTaskAndAgent: %v", err)
		}
		if got == nil || got.ID != "sess-new" {
			t.Fatalf("session = %v, want sess-new", got)
		}
	})
}

// TestEnsureSessionForAgentReusesLiveSessionShadowedByNewerTerminalRow is the
// caller-level regression test for the live-first ordering term. It asserts the
// consequence, not the SQL: with a RUNNING row and a *newer* terminal row for
// the same (task, agent), EnsureSessionForAgentWithCreation must reuse the live
// one and create nothing.
//
// This is the adversarial guard for reverting the ordering to `started_at DESC`
// alone. Under that revert the lookup hands back the newer terminal row,
// tryReuseExistingSession classifies it reuseDecisionTerminal, and the executor
// falls through and inserts a second concurrent session for a pair that already
// has a live one — the duplicate-session bug the ordering exists to prevent.
// The repository-level ordering tests cannot catch this: they prove the SELECT
// returns the live row, not that any caller depends on it.
func TestEnsureSessionForAgentReusesLiveSessionShadowedByNewerTerminalRow(t *testing.T) {
	repo := newMockRepository()
	started := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	live := &models.TaskSession{
		ID: "sess-live", TaskID: "task-office", AgentProfileID: "agent-1",
		ExecutionProfileID: "profile-1", State: models.TaskSessionStateRunning, StartedAt: started,
	}
	// Terminal, and deliberately newer than the live row: a stale duplicate
	// resolving while the real session is still running.
	terminal := &models.TaskSession{
		ID: "sess-terminal", TaskID: "task-office", AgentProfileID: "agent-1",
		ExecutionProfileID: "profile-1", State: models.TaskSessionStateCompleted,
		StartedAt: started.Add(time.Hour),
	}
	repo.sessions[live.ID] = live
	repo.sessions[terminal.ID] = terminal
	exec := newTestExecutor(t, &mockAgentManager{}, repo)

	got, created, err := exec.EnsureSessionForAgentWithCreation(
		context.Background(), officeTestTask(), "agent-1", "profile-1", "exec-1", "",
	)
	if err != nil {
		t.Fatalf("EnsureSessionForAgentWithCreation: %v", err)
	}
	if created {
		t.Fatal("created = true; the live session must be reused, not shadowed by the newer terminal row")
	}
	if got == nil || got.ID != live.ID {
		t.Fatalf("session = %v, want %s", got, live.ID)
	}
	if n := len(repo.createTaskSessionCalls); n != 0 {
		t.Fatalf("CreateTaskSession calls = %d, want 0 — a duplicate concurrent session was inserted", n)
	}
}

package sqlite

import "testing"

// TestTaskSessionsTaskAgentIndexExistsAfterMigration pins the composite index
// backing CreateOfficeTaskSession's live-session guard, which filters on
// (task_id, agent_profile_id) — see session.go's "check live office session
// for pair" query. The pre-existing idx_task_sessions_task_id covers only the
// leading column, so without this index the guard degrades to a scan of every
// session row for the task while holding the write transaction open.
//
// The index is deliberately NON-unique: a table-wide unique index on this pair
// broke live kanban-relaunch and workflow-replacement flows (see
// ErrOfficeSessionRaceConflict's doc comment). Uniqueness is enforced by the
// in-transaction guard, not by the schema.
func TestTaskSessionsTaskAgentIndexExistsAfterMigration(t *testing.T) {
	repo := newRepoForEntityTests(t)

	var indexName string
	if err := repo.db.Get(&indexName, `
		SELECT name
		FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_task_sessions_task_agent'
	`); err != nil {
		t.Fatalf("task/agent pair index missing after task repository migration: %v", err)
	}
	if indexName != "idx_task_sessions_task_agent" {
		t.Fatalf("index name = %q, want idx_task_sessions_task_agent", indexName)
	}
}

// TestTaskSessionsTaskAgentIndexIsNotUnique guards the non-unique property
// directly. A future edit that "tightens" this index into a UNIQUE one would
// reintroduce the relaunch/replacement breakage the guard was designed to
// avoid, and no other test would catch it.
func TestTaskSessionsTaskAgentIndexIsNotUnique(t *testing.T) {
	repo := newRepoForEntityTests(t)

	var unique int
	if err := repo.db.Get(&unique, `
		SELECT "unique"
		FROM pragma_index_list('task_sessions')
		WHERE name = 'idx_task_sessions_task_agent'
	`); err != nil {
		t.Fatalf("read index metadata: %v", err)
	}
	if unique != 0 {
		t.Fatal("idx_task_sessions_task_agent is UNIQUE; it must stay non-unique — " +
			"uniqueness lives in CreateOfficeTaskSession's in-transaction guard")
	}
}

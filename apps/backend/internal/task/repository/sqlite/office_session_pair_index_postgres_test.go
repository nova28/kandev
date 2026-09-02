package sqlite

import (
	"testing"

	"github.com/kandev/kandev/internal/testutil"
)

// TestPostgresTaskSessionsTaskAgentIndex is the Postgres counterpart to
// TestTaskSessionsTaskAgentIndexExistsAfterMigration /
// TestTaskSessionsTaskAgentIndexIsNotUnique. CreateOfficeTaskSession's live
// -session guard runs on both dialects, so the index backing it must exist —
// and must stay non-unique — on both. Skips unless KANDEV_TEST_POSTGRES_DSN
// is set.
func TestPostgresTaskSessionsTaskAgentIndex(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	// Production boot order (internal/persistence/provider.go) creates
	// kandev_meta before the task repository; mirror that here since this
	// package's repository never creates it itself.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS kandev_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("create kandev_meta: %v", err)
	}
	if _, err := NewWithDB(db, db, nil); err != nil {
		t.Fatalf("init postgres schema: %v", err)
	}

	var name string
	if err := db.Get(&name, `SELECT indexname FROM pg_indexes WHERE indexname = $1`, "idx_task_sessions_task_agent"); err != nil {
		t.Fatalf("task/agent pair index missing on postgres: %v", err)
	}

	var isUnique bool
	if err := db.Get(&isUnique, `
		SELECT i.indisunique
		FROM pg_class c
		JOIN pg_index i ON i.indexrelid = c.oid
		WHERE c.relname = $1
	`, "idx_task_sessions_task_agent"); err != nil {
		t.Fatalf("read index metadata: %v", err)
	}
	if isUnique {
		t.Fatal("idx_task_sessions_task_agent is UNIQUE on postgres; it must stay non-unique — " +
			"uniqueness lives in CreateOfficeTaskSession's in-transaction guard")
	}
}

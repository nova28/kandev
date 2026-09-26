package coordinator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/db/dialect"
)

// ErrNotFound is returned when a coordinator, proposal or stall row does not
// exist, or exists but is scoped to a different workspace or coordinator.
var ErrNotFound = errors.New("coordinator: not found")

// Store provides SQLite/PostgreSQL persistence for coordinators, their
// proposals and stall records. See docs/specs/coordinator/system-design/
// {coordinators,proposals,needs-you}.md.
type Store struct {
	db *sqlx.DB // writer
	ro *sqlx.DB // reader

	// now is the injectable clock. Overridden only by tests (decision 7's
	// deterministic-order test needs a strictly increasing fake clock).
	now func() time.Time
}

// NewStore creates the coordinator store and initializes its schema.
func NewStore(writer, reader *sqlx.DB) (*Store, error) {
	s := &Store{
		db:  writer,
		ro:  reader,
		now: func() time.Time { return time.Now().UTC() },
	}
	if err := s.initSchema(); err != nil {
		return nil, fmt.Errorf("coordinator schema init: %w", err)
	}
	return s, nil
}

// createTablesSQL defines all three tables up front: no later work order adds
// a migration (docs/plans/workspace-coordinator/task-01-shared-interface.md).
const createTablesSQL = `
	CREATE TABLE IF NOT EXISTS coordinators (
		id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL,
		name TEXT NOT NULL,
		agent_profile_id TEXT NOT NULL,
		executor_profile_id TEXT NOT NULL,
		context TEXT NOT NULL DEFAULT '',
		conversation_task_id TEXT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_coordinators_workspace_created ON coordinators(workspace_id, created_at, id);

	CREATE TABLE IF NOT EXISTS coordinator_proposals (
		id TEXT PRIMARY KEY,
		coordinator_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		status TEXT NOT NULL,
		spec_json TEXT NOT NULL,
		final_spec_json TEXT,
		claimed_at DATETIME,
		claim_token TEXT,
		task_id TEXT,
		error TEXT,
		reject_reason TEXT,
		decided_by TEXT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_coordinator_proposals_coordinator ON coordinator_proposals(coordinator_id, status, created_at, id);

	CREATE TABLE IF NOT EXISTS coordinator_stalls (
		task_id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL,
		stalled_for_ms INTEGER NOT NULL,
		last_event_at DATETIME NOT NULL,
		detected_at DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_coordinator_stalls_workspace ON coordinator_stalls(workspace_id);
`

func (s *Store) initSchema() error {
	rendered := dialect.MustRenderSchema(s.db.DriverName(), createTablesSQL)
	if _, err := s.db.Exec(rendered); err != nil {
		return err
	}
	migrate := db.NewRequiredMigrateLogger(s.db, nil)
	if err := migrate.Err(); err != nil {
		return fmt.Errorf("required coordinator migration: %w", err)
	}
	return nil
}

// coordinatorRow is the DB scan target for coordinators.
type coordinatorRow struct {
	ID                 string         `db:"id"`
	WorkspaceID        string         `db:"workspace_id"`
	Name               string         `db:"name"`
	AgentProfileID     string         `db:"agent_profile_id"`
	ExecutorProfileID  string         `db:"executor_profile_id"`
	Context            string         `db:"context"`
	ConversationTaskID sql.NullString `db:"conversation_task_id"`
	CreatedAt          time.Time      `db:"created_at"`
	UpdatedAt          time.Time      `db:"updated_at"`
}

func (r *coordinatorRow) toCoordinator() *Coordinator {
	c := &Coordinator{
		ID:                r.ID,
		WorkspaceID:       r.WorkspaceID,
		Name:              r.Name,
		AgentProfileID:    r.AgentProfileID,
		ExecutorProfileID: r.ExecutorProfileID,
		Context:           r.Context,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
	if r.ConversationTaskID.Valid {
		id := r.ConversationTaskID.String
		c.ConversationTaskID = &id
	}
	return c
}

const coordinatorColumns = `id, workspace_id, name, agent_profile_id, executor_profile_id, context, conversation_task_id, created_at, updated_at`

// CreateCoordinator inserts a new coordinator, assigning an id and timestamps
// when unset.
func (s *Store) CreateCoordinator(ctx context.Context, c *Coordinator) error {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	now := s.now()
	c.CreatedAt = now
	c.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, s.db.Rebind(`
		INSERT INTO coordinators (`+coordinatorColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		c.ID, c.WorkspaceID, c.Name, c.AgentProfileID, c.ExecutorProfileID, c.Context,
		nullableString(c.ConversationTaskID), c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert coordinator: %w", err)
	}
	return nil
}

// GetCoordinator returns the coordinator with the given id in the given
// workspace. A coordinator whose workspace_id differs from workspaceID is
// treated as absent, per coordinators.md#routes.
func (s *Store) GetCoordinator(ctx context.Context, workspaceID, id string) (*Coordinator, error) {
	var row coordinatorRow
	err := s.ro.GetContext(ctx, &row, s.ro.Rebind(`
		SELECT `+coordinatorColumns+` FROM coordinators WHERE id = ? AND workspace_id = ?`),
		id, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get coordinator: %w", err)
	}
	return row.toCoordinator(), nil
}

// ListCoordinators returns every coordinator of a workspace ordered by
// created_at then id, never nil.
func (s *Store) ListCoordinators(ctx context.Context, workspaceID string) ([]*Coordinator, error) {
	var rows []coordinatorRow
	err := s.ro.SelectContext(ctx, &rows, s.ro.Rebind(`
		SELECT `+coordinatorColumns+` FROM coordinators WHERE workspace_id = ? ORDER BY created_at, id`),
		workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list coordinators: %w", err)
	}
	result := make([]*Coordinator, len(rows))
	for i := range rows {
		result[i] = rows[i].toCoordinator()
	}
	return result, nil
}

// DeleteCoordinator deletes the coordinator's proposals and the coordinator
// row in one transaction (coordinators.md#routes). Conversation-task deletion
// is added by a later work order. ErrNotFound when no row matched.
func (s *Store) DeleteCoordinator(ctx context.Context, workspaceID, id string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete coordinator: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, tx.Rebind(`
		DELETE FROM coordinator_proposals WHERE coordinator_id = ? AND workspace_id = ?`),
		id, workspaceID); err != nil {
		return fmt.Errorf("delete coordinator proposals: %w", err)
	}
	res, err := tx.ExecContext(ctx, tx.Rebind(`
		DELETE FROM coordinators WHERE id = ? AND workspace_id = ?`), id, workspaceID)
	if err != nil {
		return fmt.Errorf("delete coordinator: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete coordinator: %w", err)
	}
	return nil
}

func nullableString(v *string) sql.NullString {
	if v == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *v, Valid: true}
}

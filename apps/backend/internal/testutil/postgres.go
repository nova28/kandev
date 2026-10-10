package testutil

import (
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	internaldb "github.com/kandev/kandev/internal/db"
)

// OpenIsolatedPostgres opens dsn with a unique schema on every pooled connection.
// It lets package tests share one Postgres database without racing on
// DROP SCHEMA public when Go runs packages in parallel.
func OpenIsolatedPostgres(t testing.TB, dsn string) *sqlx.DB {
	t.Helper()

	schema := "kandev_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")

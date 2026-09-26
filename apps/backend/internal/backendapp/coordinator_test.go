package backendapp

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"

	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/persistence/requiredstores"
	"github.com/kandev/kandev/internal/startup"
)

func newCoordinatorTestTracker(t *testing.T) *requiredstores.Tracker {
	t.Helper()
	tracker, err := requiredstores.NewTracker([]requiredstores.Descriptor{
		{ID: "coordinator", OwnerPackage: "internal/coordinator", RequiredTables: []string{"coordinators"}, Sweep: startup.StepStoresServices},
	})
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}
	return tracker
}

func newCoordinatorTestPool(t *testing.T) *db.Pool {
	t.Helper()
	conn, err := sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("sqlx.Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return db.NewPool(conn, conn)
}

// TestInitCoordinatorWiring_DisabledBuildsStoreOnly verifies Build decision
// 15: with features.coordinator off, the store is still constructed and
// recorded (it is a requiredstores catalog entry), but no service is built.
func TestInitCoordinatorWiring_DisabledBuildsStoreOnly(t *testing.T) {
	tracker := newCoordinatorTestTracker(t)
	pool := newCoordinatorTestPool(t)

	svc, err := initCoordinatorWiring(context.Background(), pool, tracker, nil, nil, false, newTestLogger())
	if err != nil {
		t.Fatalf("initCoordinatorWiring: %v", err)
	}
	if svc != nil {
		t.Fatal("expected a nil service when features.coordinator is disabled")
	}
	if _, ok := tracker.DescriptorSweep("coordinator"); !ok {
		t.Fatal("expected the coordinator descriptor to be registered")
	}
}

// TestInitCoordinatorWiring_EnabledBuildsService verifies that with
// features.coordinator on, the store, validator and service are all built.
func TestInitCoordinatorWiring_EnabledBuildsService(t *testing.T) {
	tracker := newCoordinatorTestTracker(t)
	pool := newCoordinatorTestPool(t)

	svc, err := initCoordinatorWiring(context.Background(), pool, tracker, nil, nil, true, newTestLogger())
	if err != nil {
		t.Fatalf("initCoordinatorWiring: %v", err)
	}
	if svc == nil {
		t.Fatal("expected a non-nil service when features.coordinator is enabled")
	}
}

// TestInitCoordinatorWiring_StoreErrorPropagates verifies that a store
// construction failure is fatal, even when features.coordinator is disabled.
func TestInitCoordinatorWiring_StoreErrorPropagates(t *testing.T) {
	tracker := newCoordinatorTestTracker(t)
	pool := newCoordinatorTestPool(t)
	if err := pool.Writer().Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	svc, err := initCoordinatorWiring(context.Background(), pool, tracker, nil, nil, false, newTestLogger())
	if err == nil {
		t.Fatal("expected an error when the coordinator store fails to initialize")
	}
	if svc != nil {
		t.Fatal("expected a nil service on store initialization failure")
	}
}

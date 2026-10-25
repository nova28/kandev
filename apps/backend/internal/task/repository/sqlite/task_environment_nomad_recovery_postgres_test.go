package sqlite

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agent/nomad"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
)

// TestPublishRecoveredNomadEnvironmentReady_PostgresRacesCheckpointNomadExecutorInventory
// runs publication and an inventory checkpoint concurrently on one
// environment and session under real PostgreSQL row locks.
func TestPublishRecoveredNomadEnvironmentReady_PostgresRacesCheckpointNomadExecutorInventory(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	repo, err := NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx := newPublishFixtureOn(t, repo)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	checkpoint := nomadExecutorRunningFixture(publishExecutionID, publishGeneration, "ready")
	state, err := nomad.EnvironmentStateFromMetadata(checkpoint.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	state.AllocationID, state.AllocationCreateIndex, state.ServerIdentity = "allocation-publish", 7, "server-publish"
	if err := nomad.PutEnvironmentState(checkpoint.Metadata, state); err != nil {
		t.Fatal(err)
	}
	checkpoint.ExpectedNomadExecutorRevision = fx.revision

	var wg sync.WaitGroup
	var published bool
	var publishErr, checkpointErr error
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		published, publishErr = repo.PublishRecoveredNomadEnvironmentReady(ctx, fx.params())
	}()
	go func() {
		defer wg.Done()
		<-start
		checkpointErr = repo.CheckpointNomadExecutorInventory(ctx, checkpoint)
	}()
	close(start)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("publish and checkpoint did not finish: possible deadlock")
	}

	// A PostgreSQL deadlock abort is a retryable database error, never a
	// sentinel; every other outcome must be one of the defined results.
	if publishErr != nil && !published && !errors.Is(publishErr, models.ErrExecutionRotated) && !errors.Is(publishErr, models.ErrWorkspaceReuseUnsafe) {
		t.Logf("publish returned a database error: %v", publishErr)
	}
	if checkpointErr != nil {
		t.Logf("checkpoint returned: %v", checkpointErr)
	}
	env := fx.environment(t)
	switch {
	case published:
		fx.assertPublished(t)
	case publishErr != nil:
		fx.assertUnpublished(t)
	default:
		t.Fatalf("publish = (false, nil) with environment %+v", env)
	}
	if published && len(env.Repos) != 1 {
		t.Fatalf("repo rows = %d, want 1", len(env.Repos))
	}
	running, err := repo.GetExecutorRunningBySessionID(ctx, publishSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := nomad.EnvironmentStateFromMetadata(running.Metadata); err != nil || got.Phase != "ready" {
		t.Fatalf("final inventory = %+v, %v, want a consistent ready state", got, err)
	}
}

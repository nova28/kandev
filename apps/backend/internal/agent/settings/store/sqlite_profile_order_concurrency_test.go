package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"

	"github.com/kandev/kandev/internal/agent/settings/models"
	"github.com/kandev/kandev/internal/testutil"
)

func TestSQLiteProfileMembershipMutationsSerializeWithReorder(t *testing.T) {
	for _, scenario := range profileOrderMutationScenarios() {
		for _, winner := range []string{"mutation-first", "reorder-first"} {
			t.Run(scenario.name+"/"+winner, func(t *testing.T) {
				repo := openSQLiteProfileOrderRaceRepo(t)
				runProfileOrderMutationRace(t, repo, scenario, winner)
			})
		}
	}
}

func TestSQLiteProfileUpdateRetriesAfterOwnerChangesDuringRead(t *testing.T) {
	runProfileOwnerChangedDuringRead(t, openSQLiteProfileOrderRaceRepo(t))
}

func TestPostgresProfileUpdateRetriesAfterOwnerChangesDuringRead(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	db.SetMaxOpenConns(8)
	repo, err := newSQLiteRepositoryWithDB(db, db, nil)
	if err != nil {
		t.Fatalf("initialize profile order schema: %v", err)
	}
	runProfileOwnerChangedDuringRead(t, repo)
}

func runProfileOwnerChangedDuringRead(t *testing.T, repo *sqliteRepository) {
	t.Helper()
	ctx := context.Background()
	a := &models.Agent{Name: "profile-owner-a"}
	b := &models.Agent{Name: "profile-owner-b"}
	c := &models.Agent{Name: "profile-owner-c"}
	for _, agent := range []*models.Agent{a, b, c} {
		if err := repo.CreateAgent(ctx, agent); err != nil {
			t.Fatal(err)
		}
	}
	profile := &models.AgentProfile{AgentID: a.ID, Name: "owner-race", Model: "model"}
	if err := repo.CreateAgentProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	staleUpdate, err := repo.GetAgentProfile(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	staleUpdate.AgentID = b.ID

	observedA := make(chan struct{})
	releaseOwnerRead := make(chan struct{})
	var paused atomic.Bool
	repo.profileOrderAfterOwnershipRead = func(id, ownerID, _ string) error {
		if id == profile.ID && ownerID == a.ID && paused.CompareAndSwap(false, true) {
			close(observedA)
			<-releaseOwnerRead
		}
		return nil
	}
	t.Cleanup(func() {
		select {
		case <-releaseOwnerRead:
		default:
			close(releaseOwnerRead)
		}
	})
	var mu sync.Mutex
	var lockedAgents []string
	repo.profileOrderAfterLock = func(operation, agentID string) error {
		if operation == "update-profile" {
			mu.Lock()
			lockedAgents = append(lockedAgents, agentID)
			mu.Unlock()
		}
		return nil
	}
	outerDone := make(chan error, 1)
	go func() { outerDone <- repo.UpdateAgentProfile(ctx, staleUpdate) }()
	awaitProfileOrderBarrier(t, observedA)

	concurrentUpdate, err := repo.GetAgentProfile(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	concurrentUpdate.AgentID = c.ID
	if err := repo.UpdateAgentProfile(ctx, concurrentUpdate); err != nil {
		t.Fatalf("concurrent A-to-C owner update: %v", err)
	}
	close(releaseOwnerRead)
	if err := <-outerDone; err != nil {
		t.Fatalf("retried A-to-B owner update: %v", err)
	}
	current, err := repo.GetAgentProfile(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.AgentID != b.ID {
		t.Fatalf("profile owner = %s, want final owner %s", current.AgentID, b.ID)
	}
	mu.Lock()
	gotLocks := append([]string(nil), lockedAgents...)
	mu.Unlock()
	wantLocks := []string{}
	for _, pair := range [][2]string{{a.ID, c.ID}, {a.ID, b.ID}, {b.ID, c.ID}} {
		if pair[0] < pair[1] {
			wantLocks = append(wantLocks, pair[0], pair[1])
		} else {
			wantLocks = append(wantLocks, pair[1], pair[0])
		}
	}
	if len(gotLocks) != len(wantLocks) {
		t.Fatalf("membership locks = %v, want sorted actual-owner lock sets %v", gotLocks, wantLocks)
	}
	for i := range gotLocks {
		if gotLocks[i] != wantLocks[i] {
			t.Fatalf("membership locks = %v, want sorted actual-owner lock sets %v", gotLocks, wantLocks)
		}
	}
}

func TestPostgresProfileMembershipMutationsSerializeWithReorder(t *testing.T) {

//go:build unix

package executor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

var testBarrierIDs = nomadBarrierMarker{ExecutionID: "exec-1", EnvironmentID: "env-1", SessionID: "sess-1"}

func privateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	require.NoError(t, os.Mkdir(dir, 0o700))
	return dir
}

func setBarrierEnv(t *testing.T, fixtureOnly, path string) {
	t.Helper()
	t.Setenv(fixtureOnlyEnv, fixtureOnly)
	t.Setenv(nomadPrePublishBarrierEnv, path)
}

func awaitReturnsImmediately(t *testing.T, b *nomadPrePublishBarrier, execType string) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- b.await(context.Background(), zap.NewNop(), execType, testBarrierIDs) }()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("barrier blocked while it should be inert")
		return nil
	}
}

func TestNomadPrePublishBarrier_Inert(t *testing.T) {
	good := filepath.Join(privateDir(t), "marker")
	open := filepath.Join(t.TempDir(), "open")
	require.NoError(t, os.Mkdir(open, 0o777))
	require.NoError(t, os.Chmod(open, 0o777))
	cases := []struct {
		name, fixtureOnly, path, execType string
		marker                            string
	}{
		{"neither", "", "", "nomad", good},
		{"only path", "", good, "nomad", good},
		{"only opt-in", "1", "", "nomad", good},
		{"opt-in wrong value", "true", good, "nomad", good},
		{"relative path", "1", "marker", "nomad", "marker"},
		{"insecure parent", "1", filepath.Join(open, "marker"), "nomad", filepath.Join(open, "marker")},
		{"missing parent", "1", filepath.Join(good, "x", "marker"), "nomad", ""},
		{"non-nomad runtime", "1", good, "docker", good},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setBarrierEnv(t, tc.fixtureOnly, tc.path)
			b := &nomadPrePublishBarrier{}
			require.NoError(t, awaitReturnsImmediately(t, b, tc.execType))
			if tc.marker != "" {
				_, err := os.Stat(tc.marker)
				require.True(t, os.IsNotExist(err))
			}
			require.False(t, b.fired.Load())
		})
	}
}

func TestNomadPrePublishBarrier_ActiveWritesMarkerBlocksAndFiresOnce(t *testing.T) {
	path := filepath.Join(privateDir(t), "marker")
	setBarrierEnv(t, "1", path)
	b := &nomadPrePublishBarrier{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.await(ctx, zap.NewNop(), "nomad", testBarrierIDs) }()

	require.Eventually(t, func() bool { _, err := os.Stat(path); return err == nil }, 2*time.Second, 5*time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("barrier returned before context cancel: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, `{"execution_id":"exec-1","environment_id":"env-1","session_id":"sess-1"}`+"\n", string(raw))
	var got nomadBarrierMarker
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, testBarrierIDs, got)

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)

	// Second call in the same process is inert.
	require.NoError(t, awaitReturnsImmediately(t, b, "nomad"))
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temp files left behind")
}

// TestPersistTaskEnvironment_UnsetBarrierEnvDoesNotTouchBarrier drives the real
// Nomad publication of an initial materializer with the env unset: publication
// completes, the process-wide barrier never arms, and no marker appears.
func TestPersistTaskEnvironment_UnsetBarrierEnvDoesNotTouchBarrier(t *testing.T) {
	fx := newNomadReadyFixture(t)
	ctx := context.Background()
	env, err := fx.repo.GetTaskEnvironmentByTaskID(ctx, nomadReadyTaskID)
	require.NoError(t, err)
	env.Status = "creating"
	env.MaterializationSessionID = nomadReadySessionID
	require.NoError(t, fx.repo.UpdateTaskEnvironment(ctx, env))
	marker := filepath.Join(privateDir(t), "marker")
	t.Setenv(fixtureOnlyEnv, "")
	t.Setenv(nomadPrePublishBarrierEnv, "")
	saved := processNomadPrePublishBarrier
	processNomadPrePublishBarrier = &nomadPrePublishBarrier{}
	t.Cleanup(func() { processNomadPrePublishBarrier = saved })

	require.NoError(t, fx.persist(t, fx.launchRequest()))
	stored, err := fx.repo.GetTaskEnvironmentByTaskID(ctx, nomadReadyTaskID)
	require.NoError(t, err)
	require.Equal(t, "ready", string(stored.Status))
	require.False(t, processNomadPrePublishBarrier.fired.Load())
	_, statErr := os.Stat(marker)
	require.True(t, os.IsNotExist(statErr))
}

// TestPersistTaskEnvironment_NomadBarrierBlocksBeforePublication drives the real
// publication seam for an initial materializer: the barrier must fire before the
// creating->ready write, and a cancelled context must unblock it with an error
// that leaves the environment unpublished.
func TestPersistTaskEnvironment_NomadBarrierBlocksBeforePublication(t *testing.T) {
	fx := newNomadReadyFixture(t)
	ctx := context.Background()
	env, err := fx.repo.GetTaskEnvironmentByTaskID(ctx, nomadReadyTaskID)
	require.NoError(t, err)
	env.Status = "creating"
	env.MaterializationSessionID = nomadReadySessionID
	require.NoError(t, fx.repo.UpdateTaskEnvironment(ctx, env))

	saved := processNomadPrePublishBarrier
	processNomadPrePublishBarrier = &nomadPrePublishBarrier{}
	t.Cleanup(func() { processNomadPrePublishBarrier = saved })
	marker := filepath.Join(privateDir(t), "marker")
	setBarrierEnv(t, "1", marker)

	exec := newTestExecutor(t, &mockAgentManager{}, fx.repo)
	pctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		existing, _ := fx.repo.GetTaskEnvironmentByTaskID(ctx, nomadReadyTaskID)
		done <- exec.persistTaskEnvironment(pctx, nomadReadyTaskID, fx.session, existing, fx.launchRequest(),
			&LaunchAgentResponse{AgentExecutionID: nomadReadyExecutionID, WorkspacePath: fx.workspacePath},
			executorConfig{ExecutorID: "exec-nomad-fixture"})
	}()
	require.Eventually(t, func() bool { _, err := os.Stat(marker); return err == nil }, 3*time.Second, 5*time.Millisecond)
	raw, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.JSONEq(t, `{"execution_id":"`+nomadReadyExecutionID+`","environment_id":"`+nomadReadyEnvID+`","session_id":"`+nomadReadySessionID+`"}`, string(raw))
	stored, err := fx.repo.GetTaskEnvironmentByTaskID(ctx, nomadReadyTaskID)
	require.NoError(t, err)
	require.Equal(t, "creating", string(stored.Status))

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	stored, err = fx.repo.GetTaskEnvironmentByTaskID(ctx, nomadReadyTaskID)
	require.NoError(t, err)
	require.Equal(t, "creating", string(stored.Status))
}

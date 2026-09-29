package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/agent/executor"
	agentctl "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/common/logger"
)

// staleGenerationExecutor returns instances bound to a standalone agentctl
// generation older than the manager's current one, modelling a CreateInstance
// response that resolved after a relaunch began.
type staleGenerationExecutor struct {
	*createInstanceExecutor
	generation uint64
}

func (e *staleGenerationExecutor) CreateInstance(ctx context.Context, req *ExecutorCreateRequest) (*ExecutorInstance, error) {
	instance, err := e.createInstanceExecutor.CreateInstance(ctx, req)
	if instance != nil {
		instance.Generation = e.generation
	}
	return instance, err
}

// newStaleAddManager builds a manager whose current agentctl generation (2) is
// newer than the generation (1) every created instance is bound to, so the
// execution store refuses the freshly created execution at Add.
func newStaleAddManager(t *testing.T) (*Manager, *createInstanceExecutor, *agentctl.Client) {
	t.Helper()
	log, _ := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "json"})
	client := newReadyAgentctlClient(t, log)
	backend := &createInstanceExecutor{
		MockExecutor: MockExecutor{name: executor.NameStandalone},
		client:       client,
	}
	execRegistry := NewExecutorRegistry(log)
	execRegistry.Register(&staleGenerationExecutor{createInstanceExecutor: backend, generation: 1})

	mgr := NewManager(
		newTestRegistry(), &MockEventBus{}, execRegistry,
		&MockCredentialsManager{}, &MockProfileResolver{}, nil,
		ExecutorFallbackWarn, "", log,
	)
	mgr.dataDir = t.TempDir()
	mgr.workspaceInfoProvider = &mockWorkspaceInfoProvider{}
	cleanupManagerStopCh(t, mgr)
	mgr.BumpAgentctlGenerationForTesting(2)
	return mgr, backend, client
}

// requireStaleAddRolledBack asserts the launch was refused for staleness and
// that everything the aborted launch spawned was released: the runtime
// instance stopped and the agentctl client closed.
func requireStaleAddRolledBack(t *testing.T, err error, backend *createInstanceExecutor, client *agentctl.Client) {
	t.Helper()
	if !errors.Is(err, ErrExecutionGenerationStale) {
		t.Fatalf("err = %v, want ErrExecutionGenerationStale", err)
	}
	if got := backend.stopCount.Load(); got != 1 {
		t.Errorf("StopInstance called %d times, want 1 (runtime instance must be stopped when Add refuses a stale execution)", got)
	}
	if !backend.forceStopped.Load() {
		t.Error("StopInstance force = false, want true")
	}
	_, streamErr := client.StreamWorkspace(context.Background(), agentctl.WorkspaceStreamCallbacks{})
	if streamErr == nil || !strings.Contains(streamErr.Error(), "agentctl client closed") {
		t.Errorf("agentctl client not closed after rollback; StreamWorkspace err = %v", streamErr)
	}
}

func TestLaunch_RollsBackRuntimeWhenAddRefusesStaleGeneration(t *testing.T) {
	mgr, backend, client := newStaleAddManager(t)

	_, err := mgr.Launch(context.Background(), &LaunchRequest{
		TaskID:         "task-1",
		SessionID:      "session-stale",
		AgentProfileID: "profile-1",
		IsEphemeral:    true,
	})

	requireStaleAddRolledBack(t, err, backend, client)
	if _, ok := mgr.executionStore.GetBySessionID("session-stale"); ok {
		t.Fatal("stale execution must not be registered")
	}
}

func TestCreateExecution_RollsBackRuntimeWhenAddRefusesStaleGeneration(t *testing.T) {
	mgr, backend, client := newStaleAddManager(t)

	_, err := mgr.createExecution(context.Background(), "task-1", &WorkspaceInfo{
		SessionID:      "session-stale",
		AgentID:        "auggie",
		AgentProfileID: "profile-1",
		WorkspacePath:  "/workspace/task-1",
	})

	requireStaleAddRolledBack(t, err, backend, client)
	if _, ok := mgr.executionStore.GetBySessionID("session-stale"); ok {
		t.Fatal("stale execution must not be registered")
	}
}

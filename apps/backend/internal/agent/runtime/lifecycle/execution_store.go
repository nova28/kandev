// Package lifecycle manages agent execution lifecycles including tracking,
// state transitions, and cleanup.
package lifecycle

import (
	"errors"
	"fmt"
	"sync"

	v1 "github.com/kandev/kandev/pkg/api/v1"
)

// ErrExecutionNotFound is returned when an execution doesn't exist in the store.
var ErrExecutionNotFound = errors.New("execution not found")

// ErrPromptActivityNotOwned means a cancellation snapshot no longer belongs
// to the execution and prompt that were captured.
var ErrPromptActivityNotOwned = errors.New("prompt activity no longer owned")

// ErrExecutionAlreadyExistsForSession is returned by Add when the session
// already maps to a different execution. The previous behavior was to silently
// overwrite the bySession index, which orphaned the prior execution: the
// agentctl/subprocess kept running but was unreachable via GetBySessionID, so
// nothing ever cleaned it up. Callers must Remove the prior execution before
// adding a replacement.
var ErrExecutionAlreadyExistsForSession = errors.New("execution already exists for session")

// ErrAgentAlreadyRunning is returned by LaunchAgent when an agent execution is
// already tracked for the requested session. The error fires both when the
// execution is live (a concurrent caller raced us) and when it is stale (a
// prior registration whose process never started or exited without cleanup);
// callers must probe IsAgentRunningForSession to decide. Wrapped at producer
// sites with %w so callers can use errors.Is rather than string-match the
// surrounding context.
var ErrAgentAlreadyRunning = errors.New("session already has an agent running")

// ErrAgentReported is returned when the agent subprocess publishes an error
// event during a prompt turn. Distinguished from infrastructure errors
// (network, timeout) so callers can suppress duplicate error-message creation
// — the agent failure path already records the error in the session.
var ErrAgentReported = errors.New("agent error")

// ExecutionStore provides thread-safe storage and retrieval of agent executions.
// It maintains three indexes for efficient lookup by execution ID, session ID, and container ID.
//
// ExecutionStore is the sole enforcement point for stale-generation
// visibility: an execution bound to a standalone agentctl generation older
// than the current one is hidden from every accessor and retired (removed,
// with retireCallback invoked once) the first time any accessor observes it.
// A nil generationSource disables the check entirely, keeping pre-existing
// callers (most test construction) behaviorally unchanged.
type ExecutionStore struct {
	executions  map[string]*AgentExecution
	bySession   map[string]string // sessionID -> executionID
	byContainer map[string]string // containerID -> executionID
	mu          sync.RWMutex

	// generationSource reports the current agentctl generation. A nil value
	// disables stale-generation retirement.
	generationSource func() uint64
	// retireCallback runs, outside the store lock, exactly once per retired
	// execution. May be nil.
	retireCallback func(*AgentExecution)
}

// NewExecutionStore creates a new ExecutionStore with initialized maps.
//
// generationSource, when non-nil, is consulted by every accessor to decide
// whether a standalone-bound execution's generation is stale. retireCallback,
// when non-nil, is invoked exactly once for each execution an accessor
// retires, after the store lock has been released.
func NewExecutionStore(generationSource func() uint64, retireCallback func(*AgentExecution)) *ExecutionStore {
	return &ExecutionStore{
		executions:       make(map[string]*AgentExecution),
		bySession:        make(map[string]string),
		byContainer:      make(map[string]string),
		generationSource: generationSource,
		retireCallback:   retireCallback,
	}
}

// Add adds an agent execution to all tracking maps.
// The execution must have a valid ID. SessionID and ContainerID are optional
// but will be indexed if present.
//
// Returns ErrExecutionAlreadyExistsForSession if the session is already mapped
// to a different execution. Callers must explicitly Remove the prior execution
// before adding a replacement — otherwise the prior subprocess is orphaned.
func (s *ExecutionStore) Add(execution *AgentExecution) error {
	if execution == nil || execution.ID == "" {
		return fmt.Errorf("execution and execution.ID are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if execution.SessionID != "" {
		if existingID, exists := s.bySession[execution.SessionID]; exists && existingID != execution.ID {
			return fmt.Errorf("%w: session=%s existing=%s new=%s",
				ErrExecutionAlreadyExistsForSession,
				execution.SessionID, existingID, execution.ID)
		}
	}

	s.executions[execution.ID] = execution

	if execution.SessionID != "" {
		s.bySession[execution.SessionID] = execution.ID
	}

	if execution.ContainerID != "" {
		s.byContainer[execution.ContainerID] = execution.ID
	}
	agentActiveRuntimes.Set(int64(len(s.executions)))
	return nil
}

// Remove removes an agent execution from all tracking maps.
func (s *ExecutionStore) Remove(executionID string) {
	s.mu.Lock()
	execution, exists := s.executions[executionID]
	if !exists {
		s.mu.Unlock()
		return
	}
	s.deleteLocked(execution)
	s.mu.Unlock()
}

// deleteLocked removes execution from all indexes and updates the active
// runtime gauge. Callers must hold s.mu for writing.
func (s *ExecutionStore) deleteLocked(execution *AgentExecution) {
	if execution.SessionID != "" {
		delete(s.bySession, execution.SessionID)
	}
	if execution.ContainerID != "" {
		delete(s.byContainer, execution.ContainerID)
	}
	execution.clearRuntimeEnvironment()

	delete(s.executions, execution.ID)
	agentActiveRuntimes.Set(int64(len(s.executions)))
}

// currentGeneration returns the live agentctl generation, or 0 when no
// generation source is configured.
func (s *ExecutionStore) currentGeneration() uint64 {
	if s.generationSource == nil {
		return 0
	}
	return s.generationSource()
}

// staleLocked reports whether execution is bound to a standalone generation
// older than current. A zero execution.generation (non-standalone executors)
// is never stale, and a zero current generation (no source configured, or the
// source has not observed a generation yet) disables the check.
func (s *ExecutionStore) staleLocked(execution *AgentExecution, current uint64) bool {
	return s.generationSource != nil && execution.generation != 0 && current != 0 && execution.generation != current
}

// retireStale removes execution if it is still the tracked entry for its ID
// and is still stale, then runs the retire callback outside the store lock.
// The write-lock re-check means concurrent callers racing to retire the same
// snapshot only ever produce one deletion and one callback invocation; a
// caller that loses the race (or finds Remove/replacement already happened)
// is a silent no-op.
func (s *ExecutionStore) retireStale(execution *AgentExecution) {
	current := s.currentGeneration()
	s.mu.Lock()
	stored, exists := s.executions[execution.ID]
	if !exists || stored != execution || !s.staleLocked(stored, current) {
		s.mu.Unlock()
		return
	}
	s.deleteLocked(stored)
	s.mu.Unlock()
	s.runRetireCallback(stored)
}

func (s *ExecutionStore) runRetireCallback(execution *AgentExecution) {
	if s.retireCallback != nil {
		s.retireCallback(execution)
	}
}

// staleGet resolves executionID under a read lock, hiding and retiring a
// stale entry so the caller sees an ordinary not-found result.
func (s *ExecutionStore) staleGet(executionID string) (*AgentExecution, bool) {
	current := s.currentGeneration()
	s.mu.RLock()
	execution, exists := s.executions[executionID]
	if !exists {
		s.mu.RUnlock()
		return nil, false
	}
	if s.staleLocked(execution, current) {
		s.mu.RUnlock()
		s.retireStale(execution)
		return nil, false
	}
	s.mu.RUnlock()
	return execution, true
}

// Get returns an agent execution by its ID.
func (s *ExecutionStore) Get(executionID string) (*AgentExecution, bool) {
	return s.staleGet(executionID)
}

// GetBySessionID returns the agent execution associated with a session ID.
func (s *ExecutionStore) GetBySessionID(sessionID string) (*AgentExecution, bool) {
	s.mu.RLock()
	executionID, exists := s.bySession[sessionID]
	s.mu.RUnlock()
	if !exists {
		return nil, false
	}
	return s.staleGet(executionID)
}

// OwnsPromptGeneration reports whether executionID and generation still own
// sessionID's active lifecycle prompt.
func (s *ExecutionStore) OwnsPromptGeneration(sessionID, executionID string, generation uint64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	currentExecutionID, exists := s.bySession[sessionID]
	if !exists || currentExecutionID != executionID {
		return false
	}
	execution, exists := s.executions[currentExecutionID]
	return exists && generation != 0 && execution.promptGeneration == generation
}

func (s *ExecutionStore) ownsActivePromptGeneration(
	sessionID, executionID string,
	generation uint64,
) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	currentExecutionID, exists := s.bySession[sessionID]
	if !exists || currentExecutionID != executionID {
		return false
	}
	execution, exists := s.executions[currentExecutionID]
	return exists && generation != 0 &&
		execution.promptGeneration == generation &&
		execution.dispatchedPromptGeneration == generation &&
		execution.promptCompletionGeneration != generation
}

// OwnsPromptActivity reports whether the execution still owns the prompt and
// its activity has not changed since the watchdog captured its snapshot.
func (s *ExecutionStore) OwnsPromptActivity(
	sessionID, executionID string,
	generation, activityEpoch uint64,
) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	currentExecutionID, exists := s.bySession[sessionID]
	if !exists || currentExecutionID != executionID {
		return false
	}
	execution, exists := s.executions[currentExecutionID]
	if !exists || generation == 0 || activityEpoch == 0 || execution.promptGeneration != generation {
		return false
	}
	return execution.promptActivityEpochSnapshot() == activityEpoch
}

// ClaimPromptActivity validates a captured prompt identity while the store
// lock is held and returns that exact execution for a lifecycle operation.
// Callers must use the returned execution instead of looking it up by session
// again, because a session can acquire a replacement execution while the
// operation waits for the agent to settle.
func (s *ExecutionStore) ClaimPromptActivity(
	sessionID, executionID string,
	generation, activityEpoch uint64,
) (*AgentExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	currentExecutionID, exists := s.bySession[sessionID]
	if !exists {
		return nil, ErrExecutionNotFound
	}
	execution, exists := s.executions[currentExecutionID]
	if !exists {
		return nil, ErrExecutionNotFound
	}
	if currentExecutionID != executionID || generation == 0 || activityEpoch == 0 || execution.promptGeneration != generation || execution.promptActivityEpochSnapshot() != activityEpoch {
		return nil, ErrPromptActivityNotOwned
	}
	return execution, nil
}

// ActivePromptGeneration returns the generation of the prompt that is dispatched
// and still in flight for executionID, or 0 if there is none. A steer reuses this
// exact generation so the folded completion is attributed to the predecessor's
// still-open waiter rather than discarded as a mismatch. It deliberately returns
// 0 for a generation that is only admitted (not yet dispatched — its buffers are
// mid-reset) or already completed, so a steer never overtakes an about-to-start
// turn or attaches to a finished one; those cases fall back to an ordinary prompt.
func (s *ExecutionStore) ActivePromptGeneration(executionID string) uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	execution, exists := s.executions[executionID]
	if !exists {
		return 0
	}
	gen := execution.promptGeneration
	if gen == 0 || execution.dispatchedPromptGeneration != gen || execution.promptCompletionGeneration == gen {
		return 0
	}
	return gen
}

// ExecutionReference identifies one registered execution without requiring a
// later lookup through the session index. Both IDs are captured from the same
// execution-store snapshot so a caller can keep targeting the original
// execution if a session later acquires a replacement.
type ExecutionReference struct {
	SessionID   string
	ExecutionID string
}

// ListExecutionsForTask returns the session and execution IDs of executions
// registered in-memory under taskID. It is a read-only snapshot, independent
// of any session's persisted database state. Task-scoped stop uses it to
// recover an execution whose session row is already terminal (for example,
// FAILED) but whose prior teardown attempt failed.
func (s *ExecutionStore) ListExecutionsForTask(taskID string) []ExecutionReference {
	if taskID == "" {
		return nil
	}
	current := s.currentGeneration()
	s.mu.RLock()
	var references []ExecutionReference
	var stale []*AgentExecution
	for _, execution := range s.executions {
		if execution.TaskID != taskID {
			continue
		}
		if s.staleLocked(execution, current) {
			stale = append(stale, execution)
			continue
		}
		if execution.SessionID != "" && execution.ID != "" {
			references = append(references, ExecutionReference{
				SessionID:   execution.SessionID,
				ExecutionID: execution.ID,
			})
		}
	}
	s.mu.RUnlock()
	for _, execution := range stale {
		s.retireStale(execution)
	}
	return references
}

// GetByTaskEnvironmentID returns any execution associated with a task environment ID.
func (s *ExecutionStore) GetByTaskEnvironmentID(taskEnvironmentID string) (*AgentExecution, bool) {
	current := s.currentGeneration()
	s.mu.RLock()
	var found *AgentExecution
	var stale []*AgentExecution
	for _, execution := range s.executions {
		if execution.TaskEnvironmentID != taskEnvironmentID {
			continue
		}
		if s.staleLocked(execution, current) {
			stale = append(stale, execution)
			continue
		}
		if found == nil {
			found = execution
		}
	}
	s.mu.RUnlock()
	for _, execution := range stale {
		s.retireStale(execution)
	}
	if found != nil {
		return found, true
	}
	return nil, false
}

// GetByContainerID returns the agent execution associated with a container ID.
func (s *ExecutionStore) GetByContainerID(containerID string) (*AgentExecution, bool) {
	s.mu.RLock()
	executionID, exists := s.byContainer[containerID]
	s.mu.RUnlock()
	if !exists {
		return nil, false
	}
	return s.staleGet(executionID)
}

// List returns all tracked agent executions.
func (s *ExecutionStore) List() []*AgentExecution {
	current := s.currentGeneration()
	s.mu.RLock()
	result := make([]*AgentExecution, 0, len(s.executions))
	var stale []*AgentExecution
	for _, execution := range s.executions {
		if s.staleLocked(execution, current) {
			stale = append(stale, execution)
			continue
		}
		result = append(result, execution)
	}
	s.mu.RUnlock()
	for _, execution := range stale {
		s.retireStale(execution)
	}
	return result
}

// UpdateStatus updates the status of an agent execution.
func (s *ExecutionStore) UpdateStatus(executionID string, status v1.AgentStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if execution, exists := s.executions[executionID]; exists {
		execution.Status = status
	}
}

// BeginPrompt assigns a new immutable identity to the next prompt dispatch.
func (s *ExecutionStore) BeginPrompt(executionID string) (uint64, error) {
	execution, exists := s.Get(executionID)
	if !exists {
		return 0, ErrExecutionNotFound
	}
	execution.promptLifecycleMu.Lock()
	defer execution.promptLifecycleMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists := s.executions[executionID]
	if !exists || current != execution {
		return 0, ErrExecutionNotFound
	}
	return beginExecutionPrompt(current), nil
}

func beginExecutionPrompt(execution *AgentExecution) uint64 {
	// A prompt is about to be dispatched through this object, so a
	// recovered-but-not-yet-adopted generation must not later clobber it with
	// a stale pre-restart completion (see recoveredPromptGenerationPending).
	execution.recoveredPromptGenerationPending.Store(false)
	execution.promptGeneration++
	execution.promptCompletionGeneration = 0
	// The new generation is not dispatched until its triggerPrompt succeeds; a
	// steer must not reuse it until then (its buffers are still being reset).
	execution.dispatchedPromptGeneration = 0
	execution.ProviderError = nil
	execution.Status = v1.AgentStatusRunning
	execution.resetActiveTool()
	return execution.promptGeneration
}

// MarkPromptDispatched records that generation reached agentctl and is in
// flight. Ignored if a newer generation has since begun or this one already
// completed, so a steer can only ever reuse a live dispatched turn.
func (s *ExecutionStore) MarkPromptDispatched(executionID string, generation uint64) {
	if generation == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	execution, exists := s.executions[executionID]
	if !exists {
		return
	}
	if execution.promptGeneration != generation || execution.promptCompletionGeneration == generation {
		return
	}
	execution.dispatchedPromptGeneration = generation
	execution.armPromptActivity()
}

// UpdateError updates the error message of an agent execution and sets its status to failed.
func (s *ExecutionStore) UpdateError(executionID string, errorMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if execution, exists := s.executions[executionID]; exists {
		execution.ErrorMessage = errorMsg
		execution.Status = v1.AgentStatusFailed
	}
}

// WithLock executes a function with the store lock held, providing access to the execution.
// Returns ErrExecutionNotFound if the execution doesn't exist or is stale.
// The function should be fast to avoid blocking other operations.
func (s *ExecutionStore) WithLock(executionID string, fn func(execution *AgentExecution)) error {
	current := s.currentGeneration()
	s.mu.Lock()
	execution, exists := s.executions[executionID]
	if !exists {
		s.mu.Unlock()
		return ErrExecutionNotFound
	}
	if s.staleLocked(execution, current) {
		s.deleteLocked(execution)
		s.mu.Unlock()
		s.runRetireCallback(execution)
		return ErrExecutionNotFound
	}
	fn(execution)
	s.mu.Unlock()
	return nil
}

// WithRLock executes a function with the store read lock held, providing access to the execution.
// Returns ErrExecutionNotFound if the execution doesn't exist or is stale.
func (s *ExecutionStore) WithRLock(executionID string, fn func(execution *AgentExecution)) error {
	current := s.currentGeneration()
	s.mu.RLock()
	execution, exists := s.executions[executionID]
	if !exists {
		s.mu.RUnlock()
		return ErrExecutionNotFound
	}
	if s.staleLocked(execution, current) {
		s.mu.RUnlock()
		s.retireStale(execution)
		return ErrExecutionNotFound
	}
	fn(execution)
	s.mu.RUnlock()
	return nil
}

package lifecycle

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// newGenerationStore builds an ExecutionStore whose current generation is
// read from gen and whose retirements are recorded into retired.
func newGenerationStore(gen *atomic.Uint64, retired *[]string, mu *sync.Mutex) *ExecutionStore {
	return NewExecutionStore(
		gen.Load,
		func(execution *AgentExecution) {
			mu.Lock()
			*retired = append(*retired, execution.ID)
			mu.Unlock()
		},
	)
}

// TestExecutionStore_NilGenerationSourceDisablesRetirement pins the
// documented "nil disables the check" contract: a store built without a
// generation source (as ~all pre-existing call sites are) never treats any
// execution as stale, regardless of its generation field.
func TestExecutionStore_NilGenerationSourceDisablesRetirement(t *testing.T) {
	store := NewExecutionStore(nil, nil)
	exec := &AgentExecution{ID: "exec-1", SessionID: "session-1", generation: 7}
	if err := store.Add(exec); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if _, ok := store.Get("exec-1"); !ok {
		t.Fatal("Get: execution unexpectedly hidden with a nil generation source")
	}
	if got := store.List(); len(got) != 1 {
		t.Fatalf("List: want 1 execution, got %d", len(got))
	}
}

// TestExecutionStore_ZeroGenerationNeverRetired pins that an execution with
// generation 0 (non-standalone executors, or standalone executions created
// before the endpoint was ever wired) is never subject to retirement, even
// once a generation source and current generation exist.
func TestExecutionStore_ZeroGenerationNeverRetired(t *testing.T) {
	var gen atomic.Uint64
	gen.Store(5)
	var retired []string
	var mu sync.Mutex
	store := newGenerationStore(&gen, &retired, &mu)

	exec := &AgentExecution{ID: "exec-1", SessionID: "session-1"} // generation: 0
	if err := store.Add(exec); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if _, ok := store.Get("exec-1"); !ok {
		t.Fatal("Get: zero-generation execution must never be retired")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(retired) != 0 {
		t.Fatalf("retired = %v, want none", retired)
	}
}

// TestExecutionStore_GetHidesAndRetiresStaleExecution covers AC1/AC2's core:
// once the live generation advances past an execution's recorded generation,
// Get must report not-found (so callers fall through to creating a fresh
// instance rather than reconnecting to a dead port) and the retire callback
// must fire exactly once.
func TestExecutionStore_GetHidesAndRetiresStaleExecution(t *testing.T) {
	var gen atomic.Uint64
	gen.Store(1)
	var retired []string
	var mu sync.Mutex
	store := newGenerationStore(&gen, &retired, &mu)

	exec := &AgentExecution{ID: "exec-1", SessionID: "session-1", generation: 1}
	if err := store.Add(exec); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Still current: visible, not retired.
	if _, ok := store.Get("exec-1"); !ok {
		t.Fatal("Get: execution unexpectedly hidden while generation is current")
	}

	gen.Store(2)

	if _, ok := store.Get("exec-1"); ok {
		t.Fatal("Get: stale execution must be hidden")
	}
	if _, ok := store.Get("exec-1"); ok {
		t.Fatal("Get (second call): retired execution must stay hidden")
	}

	mu.Lock()
	defer mu.Unlock()
	if want := []string{"exec-1"}; len(retired) != len(want) || retired[0] != want[0] {
		t.Fatalf("retired = %v, want exactly one retirement of exec-1", retired)
	}
}

// TestExecutionStore_AddRejectsAlreadyStaleExecution pins RV3-4: unlike
// every other accessor, Add used to admit an execution unconditionally, even
// one bound to a standalone generation already superseded by the time the
// caller (e.g. a CreateInstance RPC response that resolved after a relaunch
// began) reaches Add. Add must apply the same staleLocked check as its
// siblings: refuse admission, retire it exactly once, and never let it
// become visible through any accessor.
func TestExecutionStore_AddRejectsAlreadyStaleExecution(t *testing.T) {
	var gen atomic.Uint64
	gen.Store(2)
	var retired []string
	var mu sync.Mutex
	store := newGenerationStore(&gen, &retired, &mu)

	exec := &AgentExecution{ID: "exec-1", SessionID: "session-1", generation: 1}
	err := store.Add(exec)
	if !errors.Is(err, ErrExecutionGenerationStale) {
		t.Fatalf("Add: err = %v, want ErrExecutionGenerationStale", err)
	}

	if _, ok := store.Get("exec-1"); ok {
		t.Fatal("Get: an already-stale execution must never be silently admitted")
	}
	if _, ok := store.GetBySessionID("session-1"); ok {
		t.Fatal("GetBySessionID: an already-stale execution must never be silently admitted")
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("List: want 0 executions, got %d", len(got))
	}

	mu.Lock()
	defer mu.Unlock()
	if want := []string{"exec-1"}; len(retired) != len(want) || retired[0] != want[0] {
		t.Fatalf("retired = %v, want exactly one retirement of exec-1", retired)
	}
}

// TestExecutionStore_GetBySessionIDAndContainerIDHideStale extends the same
// contract to the session and container indexes, since a reconnecting
// caller most commonly looks an execution up by session.
func TestExecutionStore_GetBySessionIDAndContainerIDHideStale(t *testing.T) {
	var gen atomic.Uint64
	gen.Store(1)
	var retired []string
	var mu sync.Mutex
	store := newGenerationStore(&gen, &retired, &mu)

	exec := &AgentExecution{ID: "exec-1", SessionID: "session-1", ContainerID: "container-1", generation: 1}
	if err := store.Add(exec); err != nil {
		t.Fatalf("Add: %v", err)
	}

	gen.Store(2)

	if _, ok := store.GetBySessionID("session-1"); ok {
		t.Fatal("GetBySessionID: stale execution must be hidden")
	}
	if _, ok := store.GetByContainerID("container-1"); ok {
		t.Fatal("GetByContainerID: stale execution must be hidden")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(retired) != 1 || retired[0] != "exec-1" {
		t.Fatalf("retired = %v, want exactly one retirement of exec-1", retired)
	}
}

// TestExecutionStore_GetByTaskEnvironmentIDSkipsStaleAndFindsLive proves a
// stale execution sharing a task environment ID with a live successor is
// retired without shadowing the live one.
func TestExecutionStore_GetByTaskEnvironmentIDSkipsStaleAndFindsLive(t *testing.T) {
	var gen atomic.Uint64
	gen.Store(1)
	var retired []string
	var mu sync.Mutex
	store := newGenerationStore(&gen, &retired, &mu)

	stale := &AgentExecution{ID: "exec-stale", TaskEnvironmentID: "env-1", generation: 1}
	if err := store.Add(stale); err != nil {
		t.Fatalf("Add(stale): %v", err)
	}
	gen.Store(2)
	live := &AgentExecution{ID: "exec-live", TaskEnvironmentID: "env-1", generation: 2}
	if err := store.Add(live); err != nil {
		t.Fatalf("Add(live): %v", err)
	}

	got, ok := store.GetByTaskEnvironmentID("env-1")
	if !ok {
		t.Fatal("GetByTaskEnvironmentID: want the live execution, got not-found")
	}
	if got.ID != "exec-live" {
		t.Fatalf("GetByTaskEnvironmentID: want exec-live, got %s", got.ID)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(retired) != 1 || retired[0] != "exec-stale" {
		t.Fatalf("retired = %v, want exactly one retirement of exec-stale", retired)
	}
}

// TestExecutionStore_ListRetiresStaleEntriesAfterUnlocking covers AC5's List
// requirement: a snapshot excludes stale executions, and their retire
// callbacks run only after the read lock is released -- proven here by
// having the callback itself call back into the store without deadlocking.
func TestExecutionStore_ListRetiresStaleEntriesAfterUnlocking(t *testing.T) {
	var gen atomic.Uint64
	gen.Store(1)
	var retired []string
	var mu sync.Mutex
	var store *ExecutionStore
	store = NewExecutionStore(
		gen.Load,
		func(execution *AgentExecution) {
			mu.Lock()
			retired = append(retired, execution.ID)
			mu.Unlock()
			// Reentering the store from the callback must not deadlock --
			// the callback is documented to run outside the store lock.
			store.List()
		},
	)

	live := &AgentExecution{ID: "exec-live", SessionID: "session-live", generation: 1}
	if err := store.Add(live); err != nil {
		t.Fatalf("Add(live): %v", err)
	}
	staleA := &AgentExecution{ID: "exec-stale-a", SessionID: "session-stale-a", generation: 1}
	if err := store.Add(staleA); err != nil {
		t.Fatalf("Add(staleA): %v", err)
	}
	staleB := &AgentExecution{ID: "exec-stale-b", SessionID: "session-stale-b", generation: 1}
	if err := store.Add(staleB); err != nil {
		t.Fatalf("Add(staleB): %v", err)
	}

	gen.Store(2)
	live.generation = 2

	got := store.List()
	if len(got) != 1 || got[0].ID != "exec-live" {
		t.Fatalf("List() = %v, want only exec-live", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(retired) != 2 {
		t.Fatalf("retired = %v, want exactly two retirements", retired)
	}
}

// TestExecutionStore_ListExecutionsForTaskSkipsAndRetiresStale mirrors the
// List coverage for the task-scoped reference accessor.
func TestExecutionStore_ListExecutionsForTaskSkipsAndRetiresStale(t *testing.T) {
	var gen atomic.Uint64
	gen.Store(1)
	var retired []string
	var mu sync.Mutex
	store := newGenerationStore(&gen, &retired, &mu)

	stale := &AgentExecution{ID: "exec-stale", TaskID: "task-1", SessionID: "session-stale", generation: 1}
	if err := store.Add(stale); err != nil {
		t.Fatalf("Add(stale): %v", err)
	}
	gen.Store(2)
	live := &AgentExecution{ID: "exec-live", TaskID: "task-1", SessionID: "session-live", generation: 2}
	if err := store.Add(live); err != nil {
		t.Fatalf("Add(live): %v", err)
	}

	refs := store.ListExecutionsForTask("task-1")
	if len(refs) != 1 || refs[0].ExecutionID != "exec-live" {
		t.Fatalf("ListExecutionsForTask = %v, want only exec-live", refs)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(retired) != 1 || retired[0] != "exec-stale" {
		t.Fatalf("retired = %v, want exactly one retirement of exec-stale", retired)
	}
}

// TestExecutionStore_WithLockRejectsStaleWithoutCallingFn covers AC5's
// WithLock requirement.
func TestExecutionStore_WithLockRejectsStaleWithoutCallingFn(t *testing.T) {
	var gen atomic.Uint64
	gen.Store(1)
	var retired []string
	var mu sync.Mutex
	store := newGenerationStore(&gen, &retired, &mu)

	exec := &AgentExecution{ID: "exec-1", generation: 1}
	if err := store.Add(exec); err != nil {
		t.Fatalf("Add: %v", err)
	}
	gen.Store(2)

	called := false
	err := store.WithLock("exec-1", func(*AgentExecution) { called = true })
	if err != ErrExecutionNotFound {
		t.Fatalf("WithLock error = %v, want ErrExecutionNotFound", err)
	}
	if called {
		t.Fatal("WithLock: fn must not run for a stale execution")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(retired) != 1 || retired[0] != "exec-1" {
		t.Fatalf("retired = %v, want exactly one retirement of exec-1", retired)
	}
}

// TestExecutionStore_WithRLockRejectsStaleWithoutCallingFn covers AC5's
// WithRLock requirement.
func TestExecutionStore_WithRLockRejectsStaleWithoutCallingFn(t *testing.T) {
	var gen atomic.Uint64
	gen.Store(1)
	var retired []string
	var mu sync.Mutex
	store := newGenerationStore(&gen, &retired, &mu)

	exec := &AgentExecution{ID: "exec-1", generation: 1}
	if err := store.Add(exec); err != nil {
		t.Fatalf("Add: %v", err)
	}
	gen.Store(2)

	called := false
	err := store.WithRLock("exec-1", func(*AgentExecution) { called = true })
	if err != ErrExecutionNotFound {
		t.Fatalf("WithRLock error = %v, want ErrExecutionNotFound", err)
	}
	if called {
		t.Fatal("WithRLock: fn must not run for a stale execution")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(retired) != 1 || retired[0] != "exec-1" {
		t.Fatalf("retired = %v, want exactly one retirement of exec-1", retired)
	}
}

// TestExecutionStore_ConcurrentRetirementRunsCallbackOnce covers AC4: many
// goroutines racing to observe the same stale execution must retire it
// exactly once. Run with -race to also validate no data race on the shared
// counter or the store's own indexes.
func TestExecutionStore_ConcurrentRetirementRunsCallbackOnce(t *testing.T) {
	var gen atomic.Uint64
	gen.Store(1)
	var callbackCount atomic.Int64
	store := NewExecutionStore(
		gen.Load,
		func(*AgentExecution) { callbackCount.Add(1) },
	)

	exec := &AgentExecution{ID: "exec-1", SessionID: "session-1", generation: 1}
	if err := store.Add(exec); err != nil {
		t.Fatalf("Add: %v", err)
	}
	gen.Store(2)

	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			switch i % 3 {
			case 0:
				store.Get("exec-1")
			case 1:
				store.GetBySessionID("session-1")
			case 2:
				store.List()
			}
		}(i)
	}
	wg.Wait()

	if got := callbackCount.Load(); got != 1 {
		t.Fatalf("retire callback ran %d times, want exactly 1", got)
	}
}

// TestExecutionStore_ExplicitRemoveRacesRetirementSafely covers the spec's
// documented race: a concurrent explicit Remove targeting an execution that
// is simultaneously discovered stale by another accessor must not panic,
// must not double-retire, and must leave the execution gone either way.
func TestExecutionStore_ExplicitRemoveRacesRetirementSafely(t *testing.T) {
	var gen atomic.Uint64
	gen.Store(1)
	var callbackCount atomic.Int64
	store := NewExecutionStore(
		gen.Load,
		func(*AgentExecution) { callbackCount.Add(1) },
	)

	exec := &AgentExecution{ID: "exec-1", SessionID: "session-1", generation: 1}
	if err := store.Add(exec); err != nil {
		t.Fatalf("Add: %v", err)
	}
	gen.Store(2)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		store.Get("exec-1")
	}()
	go func() {
		defer wg.Done()
		store.Remove("exec-1")
	}()
	wg.Wait()

	if _, ok := store.Get("exec-1"); ok {
		t.Fatal("execution must be gone after the race")
	}
	// The explicit Remove path never invokes the retire callback -- only the
	// stale-generation discovery path does, and it fires at most once
	// regardless of which goroutine's accessor observed the staleness first.
	if got := callbackCount.Load(); got > 1 {
		t.Fatalf("retire callback ran %d times, want at most 1", got)
	}
}

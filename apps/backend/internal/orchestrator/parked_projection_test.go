package orchestrator

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/task/models"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
)

var errParkedTestProbe = errors.New("simulated probe transport error")

// fakeProbe is a test double for backgroundProbePort. calls is atomic
// because onSessionParkedHook can start the sampler goroutine (via
// runParkingSampler) before returning, and that goroutine calls
// ProbeBackgroundWorkloads concurrently with the test goroutine reading calls.
type fakeProbe struct {
	result string
	err    error
	calls  atomic.Int64
}

func (f *fakeProbe) ProbeBackgroundWorkloads(_ context.Context, _ string) (string, error) {
	f.calls.Add(1)
	return f.result, f.err
}

// gatedProbe is a test double whose SECOND call blocks until the test
// releases it, deliberately ignoring the context it is given. The first call
// is left ungated because onSessionParkedHook always makes one synchronous
// probe call itself (the spec's "synchronous first sample") before a sampler
// goroutine even exists to make a second one — gating call 1 would deadlock
// inside onSessionParkedHook before the test ever reaches the sampler it
// means to observe. The second call is the sampler goroutine's own first
// tick, which is what this test needs blocked: it lets a test put the
// sampler into a deterministic "genuinely still executing, not yet exited"
// state, which a cancel-only (non-draining) implementation cannot be
// distinguished from a real drain without it — cancelling ctx alone does not
// make an in-flight call return, so only a probe that outlives cancellation
// on purpose can prove stopAllParkingSamplers is actually waiting for Done(),
// rather than merely asking for Done().
type gatedProbe struct {
	result  string
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

func (g *gatedProbe) ProbeBackgroundWorkloads(_ context.Context, _ string) (string, error) {
	n := g.calls.Add(1)
	if n == 2 {
		close(g.entered)
		<-g.release
	}
	return g.result, nil
}

// TestComputeParked_AllCombinations verifies the three-term formula for all 8 combinations.
func TestComputeParked_AllCombinations(t *testing.T) {
	tests := []struct {
		observedDetached bool
		lastSample       string
		state            models.TaskSessionState
		wantParked       bool
	}{
		{false, "", models.TaskSessionStateWaitingForInput, false},
		{true, "", models.TaskSessionStateWaitingForInput, false},
		{false, probeResultLive, models.TaskSessionStateWaitingForInput, false},
		{true, probeResultLive, models.TaskSessionStateWaitingForInput, true},
		{false, "", models.TaskSessionStateRunning, false},
		{true, "", models.TaskSessionStateRunning, false},
		{false, probeResultLive, models.TaskSessionStateRunning, false},
		{true, probeResultLive, models.TaskSessionStateRunning, false},
	}
	for _, tt := range tests {
		ps := &sessionParkedState{
			observedDetached: tt.observedDetached,
			lastSample:       tt.lastSample,
		}
		got := ps.computeParked(tt.state)
		if got != tt.wantParked {
			t.Errorf("computeParked(%v, %q, %v) = %v, want %v",
				tt.observedDetached, tt.lastSample, tt.state, got, tt.wantParked)
		}
	}
}

// parkedTestSeedSession creates the workspace/workflow/task/session chain a
// TaskSession row's foreign keys require (via the existing seedSession
// helper) and sets the session to the given state, so
// currentSessionState's fresh read (F1's fix) finds the expected live
// state. Takes the concrete repo (not svc.repo, which is narrowed to
// sessionExecutorStore and does not expose CreateTaskSession).
func parkedTestSeedSession(t *testing.T, repo *sqliterepo.Repository, taskID, sessionID string, state models.TaskSessionState) {
	t.Helper()
	seedSession(t, repo, taskID, sessionID, "")
	if err := repo.UpdateTaskSessionState(context.Background(), sessionID, state, ""); err != nil {
		t.Fatalf("failed to set seeded task session state: %v", err)
	}
}

// gatingEventBus is a bus.EventBus test double whose Publish call blocks
// until the test releases it, deliberately widening the gap between
// onSessionParkedHook releasing parkedStatesMu and whatever runs after it —
// exactly the window the pre-fix parkedSamplerWG.Add(1) placement bug lived
// in (Add(1) ran after both publishParkedChanged and updateTaskParkedState).
// Gating the publish call, rather than relying on goroutine-scheduling luck
// across many iterations, makes the interleaving deterministic instead of
// flaky: the test can prove exactly what stopAllParkingSamplers observes
// while the racing hook is provably still short of its Add(1) call.
type gatingEventBus struct {
	entered chan struct{}
	proceed chan struct{}
	once    sync.Once

	mu     sync.Mutex
	events []recordedEvent
}

func (b *gatingEventBus) Publish(_ context.Context, subject string, event *bus.Event) error {
	b.once.Do(func() {
		close(b.entered)
		<-b.proceed
	})
	b.mu.Lock()
	b.events = append(b.events, recordedEvent{subject: subject, event: event})
	b.mu.Unlock()
	return nil
}
func (b *gatingEventBus) Subscribe(string, bus.EventHandler) (bus.Subscription, error) {
	return nil, nil
}
func (b *gatingEventBus) QueueSubscribe(string, string, bus.EventHandler) (bus.Subscription, error) {
	return nil, nil
}
func (b *gatingEventBus) Request(context.Context, string, *bus.Event, time.Duration) (*bus.Event, error) {
	return nil, nil
}
func (b *gatingEventBus) Close()            {}
func (b *gatingEventBus) IsConnected() bool { return true }

// TestOnSessionParkedHook_LiveProbeSetsParked verifies that onSessionParkedHook
// with a live probe result transitions to parked=true and increments revision.
func TestOnSessionParkedHook_LiveProbeSetsParked(t *testing.T) {
	repo := setupTestRepo(t)
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	// Set a non-zero interval so the sampler goroutine can start without panicking.
	// The goroutine is stopped via deleteParkedState in t.Cleanup.
	svc.config.BackgroundSampleInterval = 10 * time.Millisecond
	probe := &fakeProbe{result: probeResultLive}
	svc.SetBackgroundProbe(probe)

	const taskID, sessionID = "task-hook", "session-hook"
	// Seed observedDetached=true so computeParked can return true.
	svc.getOrCreateParkedState(sessionID).observedDetached = true
	parkedTestSeedSession(t, repo, taskID, sessionID, models.TaskSessionStateWaitingForInput)

	svc.onSessionParkedHook(context.Background(), taskID, sessionID)

	// Stop the sampler goroutine to avoid a goroutine leak detected by
	// goleak. Registered immediately after the resource-creating call and
	// before any fatal assertion, so a failed assertion below still drains
	// the sampler instead of leaving it running and reporting a second,
	// misleading goleak failure.
	t.Cleanup(func() { svc.evictParkedState(context.Background(), taskID, sessionID, true) })

	if got := probe.calls.Load(); got != 1 {
		t.Fatalf("expected 1 probe call, got %d", got)
	}
	ps := svc.parkedStateFor(sessionID)
	if ps == nil {
		t.Fatal("expected parked state entry, got nil")
	}
	if !ps.parked {
		t.Fatal("expected parked=true after live probe with observedDetached=true")
	}
	if ps.revision != 1 {
		t.Fatalf("expected revision=1, got %d", ps.revision)
	}
}

// TestOnSessionParkedHook_SettledProbeNoChange verifies that a settled probe result
// does not set parked=true.
func TestOnSessionParkedHook_SettledProbeNoChange(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	probe := &fakeProbe{result: probeResultSettled}
	svc.SetBackgroundProbe(probe)

	const taskID, sessionID = "task-settled", "session-settled"
	svc.getOrCreateParkedState(sessionID).observedDetached = true

	svc.onSessionParkedHook(context.Background(), taskID, sessionID)

	ps := svc.parkedStateFor(sessionID)
	if ps != nil && ps.parked {
		t.Fatal("expected parked=false with settled probe")
	}
}

// TestClearObservedDetachedOnTurnStarted_ClearsLastSample verifies that the
// turn_started handler clears lastSample as part of the same critical
// section that clears observedDetached and bumps turnMarker.
func TestClearObservedDetachedOnTurnStarted_ClearsLastSample(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())

	const taskID, sessionID = "task-reset", "session-reset"
	ps := svc.getOrCreateParkedState(sessionID)
	ps.lastSample = probeResultLive
	ps.observedDetached = true

	svc.clearObservedDetachedOnTurnStarted(context.Background(), taskID, sessionID)

	if ps.lastSample != "" {
		t.Fatalf("expected empty lastSample after turn_started, got %q", ps.lastSample)
	}
	if ps.observedDetached {
		t.Fatal("expected observedDetached=false after turn_started")
	}
	if ps.turnMarker != 1 {
		t.Fatalf("expected turnMarker=1 after one turn_started, got %d", ps.turnMarker)
	}
}

// TestClearObservedDetachedOnTurnStarted_NoOpForUnknownSession verifies
// AC-41a's final clause: a turn_started for a session with no parkedState
// row is ignored — it creates no entry and increments no marker.
func TestClearObservedDetachedOnTurnStarted_NoOpForUnknownSession(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())

	svc.clearObservedDetachedOnTurnStarted(context.Background(), "task-never-attested", "session-never-attested")

	if svc.parkedStateFor("session-never-attested") != nil {
		t.Fatal("expected no parkedState row created by turn_started for an unattested session")
	}
}

// TestClearObservedDetachedOnTurnStarted_UnparksAndPublishesWhenParked
// verifies a real production gap found in PR review: a turn starting on an
// already-parked session (the self-resume path, D3/§N — a turn_started with
// no accompanying session-state-leave transition) must recompute and publish
// the parked=false transition immediately, not leave the cached ps.parked
// stale until the next sample. observedDetached is a required AND-term of
// computeParked, so clearing it deterministically makes the formula false
// regardless of lastSample or session state — there is nothing to
// re-sample.
func TestClearObservedDetachedOnTurnStarted_UnparksAndPublishesWhenParked(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	recorded := &recordingEventBus{}
	svc.eventBus = recorded
	svc.parkedEpoch = 7

	const taskID, sessionID = "task-turnstart", "session-turnstart"
	ps := svc.getOrCreateParkedState(sessionID)
	ps.observedDetached = true
	ps.parked = true
	ps.lastSample = probeResultLive
	ps.revision = 1
	svc.updateTaskParkedState(context.Background(), taskID, sessionID, true, 1)

	svc.clearObservedDetachedOnTurnStarted(context.Background(), taskID, sessionID)

	ps2 := svc.parkedStateFor(sessionID)
	if ps2.parked {
		t.Fatal("expected parked=false: a new turn starting clears observedDetached, so the formula is false")
	}
	if ps2.revision != 2 {
		t.Fatalf("expected revision=2, got %d", ps2.revision)
	}

	var found bool
	for _, e := range recorded.events {
		if e.subject != events.TaskSessionActivityChanged {
			continue
		}
		data, ok := e.event.Data.(map[string]interface{})
		if !ok || data["session_id"] != sessionID {
			continue
		}
		found = true
		if parked, _ := data["parked_on_background_work"].(bool); parked {
			t.Fatal("expected parked_on_background_work=false on the published frame")
		}
		if rev, _ := data["parked_revision"].(uint64); rev != 2 {
			t.Fatalf("expected parked_revision=2 on the published frame, got %v", data["parked_revision"])
		}
	}
	if !found {
		t.Fatal("expected a session.activity_changed publish carrying the un-park at turn start")
	}

	taskParked, _, _ := svc.TaskParkedProjectionSnapshot(taskID)
	if taskParked {
		t.Fatal("expected task-level parked_on_background_work=false: this was the task's only parked session")
	}
}

// TestClearObservedDetachedOnTurnStarted_NoOpWhenNotParked verifies that a
// turn_started on a session that was never parked publishes nothing and
// moves no revision — the common case, since a plain settle-then-resume
// cycle never touches this at all.
func TestClearObservedDetachedOnTurnStarted_NoOpWhenNotParked(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	recorded := &recordingEventBus{}
	svc.eventBus = recorded

	const taskID, sessionID = "task-turnstart2", "session-turnstart2"
	ps := svc.getOrCreateParkedState(sessionID)
	ps.revision = 3

	svc.clearObservedDetachedOnTurnStarted(context.Background(), taskID, sessionID)

	ps2 := svc.parkedStateFor(sessionID)
	if ps2.revision != 3 {
		t.Fatalf("expected revision unchanged at 3, got %d", ps2.revision)
	}
	for _, e := range recorded.events {
		if e.subject == events.TaskSessionActivityChanged {
			t.Fatal("expected no publish when the session was not parked")
		}
	}
}

// TestOnSessionParkedHook_NoProbeWhenNotAttested verifies AC-40a: a turn
// settling with no attested detached launch issues no probe at all.
func TestOnSessionParkedHook_NoProbeWhenNotAttested(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	probe := &fakeProbe{result: probeResultLive}
	svc.SetBackgroundProbe(probe)

	const taskID, sessionID = "task-noattest", "session-noattest"
	// No row at all — the common case (AC-40a).
	svc.onSessionParkedHook(context.Background(), taskID, sessionID)
	if got := probe.calls.Load(); got != 0 {
		t.Fatalf("expected 0 probe calls for a session with no parkedState row, got %d", got)
	}

	// A row exists but was never attested (observedDetached=false).
	svc.getOrCreateParkedState(sessionID)
	svc.onSessionParkedHook(context.Background(), taskID, sessionID)
	if got := probe.calls.Load(); got != 0 {
		t.Fatalf("expected 0 probe calls for an unattested session, got %d", got)
	}
}

// turnBoundaryDuringProbe is a test double for backgroundProbePort that
// simulates a turn_started arriving while a probe is in flight, by clearing
// observed_detached (and bumping turnMarker) from inside the probe call
// itself before returning a result.
type turnBoundaryDuringProbe struct {
	svc       *Service
	taskID    string
	sessionID string
	result    string
	calls     int
}

func (t *turnBoundaryDuringProbe) ProbeBackgroundWorkloads(_ context.Context, _ string) (string, error) {
	t.calls++
	t.svc.clearObservedDetachedOnTurnStarted(context.Background(), t.taskID, t.sessionID)
	return t.result, nil
}

// TestOnSessionParkedHook_DiscardsSampleWhenTurnBoundaryMovesDuringProbe
// verifies D2's revalidation rule: a sample completing after a turn boundary
// moved mid-flight is discarded — not recorded as lastSample, not published,
// no revision moves — rather than applied to the wrong turn.
func TestOnSessionParkedHook_DiscardsSampleWhenTurnBoundaryMovesDuringProbe(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-stale", "session-stale"
	svc.getOrCreateParkedState(sessionID).observedDetached = true

	probe := &turnBoundaryDuringProbe{svc: svc, taskID: taskID, sessionID: sessionID, result: probeResultLive}
	svc.SetBackgroundProbe(probe)

	svc.onSessionParkedHook(context.Background(), taskID, sessionID)

	if probe.calls != 1 {
		t.Fatalf("expected 1 probe call, got %d", probe.calls)
	}
	ps := svc.parkedStateFor(sessionID)
	if ps.parked {
		t.Fatal("expected parked=false: the stale sample must be discarded, not applied")
	}
	if ps.revision != 0 {
		t.Fatalf("expected revision=0 (discard writes nothing), got %d", ps.revision)
	}
	if ps.lastSample != "" {
		t.Fatalf("expected lastSample unset (discard writes nothing), got %q", ps.lastSample)
	}
}

// TestSampleAndPublishParked_DiscardsStaleSample is the periodic-sampler
// counterpart of the settle-hook revalidation test above.
func TestSampleAndPublishParked_DiscardsStaleSample(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-stale2", "session-stale2"
	ps := svc.getOrCreateParkedState(sessionID)
	ps.observedDetached = true
	ps.parked = true

	probe := &turnBoundaryDuringProbe{svc: svc, taskID: taskID, sessionID: sessionID, result: probeResultLive}
	svc.SetBackgroundProbe(probe)

	keepRunning := svc.sampleAndPublishParked(context.Background(), taskID, sessionID)
	if !keepRunning {
		t.Fatal("expected keepRunning=true: a discarded sample must not stop the sampler on its own")
	}
	ps2 := svc.parkedStateFor(sessionID)
	// clearObservedDetachedOnTurnStarted already un-parked and published as
	// part of its own turn-start transition (ps.parked started true); the
	// point of this test is that the DISCARDED stale "live" sample from the
	// old turn never overwrites lastSample on top of that.
	if ps2.lastSample != "" {
		t.Fatalf("expected lastSample untouched by the discarded sample, got %q", ps2.lastSample)
	}
	if ps2.parked {
		t.Fatal("expected parked=false: clearObservedDetachedOnTurnStarted already un-parked it")
	}
}

// TestOnSessionParkedHook_DoesNotParkWhenSessionLeftWaitingDuringProbe is the
// regression guard for Review round 2's F1: the settle hook must not compute
// `parked` against a state literal frozen at hook-entry. The turn boundary
// does not move here (turnMarker/observedDetached are unchanged, so D2's
// revalidation alone would let this sample through) — only the session's own
// state changed while the probe was in flight. The fresh read inside the
// write's critical section must catch it.
func TestOnSessionParkedHook_DoesNotParkWhenSessionLeftWaitingDuringProbe(t *testing.T) {
	repo := setupTestRepo(t)
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-left-waiting", "session-left-waiting"
	svc.getOrCreateParkedState(sessionID).observedDetached = true
	// The session has already left WAITING_FOR_INPUT by the time the probe
	// (below) returns — simulating an admitted prompt racing the probe's own
	// round trip.
	parkedTestSeedSession(t, repo, taskID, sessionID, models.TaskSessionStateRunning)

	probe := &fakeProbe{result: probeResultLive}
	svc.SetBackgroundProbe(probe)

	svc.onSessionParkedHook(context.Background(), taskID, sessionID)

	ps := svc.parkedStateFor(sessionID)
	if ps.parked {
		t.Fatal("expected parked=false: the session left WAITING_FOR_INPUT before the write, so a live sample must not park it")
	}
}

// TestSampleAndPublishParked_DoesNotReparkWhenSessionLeftWaitingDuringProbe
// is the periodic-sampler counterpart: a session already parked, whose
// in-memory row has not yet been touched by unparkOnStateLeave, must not be
// re-affirmed as parked by a probe sample that completes after the session's
// state has already moved on in the repository.
func TestSampleAndPublishParked_DoesNotReparkWhenSessionLeftWaitingDuringProbe(t *testing.T) {
	repo := setupTestRepo(t)
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-left-waiting2", "session-left-waiting2"
	ps := svc.getOrCreateParkedState(sessionID)
	ps.observedDetached = true
	ps.parked = true
	// unparkOnStateLeave has not run yet in-memory, but the session's real
	// state has already moved on.
	parkedTestSeedSession(t, repo, taskID, sessionID, models.TaskSessionStateRunning)

	probe := &fakeProbe{result: probeResultLive}
	svc.SetBackgroundProbe(probe)

	keepRunning := svc.sampleAndPublishParked(context.Background(), taskID, sessionID)
	if keepRunning {
		t.Fatal("expected keepRunning=false: the session is no longer WAITING_FOR_INPUT")
	}
	ps2 := svc.parkedStateFor(sessionID)
	if ps2.parked {
		t.Fatal("expected parked=false: a live sample must not re-park a session that already left WAITING_FOR_INPUT")
	}
}

// evictAndReviveDuringProbe is a test double for backgroundProbePort that
// simulates Review round 2's F2: the session's parkedState row is evicted
// (execution end) and then revived by a fresh attestation for a NEW
// execution, all while the original probe call is in flight. turnMarker
// restarts at 0 on both eviction and revival per spec, so it alone cannot
// tell the two executions apart — this exercises the anti-ABA generation
// guard instead.
type evictAndReviveDuringProbe struct {
	svc       *Service
	taskID    string
	sessionID string
	result    string
	calls     int
}

func (p *evictAndReviveDuringProbe) ProbeBackgroundWorkloads(_ context.Context, _ string) (string, error) {
	p.calls++
	p.svc.evictParkedState(context.Background(), p.taskID, p.sessionID, false)
	p.svc.markObservedDetached(p.sessionID)
	return p.result, nil
}

// TestSampleAndPublishParked_DiscardsSampleAcrossEvictionAndRevival is the
// regression guard for F2: a stale sample from execution A must not be
// applied to execution B's freshly revived row just because
// (observedDetached, turnMarker) happen to read the same values again.
func TestSampleAndPublishParked_DiscardsSampleAcrossEvictionAndRevival(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-aba", "session-aba"
	ps := svc.getOrCreateParkedState(sessionID)
	ps.observedDetached = true
	ps.parked = true

	probe := &evictAndReviveDuringProbe{svc: svc, taskID: taskID, sessionID: sessionID, result: probeResultLive}
	svc.SetBackgroundProbe(probe)

	keepRunning := svc.sampleAndPublishParked(context.Background(), taskID, sessionID)
	if !keepRunning {
		t.Fatal("expected keepRunning=true: a discarded sample must not stop the sampler on its own")
	}

	ps2 := svc.parkedStateFor(sessionID)
	if ps2.lastSample != "" {
		t.Fatalf("expected lastSample untouched by execution A's stale sample, got %q", ps2.lastSample)
	}
	if ps2.generation != 1 {
		t.Fatalf("expected generation=1 after one eviction, got %d", ps2.generation)
	}
}

// TestRunProbe_ErrorResolvesToUnknown verifies AC-46: a non-nil error
// resolves to "unknown" regardless of the value returned beside it.
func TestRunProbe_ErrorResolvesToUnknown(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	svc.SetBackgroundProbe(&fakeProbe{result: probeResultLive, err: errParkedTestProbe})

	got := svc.runProbe(context.Background(), "session-err")
	if got != "unknown" {
		t.Fatalf("expected unknown on probe error, got %q", got)
	}
}

// TestRunProbe_UnexpectedValueResolvesToUnknown verifies AC-46: a result
// outside the three literals is "unknown" even with a nil error.
func TestRunProbe_UnexpectedValueResolvesToUnknown(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	svc.SetBackgroundProbe(&fakeProbe{result: "bogus"})

	got := svc.runProbe(context.Background(), "session-bogus")
	if got != "unknown" {
		t.Fatalf("expected unknown for an out-of-domain result, got %q", got)
	}
}

// panicProbe is a test double whose ProbeBackgroundWorkloads panics.
type panicProbe struct{}

func (panicProbe) ProbeBackgroundWorkloads(context.Context, string) (string, error) {
	panic("simulated probe implementation panic")
}

// TestRunProbe_PanicResolvesToUnknown verifies AC-46's panic clause: a
// panicking port implementation resolves to "unknown" rather than escaping
// the settle path.
func TestRunProbe_PanicResolvesToUnknown(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	svc.SetBackgroundProbe(panicProbe{})

	got := svc.runProbe(context.Background(), "session-panic")
	if got != "unknown" {
		t.Fatalf("expected unknown when the probe panics, got %q", got)
	}
}

// TestUnparkOnStateLeave_PublishesWhenParked verifies AC-53/AC-68: leaving
// WAITING_FOR_INPUT un-parks immediately without taking a further sample.
func TestUnparkOnStateLeave_PublishesWhenParked(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-leave", "session-leave"
	ps := svc.getOrCreateParkedState(sessionID)
	ps.observedDetached = true
	ps.parked = true
	ps.lastSample = probeResultLive
	ps.revision = 1

	svc.unparkOnStateLeave(context.Background(), taskID, sessionID)

	ps2 := svc.parkedStateFor(sessionID)
	if ps2.parked {
		t.Fatal("expected parked=false after the session left WAITING_FOR_INPUT")
	}
	if ps2.revision != 2 {
		t.Fatalf("expected revision=2, got %d", ps2.revision)
	}
}

// TestUnparkOnStateLeave_NoOpWhenNotParked verifies that leaving
// WAITING_FOR_INPUT while not parked publishes nothing and moves no revision.
func TestUnparkOnStateLeave_NoOpWhenNotParked(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-leave2", "session-leave2"
	ps := svc.getOrCreateParkedState(sessionID)
	ps.revision = 3

	svc.unparkOnStateLeave(context.Background(), taskID, sessionID)

	ps2 := svc.parkedStateFor(sessionID)
	if ps2.revision != 3 {
		t.Fatalf("expected revision unchanged at 3, got %d", ps2.revision)
	}
}

// TestEvictParkedState_UnparksThenReduces verifies AC-85: an eviction of a
// parked row (remove=false — execution end / context reset) un-parks first
// with a strictly higher revision, then reduces the row in place rather than
// deleting it, retaining the revision.
func TestEvictParkedState_UnparksThenReduces(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-evict1", "session-evict1"
	ps := svc.getOrCreateParkedState(sessionID)
	ps.observedDetached = true
	ps.parked = true
	ps.lastSample = probeResultLive
	ps.turnMarker = 7
	ps.revision = 3

	svc.evictParkedState(context.Background(), taskID, sessionID, false)

	ps2 := svc.parkedStateFor(sessionID)
	if ps2 == nil {
		t.Fatal("expected the row to be reduced, not removed, when remove=false")
	}
	if ps2.parked {
		t.Fatal("expected parked=false after eviction")
	}
	if ps2.revision != 4 {
		t.Fatalf("expected revision retained and bumped to 4, got %d", ps2.revision)
	}
	if ps2.observedDetached || ps2.turnMarker != 0 || ps2.lastSample != "" {
		t.Fatalf("expected a reduced row (observedDetached=false, turnMarker=0, lastSample=\"\"), got %+v", ps2)
	}
	if ps2.generation != 1 {
		t.Fatalf("expected generation bumped to 1 by eviction (F2's anti-ABA guard), got %d", ps2.generation)
	}
}

// TestEvictParkedState_RemovesRowWhenRemoveTrue verifies AC-85's "dropped for
// good when the session is deleted" clause.
func TestEvictParkedState_RemovesRowWhenRemoveTrue(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	const taskID, sessionID = "task-evict2", "session-evict2"
	ps := svc.getOrCreateParkedState(sessionID)
	ps.parked = true

	svc.evictParkedState(context.Background(), taskID, sessionID, true)

	if svc.parkedStateFor(sessionID) != nil {
		t.Fatal("expected the row to be removed outright when remove=true")
	}
}

// TestEvictParkedState_NoOpWhenAbsent verifies eviction of an unknown session
// does not panic and creates no row.
func TestEvictParkedState_NoOpWhenAbsent(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	svc.evictParkedState(context.Background(), "task-x", "session-never-existed", false)
	if svc.parkedStateFor("session-never-existed") != nil {
		t.Fatal("expected no row to be created by evicting an unknown session")
	}
}

// TestParkedProjectionSnapshot_ReturnsCorrectValues verifies the
// ParkedProjectionSnapshot method.
func TestParkedProjectionSnapshot_ReturnsCorrectValues(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())

	const sessionID = "session-snap"
	ps := svc.getOrCreateParkedState(sessionID)
	ps.parked = true
	ps.revision = 5

	parked, epoch, revision := svc.ParkedProjectionSnapshot(sessionID)
	if !parked {
		t.Fatal("expected parked=true")
	}
	if epoch != svc.parkedEpoch {
		t.Fatalf("expected epoch=%d, got %d", svc.parkedEpoch, epoch)
	}
	if revision != 5 {
		t.Fatalf("expected revision=5, got %d", revision)
	}
}

// TestParkedProjectionSnapshot_UnknownSession verifies that an unknown session
// returns false with revision=0 and the process epoch.
func TestParkedProjectionSnapshot_UnknownSession(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())

	parked, epoch, revision := svc.ParkedProjectionSnapshot("unknown-session")
	if parked {
		t.Fatal("expected parked=false for unknown session")
	}
	if epoch != svc.parkedEpoch {
		t.Fatalf("expected epoch=%d, got %d", svc.parkedEpoch, epoch)
	}
	if revision != 0 {
		t.Fatalf("expected revision=0, got %d", revision)
	}
}

// TestTaskParkedProjectionSnapshot_MultiSessionOR verifies that the task-level
// parked state is the OR of all session parked states.
func TestTaskParkedProjectionSnapshot_MultiSessionOR(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())

	const taskID = "task-or"

	// session A is not parked, session B is parked.
	svc.updateTaskParkedState(context.Background(), taskID, "sess-a", false, 1)
	svc.updateTaskParkedState(context.Background(), taskID, "sess-b", true, 1)

	parked, _, revision := svc.TaskParkedProjectionSnapshot(taskID)
	if !parked {
		t.Fatal("expected task parked=true when any session is parked")
	}
	if revision == 0 {
		t.Fatal("expected non-zero revision after OR flip")
	}

	// Now session B clears — both sessions not parked → OR = false. A higher
	// sessionRevision than sess-b's first call (1), reflecting a later
	// session-level transition.
	svc.updateTaskParkedState(context.Background(), taskID, "sess-b", false, 2)

	parked, _, revision2 := svc.TaskParkedProjectionSnapshot(taskID)
	if parked {
		t.Fatal("expected task parked=false when all sessions not parked")
	}
	if revision2 <= revision {
		t.Fatal("expected revision to increment after second OR flip")
	}
}

// TestUpdateTaskParkedState_Concurrent is AC-49a's regression guard: two of a
// task's sessions transitioning into parked GENUINELY concurrently, not the
// sequential calls TestTaskParkedProjectionSnapshot_MultiSessionOR makes.
// Both sessions flip their own member entry from unset to true, so both
// individually make anyParkedMember(members) true — but the task-level OR
// itself only flips once, from false to true, and only the goroutine whose
// critical section happens to run first sees changed=true and increments
// parked_revision. A regression that races the members write, the OR
// recompute, and the parked/revision update outside one critical section
// (spec: "boolean and the counter are read for serialization in ONE critical
// section") could double-increment the revision, or -race could catch an
// unsynchronized concurrent map write — this test runs many iterations under
// -race specifically to make either failure mode reproducible rather than
// relying on scheduler luck for one shot.
func TestUpdateTaskParkedState_Concurrent(t *testing.T) {
	for i := 0; i < 50; i++ {
		svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
		taskID := "task-race"

		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			svc.updateTaskParkedState(context.Background(), taskID, "sess-a", true, 1)
		}()
		go func() {
			defer wg.Done()
			<-start
			svc.updateTaskParkedState(context.Background(), taskID, "sess-b", true, 1)
		}()
		close(start)
		wg.Wait()

		parked, _, revision := svc.TaskParkedProjectionSnapshot(taskID)
		if !parked {
			t.Fatalf("iteration %d: expected task parked=true once either session parks", i)
		}
		if revision != 1 {
			t.Fatalf("iteration %d: expected exactly one revision increment for the single false->true OR flip, got %d", i, revision)
		}
	}
}

// TestSampleAndPublishParked_StopsWhenNotParked verifies AC-62's live→settled
// transition: sampleAndPublishParked returns false (stop sampling), flips
// ps.parked, bumps the revision, publishes the un-park on
// session.activity_changed (Review round 2, F3 — the spec's named carrier,
// not a dedicated event) carrying the un-parked triple, and flips the
// task-level OR because this is the task's only parked session.
func TestSampleAndPublishParked_StopsWhenNotParked(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	recorded := &recordingEventBus{}
	svc.eventBus = recorded
	svc.parkedEpoch = 42

	probe := &fakeProbe{result: probeResultSettled}
	svc.SetBackgroundProbe(probe)

	const taskID, sessionID = "task-sample", "session-sample"
	ps := svc.getOrCreateParkedState(sessionID)
	ps.observedDetached = true
	ps.parked = true // pretend was parked
	ps.revision = 1
	// Register this session as the task's only parked member, mirroring what
	// onSessionParkedHook would already have done when it first parked.
	svc.updateTaskParkedState(context.Background(), taskID, sessionID, true, 1)

	keepRunning := svc.sampleAndPublishParked(context.Background(), taskID, sessionID)
	if keepRunning {
		t.Fatal("expected keepRunning=false when probe returns settled")
	}

	ps2 := svc.parkedStateFor(sessionID)
	if ps2.parked {
		t.Fatal("expected parked=false after a settled probe")
	}
	if ps2.revision != 2 {
		t.Fatalf("expected revision incremented to 2, got %d", ps2.revision)
	}

	var found bool
	for _, e := range recorded.events {
		if e.subject != events.TaskSessionActivityChanged {
			continue
		}
		data, ok := e.event.Data.(map[string]interface{})
		if !ok || data["session_id"] != sessionID {
			continue
		}
		found = true
		if parked, _ := data["parked_on_background_work"].(bool); parked {
			t.Fatal("expected parked_on_background_work=false on the published frame")
		}
		if rev, _ := data["parked_revision"].(uint64); rev != 2 {
			t.Fatalf("expected parked_revision=2 on the published frame, got %v", data["parked_revision"])
		}
		if epoch, _ := data["parked_epoch"].(int64); epoch != 42 {
			t.Fatalf("expected parked_epoch=42 on the published frame, got %v", data["parked_epoch"])
		}
	}
	if !found {
		t.Fatal("expected a session.activity_changed publish carrying the un-park for this session")
	}

	taskParked, _, _ := svc.TaskParkedProjectionSnapshot(taskID)
	if taskParked {
		t.Fatal("expected task-level parked_on_background_work=false: this was the task's only parked session")
	}
}

// TestRunParkingSampler_ContextCancellation verifies that the sampler exits
// promptly when its context is cancelled.
func TestRunParkingSampler_ContextCancellation(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())
	svc.config.BackgroundSampleInterval = 100 * time.Millisecond
	probe := &fakeProbe{result: probeResultLive}
	svc.SetBackgroundProbe(probe)

	const taskID, sessionID = "task-ctx", "session-ctx"
	ps := svc.getOrCreateParkedState(sessionID)
	ps.observedDetached = true
	ps.parked = true
	ps.lastSample = probeResultLive

	ctx, cancel := context.WithCancel(context.Background())
	// Mirrors the real spawn site (onSessionParkedHook): Add(1) before
	// spawning, since runParkingSampler's own deferred Done() assumes it.
	svc.parkedSamplerWG.Add(1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.runParkingSampler(ctx, taskID, sessionID)
	}()

	cancel()
	select {
	case <-done:
		// sampler exited cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("sampler goroutine did not exit after context cancellation")
	}
}

// TestStopAllParkingSamplers_DrainsRunningSampler verifies a real production
// gap found in PR review: Service.Stop() never visited s.parkedStates, so a
// running parking-sampler goroutine (spawned with a context rooted in
// context.Background(), never the service's own lifecycle context) outlived
// shutdown. stopAllParkingSamplers is the single owner Stop() now calls:
// cancel every session's sampler and block until each has actually exited,
// mirroring the established stopSendNowWorkers pattern in this same package.
//
// This uses gatedProbe rather than fakeProbe specifically to prove BLOCKING,
// not just cancellation. A cancel-only implementation (cancel the context and
// return immediately, with no WaitGroup drain at all) is indistinguishable
// from a real drain if the sampler goroutine happens to exit fast — which is
// exactly what a fakeProbe-based version of this test could not tell apart,
// and did not: deleting stopAllParkingSamplers's entire wg.Wait() half left
// this test (and the whole package) green (Review round 5 finding, proved by
// mutation). gatedProbe keeps the sampler goroutine demonstrably still
// executing (blocked inside the probe call, ignoring ctx cancellation) at the
// moment stopAllParkingSamplers is invoked, so the assertion that it does NOT
// return while the goroutine is still alive has real teeth.
func TestStopAllParkingSamplers_DrainsRunningSampler(t *testing.T) {
	repo := setupTestRepo(t)
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	svc.config.BackgroundSampleInterval = 5 * time.Millisecond
	probe := &gatedProbe{result: probeResultLive, entered: make(chan struct{}), release: make(chan struct{})}
	svc.SetBackgroundProbe(probe)

	const taskID, sessionID = "task-drain", "session-drain"
	svc.getOrCreateParkedState(sessionID).observedDetached = true
	parkedTestSeedSession(t, repo, taskID, sessionID, models.TaskSessionStateWaitingForInput)

	svc.onSessionParkedHook(context.Background(), taskID, sessionID)
	ps := svc.parkedStateFor(sessionID)
	if ps == nil || !ps.parked {
		t.Fatal("expected the session to be parked with a sampler goroutine running")
	}

	// Wait for the sampler to be genuinely mid-probe-call, not merely ticking.
	select {
	case <-probe.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("sampler never entered the probe call before the test deadline")
	}

	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		svc.stopAllParkingSamplers()
	}()

	// stopAllParkingSamplers must NOT return while the sampler goroutine is
	// still blocked inside the probe call: cancellation alone does not make
	// an in-flight call return, so the only way stopAllParkingSamplers can
	// legitimately return here is if it never actually waited for Done().
	select {
	case <-stopDone:
		t.Fatal("stopAllParkingSamplers returned before the running sampler goroutine actually exited")
	case <-time.After(100 * time.Millisecond):
		// still blocked, as required
	}

	// Release the gate: the probe call returns, the sampler loops back to its
	// select, observes ctx.Done() (already cancelled by the sweep above), and
	// exits — which is what stopAllParkingSamplers's Wait() is blocked on.
	close(probe.release)

	select {
	case <-stopDone:
		// drained: stopAllParkingSamplers only returned once Done() fired
	case <-time.After(2 * time.Second):
		t.Fatal("stopAllParkingSamplers did not return after the sampler goroutine exited")
	}

	// The sampler must have actually exited, not merely been asked to: no
	// further probe calls after stopAllParkingSamplers returns.
	callsAtStop := probe.calls.Load()
	time.Sleep(20 * time.Millisecond)
	if got := probe.calls.Load(); got != callsAtStop {
		t.Fatalf("expected no further probe calls after drain, got %d more", got-callsAtStop)
	}
}

// TestOnSessionParkedHook_ConcurrentWithStop deterministically reproduces the
// interleaving 7b4259e70's own commit message named but never actually
// exercised concurrently: a session settling into "newly parked" AT THE SAME
// TIME Service.Stop() is draining. Rather than relying on goroutine-
// scheduling luck across many iterations (flaky, and in practice did not
// reproduce the bug reliably even across 50 iterations, since the vulnerable
// window is a handful of instructions wide), gatingEventBus pins
// onSessionParkedHook exactly inside that window — released parkedStatesMu,
// not yet past its publish step — so the test can assert precisely what
// stopAllParkingSamplers observes while it is provably still there.
//
// Before the Review-round-5 fix, parkedSamplerWG.Add(1) ran AFTER both
// publishParkedChanged and updateTaskParkedState, i.e. strictly after the
// point this test pins the hook at. So on pre-fix code,
// stopAllParkingSamplers's Wait() sees a WaitGroup counter of zero (the
// racing Add() has not happened yet) and returns immediately — proving Stop()
// can complete having drained nothing for the in-flight session, exactly the
// defect this test guards. On fixed code, Add(1) already ran inside the same
// critical section that unlocked parkedStatesMu, so the counter is already 1
// and stopAllParkingSamplers's Wait() genuinely blocks until the eventual
// sampler goroutine (spawned with an already-cancelled context, since the
// cancel sweep already found ps.samplerCancel set) exits and calls Done().
func TestOnSessionParkedHook_ConcurrentWithStop(t *testing.T) {
	repo := setupTestRepo(t)
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	svc.config.BackgroundSampleInterval = time.Millisecond
	probe := &fakeProbe{result: probeResultLive}
	svc.SetBackgroundProbe(probe)

	geb := &gatingEventBus{entered: make(chan struct{}), proceed: make(chan struct{})}
	svc.eventBus = geb

	const taskID, sessionID = "task-race", "session-race"
	svc.getOrCreateParkedState(sessionID).observedDetached = true
	parkedTestSeedSession(t, repo, taskID, sessionID, models.TaskSessionStateWaitingForInput)

	hookDone := make(chan struct{})
	go func() {
		defer close(hookDone)
		svc.onSessionParkedHook(context.Background(), taskID, sessionID)
	}()

	// Wait until onSessionParkedHook has passed its locked decision (parked
	// flipped true, the sampler's context stored on the row) and reached the
	// publish step — the exact window between unlocking parkedStatesMu and
	// (pre-fix) calling parkedSamplerWG.Add(1) that this test targets.
	select {
	case <-geb.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("onSessionParkedHook never reached the publish step")
	}

	// stopAllParkingSamplers races the still-in-flight hook here — the
	// interleaving 7b4259e70's commit message named but never tested.
	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		svc.stopAllParkingSamplers()
	}()

	select {
	case <-stopDone:
		t.Fatal("stopAllParkingSamplers returned before the racing sampler was ever counted — Add(1) is not ordered before Wait(), so Stop() drained nothing for this session")
	case <-time.After(100 * time.Millisecond):
		// correctly still blocked: the WaitGroup already reflects the racing
		// Add() and stopAllParkingSamplers is genuinely waiting on it
	}

	close(geb.proceed)

	select {
	case <-stopDone:
		// drained: stopAllParkingSamplers only returned once the racing
		// sampler was accounted for and had actually exited
	case <-time.After(2 * time.Second):
		t.Fatal("stopAllParkingSamplers did not return after the racing hook completed")
	}
	<-hookDone

	// No sampler goroutine may have survived: only the synchronous first
	// sample (already made before this window opened) should have called the
	// probe. Give a wrongly-spawned sampler a moment to tick, then confirm it
	// never did.
	time.Sleep(15 * time.Millisecond)
	if got := probe.calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 probe call (the synchronous first sample only), got %d — a sampler survived shutdown", got)
	}
}

// TestStopAllParkingSamplers_NoOpWhenNoneRunning verifies draining an empty
// (or all-idle) parkedStates map returns immediately rather than blocking.
func TestStopAllParkingSamplers_NoOpWhenNoneRunning(t *testing.T) {
	svc := createTestService(setupTestRepo(t), newMockStepGetter(), newMockTaskRepo())

	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.stopAllParkingSamplers()
	}()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("stopAllParkingSamplers blocked with no samplers running")
	}
}

// TestOnSessionParkedHook_RejectsNewSamplerAfterStop is the regression guard
// for a Testing-round finding: onSessionParkedHook must not start a new
// parking-sampler goroutine once stopAllParkingSamplers has already run —
// otherwise a session that settles into "newly parked" concurrently with
// Service.Stop() can spawn a sampler whose context is never cancelled,
// reintroducing the exact class of leak stopAllParkingSamplers exists to
// close, just in a narrower window. Mirrors sendNowStopped's
// reject-new-work-after-stop guard (queue_send_now.go:332-347).
func TestOnSessionParkedHook_RejectsNewSamplerAfterStop(t *testing.T) {
	repo := setupTestRepo(t)
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	svc.config.BackgroundSampleInterval = 5 * time.Millisecond
	probe := &fakeProbe{result: probeResultLive}
	svc.SetBackgroundProbe(probe)

	const taskID, sessionID = "task-afterstop", "session-afterstop"
	svc.getOrCreateParkedState(sessionID).observedDetached = true
	parkedTestSeedSession(t, repo, taskID, sessionID, models.TaskSessionStateWaitingForInput)

	// Simulate Service.Stop() having already run its drain sweep — the
	// common "stop happened first" ordering that reproduces the finding.
	svc.stopAllParkingSamplers()

	svc.onSessionParkedHook(context.Background(), taskID, sessionID)

	ps := svc.parkedStateFor(sessionID)
	if ps == nil {
		t.Fatal("expected a parked state row to exist")
	}
	if ps.samplerCancel != nil {
		t.Fatal("expected no sampler to be started after stopAllParkingSamplers ran")
	}
	if !ps.parked {
		t.Fatal("expected the synchronous first sample to still park the session — only the recurring sampler is guarded, not the parked state itself")
	}

	// Give a wrongly-started sampler a chance to tick, then confirm it
	// never did: the real production consequence of the gap is a leaked
	// goroutine that keeps calling the probe past shutdown.
	time.Sleep(20 * time.Millisecond)
	if got := probe.calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 probe call (the synchronous first sample only), got %d — a wrongly-started sampler ticked", got)
	}
}

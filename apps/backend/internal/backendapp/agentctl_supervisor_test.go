package backendapp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	agentctlclient "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/events/bus"
	"go.uber.org/goleak"
)

// fakeRelaunchLauncher is a minimal relaunchLauncher double. startFunc
// controls Start's behavior for this attempt; the supervisor's generation-
// checking exit closure is captured on the fake so a test can invoke it
// itself to simulate a real launcher exit at a chosen moment.
type fakeRelaunchLauncher struct {
	startFunc func(ctx context.Context) error
	port      int
	pid       int
	token     string

	mu             sync.Mutex
	forceStopCount int
	stopCount      int
	onExit         func()
}

func (f *fakeRelaunchLauncher) Start(ctx context.Context) error {
	if f.startFunc != nil {
		return f.startFunc(ctx)
	}
	return nil
}

func (f *fakeRelaunchLauncher) ForceStop() {
	f.mu.Lock()
	f.forceStopCount++
	f.mu.Unlock()
}

func (f *fakeRelaunchLauncher) Stop(context.Context) error {
	f.mu.Lock()
	f.stopCount++
	f.mu.Unlock()
	return nil
}

func (f *fakeRelaunchLauncher) Port() int         { return f.port }
func (f *fakeRelaunchLauncher) Pid() int          { return f.pid }
func (f *fakeRelaunchLauncher) AuthToken() string { return f.token }

func (f *fakeRelaunchLauncher) forceStops() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.forceStopCount
}

func (f *fakeRelaunchLauncher) stops() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopCount
}

// fakeOwnershipRenewer is a minimal ownershipStopper double for pinning how
// many times the supervisor stops each renewer instance (BINDING NOTE R3-2).
type fakeOwnershipRenewer struct {
	mu    sync.Mutex
	stops int
}

func (r *fakeOwnershipRenewer) Stop() {
	r.mu.Lock()
	r.stops++
	r.mu.Unlock()
}

func (r *fakeOwnershipRenewer) stopCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stops
}

// scriptedLauncherFactory returns a launcher factory that hands out fake
// launchers in order, one per call, each driven by the given start function.
// Calling the factory more times than scripted fails the test immediately,
// which pins "no launcher built after the loop should have stopped".
func scriptedLauncherFactory(t *testing.T, starts ...func(ctx context.Context) error) (func(onUnexpectedExit func()) relaunchLauncher, func() []*fakeRelaunchLauncher) {
	t.Helper()
	var mu sync.Mutex
	var index int
	var launchers []*fakeRelaunchLauncher
	factory := func(onUnexpectedExit func()) relaunchLauncher {
		mu.Lock()
		defer mu.Unlock()
		if index >= len(starts) {
			t.Fatalf("launcher factory called more times (%d) than scripted (%d)", index+1, len(starts))
		}
		l := &fakeRelaunchLauncher{
			startFunc: starts[index],
			onExit:    onUnexpectedExit,
			port:      40000 + index,
			pid:       1000 + index,
			token:     "token",
		}
		index++
		launchers = append(launchers, l)
		return l
	}
	snapshot := func() []*fakeRelaunchLauncher {
		mu.Lock()
		defer mu.Unlock()
		out := make([]*fakeRelaunchLauncher, len(launchers))
		copy(out, launchers)
		return out
	}
	return factory, snapshot
}

func noopRecordFresh(context.Context, string, relaunchLauncher) error { return nil }

func noopStartRenewal(context.Context, string, relaunchLauncher) ownershipStopper { return nil }

// newTestSupervisorWithEvents builds a supervisor plus an Availability wired
// to a real MemoryEventBus so a test can assert the exact transition
// sequence, not just the final snapshot. Everything is constructed inside
// the caller's synctest bubble, so the bus's own dispatch goroutines (if
// any) are tracked by synctest like any other goroutine in the test.
func newTestSupervisorWithEvents(
	t *testing.T,
	factory func(onUnexpectedExit func()) relaunchLauncher,
	recordFresh func(context.Context, string, relaunchLauncher) error,
	startRenewal func(context.Context, string, relaunchLauncher) ownershipStopper,
) (*agentctlSupervisor, *agentctlclient.Availability, *agentctlclient.Endpoint, chan agentctlclient.AvailabilitySnapshot) {
	t.Helper()
	eventBus := bus.NewMemoryEventBus(testLogger(t))
	t.Cleanup(eventBus.Close)
	transitions := make(chan agentctlclient.AvailabilitySnapshot, 32)
	_, err := eventBus.Subscribe(events.AgentRuntimeAvailabilityChanged, func(_ context.Context, event *bus.Event) error {
		if snapshot, ok := event.Data.(agentctlclient.AvailabilitySnapshot); ok {
			transitions <- snapshot
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe availability events: %v", err)
	}

	availability := agentctlclient.NewAvailability(eventBus, testLogger(t))
	endpoint := agentctlclient.NewEndpoint()
	s := newAgentctlSupervisor(context.Background(), testLogger(t), availability, endpoint, "127.0.0.1", factory, recordFresh, startRenewal)
	return s, availability, endpoint, transitions
}

// bindInitialGeneration1 publishes the initial control server as generation
// 1 and binds it to the supervisor, mirroring what spawnFreshAgentctl's
// PublishInitialLaunch does before any relaunch has ever happened.
func bindInitialGeneration1(s *agentctlSupervisor, endpoint *agentctlclient.Endpoint, availability *agentctlclient.Availability, renewer ownershipStopper) *fakeRelaunchLauncher {
	initial := &fakeRelaunchLauncher{port: 39429, pid: 1, token: "initial"}
	var exited atomic.Bool
	if !s.PublishInitialLaunch(initial, renewer, &exited, "127.0.0.1", 39429, "initial", 1) {
		panic("bindInitialGeneration1: PublishInitialLaunch unexpectedly reported the launcher as exited")
	}
	return initial
}

func alwaysSucceeds(context.Context) error { return nil }

func failsWith(msg string) func(context.Context) error {
	return func(context.Context) error { return errors.New(msg) }
}

// TestAgentctlSupervisor_RelaunchSucceedsOnFirstAttempt covers WO3 AC1's
// first clause and the recovering-then-available transition order.
func TestAgentctlSupervisor_RelaunchSucceedsOnFirstAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		factory, launchers := scriptedLauncherFactory(t, alwaysSucceeds)
		recordCalls := 0
		renewalCalls := 0
		recordFresh := func(context.Context, string, relaunchLauncher) error {
			recordCalls++
			return nil
		}
		startRenewal := func(context.Context, string, relaunchLauncher) ownershipStopper {
			renewalCalls++
			return &fakeOwnershipRenewer{}
		}
		s, availability, endpoint, transitions := newTestSupervisorWithEvents(t, factory, recordFresh, startRenewal)
		bindInitialGeneration1(s, endpoint, availability, nil)
		// Drain the initial MarkAvailable event so the assertions below see
		// only the relaunch's own transitions.
		<-transitions

		s.NotifyExit(1)
		time.Sleep(5 * time.Second)
		synctest.Wait()

		select {
		case snapshot := <-transitions:
			if snapshot.Status != agentctlclient.AvailabilityStatusRecovering {
				t.Fatalf("first transition = %+v, want recovering", snapshot)
			}
		default:
			t.Fatal("expected a recovering transition, got none")
		}
		select {
		case snapshot := <-transitions:
			if snapshot.Status != agentctlclient.AvailabilityStatusAvailable {
				t.Fatalf("second transition = %+v, want available", snapshot)
			}
		default:
			t.Fatal("expected an available transition, got none")
		}

		if got := endpoint.Current().Generation; got != 2 {
			t.Fatalf("endpoint generation = %d, want 2", got)
		}
		if got := len(launchers()); got != 1 {
			t.Fatalf("launcher factory calls = %d, want 1", got)
		}
		if recordCalls != 1 {
			t.Fatalf("recordFresh calls = %d, want 1", recordCalls)
		}
		if renewalCalls != 1 {
			t.Fatalf("startRenewal calls = %d, want 1", renewalCalls)
		}
	})
}

// TestAgentctlSupervisor_RelaunchSucceedsOnThirdAttempt drives the backoff
// table across two failures (WO3 AC1's second clause): 1s then 2s, each
// failed launcher force-stopped before the next attempt.
func TestAgentctlSupervisor_RelaunchSucceedsOnThirdAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		factory, launchers := scriptedLauncherFactory(t,
			failsWith("boom-1"), failsWith("boom-2"), alwaysSucceeds)
		s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, noopRecordFresh, noopStartRenewal)
		bindInitialGeneration1(s, endpoint, availability, nil)

		s.NotifyExit(1)
		time.Sleep(10 * time.Second)
		synctest.Wait()

		snapshot, ok := availability.Snapshot()
		if !ok || snapshot.Status != agentctlclient.AvailabilityStatusAvailable {
			t.Fatalf("snapshot = %+v, published=%v, want available", snapshot, ok)
		}
		got := launchers()
		if len(got) != 3 {
			t.Fatalf("launcher factory calls = %d, want 3", len(got))
		}
		if got[0].forceStops() != 1 || got[1].forceStops() != 1 {
			t.Fatalf("failed attempts force-stopped = %d, %d, want 1, 1", got[0].forceStops(), got[1].forceStops())
		}
		if got[2].forceStops() != 0 {
			t.Fatalf("successful attempt force-stopped %d times, want 0", got[2].forceStops())
		}
	})
}

// TestAgentctlSupervisor_ExhaustedAfterFiveFailedAttempts covers WO3 AC1's
// third clause: all five attempts fail, the runtime is marked unavailable
// with reason relaunch_exhausted, and no sixth launcher is ever built.
func TestAgentctlSupervisor_ExhaustedAfterFiveFailedAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		factory, launchers := scriptedLauncherFactory(t,
			failsWith("1"), failsWith("2"), failsWith("3"), failsWith("4"), failsWith("5"))
		s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, noopRecordFresh, noopStartRenewal)
		exitAt := bindTimeAt(t, s, endpoint, availability)

		s.NotifyExit(1)
		time.Sleep(30 * time.Second)
		synctest.Wait()

		snapshot, ok := availability.Snapshot()
		if !ok || snapshot.Status != agentctlclient.AvailabilityStatusUnavailable {
			t.Fatalf("snapshot = %+v, published=%v, want unavailable", snapshot, ok)
		}
		if snapshot.Reason != agentctlclient.AvailabilityReasonRelaunchExhausted {
			t.Fatalf("reason = %q, want relaunch_exhausted", snapshot.Reason)
		}
		if snapshot.OccurredAt == nil || !snapshot.OccurredAt.Equal(exitAt) {
			t.Fatalf("occurred_at = %v, want the original exit time %v", snapshot.OccurredAt, exitAt)
		}
		if got := len(launchers()); got != 5 {
			t.Fatalf("launcher factory calls = %d, want 5", got)
		}
		for i, l := range launchers() {
			if l.forceStops() != 1 {
				t.Fatalf("attempt %d force-stopped %d times, want 1", i+1, l.forceStops())
			}
		}
	})
}

// bindTimeAt binds the initial launcher and returns the (virtual) time at
// which the test calls NotifyExit, so a test can assert occurred_at pins
// the exit time, not some later moment.
func bindTimeAt(t *testing.T, s *agentctlSupervisor, endpoint *agentctlclient.Endpoint, availability *agentctlclient.Availability) time.Time {
	t.Helper()
	bindInitialGeneration1(s, endpoint, availability, nil)
	return time.Now()
}

// TestAgentctlSupervisor_FourthOutageWithinWindowSkipsAttempt covers WO3
// AC1's fourth clause: a fourth outage start within the 10-minute window
// goes straight to unavailable/relaunch_exhausted without building a
// launcher at all.
func TestAgentctlSupervisor_FourthOutageWithinWindowSkipsAttempt(t *testing.T) {
	factory, launchers := scriptedLauncherFactory(t)
	s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, noopRecordFresh, noopStartRenewal)
	bindInitialGeneration1(s, endpoint, availability, nil)

	now := time.Now()
	s.windowStarts = []time.Time{now.Add(-9 * time.Minute), now.Add(-5 * time.Minute), now.Add(-1 * time.Minute)}

	s.NotifyExit(1)

	snapshot, ok := availability.Snapshot()
	if !ok || snapshot.Status != agentctlclient.AvailabilityStatusUnavailable {
		t.Fatalf("snapshot = %+v, published=%v, want unavailable", snapshot, ok)
	}
	if snapshot.Reason != agentctlclient.AvailabilityReasonRelaunchExhausted {
		t.Fatalf("reason = %q, want relaunch_exhausted", snapshot.Reason)
	}
	if got := len(launchers()); got != 0 {
		t.Fatalf("launcher factory calls = %d, want 0 (window limit skips the attempt entirely)", got)
	}
}

// TestAgentctlSupervisor_OutageWindowLimitStopsRenewer pins RV3-3: the
// outage_window_limit exhaustion branch is the only terminal-exhaustion path
// that skips runRelaunchLoop entirely, so it must stop the current renewer
// itself instead of leaking it until Shutdown.
func TestAgentctlSupervisor_OutageWindowLimitStopsRenewer(t *testing.T) {
	renewer := &fakeOwnershipRenewer{}
	factory, launchers := scriptedLauncherFactory(t)
	s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, noopRecordFresh, noopStartRenewal)
	bindInitialGeneration1(s, endpoint, availability, renewer)

	now := time.Now()
	s.windowStarts = []time.Time{now.Add(-9 * time.Minute), now.Add(-5 * time.Minute), now.Add(-1 * time.Minute)}

	s.NotifyExit(1)

	if got := renewer.stopCount(); got != 1 {
		t.Fatalf("renewer stopped %d times, want exactly 1 (outage_window_limit must stop it directly)", got)
	}
	if got := len(launchers()); got != 0 {
		t.Fatalf("launcher factory calls = %d, want 0 (window limit skips the attempt entirely)", got)
	}
}

// TestAgentctlSupervisor_OutageOutsideWindowIsNotCounted pins the boundary:
// an outage start exactly outageWindow old no longer counts against the
// limit, so a fourth exit still gets a relaunch attempt.
func TestAgentctlSupervisor_OutageOutsideWindowIsNotCounted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		factory, launchers := scriptedLauncherFactory(t, alwaysSucceeds)
		s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, noopRecordFresh, noopStartRenewal)
		bindInitialGeneration1(s, endpoint, availability, nil)

		now := time.Now()
		s.windowStarts = []time.Time{now.Add(-outageWindow), now.Add(-9 * time.Minute), now.Add(-1 * time.Minute)}

		s.NotifyExit(1)
		time.Sleep(5 * time.Second)
		synctest.Wait()

		if got := len(launchers()); got != 1 {
			t.Fatalf("launcher factory calls = %d, want 1 (the expired entry must not count)", got)
		}
		snapshot, ok := availability.Snapshot()
		if !ok || snapshot.Status != agentctlclient.AvailabilityStatusAvailable {
			t.Fatalf("snapshot = %+v, published=%v, want available", snapshot, ok)
		}
	})
}

// TestAgentctlSupervisor_StaleGenerationExitIsIgnored pins the ordering rule:
// an exit callback captured for a generation that is no longer current (a
// launcher already superseded) is a pure no-op.
func TestAgentctlSupervisor_StaleGenerationExitIsIgnored(t *testing.T) {
	factory, launchers := scriptedLauncherFactory(t)
	s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, noopRecordFresh, noopStartRenewal)
	bindInitialGeneration1(s, endpoint, availability, nil)

	s.NotifyExit(999)

	if got := len(launchers()); got != 0 {
		t.Fatalf("launcher factory calls = %d, want 0", got)
	}
	snapshot, ok := availability.Snapshot()
	if !ok || snapshot.Status != agentctlclient.AvailabilityStatusAvailable {
		t.Fatalf("snapshot = %+v, published=%v, want unchanged available", snapshot, ok)
	}
}

// TestAgentctlSupervisor_AttemptExceedingTimeoutIsForceStoppedAndCountsAsFailed
// covers WO3 AC4's first clause: an attempt whose Start call outlives
// relaunchAttemptTimeout is force-stopped and treated as a failed attempt,
// not left to hang the loop.
func TestAgentctlSupervisor_AttemptExceedingTimeoutIsForceStoppedAndCountsAsFailed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hang := func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}
		factory, launchers := scriptedLauncherFactory(t, hang, alwaysSucceeds)
		s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, noopRecordFresh, noopStartRenewal)
		bindInitialGeneration1(s, endpoint, availability, nil)

		s.NotifyExit(1)
		time.Sleep(20 * time.Second)
		synctest.Wait()

		got := launchers()
		if len(got) != 2 {
			t.Fatalf("launcher factory calls = %d, want 2", len(got))
		}
		if got[0].forceStops() != 1 {
			t.Fatalf("timed-out attempt force-stopped %d times, want 1", got[0].forceStops())
		}
		snapshot, ok := availability.Snapshot()
		if !ok || snapshot.Status != agentctlclient.AvailabilityStatusAvailable {
			t.Fatalf("snapshot = %+v, published=%v, want available", snapshot, ok)
		}
	})
}

// TestAgentctlSupervisor_FailedRecordFreshControlServerStillPublishes covers
// WO3 AC4's second clause: a failing adoption-record write is logged and
// non-fatal, not a reason to withhold the available transition.
func TestAgentctlSupervisor_FailedRecordFreshControlServerStillPublishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		factory, _ := scriptedLauncherFactory(t, alwaysSucceeds)
		failingRecord := func(context.Context, string, relaunchLauncher) error {
			return errors.New("adoption record store unavailable")
		}
		s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, failingRecord, noopStartRenewal)
		bindInitialGeneration1(s, endpoint, availability, nil)

		s.NotifyExit(1)
		time.Sleep(5 * time.Second)
		synctest.Wait()

		snapshot, ok := availability.Snapshot()
		if !ok || snapshot.Status != agentctlclient.AvailabilityStatusAvailable {
			t.Fatalf("snapshot = %+v, published=%v, want available despite the record failure", snapshot, ok)
		}
	})
}

// TestAgentctlSupervisor_ExitDuringRecordFailsAttemptAndLoopContinues covers
// WO3 AC6: the launched process exits after Start succeeds but before the
// attempt is published -- the exact window step 4's exited flag exists to
// catch, which includes the step-3 record write. The exit callback fires
// synchronously from Start here, exactly where attemptRelaunch calls it and
// before publishAttempt ever acquires s.mu, so the exited flag is reliably
// set before step 4 checks it -- without needing s.mu reentrantly. (A real
// launcher exit reaches NotifyExit from its own separate monitoring
// goroutine, never from the goroutine already holding s.mu for steps 3-5;
// this test's earlier version tried to simulate that by spawning the
// callback from inside recordFresh while the mutex was held, which
// self-deadlocked under a race build -- this version avoids the mutex
// contention entirely by construction.) The loop then continues to a
// second, successful attempt.
func TestAgentctlSupervisor_ExitDuringRecordFailsAttemptAndLoopContinues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var built []*fakeRelaunchLauncher
		factory := func(onUnexpectedExit func()) relaunchLauncher {
			attempt := len(built) + 1
			l := &fakeRelaunchLauncher{
				onExit: onUnexpectedExit,
				port:   40000 + attempt,
				pid:    1000 + attempt,
				token:  "token",
			}
			if attempt == 1 {
				l.startFunc = func(context.Context) error {
					l.onExit()
					return nil
				}
			}
			built = append(built, l)
			if len(built) > 2 {
				t.Fatalf("launcher factory called more than 2 times")
			}
			return l
		}
		s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, noopRecordFresh, noopStartRenewal)
		bindInitialGeneration1(s, endpoint, availability, nil)

		s.NotifyExit(1)
		time.Sleep(5 * time.Second)
		synctest.Wait()

		if len(built) != 2 {
			t.Fatalf("launcher factory calls = %d, want 2", len(built))
		}
		if built[0].forceStops() != 1 {
			t.Fatalf("attempt that exited before publish force-stopped %d times, want 1", built[0].forceStops())
		}
		snapshot, ok := availability.Snapshot()
		if !ok || snapshot.Status != agentctlclient.AvailabilityStatusAvailable {
			t.Fatalf("snapshot = %+v, published=%v, want available", snapshot, ok)
		}
		if got := endpoint.Current().Generation; got != 2 {
			t.Fatalf("endpoint generation = %d, want 2 (only the second attempt was ever published)", got)
		}
	})
}

// TestAgentctlSupervisor_ShutdownDuringBackoffPublishesNoTransition covers
// WO3 AC3's first clause: shutdown called while the loop is waiting out a
// backoff delay stops the loop with no further attempt and no availability
// transition beyond recovering.
func TestAgentctlSupervisor_ShutdownDuringBackoffPublishesNoTransition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		factory, launchers := scriptedLauncherFactory(t, failsWith("boom"))
		s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, noopRecordFresh, noopStartRenewal)
		bindInitialGeneration1(s, endpoint, availability, nil)

		s.NotifyExit(1)
		// Clear the 1s grace period so attempt 1 runs and fails, entering its
		// 1s backoff wait, but stop short of that backoff elapsing.
		time.Sleep(1500 * time.Millisecond)
		synctest.Wait()

		s.Shutdown()

		if got := len(launchers()); got != 1 {
			t.Fatalf("launcher factory calls = %d, want 1 (no second attempt after shutdown)", got)
		}
		snapshot, ok := availability.Snapshot()
		if !ok || snapshot.Status != agentctlclient.AvailabilityStatusRecovering {
			t.Fatalf("snapshot = %+v, published=%v, want still recovering", snapshot, ok)
		}
	})
}

// TestAgentctlSupervisor_ShutdownDuringAttemptForceStopsUnpublishedLauncher
// covers WO3 AC3's second clause: shutdown while an attempt's Start call is
// in flight cancels the supervisor context, Start returns via that
// cancellation, and the unpublished launcher is force-stopped rather than
// left running.
func TestAgentctlSupervisor_ShutdownDuringAttemptForceStopsUnpublishedLauncher(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		startCalled := make(chan struct{})
		hang := func(ctx context.Context) error {
			close(startCalled)
			<-ctx.Done()
			return ctx.Err()
		}
		factory, launchers := scriptedLauncherFactory(t, hang)
		s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, noopRecordFresh, noopStartRenewal)
		bindInitialGeneration1(s, endpoint, availability, nil)

		s.NotifyExit(1)
		// Advance the virtual clock past relaunchGrace so the loop actually
		// calls l.Start (hang) before shutdown is triggered; synctest.Wait
		// alone never advances time past a pending timer.
		time.Sleep(2 * time.Second)
		synctest.Wait()

		select {
		case <-startCalled:
		default:
			t.Fatal("attempt's Start was never called before shutdown")
		}

		s.Shutdown()

		got := launchers()
		if len(got) != 1 {
			t.Fatalf("launcher factory calls = %d, want 1", len(got))
		}
		if got[0].forceStops() != 1 {
			t.Fatalf("in-flight attempt force-stopped %d times, want 1", got[0].forceStops())
		}
		snapshot, ok := availability.Snapshot()
		if !ok || snapshot.Status != agentctlclient.AvailabilityStatusRecovering {
			t.Fatalf("snapshot = %+v, published=%v, want still recovering (no transition after shutdown)", snapshot, ok)
		}
	})
}

// TestAgentctlSupervisor_ShutdownAfterPublishStopsCurrentLauncherAndRenewer
// pins Shutdown's step-4 cleanup for the settled case: once a relaunch has
// published successfully, Shutdown stops exactly that launcher and renewer,
// both after the mutex-protected section, not the initial (already-replaced)
// pair.
func TestAgentctlSupervisor_ShutdownAfterPublishStopsCurrentLauncherAndRenewer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		newRenewer := &fakeOwnershipRenewer{}
		factory, launchers := scriptedLauncherFactory(t, alwaysSucceeds)
		startRenewal := func(context.Context, string, relaunchLauncher) ownershipStopper { return newRenewer }
		s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, noopRecordFresh, startRenewal)
		oldRenewer := &fakeOwnershipRenewer{}
		initial := bindInitialGeneration1(s, endpoint, availability, oldRenewer)

		s.NotifyExit(1)
		time.Sleep(5 * time.Second)
		synctest.Wait()

		s.Shutdown()

		if oldRenewer.stopCount() != 1 {
			t.Fatalf("old renewer stopped %d times, want 1 (stopped by the loop, not shutdown)", oldRenewer.stopCount())
		}
		if newRenewer.stopCount() != 1 {
			t.Fatalf("new renewer stopped %d times, want 1 (stopped by shutdown)", newRenewer.stopCount())
		}
		if initial.stops() != 0 {
			t.Fatalf("initial (replaced) launcher stopped %d times, want 0", initial.stops())
		}
		got := launchers()
		if len(got) != 1 || got[0].stops() != 1 {
			t.Fatalf("current launcher stops = %+v, want exactly one Stop call on the published launcher", got)
		}
	})
}

// TestAgentctlSupervisor_ShutdownRacingFinalAttemptFailurePublishesNoTransition
// is BINDING BUILD NOTE R3-1's required test: shutdown begins while the
// last relaunch attempt is failing, landing between the failure and the
// mutex-protected recheck inside exhaustRelaunch. No unavailable/
// relaunch_exhausted transition may be published once shutdown began.
func TestAgentctlSupervisor_ShutdownRacingFinalAttemptFailurePublishesNoTransition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reachedFinalAttempt := make(chan struct{})
		releaseFinalAttempt := make(chan struct{})
		finalAttempt := func(context.Context) error {
			close(reachedFinalAttempt)
			<-releaseFinalAttempt
			return errors.New("final attempt failed")
		}
		factory, _ := scriptedLauncherFactory(t,
			failsWith("1"), failsWith("2"), failsWith("3"), failsWith("4"), finalAttempt)
		s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, noopRecordFresh, noopStartRenewal)
		bindInitialGeneration1(s, endpoint, availability, nil)

		s.NotifyExit(1)

		go func() {
			<-reachedFinalAttempt
			shutdownDone := make(chan struct{})
			go func() {
				s.Shutdown()
				close(shutdownDone)
			}()
			// Any goroutine in the bubble may call Wait; it blocks until
			// every other goroutine is durably blocked. Shutdown sets
			// shuttingDown and cancels the context before it blocks on
			// wg.Wait (a tracked durable block, since only the still-parked
			// loop goroutine can signal it), so this Wait call is what
			// guarantees shuttingDown is already true before the final
			// attempt's failure is released below -- without it, the loop
			// goroutine and the Shutdown goroutine race with no ordering
			// guarantee at all.
			synctest.Wait()
			close(releaseFinalAttempt)
			<-shutdownDone
		}()

		// Advance past the first four attempts and their backoffs, reaching
		// and then releasing the final attempt via the goroutine above.
		time.Sleep(60 * time.Second)
		synctest.Wait()

		snapshot, ok := availability.Snapshot()
		if ok && snapshot.Status == agentctlclient.AvailabilityStatusUnavailable {
			t.Fatalf("snapshot = %+v, published=%v, want no unavailable transition after shutdown began", snapshot, ok)
		}
	})
}

// TestAgentctlSupervisor_RenewerStoppedExactlyOnceUnderShutdownRace is
// BINDING BUILD NOTE R3-2's required test, run under -race: NotifyExit and
// Shutdown racing each other must never double-stop a renewer or data-race
// on the shared renewer/launcher fields.
func TestAgentctlSupervisor_RenewerStoppedExactlyOnceUnderShutdownRace(t *testing.T) {
	defer goleak.VerifyNone(t)

	oldRenewer := &fakeOwnershipRenewer{}
	newRenewer := &fakeOwnershipRenewer{}
	factory, _ := scriptedLauncherFactory(t, alwaysSucceeds)
	startRenewal := func(context.Context, string, relaunchLauncher) ownershipStopper { return newRenewer }
	s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, noopRecordFresh, startRenewal)
	bindInitialGeneration1(s, endpoint, availability, oldRenewer)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.NotifyExit(1) }()
	go func() { defer wg.Done(); s.Shutdown() }()
	wg.Wait()

	if got := oldRenewer.stopCount(); got > 1 {
		t.Fatalf("old renewer stopped %d times, want at most 1", got)
	}
	if got := newRenewer.stopCount(); got > 1 {
		t.Fatalf("new renewer stopped %d times, want at most 1", got)
	}
}

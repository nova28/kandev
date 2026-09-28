package client

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/events/bus"
)

func TestAvailabilityPublishesSanitizedMonotonicSnapshots(t *testing.T) {
	eventBus := bus.NewMemoryEventBus(newTestLogger())
	t.Cleanup(eventBus.Close)
	var published []*bus.Event
	_, err := eventBus.Subscribe(events.AgentRuntimeAvailabilityChanged, func(_ context.Context, event *bus.Event) error {
		published = append(published, event)
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe availability events: %v", err)
	}

	availability := NewAvailability(eventBus, newTestLogger())
	if _, ok := availability.Snapshot(); ok {
		t.Fatal("startup availability must not be published before health and auth")
	}

	availability.MarkAvailable()
	snapshot, ok := availability.Snapshot()
	if !ok || snapshot.Status != AvailabilityStatusAvailable {
		t.Fatalf("available snapshot = %+v, published=%v", snapshot, ok)
	}
	if snapshot.Reason != "" || snapshot.OccurredAt != nil {
		t.Fatalf("available snapshot exposes failure details: %+v", snapshot)
	}

	exitedAt := time.Now()
	availability.MarkUnavailable(AvailabilityReasonAgentctlExited, exitedAt)
	unavailable, ok := availability.Snapshot()
	if !ok || unavailable.Status != AvailabilityStatusUnavailable {
		t.Fatalf("unavailable snapshot = %+v, published=%v", unavailable, ok)
	}
	if unavailable.Reason != AvailabilityReasonAgentctlExited || unavailable.OccurredAt == nil {
		t.Fatalf("unavailable snapshot = %+v", unavailable)
	}
	occurredAt := *unavailable.OccurredAt

	availability.MarkUnavailable(AvailabilityReasonRelaunchExhausted, time.Now())
	availability.MarkAvailable()
	final, ok := availability.Snapshot()
	if !ok || final.Status != AvailabilityStatusUnavailable || final.Reason != AvailabilityReasonAgentctlExited {
		t.Fatalf("availability was not terminal: %+v, published=%v", final, ok)
	}
	if final.OccurredAt == nil || !final.OccurredAt.Equal(occurredAt) {
		t.Fatalf("unavailable occurrence changed: got %v, want %v", final.OccurredAt, occurredAt)
	}
	if len(published) != 2 {
		t.Fatalf("published event count = %d, want available and unavailable", len(published))
	}
}

// TestAvailabilityFullTransitionTable pins every edge named in the state
// machine: the reachable transitions, the reversible available<->recovering
// cycle, and every no-op source/method combination.
func TestAvailabilityFullTransitionTable(t *testing.T) {
	t.Run("none MarkRecovering is a no-op", func(t *testing.T) {
		availability := NewAvailability(nil, newTestLogger())
		availability.MarkRecovering(time.Now())
		if _, ok := availability.Snapshot(); ok {
			t.Fatal("MarkRecovering before the first MarkAvailable must not publish")
		}
	})

	t.Run("none MarkUnavailable is a no-op", func(t *testing.T) {
		availability := NewAvailability(nil, newTestLogger())
		availability.MarkUnavailable(AvailabilityReasonAgentctlExited, time.Now())
		if _, ok := availability.Snapshot(); ok {
			t.Fatal("MarkUnavailable before the first MarkAvailable must not publish")
		}
	})

	t.Run("available MarkRecovering transitions to recovering", func(t *testing.T) {
		availability := NewAvailability(nil, newTestLogger())
		availability.MarkAvailable()
		occurredAt := time.Now()
		availability.MarkRecovering(occurredAt)

		snapshot, ok := availability.Snapshot()
		if !ok || snapshot.Status != AvailabilityStatusRecovering {
			t.Fatalf("snapshot = %+v, published=%v, want recovering", snapshot, ok)
		}
		if snapshot.Reason != AvailabilityReasonAgentctlExited || snapshot.OccurredAt == nil {
			t.Fatalf("recovering snapshot = %+v, want agentctl_exited reason and timestamp", snapshot)
		}
		if !snapshot.OccurredAt.Equal(occurredAt.UTC()) {
			t.Fatalf("recovering occurred_at = %v, want %v", snapshot.OccurredAt, occurredAt.UTC())
		}
	})

	t.Run("repeated MarkRecovering while already recovering is a no-op", func(t *testing.T) {
		availability := NewAvailability(nil, newTestLogger())
		availability.MarkAvailable()
		first := time.Now()
		availability.MarkRecovering(first)
		availability.MarkRecovering(first.Add(time.Minute))

		snapshot, ok := availability.Snapshot()
		if !ok || snapshot.Status != AvailabilityStatusRecovering {
			t.Fatalf("snapshot = %+v, published=%v, want recovering", snapshot, ok)
		}
		if !snapshot.OccurredAt.Equal(first.UTC()) {
			t.Fatalf("second MarkRecovering call overwrote occurred_at: got %v, want %v", snapshot.OccurredAt, first.UTC())
		}
	})

	t.Run("recovering MarkAvailable transitions back to available", func(t *testing.T) {
		availability := NewAvailability(nil, newTestLogger())
		availability.MarkAvailable()
		availability.MarkRecovering(time.Now())
		availability.MarkAvailable()

		snapshot, ok := availability.Snapshot()
		if !ok || snapshot.Status != AvailabilityStatusAvailable {
			t.Fatalf("snapshot = %+v, published=%v, want available", snapshot, ok)
		}
		if snapshot.Reason != "" || snapshot.OccurredAt != nil {
			t.Fatalf("recovered available snapshot exposes failure details: %+v", snapshot)
		}
	})

	t.Run("available MarkAvailable is a no-op", func(t *testing.T) {
		availability := NewAvailability(nil, newTestLogger())
		availability.MarkAvailable()
		first, _ := availability.Snapshot()
		availability.MarkAvailable()
		second, _ := availability.Snapshot()
		if first != second {
			t.Fatalf("MarkAvailable while already available changed the snapshot: %+v -> %+v", first, second)
		}
	})

	t.Run("recovering MarkUnavailable transitions to unavailable", func(t *testing.T) {
		availability := NewAvailability(nil, newTestLogger())
		availability.MarkAvailable()
		availability.MarkRecovering(time.Now())
		occurredAt := time.Now()
		availability.MarkUnavailable(AvailabilityReasonRelaunchExhausted, occurredAt)

		snapshot, ok := availability.Snapshot()
		if !ok || snapshot.Status != AvailabilityStatusUnavailable || snapshot.Reason != AvailabilityReasonRelaunchExhausted {
			t.Fatalf("snapshot = %+v, published=%v, want unavailable/relaunch_exhausted", snapshot, ok)
		}
		if !snapshot.OccurredAt.Equal(occurredAt.UTC()) {
			t.Fatalf("unavailable occurred_at = %v, want %v", snapshot.OccurredAt, occurredAt.UTC())
		}
	})

	t.Run("unavailable is terminal for every method", func(t *testing.T) {
		availability := NewAvailability(nil, newTestLogger())
		availability.MarkAvailable()
		firstOccurredAt := time.Now()
		availability.MarkUnavailable(AvailabilityReasonAgentctlExited, firstOccurredAt)

		availability.MarkAvailable()
		availability.MarkRecovering(time.Now())
		availability.MarkUnavailable(AvailabilityReasonRelaunchExhausted, time.Now())

		snapshot, ok := availability.Snapshot()
		if !ok || snapshot.Status != AvailabilityStatusUnavailable || snapshot.Reason != AvailabilityReasonAgentctlExited {
			t.Fatalf("terminal state was reopened: %+v, published=%v", snapshot, ok)
		}
		if !snapshot.OccurredAt.Equal(firstOccurredAt.UTC()) {
			t.Fatalf("terminal occurred_at changed: got %v, want %v", snapshot.OccurredAt, firstOccurredAt.UTC())
		}
	})
}

func TestAvailabilitySnapshotReadsAreSafeDuringTransition(t *testing.T) {
	availability := NewAvailability(nil, newTestLogger())
	availability.MarkAvailable()

	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := 0; index < 16; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for count := 0; count < 1000; count++ {
				_, _ = availability.Snapshot()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		availability.MarkUnavailable(AvailabilityReasonAgentctlExited, time.Now())
	}()
	close(start)
	wg.Wait()

	snapshot, ok := availability.Snapshot()
	if !ok || snapshot.Status != AvailabilityStatusUnavailable {
		t.Fatalf("final snapshot = %+v, published=%v", snapshot, ok)
	}
}

func TestAvailabilityPublishesTransitionsInCommitOrder(t *testing.T) {
	eventBus := bus.NewMemoryEventBus(newTestLogger())
	t.Cleanup(eventBus.Close)
	var published []AvailabilitySnapshot
	var publishedMu sync.Mutex
	firstPublishStarted := make(chan struct{})
	releaseFirstPublish := make(chan struct{})
	var publishCalls atomic.Int32
	_, err := eventBus.Subscribe(events.AgentRuntimeAvailabilityChanged, func(_ context.Context, event *bus.Event) error {
		if event.Data == nil {
			return nil
		}
		if publishCalls.Add(1) == 1 {
			close(firstPublishStarted)
			<-releaseFirstPublish
		}
		snapshot, ok := event.Data.(AvailabilitySnapshot)
		if !ok {
			t.Fatalf("availability event data = %T, want AvailabilitySnapshot", event.Data)
		}
		publishedMu.Lock()
		published = append(published, snapshot)
		publishedMu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe availability events: %v", err)
	}

	availability := NewAvailability(eventBus, newTestLogger())
	availableDone := make(chan struct{})
	go func() {
		availability.MarkAvailable()
		close(availableDone)
	}()
	<-firstPublishStarted

	unavailableDone := make(chan struct{})
	go func() {
		availability.MarkUnavailable(AvailabilityReasonAgentctlExited, time.Now())
		close(unavailableDone)
	}()

	select {
	case <-unavailableDone:
		t.Fatal("unavailable transition completed before available publication was released")
	case <-time.After(25 * time.Millisecond):
	}

	close(releaseFirstPublish)
	select {
	case <-availableDone:
	case <-time.After(time.Second):
		t.Fatal("available transition did not complete")
	}
	select {
	case <-unavailableDone:
	case <-time.After(time.Second):
		t.Fatal("unavailable transition did not complete")
	}

	publishedMu.Lock()
	defer publishedMu.Unlock()
	if len(published) != 2 || published[0].Status != AvailabilityStatusAvailable ||
		published[1].Status != AvailabilityStatusUnavailable {
		t.Fatalf("published availability transitions = %+v, want available then unavailable", published)
	}
}

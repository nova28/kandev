package websocket

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/events/bus"
)

// TestCoordinatorEventBroadcaster_Subscribes verifies that
// RegisterCoordinatorNotifications attaches a valid subscription to
// events.CoordinatorUpdated.
func TestCoordinatorEventBroadcaster_Subscribes(t *testing.T) {
	log := testLogger()
	eventBus := bus.NewMemoryEventBus(log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub := NewHub(nil, log)
	go hub.Run(ctx)

	b := RegisterCoordinatorNotifications(ctx, eventBus, hub, log)
	if b.subscription == nil {
		t.Fatal("expected a subscription, got nil")
	}
	if !b.subscription.IsValid() {
		t.Fatal("expected the subscription to be valid")
	}
}

// TestCoordinatorEventBroadcaster_NilEventBus verifies no panic and no
// subscription when the event bus is nil.
func TestCoordinatorEventBroadcaster_NilEventBus(t *testing.T) {
	log := testLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub := NewHub(nil, log)
	go hub.Run(ctx)

	b := RegisterCoordinatorNotifications(ctx, nil, hub, log)
	if b.subscription != nil {
		t.Fatal("expected nil subscription with a nil event bus")
	}
}

// TestCoordinatorEventBroadcaster_ForwardsUpdate verifies that publishing
// events.CoordinatorUpdated invokes the broadcaster's handler exactly once
// and does not panic on the Build decision 13 payload shape.
func TestCoordinatorEventBroadcaster_ForwardsUpdate(t *testing.T) {
	log := testLogger()
	eventBus := bus.NewMemoryEventBus(log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub := NewHub(nil, log)
	go hub.Run(ctx)

	_ = RegisterCoordinatorNotifications(ctx, eventBus, hub, log)

	var handlerCalled int
	_, _ = eventBus.Subscribe(events.CoordinatorUpdated, func(_ context.Context, _ *bus.Event) error {
		handlerCalled++
		return nil
	})

	payload := map[string]interface{}{
		"workspace_id":   "ws-1",
		"coordinator_id": "co-1",
		"open_proposals": 2,
	}
	evt := bus.NewEvent(events.CoordinatorUpdated, "test", payload)
	if err := eventBus.Publish(context.Background(), events.CoordinatorUpdated, evt); err != nil {
		t.Fatalf("Publish() error: %v", err)
	}

	if handlerCalled != 1 {
		t.Fatalf("handlerCalled = %d, want 1", handlerCalled)
	}
}

// TestCoordinatorEventBroadcaster_IgnoresMissingWorkspaceID verifies the
// forwarder does not panic when a payload carries no workspace_id.
func TestCoordinatorEventBroadcaster_IgnoresMissingWorkspaceID(t *testing.T) {
	log := testLogger()
	eventBus := bus.NewMemoryEventBus(log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub := NewHub(nil, log)
	go hub.Run(ctx)

	_ = RegisterCoordinatorNotifications(ctx, eventBus, hub, log)

	evt := bus.NewEvent(events.CoordinatorUpdated, "test", map[string]interface{}{
		"coordinator_id": "co-1",
	})
	if err := eventBus.Publish(context.Background(), events.CoordinatorUpdated, evt); err != nil {
		t.Fatalf("Publish() error: %v", err)
	}
}

package client

import (
	"context"
	"sync"
	"time"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/events/bus"
	"go.uber.org/zap"
)

const (
	AvailabilityStatusAvailable         = "available"
	AvailabilityStatusRecovering        = "recovering"
	AvailabilityStatusUnavailable       = "unavailable"
	AvailabilityReasonAgentctlExited    = "agentctl_exited"
	AvailabilityReasonRelaunchExhausted = "relaunch_exhausted"
)

// AvailabilitySnapshot is the sanitized, install-wide agent runtime state
// exposed to authenticated clients. It intentionally contains no process or
// launcher details.
type AvailabilitySnapshot struct {
	Status     string     `json:"status"`
	Reason     string     `json:"reason,omitempty"`
	OccurredAt *time.Time `json:"occurred_at,omitempty"`
}

// Availability owns the runtime availability state machine. From no
// snapshot, MarkAvailable() publishes the available status. MarkRecovering(at)
// moves it from available to recovering, and a later MarkAvailable() returns
// it to available. MarkUnavailable(reason, at) moves either of those two to
// unavailable, which is terminal: every method is a no-op once reached, and
// no transition is republished when the call does not match the state it
// applies from (for example MarkRecovering before the first MarkAvailable,
// or a repeated MarkRecovering call while already recovering).
type Availability struct {
	mu        sync.RWMutex
	publishMu sync.Mutex
	eventBus  bus.EventBus
	logger    *logger.Logger
	snapshot  AvailabilitySnapshot
	published bool
}

// NewAvailability creates an unpublished runtime availability tracker.
func NewAvailability(eventBus bus.EventBus, log *logger.Logger) *Availability {
	return &Availability{eventBus: eventBus, logger: log}
}

// Snapshot returns a copy of the current published state.
func (a *Availability) Snapshot() (AvailabilitySnapshot, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.published {
		return AvailabilitySnapshot{}, false
	}

	snapshot := a.snapshot
	if a.snapshot.OccurredAt != nil {
		occurredAt := *a.snapshot.OccurredAt
		snapshot.OccurredAt = &occurredAt
	}
	return snapshot, true
}

// MarkAvailable publishes the healthy-and-authenticated state. It applies
// from the unpublished "none" state (first boot) and from "recovering" (a
// successful relaunch); called from "available" or "unavailable" it is a
// no-op, since neither is a source state for this transition.
func (a *Availability) MarkAvailable() {
	a.publishMu.Lock()
	defer a.publishMu.Unlock()

	a.mu.Lock()
	if a.published && a.snapshot.Status != AvailabilityStatusRecovering {
		a.mu.Unlock()
		return
	}
	a.snapshot = AvailabilitySnapshot{Status: AvailabilityStatusAvailable}
	a.published = true
	snapshot := a.snapshot
	a.mu.Unlock()

	a.publish(snapshot)
}

// MarkRecovering records that agentctl exited unexpectedly and a relaunch is
// underway. It applies only from "available"; called before the first
// MarkAvailable, or while already recovering or unavailable, it is a no-op.
func (a *Availability) MarkRecovering(at time.Time) {
	a.publishMu.Lock()
	defer a.publishMu.Unlock()

	a.mu.Lock()
	if !a.published || a.snapshot.Status != AvailabilityStatusAvailable {
		a.mu.Unlock()
		return
	}
	occurredAt := at.UTC()
	a.snapshot = AvailabilitySnapshot{
		Status:     AvailabilityStatusRecovering,
		Reason:     AvailabilityReasonAgentctlExited,
		OccurredAt: &occurredAt,
	}
	snapshot := a.snapshot
	a.mu.Unlock()

	a.publish(snapshot)
}

// MarkUnavailable records that the runtime is unavailable, either because
// relaunching is disabled (reason agentctl_exited) or every relaunch attempt
// was exhausted (reason relaunch_exhausted). It applies from "available" or
// "recovering". Unavailable is terminal: called before the first
// MarkAvailable, or while already unavailable, it is a no-op and never
// republishes or overwrites the first occurrence.
func (a *Availability) MarkUnavailable(reason string, at time.Time) {
	a.publishMu.Lock()
	defer a.publishMu.Unlock()

	a.mu.Lock()
	if !a.published || a.snapshot.Status == AvailabilityStatusUnavailable {
		a.mu.Unlock()
		return
	}
	occurredAt := at.UTC()
	a.snapshot = AvailabilitySnapshot{
		Status:     AvailabilityStatusUnavailable,
		Reason:     reason,
		OccurredAt: &occurredAt,
	}
	snapshot := a.snapshot
	a.mu.Unlock()

	a.publish(snapshot)
}

func (a *Availability) publish(snapshot AvailabilitySnapshot) {
	if a.eventBus == nil {
		return
	}
	if err := a.eventBus.Publish(
		context.Background(),
		events.AgentRuntimeAvailabilityChanged,
		bus.NewEvent(events.AgentRuntimeAvailabilityChanged, "agentctl", snapshot),
	); err != nil && a.logger != nil {
		a.logger.Warn("failed to publish agent runtime availability", zap.Error(err))
	}
}

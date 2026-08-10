package orchestrator

import (
	"context"
	"sync"
)

// backgroundProbePort is the orchestrator's narrow view of the BackgroundProbe
// port defined in the lifecycle package. Using a local interface instead of
// importing lifecycle directly keeps the dependency direction correct and lets
// tests inject a stub without the full lifecycle stack.
type backgroundProbePort interface {
	ProbeBackgroundWorkloads(ctx context.Context, kandevSessionID string) (string, error)
}

// sessionParkedState holds the per-session tracking state for the
// parked-on-background-work projection. Managed under Service.parkedStatesMu.
//
// Lock order: parkedStatesMu must be acquired before any per-session mutex
// (not the other way around). Never hold parkedStatesMu while calling into
// the session repository or workflow engine.
type sessionParkedState struct {
	sessionID        string
	observedDetached bool   // true once a Kind==shell detached launch was seen
	turnMarker       uint64 // increments per turn_started event via the FIFO consumer
	parked           bool
	revision         uint64
	lastSample       string             // "live" | "settled" | "unknown" | ""
	samplerCancel    context.CancelFunc // non-nil when a sampler goroutine is running
	// generation is a Go-internal anti-ABA counter, not part of the spec's
	// wire-visible Data model and never serialized on any carrier. The spec
	// requires turnMarker to restart at 0 on both eviction (reduced) and
	// revival (a fresh attestation on a reduced row), so turnMarker alone
	// cannot distinguish a stale in-flight sample from a since-evicted
	// execution from a freshly revived row that happens to read the same
	// (observedDetached, turnMarker) pair. generation increments once per
	// eviction of an existing row and is compared alongside turnMarker at
	// probe-revalidation time to close that gap.
	generation uint64
	// stateGeneration is a second Go-internal anti-staleness counter (Review
	// round 7, F6), also not part of the spec's Data model. onSessionParkedHook
	// and sampleAndPublishParked each read the session's CURRENT
	// TaskSessionState from the repository OUTSIDE parkedStatesMu (the lock
	// order comment above forbids calling into the repository while holding
	// it), then apply the write under the lock. If the session left
	// WAITING_FOR_INPUT for a reason that does NOT touch turnMarker or
	// observedDetached — e.g. straight to a terminal state, with no
	// accompanying turn_started — a probe already in flight can complete with
	// a stale "still WAITING_FOR_INPUT" premise, pass the (observedDetached,
	// turnMarker, generation) revalidation unchanged, and re-park a session
	// that has already settled elsewhere, with no sampler left running to
	// correct it (unparkOnStateLeave already stopped it). stateGeneration
	// increments on every unparkOnStateLeave call — the one place that
	// transition is observed — so an in-flight sample captured before it is
	// discarded by the same revalidation check the other two fields use,
	// without ever reading the repository under the lock.
	stateGeneration uint64
}

// taskParkedState holds the task-level OR of all session parked states.
type taskParkedState struct {
	mu sync.Mutex
	// members and memberRevision are index-aligned by sessionID: members
	// holds the last APPLIED parked bool, memberRevision the session-level
	// ps.revision it was applied from. memberRevision is the ordering guard
	// F7 (Review round 7) adds: two session-level transitions for the SAME
	// session can call updateTaskParkedState out of causal order (the older
	// goroutine descheduled between releasing parkedStatesMu and making this
	// call), and without an explicit order check the older, stale write can
	// land after and overwrite the newer one — the task-level OR then stays
	// wrong until some unrelated later transition happens to correct it. A
	// write whose sessionRevision is not strictly greater than the recorded
	// one is discarded rather than applied.
	members        map[string]bool
	memberRevision map[string]uint64
	parked         bool
	revision       uint64
}

// stopSampler cancels the running sampler goroutine (if any).
// Must be called with parkedStatesMu held.
func (ps *sessionParkedState) stopSampler() {
	if ps.samplerCancel != nil {
		ps.samplerCancel()
		ps.samplerCancel = nil
	}
}

// getOrCreateParkedState returns the session's parked state, creating it if absent.
func (s *Service) getOrCreateParkedState(sessionID string) *sessionParkedState {
	s.parkedStatesMu.Lock()
	defer s.parkedStatesMu.Unlock()
	if s.parkedStates == nil {
		s.parkedStates = make(map[string]*sessionParkedState)
	}
	if ps, ok := s.parkedStates[sessionID]; ok {
		return ps
	}
	ps := &sessionParkedState{sessionID: sessionID}
	s.parkedStates[sessionID] = ps
	return ps
}

// parkedStateFor returns the session's parked state or nil if not present.
func (s *Service) parkedStateFor(sessionID string) *sessionParkedState {
	s.parkedStatesMu.RLock()
	defer s.parkedStatesMu.RUnlock()
	return s.parkedStates[sessionID]
}

// markObservedDetached records that a registered recogniser attested a
// detached shell launch during the current turn (D3, AC-69/AC-69a). Creates
// the session's parkedState row if this is the first attestation ever seen
// for it. The lookup-or-create and the field write happen inside one
// critical section — never through getOrCreateParkedState followed by an
// unlocked field write, which races the sampler goroutine's locked reads of
// the same field.
func (s *Service) markObservedDetached(sessionID string) {
	s.parkedStatesMu.Lock()
	defer s.parkedStatesMu.Unlock()
	if s.parkedStates == nil {
		s.parkedStates = make(map[string]*sessionParkedState)
	}
	ps, ok := s.parkedStates[sessionID]
	if !ok {
		ps = &sessionParkedState{sessionID: sessionID}
		s.parkedStates[sessionID] = ps
	}
	ps.observedDetached = true
}

// clearObservedDetachedOnTurnStarted implements D3's turn-boundary rule: on
// every turn_started for a session with an existing parkedState row, it
// clears observedDetached (idempotent — a plain set-to-false) and increments
// turnMarker (NOT idempotent — it moves on every event, duplicates included,
// per D2/AC-41a's fourth clause) in the same critical section, and resets the
// sampler's in-flight sample state since a new turn invalidates the question
// any outstanding probe was asked. A turn_started for a session with no
// parkedState row is a no-op: it creates no entry and increments no marker
// (AC-41a's final clause).
//
// observedDetached is a required AND-term of computeParked, so clearing it
// deterministically makes the formula false regardless of lastSample or
// session state — a session that was parked cannot remain parked once its
// new turn has started. A turn_started can arrive with no accompanying
// session-state-leave transition (a self-resume, spec §N/D3), so
// unparkOnStateLeave is not guaranteed to run first; without recomputing and
// publishing here, ps.parked would stay stale (true) for the duration of an
// actively-running turn, with no sampler left running to correct it (this
// same call already stops it above).
func (s *Service) clearObservedDetachedOnTurnStarted(ctx context.Context, taskID, sessionID string) {
	s.parkedStatesMu.Lock()
	ps := s.parkedStates[sessionID]
	if ps == nil {
		s.parkedStatesMu.Unlock()
		return
	}
	ps.observedDetached = false
	ps.turnMarker++
	ps.stopSampler()
	ps.lastSample = ""
	wasParked := ps.parked
	var sessionRevision uint64
	if wasParked {
		ps.parked = false
		ps.revision++
		sessionRevision = ps.revision
	}
	s.parkedStatesMu.Unlock()

	if wasParked {
		s.publishParkedChanged(ctx, taskID, sessionID)
		s.updateTaskParkedState(ctx, taskID, sessionID, false, sessionRevision)
	}
}

// getOrCreateTaskParkedState returns the task's parked state, creating it if absent.
func (s *Service) getOrCreateTaskParkedState(taskID string) *taskParkedState {
	s.taskParkedStatesMu.Lock()
	defer s.taskParkedStatesMu.Unlock()
	if s.taskParkedStates == nil {
		s.taskParkedStates = make(map[string]*taskParkedState)
	}
	if ts, ok := s.taskParkedStates[taskID]; ok {
		return ts
	}
	ts := &taskParkedState{members: make(map[string]bool), memberRevision: make(map[string]uint64)}
	s.taskParkedStates[taskID] = ts
	return ts
}

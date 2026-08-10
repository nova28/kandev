package orchestrator

import (
	"context"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"go.uber.org/zap"
)

// probe result strings — match agentctl/server/process probe constants.
const (
	probeResultLive    = "live"
	probeResultSettled = "settled"
	probeResultUnknown = "unknown"
)

// computeParked returns true when all three projection terms are satisfied.
func (ps *sessionParkedState) computeParked(sessionState models.TaskSessionState) bool {
	return ps.observedDetached &&
		ps.lastSample == probeResultLive &&
		sessionState == models.TaskSessionStateWaitingForInput
}

// probeRevalidationSnapshot captures the values D2's revalidation rule
// compares against at probe completion. generation and stateGeneration are
// the anti-ABA/anti-staleness guards documented on sessionParkedState — they
// are not part of the spec's Data model and travel on no carrier.
type probeRevalidationSnapshot struct {
	observedDetached bool
	turnMarker       uint64
	generation       uint64
	stateGeneration  uint64
}

// captureProbeSnapshot returns the session's current (observedDetached,
// turnMarker, generation, stateGeneration) tuple, and ok=false when the
// session has no parkedState row or is not currently attested. AC-40a: a
// probe is issued only when the settling turn actually attested a detached
// launch, so the common turn end pays nothing.
func (s *Service) captureProbeSnapshot(sessionID string) (probeRevalidationSnapshot, bool) {
	s.parkedStatesMu.RLock()
	defer s.parkedStatesMu.RUnlock()
	ps := s.parkedStates[sessionID]
	if ps == nil || !ps.observedDetached {
		return probeRevalidationSnapshot{}, false
	}
	return probeRevalidationSnapshot{
		observedDetached: true,
		turnMarker:       ps.turnMarker,
		generation:       ps.generation,
		stateGeneration:  ps.stateGeneration,
	}, true
}

// currentSessionState reads the session's live TaskSessionState immediately
// before a parked write is applied, rather than trusting the state at
// hook-entry time. The settle hook that fires onSessionParkedHook only
// guarantees WAITING_FOR_INPUT at the instant it fires — the probe's own
// round trip (up to KANDEV_PARKED_PROBE_BUDGET) can outlast that window, and
// a session that leaves WAITING_FOR_INPUT while the probe is in flight must
// not be re-parked by a write that still assumes the state it had at entry.
// A lookup failure returns the zero value, which computeParked never treats
// as WAITING_FOR_INPUT — the cheap, non-parking direction.
func (s *Service) currentSessionState(ctx context.Context, sessionID string) models.TaskSessionState {
	session, err := s.repo.GetTaskSession(ctx, sessionID)
	if err != nil || session == nil {
		return ""
	}
	return session.State
}

// onSessionParkedHook fires synchronously after a session transitions to
// WAITING_FOR_INPUT. It runs the first probe — but only when the settling
// turn actually attested a detached launch (AC-40a) — updates the parked
// projection, and starts the background sampler goroutine if the session is
// parked.
func (s *Service) onSessionParkedHook(ctx context.Context, taskID, sessionID string) {
	if s.backgroundProbe == nil {
		return
	}
	snap, ok := s.captureProbeSnapshot(sessionID)
	if !ok {
		return
	}
	sample := s.runProbe(ctx, sessionID)
	sessionState := s.currentSessionState(ctx, sessionID)

	s.parkedStatesMu.Lock()
	ps := s.parkedStates[sessionID]
	if ps == nil || ps.observedDetached != snap.observedDetached || ps.turnMarker != snap.turnMarker || ps.generation != snap.generation || ps.stateGeneration != snap.stateGeneration {
		// D2 revalidation, applied in the SAME critical section as the write
		// below rather than as a separate, unlocked check beforehand — a
		// concurrent turn_started (or an eviction) cannot slip a state change
		// into the gap between validating and applying. Discarding writes
		// nothing: no transition, no revision move.
		s.parkedStatesMu.Unlock()
		return
	}
	ps.lastSample = sample
	oldParked := ps.parked
	newParked := ps.computeParked(sessionState)
	var startSampler bool
	var samplerCtx context.Context
	var sessionRevision uint64
	if newParked != oldParked {
		ps.parked = newParked
		ps.revision++
		// Captured in this same critical section — see updateTaskParkedState's
		// doc comment (F7, Review round 7) for why the outside-lock call below
		// needs it rather than trusting call order.
		sessionRevision = ps.revision
	}
	if newParked {
		ps.stopSampler()
		// Reject starting a new sampler once stopAllParkingSamplers has run
		// (Service.Stop() in progress or complete) — otherwise a session
		// settling into "newly parked" concurrently with shutdown could spawn
		// a sampler whose context nothing ever cancels. Checked in this same
		// critical section as the assignment below, mirroring sendNowStopped.
		if !s.parkedSamplersStopped {
			samplerCtx, ps.samplerCancel = context.WithCancel(context.Background())
			startSampler = true
			// Add(1) MUST happen in this same critical section, not after
			// unlock: stopAllParkingSamplers reads parkedSamplersStopped and
			// calls parkedSamplerWG.Wait() from outside this lock, so an
			// Add() that raced the unlock could land while Wait() already
			// observed a zero counter — a sync.WaitGroup misuse ("Add called
			// concurrently with Wait") that can panic, and in the
			// non-panicking case lets Stop() return before this sampler is
			// even registered. Mirrors launchSendNowClaim's
			// sendNowWorkers.Add(1), which is likewise called before that
			// function's mutex unlock (queue_send_now.go).
			s.parkedSamplerWG.Add(1)
		}
	} else {
		ps.stopSampler()
	}
	s.parkedStatesMu.Unlock()

	if newParked != oldParked {
		s.publishParkedChanged(ctx, taskID, sessionID)
		s.updateTaskParkedState(ctx, taskID, sessionID, newParked, sessionRevision)
	}

	if startSampler {
		go s.runParkingSampler(samplerCtx, taskID, sessionID)
	}
}

// runProbe calls the background probe with the configured budget. Every
// error, an out-of-domain result, and a panicking implementation all resolve
// to "unknown" (AC-46) — the caller MUST NOT read the port's returned value
// when it also returned a non-nil error, so that rule is applied once here
// rather than trusted to every caller.
func (s *Service) runProbe(parent context.Context, sessionID string) (result string) {
	result = probeResultUnknown
	defer func() {
		if r := recover(); r != nil {
			if s.logger != nil {
				s.logger.Warn("background probe panicked, treating as unknown",
					zap.String("session_id", sessionID),
					zap.Any("panic", r))
			}
			result = probeResultUnknown
		}
	}()
	probeCtx, cancel := context.WithTimeout(parent, s.config.BackgroundProbeBudget)
	defer cancel()
	value, err := s.backgroundProbe.ProbeBackgroundWorkloads(probeCtx, sessionID)
	if err != nil {
		return probeResultUnknown
	}
	switch value {
	case probeResultLive, probeResultSettled:
		return value
	default:
		return probeResultUnknown
	}
}

// sampleAndPublishParked runs one probe, updates the session parked state,
// and publishes if the parked value changed. Returns true if the sampler
// should continue running.
func (s *Service) sampleAndPublishParked(ctx context.Context, taskID, sessionID string) bool {
	if s.backgroundProbe == nil {
		return false
	}
	snap, ok := s.captureProbeSnapshot(sessionID)
	if !ok {
		return false
	}
	sample := s.runProbe(ctx, sessionID)
	sessionState := s.currentSessionState(ctx, sessionID)

	s.parkedStatesMu.Lock()
	ps := s.parkedStates[sessionID]
	if ps == nil || ps.observedDetached != snap.observedDetached || ps.turnMarker != snap.turnMarker || ps.generation != snap.generation || ps.stateGeneration != snap.stateGeneration {
		// Discarded per D2, revalidated in the same critical section as the
		// write below (see onSessionParkedHook). Nothing is written; the
		// loop's next tick re-issues against whatever the new turn's state
		// actually is.
		s.parkedStatesMu.Unlock()
		return true
	}
	ps.lastSample = sample
	oldParked := ps.parked
	newParked := ps.computeParked(sessionState)
	var sessionRevision uint64
	if newParked != oldParked {
		ps.parked = newParked
		ps.revision++
		sessionRevision = ps.revision
	}
	if !newParked {
		ps.stopSampler()
	}
	s.parkedStatesMu.Unlock()

	if newParked != oldParked {
		s.publishParkedChanged(ctx, taskID, sessionID)
		s.updateTaskParkedState(ctx, taskID, sessionID, newParked, sessionRevision)
	}

	return newParked
}

// unparkOnStateLeave implements AC-53/AC-68: when a session leaves
// WAITING_FOR_INPUT, sampling stops immediately — the session-state term of
// the formula is now false regardless of last_sample — and if the session
// was parked, the un-park is published without taking a further sample;
// last_sample is never re-read for this transition.
func (s *Service) unparkOnStateLeave(ctx context.Context, taskID, sessionID string) {
	s.parkedStatesMu.Lock()
	ps := s.parkedStates[sessionID]
	if ps == nil {
		s.parkedStatesMu.Unlock()
		return
	}
	ps.stopSampler()
	// F6 (Review round 7): bump stateGeneration on every call, not only when
	// wasParked — an in-flight sample racing this transition captured its
	// snapshot before knowing whether the session was parked, so the
	// revalidation this guards must not depend on that outcome either.
	ps.stateGeneration++
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

// evictParkedState implements the Entry-lifecycle eviction rule (AC-85): if
// the session is currently parked, it performs the ordinary true→false
// transition — flip parked, bump revision, publish session.activity_changed,
// update task-level membership — before touching the row itself. Only after
// that publish does it either reduce the row in place (remove=false: used on
// execution end and agent-context reset, where the session continues to
// exist — observedDetached/turnMarker/lastSample are cleared but revision is
// retained, so a later serialization still carries the retained revision
// rather than resetting to 0) or drop it outright (remove=true, used only
// when the session itself is being deleted — nothing will ever read this row
// again). A row that is already unparked, or absent, publishes nothing.
func (s *Service) evictParkedState(ctx context.Context, taskID, sessionID string, remove bool) {
	if sessionID == "" {
		return
	}
	s.parkedStatesMu.Lock()
	ps, ok := s.parkedStates[sessionID]
	if !ok {
		s.parkedStatesMu.Unlock()
		return
	}
	wasParked := ps.parked
	if wasParked {
		ps.parked = false
		ps.revision++
	}
	// Captured regardless of wasParked, for removeTaskParkedMember's ordering
	// guard below (F7) — even a no-op eviction (already unparked) needs a
	// revision to compare against a same-session write that might otherwise
	// race it.
	sessionRevision := ps.revision
	ps.stopSampler()
	ps.observedDetached = false
	ps.turnMarker = 0
	ps.lastSample = ""
	// F2 (Review round 2): bump the anti-ABA generation on every eviction of
	// an existing row, not just the parked ones. turnMarker restarts at 0
	// here and again on revival (spec: Data model → Entry lifecycle), so a
	// stale in-flight sample captured before this eviction could otherwise
	// pass revalidation against a later, unrelated execution's freshly
	// revived row that happens to read the same (observedDetached=true,
	// turnMarker=0) pair. generation, never reset, breaks that tie.
	ps.generation++
	if remove {
		delete(s.parkedStates, sessionID)
	}
	s.parkedStatesMu.Unlock()

	if wasParked {
		s.publishParkedChanged(ctx, taskID, sessionID)
	}
	// Spec (Task-level projection → "A session's entry is removed from
	// members when its parkedState row is evicted"): every eviction — not
	// only remove=true — drops the session from the task's membership,
	// never merely sets it to false. Leaving a false tombstone in ts.members
	// forever is the unbounded-growth defect this call replaces: a task
	// whose every session has since been deleted would otherwise still hold
	// one map entry per session ever parked, for the life of the process.
	s.removeTaskParkedMember(ctx, taskID, sessionID, sessionRevision)
}

// runParkingSampler ticks at BackgroundSampleInterval until context is done
// or the session is no longer parked.
func (s *Service) runParkingSampler(ctx context.Context, taskID, sessionID string) {
	defer s.parkedSamplerWG.Done()
	interval := s.config.BackgroundSampleInterval
	// AC-74: zero disables periodic sampling entirely — not "use the
	// default". The session stays parked at whatever the synchronous first
	// sample decided until it leaves WAITING_FOR_INPUT (unparkOnStateLeave).
	// Negative is already rejected at config-load time (parseParkedProbeInterval),
	// so this can only be reached with interval==0 or interval>0.
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			keepRunning := s.sampleAndPublishParked(ctx, taskID, sessionID)
			if !keepRunning {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// parkedSamplerShutdownTimeout bounds how long Stop() waits for parking
// samplers to drain before giving up and logging — mirrors
// sendNowClaimRecoveryTimeout's role for stopSendNowWorkers.
const parkedSamplerShutdownTimeout = 5 * time.Second

// stopAllParkingSamplers cancels every session's running parking-sampler
// goroutine and blocks until each has actually exited (parkedSamplerWG),
// rather than merely asking them to stop. Called from Service.Stop() so a
// backend shutdown does not leave sampler goroutines running against
// contexts rooted in context.Background(), with no owner left to cancel
// them. Also sets parkedSamplersStopped, under the same lock as the cancel
// sweep, so onSessionParkedHook cannot start a NEW sampler that this sweep
// would never see — mirrors stopSendNowWorkers's cancel-then-bounded-wait
// shape, including its reject-new-work-after-stop half.
func (s *Service) stopAllParkingSamplers() {
	s.parkedStatesMu.Lock()
	s.parkedSamplersStopped = true
	for _, ps := range s.parkedStates {
		ps.stopSampler()
	}
	s.parkedStatesMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.parkedSamplerWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(parkedSamplerShutdownTimeout):
		if s.logger != nil {
			s.logger.Warn("timed out waiting for parking samplers to drain during shutdown")
		}
	}
}

// publishParkedChanged re-publishes session.activity_changed so a parked
// transition rides on the spec's named carrier (Review round 2, F3: the API
// surface section and AC-62/68/78/85 all name session.activity_changed, not
// a dedicated event). The caller has already written the new
// parked/revision under parkedStatesMu and released it before calling this;
// publishForegroundActivityNow re-derives the parked triple itself via
// ParkedProjectionSnapshot, so it reads back exactly what was just written.
// foreground_activity and active_subagent_count are re-derived fresh too, so
// the frame is complete regardless of what triggered it.
func (s *Service) publishParkedChanged(ctx context.Context, taskID, sessionID string) {
	if s.eventBus == nil || sessionID == "" {
		return
	}
	s.publishForegroundActivityNow(
		ctx, taskID, sessionID,
		string(s.ForegroundActivity(sessionID)),
		s.ActiveSubagentCount(sessionID),
	)
}

// updateTaskParkedState updates the task-level OR and publishes a task update
// if the OR changed.
//
// sessionRevision is the session's ps.revision at the exact instant this
// write's parked value was decided, captured by the caller in the SAME
// critical section as that decision (see onSessionParkedHook,
// sampleAndPublishParked, unparkOnStateLeave). Two transitions for the same
// session can call this function out of causal order — the older
// goroutine's call can be descheduled between releasing parkedStatesMu and
// reaching this call, and land after a newer transition's call already did
// (Review round 7, F7). Comparing against the last-applied revision for
// this session, per task, rejects the stale write rather than letting it
// silently overwrite a newer one: the task-level OR would otherwise stay
// wrong until an unrelated later transition happened to correct it.
func (s *Service) updateTaskParkedState(ctx context.Context, taskID, sessionID string, parked bool, sessionRevision uint64) {
	if taskID == "" {
		return
	}
	ts := s.getOrCreateTaskParkedState(taskID)
	ts.mu.Lock()
	if ts.members == nil {
		ts.members = make(map[string]bool)
	}
	if ts.memberRevision == nil {
		ts.memberRevision = make(map[string]uint64)
	}
	if lastRevision, ok := ts.memberRevision[sessionID]; ok && sessionRevision <= lastRevision {
		ts.mu.Unlock()
		return
	}
	ts.memberRevision[sessionID] = sessionRevision
	ts.members[sessionID] = parked
	newTaskParked := anyParkedMember(ts.members)
	changed := newTaskParked != ts.parked
	if changed {
		ts.parked = newTaskParked
		ts.revision++
	}
	ts.mu.Unlock()

	if changed {
		s.publishTaskParkedChanged(ctx, taskID)
	}
}

// removeTaskParkedMember deletes a session's entry from the task's parked
// membership (rather than setting it to false, which updateTaskParkedState
// does for an ordinary transition), recomputes the task-level OR, and
// publishes on change. This is what evictParkedState calls, per the spec's
// eviction rule: a session's entry is removed from members when its
// parkedState row is evicted, for every eviction cause — not only when the
// session itself is deleted. Leaving a false tombstone behind would let
// ts.members grow by one entry per session ever parked, for the life of the
// process, even after the owning task and every one of its sessions are
// gone.
//
// If this leaves members empty, the task-level row itself is dropped —
// nothing will read it again until a future session on this task parks and
// getOrCreateTaskParkedState recreates it.
//
// sessionRevision is the session's ps.revision captured by evictParkedState
// in the same critical section as its own decision, mirroring
// updateTaskParkedState's ordering guard (F7): a stale, delayed eviction
// call must not remove a member a strictly newer transition already wrote.
// Unlike updateTaskParkedState, a call at the SAME revision (no session-level
// increment happened, e.g. the row was already unparked) still proceeds —
// only a strictly newer recorded revision blocks the removal.
func (s *Service) removeTaskParkedMember(ctx context.Context, taskID, sessionID string, sessionRevision uint64) {
	if taskID == "" {
		return
	}
	s.taskParkedStatesMu.Lock()
	ts, ok := s.taskParkedStates[taskID]
	if !ok {
		s.taskParkedStatesMu.Unlock()
		return
	}
	ts.mu.Lock()
	if lastRevision, ok := ts.memberRevision[sessionID]; ok && sessionRevision < lastRevision {
		ts.mu.Unlock()
		s.taskParkedStatesMu.Unlock()
		return
	}
	delete(ts.members, sessionID)
	delete(ts.memberRevision, sessionID)
	newTaskParked := anyParkedMember(ts.members)
	changed := newTaskParked != ts.parked
	if changed {
		ts.parked = newTaskParked
		ts.revision++
	}
	membersEmpty := len(ts.members) == 0
	ts.mu.Unlock()

	if membersEmpty {
		delete(s.taskParkedStates, taskID)
	}
	s.taskParkedStatesMu.Unlock()

	if changed {
		s.publishTaskParkedChanged(ctx, taskID)
	}
}

// anyParkedMember returns true if any member value is true.
func anyParkedMember(members map[string]bool) bool {
	for _, v := range members {
		if v {
			return true
		}
	}
	return false
}

// publishTaskParkedChanged triggers a task.updated event so the task DTO
// carries the refreshed task-level parked projection to clients.
func (s *Service) publishTaskParkedChanged(ctx context.Context, taskID string) {
	s.publishTaskActivityIfChanged(ctx, taskID)
}

// ParkedProjectionSnapshot implements dto.ParkedProjectionProvider.
func (s *Service) ParkedProjectionSnapshot(sessionID string) (bool, int64, uint64) {
	s.parkedStatesMu.RLock()
	ps := s.parkedStates[sessionID]
	if ps == nil {
		s.parkedStatesMu.RUnlock()
		return false, s.parkedEpoch, 0
	}
	parked, revision := ps.parked, ps.revision
	s.parkedStatesMu.RUnlock()
	return parked, s.parkedEpoch, revision
}

// TaskParkedProjectionSnapshot implements dto.TaskParkedProjectionProvider and
// internal/task/service's TaskParkedProvider. epoch is s.parkedEpoch, the same
// process-global epoch every session carrier already serializes (D1: identical
// on every carrier and every session and task, never a second task-scoped value).
func (s *Service) TaskParkedProjectionSnapshot(taskID string) (bool, int64, uint64) {
	s.taskParkedStatesMu.Lock()
	ts := s.taskParkedStates[taskID]
	s.taskParkedStatesMu.Unlock()
	if ts == nil {
		return false, s.parkedEpoch, 0
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.parked, s.parkedEpoch, ts.revision
}

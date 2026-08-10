package acp

import (
	"errors"
	"fmt"
	"sync"

	"github.com/kandev/kandev/internal/agentctl/types/streams"
)

// BackgroundLaunchRecognizer recognises a detached background-shell launch
// for one agent. Spec: docs/specs/disambiguate-waiting/spec.md, "API
// surface -> The launch-recogniser seam". The registry lives here, in
// agentctl, at the point that already dispatches on vendor
// (stampBackgroundShellWork) — the backend never keys anything by vendor
// (ADR-0049).
type BackgroundLaunchRecognizer interface {
	// AgentID returns the agentctl-internal agent id this recogniser is
	// registered for (matched against the normalizer's n.agentID).
	AgentID() string
	// RecognizesDetachedLaunch reports whether payload represents a detached
	// background-shell launch for this recogniser's agent.
	RecognizesDetachedLaunch(payload *streams.NormalizedPayload) bool
}

// backgroundLaunchRecognizers is the process-lifetime registry, keyed by
// agent id. Guarded by backgroundLaunchRecognizersMu rather than built once
// at init time, so tests can register an additional recogniser (AC-69)
// without a package-level var-init ordering dependency.
var (
	backgroundLaunchRecognizersMu sync.Mutex
	backgroundLaunchRecognizers   = map[string]BackgroundLaunchRecognizer{}
)

// RegisterBackgroundLaunchRecognizer registers r for its AgentID(). D7:
// registration is process-start and single-valued — at most one recogniser
// per agent id, ever. A nil recogniser or one with an empty AgentID() is
// rejected here rather than silently accepted. A duplicate registration for
// an id that already has a recogniser is a programming error, not a runtime
// merge, and panics — mirroring the stdlib convention for this exact shape
// (e.g. sql.Register, image.RegisterFormat) rather than silently last-write-
// wins, which would make a duplicate registration undetectable.
//
// This is the public seam AC-69 exercises: registering a second recogniser
// for a different agent id must not require changing the probe, the
// projection, or any icon call site.
func RegisterBackgroundLaunchRecognizer(r BackgroundLaunchRecognizer) error {
	if r == nil {
		return errors.New("acp: nil BackgroundLaunchRecognizer")
	}
	id := r.AgentID()
	if id == "" {
		return errors.New("acp: BackgroundLaunchRecognizer has an empty AgentID()")
	}
	backgroundLaunchRecognizersMu.Lock()
	defer backgroundLaunchRecognizersMu.Unlock()
	if _, exists := backgroundLaunchRecognizers[id]; exists {
		panic(fmt.Sprintf("acp: BackgroundLaunchRecognizer already registered for agent id %q", id))
	}
	backgroundLaunchRecognizers[id] = r
	return nil
}

// unregisterBackgroundLaunchRecognizer removes a registered recogniser.
// Test-only: production registration is process-start and permanent: real
// recognisers are never unregistered. Exists so AC-69's test can register a
// second recogniser without leaking it into every other test in the
// package.
func unregisterBackgroundLaunchRecognizer(id string) {
	backgroundLaunchRecognizersMu.Lock()
	defer backgroundLaunchRecognizersMu.Unlock()
	delete(backgroundLaunchRecognizers, id)
}

// recognizesDetachedBackgroundLaunch looks up the registered recogniser for
// agentID and asks it whether payload is a detached background launch. An
// agent with no registered recogniser is never recognised, by construction
// — this is one of the two halves the spec's Kind==shell filter combines
// with (the other lives in the backend's event_handlers_streaming.go). A
// recogniser that panics is treated as "did not recognise" rather than
// crashing the normalizer.
func recognizesDetachedBackgroundLaunch(agentID string, payload *streams.NormalizedPayload) (recognized bool) {
	backgroundLaunchRecognizersMu.Lock()
	r, ok := backgroundLaunchRecognizers[agentID]
	backgroundLaunchRecognizersMu.Unlock()
	if !ok {
		return false
	}
	defer func() {
		if recover() != nil {
			recognized = false
		}
	}()
	return r.RecognizesDetachedLaunch(payload)
}

// claudeBackgroundLaunchRecognizer recognises Claude's detached background
// shell launches. Body is the shipped condition, unchanged:
// payload.ShellExec() != nil && payload.ShellExec().Background.
type claudeBackgroundLaunchRecognizer struct{}

func (claudeBackgroundLaunchRecognizer) AgentID() string { return claudeAgentID }

func (claudeBackgroundLaunchRecognizer) RecognizesDetachedLaunch(payload *streams.NormalizedPayload) bool {
	return payload != nil && payload.ShellExec() != nil && payload.ShellExec().Background
}

func init() {
	if err := RegisterBackgroundLaunchRecognizer(claudeBackgroundLaunchRecognizer{}); err != nil {
		panic(err)
	}
}

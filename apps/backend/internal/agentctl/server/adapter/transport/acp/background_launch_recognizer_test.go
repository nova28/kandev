package acp

import (
	"testing"

	"github.com/kandev/kandev/internal/agentctl/types/streams"
)

// fakeVendorRecognizer is a second, test-only recogniser for a fictional
// agent id. It recognises any active shell exec, regardless of the
// Background flag, so its behaviour is trivially distinguishable from
// Claude's real recogniser in assertions below.
type fakeVendorRecognizer struct {
	agentID string
}

func (f fakeVendorRecognizer) AgentID() string { return f.agentID }

func (f fakeVendorRecognizer) RecognizesDetachedLaunch(payload *streams.NormalizedPayload) bool {
	return payload != nil && payload.ShellExec() != nil
}

// TestRegisterBackgroundLaunchRecognizer_SecondVendor is AC-69: registering
// a second recogniser for a different agent id, through the public
// registration API, from test code only, must not require changing the
// probe, the projection, the backend consumer, or any icon call site — it
// is exercised end to end here purely through NormalizeToolCall and the
// exported stampBackgroundShellWork path.
func TestRegisterBackgroundLaunchRecognizer_SecondVendor(t *testing.T) {
	const fakeAgentID = "fake-vendor-acp"
	if err := RegisterBackgroundLaunchRecognizer(fakeVendorRecognizer{agentID: fakeAgentID}); err != nil {
		t.Fatalf("RegisterBackgroundLaunchRecognizer: %v", err)
	}
	t.Cleanup(func() { unregisterBackgroundLaunchRecognizer(fakeAgentID) })

	args := map[string]any{
		"kind": "execute",
		"raw_input": map[string]any{
			"command": "ls",
			// Deliberately NOT run_in_background:true — the fake recogniser
			// recognises every shell exec regardless, unlike Claude's, so a
			// pass here proves the registered recogniser actually ran rather
			// than coincidentally matching Claude's condition.
		},
	}

	payload := NewNormalizer(fakeAgentID).NormalizeToolCall("execute", args)
	if !payload.IsActiveBackgroundWork() || !payload.IsDetachedBackgroundLaunch() {
		t.Fatal("expected the registered fake-vendor recogniser to attest a detached launch")
	}
	if got := payload.BackgroundWork().Kind; got != streams.BackgroundWorkKindShell {
		t.Fatalf("expected Kind=shell, got %q", got)
	}

	// A DIFFERENT, still-unregistered agent id must remain unattested — the
	// registration is scoped to fakeAgentID only.
	otherPayload := NewNormalizer("still-unregistered-acp").NormalizeToolCall("execute", args)
	if otherPayload.IsActiveBackgroundWork() {
		t.Fatal("expected an agent id with no registered recogniser to stay unattested")
	}
}

// TestRegisterBackgroundLaunchRecognizer_RejectsNilAndEmptyID verifies the
// registration-time rejection rules (D7).
func TestRegisterBackgroundLaunchRecognizer_RejectsNilAndEmptyID(t *testing.T) {
	if err := RegisterBackgroundLaunchRecognizer(nil); err == nil {
		t.Fatal("expected an error registering a nil recogniser")
	}
	if err := RegisterBackgroundLaunchRecognizer(fakeVendorRecognizer{agentID: ""}); err == nil {
		t.Fatal("expected an error registering a recogniser with an empty AgentID()")
	}
}

// TestRegisterBackgroundLaunchRecognizer_DuplicatePanics verifies D7's "a
// duplicate registration for the same id is a programming error, not a
// runtime merge" rule.
func TestRegisterBackgroundLaunchRecognizer_DuplicatePanics(t *testing.T) {
	const dupAgentID = "dup-vendor-acp"
	if err := RegisterBackgroundLaunchRecognizer(fakeVendorRecognizer{agentID: dupAgentID}); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	t.Cleanup(func() { unregisterBackgroundLaunchRecognizer(dupAgentID) })

	defer func() {
		if recover() == nil {
			t.Fatal("expected a duplicate registration for the same agent id to panic")
		}
	}()
	_ = RegisterBackgroundLaunchRecognizer(fakeVendorRecognizer{agentID: dupAgentID})
}

// panickingRecognizer always panics — used to verify a panicking recogniser
// is treated as "did not recognise" rather than crashing the normalizer.
type panickingRecognizer struct{ agentID string }

func (p panickingRecognizer) AgentID() string { return p.agentID }

func (p panickingRecognizer) RecognizesDetachedLaunch(*streams.NormalizedPayload) bool {
	panic("boom")
}

// TestRecognizesDetachedBackgroundLaunch_PanicIsTreatedAsNotRecognized
// verifies D7's "a recogniser that panics is treated as 'did not
// recognise'" rule.
func TestRecognizesDetachedBackgroundLaunch_PanicIsTreatedAsNotRecognized(t *testing.T) {
	const panicAgentID = "panic-vendor-acp"
	if err := RegisterBackgroundLaunchRecognizer(panickingRecognizer{agentID: panicAgentID}); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { unregisterBackgroundLaunchRecognizer(panicAgentID) })

	payload := streams.NewShellExec("sleep 30", "", "", 0, true)
	if recognizesDetachedBackgroundLaunch(panicAgentID, payload) {
		t.Fatal("expected a panicking recogniser to resolve to not-recognised, not propagate the panic")
	}
}

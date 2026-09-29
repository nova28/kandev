package backendapp

import (
	"context"
	"testing"
	"time"

	agentctlclient "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/common/config"
)

// TestWireUnexpectedExitHandlingWithoutRelaunchIsPlainMarkUnavailable pins
// spawnFreshAgentctl's cfg.Features.AgentctlRelaunch == false branch (RV2-9):
// with the flag disabled, wireUnexpectedExitHandling must return a nil
// supervisor and reduce onUnexpectedExit to the pre-relaunch-supervisor
// behavior -- an unexpected exit only marks the runtime unavailable, and
// never touches the exited flag PublishInitialLaunch depends on for the
// enabled path.
func TestWireUnexpectedExitHandlingWithoutRelaunchIsPlainMarkUnavailable(t *testing.T) {
	cfg := &config.Config{}
	cfg.Features.AgentctlRelaunch = false

	availability := agentctlclient.NewAvailability(nil, testLogger(t))
	availability.MarkAvailable()

	supervisor, exited, onUnexpectedExit := wireUnexpectedExitHandling(
		context.Background(), cfg, testLogger(t), availability,
		agentctlclient.NewEndpoint(), nil, nil)

	if supervisor != nil {
		t.Fatalf("supervisor = %#v, want nil with AgentctlRelaunch disabled", supervisor)
	}
	if exited == nil {
		t.Fatal("exited flag is nil")
	}
	if exited.Load() {
		t.Fatal("exited flag was already set before onUnexpectedExit ran")
	}

	onUnexpectedExit()

	if exited.Load() {
		t.Fatal("onUnexpectedExit flipped the exited flag; the disabled branch must not touch it")
	}
	snapshot, ok := availability.Snapshot()
	if !ok {
		t.Fatal("Snapshot() = not ok, want a published snapshot after onUnexpectedExit")
	}
	if snapshot.Status != agentctlclient.AvailabilityStatusUnavailable {
		t.Fatalf("Status = %q, want %q", snapshot.Status, agentctlclient.AvailabilityStatusUnavailable)
	}
	if snapshot.Reason != agentctlclient.AvailabilityReasonAgentctlExited {
		t.Fatalf("Reason = %q, want %q", snapshot.Reason, agentctlclient.AvailabilityReasonAgentctlExited)
	}
}

// TestWireUnexpectedExitHandlingWithRelaunchBuildsSupervisor is the enabled
// side of the same decision: with the flag on, a non-nil supervisor is
// returned and onUnexpectedExit flips the exited flag before notifying it,
// matching PublishInitialLaunch's expectations for the enabled path.
func TestWireUnexpectedExitHandlingWithRelaunchBuildsSupervisor(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.StandaloneHost = "localhost"
	cfg.Agent.StandalonePort = 39429
	cfg.Features.AgentctlRelaunch = true

	availability := agentctlclient.NewAvailability(nil, testLogger(t))

	supervisor, exited, onUnexpectedExit := wireUnexpectedExitHandling(
		context.Background(), cfg, testLogger(t), availability,
		agentctlclient.NewEndpoint(), nil, nil)

	if supervisor == nil {
		t.Fatal("supervisor = nil, want a built supervisor with AgentctlRelaunch enabled")
	}

	done := make(chan struct{})
	go func() {
		onUnexpectedExit()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("onUnexpectedExit did not return; NextExitCallback's notify may be blocked")
	}

	if !exited.Load() {
		t.Fatal("onUnexpectedExit did not flip the exited flag with AgentctlRelaunch enabled")
	}
}

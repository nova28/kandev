package backendapp

import (
	"context"
	"testing"

	agentctlclient "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/common/config"
)

// TestBuildAgentctlSupervisorRecordAndRenewalAreNoopsWhenSurvivalDisabled
// pins newAgentctlSupervisor's own contract ("the caller decides, not this
// type"): with agent survival off, buildAgentctlSupervisor's closures must
// not touch the (nil here) store/secretStore, and startRenewal must return a
// true nil interface. Returning a nil *agentruntime.OwnershipRenewer wrapped
// in the ownershipStopper interface would make every `!= nil` check
// elsewhere in the supervisor see a non-nil interface holding a nil pointer
// -- the classic Go typed-nil trap -- and a later Stop() call on it would
// follow.
func TestBuildAgentctlSupervisorRecordAndRenewalAreNoopsWhenSurvivalDisabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.StandaloneHost = "localhost"
	cfg.Agent.StandalonePort = 39429
	cfg.Features.AgentSurvival = false

	supervisor := buildAgentctlSupervisor(context.Background(), cfg, testLogger(t),
		agentctlclient.NewAvailability(nil, testLogger(t)), agentctlclient.NewEndpoint(), nil, nil)

	fake := &fakeRelaunchLauncher{port: 40001, pid: 1234, token: "tok"}

	if err := supervisor.recordFresh(context.Background(), cfg.Agent.StandaloneHost, fake); err != nil {
		t.Fatalf("recordFresh = %v, want nil (no-op with survival disabled)", err)
	}

	stopper := supervisor.startRenewal(context.Background(), cfg.Agent.StandaloneHost, fake)
	if stopper != nil {
		t.Fatalf("startRenewal = %#v, want a true nil interface with survival disabled", stopper)
	}
}

// TestBuildAgentctlSupervisorNewLauncherUsesConfiguredHostAndPort pins that
// the relaunch attempt factory builds each attempt's launcher from cfg at
// call time, not a snapshot taken when the supervisor was constructed -- a
// port spawnFreshAgentctl updates after a fallback (or a future relaunch
// publish) must take effect for the next relaunch attempt too.
func TestBuildAgentctlSupervisorNewLauncherUsesConfiguredHostAndPort(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.StandaloneHost = "127.0.0.1"
	cfg.Agent.StandalonePort = 40555

	supervisor := buildAgentctlSupervisor(context.Background(), cfg, testLogger(t),
		agentctlclient.NewAvailability(nil, testLogger(t)), agentctlclient.NewEndpoint(), nil, nil)

	l := supervisor.newLauncher(func() {})
	if got := l.Port(); got != 40555 {
		t.Fatalf("newLauncher's Port() = %d, want the configured port 40555 before Start", got)
	}

	cfg.Agent.StandalonePort = 40777
	l2 := supervisor.newLauncher(func() {})
	if got := l2.Port(); got != 40777 {
		t.Fatalf("newLauncher's Port() = %d, want the updated port 40777 (read cfg at call time)", got)
	}
}

// TestBuildAgentctlSupervisorRecordFreshPersistsWhenSurvivalEnabled pins the
// enabled side against a real (fake) control server: recordFresh must
// durably record the relaunch attempt's own identity, and startRenewal must
// return a stopper that starts and stops cleanly, mirroring
// spawnFreshAgentctl's own initial-launch behavior applied to a later
// relaunch attempt's launcher instead.
func TestBuildAgentctlSupervisorRecordFreshPersistsWhenSurvivalEnabled(t *testing.T) {
	server := newRecordingControlServer(t)
	secretStore := newIDGeneratingSecretStore()
	store := &fakeControlServerStore{}

	cfg := &config.Config{}
	cfg.HomeDir = gateTestHomeDir
	cfg.Features.AgentSurvival = true

	supervisor := buildAgentctlSupervisor(context.Background(), cfg, testLogger(t),
		agentctlclient.NewAvailability(nil, testLogger(t)), agentctlclient.NewEndpoint(), store, secretStore)

	host, port, err := splitEndpoint(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("splitEndpoint: %v", err)
	}
	fake := &fakeRelaunchLauncher{port: port, pid: 4321, token: "relaunched-token"}

	if err := supervisor.recordFresh(context.Background(), host, fake); err != nil {
		t.Fatalf("recordFresh = %v, want nil", err)
	}
	if len(store.upserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(store.upserts))
	}
	if store.upserts[0].CredentialSecretID == "" {
		t.Fatal("recorded credential secret ID is empty")
	}

	stopper := supervisor.startRenewal(context.Background(), host, fake)
	if stopper == nil {
		t.Fatal("startRenewal = nil, want a started renewer with survival enabled")
	}
	stopper.Stop()
}

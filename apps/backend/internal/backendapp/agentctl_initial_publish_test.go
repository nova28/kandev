package backendapp

import (
	"sync/atomic"
	"testing"

	agentctlclient "github.com/kandev/kandev/internal/agent/runtime/agentctl"
)

// TestAgentctlSupervisor_PublishInitialLaunchSkippedWhenExitedBeforePublish
// covers RV1-1: agentctl can exit unexpectedly after launcher.Provide starts
// it but before spawnFreshAgentctl gets a chance to publish it as the
// endpoint's current identity. NextExitCallback's generation-1 callback has
// nothing published yet to match, so NotifyExit alone silently drops that
// exit -- PublishInitialLaunch's own synchronous exited check is what must
// catch it instead, by refusing to publish a generation that never survived
// to be current.
func TestAgentctlSupervisor_PublishInitialLaunchSkippedWhenExitedBeforePublish(t *testing.T) {
	factory, _ := scriptedLauncherFactory(t)
	s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, noopRecordFresh, noopStartRenewal)

	l := &fakeRelaunchLauncher{port: 40001, pid: 1234, token: "tok"}
	var exited atomic.Bool
	exited.Store(true)

	ok := s.PublishInitialLaunch(l, nil, &exited, "127.0.0.1", 40001, "tok", 1234)
	if ok {
		t.Fatal("PublishInitialLaunch = true, want false when the launcher already exited")
	}

	if got := endpoint.Current().Generation; got != 0 {
		t.Fatalf("endpoint generation = %d, want 0 (never published)", got)
	}
	if _, published := availability.Snapshot(); published {
		t.Fatal("availability snapshot is published, want still unpublished")
	}

	s.mu.Lock()
	current := s.current
	s.mu.Unlock()
	if current != nil {
		t.Fatal("supervisor bound a launcher that exited before publish")
	}
}

// TestAgentctlSupervisor_PublishInitialLaunchPublishesWhenNotExited pins the
// happy path this same method must preserve: with no exit observed,
// PublishInitialLaunch publishes the endpoint, marks the runtime available,
// and binds the launcher/renewer as the supervisor's current pair -- exactly
// what spawnFreshAgentctl's own MarkAvailable/Publish/BindInitialLauncher
// sequence did before this method consolidated them into one critical
// section.
func TestAgentctlSupervisor_PublishInitialLaunchPublishesWhenNotExited(t *testing.T) {
	factory, _ := scriptedLauncherFactory(t)
	s, availability, endpoint, _ := newTestSupervisorWithEvents(t, factory, noopRecordFresh, noopStartRenewal)

	l := &fakeRelaunchLauncher{port: 40001, pid: 1234, token: "tok"}
	renewer := &fakeOwnershipRenewer{}
	var exited atomic.Bool

	ok := s.PublishInitialLaunch(l, renewer, &exited, "127.0.0.1", 40001, "tok", 1234)
	if !ok {
		t.Fatal("PublishInitialLaunch = false, want true when the launcher never exited")
	}

	id := endpoint.Current()
	if id.Generation != 1 || id.Port != 40001 || id.Token != "tok" || id.PID != 1234 {
		t.Fatalf("endpoint.Current() = %+v, want generation 1 with the published launch's identity", id)
	}
	snapshot, published := availability.Snapshot()
	if !published || snapshot.Status != agentctlclient.AvailabilityStatusAvailable {
		t.Fatalf("snapshot = %+v, published=%v, want available", snapshot, published)
	}

	s.mu.Lock()
	current, boundRenewer := s.current, s.renewer
	s.mu.Unlock()
	if current != l {
		t.Fatal("supervisor did not bind the published launcher as current")
	}
	if boundRenewer != renewer {
		t.Fatal("supervisor did not bind the given renewer")
	}
}

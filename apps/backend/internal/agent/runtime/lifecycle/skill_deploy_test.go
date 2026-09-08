package lifecycle

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	settingsmodels "github.com/kandev/kandev/internal/agent/settings/models"
	"github.com/kandev/kandev/internal/common/logger"
)

// fakeProfileReader returns a canned profile (or error) so we can drive the
// skill-deploy hook without wiring the full settings store.
type fakeProfileReader struct {
	profile *settingsmodels.AgentProfile
	err     error
}

func (f *fakeProfileReader) GetAgentProfile(_ context.Context, _ string) (*settingsmodels.AgentProfile, error) {
	return f.profile, f.err
}

// recordingDeployer captures whether DeploySkills was called and lets tests
// inject a return value.
type recordingDeployer struct {
	called atomic.Int32
	last   SkillDeployRequest
	result SkillDeployResult
	err    error
}

func (r *recordingDeployer) DeploySkills(_ context.Context, req SkillDeployRequest) (SkillDeployResult, error) {
	r.called.Add(1)
	r.last = req
	return r.result, r.err
}

func newSkillDeployTestManager(t *testing.T) *Manager {
	t.Helper()
	log, _ := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "console"})
	return &Manager{
		logger:        log,
		skillDeployer: NoopSkillDeployer(),
	}
}

// TestRunSkillDeploy_NoOpWithoutReader verifies the launch-prep hook
// short-circuits when no profile reader has been wired.
func TestRunSkillDeploy_NoOpWithoutReader(t *testing.T) {
	mgr := newSkillDeployTestManager(t)
	rec := &recordingDeployer{}
	mgr.skillDeployer = rec
	// agentProfileReader stays nil — should bail before reaching the deployer.

	mgr.runSkillDeploy(context.Background(),
		&LaunchRequest{AgentProfileID: "p1"},
		&LaunchRequest{WorkspacePath: "/tmp/ws"})

	if rec.called.Load() != 0 {
		t.Errorf("expected deployer to not be called, was called %d times", rec.called.Load())
	}
}

// TestRunSkillDeploy_FastPathEmptyProfile verifies that a shallow / kanban
// profile (empty skill_ids, empty desired_skills) short-circuits before
// invoking the deployer.
func TestRunSkillDeploy_FastPathEmptyProfile(t *testing.T) {
	mgr := newSkillDeployTestManager(t)
	rec := &recordingDeployer{}
	mgr.skillDeployer = rec
	mgr.agentProfileReader = &fakeProfileReader{
		profile: &settingsmodels.AgentProfile{ID: "p1", AgentID: "a1"},
	}

	mgr.runSkillDeploy(context.Background(),
		&LaunchRequest{AgentProfileID: "p1"},
		&LaunchRequest{WorkspacePath: "/tmp/ws", ExecutorType: "local_pc"})

	if rec.called.Load() != 0 {
		t.Errorf("shallow profile should fast-path, deployer was called %d times", rec.called.Load())
	}
}

// TestRunSkillDeploy_RichProfileInvokesDeployer verifies the deployer fires
// when the profile carries any of skill_ids or desired_skills.
func TestRunSkillDeploy_RichProfileInvokesDeployer(t *testing.T) {
	mgr := newSkillDeployTestManager(t)
	rec := &recordingDeployer{}
	mgr.skillDeployer = rec
	rich := &settingsmodels.AgentProfile{
		ID:          "p1",
		AgentID:     "a1",
		WorkspaceID: "ws-1",
		SkillIDs:    `["sk-foo"]`,
	}
	mgr.agentProfileReader = &fakeProfileReader{profile: rich}

	mgr.runSkillDeploy(context.Background(),
		&LaunchRequest{AgentProfileID: "p1", SessionID: "sess-1"},
		&LaunchRequest{WorkspacePath: "/tmp/ws", ExecutorType: "local_docker"})

	if rec.called.Load() != 1 {
		t.Fatalf("expected deployer once, got %d", rec.called.Load())
	}
	if rec.last.Profile != rich {
		t.Errorf("deployer received wrong profile: %+v", rec.last.Profile)
	}
	if rec.last.WorkspacePath != "/tmp/ws" || rec.last.ExecutorType != "local_docker" {
		t.Errorf("deployer received wrong context: %+v", rec.last)
	}
	if rec.last.WorkspaceID != "ws-1" || rec.last.SessionID != "sess-1" {
		t.Errorf("deployer received wrong ids: %+v", rec.last)
	}
}

// TestRunSkillDeploy_DeployerErrorDoesNotAbort verifies that a deployer
// error is logged but does not propagate (launch must keep going).
func TestRunSkillDeploy_DeployerErrorDoesNotAbort(t *testing.T) {
	mgr := newSkillDeployTestManager(t)
	rec := &recordingDeployer{err: errors.New("disk full")}
	mgr.skillDeployer = rec
	mgr.agentProfileReader = &fakeProfileReader{
		profile: &settingsmodels.AgentProfile{
			ID: "p1", AgentID: "a1", SkillIDs: `["sk-x"]`,
		},
	}

	// Must not panic, must not propagate. We assert by reaching the line
	// after the call and by checking the deployer was actually invoked.
	mgr.runSkillDeploy(context.Background(),
		&LaunchRequest{AgentProfileID: "p1"},
		&LaunchRequest{WorkspacePath: "/tmp/ws"})

	if rec.called.Load() != 1 {
		t.Errorf("deployer should still have been called, got %d", rec.called.Load())
	}
}

// TestRunSkillDeploy_MergesMetadataOnPreparedRequest verifies that any
// metadata patches and instructions directory the deployer returns are
// merged onto the prepared LaunchRequest so executor backends pick them
// up (Sprites manifest upload, instructions-dir hint).
func TestRunSkillDeploy_MergesMetadataOnPreparedRequest(t *testing.T) {
	mgr := newSkillDeployTestManager(t)
	rec := &recordingDeployer{
		result: SkillDeployResult{
			Metadata: map[string]any{
				MetadataKeySkillManifestJSON: `{"Skills":[]}`,
			},
			InstructionsDir: "/tmp/kandev/runtime/default/instructions/p1",
		},
	}
	mgr.skillDeployer = rec
	mgr.agentProfileReader = &fakeProfileReader{
		profile: &settingsmodels.AgentProfile{
			ID: "p1", AgentID: "a1", SkillIDs: `["sk-y"]`,
		},
	}
	prepared := &LaunchRequest{WorkspacePath: "/tmp/ws", ExecutorType: "local_docker"}

	mgr.runSkillDeploy(context.Background(),
		&LaunchRequest{AgentProfileID: "p1"}, prepared)

	if rec.called.Load() != 1 {
		t.Fatalf("expected deployer once, got %d", rec.called.Load())
	}
	if got := prepared.Metadata[MetadataKeySkillManifestJSON]; got != `{"Skills":[]}` {
		t.Errorf("manifest metadata = %v", got)
	}
	if got := prepared.Metadata[MetadataKeyInstructionsDir]; got != "/tmp/kandev/runtime/default/instructions/p1" {
		t.Errorf("instructions dir metadata = %v", got)
	}
}

// TestRunSkillDeploy_OfficeRuntimeFromEnv verifies OfficeRuntime is derived
// from the prepared (finalized) launch env carrying BOTH KANDEV_CLI and
// KANDEV_RUN_ID — both present for an office scheduler launch, both absent
// for a kanban launch (e.g. a heavy-routine task), which is the signal
// system skills gate on. KANDEV_CLI alone is deliberately insufficient: see
// the "KANDEV_CLI present without KANDEV_RUN_ID" case below.
func TestRunSkillDeploy_OfficeRuntimeFromEnv(t *testing.T) {
	rich := &settingsmodels.AgentProfile{ID: "p1", AgentID: "a1", SkillIDs: `["sk-foo"]`}

	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{
			name: "neither key present",
			env:  map[string]string{"GITLAB_TOKEN": "x"},
			want: false,
		},
		{
			// A profile env_vars entry can name a key "KANDEV_CLI" (nothing
			// rejects the reserved prefix) and mergeEnvFillMissing backfills
			// it into an ordinary kanban launch's env. That alone must not
			// flip OfficeRuntime on, since KANDEV_RUN_ID — set only by the
			// Office scheduler from a real Run row — is still absent.
			name: "KANDEV_CLI present without KANDEV_RUN_ID",
			env:  map[string]string{"KANDEV_CLI": "/usr/local/bin/agentctl"},
			want: false,
		},
		{
			name: "KANDEV_RUN_ID present without KANDEV_CLI",
			env:  map[string]string{"KANDEV_RUN_ID": "run-1"},
			want: false,
		},
		{
			name: "both KANDEV_CLI and KANDEV_RUN_ID present",
			env: map[string]string{
				"KANDEV_CLI":    "/usr/local/bin/agentctl",
				"KANDEV_RUN_ID": "run-1",
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := newSkillDeployTestManager(t)
			rec := &recordingDeployer{}
			mgr.skillDeployer = rec
			mgr.agentProfileReader = &fakeProfileReader{profile: rich}

			mgr.runSkillDeploy(context.Background(),
				&LaunchRequest{AgentProfileID: "p1"},
				&LaunchRequest{WorkspacePath: "/tmp/ws", Env: tc.env})

			if rec.called.Load() != 1 {
				t.Fatalf("expected deployer once, got %d", rec.called.Load())
			}
			if rec.last.OfficeRuntime != tc.want {
				t.Errorf("OfficeRuntime = %v, want %v", rec.last.OfficeRuntime, tc.want)
			}
		})
	}
}

// TestRunSkillDeploy_NoProfileID verifies that a launch without a profile id
// (legacy / pass-through executions) short-circuits.
func TestRunSkillDeploy_NoProfileID(t *testing.T) {
	mgr := newSkillDeployTestManager(t)
	rec := &recordingDeployer{}
	mgr.skillDeployer = rec
	mgr.agentProfileReader = &fakeProfileReader{
		profile: &settingsmodels.AgentProfile{ID: "p1", SkillIDs: `["sk-x"]`},
	}

	mgr.runSkillDeploy(context.Background(),
		&LaunchRequest{},
		&LaunchRequest{WorkspacePath: "/tmp/ws"})

	if rec.called.Load() != 0 {
		t.Errorf("missing profile id should skip deploy, called %d times", rec.called.Load())
	}
}

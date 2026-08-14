package agents

import (
	"slices"
	"testing"

	"github.com/kandev/kandev/pkg/agent"
)

func TestAntigravityACPContract(t *testing.T) {
	a := NewAntigravityACP()
	if got := a.ID(); got != "antigravity-acp" {
		t.Errorf("ID() = %q, want antigravity-acp", got)
	}
	if got := a.DisplayName(); got != "Antigravity (experimental)" {
		t.Errorf("DisplayName() = %q", got)
	}
	if !a.Enabled() || a.DisplayOrder() != 22 {
		t.Errorf("enabled/order = %v/%d, want true/22", a.Enabled(), a.DisplayOrder())
	}
	if got := a.BuildCommand(CommandOptions{}).Args(); !slices.Equal(got, []string{"agy-acp"}) {
		t.Errorf("BuildCommand() = %#v, want [agy-acp]", got)
	}

	rt := a.Runtime()
	if rt.Protocol != agent.ProtocolACP {
		t.Errorf("Runtime.Protocol = %q, want ACP", rt.Protocol)
	}
	if got := rt.Cmd.Args(); !slices.Equal(got, []string{"agy-acp"}) {
		t.Errorf("Runtime.Cmd = %#v, want [agy-acp]", got)
	}
	if rt.WorkingDir != "{workspace}" {
		t.Errorf("WorkingDir = %q, want {workspace}", rt.WorkingDir)
	}
	if !rt.SessionConfig.NativeSessionResume || rt.SessionConfig.CanRecover == nil || !*rt.SessionConfig.CanRecover {
		t.Error("session recovery must use agy-acp's persisted ACP session mapping")
	}
	if got := rt.SessionConfig.SessionDirTemplate; got != "{home}/.openab/agy-acp" {
		t.Errorf("SessionDirTemplate = %q, want {home}/.openab/agy-acp", got)
	}
	if a.RemoteAuth() != nil || a.InstallScript() != "" {
		t.Error("the private bridge must not advertise unsupported remote executor setup")
	}
	if got := a.PermissionSettings(); len(got) != 0 {
		t.Errorf("PermissionSettings() = %#v, want empty", got)
	}
	if got := a.InferenceConfig(); got == nil || !got.Supported || !slices.Equal(got.Command.Args(), []string{"agy-acp"}) {
		t.Errorf("InferenceConfig() = %+v, want supported agy-acp", got)
	}
}

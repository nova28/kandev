package agents

import (
	"context"
	"os/exec"
	"time"

	"github.com/kandev/kandev/internal/agent/usage"
	"github.com/kandev/kandev/pkg/agent"
)

const (
	antigravityACPBin = "agy-acp"
	antigravityBin    = "agy"
)

var (
	_ Agent          = (*AntigravityACP)(nil)
	_ InferenceAgent = (*AntigravityACP)(nil)
)

// AntigravityACP runs Google Antigravity through the locally installed
// agy-acp bridge. Antigravity CLI does not natively speak ACP; the bridge owns
// the ACP session-to-conversation mapping in ~/.openab/agy-acp.
//
// This is intentionally a local-install integration. Remote and Docker
// executors need an explicit bridge-install and credential-provisioning
// contract before they can be supported safely.
type AntigravityACP struct{}

func NewAntigravityACP() *AntigravityACP { return &AntigravityACP{} }

func (a *AntigravityACP) ID() string          { return "antigravity-acp" }
func (a *AntigravityACP) Name() string        { return "Google Antigravity ACP Agent" }
func (a *AntigravityACP) DisplayName() string { return "Antigravity (experimental)" }
func (a *AntigravityACP) Description() string {
	return "Google Antigravity via the local agy-acp ACP bridge. Local executor only; MCP and permission bridging are not yet supported."
}
func (a *AntigravityACP) Enabled() bool     { return true }
func (a *AntigravityACP) DisplayOrder() int { return 22 }

func (a *AntigravityACP) Logo(LogoVariant) []byte { return nil }

func (a *AntigravityACP) IsInstalled(ctx context.Context) (*DiscoveryResult, error) {
	result, err := Detect(ctx, WithCommand(antigravityACPBin))
	if err != nil || !result.Available {
		return result, err
	}
	if _, err := exec.LookPath(antigravityBin); err != nil {
		return &DiscoveryResult{}, nil
	}
	result.Capabilities = DiscoveryCapabilities{SupportsSessionResume: true}
	return result, nil
}

func (a *AntigravityACP) BuildCommand(CommandOptions) Command {
	return Cmd(antigravityACPBin).Build()
}

func (a *AntigravityACP) Runtime() *RuntimeConfig {
	canRecover := true
	return &RuntimeConfig{
		Cmd:            Cmd(antigravityACPBin).Build(),
		WorkingDir:     "{workspace}",
		Env:            map[string]string{},
		ResourceLimits: ResourceLimits{MemoryMB: 4096, CPUCores: 2.0, Timeout: time.Hour},
		Protocol:       agent.ProtocolACP,
		SessionConfig: SessionConfig{
			NativeSessionResume: true,
			CanRecover:          &canRecover,
			SessionDirTemplate:  "{home}/.openab/agy-acp",
		},
	}
}

func (a *AntigravityACP) RemoteAuth() *RemoteAuth { return nil }

// agy-acp is a deliberately local dependency. Do not claim that a remote
// executor can install it until its Rust binary and Antigravity credentials
// have a supported provisioning path.
func (a *AntigravityACP) InstallScript() string { return "" }

func (a *AntigravityACP) PermissionSettings() map[string]PermissionSetting {
	return emptyPermSettings
}

func (a *AntigravityACP) InferenceConfig() *InferenceConfig {
	return &InferenceConfig{Supported: true, Command: Cmd(antigravityACPBin).Build()}
}

func (a *AntigravityACP) BillingType() usage.BillingType { return defaultBillingType() }

// Package mcpmode defines the MCP mode values shared by the orchestrator and
// agentctl. Keeping the wire contract here prevents the two process tiers from
// accepting different values.
package mcpmode

const (
	Task             = "task"
	TaskTitlePending = "task-title-pending"
	Config           = "config"
	External         = "external"
	Office           = "office"
	Automation       = "automation"
	// Coordinator identifies a workspace coordinator's conversation session.
	// It is declared here for the copilot and settings DTOs that reference
	// it, but is deliberately absent from instanceModes: wiring it into the
	// agentctl instance API is owned by a later work package.
	Coordinator = "coordinator"
)

var instanceModes = [...]string{
	Task,
	TaskTitlePending,
	Config,
	Office,
	Automation,
}

// InstanceModes returns the modes accepted by the agentctl instance API.
// External is intentionally excluded because it belongs to the backend MCP
// endpoint, not to a live agentctl instance.
func InstanceModes() []string {
	return append([]string(nil), instanceModes[:]...)
}

// IsInstanceMode reports whether mode is valid for the agentctl instance API.
func IsInstanceMode(mode string) bool {
	for _, accepted := range instanceModes {
		if mode == accepted {
			return true
		}
	}
	return false
}

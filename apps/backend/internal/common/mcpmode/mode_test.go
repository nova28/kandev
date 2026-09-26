package mcpmode

import "testing"

func TestInstanceModes(t *testing.T) {
	for _, mode := range InstanceModes() {
		if !IsInstanceMode(mode) {
			t.Errorf("IsInstanceMode(%q) = false, want true", mode)
		}
	}

	if IsInstanceMode(External) {
		t.Errorf("IsInstanceMode(%q) = true, want false", External)
	}
}

// TestCoordinator_NotAnInstanceMode proves the coordinator MCP mode value
// exists but is deliberately withheld from the agentctl instance API: wiring
// it into live agent sessions is task 03's job, not WP-1's.
func TestCoordinator_NotAnInstanceMode(t *testing.T) {
	if Coordinator != "coordinator" {
		t.Errorf("Coordinator = %q, want %q", Coordinator, "coordinator")
	}
	if IsInstanceMode(Coordinator) {
		t.Errorf("IsInstanceMode(%q) = true, want false", Coordinator)
	}
}

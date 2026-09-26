package profile

import "testing"

// TestSurfaceCoordinator_Value pins the coordinator MCP surface value used by
// the copilot's conversation profile. Wiring it into Legacy()/normalizeSurface
// is owned by a later work package; this only declares the constant.
func TestSurfaceCoordinator_Value(t *testing.T) {
	if SurfaceCoordinator != "coordinator" {
		t.Errorf("SurfaceCoordinator = %q, want %q", SurfaceCoordinator, "coordinator")
	}
}

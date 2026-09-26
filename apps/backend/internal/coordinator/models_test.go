package coordinator

import "testing"

// TestActionProposeTask_Value pins the MCP action name the copilot's
// propose_task_kandev tool dispatches, per
// docs/specs/coordinator/system-design/proposals.md#propose. The dispatch
// site is owned by a later work package; this only declares the constant.
func TestActionProposeTask_Value(t *testing.T) {
	if ActionProposeTask != "coordinator.propose_task" {
		t.Errorf("ActionProposeTask = %q, want %q", ActionProposeTask, "coordinator.propose_task")
	}
}

// TestProposalStatus_Values pins the proposal status enum used by the store,
// service and DTOs.
func TestProposalStatus_Values(t *testing.T) {
	cases := map[ProposalStatus]string{
		ProposalStatusPending:   "pending",
		ProposalStatusApproving: "approving",
		ProposalStatusApproved:  "approved",
		ProposalStatusRejected:  "rejected",
		ProposalStatusFailed:    "failed",
	}
	for status, want := range cases {
		if string(status) != want {
			t.Errorf("status = %q, want %q", status, want)
		}
	}
}

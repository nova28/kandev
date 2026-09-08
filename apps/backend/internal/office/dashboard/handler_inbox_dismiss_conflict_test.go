package dashboard_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/office/shared"
)

// stubMarkFixed returns a caller-chosen error from MarkAgentPausedFixed so a
// test can drive the dismiss endpoint's error mapping.
type stubMarkFixed struct {
	pausedErr error
}

func (s *stubMarkFixed) MarkAgentRunFailedFixed(_ context.Context, _, _ string) error {
	return nil
}

func (s *stubMarkFixed) MarkAgentPausedFixed(_ context.Context, _, _ string) error {
	return s.pausedErr
}

func postDismissPaused(t *testing.T, deps *testDeps, agentID string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"kind":"agent_paused_after_failures","item_id":%q}`, agentID)
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/office/inbox/dismiss", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	deps.router.ServeHTTP(w, req)
	return w
}

// TestDismissInboxItem_StatusConflictReturns409 pins the transport half of
// the agent-status CAS: a concurrent writer moving the agent mid-recovery is
// an expected conflict the caller can retry, not an internal failure. The
// sentinel is raised inside office/service, which this package does not
// import, so the mapping has to recognise it through office/shared.
func TestDismissInboxItem_StatusConflictReturns409(t *testing.T) {
	deps := newTestDeps(t)
	deps.svc.SetMarkFixedHandler(&stubMarkFixed{
		// Wrapped exactly as MarkAgentPausedFixed wraps it, so the test
		// fails if the mapping ever switches to an equality check.
		pausedErr: fmt.Errorf("unpause agent: %w", shared.ErrAgentStatusChanged),
	})

	w := postDismissPaused(t, deps, "agent-1")

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["code"] != "agent_status_changed" {
		t.Fatalf("code = %q, want agent_status_changed", resp["code"])
	}
}

// TestDismissInboxItem_OtherErrorsStay500 keeps the conflict mapping narrow:
// a genuine backend failure must not be reported to the caller as a
// retryable conflict.
func TestDismissInboxItem_OtherErrorsStay500(t *testing.T) {
	deps := newTestDeps(t)
	deps.svc.SetMarkFixedHandler(&stubMarkFixed{
		pausedErr: errors.New("list failed runs: disk exploded"),
	})

	w := postDismissPaused(t, deps, "agent-1")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", w.Code, w.Body.String())
	}
}

// TestDismissInboxItem_SuccessReturns200 is the control: the conflict
// mapping must not change the happy path.
func TestDismissInboxItem_SuccessReturns200(t *testing.T) {
	deps := newTestDeps(t)
	deps.svc.SetMarkFixedHandler(&stubMarkFixed{})

	w := postDismissPaused(t, deps, "agent-1")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
}

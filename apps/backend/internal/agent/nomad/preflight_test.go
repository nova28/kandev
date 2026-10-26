package nomad

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func preflightServer(t *testing.T, pool, scheduler string, poolStatus int) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/node/pool/"):
			if poolStatus != 0 && poolStatus != http.StatusOK {
				http.Error(w, "missing", poolStatus)
				return
			}
			_, _ = w.Write([]byte(pool))
		case r.URL.Path == "/v1/operator/scheduler/configuration":
			_, _ = w.Write([]byte(scheduler))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	client, err := NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func poolPreflightProfile(placement string, max int) ProfileConfig {
	return ProfileConfig{Mode: ModePool, NodePool: "kandev", Placement: placement, MemoryMB: 256, MemoryMaxMB: max}
}

func TestPreflightPoolAcceptsPreparedCluster(t *testing.T) {
	c := preflightServer(t, `{"Name":"kandev"}`, `{"SchedulerConfig":{"SchedulerAlgorithm":"spread","MemoryOversubscriptionEnabled":true}}`, 0)
	if err := c.PreflightPool(context.Background(), poolPreflightProfile(PlacementSpread, 2048)); err != nil {
		t.Fatal(err)
	}
}

func TestPreflightPoolRefusals(t *testing.T) {
	enabled := `{"SchedulerConfig":{"SchedulerAlgorithm":"spread","MemoryOversubscriptionEnabled":true}}`
	disabled := `{"SchedulerConfig":{"SchedulerAlgorithm":"spread","MemoryOversubscriptionEnabled":false}}`
	cases := []struct {
		name    string
		client  *Client
		profile ProfileConfig
		want    string
	}{
		{"missing pool", preflightServer(t, ``, enabled, http.StatusNotFound), poolPreflightProfile(PlacementSpread, 2048), "not readable"},
		{"wrong pool", preflightServer(t, `{"Name":"other"}`, enabled, 0), poolPreflightProfile(PlacementSpread, 2048), "was not returned"},
		{"oversubscription off", preflightServer(t, `{"Name":"kandev"}`, disabled, 0), poolPreflightProfile(PlacementSpread, 2048), "oversubscription"},
		{"binpack on spread cluster", preflightServer(t, `{"Name":"kandev"}`, enabled, 0), poolPreflightProfile(PlacementBinpack, 2048), "binpack scheduler"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.client.PreflightPool(context.Background(), tc.profile)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPreflightPoolBinpackOverrideOnNodePool(t *testing.T) {
	c := preflightServer(t, `{"Name":"kandev","SchedulerConfiguration":{"SchedulerAlgorithm":"binpack"}}`,
		`{"SchedulerConfig":{"SchedulerAlgorithm":"spread","MemoryOversubscriptionEnabled":true}}`, 0)
	if err := c.PreflightPool(context.Background(), poolPreflightProfile(PlacementBinpack, 2048)); err != nil {
		t.Fatal(err)
	}
}

func TestPreflightPoolRejectsLegacyProfile(t *testing.T) {
	c := preflightServer(t, `{"Name":"kandev"}`, `{}`, 0)
	if err := c.PreflightPool(context.Background(), ProfileConfig{}); err == nil {
		t.Fatal("legacy profile must be rejected")
	}
}

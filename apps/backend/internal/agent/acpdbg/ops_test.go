package acpdbg

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProbe_DefaultWorkdirReachesSessionNew(t *testing.T) {
	t.Setenv("ACPDBG_HELPER_PROCESS", "1")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	runner, err := NewRunner(ctx, filepath.Join(t.TempDir(), "frames.jsonl"), RunConfig{
		AgentID: "helper",
		Command: []string{os.Args[0], "-test.run=^TestACPDBGHelperProcess$"},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	defer runner.Close("completed")

	if _, err := Probe(ctx, runner); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
}

func TestACPDBGHelperProcess(t *testing.T) {
	if os.Getenv("ACPDBG_HELPER_PROCESS") != "1" {
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(1)
		}
		switch request["method"] {
		case "initialize":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"result":  map[string]any{"protocolVersion": 1},
			})
		case "session/new":
			params, _ := request["params"].(map[string]any)
			cwd, _ := params["cwd"].(string)
			if !filepath.IsAbs(cwd) {
				_ = encoder.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"error":   map[string]any{"message": "cwd must be absolute"},
				})
				continue
			}
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"result":  map[string]any{"sessionId": "session-1"},
			})
		}
	}
	os.Exit(0)
}

func TestSessionLoadParamsIncludeChangedWorkdir(t *testing.T) {
	t.Parallel()

	got := sessionLoadParams("session-1", "/workspace-b")

	if got["sessionId"] != "session-1" {
		t.Fatalf("sessionId = %v, want session-1", got["sessionId"])
	}
	if got["cwd"] != "/workspace-b" {
		t.Fatalf("cwd = %v, want /workspace-b", got["cwd"])
	}
}

func TestBuildProbeResult_FallsBackToConfigOptionModels(t *testing.T) {
	t.Parallel()

	initResp := Frame{"result": map[string]any{
		"protocolVersion": float64(1),
		"agentInfo":       map[string]any{"name": "claude-agent-acp", "version": "0.42.0"},
	}}
	newResp := Frame{"result": map[string]any{
		"sessionId": "session-1",
		"configOptions": []any{
			map[string]any{
				"category":     "model",
				"currentValue": "default",
				"type":         "select",
				"options": []any{
					map[string]any{"value": "default", "name": "Default"},
					map[string]any{"value": "opus", "name": "Opus"},
				},
			},
			map[string]any{
				"category":     "mode",
				"currentValue": "plan",
				"type":         "select",
				"options": []any{
					map[string]any{"value": "default", "name": "Default"},
					map[string]any{"value": "plan", "name": "Plan Mode"},
				},
			},
		},
	}}

	got := buildProbeResult(initResp, newResp)

	if got.CurrentModelID != "default" {
		t.Fatalf("CurrentModelID = %q, want default", got.CurrentModelID)
	}
	if len(got.Models) != 2 || got.Models[0] != "default" || got.Models[1] != "opus" {
		t.Fatalf("Models = %+v, want [default opus]", got.Models)
	}
	if got.CurrentModeID != "plan" {
		t.Fatalf("CurrentModeID = %q, want plan", got.CurrentModeID)
	}
	if len(got.Modes) != 2 || got.Modes[0] != "default" || got.Modes[1] != "plan" {
		t.Fatalf("Modes = %+v, want [default plan]", got.Modes)
	}
}

func TestBuildProbeResult_PrefersLegacyModels(t *testing.T) {
	t.Parallel()

	got := buildProbeResult(Frame{}, Frame{"result": map[string]any{
		"models": map[string]any{
			"currentModelId":  "legacy",
			"availableModels": []any{map[string]any{"modelId": "legacy"}},
		},
		"configOptions": []any{map[string]any{
			"category":     "model",
			"currentValue": "fallback",
			"type":         "select",
			"options":      []any{map[string]any{"value": "fallback"}},
		}},
	}})

	if got.CurrentModelID != "legacy" {
		t.Fatalf("CurrentModelID = %q, want legacy", got.CurrentModelID)
	}
	if len(got.Models) != 1 || got.Models[0] != "legacy" {
		t.Fatalf("Models = %+v, want [legacy]", got.Models)
	}
}

func TestBuildProbeResult_PrefersLegacyModes(t *testing.T) {
	t.Parallel()

	got := buildProbeResult(Frame{}, Frame{"result": map[string]any{
		"modes": map[string]any{
			"currentModeId":  "legacy-mode",
			"availableModes": []any{map[string]any{"id": "legacy-mode"}},
		},
		"configOptions": []any{map[string]any{
			"category":     "mode",
			"currentValue": "fallback-mode",
			"type":         "select",
			"options":      []any{map[string]any{"value": "fallback-mode"}},
		}},
	}})

	if got.CurrentModeID != "legacy-mode" {
		t.Fatalf("CurrentModeID = %q, want legacy-mode", got.CurrentModeID)
	}
	if len(got.Modes) != 1 || got.Modes[0] != "legacy-mode" {
		t.Fatalf("Modes = %+v, want [legacy-mode]", got.Modes)
	}
}

func TestBuildProbeResult_FallsBackToConfigOptionGroupedModels(t *testing.T) {
	t.Parallel()

	got := buildProbeResult(Frame{}, Frame{"result": map[string]any{
		"sessionId": "session-grouped",
		"configOptions": []any{
			map[string]any{
				"category":     "model",
				"currentValue": "opus",
				"type":         "select",
				"options": []any{
					map[string]any{
						"group": "Anthropic",
						"options": []any{
							map[string]any{"value": "opus", "name": "Opus"},
							map[string]any{"value": "sonnet", "name": "Sonnet"},
						},
					},
					map[string]any{
						"group": "Other",
						"options": []any{
							map[string]any{"value": "haiku", "name": "Haiku"},
						},
					},
				},
			},
		},
	}})

	if got.CurrentModelID != "opus" {
		t.Fatalf("CurrentModelID = %q, want opus", got.CurrentModelID)
	}
	wantModels := map[string]bool{"opus": true, "sonnet": true, "haiku": true}
	if len(got.Models) != 3 {
		t.Fatalf("len(Models) = %d, want 3: %v", len(got.Models), got.Models)
	}
	for _, model := range got.Models {
		if !wantModels[model] {
			t.Fatalf("unexpected model %q in %v", model, got.Models)
		}
	}
}

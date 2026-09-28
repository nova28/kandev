import { produce } from "immer";
import type { Draft } from "immer";
import { describe, expect, it } from "vitest";
import { hydrateState } from "./hydrator";
import { defaultState } from "@/lib/state/default-state";
import type { AppState } from "@/lib/state/store";
import type { AgentRuntimeAvailability } from "@/lib/types/agent-runtime";

/** Builds a deep-cloned AppState from the default state for full-state hydration tests. */
function makeAppDraft(): AppState {
  return structuredClone(defaultState) as AppState;
}

describe("hydrateState — agent runtime availability", () => {
  it("stores a valid recovering snapshot", () => {
    const recovering: AgentRuntimeAvailability = {
      status: "recovering",
      reason: "agentctl_exited",
      occurred_at: "2026-09-29T00:00:00Z",
    };

    const result = produce(makeAppDraft(), (draft: Draft<AppState>) => {
      hydrateState(draft, { agentRuntime: recovering } as unknown as Partial<AppState>);
    });

    expect(result.agentRuntime).toEqual(recovering);
  });

  it("ignores a snapshot with an unknown status and keeps the previous value", () => {
    const unavailable: AgentRuntimeAvailability = {
      status: "unavailable",
      reason: "agentctl_exited",
      occurred_at: "2026-08-08T14:22:52Z",
    };

    const result = produce(makeAppDraft(), (draft: Draft<AppState>) => {
      draft.agentRuntime = unavailable;
      hydrateState(draft, {
        agentRuntime: { status: "degraded" },
      } as unknown as Partial<AppState>);
    });

    expect(result.agentRuntime).toEqual(unavailable);
  });
});

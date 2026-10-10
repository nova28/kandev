import { useState } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { TooltipProvider } from "@kandev/ui/tooltip";
import type { StoreApi } from "zustand";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { StateProvider, useAppStore, useAppStoreApi } from "@/components/state-provider";
import { ToastProvider } from "@/components/toast-provider";
import { AgentProfilePicker } from "@/components/settings/agent-profile-picker";
import {
  SettingsSaveProvider,
  useSettingsSaveCoordinator,
  type SettingsSaveCoordinator,
} from "@/components/settings/settings-save-provider";
import { fetchJson } from "@/lib/api/client";
import { normalizeAgentProfile } from "@/lib/api/domains/agent-profile-normalize";
import { clearNavigationBlockerForTests } from "@/lib/routing/navigation-guard";
import { toAgentProfileOption } from "@/lib/state/slices/settings/types";
import type { AppState } from "@/lib/state/store";
import type { AgentProfilePayload } from "@/lib/types/agent-profile";
import type { AgentProfilePayload as WsProfile } from "@/lib/types/backend";
import type { Agent, AvailableAgent } from "@/lib/types/http";
import { registerAgentsHandlers } from "@/lib/ws/handlers/agents";
import AgentSetupPage from "./page";
import type { DraftProfile } from "./agent-save-helpers";

vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  fetchJson: vi.fn(),
}));

const OWNER = "target-creation-owner";
const EXISTING = "target-original-profile";
const SIBLING = "target-arrived-profile";
const ACCEPTED = "target-accepted-profile";
const ROUTE = "/settings/agents/claude-code?mode=create";
const PICKER = "target-creation-picker";
const NAME = "Additional reviewer";
const MODEL = "review-model";
const LIVE_MODEL = "live-model";
const NAME_INPUT = "profile-name-input";
let sequence = 0;
let instant = "";
type SaveResult = Awaited<ReturnType<SettingsSaveCoordinator["saveAll"]>>;
const saves: Promise<SaveResult>[] = [];
const finishers: Array<() => void> = [];

function wire(id: string, name: string, overrides: Partial<AgentProfilePayload> = {}) {
  return {
    id,
    agent_id: OWNER,
    agent_display_name: "Claude Code",
    kind: "concrete",
    name,
    model: MODEL,
    allow_indexing: false,
    auto_approve: false,
    dangerously_skip_permissions: false,
    plan: "",
    cli_flags: [],
    cli_passthrough: false,
    enabled: true,
    created_at: instant,
    updated_at: instant,
    ...overrides,
  } satisfies AgentProfilePayload & WsProfile;
}

function configuredOwner(): Agent {
  return {
    id: OWNER,
    name: "claude-code",
    supports_mcp: true,
    mcp_config_path: "",
    inference_capable: true,
    profiles: [normalizeAgentProfile(wire(EXISTING, "Original profile"))],
    created_at: instant,
    updated_at: instant,
  };
}

function discovery(): AvailableAgent {
  return {
    name: "claude-code",
    display_name: "Claude Code",
    available: true,
    supports_mcp: true,
    installation_paths: [],
    capabilities: {
      supports_session_resume: false,
      supports_shell: false,
      supports_workspace_only: false,
    },
    model_config: {
      default_model: MODEL,
      available_models: [
        { id: MODEL, name: "Review model" },
        { id: LIVE_MODEL, name: "Live model" },
      ],
      supports_dynamic_models: false,
    },
    permission_settings: {},
    updated_at: instant,
  };
}

function acceptedPatch(raw: unknown) {
  const body = JSON.parse(String(raw)) as Record<string, unknown>;
  return wire(ACCEPTED, String(body.name ?? NAME), {
    ...body,
    updated_at: new Date(Date.parse(instant) + 200).toISOString(),
  });
}

function transport() {
  let accept!: (response: unknown) => void;
  let reject!: (error: Error) => void;
  const pending = new Promise<unknown>((resolve, fail) => {
    accept = resolve;
    reject = fail;
  });
  finishers.push(() => accept(wire(ACCEPTED, NAME)));
  const requests: Array<{ path: string; method: string; body: unknown }> = [];
  const state = { failMcp: false };
  vi.mocked(fetchJson).mockImplementation(
    async <T,>(url: string, options?: Parameters<typeof fetchJson>[1]) => {
      const path = new URL(url).pathname;
      const method = options?.init?.method ?? "GET";
      requests.push({ path, method, body: options?.init?.body });
      if (path.endsWith("/mcp-config")) {
        if (method === "POST" && state.failMcp) throw new Error("Target MCP save refused");
        return { profile_id: ACCEPTED, enabled: method === "POST", servers: {} } as T;
      }
      if (path === `/api/v1/agent-profiles/${ACCEPTED}` && method === "PATCH")
        return acceptedPatch(options?.init?.body) as T;
      if (path === `/api/v1/agents/${OWNER}/profiles` && method === "POST")
        return pending as Promise<T>;
      throw new Error(`Unexpected target transport: ${method} ${path}`);
    },
  );
  return { accept, reject, requests, state };
}

function mount() {
  const owner = configuredOwner();
  const remote = transport();
  let store!: StoreApi<AppState>;
  let coordinator!: SettingsSaveCoordinator;
  function Subject() {
    store = useAppStoreApi();
    coordinator = useSettingsSaveCoordinator();
    const profiles = useAppStore((state) => state.agentProfiles.items);
    const [selected, setSelected] = useState("");
    return (
      <>
        <AgentSetupPage />
        <AgentProfilePicker
          profiles={profiles}
          value={selected}
          onValueChange={setSelected}
          testId={PICKER}
        />
      </>
    );
  }
  render(
    <StateProvider
      initialState={{
        auth: {
          mode: "enabled",
          authenticated: true,
          user: {
            id: "target-admin",
            email: "target@example.test",
            display_name: "Admin",
            role: "admin",
            status: "active",
          },
        },
        settingsAgents: { items: [owner] },
        agentProfiles: {
          orderByAgent: {},
          items: owner.profiles.map((profile) => toAgentProfileOption(owner, profile)),
          version: 1,
        },
        agentDiscovery: { items: [discovery()], loaded: true, loading: false },
        availableAgents: { items: [discovery()], tools: [], loaded: true, loading: false },
        secrets: { items: [], loaded: true, loading: false },
      }}
    >
      <TooltipProvider>
        <ToastProvider>
          <SettingsSaveProvider>
            <Subject />
          </SettingsSaveProvider>
        </ToastProvider>
      </TooltipProvider>
    </StateProvider>,
  );
  return { ...remote, store: () => store, coordinator: () => coordinator };
}

type Context = ReturnType<typeof mount>;

beforeEach(() => {
  sequence += 1;
  instant = new Date(Date.UTC(2026, 9, 10, 12, sequence)).toISOString();
  vi.mocked(fetchJson).mockReset();
  window.history.replaceState({}, "", ROUTE);
});

afterEach(async () => {
  await act(async () => {
    finishers.splice(0).forEach((finish) => finish());
    await Promise.all(saves.splice(0));
  });
  cleanup();
  clearNavigationBlockerForTests();
  window.history.replaceState({}, "", "/");
});

async function submit(context: Context) {
  fireEvent.change(screen.getByTestId(NAME_INPUT), { target: { value: NAME } });
  await waitFor(() => expect(context.coordinator().hasDirty).toBe(true));
  let save!: Promise<SaveResult>;
  act(() => {
    save = context.coordinator().saveAll();
    saves.push(save);
  });
  await waitFor(() =>
    expect(
      context.requests.some(
        (request) =>
          request.path === `/api/v1/agents/${OWNER}/profiles` && request.method === "POST",
      ),
    ).toBe(true),
  );
  return { save };
}

function receive(context: Context, profile = wire(SIBLING, "Live sibling")) {
  act(() =>
    registerAgentsHandlers(context.store())["agent.profile.created"]!({
      id: `target-event-${sequence}-${profile.id}`,
      type: "notification",
      action: "agent.profile.created",
      timestamp: profile.updated_at,
      payload: { profile },
    }),
  );
}

async function selectSibling(context: Context) {
  receive(context);
  fireEvent.click(screen.getByTestId(PICKER));
  const option = screen.getByRole("option", { name: /Live sibling/ });
  expect(option.getAttribute("aria-disabled")).toBe("false");
  fireEvent.click(option);
  await waitFor(() => expect(screen.getByTestId(PICKER).textContent).toContain("Live sibling"));
}

function assertSibling(context: Context) {
  const state = context.store().getState();
  const owner = state.settingsAgents.items.find((item) => item.id === OWNER)!;
  expect.soft(owner.profiles.map((profile) => profile.id)).toContain(SIBLING);
  expect.soft(state.agentProfiles.items.map((profile) => profile.id)).toContain(SIBLING);
  expect.soft(screen.getByTestId(PICKER).textContent).toContain("Live sibling");
  expect.soft(screen.getByTestId(PICKER).textContent).not.toContain("Unavailable");
  fireEvent.click(screen.getByTestId(PICKER));
  const choice = screen.queryByRole("option", { name: /Live sibling/ });
  expect.soft(choice?.getAttribute("aria-disabled")).toBe("false");
  if (choice) fireEvent.click(choice);
}

async function acknowledge(context: Context, save: Promise<SaveResult>) {
  let result!: SaveResult;
  await act(async () => {
    context.accept(wire(ACCEPTED, NAME));
    result = await save;
  });
  return result;
}

describe("existing-owner creation publication", () => {
  // @covers AC-AGENTS-TARGET-CREATION-CATALOGUE-001.1
  // @covers AC-AGENTS-TARGET-CREATION-CATALOGUE-001.2
  // @covers AC-AGENTS-TARGET-CREATION-CATALOGUE-001.6
  it("retains a selected same-owner sibling after creation ACK", async () => {
    const context = mount();
    const { save } = await submit(context);
    await selectSibling(context);
    expect((await acknowledge(context, save)).failedIds.size).toBe(0);
    assertSibling(context);
  });

  // @covers AC-AGENTS-TARGET-CREATION-CATALOGUE-001.3
  it("accepts additional-profile creation without a live event", async () => {
    const context = mount();
    const { save } = await submit(context);
    expect((await acknowledge(context, save)).failedIds.size).toBe(0);
    const state = context.store().getState();
    expect(state.settingsAgents.items[0].profiles.map((profile) => profile.id)).toEqual([
      ACCEPTED,
      EXISTING,
    ]);

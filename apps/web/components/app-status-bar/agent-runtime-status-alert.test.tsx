import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { StateProvider, useAppStoreApi } from "@/components/state-provider";
import type { RestartCapabilityState } from "@/hooks/domains/system/use-restart-capability";
import type { KandevRestartController } from "@/hooks/domains/system/use-kandev-restart";
import type { AgentRuntimeAvailability } from "@/lib/types/agent-runtime";
import { AgentRuntimeStatusAlert } from "./agent-runtime-status-alert";

const RESTART_BUTTON_LABEL = "Restart Kandev";
const ALERT_TEST_ID = "agent-runtime-alert";

const restartCapability = vi.hoisted(() => ({
  value: {
    status: "resolved",
    capability: { supported: true, mode: "supervisor", adapter: "supervisor" },
  } as RestartCapabilityState,
}));
const restartController = vi.hoisted(() => ({
  value: {
    phase: "idle",
    errorMessage: null,
    isRestarting: false,
    start: vi.fn(),
    dismiss: vi.fn(),
  } as KandevRestartController,
}));

vi.mock("@/hooks/domains/system/use-restart-capability", () => ({
  useRestartCapability: () => restartCapability.value,
}));

vi.mock("@/hooks/domains/system/use-kandev-restart", () => ({
  useKandevRestart: () => restartController.value,
}));

vi.mock("@/components/settings/system/restart-progress-dialog", () => ({
  RestartProgressDialog: ({ phase }: { phase: string }) =>
    phase === "idle" ? null : <div data-testid="restart-progress-dialog" data-phase={phase} />,
}));

function RuntimeStateControls() {
  const store = useAppStoreApi();
  return (
    <>
      <button
        type="button"
        data-testid="runtime-recover"
        onClick={() => store.getState().setAgentRuntime({ status: "available" })}
      />
      <button
        type="button"
        data-testid="connection-recover"
        onClick={() => {
          store.getState().setConnectionStatus("connected");
          store.getState().setConnectionIssueSeverity("none");
        }}
      />
    </>
  );
}

function renderAlert(agentRuntime: AgentRuntimeAvailability | null) {
  return render(
    <StateProvider initialState={{ agentRuntime }}>
      <AgentRuntimeStatusAlert />
      <div data-testid="retained-route-content">Last known task</div>
      <RuntimeStateControls />
    </StateProvider>,
  );
}

const unavailableAgentctlExited: AgentRuntimeAvailability = {
  status: "unavailable",
  reason: "agentctl_exited",
  occurred_at: "2026-08-08T14:22:52Z",
};

const unavailableRelaunchExhausted: AgentRuntimeAvailability = {
  status: "unavailable",
  reason: "relaunch_exhausted",
  occurred_at: "2026-08-08T14:22:52Z",
};

const recovering: AgentRuntimeAvailability = {
  status: "recovering",
  reason: "agentctl_exited",
  occurred_at: "2026-08-08T14:22:52Z",
};

describe("AgentRuntimeStatusAlert", () => {
  beforeEach(() => {
    restartCapability.value = {
      status: "resolved",
      capability: { supported: true, mode: "supervisor", adapter: "supervisor" },
    };
    restartController.value = {
      phase: "idle",
      errorMessage: null,
      isRestarting: false,
      start: vi.fn(),
      dismiss: vi.fn(),
    };
  });

  afterEach(cleanup);

  it("renders one persistent alert with the supported restart flow", () => {
    renderAlert(unavailableAgentctlExited);

    expect(screen.getAllByRole("alert")).toHaveLength(1);
    expect(screen.getByText("Local agent runtime stopped")).toBeTruthy();
    expect(screen.getByText(/saved data remains safe/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: RESTART_BUTTON_LABEL })).toBeTruthy();
    expect(screen.getByTestId("retained-route-content")).toBeTruthy();
    expect(screen.getByTestId(ALERT_TEST_ID).dataset.status).toBe("unavailable");

    fireEvent.click(screen.getByRole("button", { name: RESTART_BUTTON_LABEL }));

    expect(restartController.value.start).toHaveBeenCalledOnce();
  });

  it("uses manual guidance when capability lookup is unavailable", () => {
    restartCapability.value = { status: "unavailable", capability: null };
    renderAlert(unavailableAgentctlExited);

    expect(screen.queryByRole("button", { name: RESTART_BUTTON_LABEL })).toBeNull();
    expect(screen.getByText(/terminal or service manager/i)).toBeTruthy();
  });

  it("keeps the alert through connection changes and restart errors", () => {
    restartController.value = {
      phase: "error",
      errorMessage: "restart request failed",
      isRestarting: false,
      start: vi.fn(),
      dismiss: vi.fn(),
    };
    renderAlert(unavailableAgentctlExited);

    fireEvent.click(screen.getByTestId("connection-recover"));
    expect(screen.getByRole("alert")).toBeTruthy();

    fireEvent.click(screen.getByTestId("runtime-recover"));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("keeps the phone action at the touch-target size and stacks the content", () => {
    renderAlert(unavailableAgentctlExited);

    const alert = screen.getByRole("alert");
    const action = screen.getByRole("button", { name: RESTART_BUTTON_LABEL });
    expect(alert.className).toContain("min-w-0");
    expect(action.className).toContain("h-11");
    expect(action.className).toContain("w-full");
    expect(action.className).toContain("sm:w-auto");
  });

  it("states that automatic recovery did not succeed for relaunch_exhausted, and still offers restart", () => {
    renderAlert(unavailableRelaunchExhausted);

    expect(screen.getByText("Automatic recovery did not succeed.")).toBeTruthy();
    expect(screen.getByRole("button", { name: RESTART_BUTTON_LABEL })).toBeTruthy();
  });

  it("does not state relaunch failure for a plain agentctl_exited outage", () => {
    renderAlert(unavailableAgentctlExited);

    expect(screen.queryByText("Automatic recovery did not succeed.")).toBeNull();
  });

  it("renders a neutral, non-dismissible, actionless notice while recovering", () => {
    renderAlert(recovering);

    expect(screen.getByRole("status")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByText("Restarting local agent runtime")).toBeTruthy();
    expect(screen.getByText(/paused while Kandev restarts/i)).toBeTruthy();
    const alertContainer = screen.getByTestId(ALERT_TEST_ID);
    expect(alertContainer.dataset.status).toBe("recovering");
    expect(within(alertContainer).queryByRole("button")).toBeNull();
    expect(screen.getByTestId("retained-route-content")).toBeTruthy();
  });

  it("clears the recovering notice without a reload once available", () => {
    renderAlert(recovering);
    expect(screen.getByRole("status")).toBeTruthy();

    fireEvent.click(screen.getByTestId("runtime-recover"));

    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.queryByTestId(ALERT_TEST_ID)).toBeNull();
  });

  it("renders nothing when available", () => {
    renderAlert({ status: "available" });

    expect(screen.queryByTestId(ALERT_TEST_ID)).toBeNull();
  });

  it("renders nothing when there is no snapshot yet", () => {
    renderAlert(null);

    expect(screen.queryByTestId(ALERT_TEST_ID)).toBeNull();
  });

  it("renders nothing for an unrecognized status", () => {
    renderAlert({ status: "degraded" } as unknown as AgentRuntimeAvailability);

    expect(screen.queryByTestId(ALERT_TEST_ID)).toBeNull();
  });
});

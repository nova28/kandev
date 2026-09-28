import { execFileSync } from "node:child_process";
import fs from "node:fs";
import type { Page } from "@playwright/test";
import type { AgentRuntimeAvailability } from "@/lib/types/agent-runtime";

export type AgentRuntimeAvailabilitySnapshot = AgentRuntimeAvailability;

type E2EStoreWindow = Window & {
  __KANDEV_E2E_STORE__?: {
    getState: () => {
      setAgentRuntime: (snapshot: AgentRuntimeAvailabilitySnapshot | null) => void;
    };
  };
};

export async function setAgentRuntimeAvailability(
  page: Page,
  snapshot: AgentRuntimeAvailabilitySnapshot | null,
): Promise<void> {
  await page.evaluate((nextSnapshot) => {
    const store = (window as E2EStoreWindow).__KANDEV_E2E_STORE__;
    if (!store) throw new Error("E2E store bridge missing");
    store.getState().setAgentRuntime(nextSnapshot);
  }, snapshot);
}

export async function stubAgentRuntimeRestart(page: Page): Promise<() => number> {
  let restartRequests = 0;

  await page.route("**/api/v1/system/restart-capability", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ supported: true, mode: "supervisor", adapter: "supervisor" }),
    }),
  );
  await page.route("**/api/v1/system/info", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ boot_id: "e2e-stable-boot" }),
    }),
  );
  await page.route("**/api/v1/system/restart", (route) => {
    restartRequests += 1;
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ accepted: true, message: "accepted" }),
    });
  });

  return () => restartRequests;
}

/**
 * Finds the OS PID of the standalone agentctl process the e2e backend
 * launched as a direct child. `pgrep -P` is the primary lookup; when the
 * process tree scan finds nothing (for example, because the child forked
 * through an intermediate process on some platform), this falls back to the
 * PID the backend recorded in its structured log at launch.
 */
export function findAgentctlPid(backendPid: number, backendLogPath: string): number {
  try {
    const output = execFileSync("pgrep", ["-P", String(backendPid)], { encoding: "utf8" });
    const firstChildPid = Number(output.trim().split("\n")[0]);
    if (Number.isInteger(firstChildPid) && firstChildPid > 0) return firstChildPid;
  } catch {
    // pgrep exits with status 1 and no output when there are no matching
    // children; fall through to the log-based lookup.
  }

  const log = fs.readFileSync(backendLogPath, "utf8");
  const startLine = log
    .split("\n")
    .reverse()
    .find((line) => line.includes("agentctl process started"));
  const match = startLine?.match(/"pid":\s*(\d+)/) ?? startLine?.match(/\bpid[:=]\s*(\d+)/);
  const loggedPid = match ? Number(match[1]) : NaN;
  if (!Number.isInteger(loggedPid) || loggedPid <= 0) {
    throw new Error("could not resolve the standalone agentctl PID from pgrep or the backend log");
  }
  return loggedPid;
}

/** Kills the standalone agentctl process to simulate an unexpected exit. */
export function killAgentctl(pid: number): void {
  process.kill(pid, "SIGKILL");
}

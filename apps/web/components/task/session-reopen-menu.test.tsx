import { describe, expect, it } from "vitest";
import { shouldShowReopenStateIcon } from "./session-reopen-menu";

describe("shouldShowReopenStateIcon", () => {
  it("surfaces the icon for a background-running session (RUNNING + background)", () => {
    // The defect this fixes: a session whose foreground turn is idle while
    // background work runs previously showed no icon (state dropped). It must
    // now render — the shared background-running spinner, never a done check.
    expect(shouldShowReopenStateIcon("RUNNING", "background")).toBe(true);
  });

  it("keeps a generating RUNNING session icon-less (established silent affordance)", () => {
    expect(shouldShowReopenStateIcon("RUNNING", "generating")).toBe(false);
  });

  it("falls back to silence — not done — when a RUNNING substate is unknown", () => {
    // §req safe-defaults: an unknown substate on a live session must never
    // resolve to a done affordance. Silence (no icon) is the safe reading here.
    expect(shouldShowReopenStateIcon("RUNNING", null)).toBe(false);
    expect(shouldShowReopenStateIcon("RUNNING", undefined)).toBe(false);
  });

  it("keeps STARTING icon-less (still launching)", () => {
    expect(shouldShowReopenStateIcon("STARTING", null)).toBe(false);
  });

  it("ignores stale pending input while a session is still STARTING", () => {
    expect(shouldShowReopenStateIcon("STARTING", null, true, false)).toBe(false);
    expect(shouldShowReopenStateIcon("STARTING", null, false, true)).toBe(false);
  });

  it("keeps a plain waiting session icon-less when no input is pending", () => {
    // WAITING_FOR_INPUT also means an ordinary turn finished and the session is
    // ready for another prompt; it is not proof that the agent asked a question.
    expect(shouldShowReopenStateIcon("WAITING_FOR_INPUT", null)).toBe(false);
  });

  it("surfaces explicit pending input while waiting", () => {
    expect(shouldShowReopenStateIcon("WAITING_FOR_INPUT", null, true, false)).toBe(true);
    expect(shouldShowReopenStateIcon("WAITING_FOR_INPUT", null, false, true)).toBe(true);
    expect(shouldShowReopenStateIcon("WAITING_FOR_INPUT", "background")).toBe(true);
  });

  it("surfaces the icon for a pending prompt even mid-turn (still coarsely RUNNING)", () => {
    // A pending clarification / permission is actionable; it must not be masked
    // by the generating-RUNNING silence rule.
    expect(shouldShowReopenStateIcon("RUNNING", "generating", true, false)).toBe(true);
    expect(shouldShowReopenStateIcon("RUNNING", "generating", false, true)).toBe(true);
  });

  it("renders the existing icon for terminal / other states", () => {
    expect(shouldShowReopenStateIcon("COMPLETED", null)).toBe(true);
    expect(shouldShowReopenStateIcon("FAILED", null)).toBe(true);
    expect(shouldShowReopenStateIcon("CANCELLED", null)).toBe(true);
    expect(shouldShowReopenStateIcon("CREATED", null)).toBe(true);
  });

  // Review round 7, F3: this gate was never given a parkedOnBackgroundWork
  // parameter, so a parked session (WAITING_FOR_INPUT + a detached background
  // workload still live) rendered NO icon at all in this AC-51-named call
  // site, even though the icon-rendering call right below it already read
  // session.parked_on_background_work correctly.
  it("surfaces the icon for a parked WAITING_FOR_INPUT session (AC-51)", () => {
    expect(shouldShowReopenStateIcon("WAITING_FOR_INPUT", null, false, false, true)).toBe(true);
  });

  it("surfaces the icon for a RUNNING-then-parked session (AC-51)", () => {
    expect(shouldShowReopenStateIcon("RUNNING", null, false, false, true)).toBe(true);
  });

  it("keeps a plain waiting session icon-less when not parked", () => {
    expect(shouldShowReopenStateIcon("WAITING_FOR_INPUT", null, false, false, false)).toBe(false);
  });

  it("lets a pending prompt win over parked (needs-me always tops)", () => {
    expect(shouldShowReopenStateIcon("WAITING_FOR_INPUT", null, true, false, true)).toBe(true);
    expect(shouldShowReopenStateIcon("WAITING_FOR_INPUT", null, false, true, true)).toBe(true);
  });

  it("keeps STARTING icon-less even when parked", () => {
    expect(shouldShowReopenStateIcon("STARTING", null, false, false, true)).toBe(false);
  });
});

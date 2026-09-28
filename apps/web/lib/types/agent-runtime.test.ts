import { describe, expect, it } from "vitest";
import { isAgentRuntimeAvailability } from "./agent-runtime";

const OCCURRED_AT = "2026-09-29T00:00:00Z";

describe("isAgentRuntimeAvailability", () => {
  it("accepts available", () => {
    expect(isAgentRuntimeAvailability({ status: "available" })).toBe(true);
  });

  it("accepts recovering with reason and occurred_at", () => {
    expect(
      isAgentRuntimeAvailability({
        status: "recovering",
        reason: "agentctl_exited",
        occurred_at: OCCURRED_AT,
      }),
    ).toBe(true);
  });

  it("accepts unavailable with reason agentctl_exited", () => {
    expect(
      isAgentRuntimeAvailability({
        status: "unavailable",
        reason: "agentctl_exited",
        occurred_at: OCCURRED_AT,
      }),
    ).toBe(true);
  });

  it("accepts unavailable with reason relaunch_exhausted", () => {
    expect(
      isAgentRuntimeAvailability({
        status: "unavailable",
        reason: "relaunch_exhausted",
        occurred_at: OCCURRED_AT,
      }),
    ).toBe(true);
  });

  it("rejects an unknown status", () => {
    expect(isAgentRuntimeAvailability({ status: "degraded" })).toBe(false);
  });

  it("rejects recovering with an unrecognized reason", () => {
    expect(
      isAgentRuntimeAvailability({
        status: "recovering",
        reason: "relaunch_exhausted",
        occurred_at: OCCURRED_AT,
      }),
    ).toBe(false);
  });

  it("rejects a snapshot missing occurred_at", () => {
    expect(isAgentRuntimeAvailability({ status: "unavailable", reason: "agentctl_exited" })).toBe(
      false,
    );
  });

  it("rejects null and non-object values", () => {
    expect(isAgentRuntimeAvailability(null)).toBe(false);
    expect(isAgentRuntimeAvailability("unavailable")).toBe(false);
    expect(isAgentRuntimeAvailability(undefined)).toBe(false);
  });
});

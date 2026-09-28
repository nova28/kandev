export type AgentRuntimeAvailabilityStatus = "available" | "recovering" | "unavailable";

export type AgentRuntimeAvailabilityReason = "agentctl_exited" | "relaunch_exhausted";

export type AgentRuntimeAvailability =
  | { status: Extract<AgentRuntimeAvailabilityStatus, "available"> }
  | {
      status: Extract<AgentRuntimeAvailabilityStatus, "recovering">;
      reason: Extract<AgentRuntimeAvailabilityReason, "agentctl_exited">;
      occurred_at: string;
    }
  | {
      status: Extract<AgentRuntimeAvailabilityStatus, "unavailable">;
      reason: AgentRuntimeAvailabilityReason;
      occurred_at: string;
    };

/**
 * Validates an untrusted snapshot (WS payload or boot-payload hydration)
 * against the known status/reason combinations. An older frontend build must
 * not crash or overwrite the current snapshot on a status a newer backend
 * introduces later; the caller keeps its previous value instead.
 */
export function isAgentRuntimeAvailability(value: unknown): value is AgentRuntimeAvailability {
  if (typeof value !== "object" || value === null) return false;
  const { status, reason, occurred_at: occurredAt } = value as Record<string, unknown>;

  if (status === "available") return true;
  if (status === "recovering")
    return reason === "agentctl_exited" && typeof occurredAt === "string";
  if (status === "unavailable") {
    return (
      (reason === "agentctl_exited" || reason === "relaunch_exhausted") &&
      typeof occurredAt === "string"
    );
  }
  return false;
}

---
status: draft
system: agents
created: 2026-10-26
owners:
  - kandev
---

# Sandboxed session requirements

## Overview

Extends [Sandbox policy requirements](sandbox-policies.md), which own every behavior not restated here. Its terms apply. [Design](../system-design/sandbox-session-conformance.md). Related: [guarded surface](sandbox-guarded-session-surface.md), [macOS support](sandbox-macos-support.md).

This file defines when Kandev may call a session sandboxed. Individual mechanisms are owned elsewhere; this file owns the end-to-end contract and the release gate that proves it.

Every item below ends in one of three outcomes: the guarantee holds, the launch is refused with a reason, or the session shows as degraded with a reason. A fourth outcome, a launch that proceeds while an item is silently unmet, is a defect in every case. A refusal never stops the backend from serving other work.

## Requirements

### REQ-AGENTS-SANDBOX-012: Sandboxed session

**Intent:** An operator who selects a sandbox can rely on the session being confined as promised, or is told exactly why not, on every supported host and agent.

- **AC-AGENTS-SANDBOX-012.1:** When the sandbox feature is enabled and sandbox setup is unavailable on the host, Kandev shall keep serving, shall refuse each launch that requires a sandbox with class `sandbox_runtime_unavailable` and a reason code, and shall show that reason in Settings availability.
  Reason codes are a closed set: `state_root_denied` (the data directory is under a temporary root or traverses a symlink), `state_root_unusable`, `provider_missing`, `provider_unavailable`, `host_unsupported`. Unsandboxed launches are unaffected. The backend never exits because of this condition.
- **AC-AGENTS-SANDBOX-012.2:** When a sandboxed agent exits before its protocol handshake completes, the session error shall name a bounded cause, not `unknown`.
  Causes are a closed set: `agent_binary_denied`, `agent_credentials_missing`, `agent_exited`, `sandbox_denied_other`. The cause is derived from the exit status and at most the last 2 KiB of the agent's stderr after redaction; stderr text itself is never stored or sent to the client. If no cause can be derived the cause is `agent_exited`.
- **AC-AGENTS-SANDBOX-012.3:** A session shall have its policy pinned at launch; a later policy, binding, or executor change shall not alter a running attempt, and a child task shall receive grants that are a subset of its parent's. A child request for a wider grant shall be refused with `sandbox_outside_ceiling` and no child created.
  The comparison is on canonical grants after path resolution. Two concurrent child creations from one parent are evaluated independently against the same pinned parent revision.
- **AC-AGENTS-SANDBOX-012.4:** In a guarded session, the Kandev MCP tool surface shall be enforced by the server for every tool class, and no mutating tool shall be auto-approved. This criterion is satisfied by REQ-AGENTS-SANDBOX-009 and -010 and adds only the conformance canary in AC-AGENTS-SANDBOX-012.13.
- **AC-AGENTS-SANDBOX-012.5:** Agent-facing MCP routes and the Git credential broker shall accept only the per-session token of the calling instance, and a request carrying another session's token, no token, or a token of a finished session shall be rejected with status 401 and no side effect.
  The token is bound to one agentctl instance and one attempt generation, is not logged, and is revoked when the attempt ends. A replacement attempt receives a new token.
- **AC-AGENTS-SANDBOX-012.6:** Executor REST routes on agentctl shall require an authenticated caller that holds the instance credential, and agentctl shall offer a loopback-only bind that Local and Worktree sandboxed executors use by default. A request without the credential shall be rejected with status 401 before any handler runs.
  Today `server.host` defaults to `0.0.0.0`; the default is unchanged for unsandboxed use. When a sandboxed attempt starts on an instance bound to a non-loopback address, the session is refused with `sandbox_bind_exposed`.
- **AC-AGENTS-SANDBOX-012.7:** Managed Git operations performed by Kandev on a sandboxed task's repository shall run no code supplied by that repository, unless the repository explicitly opts in. This criterion is satisfied by REQ-AGENTS-GITISOLATION-001 to -003 and adds the canary in AC-AGENTS-SANDBOX-012.13.
- **AC-AGENTS-SANDBOX-012.8:** Every process that Kandev starts on behalf of a sandboxed session (setup and cleanup scripts, dev processes, language servers, native terminals) shall either run inside the session's pinned sandbox or be shown in Execution details as `unsandboxed` with its kind.
  A process that starts while its sandbox state cannot be determined is shown as `unsandboxed`, never as confined. Labels are per process kind and appear before the process produces output. A native terminal on a host where `sandbox_transport_unsupported` applies is refused unless the operator started it explicitly from a labelled control.
- **AC-AGENTS-SANDBOX-012.9:** Preview content produced by a sandboxed session shall be served from an origin different from the Kandev UI origin, and a preview page shall not be able to read Kandev UI cookies or storage.
  When no separate origin can be provided, the preview is disabled with reason `preview_origin_unavailable` instead of being served from the UI origin. The origin is derived per task and never reused across tasks.
- **AC-AGENTS-SANDBOX-012.10:** The child environment shall contain only variables named by the policy's environment allowlist plus the fixed minimal set the provider documents, and a variable that is not allowed shall be absent, not empty.
  Matching is exact on name. Values are never written to logs or reports; reports list names only. A name on the allowlist that has no value on the host is reported as `missing`, not as an error.
- **AC-AGENTS-SANDBOX-012.11:** Each supported agent shall declare its credential requirement in a registry of kind, host platform, and delivery path, and a sandboxed launch of an agent whose requirement cannot be met on the host shall be refused with `sandbox_credentials_unsupported` and the agent and platform named.
  Kinds are a closed set: `env_key`, `file_in_agent_state`, `host_keychain`. `host_keychain` is unsupported under a sandbox on darwin (AC-AGENTS-SANDBOX-011.3), so the registry entry for an agent that only logs in through the keychain produces the refusal on that host. An agent absent from the registry is refused with `sandbox_credentials_unknown`. Credential values never cross into the registry, the report, or an error.
- **AC-AGENTS-SANDBOX-012.12:** For a remote executor, Kandev shall either run a host probe that proves confinement works on that host before the first sandboxed launch, or report the executor as `sandbox_unsupported` in Settings and refuse sandboxed launches with `sandbox_host_unsupported`.
  There is no third state. A probe that has not completed is `sandbox_unsupported`. Until a probe exists for a remote executor type, that type is reported unsupported.
- **AC-AGENTS-SANDBOX-012.13:** The release gate shall be a conformance suite that runs the operator journey with a real agent on a real host and, for every escape named in this file, an escape probe paired with a control proving the probe can detect the escape. A probe result is valid only if its control detected the escape.
  The suite design is in the system design. A canary that cannot show its control detecting the escape is reported `invalid`, which fails the run. The suite prints probe identifiers and outcomes only; it never prints a real file name outside its fixture root, a credential, or a token.
- **AC-AGENTS-SANDBOX-012.14:** Execution details shall show for the current attempt which of AC-AGENTS-SANDBOX-012.1 to .12 hold, degrade, or are unsupported, each with its reason code, and shall never show a session as sandboxed while any item is unmet.
  The summary state is `confined` only when every applicable item holds. Otherwise it is `degraded` with the unmet items, or `refused` before launch. State is derived from the pinned attempt, not from current settings.

## Boundaries and defaults

- **Ordering.** Items are evaluated in the order of the AC numbers 012.1 to 012.12 at launch admission. The first unmet item that refuses a launch is the one reported; all unmet items are recorded on the attempt. A tie within one item is broken by reason code ascending.
- **Idempotency.** Retrying a refused launch re-runs admission against current state and creates a new attempt; a refusal never mutates policy or bindings.
- **Concurrency.** Two launches on one executor are admitted independently. A token or origin is allocated per attempt, so concurrent attempts never share one.
- **Defaults.** `features.sandboxPolicies` is off in every shipped profile. Remote executors are `sandbox_unsupported`. Missing registry entries refuse. Missing reason data is `unknown` only in the UI for sessions that predate this requirement.

## Out of scope

- Windows hosts, and remote executors beyond the explicit `sandbox_unsupported` state (follow-up: a host probe per remote executor type).
- Network egress beyond the policy's exact destinations; owned by REQ-AGENTS-SANDBOX-005.
- Approvals on macOS (REQ-AGENTS-SANDBOX-011 excludes them).
- Keychain-based agent login under a macOS sandbox. This file refuses it; a follow-up would add a broker that supplies a short-lived token without granting the keychain.

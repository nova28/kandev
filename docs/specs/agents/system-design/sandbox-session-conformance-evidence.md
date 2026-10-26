---
status: draft
system: agents
created: 2026-10-26
requirements:
  - REQ-AGENTS-SANDBOX-012
---

# Sandboxed session evidence, preview and release-record design

Companion to the [sandboxed session system design](sandbox-session-conformance.md), which owns the requirement map and the shared vocabulary. This file owns the child grant request, the evidence recompute rules, the journey's expected states, the darwin result mapping, the preview ticket lifecycle and the release-record hash.

## Requirement-to-mechanism map

| AC | Mechanism | Work order |
| --- | --- | --- |
| 012.14 | Evidence recompute, journey expected states | 24, 20 |
| 012.13 | Darwin result mapping, `scope_hash` algorithm | 20, 23 |
| 012.9 | Preview transition barrier, route shape, ticket lifecycle | 16 |
| 012.15 | `sandbox_grants` request and effective guard | 22 |

## Child grant request (012.15)

The guard of record of a guarded task is a resolved policy revision plus an optional `narrowing` stored in its `sandbox_task_guards` row. `sandbox_grants` is accepted on the REST create-task request and the MCP create-task tool (one request type). Shape: an object with optional keys `grants` (array of `{role, access}`), `environment_keys` (array of strings), `network_destinations` (array of strings) and `provider` (string identity, equality only). A key that is absent or `null` is inherited; an array, even `[]`, is explicit, `[]` being the narrowest; an unknown key, a wrong type or an empty `provider` string is a 400 `sandbox_grants_invalid` with no row. A request whose creation has no ceiling (no guarded parent and no guarded caller task) is a 400 `sandbox_grants_without_guard` with no row.

The ceiling is the narrowest of the parent guard and the MCP caller task guard, the sources `InheritTaskGuard` already reads; both are kept. The effective child policy is the child's bound resolved policy narrowed by the stored `narrowing` (a dimension with no narrowing is taken as bound); the containment test of `checkParentGuard` applies to the effective policy, so a child whose bound policy is wider than the ceiling but whose narrowing brings it inside is allowed, and one still outside is refused `sandbox_outside_ceiling`. At launch the effective policy is what compiles. An explicit empty request is created; if the provider cannot compile the resulting policy the launch is refused with the existing policy error and a refusal record, never silently widened. Lock order for the insert transaction is workspace, step, then the parent task row, then the new row; two task rows are locked in ascending id.

## Evidence recompute (012.14)

Runtime items read these records on `evidence_restored` and `attempt_admitted`: 012.2 the persisted `launch_cause` and handshake record; 012.8 the persisted process labels; 012.9 the preview state in core; 012.10 the persisted environment report; 012.5 the `token` field of `/identity` and the persisted revocation record. Only 012.5 needs the instance; if the instance does not report a token state it stays `unknown` `evidence_unavailable`. A persisted revocation wins over the instance report. Events are applied in per-attempt sequence order under one mutex, so a delayed `evidence_lost` that arrives after `evidence_restored` carries a lower sequence and is dropped.

## Journey expected states (012.14)

The harness asserts the summary and indicator after each step on both hosts. The journey fixture has no setup, cleanup or custom script and starts no dev process before the user terminal step.

| After step | Summary | Indicator |
| --- | --- | --- |
| fault: state root through a symlink or temp root | `refused` (`state_root_denied`) on the second instance | failed |
| fault: agent binary outside the grant | `degraded` (012.2 `agent_binary_denied`), status `failed` | failed |
| prompt reply read, native terminal, MCP tool call, commit and push, denial, approval | `confined` | shield |
| user terminal opened | `degraded` (012.8 `process_unsandboxed`) | warning |
| restart and resume | equals the replacement attempt's own items; items other than 012.2 and 012.8 equal the predecessor's | per the matrix |
| cancel | frozen at its last value | not the shield (status not active) |

On macOS the native terminal step is `passed` when `sandbox_transport_unsupported` is returned and leaves the summary `confined`; the denial step is `not_applicable` `denial_report_unsupported_host` (AC-AGENTS-SANDBOX-011.7). The effective-flags assertion before step one replaces a separate enable step.

## Darwin result mapping (012.13)

The AC-AGENTS-SANDBOX-011.11 canaries map as follows. A `denied` canary with a valid control is `contained`; a sandboxed run that returns `allowed` is `escaped`; `control_failed` is `invalid`; `skipped` is `not_applicable` `host_os` only for a host-level reason, otherwise `error`; a crash or timeout is `error`. The `allowed` canaries (permitted write, MCP relay route, ACP stdio round trip, ownership) are wrapped as liveness checks `L_<name>` with states `holds`, `broken` or `error`; `broken` or `error` fails the cell, and a `broken` ownership check stops the run. Liveness checks are counted apart from canaries.

## Preview (012.9)

**Route shape.** The preview origin keeps the existing proxy layout below its root: `<task-token>.<baseDomain>/port-proxy/:sessionId/:port/*path`; the session and port come from the path and the session must belong to the origin's task. **Capability.** With authentication on, the existing port-proxy capability cookie check applies on the preview origin after ticket exchange. With authentication off no capability is minted, so the 256-bit task token in the host name is the only gate, which is the stated trust boundary.

**Transition barrier.** UI-origin preview is refused for a task by a durable test, "the task has a guard or any attempt row", checked on every HTTP request and WebSocket upgrade, not by the origin row. Creating the origin row and the guard or attempt happen in one transaction; the row's failure leaves previews disabled (503 `preview_origin_unavailable`) and does not refuse the launch. A WebSocket registers in the task's socket set before the check at upgrade completion; a post-commit hook closes every registered UI-origin socket of the task, so a socket upgraded before the commit is closed and one upgraded after it fails the check.

**Ticket lifecycle.** Tickets are stored as a SHA-256 digest with task id and expiry (60 s) and consumed by one delete-returning statement, so concurrent exchanges have one winner and the rest get 403. The exchange creates a preview session stored as a digest with task id, user and expiry (8 h); the cookie carries the raw value. Logout, deletion of the user and a change of the authenticated user delete that user's preview sessions; an expired row is rejected and pruned on use.

## Release-record hash (012.13)

`scope_hash` is the SHA-256 of the concatenation, for each path listed in `conformance-scope.txt` in ascending byte order of the UTF-8 path, of `path`, a NUL byte, the lowercase hex SHA-256 of the file bytes, and a newline. Paths are repository-relative with forward slashes; file bytes are hashed as stored, with no line-ending change. The manifest itself is one of the listed paths. A listed path that is missing, or a duplicate path, makes `sandbox-conformance` fail with an error and no hash.

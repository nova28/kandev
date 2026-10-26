---
status: draft
system: agents
created: 2026-10-26
requirements:
  - REQ-AGENTS-SANDBOX-012
---

# Sandboxed session evidence and release-record design

Companion to the [sandboxed session system design](sandbox-session-conformance.md), which owns the requirement map and the shared vocabulary. This file owns the evidence recompute rules, the journey's expected states, the darwin result mapping and the release-record hash.

## Requirement-to-mechanism map

| AC | Mechanism | Work order |
| --- | --- | --- |
| 012.14 | Evidence recompute, journey expected states | 24, 20 |
| 012.13 | Darwin result mapping, `scope_hash` algorithm | 20, 23 |

## Evidence recompute (012.14)

Runtime items read these records on `evidence_restored` and `attempt_admitted`: 012.2 the persisted `launch_cause` and handshake record; 012.8 the persisted process labels; 012.9 the persisted preview state; 012.10 the persisted environment report; 012.5 the `mcp_token_fingerprint` field of the instance `/identity` and core's own-revocation record. Only 012.5 needs the instance; if it cannot be read the item stays `unknown` `evidence_unavailable`. A recorded own-revocation wins over the instance report. Events are applied strictly in receipt order under one mutex; a poll-sourced event whose counter is lower than the highest applied poll counter is dropped (below), and a stream-sourced event carries no counter and is applied where it arrives.

Core polls each live attempt's instance `/identity` every 10 s (Task 24 owns the probe); every poll carries a per-attempt counter taken when the request is sent, and a result whose counter is lower than the last applied poll is dropped, so a slow poll never overrides a newer one. Each attempt tracks a set of failed sources, initially empty: `identity` (the poll) and `stream` (the provider evidence stream). `evidence_lost` fires when a source is added to an empty set, after 30 s of consecutive poll failures (adds `identity`) or when the stream ends before `attempt_ended` (adds `stream`), and turns runtime items `unknown` `evidence_unavailable`; a source added to a non-empty set fires nothing. A successful poll removes `identity` and a resumed stream removes `stream`; `evidence_restored` fires only when a removal empties the set and recomputes each runtime item from the sources above, except an item scoped by an `evidence_gap`, which stays `unknown` `evidence_unavailable`. While the set is non-empty runtime items stay `unknown` `evidence_unavailable`.

`evidence_gap` is a latch stored on the attempt row, not gated by the summary flag, and cleared only by `attempt_ended`. It is set (a) at the `evidence_restored` that empties the failed-source set, before the recompute, for each runtime item whose state changed while the set was non-empty in a way that cannot be recomputed from the persisted records, scoped to those items, and (b) at the moment `features.sandboxSessionSummary` is turned off, for every live attempt, scoped to every runtime item, since nothing is persisted while the flag is off. While it is set, `evidence_restored` and a flag turned back on leave the scoped items `unknown` `evidence_unavailable`.

## Journey expected states (012.14)

The harness asserts the summary and indicator after each step on both hosts. The journey fixture has no setup, cleanup or custom script and starts no dev process before the user terminal step.

| After step | Summary | Indicator |
| --- | --- | --- |
| fault: state root through a symlink or temp root | `refused` (`state_root_denied`) on the second instance | failed |
| fault: agent binary outside the grant | `degraded` (012.2 `agent_binary_denied`), status `failed` | failed |
| prompt reply read, native terminal, MCP tool call, commit and push, denial, approval (all before the preview step) | `confined` | shield |
| preview served | `degraded` (012.9 `preview_same_origin`) | warning |
| user terminal opened | `degraded` (012.8 `process_unsandboxed`, sticky) | warning |
| restart and resume | equals the replacement attempt's own items: 012.2 `not_applicable` `clean_handshake`, 012.8 and 012.9 `holds` (a replacement starts with no preview served and no user process), every other item equal to the predecessor's, so `confined` when no item degraded | the shield when `confined` with an active status, else per the matrix |
| cancel | frozen at its last value | not the shield (status not active) |

The terminal step runs after approval so that the `confined` rows are asserted before any step that degrades the summary.

On macOS the native terminal step is `passed` when `sandbox_transport_unsupported` is returned and leaves the summary `confined`; the denial step is `not_applicable` `denial_report_unsupported_host` (AC-AGENTS-SANDBOX-011.7). The effective-flags assertion before step one replaces a separate enable step.

## Darwin result mapping (012.13)

The AC-AGENTS-SANDBOX-011.11 canaries map as follows. A `denied` canary with a valid control is `contained`; a sandboxed run that returns `allowed` is `escaped`; `control_failed` is `invalid`; `skipped` is `not_applicable` `host_os` only for a host-level reason, otherwise `error`; a crash or timeout is `error`. The `allowed` canaries (permitted write, MCP relay route, ACP stdio round trip, ownership) are wrapped as liveness checks `L_<name>` with states `holds`, `broken` or `error`; `broken` or `error` fails the cell, and a `broken` ownership check stops the run. Liveness checks are counted apart from canaries.

## Release-record hash (012.13)

`scope_hash` is the SHA-256 of the concatenation, for each path listed in `conformance-scope.txt` in ascending byte order of the UTF-8 path, of `path`, a NUL byte, the lowercase hex SHA-256 of the file bytes, and a newline. Paths are repository-relative with forward slashes; file bytes are hashed as stored, with no line-ending change. The manifest itself is one of the listed paths. A listed path that is missing, or a duplicate path, makes `sandbox-conformance` fail with an error and no hash.

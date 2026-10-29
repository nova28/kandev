---
id: "18-environment-listing"
title: "Host environment listing (ADMISSION-002)"
status: pending
wave: 8
depends_on:
  - "11"
plan: "plan.md"
requirements:
  - REQ-EXECUTORS-PLUGIN-ADMISSION-002
acceptance_criteria:
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.1
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.2
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.3
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.4
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.5
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.6
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.7
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.8
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.9
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.10
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.11
system_design:
  - ../../specs/executors/system-design/remote-executor-plugin-environment-listing.md
---

# Task 18: Host environment listing

## Summary

Add the `environment_listing` registry row (no `Requires` edge), the lease-bound `Host.ListExecutorEnvironments` callback with its check order and per-installation token bucket, the lifecycle handler that reads the existing plugin inventory scan, the phase-to-state mapping, the optional SDK interface, and the fixture provider's use of it. Depends on work order 11 because the mapping table includes the four placement phase constants it creates and the fixture refuses a launch through `provision_refusal`.

## Size

S: ~300 source, ~500 test lines.

## In scope

- Registry row; `TestRegistryHoldsTheClosedNameSetAtVersionOne` extended to 17 names; dependency-closure assertion.
- Additive proto (`ListExecutorEnvironments*`, `ExecutorEnvironmentEntry`) and the optional `pluginsdk.ExecutorEnvironmentLister` interface.
- Handler in `internal/plugins` (lease, rate limit, handler lookup) delegating to the new optional `ExecutorEnvironmentListingHandler`, implemented by `*lifecycle.Manager`, which decides the capability from the launch's callback registration (name part of `RequiredCapabilities`), validates, and reads through `ListExecutorsRunningPluginRemote`. `authorize`, `activeExecutorProviderOperation`, and the request context are unchanged.
- `pluginExecutorListedState` and its exhaustive go/parser phase-constant test over every non-test file of the lifecycle package (including `creating`); the 60-second warning limiter for skipped rows.
- Metrics `executor_plugin_environment_listing_total` and `executor_plugin_environment_listing_skipped_total`.
- Fixture provider lists during its own Provision and sees its own record flagged `is_calling_environment`.

## Out of scope

- Any write, lock, or reservation; a per-node host claim; an administrator view; a listing callable outside an operation.

## Acceptance

- A provider with the capability pages its own records in byte order, once each, with the mapped state, and a full last page has no cursor.
- A provider without it gets `unsupported` with zero store reads; an ended lease gets the transport error with zero reads and zero tokens.

## Failing-first tests

- Check order: lease, then rate (11 calls at one fake instant, refill after 100 ms), then capability (launch registration), then validation, with a counting store; an operation with no registration is `unsupported`; no handler configured is `unavailable`.
- Cursor paging with a record inserted between pages (SQLite and PostgreSQL scans), page_size 0 / negative / above 100, malformed and over-long cursor.
- Foreign installation and provider key rows; several rows with one `environment_id`; `absent` rows; undecodable envelope and empty `environment_id` rows.
- Phase mapping table covering every `pluginExecutorPhase*` constant (go/parser), including `ready` with each `last_inspection_state`.
- 256 KiB size cut with `include_state` and 20 KiB and 32 KiB states, asserting `proto.Size(response) <= 262144` (100 entries at 32 KiB); store error, unwired store, and 5 second bound give `unavailable`; a cancelled caller gets transport cancellation and no response; `ready` with an unrecognised inspection state is `cleaning`; `creating` is `starting`; registry test that no `IntroducedIn` is later than the build version.
- The listing never delays a concurrent launch (blocking-store unit test).
- Fixture provider lists during its own Provision and sees its own record flagged.

## Verification

Run from the repository root:

```bash
(cd apps/backend && go test -race -trimpath ./internal/plugins/... ./internal/agent/runtime/lifecycle/... ./cmd/plugin-fixture -count=1)
```

```bash
(cd pluginsdk && go test -race -trimpath ./... -count=1)
```

## Likely files and risks

- `apps/backend/internal/plugins/hostcaps/registry.go` and `registry_test.go` (closed-name-set test)
- `apps/backend/internal/plugins/executor_provider_host.go`
- `apps/backend/internal/agent/runtime/lifecycle/executor_plugin_callbacks.go` and a new `executor_plugin_listing.go`
- `pluginsdk/proto/kandev/plugin/v1/plugin.proto`, `pluginsdk/executor_provider.go`
- Risk: the closed-name-set test changes in the same commit as the registry row.
- Risk: the listing reuses the launch callback registration; any change to how work order 11 registers (or does not register) the placement continuation changes which dispatches may list (open question Q-O5).

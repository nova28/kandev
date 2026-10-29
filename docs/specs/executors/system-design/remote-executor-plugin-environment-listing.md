---
status: draft
system: executors
requirements:
  - REQ-EXECUTORS-PLUGIN-ADMISSION-002
---

# Remote executor plugins: host environment listing system design

## Purpose and boundaries

One read-only host callback in the provider SDK, gated by one registry capability. It reads the existing `plugin_executor`
envelope rows through the plugin inventory store and adds no table, DDL, or write. Capability gating comes from the
[versioning design](remote-executor-plugin-capability-versioning.md); the admission scope lock and reservations of the
[admission design](remote-executor-plugin-admission.md) are not touched. Names marked **new** do not describe existing APIs.

## Requirement mapping

| Requirement | Sections |
| --- | --- |
| REQ-EXECUTORS-PLUGIN-ADMISSION-002 | Capability, Callback, State mapping, Paging and budget, Concurrency |

## Capability

**New** registry row `environment_listing` (version 1, requires `workspace_descriptor`) in `internal/plugins/hostcaps`, with a
manifest validator test and a dependency test. Adding it is a table row, never a contract version change (contract stays 1).
The host callback checks the negotiated set recorded for the calling dispatch generation (the same source as the echo gate)
and answers `unsupported` before any store call (AC 002.1).

## Callback

**New** `Host.ListExecutorEnvironments(ListExecutorEnvironmentsRequest) returns ListExecutorEnvironmentsResponse` in
`pluginsdk/proto/kandev/plugin/v1/plugin.proto`, additive. The request holds `provider_key`, `page_size`, `cursor`,
`include_state`; the response holds entries (`environment_ref`, `resource_id`, `state`, `generation`, `provider_state`) and
`next_cursor`. The installation is taken from the authenticated plugin call context, never from a request field (AC 002.2).
The handler lives beside the other `Host` handlers in `internal/plugins` and reads through a **new** optional interface on the
plugin inventory store, `ListPluginExecutorEnvironments(installation, providerKey, afterRef, limit)`; a store that lacks it
makes the callback `unavailable` (fail closed).

## State mapping

A pure function maps the envelope phase to the listed state of AC 002.6, with a default branch of `cleaning`, and a table test
enumerates every phase constant of the lifecycle package so a new phase fails the test until it is mapped.

## Paging and budget

The store query orders by environment reference (byte order) and selects `ref > cursor` with `LIMIT page_size`; the cursor is
the base64 of the last reference and is validated for length and charset. SQLite and PostgreSQL use the same predicate. The
page is read in one statement (one snapshot per page, AC 002.5). The encoded response is measured before sending and a page
over 256 KiB is refused `unavailable` (20 entries at the 12 KiB state maximum stays under it, so `include_state` pages cannot
exceed it with a conforming provider).

## Concurrency

The query holds no lock beyond a statement. A launch, a settle, or a cancel racing the read changes only which page sees the
record; no writer waits for it and it waits for none (AC 002.9). The rate limiter is an in-memory token bucket per installation,
checked before the capability check's store read.

## Failure and interleavings

| Interleaving | Required outcome | Test (level) |
| --- | --- | --- |
| A record is created between page 1 and page 2 | shown at most once; never duplicated | store test, SQLite and PostgreSQL |
| A record settles absent while a page is being read | listed or not listed, never half-listed | store race test |
| A launch commits its operation record during the read | no lock taken; the launch is not delayed | lifecycle unit with a blocking store |
| Foreign installation or provider key | `not_found` or empty; never another installation's record | store and handler test |
| Provider without the capability calls | `unsupported`, zero store reads | handler test with a counting store |
| Unknown phase | listed `cleaning` | mapping table test |
| Over-budget page, store error | `unavailable`, no partial page | handler test |
| 11 calls inside one second | the 11th is `rate_limited`, no store read | handler test with a fake clock |

## Observability

`executor_plugin_environment_listing_total` (`outcome`: ok, unsupported, rate_limited, unavailable). No installation or
environment identifier is a label.

## Security

The callback exposes only opaque references, states, and the provider's own recorded state, to the owning installation. It is
unavailable to any account, route, or other plugin.

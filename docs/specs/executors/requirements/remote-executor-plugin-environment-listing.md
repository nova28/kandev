---
status: draft
system: executors
created: 2026-10-29
owners:
  - kandev
---

# Remote executor plugins: host environment listing requirements

## Overview

A worker-pool provider decides where to place a launch. A retained or half-created environment that the provider has no
marker for yet still occupies a worker, and the provider cannot see it. This document adds one opt-in
[capability](remote-executor-plugin-capability-versioning.md), `environment_listing`, with which a provider reads the
non-terminal environments the host holds for its own installation. It belongs to the admission and placement slice and
complements, and does not replace, the admission hook of [admission](remote-executor-plugin-admission.md). A provider that
does not require the capability sees no change.

## Terminology

- **Host environment record:** The host's `plugin_executor` record of one task environment (the envelope in `executors_running.metadata`).
- **Listed state:** A derived, closed value describing a record for the provider: `placing`, `starting`, `ready`, `retained`, or `cleaning`.
- **Provider key:** The `executor_providers[]` key of an installation that declares several providers.

## Requirements

### REQ-EXECUTORS-PLUGIN-ADMISSION-002: Host environment listing for provider admission

**Intent:** A provider can count every environment the host believes is live on its installation, so a placement decision does not miss an environment that has no provider-side marker yet.

#### Acceptance criteria

- **AC-EXECUTORS-PLUGIN-ADMISSION-002.1:** When a provider requires `environment_listing`, the host shall serve a paged, read-only host callback `ListExecutorEnvironments` to that installation. A call from an installation whose negotiated set (for the calling dispatch generation) lacks the capability shall be refused `unsupported`, and the host shall read nothing for it.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.2:** The callback shall return only host environment records of the calling installation and the requested `provider_key` (omittable only when the installation declares one provider; an unknown key is `not_found`). It shall never return a record of another installation, and shall return no record that has settled absent.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.3:** Each entry shall carry the host environment reference, the provider resource ID (the empty string when none is recorded yet), the listed state, the environment generation, and the provider state recorded for the environment only when the request sets `include_state`. An entry shall carry no secret reference, profile value, admission identity, reservation, scope, or session or task content.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.4:** Entries shall be ordered by host environment reference in byte order. A page shall hold at most 100 entries (20 when `include_state` is set), the default page size is that maximum, a larger request is clamped, and a request of zero or less is `invalid_request`. The cursor is opaque, at most 256 bytes; an empty cursor starts the read, and an absent next cursor ends it. A malformed or foreign cursor is `invalid_request`.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.5:** Each page shall be read in one consistent store read. Across pages there is no snapshot: a record created or settled between two pages can be missed or shown once, and no record shall appear twice in one traversal, because the cursor is the last reference returned and the next page holds only strictly greater references.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.6:** The host shall map a record to a listed state from its envelope phase: a record in `placement_pending`, `cancelling`, `placement_failed`, or `placement_timeout` is `placing`; `allocating` and `creating` are `starting`; a live ready environment is `ready`; a stopped environment the host retains is `retained`; `cleanup_pending` is `cleaning`. A phase outside this mapping shall be listed `cleaning`, never omitted, so an unknown record still counts as occupied.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.7:** A response over 256 KiB, or a store failure, shall fail the call `unavailable` with no partial page; the host never truncates silently. An installation with no record gets an empty page and no next cursor.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.8:** The host shall limit the callback to 10 calls per second per installation (a token bucket of burst 10, held in memory and reset by a backend restart) and shall refuse the excess `rate_limited` without a store read.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.9:** The listing shall change no host record and take no admission scope lock, no recovery claim, and no environment lock. It is advisory for the provider: it grants no reservation, and a launch the host admits after a listing can still collide with the provider's own fence, which stays the provider's authority.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.10:** A provider that is disabled, unavailable, or at a different negotiated version shall get `unavailable`, and the host shall keep launching without the callback.

## Out of scope

- A per-node claim written by the host: the node is a provider concept, and the host records none.
- A cross-installation or administrator view: the administrator inventory read of REQ-EXECUTORS-PLUGIN-OPS-001 stays the operator surface.
- Host-side enforcement of per-node capacity.

## Implementation plans

- [Native worker slices 2 and 3](../../../plans/executor-plugin-native-workers-slices-2-3/plan.md)

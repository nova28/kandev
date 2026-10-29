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
non-terminal environments the host holds for its own installation and provider key, from inside a host-admitted provider
operation (in practice its `ProvisionExecutorEnvironment`). It belongs to the admission and placement slice and
complements, and does not replace, the admission hook of [admission](remote-executor-plugin-admission.md). A provider that
does not require the capability sees no change.

Relationship to the closed capability set: the registry of the capability-versioning requirements pins 16 names
(AC-EXECUTORS-PLUGIN-CAPABILITY-001.2). This requirement adds the 17th, `environment_listing`, and extends that set; the
slice-1 document is not edited by this change, so the extension is recorded here and the registry test is updated in the
same change as the registry row.

## Terminology

- **Host environment record:** The host's `plugin_executor` record of one task environment (the envelope in `executors_running.metadata`, one row per `executors_running.session_id`).
- **Host environment reference:** The envelope `environment_id` (a host-generated UUID of 36 bytes). It identifies the task environment; several rows can carry the same reference.
- **Calling operation:** The host-admitted provider operation whose `ExecutorProviderRequestContext` (with its `dispatch_generation`, the per-dispatch counter) authorizes the call.
- **Listed state:** A derived, closed value describing a record for the provider: `placing`, `starting`, `ready`, `retained`, or `cleaning`. Every listed state counts as occupied; none is a promise that the provider's resource still exists.
- **Settled absent:** A record whose envelope `phase` is `absent`. It is never listed.

## Requirements

### REQ-EXECUTORS-PLUGIN-ADMISSION-002: Host environment listing for provider admission

**Intent:** A provider can count every environment the host believes is live on its installation, so a placement decision does not miss an environment that has no provider-side marker yet.

#### Acceptance criteria

- **AC-EXECUTORS-PLUGIN-ADMISSION-002.1:** The host shall serve the paged, read-only host callback `ListExecutorEnvironments` only inside a host-admitted provider operation that the lifecycle manager holds an active callback registration for (today the `ProvisionExecutorEnvironment` operation of a launch, registered by `registerPluginExecutorCallbacks`; any other operation, including attach, recover, inspect, destroy, and a placement poll, has no registration and receives the error `unsupported`). The request shall carry the operation's `ExecutorProviderRequestContext`. The lease check is that of `CheckpointExecutorResource` and `ReportExecutorProgress`: a missing, incomplete, expired, stale, or ended lease fails with the same transport error those callbacks return. The ownership check differs by design: the listing applies the in-memory registration checks of those callbacks (the registration exists for the lease's execution ID and matches its task, session, environment, operation ID, plugin, installation, provider key, contract version, and ownership generation) and does not repeat their session and environment ownership store reads, because it returns only the calling installation's records and AC 002.9 permits one store statement. The installation and the provider key of every read shall be those of the lease context; the request has no field for either. The capability gate holds when the registered launch's provider requirements (`RequiredCapabilities`, the set `negotiatedCapabilities` records as `name@version`) contain an entry whose name is `environment_listing`; otherwise the response carries the error `unsupported`. The set is read from the registration in memory, so no sibling lease structure, request context field, or echoed value changes.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.2:** The callback shall return only host environment records whose envelope `installation_id` and `provider_key` equal those of the lease context, and no record whose phase is `absent`. A decoded envelope with an empty `installation_id` or an empty `provider_key` belongs to no provider (the host never writes one: `invokeExecutorProvider` rejects an empty provider key) and is dropped as a non-match without a count. The host shall decode each candidate row's envelope; a row whose envelope cannot be decoded, whose `environment_id` is empty, or whose `environment_id` is over 128 bytes shall be left out of the listing and counted in `executor_plugin_environment_listing_skipped_total` (`reason`: `undecodable`, `unaddressable`) on every call that meets it. A warning is logged at most once per calling installation per 60 seconds, carrying the counts of that call, so a foreign installation's undecodable row (whose owner cannot be read) cannot produce more than one warning per minute per caller. Left-out rows and the empty-key rows above are the only cases in which a stored record is not listed.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.3:** There shall be one entry per host environment reference. When several rows carry the same reference, the entry shall come from the row with the highest envelope `environment_generation`, then the latest `executors_running.updated_at`, then the greatest `executors_running.session_id`. Each entry shall carry: `environment_ref` (the reference); `resource_id` (the envelope `resource.resource_handle`, the empty string when the envelope has no resource yet); `state` (the listed state); `generation` (the envelope `environment_generation`, 0 when it is negative); `is_calling_environment` (true when the reference equals the lease's `environment_id`); and `provider_state` (the envelope `resource.state_json`, set only when the request sets `include_state` and the envelope has a resource, otherwise the empty string). An entry shall carry no secret reference, profile value, admission identity, reservation, scope, placement field, or session or task content. The calling operation's own record is listed like any other (a provider that adds its new placement to the count shall skip the entry flagged `is_calling_environment`).
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.4:** Entries shall be ordered by `environment_ref` in byte order (the order of Go string comparison; no database collation takes part). A page shall hold at most 100 entries. `page_size` 0 (or absent) means 100, a value above 100 is clamped to 100, and a negative value is `invalid_request`. The cursor is opaque: the unpadded URL-safe base64 of `v1:` followed by the last reference returned, at most 256 bytes encoded. An empty cursor starts the read. A cursor that does not decode, lacks the `v1:` prefix, or decodes to an empty reference or to one over 128 bytes is `invalid_request`. A cursor is only a position inside the caller's own result set, so a cursor produced for another installation or provider key names a position in the caller's set and discloses nothing; `include_state` and `page_size` may differ between the pages of one traversal. A cursor over 56 bytes is possible only for a reference longer than the host's 36 bytes, which the host never emits. The response sets `next_cursor` only when at least one more entry exists after the page; a last page that is exactly full has no `next_cursor` and needs no empty follow-up page.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.5:** Each page shall be read in one store statement (one consistent read). Across pages there is no snapshot: a record created, re-keyed, or settled between two pages can be missed or shown once, and no reference shall appear twice in one traversal, because the next page holds only references strictly greater than the cursor reference and the entries of a page are distinct references.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.6:** The host shall map a record to a listed state from its envelope `phase` (and, for `ready` only, its `last_inspection_state`) with this table, and a phase not in the table shall be listed `cleaning`, never omitted:

  | Envelope phase | Listed state |
  | --- | --- |
  | `placement_pending`, `cancelling`, `placement_failed`, `placement_timeout` | `placing` |
  | `creating` (the phase the completion of a pending placement writes, AC-EXECUTORS-PLUGIN-PLACEMENT-001.5), `allocating`, `artifact_staging`, `bootstrapping`, `provisioned`, `recovery_progress` | `starting` |
  | `ready` with `last_inspection_state` empty, `running`, or `unknown` | `ready` |
  | `ready` with `last_inspection_state` `suspended` | `retained` |
  | `ready` with `last_inspection_state` `terminated` or `absent`, or any value not named in this table; `cleanup_pending`; `cleanup_progress`; `expired` | `cleaning` |
  | `absent` | not listed (AC 002.2) |

  `creating` is an envelope phase declared by the placement work order (work order 11), not only a task-environment status. A test enumerates every phase constant declared in any non-test file of the lifecycle package, so a new phase fails the test until it is added to this table.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.7:** `proto.Size` of the marshaled response shall be at most 262144 bytes (256 KiB). The host shall count each added entry as its marshaled size plus its field tag byte and length varint, and shall reserve 178 bytes for `next_cursor` (a 128-byte reference), and add entries in order until the next entry would exceed the cap, then return the page so far (at least one entry; one entry is at most 32 KiB because the stored state is) with a `next_cursor`, so an `include_state` page can hold fewer than 100 entries and a traversal always makes progress. A store failure, an unwired inventory store, or a read that exceeds a 5 second server-side bound shall fail the call with the error `unavailable` in the response and no partial page. A caller that cancels its own call ends the read and receives the transport's cancellation, not a response; the host writes nothing and counts the call as `cancelled`. The host never truncates silently. An installation with no listed record gets an empty page and no `next_cursor`.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.8:** The host shall check a call in this order and stop at the first refusal: (1) the lease (AC 002.1), which spends no token; (2) the rate limit; (3) the capability (AC 002.1), which is `unsupported` when the operation has no registration and `unavailable` when the host has no listing handler wired; (4) request validation (AC 002.4); (5) the store read (AC 002.7). The rate limit is a token bucket per installation: burst 10, refill 10 tokens per second, full when first used, held in memory and reset by a backend restart. Every call that passes the lease check takes one token, including a call refused at step 3 or 4. A call with no token left is refused with the error `rate_limited`, `retry_after_seconds` 1, and no store read. Steps 2 to 4 read nothing from the store.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.9:** The listing shall change no host record and take no admission scope lock, no recovery claim, no environment lock, and no lease or reservation. Its only store access is the one read-only statement of AC 002.5. It is advisory for the provider: it grants no reservation, and a launch the host admits after a listing can still collide with the provider's own fence, which stays the provider's authority.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.10:** No host launch, resume, recovery, or cleanup path shall call, wait for, or require the listing. A provider that never calls it, or that declares no `environment_listing` requirement, launches, resumes, and is cleaned up exactly as before this capability. A call made after its operation ended (the lease expired, or the operation's entry was removed when the dispatch finished, including when a plugin upgrade or restart ended it; a newer dispatch of the same operation does not end an older one that is still running) fails at step 1 of AC 002.8 and reads nothing.
- **AC-EXECUTORS-PLUGIN-ADMISSION-002.11:** A refusal of AC 002.1, 002.4, 002.7, and 002.8 other than the lease check shall be returned in the response's `error` field (`ExecutorProviderError`) with the code `unsupported`, `invalid_request`, `rate_limited`, or `unavailable` and with empty `entries` and `next_cursor`; the transport call itself succeeds. `invalid_request` is a host-originated code, not a member of the closed provider error set, the same way `state_too_large` is. A traversal interrupted by `rate_limited`, `unavailable`, or the caller's own cancellation is resumable: the cursor of the last successful page stays valid. If the plugin upgrade or the removal of the capability ends the dispatch mid-traversal, the next call fails at the lease check. A host without a listing handler wired returns `unavailable`, not the plain transport error of the sibling callbacks, because the lease already succeeded and the refusal is typed.

## Out of scope

- A per-node claim written by the host: the node is a provider concept, and the host records none.
- A cross-installation or administrator view: the administrator inventory read of REQ-EXECUTORS-PLUGIN-OPS-001 stays the operator surface.
- Host-side enforcement of per-node capacity.
- A listing callable outside a host-admitted operation (for example a provider timer), or during attach, recover, inspect, destroy, or a resume. A provider that needs one asks for a new capability or a widened registration rule (open question Q-O5).

## Implementation plans

- [Native worker slices 2 and 3](../../../plans/executor-plugin-native-workers-slices-2-3/plan.md)

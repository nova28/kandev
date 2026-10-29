---
status: draft
system: executors
requirements:
  - REQ-EXECUTORS-PLUGIN-ADMISSION-002
---

# Remote executor plugins: host environment listing system design

## Purpose and boundaries

One read-only host callback in the provider SDK, gated by one registry capability and authorized by the same operation lease
as the other executor callbacks. It reads the existing `plugin_executor` envelope rows through the inventory store the
lifecycle runtime already holds and adds no table, DDL, store method, or write. Capability gating comes from the
[versioning design](remote-executor-plugin-capability-versioning.md); the admission scope lock and reservations of the
[admission design](remote-executor-plugin-admission.md) are not touched. Names marked **new** do not describe existing APIs.

## Requirement mapping

| Requirement | Sections |
| --- | --- |
| REQ-EXECUTORS-PLUGIN-ADMISSION-002.1, .8, .10, .11 | Capability and lease, Check order, Wire contract |
| REQ-EXECUTORS-PLUGIN-ADMISSION-002.2, .3, .4, .5, .7 | Data source, Paging and budget |
| REQ-EXECUTORS-PLUGIN-ADMISSION-002.6 | State mapping |
| REQ-EXECUTORS-PLUGIN-ADMISSION-002.9 | Concurrency |

## Capability and lease

**New** registry row `{Name: "environment_listing", Min: 1, Max: 1, IntroducedIn: firstRelease}` in `internal/plugins/hostcaps`
(`apps/backend/internal/plugins/hostcaps/registry.go`), with no `Requires` edge: the listing reads no workspace descriptor, so
a dependency would only constrain providers. Adding it is a table row, never a contract version change (contract stays 1).
`TestRegistryHoldsTheClosedNameSetAtVersionOne` pins the 16 names of AC-EXECUTORS-PLUGIN-CAPABILITY-001.2; the same change
extends it to 17 and adds a dependency-closure assertion for the new row. `testdata/retired.golden` lists retired pairs only and does not change.

The callback is bound to a host dispatch, like its siblings. `executorProviderHost.authorize` (`internal/plugins/executor_provider_host.go`)
already requires an `ExecutorProviderRequestContext` with plugin ID, operation ID, a non-zero `dispatch_generation` (the
per-dispatch counter `executorProviderDispatchID`, which is a different value from `pluginDispatchGeneration`, the install-generation key of the echo gate),
an unexpired deadline, and an exact match against `executorProviderOps`. `ListExecutorEnvironments` calls `authorize` first and
holds the returned release function for the whole call, so a dispatch cannot end mid-read. The installation and provider key
come from the trusted, host-stored copy of the context. **New** field `negotiated []string` on `activeExecutorProviderOperation`,
filled by `beginExecutorProviderOperation` from the negotiated capability set that the launch path computed after the
capability gate accepted (for the continuation of a pending placement and for a resume, the set recorded in the envelope);
a dispatch started without a set records nil. The capability check is a membership test on that slice, so it needs no store
read and no echo lookup. No sibling lease, claim, or lock is added.

## Check order

Implemented as one function in `internal/plugins` (`executorProviderHost.ListExecutorEnvironments`), steps in this fixed order:

1. `authorize` (transport error on failure, no token spent).
2. Rate limiter: **new** `listingLimiter`, a map from installation ID to a token bucket (burst 10, refill 10 per second, created full on first use, mutex-guarded, injected clock). Takes one token for every call that passed step 1.
3. Capability membership (`unsupported`).
4. Validation of `page_size` and `cursor` (`invalid_request`).
5. Delegation to the handler for the read (`unavailable` on a store error or a 5 second context bound, derived from the caller's context so a cancelled caller ends the read).

Steps 2 to 4 never touch the store, so the interleaving "11 calls at one instant" and "unsupported with a counting store" are
both observable on a fake store.

## Wire contract

**New** in `pluginsdk/proto/kandev/plugin/v1/plugin.proto`, additive, in `service Host`:

```proto
rpc ListExecutorEnvironments(ListExecutorEnvironmentsRequest) returns (ListExecutorEnvironmentsResponse);

message ListExecutorEnvironmentsRequest {
  ExecutorProviderRequestContext context = 1;
  int32 page_size = 2;      // 0 = default 100; negative = invalid_request
  string cursor = 3;        // opaque, empty starts the read
  bool include_state = 4;
}
message ExecutorEnvironmentEntry {
  string environment_ref = 1;
  string resource_id = 2;
  string state = 3;         // placing | starting | ready | retained | cleaning
  uint64 generation = 4;
  string provider_state = 5; // resource.state_json when requested and present
  bool is_calling_environment = 6;
}
message ListExecutorEnvironmentsResponse {
  repeated ExecutorEnvironmentEntry entries = 1;
  string next_cursor = 2;
  ExecutorProviderError error = 3; // unsupported | invalid_request | rate_limited | unavailable
}
```

Refusals other than the lease check travel in `error` (the carrier every other provider response already uses; `rate_limited`
sets `retry_after_seconds` 1) and the gRPC call succeeds; a lease failure is the untyped transport error the sibling callbacks
return. `invalid_request` is documented beside `state_too_large` as a host-originated code outside the closed provider set.

SDK: the method is **not** added to the exported `ExecutorProviderHostAPI` interface, which would break external
implementers and fakes. **New** optional interface `ExecutorEnvironmentLister` in `pluginsdk/executor_provider.go`, implemented
by `grpcExecutorProviderHost`; a provider obtains it by type assertion on `host.ExecutorProvider()`. The unit-test fakes in
`pluginsdk` implement it where needed. The Go and npm SDKs are versioned independently from 0.1.0, and an additive method
and an optional interface are a minor change.

## Data source

Package layering: `internal/plugins` does not import `internal/agent/runtime/lifecycle`. The bridge is the existing interface
`ExecutorProviderHostHandler` (`executor_provider_host.go`), implemented by `*lifecycle.Manager` in
`executor_plugin_callbacks.go`. **New** method `ListExecutorEnvironments(ctx, installationID, providerKey, callingEnvironmentID, afterRef string, limit int, includeState bool) (entries []*pluginsdk.ExecutorEnvironmentEntry, more bool, err error)` on that interface,
implemented by the manager beside the other callbacks. It reads through the **existing** `PluginExecutorInventoryStore.ListExecutorsRunningPluginRemote`
(one `SELECT ... FROM executors_running WHERE runtime = 'plugin_remote'`, no lock, the same scan the readability count of the
versioning design uses and bounded by the active and retained plugin environments), then in Go: decodes each row's
`metadata` envelope (`pluginExecutorInventory`), drops other installations and provider keys, drops phase `absent`,
groups by `environment_id` choosing the row by envelope `environment_generation`, then `updated_at`, then `session_id`
(AC 002.3), sorts by `environment_id` as a Go string (byte order, so no `COLLATE` is involved on SQLite or PostgreSQL), keeps references strictly
greater than `afterRef`, and takes `limit+1` of them to decide `more`. The installation, provider key, and ordering key live in the
JSON envelope and not in columns, so adding SQL predicates is neither needed nor done. Both dialects therefore run the same
statement and the same Go code. Skipped rows are counted as `executor_plugin_environment_listing_skipped_total`.

Memory per call is bounded by the number of plugin rows times the 32 KiB envelope cap; at the rate limit this is the same
order as the existing recovery scan.

## State mapping

`pluginExecutorListedState(inventory) (state string, listed bool)` in the lifecycle package, a pure function implementing the table of AC 002.6
with a default branch of `cleaning`. The exhaustive test parses `executor_plugin.go` with `go/parser`, collects every constant whose name starts with
`pluginExecutorPhase`, and fails if any value is neither in the mapping table nor in the explicit not-listed set (`absent`), so a new phase
fails the test until it is mapped. `ready`'s `last_inspection_state` split uses the existing `pluginExecutorStateSuspended`,
`pluginExecutorStateTerminated`, and `pluginExecutorPhaseAbsent` constants. The four placement phases of the
[admission design](remote-executor-plugin-admission.md) (`placement_pending`, `cancelling`, `placement_failed`, `placement_timeout`) are constants created by work order 11,
which is why work order 18 depends on it.

## Paging and budget

The cursor is `base64.RawURLEncoding` of `"v1:" + lastRef`; decoding failure, a missing prefix, an empty reference, or a reference
over 128 bytes is `invalid_request`. The host-generated reference is 36 bytes, so an emitted cursor is at most 56 bytes; a
test asserts the bound. The page is read in one statement (AC 002.5). The handler adds entries while the running marshaled size stays at
or under 256 KiB (proto size of each entry plus a fixed 64 byte allowance for the envelope of the response), always keeps at least one, and sets
`more` when entries remain, so `next_cursor` is the last returned reference. A single entry cannot exceed the 32 KiB envelope cap, so one always fits.

## Concurrency

The query holds no lock beyond the one read-only statement. A launch, a settle, or a cancel racing the read changes only which
page sees the record; no writer waits for it and it waits for none. "No wait" means no application lock or claim; ordinary database
contention applies and surfaces as the store error `unavailable` after the 5 second bound. The rate limiter is an in-memory bucket per installation, and concurrent calls of one installation share it under its mutex, so
10 simultaneous calls at an empty clock all succeed and the 11th is refused; results of concurrent calls are independent.

## Failure and interleavings

| Interleaving | Required outcome | Test (level) |
| --- | --- | --- |
| A record is created between page 1 and page 2 | shown at most once; never duplicated | store test, SQLite and PostgreSQL |
| A record settles `absent` while a page is being read | listed or not listed, never half-listed | store race test |
| A launch commits its operation record during the read | no lock taken; the launch is not delayed | lifecycle unit with a blocking store |
| Foreign installation or provider key rows exist | never returned; a cursor from another traversal only selects a position in the caller's set | store and handler test |
| Dispatch whose negotiated set lacks the capability | `unsupported`, zero store reads | handler test with a counting store |
| Call after the dispatch ended or was superseded | transport error from `authorize`, zero reads, zero tokens | handler test |
| Unknown phase | listed `cleaning` | mapping table test |
| Several rows with one `environment_id` | one entry, chosen by generation, `updated_at`, `session_id` | lifecycle unit |
| Row with undecodable envelope or empty `environment_id` | skipped, WARN, counter | lifecycle unit |
| Page larger than 256 KiB with `include_state` | page cut by size with a `next_cursor`; traversal completes | lifecycle unit with 20 KiB states |
| Store error, deadline, caller cancel | `unavailable`, no partial page | handler test |
| 11 calls at one fake instant | the 11th is `rate_limited` with `retry_after_seconds` 1, no store read; after 100 ms one more is allowed | handler test with a fake clock |
| Provider lists during its own Provision | its own record appears flagged `is_calling_environment` | fixture provider test |
| Plugin upgrade ends the dispatch mid-traversal | next call fails at the lease check; the earlier cursor is not reused | handler test |

## Observability

`executor_plugin_environment_listing_total` (`outcome`: ok, unauthorized, rate_limited, unsupported, invalid_request, unavailable)
and `executor_plugin_environment_listing_skipped_total` (`reason`: undecodable, unaddressable). Both label sets are closed; no
installation or environment identifier is a label.

## Security

The callback exposes only opaque references, states, and the provider's own recorded state, to the owning installation, and only
during a host-admitted operation lease. It is unavailable to any account, route, or other plugin. A fabricated cursor selects a
start position inside the caller's own filtered set and discloses nothing outside it.

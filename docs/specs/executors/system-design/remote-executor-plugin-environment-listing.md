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
| AC-EXECUTORS-PLUGIN-ADMISSION-002.1, .8, .10, .11 | Capability and lease, Check order, Wire contract |
| AC-EXECUTORS-PLUGIN-ADMISSION-002.2, .3, .4, .5, .7 | Data source, Paging and budget |
| AC-EXECUTORS-PLUGIN-ADMISSION-002.6 | State mapping |
| AC-EXECUTORS-PLUGIN-ADMISSION-002.9 | Concurrency |

## Capability and lease

**New** registry row `{Name: "environment_listing", Min: 1, Max: 1, IntroducedIn: firstRelease}` in `internal/plugins/hostcaps`
(`apps/backend/internal/plugins/hostcaps/registry.go`), with no `Requires` edge: the listing reads no workspace descriptor, so
a dependency would only constrain providers. Adding it is a table row, never a contract version change (contract stays 1).
`TestRegistryHoldsTheClosedNameSetAtVersionOne` pins the 16 names of AC-EXECUTORS-PLUGIN-CAPABILITY-001.2; the same change
extends it to 17 and adds a dependency-closure assertion for the new row. `testdata/retired.golden` lists retired pairs only and does not change.

`IntroducedIn` is `firstRelease` while the contract is unreleased (the registry constant is 0.99.0 and the current version is lower). If this change lands after the release that contains slice 1 is cut, `IntroducedIn` is the version of the release this slice ships in, set at Build from the version file; a registry test asserts that no row's `IntroducedIn` is later than the version being built, so a row cannot claim a release that lacks it.

The callback is bound to a host dispatch, like its siblings. `executorProviderHost.authorize` (`internal/plugins/executor_provider_host.go`)
already requires an `ExecutorProviderRequestContext` with plugin ID, operation ID, a non-zero `dispatch_generation` (the
per-dispatch counter `executorProviderDispatchID`, which is a different value from `pluginDispatchGeneration`, the install-generation key of the echo gate),
an unexpired deadline, and an exact match (`proto.Equal`) against the entry in `executorProviderOps` keyed by plugin, operation ID, and that
generation. `beginExecutorProviderOperation` adds an entry per dispatch and its release function removes it; it never invalidates an older entry,
so a lease ends when its dispatch finishes, expires, or the plugin is upgraded or restarted, and not because a newer dispatch started.
`ListExecutorEnvironments` calls `authorize` first and holds the returned release function for the whole call, so a dispatch cannot end mid-read.
`authorize` is unchanged; no field is added to `activeExecutorProviderOperation` or to `ExecutorProviderRequestContext`.

The capability check does not travel through the plugin service. The negotiated set is computed in the lifecycle package
(`negotiatedCapabilities`, from the launch profile's `RequiredCapabilities`) and the launch already registers its `ExecutorCreateRequest` in
`Manager.pluginExecutorCallbacks` (`registerPluginExecutorCallbacks`, keyed by execution ID, which equals the operation ID). The lifecycle handler
therefore decides the capability from that registration: it looks up the registration for the lease's `execution_id`, applies the in-memory matches of
`pluginExecutorCallbackMatchesLaunch` and `pluginExecutorCallbackMatchesProvider` (no store read; the session and environment ownership reads of
`validatePluginExecutorOwnership` are deliberately not repeated, see AC 002.1), and passes when some `RequiredCapabilities` entry has `Name == "environment_listing"`
(the name only; the stored `name@version` string is never compared). No registration, or a mismatch, is `unsupported`. Only the launch's Provision operation
registers today; a resume, recover, attach, inspect, destroy, or placement-poll dispatch has no registration and is `unsupported`. If work order 11 makes the
continuation of a pending placement register its request, that dispatch is covered by the same rule with no change here.

The handler seam is an **optional** interface so existing `ExecutorProviderHostHandler` implementations and fakes keep compiling: **new** `ExecutorEnvironmentListingHandler` in `internal/plugins`, with
`ListExecutorEnvironments(ctx, *pluginsdk.ListExecutorEnvironmentsRequest) (*pluginsdk.ListExecutorEnvironmentsResponse, error)`, implemented by `*lifecycle.Manager`. The plugin service obtains it by type assertion on the configured handler; a missing handler, or one that does not implement it, is the typed `unavailable` refusal. No sibling lease, claim, or lock is added.

## Check order

Implemented as `executorProviderHost.ListExecutorEnvironments` in `internal/plugins` (steps 1 to 3a) and the handler method in the lifecycle package (steps 3b to 5), in this fixed order:

1. `authorize` (transport error on failure, no token spent).
2. Rate limiter: **new** `listingLimiter`, a map from installation ID to a token bucket (burst 10, refill 10 per second, created full on first use, mutex-guarded, injected clock). Takes one token for every call that passed step 1.
3. Handler lookup (`unavailable` when no `ExecutorEnvironmentListingHandler` is configured) and then, inside the handler, the capability decision from the launch registration (`unsupported`).
4. Validation of `page_size` and `cursor` (`invalid_request`), inside the handler.
5. The read, inside the handler (`unavailable` on a store error, an unwired inventory store, or a 5 second bound derived from the caller's context). If the caller's context is cancelled the read ends, no response is produced, the transport reports the cancellation, and the metric outcome is `cancelled`.

Steps 2 to 4 never touch the store (the capability decision reads the in-memory registration), so the interleaving "11 calls at one instant" and "unsupported with a counting store" are
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

Package layering: `internal/plugins` does not import `internal/agent/runtime/lifecycle`. The bridge is the optional interface
`ExecutorEnvironmentListingHandler` (see Capability and lease), implemented by `*lifecycle.Manager` in
`executor_plugin_callbacks.go`; the manager's internal read helper takes `(ctx, installationID, providerKey, callingEnvironmentID, afterRef string, limit int, includeState bool)` and returns `(entries, more, err)`.
An unwired inventory store is `unavailable`. It reads through the **existing** `PluginExecutorInventoryStore.ListExecutorsRunningPluginRemote`
(one `SELECT ... FROM executors_running WHERE runtime = 'plugin_remote'`, no lock, the same scan the readability count of the
versioning design uses and bounded by the active and retained plugin environments), then in Go: decodes each row's
`metadata` envelope (`pluginExecutorInventory`), drops other installations and provider keys (an envelope with an empty installation or provider key matches no caller), drops phase `absent`,
groups by `environment_id` choosing the row by envelope `environment_generation`, then `updated_at`, then `session_id`
(AC 002.3), sorts by `environment_id` as a Go string (byte order, so no `COLLATE` is involved on SQLite or PostgreSQL), keeps references strictly
greater than `afterRef`, and takes `limit+1` of them to decide `more`. The installation, provider key, and ordering key live in the
JSON envelope and not in columns, so adding SQL predicates is neither needed nor done. Both dialects therefore run the same
statement and the same Go code. Skipped rows are counted as `executor_plugin_environment_listing_skipped_total`; the warning log is limited to one per calling installation per 60 seconds by an in-memory timestamp map under a mutex (injected clock).

Memory per call is bounded by the number of plugin rows times the 32 KiB envelope cap; at the rate limit this is the same
order as the existing recovery scan.

## State mapping

`pluginExecutorListedState(inventory) (state string, listed bool)` in the lifecycle package, a pure function implementing the table of AC 002.6
with a default branch of `cleaning`. The exhaustive test parses every non-test `.go` file of the lifecycle package directory with `go/parser`, collects every constant whose name starts with
`pluginExecutorPhase`, and fails if any value is neither in the mapping table nor in the explicit not-listed set (`absent`), so a new phase
fails the test until it is mapped. `ready`'s `last_inspection_state` split uses the existing `pluginExecutorStateSuspended`,
`pluginExecutorStateTerminated`, and `pluginExecutorPhaseAbsent` constants. The four placement phases of the
[admission design](remote-executor-plugin-admission.md) (`placement_pending`, `cancelling`, `placement_failed`, `placement_timeout`) and the `creating` phase that a completed placement writes are constants created by work order 11,
which is why work order 18 depends on it. A `ready` record whose `last_inspection_state` is outside the named values maps to `cleaning` by the same default rule.

## Paging and budget

The cursor is `base64.RawURLEncoding` of `"v1:" + lastRef`; decoding failure, a missing prefix, an empty reference, or a reference
over 128 bytes is `invalid_request`. The host-generated reference is 36 bytes, so an emitted cursor is at most 56 bytes; a
test asserts the bound. The page is read in one statement (AC 002.5). The handler adds entries while the running marshaled size stays at
or under 262144 bytes, counting each entry as `1 + protowire.SizeVarint(n) + n` where `n = proto.Size(entry)` (tag byte, length varint, body) and reserving 178 bytes up front for the `next_cursor` field (tag, length, and a 175-byte cursor for a 128-byte reference), always keeps at least one, and sets
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
| Launch whose requirements lack `environment_listing`, or an operation with no launch registration (attach, recover, inspect, destroy, poll) | `unsupported`, zero store reads | handler test with a counting store |
| Call after the dispatch ended | transport error from `authorize`, zero reads, zero tokens | handler test |
| Unknown phase | listed `cleaning` | mapping table test |
| Several rows with one `environment_id` | one entry, chosen by generation, `updated_at`, `session_id` | lifecycle unit |
| Row with undecodable envelope or empty `environment_id` | skipped, WARN, counter | lifecycle unit |
| Page larger than 256 KiB with `include_state` | page cut by size with a `next_cursor`; traversal completes | lifecycle unit with 20 KiB states |
| Store error, unwired store, 5 s bound | `unavailable` in the response, no partial page | handler test |
| Caller cancels mid-read | read ends, no response, transport cancellation, metric `cancelled`; cursor of the last page still valid | handler test |
| No listing handler configured | `unavailable` in the response after the rate step, zero reads | service test |
| `ready` record with an unrecognised `last_inspection_state` | listed `cleaning` | mapping table test |
| `creating` record | listed `starting` | mapping table test |
| Envelope with empty installation or provider key | dropped, not counted | lifecycle unit |
| Undecodable foreign rows on 12 calls within 60 s by one caller | counter increases each call; one WARN | lifecycle unit with a fake clock |
| Newer dispatch of the same operation starts while an older one runs | the older lease still works until its dispatch ends | service test |
| Page of 100 entries at 32 KiB state each | `proto.Size(response) <= 262144` | lifecycle unit |
| 11 calls at one fake instant | the 11th is `rate_limited` with `retry_after_seconds` 1, no store read; after 100 ms one more is allowed | handler test with a fake clock |
| Provider lists during its own Provision | its own record appears flagged `is_calling_environment` | fixture provider test |
| Plugin upgrade ends the dispatch mid-traversal | next call fails at the lease check; the earlier cursor is not reused | handler test |

## Observability

`executor_plugin_environment_listing_total` (`outcome`: ok, unauthorized, rate_limited, unsupported, invalid_request, unavailable, cancelled)
and `executor_plugin_environment_listing_skipped_total` (`reason`: undecodable, unaddressable). Both label sets are closed; no
installation or environment identifier is a label.

## Security

The callback exposes only opaque references, states, and the provider's own recorded state, to the owning installation, and only
during a host-admitted operation lease. It is unavailable to any account, route, or other plugin. A fabricated cursor selects a
start position inside the caller's own filtered set and discloses nothing outside it.

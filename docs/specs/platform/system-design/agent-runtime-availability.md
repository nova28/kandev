---
status: draft
system: platform
requirements:
  - REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001
  - REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002
created: 2026-09-27
owners:
  - kandev
---

# Agent Runtime Availability System Design

## Purpose and boundaries

This design covers the install-wide availability snapshot of the standalone
`agentctl` control server that the backend starts for local and worktree
executors, and the in-process relaunch of that server after an unexpected exit.

The backend owns the process, its endpoint identity, and the snapshot. The
shared application shell owns the notice and alert. Session interruption keeps
its existing lifecycle paths.

This design does not cover an adopted control server under agent survival, a
remote executor, orphaned agent processes, or `/health` readiness.

## Requirement mapping

| Requirement | Design sections |
| --- | --- |
| `REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001` | [Snapshot contract](#snapshot-contract), [State machine](#state-machine), [Delivery](#delivery), [Browser surface](#browser-surface), [Testing strategy](#testing-strategy) |
| `REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002` | [Endpoint owner](#endpoint-owner), [Relaunch supervisor](#relaunch-supervisor), [Consumer rebinding](#consumer-rebinding), [Session behavior](#session-behavior), [State machine](#state-machine), [Runtime flag](#runtime-flag), [Testing strategy](#testing-strategy) |

## Snapshot contract

The boot payload and `/api/v1/app-state` include `initialState.agentRuntime`
for authenticated visitors. The same object is the payload of the install-wide
WebSocket notification `system.agent_runtime.status_changed`.

```json
{ "status": "recovering", "reason": "agentctl_exited", "occurred_at": "2026-09-27T14:22:52Z" }
```

| `status` | `reason` | `occurred_at` |
| --- | --- | --- |
| `available` | omitted | omitted |
| `recovering` | `agentctl_exited` | time of the exit that started the outage |
| `unavailable` | `agentctl_exited` (relaunch disabled) or `relaunch_exhausted` | time of the exit that started the outage |

`occurred_at` stays the first exit time for the whole outage, including the
terminal `relaunch_exhausted` snapshot. Status and reason are stable enums.
PIDs, ports, exit codes, and tokens are logged, never published.

The bus subject stays `system.agent_runtime.availability_changed`
(`events.AgentRuntimeAvailabilityChanged`). `client.Availability` in
`internal/agent/runtime/agentctl/availability.go` remains the only writer and
keeps `publishMu` so events leave in commit order.

## State machine

`client.Availability` replaces its one-way `published` latch with explicit
transitions. Every method is a no-op when its source state does not match.

| Current | Method | Next | Published |
| --- | --- | --- | --- |
| none | `MarkAvailable()` | `available` | yes |
| `available` | `MarkRecovering(at)` | `recovering` | yes |
| `recovering` | `MarkAvailable()` | `available` | yes |
| `available` or `recovering` | `MarkUnavailable(reason, at)` | `unavailable` | yes |
| `unavailable` | any | `unavailable` | no |
| none | `MarkRecovering` or `MarkUnavailable` | none | no |

`unavailable` is terminal for the backend boot (AC-001.10). With the relaunch
flag off, the exit callback calls `MarkUnavailable(agentctl_exited, now)` and
nothing ever calls `MarkRecovering`, so the published sequence is exactly the
2026-08-08 contract (AC-002.10).

Each publish runs under `publishMu` after the state commit, so two racing
transitions publish in the order they committed.

## Endpoint owner

A new type, `client.Endpoint` (package `internal/agent/runtime/agentctl`), holds
one immutable value:

```go
type EndpointIdentity struct {
    Generation uint64 // starts at 1, +1 per publish
    Host       string
    Port       int
    Token      string
    PID        int
}
```

- `Current() EndpointIdentity` returns the value through an `atomic.Pointer`.
  Readers never see a partial update.
- `Publish(host, port, token, pid)` stores a new value with
  `Generation = previous + 1`. Only the backend startup path and the relaunch
  supervisor call it.
- `Subscribe(func(EndpointIdentity))` registers a synchronous observer, called
  after each publish in registration order. `Publish` and `Subscribe` serialize
  on one internal mutex. When `Subscribe` runs after generation 1 exists, it
  calls the new observer once with `Current()` before returning, so an observer
  never misses the identity that was current when it registered and never sees
  generations out of order.
- Observers run under that mutex and must not block, call `Publish`, or call
  `Subscribe`. They only store values (for example an `atomic.Int64`). Work
  that can block, such as stopping and starting the ownership renewer, is not
  an observer; the supervisor does it directly (see
  [Relaunch loop](#relaunch-loop)).

`cfg.Agent.StandaloneHost`, `StandalonePort`, `StandaloneAuthToken`, and
`StandalonePID` are set once at startup as today for logging and adoption. No
consumer reads them after the endpoint owner exists.

## Consumer rebinding

Every consumer that captures the identity at startup reads from the endpoint
owner instead.

| Consumer (today) | Change |
| --- | --- |
| Standalone `ControlClient` built in `backendapp/agents.go:53` with `WithControlAuthToken` | Built with an endpoint source. Each request resolves base URL and bearer token from `Current()`. The auth transport reads the token per request. |
| `StandaloneExecutor` (`lifecycle/executor_standalone.go`), host, port, and `SetAuthToken` | Holds the endpoint source. Instance creation and each per-instance `agentctl.Client` read host and token from `Current()` when built, and record the generation on the execution. |
| Host-utility `ControlClient` and `hostutility.Manager` (`backendapp/main.go:1107-1112`) | Same endpoint source. `SetAuthToken` is removed. Cached per-agent-type instances record their generation; an instance from an earlier generation is dropped through the existing `dropStaleInstance` path before use. |
| Gateway caches keyed by session: VS Code proxy (`gateway/websocket/vscode_proxy.go`), port proxy (`port_proxy.go`), and port tunnels (`port_tunnel.go`) | Invalidated through the lifecycle retirement hook when the execution they target is retired for a stale generation (see [Stale-generation retirement](#stale-generation-retirement)). A 401 alone never invalidates them today, and the replacement `agentctl` reuses the same instance port range with a new token. |
| `lifecycleMgr.SetStandaloneHostPID` (`agents.go:177`) | An endpoint observer stores the new PID in the existing `atomic.Int64`, so `executors_running.local_pid` records the live process. |
| Ownership renewal loop (`startOwnershipRenewal`, agent survival only) | Owned by the supervisor, not an observer. The relaunch loop stops the old renewer before the grace and starts the new one in step 4, immediately before publish (see [Relaunch loop](#relaunch-loop)). |

Existing client option constructors stay for tests and for remote executors,
which keep their own per-launch tokens.

## Relaunch supervisor

A new `agentctlSupervisor` in `internal/backendapp` owns the launcher, the
endpoint owner, and availability for the process this boot started. It replaces
the direct `OnUnexpectedExit: availability.MarkUnavailable` wiring in
`spawnFreshAgentctl`. `launcher.Launcher` cannot restart, so each attempt
builds a new launcher instance from the same `launcher.Config` through an
injected factory.

### Exit handling

Each launcher gets an `OnUnexpectedExit` closure that captures the generation
it was published as. The supervisor ignores a callback whose generation is not
the current one, and it ignores every callback after shutdown began.

On an accepted exit with the flag enabled:

1. Record the exit time `t0`. Prune outage starts older than 10 minutes from the
   window. If the window already holds 3 starts, call
   `MarkUnavailable(relaunch_exhausted, t0)` and stop (AC-002.5).
2. Append `t0` to the window, call `MarkRecovering(t0)`, and start the relaunch
   loop in the supervisor goroutine.

A single `sync.Mutex` guards the window, the loop state, and the current
launcher. Only one relaunch loop runs at a time.

**Lock discipline.** The launcher runs `OnUnexpectedExit` synchronously inside
`monitorExit`, before it closes `exited` (`launcher.go`), and every exit
closure takes the supervisor mutex. Therefore no goroutine may wait on a
launcher while it holds the supervisor mutex: `ForceStop`, `Stop`, and any
receive on a launcher's exit are always called after the mutex is released. The
same holds for `OwnershipRenewer.Stop` and `wg.Wait`. Under the mutex the
supervisor only reads and writes its own fields, calls `Endpoint.Publish` and
the `Availability` methods (in-memory, non-blocking), starts goroutines, and
makes the one bounded `RecordFreshControlServer` call (step 3). With that rule,
an exit closure blocked on the mutex always gets it once the holder finishes
in-memory work or the 2 second record, so `monitorExit` always reaches
`close(exited)` and a later `ForceStop` returns.

### Relaunch loop

| Attempt | Delay before start |
| --- | --- |
| 1 | 0 s (after the 1 s grace below) |
| 2 | 1 s |
| 3 | 2 s |
| 4 | 4 s |
| 5 | 8 s |

The loop first stops the old ownership renewer, when there is one, outside
the supervisor mutex (`OwnershipRenewer.Stop` cancels and waits for its one
goroutine; the in-flight claim honours that cancellation, so the wait is short;
its endpoint is already dead). It then waits 1 second before attempt 1, so the
old process can release its port. Each attempt:

1. Checks cancellation (see [Shutdown](#shutdown)). Builds a launcher and calls
   `Start(attemptCtx)`, where `attemptCtx` is the supervisor context with a
   `relaunchAttemptTimeout` (10 s) deadline. `Start` runs the port check (falls
   back to a free OS port), process start, the health wait, and the nonce
   handshake. The health wait and the handshake already return when their
   context ends, so the attempt deadline caps them below the launcher's own 30
   second health deadline. `Start` uses `exec.Command`, not
   `exec.CommandContext`, so ending `attemptCtx` after a successful `Start`
   does not signal the process. The attempt cancels `attemptCtx` when `Start`
   returns.
2. On failure, including the attempt deadline, calls `ForceStop` on that
   launcher and waits for the next delay in a `select` on a `time.Timer` and
   the supervisor context.
3. On success, takes the supervisor mutex and re-checks cancellation. If
   shutdown began, it releases the mutex, calls `ForceStop`, and returns.
   Otherwise, under agent survival only, it calls `RecordFreshControlServer`
   with a `relaunchRecordTimeout` (2 s) context. A failure is logged at WARN and
   does not fail the attempt, matching boot (`spawnFreshAgentctl`).
4. Checks the attempt's `exited` flag. If it is set, it releases the mutex,
   calls `ForceStop`, and treats the attempt as failed (next delay, or
   `relaunch_exhausted` after attempt 5). A record already written for that
   process is left in place. A later successful attempt overwrites it; if none
   succeeds, the next boot finds its endpoint answering nothing
   (`AdoptionReasonNoServer`, `control_server_adoption.go`) and spawns fresh.
   Otherwise, under agent
   survival only, it starts a new ownership renewer for the new endpoint and
   token with `startOwnershipRenewal`, which only launches its goroutine and
   does not block, and then calls `Endpoint.Publish` with the new host, port,
   token, and PID. Observers update the PID.
5. Calls `MarkAvailable()` and releases the mutex.

When attempt 5 fails, the loop calls `MarkUnavailable(relaunch_exhausted, t0)`.

**Timing bound for AC-002.2.** Attempt 1 starts 1 second after the exit. Its
`Start` ends within 10 seconds or the attempt fails. Step 3 adds at most 2
seconds for the record. Steps 4 and 5 start a goroutine and are otherwise
in-memory. A successful attempt 1
therefore publishes `available` within 15 seconds of the exit.

**Why a record failure is non-fatal.** Boot already treats it as non-fatal: the
server runs, the backend owns it, and only a later backend restart's adoption
depends on the record. Failing the attempt would kill a healthy replacement
over a bookkeeping write and could exhaust the outage over a database fault.
One behavior for boot and relaunch keeps the adoption contract identical:
without a current record, the next backend start spawns a fresh server, which
is today's recovery path. The renewer still starts, because renewal uses the
launch token, not the record.

### Ordering and edge rules

- **Exit before publish.** Each attempt's exit closure captures the generation
  that attempt would publish. The closure first sets the attempt's `exited`
  flag (an `atomic.Bool`, no lock), then takes the supervisor mutex, which the
  attempt holds from step 3 through step 5. Step 4 checks the flag before
  publishing, so an exit at any point before that check, including during the
  step 3 record, fails the attempt instead of publishing a dead endpoint; the
  loop releases the mutex before its `ForceStop` (see Lock discipline). The
  closure then acquires the mutex, finds its generation unpublished, and
  returns; `monitorExit` then closes `exited`, which lets `ForceStop` return.
  An exit after the step 4 check reaches the mutex after step 5 releases it,
  finds its generation current, and is a new outage.
- **Window boundary.** An outage start is pruned when `t0 - start >= 10m`. A
  start exactly 10 minutes earlier no longer counts. The window lives in memory
  and resets on every backend boot.
- **Forced stop of unpublished launchers.** `Launcher.Stop` is a no-op under
  agent survival. The launcher gains an exported `ForceStop()` that ignores
  survival. In order it:
  1. Under the launcher's own `mu`, sets `stopping` and reads `cmd`. If `cmd`
     is nil (no process was started, for example a port or nonce failure in
     `Start`), it closes the liveness pipe and releases the child lifecycle,
     then returns.
  2. If `exited` is not yet closed, sends `SIGKILL` to the process group
     through the existing `forceKill`. Once `exited` is closed the process has
     been reaped, so no signal is sent to a PID the OS may have reused.
  3. Closes the liveness pipe (`closeParentPipe`) and releases the child
     lifecycle (`releaseChildLifecycle`, the Windows Job Object).
  4. Waits for `exited` to close.

  Setting `stopping` keeps `monitorExit` from calling `OnUnexpectedExit` only if
  it runs before `monitorExit` reads the flag; an exit already observed still
  calls the closure, which is safe by the generation rule and the lock
  discipline. Every step is idempotent. `closeParentPipe` nils the pipe, and
  `releaseChildLifecycle` swaps the job handle to 0. So `ForceStop` after
  `Start`'s own failure cleanup (`forceKill`, `closeParentPipe`,
  `releaseChildLifecycle`), and a second `ForceStop`, are no-ops apart from
  the wait. The loop calls it on every failed or abandoned attempt, always
  without holding the supervisor mutex, so no unpublished `agentctl` outlives
  its attempt. `ForceStop` is never called on a published launcher.
- **Before the first publish.** `Current()` returns the zero value with
  `Generation == 0`. Consumers are constructed only after startup publishes
  generation 1. A consumer that reads generation 0 returns an error without
  sending a request.
- **Repeated calls.** `MarkRecovering` while already `recovering` is a no-op, so
  a duplicate exit notification never restarts the loop or resets `t0`.

The constants `relaunchGrace`, `relaunchBackoff`, `relaunchMaxAttempts`,
`relaunchAttemptTimeout`, `relaunchRecordTimeout`, `outageWindow`, and
`outageWindowLimit` are package constants, not configuration.

### Shutdown

The supervisor context is the backend lifecycle context. Cleanup runs in this
order:

1. Takes the supervisor mutex, sets `shuttingDown`, and releases the mutex.
2. Cancels the supervisor context.
3. Waits for the loop goroutine to return (`wg.Wait`).
4. Takes the mutex, reads the current published launcher and renewer, and
   releases the mutex. It then stops the ownership renewer and calls `Stop` on
   that launcher, both outside the mutex (see Lock discipline). Under agent
   survival that `Stop` is the existing no-op, so a surviving server keeps
   running.

The loop checks cancellation at three points: before each attempt starts
(step 1), after `Start` returns, and after taking the mutex in step 3. At each
point, `shuttingDown` or a done context makes the loop release the mutex if it
holds it, `ForceStop` the unpublished launcher, if any, and return without
calling an availability method. A delay ends through the `select` on the context. A `Start` in
progress returns early because its health wait and handshake watch the attempt
context, and `Start` itself kills the process on that failure.

Because steps 3 to 5 run under the mutex that shutdown step 1 takes, shutdown
cannot land between publish and `MarkAvailable`. Either shutdown sets
`shuttingDown` first, and the attempt force-stops its unpublished launcher, or
the attempt publishes and marks `available` first, and the replacement is the
current launcher that shutdown step 4 handles like any other: kept running
under agent survival and stopped otherwise. In both orders no availability
transition is published after shutdown began (AC-002.9). Step 3's record call
uses its own timeout, not the cancellable supervisor context, so shutdown
waits at most that timeout for the mutex and never cancels the step midway.

## Session behavior

- Stream loss from the exited process keeps its current handling in
  `lifecycle/manager_events.go` (`handleStreamDisconnect*`): the execution is
  marked failed and its error event is published. The supervisor does not touch
  sessions (AC-002.6).
- Every `AgentExecution` created by `StandaloneExecutor` records the endpoint
  generation. An execution with a nonzero generation below
  `Current().Generation` is stale. Stale executions are retired in the
  execution store described below, so every caller that finds an execution
  sees only live ones. The existing on-demand paths
  (`GetOrEnsureExecution` and session resume) then create a fresh instance on
  the replacement (AC-002.6).
- The replacement allocator starts again at 41001, so a new instance can reuse
  an old instance's port. A stale client still carries the old token and gets
  401 from the new server, so no request reaches another session's instance.
  Retirement prevents that stale client from being used at all.
- Per-instance ports come from the in-process allocator inside `agentctl`
  (`agentctl/server/instance/port_allocator.go`, range 41001-41100). The
  replacement starts with an empty allocator, so no port lease crosses the
  relaunch. The port-lease owner-safety work in issue #3961 is independent of
  this design and is not a build dependency.
- A session with a prompt in flight already returns to waiting for input with a
  transient "agent stream disconnected" error. A session without a prompt in
  flight keeps its state and shows the `agentctl.error` event. Neither path
  changes.

### Stale-generation retirement

**Enforcement point.** The check lives in `lifecycle.ExecutionStore`, the one
place every execution lookup reads from. The Manager resolves executions
through the store in about 90 places (`manager_interaction.go`,
`manager_execution.go`, `manager_passthrough.go`, `process_runner.go`, and
others), and the public `GetExecutionBySessionID` and `GetExecution` that the
gateway VS Code proxy, port proxy, port tunnels, terminal sessions, process,
passthrough, and Git handlers call are thin wrappers over it. A check in the
Manager would have to be repeated at each site; a check in the store cannot be
bypassed.

- `NewExecutionStore` takes a generation source (`func() uint64`, wired to
  `Endpoint.Current().Generation`) and a retire callback
  (`func(*AgentExecution)`). A nil source disables the check, which keeps
  existing store tests and non-standalone setups unchanged.
- Every accessor that returns executions (`Get`, `GetBySessionID`,
  `GetByTaskEnvironmentID`, `GetByContainerID`, `List`,
  `ListExecutionsForTask`, `WithLock`, `WithRLock`) treats an execution with a
  nonzero generation below the current generation as absent.
- An execution with generation 0 (created by a non-standalone executor) is
  never stale.
- `restoreRecoveredFailedExecution` (`manager_interaction.go`) and the
  `GetOrEnsureExecution` fast path read through `GetBySessionID`, so neither
  reconnects to a recorded instance port from an earlier generation.

**Retirement.** Stale entries are deleted from the store under the write
lock, and the retire callback runs for each deleted entry only after every store
lock is released, in the order the entries were deleted. The callback is
invoked only by the call that deleted the entry. By accessor kind:

- **Single-entry read accessors** (`Get`, `GetBySessionID`,
  `GetByTaskEnvironmentID`, `GetByContainerID`, `WithRLock`): on finding a stale
  entry under the read lock, the accessor releases the read lock without
  calling any `fn`, takes the write lock, re-checks that the same execution ID
  is still present and stale, deletes it if so, releases the lock, and then
  runs the callback if it deleted. It returns not-found (`false`, or
  `ErrExecutionNotFound` from `WithRLock`) whether or not it deleted.
- **`WithLock`**: it already holds the write lock, so it deletes a stale entry
  in place, does not call `fn`, releases the lock, runs the callback, and
  returns `ErrExecutionNotFound`.
- **List accessors** (`List`, `ListExecutionsForTask`): they collect the stale
  execution IDs under the read lock and return only live entries in their
  existing order. If any were stale, they then take the write lock, re-check
  and delete each collected ID that is still present and stale, release the
  lock, and run one callback per deleted entry. The returned slice is the live
  set observed under the read lock; it is not recomputed after retirement.

No accessor calls the retire callback while holding a store lock, so a
callback may itself read the store. The Manager's callback runs the rest of
`RemoveExecution`'s cleanup
(`releaseActivity`, `closeStreamCoalescer`, `cleanupPassthroughMCPConfig`, and
clearing runtime interest) and then the retirement hook below. It does not
call the instance's delete endpoint, because the instance died with the exited
process.

**Concurrent lookups.** Two callers that find the same stale execution race on
the write lock. The second re-check finds the entry gone, so it skips the
callback and returns not-found like the first. Cleanup and the hook run
exactly once. A concurrent explicit `RemoveExecution` is the same race: the
store delete is by execution ID and idempotent. A concurrent
`GetOrEnsureExecution` recreates through its existing singleflight, so one
fresh instance is built per session.

**Callbacks from the exited process.** Stream-disconnect and status callbacks
for the exited process arrive while the status is `recovering`, before step 4
publishes, so the execution is not stale yet and the existing failure handling
runs unchanged. A callback that arrives after publish resolves by execution ID,
finds nothing, and is dropped by the existing callback identity rule.

**Gateway invalidation wiring.** `lifecycle.Manager` gains
`SetExecutionRetiredHook(func(sessionID string))`. `backendapp` sets it once,
after the gateway handlers exist, to a function that calls
`VscodeProxyHandler.InvalidateProxy`, `PortProxyHandler.InvalidateSession`, and
`TunnelManager.InvalidateSession` for that session. The hook runs after the
store removal and outside the store lock, and each invalidation method is
idempotent. A nil hook is a no-op, so tests and non-gateway builds need no
wiring.

### Requests during recovering

- A request that reached the exited process fails with its transport error and
  is not retried (AC-002.11).
- While the status is `recovering`, `Current()` still returns the exited
  generation, so no execution is stale yet. Every call to the local runtime,
  including a new session launch and an on-demand workspace operation
  (`GetOrEnsureExecution` for files, shell, Git, ports, or host utilities),
  resolves to the dead endpoint and fails with its existing error. Nothing is
  gated, queued, or retried.
- A request that starts after step 4 publishes reads the new generation and
  reaches the replacement, even if `MarkAvailable` in step 5 has not been
  published yet. The only effect of that window is a request that succeeds
  while the browser still shows the `recovering` notice for a moment.

## Runtime flag

| Field | Value |
| --- | --- |
| Key | `features.agentctlRelaunch` |
| Environment | `KANDEV_FEATURES_AGENTCTL_RELAUNCH` |
| Kind, stability, risk | feature, experimental, medium |
| Restart required | yes |
| Profiles | `prod: "false"`, `dev: "true"`, `e2e: "true"` |

The supervisor reads `cfg.Features.AgentctlRelaunch` once at construction. Flag
work follows the `/runtime-feature-flags` checklist, including the frontend
feature-toggle metadata and completeness tests.

## Delivery

Delivery is unchanged. `RegisterAgentRuntimeNotifications` broadcasts every bus
event to all clients and replays the current snapshot when a user subscribes.
Boot hydration reads `Snapshot()`. Both carry the new status and reason
unchanged, because they serialize `AvailabilitySnapshot` as is.

## Browser surface

- `lib/types/agent-runtime.ts` adds the `recovering` variant and the
  `relaunch_exhausted` reason. The WebSocket handler and hydrator store any
  valid snapshot. A snapshot with an unknown status is ignored and the previous
  value is kept.
- `AgentRuntimeUnavailableAlert` becomes `AgentRuntimeStatusAlert`, mounted in
  the same slot of `app-status-surface-provider.tsx`:
  - `recovering`: a neutral (non-destructive) alert with `role="status"`, a
    spinner icon, title, and body. No action and no restart dialog.
  - `unavailable`: today's destructive alert with `role="alert"`. For
    `relaunch_exhausted`, one extra line states that automatic recovery did not
    succeed. The restart action and manual guidance are unchanged.
  - `available` or `null`: renders nothing.
- The test ID stays `agent-runtime-alert`; a `data-status` attribute carries the
  status for tests.
- Layout, 44 px phone targets, and overflow rules are unchanged from REQ-001.

New `system` namespace keys, in all six locales plus pseudo:

| Key | English |
| --- | --- |
| `agentRuntimeRecoveringTitle` | Restarting local agent runtime |
| `agentRuntimeRecoveringBody` | Agent actions and workspace tools are paused while Kandev restarts the local runtime. Your saved data remains safe. |
| `agentRuntimeRelaunchExhausted` | Automatic recovery did not succeed. |

## Observability

The supervisor logs one structured line per event: `agentctl exited
unexpectedly` (WARN, with PID and exit error), `agentctl relaunch attempt`
(INFO, attempt number and delay), `agentctl relaunch failed` (WARN, error),
`agentctl relaunched` (INFO, generation, port, PID, and outage duration), and
`agentctl relaunch exhausted` (ERROR, reason `attempts` or `window`). Tokens are
never logged. No metric is added.

## Security

The token is never exposed in a browser payload, a log, or a flag value. A new
token is minted for each launch by the existing nonce handshake. The old token
dies with the old process. Consumers read the token per request, so no stale
credential is retried against the new process.

## Failure handling

| Case | Behavior |
| --- | --- |
| Replacement fails health or handshake | Attempt fails; next backoff |
| Replacement exits before publish | `Start` or the handshake fails; attempt fails |
| Replacement exits after publish | New outage, subject to the window |
| Configured port still bound | `Start` falls back to a free OS port; consumers follow the endpoint |
| `RecordFreshControlServer` fails | Logged at WARN; the attempt continues and publishes, as at boot |
| Attempt exceeds `relaunchAttemptTimeout` | `Start` returns the context error; the launcher is force-stopped; next backoff |
| Shutdown during a delay or attempt | Loop returns; the unpublished launcher is force-stopped; no transition |
| Shutdown after publish | The replacement is the current launcher; survival rules apply; no transition |
| Exit callback from a superseded generation | Ignored |

## Testing strategy

- **Availability unit tests** (`availability_test.go`): the full transition
  table, no-op cases, terminal `unavailable`, commit-ordered publishes, and
  concurrent readers.
- **Endpoint owner tests**: generation increments, atomic reads under `-race`,
  observer order, and `Subscribe` after generation 1 delivering the current
  identity once before any later publish.
- **Supervisor tests** with a fake launcher factory and the injected clock from
  `testing/synctest`: success on attempt 1, success on attempt 3, exhaustion
  after 5, the window limit on the fourth outage, a stale-generation callback,
  an attempt that exceeds `relaunchAttemptTimeout` (force-stopped, next
  backoff), shutdown during a delay, shutdown during an attempt, shutdown
  racing step 3 (either force-stopped with no transition, or published and
  kept under survival), flag off, a failed `RecordFreshControlServer` that
  still publishes, an exit during the step 3 record that fails the attempt
  and returns (a real `monitorExit`-style fake that calls the exit closure
  before closing its exit channel, run with a test deadline so a deadlock
  fails instead of hanging), shutdown while that exit closure waits on the
  mutex (completes, no transition),
  the renewer stopped before the grace and restarted in step 4 before publish,
  and no renewer started for an attempt whose `exited` flag was set.
- **Launcher tests**: `ForceStop` kills a started process under agent survival
  and does not fire `OnUnexpectedExit`; returns at once on a launcher whose
  `Start` failed before a process existed; after `Start`'s own failure cleanup
  and on a second call it sends no signal and returns; and it returns when an
  `OnUnexpectedExit` callback that blocks on an outside mutex is released.
- **Consumer tests**: the control client and a per-instance client send the new
  token after a publish.
- **Execution store tests**: every accessor hides a stale entry, `WithLock`
  and `WithRLock` return `ErrExecutionNotFound` without calling `fn`, a list
  holding two stale entries returns only live ones and runs two callbacks after
  unlocking (the callback reads the store without deadlocking), a
  generation-0 entry is never stale, concurrent lookups of one stale entry run
  the retire callback exactly once (under `-race`), and the Manager's callback
  invalidates the VS Code proxy, port proxy, and port tunnels through the
  retirement hook. A stale execution is recreated by `GetOrEnsureExecution`.
- **Frontend unit tests**: the three alert states, the exhausted line, unknown
  status ignored, and the store and WebSocket handler for `recovering`.
- **Playwright**: client-injected `recovering` and `relaunch_exhausted` states
  on desktop and phone; plus one real-crash scenario that sends `SIGKILL` to
  the backend's `agentctl` child, observes `recovering` then no alert, launches
  a mock-agent session to completion, and restarts the worker backend in
  cleanup.

## Related decisions

- [2026-08-08 agentctl crash containment](../../../decisions/2026-08-08-agentctl-crash-containment.md)
- [2026-09-27 in-process agentctl relaunch](../../../decisions/2026-09-27-in-process-agentctl-relaunch.md)
- [0018 runtime settings overrides](../../../decisions/0018-runtime-settings-overrides.md)

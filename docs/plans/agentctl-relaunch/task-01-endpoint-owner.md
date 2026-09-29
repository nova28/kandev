---
id: "01-endpoint-owner"
title: "Route standalone identity through one endpoint owner"
status: pending
wave: 1
depends_on: []
plan: "plan.md"
requirements:
  - REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002
acceptance_criteria:
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.3
system_design:
  - ../../specs/platform/system-design/agent-runtime-availability.md
---

# Task 01: Route standalone identity through one endpoint owner

## Summary

Add `client.Endpoint` and make every standalone consumer read host, port, token,
and PID from it per request. Behavior does not change: this boot publishes one
generation and nothing publishes a second yet.

## In scope

- `EndpointIdentity` and `Endpoint` (`Current`, `Publish`, `Subscribe`) in
  `internal/agent/runtime/agentctl`. `Subscribe` after generation 1 delivers
  the current identity once before returning.
- A `ControlClient` constructor that resolves base URL and bearer token from an
  endpoint source per request.
- `StandaloneExecutor` and `hostutility.Manager` built from the endpoint source;
  remove their `SetAuthToken` methods.
- A PID observer that updates `lifecycle.Manager.SetStandaloneHostPID`.
- Wiring in `backendapp/agentctl.go`, `agents.go`, and `main.go`: publish
  generation 1 after the fresh spawn or adoption.

## Exclusions

- Relaunch, availability changes, and the runtime flag (task 03).
- Execution generation checks (task 02).

## Acceptance conditions

1. After a second `Publish` in a test, the standalone control client, a
   host-utility request, and a newly created per-instance client all send the
   new token to the new port.
2. `Current()` never returns a mix of two generations under `go test -race`
   with concurrent readers and publishers.
3. Existing standalone executor, host-utility, and backendapp tests pass
   unchanged apart from constructor wiring.

## Verification

```bash
cd apps/backend && go test -race ./internal/agent/runtime/agentctl/... ./internal/agent/runtime/lifecycle/... ./internal/agent/hostutility/... ./internal/backendapp/... -count=1
cd apps/backend && golangci-lint run ./... --new-from-rev=main --timeout=5m
```

## Likely files

- `apps/backend/internal/agent/runtime/agentctl/endpoint.go` (new) and test
- `apps/backend/internal/agent/runtime/agentctl/control.go`
- `apps/backend/internal/agent/runtime/lifecycle/executor_standalone.go`
- `apps/backend/internal/agent/hostutility/manager.go`
- `apps/backend/internal/backendapp/agentctl.go`, `agents.go`, `main.go`

## Risks

- `ControlClient.SetAuthToken` races with in-flight requests today. The new
  constructor must not swap `http.Client.Transport`; read the token inside the
  round tripper.
- Adoption and remote executors keep their existing client options.

---
name: pr-fixup
description: Wait for CI and automated reviews on a PR, fix valid failures and comments in the primary conversation, verify, and push.
---

# PR Fixup

Use this workflow directly in the user-started primary conversation. Do not
launch a verifier, implementer, or other remediation subagent. A read-only
`pr-poller` is the sole exception: launch it only when the user explicitly asks
to wait for or monitor PR updates. For a cost-controlled workflow, the user may
switch the same conversation to the lower-cost implementation/test model before
starting CI remediation.

Use `gh` by default; when it is unavailable after access is approved, use an
available GitHub integration. `scripts/pr-state`, `scripts/pr-resolve`, and
`scripts/run-quiet` are at the worktree root.

## Pipeline

Create a visible checklist:

1. Gather PR state
2. Fix failing CI checks
3. Triage review comments
4. Address valid comments
5. Commit, rerun affected checks, and push
6. Re-check the new head
7. Report

## 1. Gather PR State

Before the first GitHub helper call, request any runtime network approval that
the environment requires. If access is denied, cancelled, or interrupted, stop
the workflow permanently; retry only transient fetch failures after access is
approved.

Run `scripts/pr-state --summary <PR>` once. Record `checks_head_sha`,
`checks_snapshot_complete`, `failed_checks`, `pending_checks`,
`unresolved_review_thread_count`, `hidden_unresolved_threads`, and
`actionable_issue_comment_count`. Inspect mergeability separately through
`references/merge-conflicts.md`; it is not a `pr-state --summary` field. If a
named reviewer is the semantic evidence source, use `--trusted-reviewer` only
when `review_evidence.trusted_producer` is `"true"`; never use that shortcut
for forks, security, or architecture.
Treat `trusted_producer=true` as qualifying provenance only for the dedicated OpenCode App, never merely because a reviewer name matches.
If `hidden_unresolved_threads` is non-empty, immediately run
`scripts/pr-resolve list <PR>` and triage its output. A zero current-head
unresolved count does not make hidden threads clean.

For pending CI, do not run a rapid polling loop. Wait at a reasonable interval,
then run the same summary again. Stop after about 20 minutes and report the
exact pending checks as "CI in progress." If the user specifies a fixed
monitoring duration, remain in this direct loop until that duration elapses or
the PR reaches a terminal clean/failed state; do not return an early status.

Treat the state as clean only when the current head has no failed or pending
checks, no merge conflict, no actionable review thread or issue comment, and
qualifying exact-head semantic evidence where PR delivery requires it.

## 2. Fix CI Failures

Before changing code, confirm every reported failed check, its `run_id`, and
the parent workflow/job status. A failed job can be visible while its workflow
is still in progress; confirm its conclusion and failing step before treating
it as reproducible code evidence. Use
`scripts/run-quiet gh-run -- gh run view <run-id> --log-failed` so large logs do
not flood the conversation. If logs are temporarily unavailable, use the
fallback in `references/ci-troubleshooting.md`. Reproduce the exact failed command where possible;
CI-specific Go lint often needs `golangci-lint run ./... --new-from-rev=<base>
--timeout=5m`.

Fix with `/tdd` or `/e2e` as applicable, run focused checks, and keep each
remediation scoped to the reported failure. Do not suppress a failure or mark a
check clean without fresh evidence.

## 3. Triage And Address Reviews

Use `scripts/pr-resolve list <PR>` to obtain unresolved threads. For each
comment, decide whether it is valid, already addressed, a preference, or wrong
for this codebase. Validate against the current head, the spec, and existing
architecture before editing or replying.

Make only valid changes. For an invalid comment, reply with concrete reasoning
only when the user asks to respond. Resolve a thread only when the change or
response genuinely addresses it.

## 4. Commit, Verify, Push

Commit through `/commit`, then rerun only the unit, integration, or E2E command
affected by the remediation. Push when that targeted check passes for the exact
current `HEAD`. Run broad `/verify` only if the user explicitly requests it or
the PR/CI finding requires it.

## 5. Re-check

After every push, run `scripts/pr-state --summary <PR>` again for the new head.
Require `checks_head_sha` to match that head, report pending checks separately
from failures, and rerun `scripts/pr-resolve list <PR>` before declaring the
PR clean. Treat prior review evidence as stale. A duplicate or stale bot thread
still needs an explicit reply and resolution once current source proves the
finding is already fixed, including a thread surfaced only in
`hidden_unresolved_threads`; only current-head actionable threads drive code
changes. Declare the PR clean only when `failed_checks=[]`, `pending_checks=[]`,
there is no merge conflict, and `scripts/pr-resolve list <PR>` is empty. Within
the user's monitoring limit, continue checking after resolutions until automated
review jobs are terminal; otherwise report the exact pending check names.

## 6. User-Requested Merge

Merge only after the user explicitly asks and the current-head state is clean.
From a linked worktree, run `gh pr merge <PR> --squash` without
`--delete-branch`: that flag can attempt a local checkout of the base branch
and fail when another worktree owns it, even after the remote merge succeeds.
Report the remote merge separately. Delete a remote or local branch only when
requested and through a worktree-safe cleanup flow.

## Guardrails

- Do not create Kandev subtasks unless the user explicitly asks for task
  tracking.
- Do not use native delegation or a full-history context fork to poll CI.
- Do not push, post comments, or resolve threads when the user asked for review
  only.
- Do not proceed with an unverified PR when mandatory verification is blocked.

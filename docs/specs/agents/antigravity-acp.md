---
status: building
created: 2026-08-15
owner: private deployment
---

# Antigravity ACP Agent Controls

## Why

Kandev can start Google Antigravity through the local `agy-acp` bridge and
already discovers its models and restores ACP sessions. The integration is not
yet natural to operate: users cannot select `agy`'s start mode or effort from
an agent profile, the only working approval bypass is an undocumented
`AGY_EXTRA_ARGS` environment workaround, and `agy` shell calls report
`CommandLine` rather than the `command` field Kandev displays. The latter made
valid commands appear blank in task chat.

This feature turns those existing `agy` capabilities into explicit, profile
scoped Kandev controls. It preserves the local-only boundary: Kandev does not
provision Antigravity credentials or claim support for remote executors.

## What

- The `agy-acp` bridge advertises the two observed `agy` start modes as ACP
  modes: `accept-edits` and `plan`. A new ACP session starts in `accept-edits`.
  Changing the selected mode applies `agy --mode <id>` to later prompts in that
  session and survives bridge restart/session restore.
- The bridge advertises an ACP `select` configuration option named `effort`
  with the observed values `low`, `medium`, and `high`. The bridge applies a
  selected value as `agy --effort <value>` for later prompts and persists it
  with the session. The existing model option remains available and unchanged.
- Kandev profiles expose explicit, opt-in curated command switches for
  `--dangerously-skip-permissions` and `--sandbox`. These switches are passed
  to the bridge process, which maps them to the child `agy` invocation. They
  are not represented by Kandev's deprecated global dangerous-permission
  field, because that field is not exposed in the current profile editor.
- `AGY_EXTRA_ARGS` remains backward compatible for existing private profiles,
  but is documented as a compatibility escape hatch rather than the normal
  way to configure permission behavior. Profile-curated switches are the
  supported path for new or edited profiles.
- The bridge normalizes `agy` tool parameters before emitting ACP updates. In
  particular, `CommandLine` becomes `command` and `Cwd` becomes `cwd` while
  retaining the original raw parameter fields for diagnostics. Kandev's
  existing generic ACP shell normalization then renders the command and
  working directory without an Antigravity-specific UI branch.

## Data model

`~/.openab/agy-acp/sessions.json` extends each stored session binding with
optional `mode_id` and `effort` fields. Missing fields from existing files
mean the defaults (`accept-edits` and no explicit effort flag) and must remain
loadable. No Kandev database migration is needed: existing profile `mode`,
`config_options`, and curated CLI-flag persistence remain the source of truth.

## ACP contract

On `session/new`, `session/load`, and config/mode updates, the bridge returns
the existing `models` and `configOptions` fields plus:

```json
{
  "modes": {
    "currentModeId": "accept-edits",
    "availableModes": [
      { "id": "accept-edits", "name": "Accept edits" },
      { "id": "plan", "name": "Plan" }
    ]
  },
  "configOptions": [
    { "id": "model", "category": "model", "type": "select" },
    {
      "id": "effort",
      "name": "Effort",
      "category": "general",
      "type": "select",
      "currentValue": "medium",
      "options": ["low", "medium", "high"]
    }
  ]
}
```

The exact option DTO remains compatible with Kandev's existing generic ACP
normalizer. The bridge accepts both the snake_case and camelCase ACP method
spellings already used for model configuration. Unknown mode or effort values
return a JSON-RPC invalid-parameter error without changing the session.

## Profile and permission behavior

- `accept-edits` is the safe operational default for Kandev implementation
  tasks. `plan` is available when a user deliberately wants no edits from the
  Antigravity session.
- `--dangerously-skip-permissions` is an explicit high-risk opt-in for trusted
  local worktrees only. It does not grant permissions to Kandev itself and it
  must be visibly described as bypassing Antigravity confirmations.
- `--sandbox` is an explicit child-CLI mode request. Kandev does not claim a
  stronger isolation guarantee than the installed `agy` version provides.
- If both an explicit bridge switch and `AGY_EXTRA_ARGS` specify the same
  child flag, the bridge must avoid producing contradictory flags. The
  explicit profile switch is authoritative for supported boolean controls.

## Scenarios

### Profile start mode

Given an available Antigravity ACP profile, when its capability probe runs,
then Kandev shows `accept-edits` and `plan` through the existing generic mode
control. When the profile selects `plan` and starts a task, the first and all
later prompts execute `agy --mode plan`.

### Profile effort

Given an Antigravity ACP profile, when the user selects `high` effort through
the existing dynamic ACP option editor, then a new task session invokes `agy
--effort high`. When the session resumes after the bridge process restarts, the
next prompt retains the same effort selection.

### Explicit trusted-worktree permissions

Given a profile with the curated skip-permissions switch enabled, when Kandev
starts `agy-acp`, then the bridge receives `--dangerously-skip-permissions`
and invokes the child `agy` with that flag. A profile without the switch must
not receive the flag. The profile editor describes the risk before the switch
can be selected.

### Shell command visibility

Given `agy` emits a `run_command` tool call with `CommandLine` and `Cwd`, when
the bridge sends its ACP tool update, then the raw input also contains
`command` and `cwd` with the same values. Kandev renders the shell command and
working directory through its standard ACP path.

### Existing sessions and profiles

Given a session file created before these fields existed, when it loads, then
it restores successfully with the documented defaults. Given an existing
profile that uses `AGY_EXTRA_ARGS`, when it runs after this change, then its
existing non-conflicting extra arguments still reach `agy`.

## Failure modes

- If the local `agy` executable is unavailable, capability discovery remains
  unavailable and no empty synthetic model or mode is persisted.
- If `agy` rejects a mode, effort, sandbox, or permission flag at prompt time,
  the bridge returns the sanitized subprocess failure through the existing ACP
  error path; it does not falsely report a successful tool call.
- Invalid ACP mode/effort update requests leave persisted and in-memory state
  unchanged.
- A failed atomic write of the session file may lose the latest optional mode
  or effort selection, but must not corrupt a previously readable file or the
  established conversation binding.
- Raw tool input that is absent or not an object remains pass-through; the
  normalizer must not panic or invent a command.

## Persistence guarantees

- Session conversation ID, step index, model, mode, and effort are written
  together using the bridge's existing lock and atomic rename process.
- Older session files remain readable without migration.
- Kandev profile settings remain independent of bridge session persistence;
  creating a new task reapplies the profile's selected mode/options/CLI flags.

## Out of scope

- Per-session MCP server injection or claiming that `agy plugin` is an ACP
  MCP transport.
- Token, cost, or quota accounting. The observed `agy --output-format
  stream-json` result payload does not yet supply a stable usage schema.
- Remote, Docker, or SSH executor installation and credential provisioning.
- A custom Antigravity-only React editor. Kandev's generic ACP mode, config,
  and curated CLI-flag UI is the intended surface.
- Changing the semantics of the generic Kandev auto-approve setting.

## Success criteria

- A locally installed `agy-acp` profile exposes selectable Start Mode and
  Effort in Kandev without a provider-specific frontend implementation.
- Saved mode and effort affect the launched child `agy` command and survive
  session restore.
- Profile users can deliberately select sandbox and skip-permissions behavior
  without relying on an opaque environment variable.
- Antigravity shell tool calls show their command and working directory in the
  existing task chat.
- Unit and contract tests cover valid values, invalid values, legacy session
  restore, child argument construction, curated flag propagation, and command
  normalization.

## Implementation plan

See [the implementation plan](../../plans/antigravity-acp-controls/plan.md).

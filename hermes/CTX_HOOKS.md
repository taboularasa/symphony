# Hermes ctx And Workspace Hooks

Linear issue: HAD-662

`hermes/WORKFLOW.md` configures a `before_run` hook for Hermes-owned work. The
hook is deliberately narrow: it validates the workspace boundary before a
worker starts and does not read private ctx task payloads.

## What The Hook Checks

- The repository origin must be `taboularasa/hermes-agent` or
  `taboularasa/phoneitin`.
- The workspace root must be either a ctx/Hermes worktree path ending in one of
  those repos or an explicitly allowed canonical host checkout.
- Nested Hermes worktrees are rejected before broader allow-list matching.
- The hook runs with `hooks.timeout_ms: 60000` so a stuck shell command cannot
  hang dispatch.

## ctx Observation Boundary

The hook observes ctx binding only through safe execution facts:

- current working directory;
- `git rev-parse --show-toplevel`;
- `git remote get-url origin`;
- optional `ctx --help` availability.

It does not read ctx state databases, session bindings, task prompts, auth
files, Linear comments, Slack payloads, or token-bearing environment values.
Missing `ctx` CLI metadata is not fatal; Symphony may still run against an
allowed canonical checkout or already-prepared worktree.

## Test Coverage

Go tests in `internal/workflow/hooks_test.go` execute the checked-in hook and
cover:

- allowed Hermes ctx-style worktree with no `ctx` command on `PATH`;
- denied repository origin;
- nested Hermes worktree rejection;
- hook timeout propagation;
- hook stderr and non-zero exit propagation.

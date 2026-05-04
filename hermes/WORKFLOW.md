---
tracker:
  kind: linear
  endpoint: https://api.linear.app/graphql
  api_key: "$HERMES_LINEAR_TOKEN"
  project_slug: shared-agents
  owner_label: "owner:hermes"
  claim_assignee: "hermes-bot"
  require_claim_before_dispatch: true
  active_states: ["Todo", "In Progress"]
  terminal_states: ["Done", "Canceled", "Cancelled", "Duplicate"]
agent:
  max_concurrent_agents: 3
  max_concurrent_agents_by_state:
    "in progress": 2
codex:
  approval_policy: on-failure
  thread_sandbox: workspace-write
workspace:
  root: /home/david/stacks
hooks:
  timeout_ms: 60000
  before_run: |
    set -euo pipefail
    if command -v ctx >/dev/null 2>&1; then
      ctx --help >/dev/null 2>&1 || true
    fi

    origin="$(git remote get-url origin 2>/dev/null || true)"
    case "$origin" in
      *taboularasa/hermes-agent*|*taboularasa/phoneitin*) ;;
      *) echo "unexpected repository origin: $origin" >&2; exit 1 ;;
    esac

    root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
    case "$root" in
      */.hermes/worktrees/*/.hermes/worktrees/*) echo "nested Hermes worktree is not allowed: $root" >&2; exit 1 ;;
      */.hermes/worktrees/*/hermes-agent|*/.hermes/worktrees/*/phoneitin|/home/david/stacks/hermes-agent|/home/david/stacks/phoneitin|/home/david/code/phoneitin) ;;
      *) echo "unexpected workspace root: $root" >&2; exit 1 ;;
    esac
---
# Hermes Execution Manager

Hermes is the owner-label-gated execution manager for Hadto work that is
explicitly labeled `owner:hermes`. Symphony is the scheduler. Hermes receives
only work that passes the Linear tracker contract in this file and must treat
that contract as the source of truth for whether it may inspect, claim, or
dispatch an issue.

## Operating Contract

- Work only on issues returned by Symphony for `owner:hermes` in the
  `shared-agents` project and active states listed in front matter.
- Require the Linear claim gate before dispatch. The expected assignee identity
  is `hermes-bot`; do not launch implementation work unless the claim result is
  `claim_win` or already confirmed as self.
- Treat `owner:denovo`, `owner:human`, `owner:triage`, unlabeled issues, and
  issues with conflicting `owner:*` labels as outside Hermes ownership. This is
  a positive owner-label rule, not a project-name denial list.
- Do not assign, delegate, close, or move an issue unless the workflow prompt,
  current issue text, and available tools make that writeback part of the task.
- Keep comments compact and evidence-bearing: branch or PR links, commands run,
  verification result, blocked dependency, and next action.

## Engineering-Manager Behavior

When `codex_delegate` or another bounded local Codex backend is available, act
as the engineering manager for concrete implementation work:

- Start one issue-scoped run with an external key derived from the Linear issue.
- Keep the worker scope narrow, monitor status, and resume or correct the same
  run rather than spawning duplicate work for the same issue.
- Inspect worker output and repository state before reporting success.
- Fall back to direct execution only when the delegated backend is unavailable
  or the task is small enough that delegation would add more risk than value.
- Preserve dirty worktrees by inspecting them first; never delete or overwrite
  unrelated user changes.

## Workspace And ctx Contract

- Use the ctx-managed worktree as the source of truth when ctx binding is
  present.
- Do not create a nested Hermes worktree.
- Keep software project checkouts under `/home/david/stacks` unless the issue
  names a different absolute path.
- Before editing, confirm the repository origin and current branch match the
  issue target.
- Finish with a clean git state or an explicit handoff that names the branch,
  commit, PR, and remaining blocker.

## Runtime Boundaries

- Hermes Slack on this host stays in native Slack Socket Mode. Do not configure
  webhook/WASM Slack for live messaging as part of this workflow.
- Never copy raw tokens, API keys, private logs, or secret-bearing config values
  into Linear comments, pull requests, or workflow output.
- Do not use project-name negative lists to decide ownership. Visibility is
  controlled by `owner:hermes`, the claim-assignee preflight, and Symphony
  candidate filtering.
- Keep proactive Slack messages, cron reports, and watcher alerts separate from
  issue dispatch unless a Linear issue explicitly asks for that surface.

## Verification And Handoff

For every implementation issue:

- Start from clean relevant checkouts and green baseline checks.
- Add or update tests for the changed behavior before broad refactors.
- Run focused checks for the touched package and broader checks when shared
  behavior, safety policy, or runtime dispatch changes.
- Report exact commands and summarized results. If a check cannot run, leave the
  issue or checklist item unclosed and state the blocker.
- Prefer durable artifacts over status narration: commit SHA, PR URL, Linear
  comment, saved log path, or documented operational proof.

# Symphony

Symphony turns project work into isolated, autonomous implementation runs so
teams can manage work instead of supervising coding agents.

This fork is the Hadto Go implementation track. New implementation code in this
repository must be written in Go.

The upstream Elixir reference implementation is intentionally not vendored here.
Use the upstream repository as historical reference only:

> https://github.com/openai/symphony/tree/main/elixir

## Repository Layout

- `SPEC.md`: language-agnostic Symphony contract.
- `rfcs/`: Hadto extension RFCs and design decisions.
- `.github/`: pull request template and lightweight CI.
- `.codex/`: repo-local agent workflow helpers.

The Go implementation packages will be added at the repository root as Phase 1
work lands.

## Implementation Direction

- Build the runtime in Go only.
- Keep the spec and RFCs as the source of truth for behavior.
- Do not reintroduce local Elixir implementation code or Elixir validation
  gates.

## Phase 2 Linear Owner/Claim Adapter

HAD-661 implements the Phase 2 Linear owner/claim safety layer in Go. The
upstream Elixir implementation remains historical reference only; operator
examples and runtime code in this repository should target the Go packages under
`internal/`.

`WORKFLOW.md` tracker front matter can opt into the owner and claim extensions:

```yaml
tracker:
  kind: linear
  endpoint: https://api.linear.app/graphql
  api_key: "$HERMES_LINEAR_TOKEN"
  project_slug: 6a6a965c3d10
  owner_label: "owner:hermes"
  claim_assignee: "hermes"
  claim_target: "delegate"
  require_claim_before_dispatch: true
  active_states: ["Todo", "In Progress"]
  terminal_states: ["Done", "Canceled", "Cancelled", "Duplicate"]
```

Omitting `owner_label`, `claim_assignee`, and
`require_claim_before_dispatch` preserves legacy discovery behavior: project and
active-state filtering only, no owner-label filter, no Linear assignee mutation,
and no claim gate.

`project_slug` maps to Linear `Project.slugId`, which is the URL suffix such as
`6a6a965c3d10`, not the human-readable project name. When `owner_label` is set,
candidate discovery adds the owner label to the same Linear GraphQL filter
object as project slugId and active states, then defensively post-filters
returned issues. Issues missing the configured owner label, carrying a different
`owner:*` label, or carrying conflicting owner labels are not dispatchable for
that owner.

When `require_claim_before_dispatch: true`, `claim_assignee` must resolve to one
active Linear user. The resolver supports `me`, a Linear UUID, email, `name`, or
`displayName`. `claim_target` defaults to `assignee` for human-style bot users.
For Linear OAuth app actors such as Hermes, set `claim_target: delegate` so
Symphony preserves the human assignee and confirms the Linear `delegate` field
instead. Before launch, Symphony reads the current issue, claims it through
`issueUpdate`, re-fetches the issue, and proceeds only when the configured target
field confirms self. Terminal-state handling does not automatically unset the
Linear assignee or delegate.

Claim outcomes use stable reason codes:

- `claim_win`
- `claim_loss_other_agent`
- `claim_loss_human`
- `claim_cleared`
- `claim_error`

The Go observer surface emits one structured `linear_claim_outcome` event per
claim attempt. Events include `linear_id`, `issue_id`, `reason_code`, outcome,
dispatchable, and retryable fields, and omit issue descriptions, comments,
tokens, API keys, raw responses, and response bodies. In-memory counters expose
`claim_attempts`, `claim_wins`, `claim_losses`, and `claim_errors`.

Fixture-backed contention proof lives in the Go test suite. The live Linear
canary proof is disabled unless all required env vars are explicit:

```bash
SYMPHONY_LINEAR_LIVE_TEST=1 \
HERMES_LINEAR_TOKEN=... \
SYMPHONY_LINEAR_CANARY_ISSUE_ID=... \
SYMPHONY_LINEAR_SELF_USER_ID=... \
go test ./internal/linear -run TestLiveLinearClaimIntegrationRequiresExplicitEnv -count=1 -v
```

This branch does not cut over production Hermes. HAD-662 owns prompt migration
and soak/cutover work after bot-token and canary setup are complete.

## Hermes Workflow Migration

The Hermes execution policy now lives in
[`hermes/WORKFLOW.md`](hermes/WORKFLOW.md). That file is the Go-track Symphony
source of truth for Hermes-owned Linear work. The supporting migration notes are
in [`hermes/PROMPT_TRACE.md`](hermes/PROMPT_TRACE.md),
[`hermes/CTX_HOOKS.md`](hermes/CTX_HOOKS.md), and
[`hermes/ROLLBACK.md`](hermes/ROLLBACK.md).

Required operator environment:

- `HERMES_LINEAR_TOKEN`: Linear token for the active `hermes` claim identity.
- `#agents-bridge` and bot membership from HAD-658 before live smoke tests.
- Existing native Slack Socket Mode credentials stay with
  `hermes-gateway.service`; this workflow does not configure webhook/WASM
  Slack.

Required workflow fields:

```yaml
tracker:
  project_slug: 6a6a965c3d10
  owner_label: "owner:hermes"
  claim_assignee: "hermes"
  claim_target: "delegate"
  require_claim_before_dispatch: true
migration:
  legacy_loop_mode: disabled
  legacy_loop_mutates_linear: false
  shadow_mode: false
```

Live cutover gates:

- HAD-659 owner-label backfill is complete.
- HAD-661 owner-label filtering and claim-assignee preflight are merged in Go.
- HAD-658 must provide the bot/channel prerequisites before live polling proof.
- A controlled polling cycle must show only `owner:hermes` issues, a confirmed
  `claim_win` or already-self claim, and no `owner:denovo` or `owner:human`
  dispatch candidate.
- The legacy Hermes issue-selection loop is not disabled until the smoke proof
  passes. `hermes-gateway.service` remains native Slack Socket Mode.

Soak proof for closing HAD-662 requires 24 hours with zero double-claims, zero
non-Hermes project pickups, and no false watcher alerts. The paired Hermes code
change that removes the old project-name ownership block is
[`taboularasa/hermes-agent` PR #100](https://github.com/taboularasa/hermes-agent/pull/100).

---

## License

This project is licensed under the [Apache License 2.0](LICENSE).

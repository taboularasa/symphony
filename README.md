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
  project_slug: symphony
  owner_label: "owner:hermes"
  claim_assignee: "hermes-bot"
  require_claim_before_dispatch: true
  active_states: ["Todo", "In Progress"]
  terminal_states: ["Done", "Canceled", "Cancelled", "Duplicate"]
```

Omitting `owner_label`, `claim_assignee`, and
`require_claim_before_dispatch` preserves legacy discovery behavior: project and
active-state filtering only, no owner-label filter, no Linear assignee mutation,
and no claim gate.

When `owner_label` is set, candidate discovery adds the owner label to the same
Linear GraphQL filter object as project slug and active states, then defensively
post-filters returned issues. Issues missing the configured owner label, carrying
a different `owner:*` label, or carrying conflicting owner labels are not
dispatchable for that owner.

When `require_claim_before_dispatch: true`, `claim_assignee` must resolve to one
active Linear user. The resolver supports `me`, a Linear UUID, email, `name`, or
`displayName`. Before launch, Symphony reads the current issue, assigns
unassigned or self-assigned issues with `issueUpdate(... assigneeId ...)`,
re-fetches the issue, and proceeds only when the confirmed assignee ID is self.
Terminal-state handling does not automatically unset the Linear assignee.

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

---

## License

This project is licensed under the [Apache License 2.0](LICENSE).

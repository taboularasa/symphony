# RFC 0002: `tracker.claim_assignee`

Status: Draft
Linear issue: HAD-656

## Summary

Add two optional workflow front matter fields that let an owner-scoped
orchestrator require a Linear assignee claim before dispatch:

- `tracker.claim_assignee`
- `tracker.require_claim_before_dispatch`

`tracker.owner_label` from RFC 0001 controls which issues an orchestrator can
see. This RFC defines the second layer: before a worker launches, the
orchestrator must prove that the same Linear issue is assigned to the configured
bot identity.

This is a Phase 0 RFC only. It defines the SPEC extension and expected tracker
behavior, but does not require runtime mutation code in this PR.

## Motivation

Symphony already has internal `claimed` and `running` state. SPEC sections 7.1
and 7.4 require those checks before launching a worker, and they prevent
duplicate dispatch inside one orchestrator process.

That state is not a distributed lock. Two orchestrators polling the same Linear
project can both fetch a matching issue before either process updates its own
memory. Restart recovery is also tracker-driven and filesystem-driven rather
than backed by a durable scheduler database, so any cross-process coordination
must live in the shared tracker.

The Symphony project "Layer 2" plan uses Linear assignment as that shared claim.
Linear is already the source of truth for issue ownership, assignments survive
process restarts, and assignment changes are visible to humans and agents. This
keeps Symphony aligned with SPEC section 11.5: the core orchestrator remains a
scheduler/runner and tracker reader, while implementations that opt into this
extension add the smallest required tracker write for dispatch safety.

## Field Schema

### `tracker.claim_assignee`

Type: nullable string.

Default: `null`.

Required: optional for core Symphony compatibility. Required only when
`tracker.require_claim_before_dispatch == true`.

Meaning: the Linear bot/user identity that must hold the issue assignee field
for dispatch eligibility. The value is a stable configured handle such as
`hermes-bot` or `denovo-bot`; implementations resolve it to the current Linear
user ID before attempting a claim.

Empty string: invalid when the key is present. Use omission or `null` to disable
the claim-assignee extension.

Normalization:

1. Trim leading and trailing whitespace.
2. Preserve internal case for display only.
3. Compare the configured value by resolved Linear user ID after lookup.
4. Reject unresolved, suspended, or deactivated users as fatal config errors.

`claim_assignee` is a single value, not a list. A list would allow ambiguous
lock ownership and different runtimes could make different winner choices.

### `tracker.require_claim_before_dispatch`

Type: boolean.

Default: `false`.

Meaning: when `true`, dispatch is allowed only after the orchestrator has
confirmed that the issue assignee is the resolved `tracker.claim_assignee` user
ID. When `false`, older workflows keep legacy behavior and an unassigned issue
can still dispatch.

Dynamic reload:

- Changes apply to future dispatch cycles.
- Implementations SHOULD re-read effective config before claim and immediately
  before worker launch, matching SPEC section 6.2 defensive reload guidance.
- In-flight workers are not required to restart automatically on config change.

## Back Compatibility

If both fields are omitted, set to `null` where applicable, or unsupported by an
older implementation, dispatch remains the current behavior:

- Candidate polling uses project, active state, and any implemented
  `tracker.owner_label` filter.
- No Linear assignee mutation is attempted by the scheduler.
- `claimed` and `running` remain process-local scheduler state.

If `require_claim_before_dispatch` is `false`, `claim_assignee` MAY still be
configured for future rollout, but it is not a dispatch precondition.

Invalid combinations:

- `require_claim_before_dispatch: true` with omitted or null `claim_assignee`.
- `claim_assignee: ""`.
- `claim_assignee` resolving to no Linear user.
- `claim_assignee` resolving to a suspended or deactivated Linear user.
- `claim_assignee` configured as a list or map.

## Linear Claim Mechanics

Linear's public GraphQL API supports the `issueUpdate(id, input)` mutation, and
the developer docs show issue updates using the issue UUID or shorthand
identifier. Linear also exposes assignee data on issues. The docs reviewed for
this RFC do not document a native compare-and-set or expected-version field on
`issueUpdate`; implementations MUST NOT assume one exists unless they verify it
against the current public schema.

The protocol is therefore read-mutate-confirm:

1. Resolve `tracker.claim_assignee` to one Linear user ID for the current
   workspace.
2. Discover candidates matching:
   - `tracker.project_slug`
   - `tracker.active_states`
   - `tracker.owner_label`, when RFC 0001 is implemented
   - `assignee == null OR assignee.id == self_bot_user_id`
3. For an unassigned candidate, call `issueUpdate` with
   `input: { assigneeId: self_bot_user_id }`.
4. Handle GraphQL `errors` before reading any returned data, even when the HTTP
   status is 200.
5. Re-fetch the issue by ID.
6. Proceed only if the confirmed assignee ID equals `self_bot_user_id`.

Example mutation shape:

```graphql
mutation SymphonyClaimIssue($issueId: String!, $assigneeId: String!) {
  issueUpdate(id: $issueId, input: { assigneeId: $assigneeId }) {
    success
    issue {
      id
      identifier
      assignee {
        id
        name
      }
      updatedAt
    }
  }
}
```

Implementations SHOULD inspect the current Linear schema before shipping runtime
code. If the current schema exposes a documented conditional update mechanism,
the implementation MAY use it, but the confirm re-fetch remains required.

## Race Window Analysis

Without a documented compare-and-set primitive, two claimers can race between
their read and mutation calls. The safety rule is that mutation success alone is
not authority. The post-mutation re-fetch is authority.

Outcomes:

- Self wins: confirm returns `assignee.id == self_bot_user_id`; dispatch may
  continue after all other preflight checks pass.
- Other agent wins: confirm returns another allowed bot user; release the
  in-memory claim and skip dispatch.
- Human wins: confirm returns a non-bot user; release the in-memory claim and
  treat the issue as human-owned until assignment changes.
- Assignee is cleared: re-run the claim protocol on a later tick; do not launch
  from the stale local observation.
- Linear returns GraphQL errors or an unexpected payload: do not dispatch.

This protocol is deterministic because every implementation uses the same final
predicate: confirmed Linear assignee ID equals self.

## Dispatch and Preemption Rules

Dispatch preflight in SPEC section 6.3 gains this extension check when
`require_claim_before_dispatch == true`:

- Claim acquired or already held by self.

Eligibility table:

| Observed assignee | `require_claim_before_dispatch` | Dispatch behavior |
| --- | --- | --- |
| `null` | `false` | Legacy eligible after normal preflight. |
| `null` | `true` | Attempt claim, then confirm before dispatch. |
| self bot | `true` | Eligible after confirm re-fetch. |
| other agent bot | `true` | Ineligible; release local claim. |
| human user | `true` | Ineligible; treat as human-owned. |
| deactivated configured bot | `true` | Fatal config error; do not dispatch. |

An implementation MUST re-check the confirmed assignee immediately before
launching a worker. If assignment changes after workspace preparation but before
launch, cancel dispatch and release the in-memory claim.

If assignment changes while a worker is running, the next reconciliation or
worker issue-state check SHOULD treat it as preemption:

- Stop launching continuation turns.
- Let the active turn exit gracefully when practical.
- If the runtime supports cancellation, cancel with a clear
  `CanceledByReconciliation` reason.
- Do not overwrite the new assignee.

## Recovery and Release Semantics

On restart, the orchestrator has no durable internal claim database. It MUST use
Linear and the filesystem, following SPEC section 7.4.

Recovery behavior:

- Issues assigned to self and still in active states MAY be treated as
  self-claimed candidates.
- Issues assigned to another agent are not eligible.
- Issues assigned to humans are not eligible.
- Issues in terminal states are not eligible even if still assigned to self.
- Workspaces for terminal issues continue to follow normal startup terminal
  cleanup rules.

Stale self-claims:

- A self-assigned active issue is not stale by assignment alone.
- Staleness requires additional evidence, such as no running workspace, no
  recent progress, and a deployment-defined age threshold.
- Reclaiming or clearing another agent's assignment is out of scope for this
  RFC and should be an operator or future policy action.

Release policy:

- Default release leaves `assigneeId` untouched.
- `Succeeded`, `Failed`, `Stalled`, `RetryQueued`, and `Released` do not
  automatically unset the Linear assignee.
- The coding agent or workflow-specific tooling remains responsible for issue
  comments, state transitions, PR metadata, and any explicit assignee reset.

This preserves the SPEC section 11.5 boundary. A future implementation issue may
add a scheduler-owned assignee release, but that must be a separate extension
with its own idempotency and audit rules.

## Examples

### Hermes

```yaml
tracker:
  kind: linear
  project_slug: shared-agents
  active_states: ["Todo", "In Progress"]
  owner_label: "owner:hermes"
  claim_assignee: "hermes-bot"
  require_claim_before_dispatch: true
```

### De Novo

```yaml
tracker:
  kind: linear
  project_slug: shared-agents
  active_states: ["Todo", "In Progress"]
  owner_label: "owner:denovo"
  claim_assignee: "denovo-bot"
  require_claim_before_dispatch: true
```

### Legacy Unclaimed Dispatch

```yaml
tracker:
  kind: linear
  project_slug: single-agent-project
  require_claim_before_dispatch: false
```

### Invalid

```yaml
tracker:
  kind: linear
  project_slug: shared-agents
  require_claim_before_dispatch: true
```

Invalid because no `claim_assignee` is configured.

```yaml
tracker:
  kind: linear
  project_slug: shared-agents
  claim_assignee: ["hermes-bot", "denovo-bot"]
  require_claim_before_dispatch: true
```

Invalid because `claim_assignee` must be one value.

## SPEC Diff Snippets

### Section 5.3.1 `tracker`

```diff
 #### 5.3.1 `tracker` (object)

 Fields:

 - `kind` (string)
   - REQUIRED for dispatch.
   - Current supported value: `linear`
 - `endpoint` (string)
   - Default for `tracker.kind == "linear"`: `https://api.linear.app/graphql`
 - `api_key` (string)
   - MAY be a literal token or `$VAR_NAME`.
   - Canonical environment variable for `tracker.kind == "linear"`: `LINEAR_API_KEY`.
   - If `$VAR_NAME` resolves to an empty string, treat the key as missing.
 - `project_slug` (string)
   - REQUIRED for dispatch when `tracker.kind == "linear"`.
+- `claim_assignee` (nullable string)
+  - Optional. Default: `null`.
+  - Resolves to the Linear user ID that must hold issue assignment before
+    dispatch when `require_claim_before_dispatch` is `true`.
+  - Must be one value; lists and maps are invalid.
+- `require_claim_before_dispatch` (boolean)
+  - Optional. Default: `false`.
+  - When `true`, the dispatcher must confirm that the issue is assigned to
+    `claim_assignee` before launching a worker.
 - `active_states` (list of strings)
   - Default: `Todo`, `In Progress`
 - `terminal_states` (list of strings)
   - Default: `Closed`, `Cancelled`, `Canceled`, `Duplicate`, `Done`
```

### Section 6.3 Dispatch Preflight Validation

```diff
 Validation checks:

 - Workflow file can be loaded and parsed.
 - `tracker.kind` is present and supported.
 - `tracker.api_key` is present after `$` resolution.
 - `tracker.project_slug` is present when REQUIRED by the selected tracker kind.
+- When `tracker.require_claim_before_dispatch == true`,
+  `tracker.claim_assignee` resolves to an active Linear user.
+- When `tracker.require_claim_before_dispatch == true`, claim acquired or
+  already held by self.
 - `codex.command` is present and non-empty.
```

### Section 6.4 Cheat Sheet

```diff
 - `tracker.kind`: string, REQUIRED, currently `linear`
 - `tracker.endpoint`: string, default `https://api.linear.app/graphql` when `tracker.kind=linear`
 - `tracker.api_key`: string or `$VAR`, canonical env `LINEAR_API_KEY` when `tracker.kind=linear`
 - `tracker.project_slug`: string, REQUIRED when `tracker.kind=linear`
+- `tracker.claim_assignee`: nullable string, optional, default `null`; the
+  Linear bot/user identity that must hold assignee before dispatch when claim
+  enforcement is enabled
+- `tracker.require_claim_before_dispatch`: boolean, optional, default `false`
 - `tracker.active_states`: list of strings, default `["Todo", "In Progress"]`
 - `tracker.terminal_states`: list of strings, default `["Closed", "Cancelled", "Canceled", "Duplicate", "Done"]`
```

## Open Questions

- Should runtime implementations resolve `claim_assignee` by Linear display
  name, email, external integration identity, or a deployment-local alias map?
- Should Symphony eventually add a first-class tracker write API for claiming,
  or should claim mutation remain extension-specific code beside the Linear
  adapter?
- What stale-window policy should a deployment use before alerting on a
  self-assigned active issue with no running workspace?
- Should a later release extension clear assignment on terminal states, or
  should the agent always own that tracker write?

## References

- SPEC.md sections 5.3.1, 6.2, 6.3, 6.4, 7.1, 7.4, 11.2, and 11.5.
- RFC 0001: `tracker.owner_label`.
- Symphony project "Layer 2 - A claim that lives in the tracker, not in memory."
- Linear developer documentation for GraphQL endpoint, authentication, standard
  GraphQL `errors` handling, and `issueUpdate(id, input)`.
- Linear assignment documentation: an issue has a single assignee; suspended
  users cannot be assigned.

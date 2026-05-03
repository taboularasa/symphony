# RFC 0001: `tracker.owner_label`

Status: Draft
Linear issue: HAD-651

## Summary

Add an optional `tracker.owner_label` workflow front matter field that lets one
Symphony orchestrator claim only the Linear issues carrying its owner label.
When set, candidate polling MUST require the configured owner label in addition
to the existing project and active-state filters.

This is a Phase 0 RFC only. It defines the SPEC extension and expected tracker
behavior, but does not require runtime code in this PR.

## Motivation

Multiple orchestrators can poll the same Linear project and active states. Today
the core selection boundary is project plus state, so two orchestrators can see
the same candidate issue unless they are separated by project or workflow state.

Owner labels create an explicit routing boundary that stays visible on the
issue itself:

- Hermes work can be labelled `owner:hermes`.
- De Novo work can be labelled `owner:denovo`.
- Human-only work can be labelled `owner:human`.
- Intake or routing work can be labelled `owner:triage`.

## Field Schema

Field: `tracker.owner_label`

Type: nullable string.

Default: `null`.

Required: optional for core Symphony compatibility. Workflows that are intended
to run as owner-scoped orchestrators SHOULD set it. An omitted or `null` field
means owner-label filtering is disabled and the orchestrator behaves as it does
today.

Empty string: invalid when the key is present. Use omission or `null` to disable
owner scoping.

Normalization:

1. Trim leading and trailing whitespace.
2. Lowercase ASCII letters.
3. Validate the normalized value against the reserved owner label values.
4. Store and compare only the normalized value.

Current valid values:

- `owner:hermes`
- `owner:denovo`
- `owner:human`
- `owner:triage`

Future `owner:*` values require a SPEC update or an explicitly versioned
extension. Unknown `owner:*` values MUST fail validation when configured as
`tracker.owner_label`.

## Back Compatibility

If `tracker.owner_label` is omitted, `null`, or unsupported by an older
implementation, candidate selection remains the current behavior:

- Linear project filter by `tracker.project_slug`.
- Linear state filter by `tracker.active_states`.
- No owner-label filter.
- No owner-label conflict validation.

This preserves existing workflows that were written before owner-scoped routing.

## Linear Filter Semantics

When `tracker.owner_label` is set, Linear candidate polling MUST add the owner
label requirement to the same GraphQL filter object used for project and state.
Linear developer documentation says fields in a filter are ANDed by default,
and the documented label relationship filter returns issues with at least one
matching label.

Required query shape:

```graphql
query SymphonyLinearPoll(
  $projectSlug: String!
  $stateNames: [String!]!
  $ownerLabel: String!
  $first: Int!
  $relationFirst: Int!
  $after: String
) {
  issues(
    filter: {
      project: { slugId: { eq: $projectSlug } }
      state: { name: { in: $stateNames } }
      labels: { name: { eq: $ownerLabel } }
    }
    first: $first
    after: $after
  ) {
    nodes {
      id
      identifier
      labels(first: $relationFirst) {
        nodes {
          name
        }
      }
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}
```

The owner label filter MUST be ANDed with existing project and active-state
filters. Implementations MUST NOT express owner ownership as an `or` branch with
state or project filters.

If a Linear API shape change makes `labels: { name: { eq: $ownerLabel } }`
invalid, the implementation MUST keep equivalent AND semantics. A label-ID
filter is acceptable only if it has the same behavior and preserves the absent
label rule below.

## Absent Label Behavior

For an owner-scoped orchestrator, an issue without the configured owner label
MUST NOT be returned from `fetch_candidate_issues()`.

This rule is stronger than trusting the remote filter alone. Implementations
SHOULD also normalize returned issue labels and discard any candidate that does
not contain the configured owner label, so API drift or query bugs do not cross
the owner boundary.

The same ownership check SHOULD apply when refreshing active issue state. If a
running issue no longer has the configured owner label, the orchestrator SHOULD
treat it like a no-longer-active issue for that owner and stop work according to
normal reconciliation rules.

## Reserved Namespace and Conflicts

The `owner:*` label namespace is reserved for Symphony ownership routing. It is
not a general tagging namespace.

An issue SHOULD have exactly one normalized `owner:*` label. The default policy
is:

- Require the orchestrator's configured owner label.
- Forbid any different normalized `owner:*` label on the same issue.
- Treat conflicting owner labels as a dispatch blocker, not as a reason to pick
  one arbitrarily.

Examples:

- `owner:hermes` on a Hermes-scoped orchestrator: eligible.
- no `owner:*` label on a Hermes-scoped orchestrator: ineligible.
- `owner:denovo` on a Hermes-scoped orchestrator: ineligible.
- `owner:hermes` plus `owner:denovo`: conflict, ineligible for both until
  resolved.

Duplicate applications of the same label are a tracker data anomaly. They MAY
be normalized to one value if Linear returns duplicate names.

## Label Management

Labels SHOULD be created at the Linear workspace level so all teams and project
views can use the same canonical owner labels. Linear's user-facing label docs
note that same-name team labels do not collapse to one API identity, so
workspace labels avoid ambiguous team-scoped duplicates.

Initial label creation MAY be done by an operator, admin, or a trusted Hermes
bootstrap workflow. The creator MUST create only the reserved values listed in
this RFC unless policy has approved a new value.

Mutation policy:

- Hermes is the default authority allowed to add, remove, or change `owner:*`
  labels on issues.
- Humans and other orchestrators MUST NOT mutate `owner:*` values except under
  explicit policy or break-glass operator action.
- A non-Hermes orchestrator MAY read `owner:*` labels for routing and conflict
  detection, but SHOULD NOT rewrite them.

Race rules:

1. Label mutation MUST be idempotent. Adding the already-present owner label is
   a no-op.
2. Before changing an issue from one owner to another, the mutator MUST re-read
   current labels and verify that the observed owner set matches the expected
   source owner.
3. If a different `owner:*` label appears between read and write, the mutator
   MUST stop and surface a conflict instead of overwriting it.
4. Dispatch SHOULD re-check labels immediately before launching a worker. If the
   label disappeared or a conflicting owner label appeared, skip dispatch.

## Validation Rules for SPEC Section 6.3

Add these dispatch preflight checks when the owner-label extension is
implemented:

- `tracker.owner_label`, when omitted or `null`, disables owner-label
  validation and filtering.
- `tracker.owner_label`, when present, is a string.
- After trimming and lowercasing, `tracker.owner_label` is non-empty.
- The normalized value is one of the reserved owner label values.
- The normalized value starts with `owner:` and uses only lowercase ASCII
  letters, digits, and hyphen after the prefix.
- For `tracker.kind == "linear"`, the candidate issue query includes the owner
  label filter in the same ANDed filter object as project and state.

Runtime candidate validation:

- Owner-scoped candidate issues without the configured owner label are rejected.
- Owner-scoped candidate issues with a different `owner:*` label are rejected as
  conflicts.
- Conflict errors SHOULD include the issue identifier and observed owner labels,
  but SHOULD NOT block reconciliation of unrelated active runs.

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
+- `owner_label` (nullable string)
+  - Optional. Default: `null`.
+  - When set, restricts owner-scoped candidate polling to issues carrying the
+    normalized owner label.
+  - Valid initial values: `owner:hermes`, `owner:denovo`, `owner:human`,
+    `owner:triage`.
+  - Omitted or `null` preserves current unscoped behavior.
 - `active_states` (list of strings)
   - Default: `Todo`, `In Progress`
 - `terminal_states` (list of strings)
   - Default: `Closed`, `Cancelled`, `Canceled`, `Duplicate`, `Done`
```

### Section 6.4 Cheat Sheet

```diff
 - `tracker.kind`: string, REQUIRED, currently `linear`
 - `tracker.endpoint`: string, default `https://api.linear.app/graphql` when `tracker.kind=linear`
 - `tracker.api_key`: string or `$VAR`, canonical env `LINEAR_API_KEY` when `tracker.kind=linear`
 - `tracker.project_slug`: string, REQUIRED when `tracker.kind=linear`
+- `tracker.owner_label`: nullable string, optional, default `null`; valid
+  owner-scoped values are `owner:hermes`, `owner:denovo`, `owner:human`,
+  `owner:triage`
 - `tracker.active_states`: list of strings, default `["Todo", "In Progress"]`
 - `tracker.terminal_states`: list of strings, default `["Closed", "Cancelled", "Canceled", "Duplicate", "Done"]`
```

## Open Questions

- Should runtime implementation resolve the configured owner label to a Linear
  label ID and filter by ID instead of name when team-scoped labels with the
  same name exist?
- Should Symphony expose a first-class label mutation API, or should label
  mutation remain a Hermes-only Linear GraphQL responsibility outside the core
  tracker reader?
- Should removing the configured owner label from an already-running issue
  terminate immediately, pause for human review, or finish the current turn and
  then stop?
- Should `owner:triage` be a terminal routing owner that never launches Codex,
  or a normal owner value for triage-specific automation?
- Should future owner labels be SPEC-managed only, or can deployments define a
  local allowlist extension?

## References

- SPEC.md sections 5.3, 5.3.1, 6.3, and 6.4.
- Upstream `openai/symphony` Elixir README workflow front matter example and
  Linear consumption notes.
- Linear developer documentation for GraphQL filtering: filters are ANDed by
  default, and relationship filtering supports `labels: { name: { eq: ... } }`.
- Linear label documentation: workspace labels avoid API ambiguity from
  same-name team labels.

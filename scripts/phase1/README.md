# Phase 1 Tooling

This directory contains Go-only operator tooling for Symphony Phase 1. Shell
commands in this document are examples and verification invocations only; do
not add Bash, Python, Elixir, or other-language implementation scripts.

## Owner Label Provisioning

Dry-run the reserved owner labels for the Hadto Linear team:

```sh
LINEAR_API_KEY=... go run ./scripts/phase1 labels --team Hadto
```

Apply the missing labels:

```sh
LINEAR_API_KEY=... go run ./scripts/phase1 labels --team Hadto --apply
```

Run apply twice. The second run must report every reserved owner label as
`exists`, not `created`.

The command emits JSON and never prints the token. It creates only these
reserved labels:

- `owner:hermes`
- `owner:denovo`
- `owner:human`
- `owner:triage`

The command is intentionally conservative. If a same-name owner label exists in
another Linear API scope, it fails and asks the operator to resolve the duplicate
API identity before creating more labels.

## Verification

Run these before opening a PR:

```sh
git diff --check
go test ./...
go vet ./...
```

If live credentials are unavailable, include the failure as operator-only proof
and rely on the fixture-backed tests for code behavior.

## GraphQL Operations

The Phase 1 label command uses these Linear GraphQL operations:

- `teams(first:, after:)` to resolve the team key, name, or UUID.
- `issueLabels(first:, after:, includeArchived: false)` to list current labels.
- `issueLabelCreate(input: IssueLabelCreateInput!)` to create missing labels.

Live schema introspection on 2026-05-03 showed:

- `IssueLabelCreateInput` fields: `id`, `name`, `description`, `color`,
  `parentId`, `teamId`, `isGroup`, `retiredAt`.
- `issueLabelCreate` arguments: optional `replaceTeamLabels`, required `input`.
- `IssueLabelPayload` fields: `success`, `issueLabel`, `lastSyncId`.

The implementation handles GraphQL `errors` before reading `data`, treats HTTP
429 as a failed run, and keeps rate-limit headers in error text without logging
request or response bodies.

## Owner Backfill

Dry-run owner-label backfill for the Hadto team:

```sh
LINEAR_API_KEY=... go run ./scripts/phase1 backfill \
  --team Hadto \
  --policy scripts/phase1/backfill_policy.example.json \
  --csv backfill_dry_run.csv
```

Apply after the dry-run artifact has been reviewed and the labels from
`HAD-658` exist:

```sh
LINEAR_API_KEY=... go run ./scripts/phase1 backfill \
  --team Hadto \
  --policy scripts/phase1/backfill_policy.example.json \
  --csv backfill_apply.csv \
  --timeout 10m \
  --apply
```

The migration never removes labels. It skips issues that already have exactly
one `owner:*` label, reports conflicts when multiple owner labels exist, and
uses `issueUpdate(input: { addedLabelIds: [...] })` only for safe append
decisions.

Use `--timeout` for large live applies so reviewed migrations do not fail
mid-run on the default two-minute command timeout.

If Linear rate limits a live apply after a dry-run has already been reviewed,
retry from that saved JSON plan to avoid re-scanning the whole team:

```sh
LINEAR_API_KEY=... go run ./scripts/phase1 backfill \
  --team Hadto \
  --policy scripts/phase1/backfill_policy.example.json \
  --plan-json backfill_dry_run.json \
  --csv backfill_apply_retry.csv \
  --timeout 10m \
  --apply
```

The plan JSON policy hash must match the current policy file.

The default policy is intentionally conservative:

- historical Hermes project: `owner:hermes`
- current De Novo project: `owner:denovo`
- Symphony project: `owner:human`
- all other projects: `owner:human`

Sub-issues inherit their parent owner by default unless the sub-issue's project
has an explicit override.

The CSV audit file contains only:

- `issue_id`
- `project`
- `parent_issue_id`
- `prior_owner_labels`
- `applied_label`
- `decision_reason`
- `skipped_reason`

Do not commit raw issue bodies, comments, token values, raw API responses, or
secret-derived values.

## Owner Views

The CLI does not create Linear custom views in HAD-658. Views are useful for
human visibility, but label creation and bot/channel substrate should not depend
on custom-view API behavior. Use `OPERATOR.md` for the manual view setup and
verification checklist.

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

## Owner Views

The CLI does not create Linear custom views in HAD-658. Views are useful for
human visibility, but label creation and bot/channel substrate should not depend
on custom-view API behavior. Use `OPERATOR.md` for the manual view setup and
verification checklist.

# Hermes Migration Rollback Controls

Linear issue: HAD-662

## Transition Mode

The migration default is no shadow dispatcher:

- `migration.legacy_loop_mode: disabled`
- `migration.legacy_loop_mutates_linear: false`
- `migration.shadow_mode: false`

This means Symphony is the only issue-selection path once the Hermes workflow is
authoritative. The old Hermes loop is not allowed to keep mutating Linear in
parallel because that creates double-dispatch risk. The active
`hermes-gateway.service` remains the native Slack Socket Mode gateway until the
live decommission item, but it is not the Symphony scheduler.

## Rollback Levers

Rollback is explicit and unit-scoped:

- Stop or disable future `symphony-hermes.service` if Symphony dispatch must be
  paused.
- Keep `hermes-gateway.service` running for Slack Socket Mode unless the
  incident is inside the gateway itself.
- Change `migration.legacy_loop_mode` only as an operator-reviewed rollback
  step. Do not enable any legacy loop that can assign, delegate, or claim Linear
  issues while Symphony claim enforcement is active.
- Preserve `tracker.owner_label`, `tracker.claim_assignee`, and
  `tracker.require_claim_before_dispatch` unless the rollback explicitly exits
  the Symphony owner/claim contract.

## Double-Dispatch Proof

Local dry-run coverage lives in the Go tests:

- `internal/linear/claim_test.go` runs a contention proof where two agents race
  for the same owner-labeled issue and exactly one claim wins each round.
- `internal/linear/dispatch_test.go` proves restart recovery blocks a second
  scheduler when an owner-labeled issue is already assigned to another agent.

These tests are not a live production smoke test. Item 9 still owns the live
poll-cycle proof before anything is disabled.

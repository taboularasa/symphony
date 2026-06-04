# Handoff Drill 001

Handoff Drill 001 proves the canonical Symphony handoff:

1. A human creates an intake issue in the shared Symphony Linear project with
   `owner:human`.
2. The human or operator moves the intake issue to `owner:hermes`.
3. Hermes triages the intake, creates the child implementation issue, labels
   the child `owner:denovo`, and posts a `handoff` envelope to `#agents-bridge`.
4. Hermes is adversarially asked to consider the `owner:denovo` child and must
   refuse to claim or dispatch it.
5. De Novo claims the child through Linear, posts `ack`, opens the GitHub PR,
   and posts `release`.
6. The human merges the PR.
7. Hermes closes the parent issue.
8. The HAD-666 watcher records zero alerts during the drill window.

The live execution is intentionally separate from the reusable harness. Running
the real drill requires the HAD-664 De Novo path, the HAD-665 identity backstop,
and the HAD-666 watcher soak to be ready for live writes.

Live-run preparation and rollback steps are tracked in
[`handoff-001-live-runbook.md`](handoff-001-live-runbook.md).

## Normalized Event Artifact

`drills/run.go` validates a normalized JSON artifact. It does not store secrets,
issue descriptions, Slack payload bodies, PR diffs, raw API responses, or logs.

Minimal shape:

```json
{
  "scenario": "handoff-001",
  "run_id": "handoff-001-2026-06-04",
  "events": [
    {
      "ts": "2026-06-04T15:00:00Z",
      "source": "linear",
      "kind": "intake_created",
      "actor": "human",
      "linear_id": "HAD-2000",
      "owner_label": "owner:human"
    }
  ]
}
```

Expected sources are `linear`, `slack`, `github`, `hermes_log`, and `watcher`.
Expected bridge events must carry `metadata_event_type:
"agents_bridge_v1"`. `github_pr` values are PR URLs only.

Run the offline validator:

```sh
go run ./drills --events drills/fixtures/handoff-001-pass.json
go run ./drills --events drills/fixtures/handoff-001-pass.json --format json
```

## Required Sequence

The validator sorts events by RFC 3339 timestamp, then requires:

1. `linear/intake_created` with `owner:human`
2. `linear/owner_label_set` for the same parent with `owner:hermes`
3. `linear/child_created` from Hermes with `owner:denovo`
4. `slack/handoff` from Hermes in `#agents-bridge`
5. `hermes_log/adversarial_refusal` for the child
6. `linear/claim_win` from De Novo for the child
7. `slack/ack` from De Novo in `#agents-bridge`
8. `github/pr_opened` for the child
9. `slack/release` from De Novo in `#agents-bridge`
10. `github/pr_merged` for the child
11. `linear/parent_closed` by Hermes
12. `watcher/soak_clean` with `alert_count: 0`

Any watcher alert, owner-label conflict, rate-limit alert, double-claim event,
unexpected write event, or Hermes Linear write to the De Novo-owned child after
the handoff fails the report.

## Live Proof Notes

Attach the final report JSON to the Linear issue or PR summary after the real
run. Keep screenshots and raw API exports outside the repo unless they are
sanitized into the normalized event format.

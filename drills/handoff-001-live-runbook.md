# Handoff Drill 001 Live Runbook

This runbook prepares the live HAD-667 drill. It is intentionally not an
authorization to run the drill. Do not create, mutate, claim, close, merge, or
cancel live Linear/GitHub/Slack artifacts until an operator records explicit
authorization in HAD-667.

## Current Gate

As of 2026-06-04, the live drill is not ready to execute:

- `HERMES_LINEAR_TOKEN` is absent from Doppler `lenovo_server/dev`.
- `DENOVO_LINEAR_TOKEN` is absent from Doppler `lenovo_server/dev`.
- `WATCHER_LINEAR_TOKEN` is absent from Doppler `lenovo_server/dev`.
- `HERMES_GITHUB_APP_ID` is absent from Doppler `lenovo_server/dev`.
- `DENOVO_GITHUB_APP_ID` is absent from Doppler `lenovo_server/dev`.
- `SLACK_USER_TOKEN` is present but inactive.
- Hermes Slack bot auth succeeds as `hermes`.
- `#agents-bridge` is visible as `C0B83H1F15K`, and the Hermes bot is a
  member.
- `#hermes-ops` and `#denovo-ops` are not visible to the current bot token.
- `symphony-agent-watcher-soak.service` is running as a transient user unit,
  started at 2026-06-04 01:30:25 PDT.
- `taboularasa/de-novo` PR #143 is still draft and its live two-orchestrator
  proof is not complete.

## Authorization Record

Before running any live-write step, add a Linear HAD-667 comment with this
shape:

```text
I authorize Handoff Drill 001 live writes for <time window>.

Allowed live writes:
- create/cancel the parent and child canary Linear issues in the Symphony project
- change owner labels on those canary issues only
- run Hermes against the parent/child canaries with the documented override
- run De Novo against the child canary with the documented write gate
- post handoff/ack/release envelopes to #agents-bridge
- open and merge/close the drill PR in taboularasa/de-novo
- write sanitized drill proof comments back to HAD-667 and the canary issues

Not authorized:
- changes outside the named canary issues, #agents-bridge, and the drill PR
- changing IronClaw Slack mode
- enabling webhook/WASM Slack
- broad GitHub App installation or ruleset changes
```

## Planned Inputs

Record concrete values before execution:

| Input | Value |
| --- | --- |
| Drill run ID | `handoff-001-YYYYMMDD-HHMMZ` |
| Linear project slugId | `6a6a965c3d10` |
| Parent canary issue | TBD |
| Child canary issue | TBD |
| Parent initial owner label | `owner:human` |
| Parent Hermes owner label | `owner:hermes` |
| Child owner label | `owner:denovo` |
| Bridge channel | `C0B83H1F15K` (`#agents-bridge`) |
| Target repo | `taboularasa/de-novo` |
| Drill branch | `had-667-handoff-drill-001-<run_id>` |
| Expected PR owner label | `owner:denovo` |
| Hermes workflow | `hermes/WORKFLOW.md` |
| De Novo workflow | `/home/david/code/de-novo/denovo/WORKFLOW.md` |
| Watcher unit | `symphony-agent-watcher-soak.service` |
| Drill artifact path | `build/drills/<run_id>.json` |
| Drill report path | `build/drills/<run_id>-report.json` |

## Preflight Commands

These commands are read-only unless noted.

```sh
cd /home/david/stacks/symphony
git status --short --branch
go test ./...
go vet ./...
git diff --check

systemctl --user status symphony-agent-watcher-soak.service --no-pager -l
journalctl --user -u symphony-agent-watcher-soak.service \
  --since '2026-06-04 01:30:25' --no-pager

doppler run --project lenovo_server --config dev -- bash -lc '
  for name in LINEAR_API_KEY HERMES_LINEAR_TOKEN DENOVO_LINEAR_TOKEN \
    WATCHER_LINEAR_TOKEN SLACK_BOT_TOKEN HERMES_GITHUB_APP_ID \
    DENOVO_GITHUB_APP_ID; do
    if [ -n "$(printenv "$name" 2>/dev/null || true)" ]; then
      printf "%s=present\n" "$name"
    else
      printf "%s=absent\n" "$name"
    fi
  done
'
```

If the dedicated Linear tokens are still absent, use of `LINEAR_API_KEY` as a
temporary drill fallback must be named in the authorization comment and in the
final report.

## Canary Creation

Live write. Do not run before authorization.

Create a parent canary issue in the Symphony project with:

- title prefix: `[drill] Handoff 001 parent`
- project: Symphony (`6a6a965c3d10`)
- owner label: `owner:human`
- body: short drill scope and rollback note, no secrets

Then move the parent to `owner:hermes`, let Hermes create or update the child,
and confirm the child has:

- parent relation to the parent canary
- owner label `owner:denovo`
- no conflicting `owner:*` labels

If the child must be created manually for the run, record that deviation in the
normalized artifact and final HAD-667 comment.

## Hermes Step

Hermes must stay on native Slack Socket Mode. Do not configure webhook/WASM
Slack, do not add Slack to IronClaw WASM channels, and do not install
`~/.ironclaw/channels/slack.wasm`.

Read-only dry-run proof for a specific issue:

```sh
cd /home/david/stacks/symphony
doppler run --project lenovo_server --config dev -- \
  go run ./tools/symphony-hermes \
    --once \
    --dry-run=true \
    --allow-token-fallback \
    --check-hook \
    --issue "$PARENT_LINEAR_ID" \
    --limit=5
```

Live Hermes claim/dispatch must use the same issue filter and may only proceed
after the authorization comment names the token fallback or dedicated
`HERMES_LINEAR_TOKEN` presence.

Adversarial proof:

```sh
cd /home/david/stacks/symphony
doppler run --project lenovo_server --config dev -- \
  go run ./tools/symphony-hermes \
    --once \
    --dry-run=true \
    --allow-token-fallback \
    --issue "$CHILD_LINEAR_ID" \
    --limit=5
```

The expected outcome is no Hermes dispatch for the `owner:denovo` child. Capture
only sanitized event lines for the normalized artifact.

## De Novo Step

The current De Novo readiness PR is
`https://github.com/taboularasa/de-novo/pull/143`. Its live drill command has a
double gate for writes:

```sh
cd /home/david/code/de-novo
make symphony-linear-drill
```

The default command is live-read/no-write. Live Linear writes require both the
flag and env gate:

```sh
cd /home/david/code/de-novo
doppler run --project lenovo_server --config dev -- bash -lc '
  DENOVO_SYMPHONY_DRILL_ALLOW_LINEAR_WRITES=1 \
  go run ./cmd/symphony-linear-drill \
    --workflow denovo/WORKFLOW.md \
    --output build/symphony/handoff-001-denovo-proof.json \
    --duration 30m \
    --interval 30s \
    --allow-linear-writes
'
```

Do not use the write-gated command until the authorization comment names the
child canary issue and allowed time window.

## GitHub Backstop Expectations

For a De Novo-owned child issue:

- repository must be `taboularasa/de-novo`
- owner label must be `owner:denovo`
- expected GitHub App login is `denovo-bot[bot]`
- expected App ID env is `DENOVO_GITHUB_APP_ID`

Current provider limits mean private-repo required-check activation is not
proven. If the drill PR is opened by a human-admin fallback instead of the
expected app, record the fallback and keep HAD-665/HAD-667 live enforcement
criteria unchecked.

Local policy check shape:

```sh
cd /home/david/stacks/symphony
go run ./tools/github-owner-check \
  --repository taboularasa/de-novo \
  --branch "$DRILL_BRANCH" \
  --head-sha "$DRILL_HEAD_SHA" \
  --owner-label owner:denovo \
  --event-sender-login 'denovo-bot[bot]' \
  --event-sender-type Bot \
  --linear-token-env ''
```

## Slack Bridge Evidence

Required top-level `#agents-bridge` envelopes:

- Hermes posts `handoff` for the child.
- De Novo posts `ack` after it claims/accepts the child.
- De Novo posts `release` when the implementation PR is ready or merged.

Collect Slack envelope evidence after the run:

```sh
cd /home/david/stacks/symphony
doppler run --project lenovo_server --config dev -- \
  go run ./drills \
    --collect \
    --events "$DRILL_ARTIFACT" \
    --collect-output "$DRILL_ARTIFACT" \
    --slack-token-env SLACK_BOT_TOKEN \
    --slack-channel C0B83H1F15K \
    --since "$DRILL_STARTED_AT" \
    --until "$DRILL_FINISHED_AT"
```

## Final Artifact Collection

After all live steps finish, collect read-only normalized evidence:

```sh
cd /home/david/stacks/symphony
doppler run --project lenovo_server --config dev -- \
  go run ./drills \
    --collect \
    --events "$DRILL_ARTIFACT" \
    --collect-output "$DRILL_ARTIFACT" \
    --run-id "$DRILL_RUN_ID" \
    --linear-token-env LINEAR_API_KEY \
    --linear-parent "$PARENT_LINEAR_ID" \
    --linear-child "$CHILD_LINEAR_ID" \
    --slack-token-env SLACK_BOT_TOKEN \
    --slack-channel C0B83H1F15K \
    --github-pr "$DRILL_PR_URL" \
    --github-linear-id "$CHILD_LINEAR_ID" \
    --since "$DRILL_STARTED_AT" \
    --until "$DRILL_FINISHED_AT"

go run ./drills --events "$DRILL_ARTIFACT" --format json > "$DRILL_REPORT"
go run ./drills --events "$DRILL_ARTIFACT"
```

The text run must pass. The JSON report is the durable HAD-667 artifact.

## Rollback And Cleanup

If anything deviates:

1. Stop only the drill-specific process. Do not stop `hermes-gateway.service`
   unless the incident is inside the native Slack gateway itself.
2. Leave `symphony-agent-watcher-soak.service` running unless it is the direct
   source of the incident.
3. Close or cancel the parent and child canary issues with a short proof note.
4. Close the drill PR without merging if it was opened and not intentionally
   merged.
5. Remove only drill branches created for this run.
6. Save the failing normalized artifact and create a follow-up issue for any
   unexpected owner-routing, watcher, bridge-envelope, GitHub-backstop, or claim
   behavior.

Useful cleanup commands:

```sh
cd /home/david/stacks/symphony
systemctl --user status symphony-agent-watcher-soak.service --no-pager -l
journalctl --user -u symphony-agent-watcher-soak.service \
  --since "$DRILL_STARTED_AT" --no-pager

cd /home/david/code/de-novo
git status --short --branch
gh pr close "$DRILL_PR_URL" --comment "Closing failed/aborted HAD-667 drill PR."
git push origin --delete "$DRILL_BRANCH"
```

Do not delete local evidence under `build/drills/` or De Novo
`build/symphony/` until the Linear handoff comment links the final proof.

# Phase 1 Operator Handoff

This handoff separates Codex-completable Go tooling from human-only workspace
administration. Do not paste secret values into Linear, Slack, GitHub, PRs, or
logs. Use presence checks, screenshots with values hidden, or one-way
fingerprints where proof is required.

## 1. Provision Owner Labels

Run a dry-run first:

```sh
LINEAR_API_KEY=... go run ./scripts/phase1 labels --team Hadto
```

Review the JSON output. It should show `create` only for missing reserved owner
labels and `exists` for labels already present in the Hadto team.

Apply the labels:

```sh
LINEAR_API_KEY=... go run ./scripts/phase1 labels --team Hadto --apply
```

Run apply a second time. The second run must show all four labels as `exists`.
Attach the redacted JSON output to the PR or summarize it in a PR comment.

## 2. Create Linear Bot Users

Human-only Linear admin step:

- Create or invite `hermes-bot@hadto.net` using the display name `hermes-bot`.
- Create or invite `denovo-bot@hadto.net` using the display name `denovo-bot`.
- If Linear does not expose a separate bot-user type on this plan, use regular
  workspace members with bot-specific email addresses and document that
  convention in the PR.

Validation proof:

- Screenshot the member rows with email domains and display names visible.
- Hide unrelated personal data where possible.
- Record the Linear user UUIDs in the secure operator notes, not in the PR.

## 3. Verify Bot Token Attribution

Human-only Linear account step:

- Sign in as `hermes-bot` and create a Linear API key.
- Use the token to perform a harmless test mutation in a temporary issue or
  dedicated substrate proof issue, such as adding and then removing a test
  comment.
- Confirm the mutation is attributed to `hermes-bot`.
- Repeat for `denovo-bot`.

If a workspace admin-generated key is attributed to the admin instead of the bot
user, stop and escalate. The claim-assignee model depends on mutations being
attributed to the configured bot identity.

## 4. Store Linear Tokens In Doppler

Store tokens without echoing values in shell history:

```sh
doppler secrets set HERMES_LINEAR_TOKEN --silent
doppler secrets set DENOVO_LINEAR_TOKEN --silent
```

Presence proof only:

```sh
doppler secrets get HERMES_LINEAR_TOKEN --plain >/dev/null && echo HERMES_LINEAR_TOKEN present
doppler secrets get DENOVO_LINEAR_TOKEN --plain >/dev/null && echo DENOVO_LINEAR_TOKEN present
```

Do not paste token values into the PR or Linear.

## 5. Create Slack Channels

Human-only Slack admin step unless a workspace-admin token is explicitly
available:

- Create `#hermes-ops`.
- Create `#denovo-ops`.
- Create `#agents-bridge`.
- Invite the existing Hermes Slack bot to `#hermes-ops` and `#agents-bridge`.
- Invite the future De Novo Slack bot to `#denovo-ops` and `#agents-bridge`
  once that bot exists.

If using the Slack Web API, prefer channel IDs over names:

- `conversations.create` for channel creation.
- `conversations.invite` for bot membership.
- `chat.postMessage` proof posts only after bot membership is confirmed.

Validation proof:

- Screenshot or API output proving each channel exists.
- Screenshot or API output proving bot membership.
- Redact Slack tokens and unrelated channel/user data.

## 6. Owner Views

The Go CLI does not create Linear custom views in HAD-658. Create these manually
if human visibility is desired:

- `Owner: Hermes` filtered to `owner:hermes`.
- `Owner: De Novo` filtered to `owner:denovo`.
- `Owner: Human` filtered to `owner:human`.
- `Owner: Triage` filtered to `owner:triage`.

Coverage is not applicable for this code-free manual setup. Proof is the view
configuration screenshot or a short PR note that views were intentionally
deferred.

## 7. Watcher Bot Decision

HAD-666 recommends a dedicated `watcher-bot` identity for Linear comments. For
HAD-658, record one of these decisions in the PR:

- Provision `watcher-bot@hadto.net` now and store its token as
  `WATCHER_LINEAR_TOKEN`.
- Defer `watcher-bot` to HAD-666 and document the temporary identity that the
  watcher will use for fixture tests only.

Do not let HAD-666 silently reuse `HERMES_LINEAR_TOKEN` or a human token without
explicit operator approval.

## 8. PR Proof Checklist

- `git diff --check` passed.
- `go test ./...` passed.
- `go vet ./...` passed.
- Label dry-run output reviewed.
- Label apply output reviewed, or apply marked blocked by missing operator
  credentials.
- Bot-user screenshots attached, or marked human-only pending.
- Doppler token presence checks attached, or marked human-only pending.
- Slack channel and membership proof attached, or marked human-only pending.

## 9. Backfill Operator Notes

HAD-659 consumes the labels from this issue. Do not run production backfill apply
until:

- All four owner labels exist in the Hadto team.
- The dry-run CSV has been reviewed.
- The Hermes De Novo ignore rule is still in place.

Run dry-run:

```sh
LINEAR_API_KEY=... go run ./scripts/phase1 backfill \
  --team Hadto \
  --policy scripts/phase1/backfill_policy.example.json \
  --csv backfill_dry_run.csv
```

Run apply after review:

```sh
LINEAR_API_KEY=... go run ./scripts/phase1 backfill \
  --team Hadto \
  --policy scripts/phase1/backfill_policy.example.json \
  --csv backfill_apply.csv \
  --apply
```

Attach only the sanitized CSV or a summarized proof note. Do not attach raw
Linear exports.

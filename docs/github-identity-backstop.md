# GitHub Identity Backstop

HAD-665 adds the PR-level backstop for Symphony ownership. Linear owner labels
decide which runtime may work an issue. This GitHub guard makes the repository
boundary explicit too: a PR for an `owner:denovo` issue must come from the De
Novo GitHub App and target a De Novo-owned repository, while a PR for an
`owner:hermes` issue must come from the Hermes GitHub App and target a
Hermes-owned repository.

The hosted GitHub path is a required GitHub Actions check. GitHub Enterprise
Server pre-receive hooks can reject pushes before refs are accepted, but this
hosted setup does not expose that hook surface.

## Policy

The machine-readable policy is `config/github-owner-backstop.yaml`.

Current owners:

- `owner:hermes`
  - Repositories: `taboularasa/hermes-agent`, `taboularasa/phoneitin`
  - Expected app login: `hermes-bot[bot]`
  - App ID env: `HERMES_GITHUB_APP_ID`
- `owner:denovo`
  - Repositories: `taboularasa/de-novo`
  - Expected app login: `denovo-bot[bot]`
  - App ID env: `DENOVO_GITHUB_APP_ID`

Human bypass is `repo_admin_only`. That is a break-glass path for David or
another repository admin; it is not an automation path.

## Check Command

Local allow proof:

```bash
go run ./tools/github-owner-check \
  --repository taboularasa/de-novo \
  --branch HAD-665/github-backstop \
  --head-sha abc123 \
  --owner-label owner:denovo \
  --event-sender-login 'denovo-bot[bot]' \
  --event-sender-type Bot \
  --linear-token-env ''
```

Local deny proof:

```bash
go run ./tools/github-owner-check \
  --repository taboularasa/de-novo \
  --branch HAD-665/github-backstop \
  --owner-label owner:denovo \
  --event-sender-login 'hermes-bot[bot]' \
  --event-sender-type Bot \
  --linear-token-env ''
```

The deny command intentionally exits nonzero after printing a JSON decision.
That is the behavior the required check needs.

To preserve local proof without committing generated output, write sanitized
JSON decisions under the ignored `build/github-owner-backstop/` directory:

```bash
mkdir -p build/github-owner-backstop

go run ./tools/github-owner-check \
  --repository taboularasa/de-novo \
  --branch HAD-665/github-backstop \
  --head-sha local-allow-proof \
  --owner-label owner:denovo \
  --event-sender-login 'denovo-bot[bot]' \
  --event-sender-type Bot \
  --linear-token-env '' \
  > build/github-owner-backstop/allow-denovo-app.json

set +e
go run ./tools/github-owner-check \
  --repository taboularasa/de-novo \
  --branch HAD-665/github-backstop \
  --head-sha local-deny-proof \
  --owner-label owner:denovo \
  --event-sender-login 'hermes-bot[bot]' \
  --event-sender-type Bot \
  --linear-token-env '' \
  > build/github-owner-backstop/deny-cross-owner-app.json
deny_status=$?
set -e
test "$deny_status" -ne 0
```

When `--owner-label` is omitted and `--linear-token-env` names a populated token
env var, the command resolves the Linear issue key from `--linear-issue`,
`--branch`, or the PR body and reads the issue's `owner:*` label from Linear.
Missing owner labels, conflicting owner labels, missing Linear issue keys, and
unknown app identities fail closed.

## GitHub App Provisioning

Operator-only steps:

1. Create the `hermes-bot` GitHub App.
2. Install it only on Hermes-owned repositories:
   - `taboularasa/hermes-agent`
   - `taboularasa/phoneitin`
   - future Hermes-owned consulting repositories after explicit approval.
3. Create the `denovo-bot` GitHub App.
4. Install it only on De Novo-owned repositories:
   - `taboularasa/de-novo`
   - future De Novo spinoff repositories after explicit approval.
5. Store app IDs in Doppler without printing secret values:

```bash
doppler secrets set HERMES_GITHUB_APP_ID=<id> --project lenovo_server --config dev --silent
doppler secrets set DENOVO_GITHUB_APP_ID=<id> --project lenovo_server --config dev --silent
```

Private keys must also be stored in Doppler, but the exact secret names should
be chosen with the app-token minting code that will consume them. Do not paste
private keys into Linear, Slack, GitHub comments, pull requests, or local proof
logs.

Presence checks should prove only that values exist:

```bash
doppler secrets get HERMES_GITHUB_APP_ID --project lenovo_server --config dev --plain >/dev/null
doppler secrets get DENOVO_GITHUB_APP_ID --project lenovo_server --config dev --plain >/dev/null
```

## Required Check Activation

The workflow is `.github/workflows/github-owner-backstop.yml`.

It runs on `pull_request_target` and checks out trusted base code, not the PR
branch. This matters because a PR must not be able to weaken the check by
editing the check implementation.

After this PR is merged, make the `github-owner-backstop / owner-backstop`
check required on protected branches or repository rulesets.

Recommended activation order:

1. Merge the code and workflow.
2. Trigger one allowed app PR and one denied app PR in a disposable branch.
3. Confirm the check name appears on the latest commit.
4. Mark that check required for `main`.
5. Repeat for each repository in the policy.

Do not make the check required before the workflow exists on the protected base
branch, or GitHub may wait forever for a check that cannot be reported.

## Current Provider Limits

Live API refresh on 2026-06-04:

- `taboularasa/symphony` is public and admin-visible.
- `gh api repos/taboularasa/symphony/rulesets` returned `[]`.
- `gh api repos/taboularasa/symphony/branches/main/protection` returned
  `Branch not protected`.
- `taboularasa/de-novo` is private.
- `gh api repos/taboularasa/de-novo/rulesets` and
  `gh api repos/taboularasa/de-novo/branches/main/protection` returned:
  `Upgrade to GitHub Pro or make this repository public to enable this feature`.

That means the code path can be merged now, but live enforcement for the private
De Novo repository remains blocked until the account plan or repository
visibility supports the required protection/ruleset feature.

Earlier operator-prereq evidence on 2026-05-04 also found no GitHub App IDs or
private keys in Doppler.

## Adding a Repository

1. Add the repository under exactly one `owner:*` entry in
   `config/github-owner-backstop.yaml`.
2. If a repository must be shared, add it to `shared_repositories` and document
   why the shared path is safe.
3. Add or update fixture tests for allow and deny behavior.
4. Install only the matching GitHub App on the repository.
5. Enable the required check after the workflow has reported once.

Cross-owner repository collisions are rejected by default.

## Rollback

If the check blocks legitimate work:

1. Disable the required-check rule or put the ruleset in evaluate/disabled mode.
2. Leave the workflow in place so it can still report decisions for debugging.
3. Capture the JSON decision from the failed check.
4. Fix the policy, GitHub App installation, Linear owner label, or human bypass
   setting.
5. Re-enable the required check only after a new allowed proof passes.

Do not bypass by granting both apps access to all repositories; that removes the
backstop this issue exists to add.

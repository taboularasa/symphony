# Hermes Workflow Migration Inventory

Linear issue: HAD-662

This inventory separates code-prep work from the live Hermes cutover. The live
cutover remains blocked until HAD-658 provides explicit bot/channel
prerequisites.

## Gate Snapshot

| Gate | Result |
| --- | --- |
| Symphony checkout | `/home/david/stacks/symphony` clean on `main...origin/main` before HAD-662 branch work |
| Hermes checkout | `/home/david/stacks/hermes-agent` clean on `main...origin/main` before inventory |
| HAD-659 | Done |
| HAD-661 | Done and merged in `taboularasa/symphony` PR #11 |
| HAD-658 | In Progress; blocks live `HERMES_LINEAR_TOKEN`, bot membership, and canary proof |
| Symphony baseline | `git diff --check`, `go test ./...`, `go vet ./...` passed |
| Hermes targeted baseline | `uv run pytest tests/cron/test_ownership_policy.py tests/test_run_agent_ctx_runtime.py tests/test_cli_ctx_mode.py -q` passed with 13 tests |

## Surface Inventory

| Surface | Current role | HAD-662 action |
| --- | --- | --- |
| `agent/prompt_builder.py` | Generic stateless system-prompt helpers and skills/context prompt composition. | Treat as generic runtime machinery; do not move Hermes EM policy here. |
| `hadto_patches/agent_runner.py` | Builds and caches the effective agent system prompt, injects optional ephemeral system prompt, and appends ctx binding notes. | Use as source for prompt mechanics and ctx behavior; keep generic runtime behavior in Hermes unless explicitly retired. |
| `hadto_patches/gateway_run.py` | Loads `agent.system_prompt` from `~/.hermes/config.yaml`, manages prompt commands, combines ephemeral system prompts, and pre-binds ctx for gateway sessions. | Inventory current prompt source; update docs once Symphony `WORKFLOW.md` becomes the source of truth. |
| `hadto_patches/ownership_policy.py` | Contains `_DENIED_PROJECTS = {"de novo", "denovo"}` and emits `de_novo_block`. | Remove only after owner-label and claim filtering are effective in the Hermes workflow. |
| `tests/cron/test_ownership_policy.py` | Asserts `de_novo_block` deny behavior and audit output. | Replace project-name denial expectations with owner/claim ineligibility expectations when item 6 is reached. |
| `~/.hermes/config.yaml` | Sanitized inspection found a config file with `ctx` keys: `coding_mode`, `coding_toolsets`, `data_dir`, `enabled`. | Do not copy secret values. No `agent.system_prompt` value was printed during inventory. |
| `hermes-gateway.service` | Active user-systemd service running Hermes gateway from `/home/david/stacks/hermes-agent`. | Keep native Slack Socket Mode active; do not configure webhook/WASM Slack for this host. |
| Symphony service | No active user-systemd Symphony service was observed during inventory. | Do not decommission Hermes loop until Symphony runtime smoke proof exists. |

## De Novo Hit Summary

Required commands were run from `/home/david/stacks/hermes-agent`:

- `git log --all --oneline -S 'de-novo' -- hadto_patches tests agent README.md`
- `git log --all --oneline -S 'de_novo_block' -- hadto_patches tests`
- `git log --all --oneline -S 'denovo' -- hadto_patches tests`
- `git log --all --oneline -S 'De Novo' -- hadto_patches tests`
- `rg -n -i 'de novo|de-novo|denovo' hadto_patches tests agent README.md`

Current grep hit files:

- `hadto_patches/ownership_policy.py`: project-name negative list and `de_novo_block`; remove in item 6.
- `tests/cron/test_ownership_policy.py`: tests for the negative-list behavior; update in item 6.
- `hadto_patches/denovo_slack_notification.py`: De Novo Slack wake-up payload validation; preserve.
- `hadto_patches/denovo_source_reference.py`: source-reference contracts for De Novo ingestion; preserve.
- `hadto_patches/denovo_book_study.py`: book-study response contract; preserve.
- `hadto_patches/platform_webhook.py`: De Novo Slack wake-up webhook route; preserve.
- `hadto_patches/platform_slack.py`: De Novo wake-up thread-context fetch; preserve.
- `tests/gateway/test_denovo_*`, `tests/gateway/test_webhook_adapter.py`, `tests/gateway/test_slack.py`: tests for De Novo Slack/source-reference behavior; preserve unless a later item intentionally changes that behavior.

History hits are mostly from HAD-546, HAD-547, HAD-550, and ownership-policy
commits. The only current negative-list ownership target is
`hadto_patches/ownership_policy.py` plus its cron tests.

## Prompt And ctx Notes

Prompt search confirmed the active prompt surfaces are split between generic
prompt composition, ephemeral prompt injection, gateway config loading, and ctx
binding notes. The workflow migration should place Hermes EM policy in
`hermes/WORKFLOW.md` while leaving reusable runtime mechanics in Hermes.

The item 4 prompt-body trace is recorded in `hermes/PROMPT_TRACE.md`. That file
lists each prompt source inspected, what was migrated into `WORKFLOW.md`, and
which snippets remain generic Hermes runtime behavior instead of Hermes EM
policy.

ctx host tooling at `/home/david/code/ctx-host-tools` is a conservative
host-side orphan-task reaper. It is not the ctx daemon source. HAD-662 hook work
should observe ctx binding/worktree metadata without copying private task data.

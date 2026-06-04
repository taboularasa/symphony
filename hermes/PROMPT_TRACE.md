# Hermes Prompt Trace

Linear issue: HAD-662

This trace records how the Hermes EM/Field-Copilot prompt body was consolidated
into `hermes/WORKFLOW.md`. It intentionally separates Hermes-specific execution
policy from generic Hermes runtime prompt machinery.

## Source Disposition

| Source | Current role | Disposition in `WORKFLOW.md` |
| --- | --- | --- |
| `~/.hermes/config.yaml` `agent.system_prompt` | Sanitized inspection found no configured system-prompt value and no agent keys. | Nothing copied. `api_key` remains secret indirection through `$HERMES_LINEAR_TOKEN` only. |
| `agent/prompt_builder.py` `DEFAULT_AGENT_IDENTITY` | Generic Hermes assistant identity and broad tool-use posture. | Not copied; Symphony workflow policy starts at Hermes issue execution, not base assistant identity. |
| `agent/prompt_builder.py` memory, session search, skills, tool-use, and model execution guidance | Generic runtime instructions loaded for Hermes conversations. | Not copied; these remain reusable Hermes runtime behavior. |
| `agent/prompt_builder.py` context-file scanning | Prompt-injection guard for `AGENTS.md`, `.hermes.md`, `HERMES.md`, and similar files. | Not copied; context-file loading remains a Hermes runtime concern. |
| `hadto_patches/agent_runner.py` prompt assembly | Builds system prompt pieces, context files, timestamp, platform hints, and ctx notes. | Not copied as policy. The workflow body relies on Symphony for scheduling and leaves prompt assembly mechanics in Hermes. |
| `hadto_patches/gateway_run.py` `_load_ephemeral_system_prompt` | Loads `HERMES_EPHEMERAL_SYSTEM_PROMPT` or `agent.system_prompt` overlays. | Documented as an overlay source only. It is no longer the Hermes EM source of truth. |
| `hadto_patches/ctx.py` `CtxBinding.system_prompt_note()` | Tells Hermes to use the ctx worktree, avoid nested Hermes worktrees, and act as engineering manager when `codex_delegate` is available. | Migrated into the `Workspace And ctx Contract` and `Engineering-Manager Behavior` sections. |
| `hadto_patches/cron_scheduler.py` ownership audit prompt prefix | Requires compact ownership and execution evidence in coordinator reports. | Migrated into evidence-bearing Linear comments and durable artifact handoff guidance. Legacy `de_novo_block` wording was not copied. |
| `hadto_patches/ownership_policy.py` | Contains `_DENIED_PROJECTS = {"de novo", "denovo"}` and `de_novo_block`. | Not copied. The workflow body states positive owner-label ownership and forbids project-name denial lists. Actual deletion remains item 6. |
| `/home/david/stacks/AGENTS.md` | Host-domain rules for `/home/david/stacks`, preserving unrelated work, localhost-first exposure, and reporting. | Workspace root and dirty-worktree preservation were migrated where relevant; unrelated network/service guidance stays host-level. |

## Migrated Guardrails

- Hermes acts only on Symphony-returned Linear issues with `owner:hermes`.
- Dispatch requires the `hermes` Linear app-user delegate claim gate to win or
  confirm self.
- `owner:denovo`, `owner:human`, `owner:triage`, unlabeled issues, and
  conflicting owner labels are excluded by positive label selection, not
  project-name negative lists.
- `codex_delegate` is the preferred implementation backend for non-trivial
  issue work when available; retries should resume or correct the same
  issue-scoped run.
- ctx-bound sessions must use the ctx-managed worktree and must not create
  nested Hermes worktrees.
- Native Slack Socket Mode remains the live host boundary; this workflow does
  not enable webhook/WASM Slack.
- Handoffs must cite durable evidence such as branch, commit, PR, commands, or
  sanitized operational proof.

## Prompt Tests

`internal/workflow/hermes_test.go` loads the checked-in workflow file and
asserts:

- the tracker contract uses `owner:hermes`, `hermes`, and
  `require_claim_before_dispatch: true`;
- `$HERMES_LINEAR_TOKEN` resolves by environment indirection in tests without
  embedding a real token;
- required sections and guardrail fragments remain present;
- missing owner/claim/gate fixtures fail the Hermes contract validator;
- legacy project-name ownership wording such as `de_novo_block` is absent.

No Hermes prompt-builder tests were changed because no Hermes runtime prompt
assembly behavior changes in this item.

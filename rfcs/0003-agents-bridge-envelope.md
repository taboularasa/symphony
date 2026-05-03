# RFC 0003: `#agents-bridge` envelope

Status: Draft
Linear issue: HAD-657

## Summary

Define the versioned Slack message contract used by Hermes and De Novo for
cross-agent handoff in `#agents-bridge`.

Each bridge message is a top-level Slack message with:

- Slack message metadata for the machine-readable envelope.
- Block Kit blocks for human scanning.
- Top-level `text` for notifications and accessibility fallback.

This is a Phase 0 RFC only. It defines the schema and fixtures, but does not add
Slack reader or writer code in either runtime.

## Motivation

The Symphony project Slack contract has one channel per agent identity plus a
shared `#agents-bridge`. Hermes and De Novo use their own operational channels,
but every cross-agent handoff must be observable in one shared channel. The
bridge is not a command channel and not a payload transport. It carries small
references to the real systems of record: Linear issues, GitHub PRs, and a short
reason string.

Existing Slack surfaces do not already provide this generic bridge contract:

- Hermes uses Slack Bolt Socket Mode, `chat_postMessage`, optional `thread_ts`,
  Block Kit approval buttons, reaction helpers, assistant-thread metadata
  caches, and bot-message filtering based on `bot_id` or `subtype:
  "bot_message"`.
- `taboularasa/codex-slack-bot` uses Socket Mode, `app_mention`, threaded plain
  text replies, and progress updates. No reusable bridge envelope was found in
  its README or `bot.js`.

RFC 0003 therefore defines a new `v: 1` contract. It coexists with those
systems; it does not reinterpret their existing app-mention or approval-message
formats.

## Wire Placement

Normative wire shape:

```json
{
  "channel": "CAGENTSBRIDGE",
  "text": "handoff HAD-123 from hermes to denovo",
  "metadata": {
    "event_type": "agents_bridge_v1",
    "event_payload": {
      "v": 1,
      "from": "hermes",
      "kind": "handoff",
      "linear_id": "HAD-123",
      "github_pr": "https://github.com/taboularasa/de-novo/pull/42",
      "reason": "Implementation is ready for De Novo ownership.",
      "ts": "2026-05-03T15:00:00Z"
    }
  },
  "blocks": []
}
```

Slack's current metadata guidance says metadata contains `event_type` and
`event_payload`, and that property names should use lowercase letters, numbers,
and underscores. Use `agents_bridge_v1` as the actual Slack `event_type`. The
logical schema name remains `agents-bridge` and the payload version remains
`v: 1`.

Top-level `text` MUST be present even when `blocks` are present. Slack uses it
for notifications and screen-reader fallback. Keep it short and human-readable.

Bridge messages MUST be top-level channel messages. Do not use `thread_ts` for
canonical handoff envelopes; discussion may happen in replies, but the envelope
itself is not chained inside another thread.

Fallback mode:

- If an implementation spike proves that the deployed Slack app cannot post or
  read metadata, the sender MAY put a fenced JSON envelope in top-level `text`
  prefixed by `agents-bridge:v1`.
- The same Block Kit and origin validation rules still apply.
- Runtime implementation must record the Slack error, for example
  `metadata_must_be_sent_from_app` or SDK support failure, before enabling this
  compatibility mode.

## Envelope Schema

The metadata `event_payload` is the canonical envelope.

Fields:

| Field | Required | Type | Rule |
| --- | --- | --- | --- |
| `v` | yes | integer | Must be `1`. Unknown versions are ignored and logged. |
| `from` | yes | string | One of `hermes`, `denovo`. |
| `kind` | yes | string | One of `handoff`, `ack`, `block`, `release`. |
| `linear_id` | yes | string | Linear identifier matching `^[A-Z]+-[0-9]+$`. |
| `github_pr` | no | string or null | HTTPS GitHub PR URL, when relevant. |
| `reason` | conditional | string or null | Required and non-empty for `block`; optional otherwise. |
| `ts` | yes | string | RFC 3339 UTC timestamp. |

Payload constraints:

- Payloads MUST NOT include secrets, private diffs, logs, or copied issue bodies.
- `github_pr` points to the PR; it does not embed PR content.
- `reason` is short operator text, not task instructions.
- Receivers MUST reject unknown required fields only if their schema version says
  so; for `v: 1`, unknown extra fields are ignored after logging.

Upgrade handling:

- `v == 1`: parse with this RFC.
- `v > 1`: do not act automatically; record an unsupported-version event.
- missing `v`: invalid.
- malformed JSON: invalid.

## Semantics

`handoff`: The sender has finished its side of the work and is asking the next
owner to inspect and claim the referenced Linear issue. This is a request for
coordination, not an order.

`ack`: The receiver confirms that it accepted or claimed the work.

`block`: The receiver refuses or cannot proceed. `reason` is required.

`release`: The receiver finished its loop and is closing the bridge sequence.

Canonical flow, Hermes intake to De Novo implementation:

1. Hermes triages an issue, creates or updates the Linear handoff issue, and
   labels ownership according to RFC 0001.
2. Hermes posts `handoff` in `#agents-bridge`.
3. De Novo validates Slack origin, reads `linear_id`, verifies Linear ownership,
   and posts `ack` only after it can claim or intentionally accept the issue.
4. De Novo opens or updates the GitHub PR and posts `release` when its side is
   complete, or `block` with a reason if it cannot proceed.

Idempotency key: `(linear_id, kind, from)`.

Repeated envelopes with the same key are tolerated. They MUST NOT reopen work,
re-run a completed handoff, or create duplicate child issues. If a sender needs
to correct a prior message, it posts a new envelope with a different `kind` or
updates the source system and posts a short new reason.

Non-goals:

- No DMs between agents.
- No command-and-control orders.
- No copied PR diffs, issue bodies, logs, or credentials.
- No implicit signals through reactions alone.

## Authenticity and Audit Rules

The envelope payload is routing data, not authority by itself. Receivers MUST
validate Slack-origin fields outside the payload:

- `app_id` must match the allowlist entry for the claimed `from` agent.
- `bot_id` or bot user ID must match the allowlist entry for that agent.
- User-authored lookalikes are human notes only, even if they contain valid
  metadata-shaped JSON in text.
- Bot messages from unapproved apps are ignored and logged.
- Metadata is visible to apps and users with channel access, so it is not a
  secret store.

Cryptographic signing is deferred. Slack app and bot identity checks are enough
for `v: 1`; a future RFC can add signatures if bridge spoofing becomes a
practical risk.

Human audit reactions:

| Envelope kind | Reaction name |
| --- | --- |
| `ack` | `white_check_mark` |
| `block` | `no_entry` |
| `release` | `checkered_flag` |

Reactions are audit affordances only. The metadata envelope remains the source
of truth. Implementations need Slack `reactions:write`; `already_reacted`
responses are idempotent success.

## Block Kit Mirror

The human-readable Block Kit body SHOULD include:

- A section block with `kind`, `linear_id`, and `from`.
- A context block with the GitHub PR URL when present.
- A short reason field when provided.

Blocks are not parsed for machine behavior. They mirror metadata for people and
may change layout without changing the envelope schema. Slack currently allows
up to 50 blocks in a message; bridge messages SHOULD stay under 5.

## Fixtures

Reusable fixtures live in `rfcs/fixtures/agents-bridge/`:

- `valid-handoff-message.json`
- `valid-ack-message.json`
- `valid-release-message.json`
- `invalid-missing-reason-block-message.json`
- `invalid-unknown-version-message.json`
- `invalid-spoofed-origin-message.json`
- `invalid-missing-origin-message.json`

All fixture files MUST parse with `jq -e .`. Invalid fixtures are JSON-valid but
schema-invalid, so later runtime tests can assert semantic rejection.

## References

- Symphony project Slack contract: one channel per agent identity plus shared
  `#agents-bridge`, no agent DMs, Linear IDs and PR URLs as references.
- Hermes Slack adapter: `/home/david/stacks/hermes-agent/hadto_patches/platform_slack.py`.
- Hermes Slack manifest: `/home/david/stacks/hermes-agent/slack-manifest.yaml`.
- `taboularasa/codex-slack-bot` README and `bot.js`.
- Slack `chat.postMessage` docs: JSON POST, `metadata`, top-level `text`,
  `blocks`, `thread_ts`, truncation, and rate-limit guidance.
- Slack message metadata docs: `event_type`, `event_payload`, Web API and Events
  API receipt, and `app_id` in message payloads.
- Slack Block Kit block docs and `reactions.add` docs.

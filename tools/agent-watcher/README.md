# Agent Watcher

`tools/agent-watcher` is the Phase 1 passive detector for agent writes in
projects they should not touch. It is implemented in Go only.

## Local Development

```sh
go test ./...
go vet ./...
LINEAR_WEBHOOK_SECRET=dev-secret go run ./tools/agent-watcher \
  --config tools/agent-watcher/watcher.example.yaml \
  --listen 127.0.0.1:18080
```

The webhook endpoint is:

```text
POST /webhooks/linear
GET /healthz
```

The receiver verifies `Linear-Signature` as HMAC-SHA256 over the raw body using
the secret named by `webhook.signing_secret_env`. It also enforces
`webhookTimestamp` freshness when present and dedupes by `Linear-Delivery`, with
a body-hash fallback. Use `--dedupe-file` to persist delivery IDs across process
restarts.

## Linear Webhook

Configure a Linear webhook for these resource types:

- `Comment`
- `Issue`
- `IssueLabel`

The webhook should point at the deployed watcher URL. The production signing
secret should be stored in Doppler or the service manager and exposed as
`LINEAR_WEBHOOK_SECRET` unless the config uses another env var.

Polling remains a fallback only when Linear webhooks are unavailable. Run it
with an explicit token env, page size, and conservative interval:

```sh
LINEAR_API_KEY=... go run ./tools/agent-watcher \
  --config tools/agent-watcher/watcher.example.yaml \
  --mode poll \
  --linear-token-env LINEAR_API_KEY \
  --poll-interval 30s \
  --poll-page-size 50
```

Polling queries recently updated issues ordered by `updatedAt`, caps page size
at 100, and surfaces Linear rate-limit responses as poll errors. Do not replace
webhook mode with aggressive polling.

## Config

Use valid YAML with list entries, not duplicate keys:

```yaml
forbidden_for:
  - project: "Symphony"
    actors: ["hermes"]
  - project: "De Novo"
    actors: ["hermes"]
```

The config validator rejects duplicate YAML keys, duplicate project/actor rules,
unknown actors in `forbidden_for`, invalid rate limits, missing alert targets,
and missing webhook signing-secret env names.

## Alert Taxonomy

- `forbidden_project_write`: configured actor wrote in a forbidden project.
- `owner_label_conflict`: issue carried more than one `owner:*` label.
- `actor_rate_limit`: actor exceeded `actor_writes_per_minute`.

Watcher-owned events are ignored using `watcher_actor`. Unconfigured normal
human writes are not alerting by themselves; unknown actor identities remain in
the normalized event stream so owner-label conflicts and rate-limit smoke
detection still have evidence.

## Slack And Linear Alerts

The Slack envelope uses `agents_bridge_v1` metadata with `kind: "block"`,
top-level `text`, `linear_id`, and a short non-secret `reason`. The Linear
comment body contains the same stable reason code and the configured human
mention.

The current executable emits alert JSON to stdout so fixture tests and systemd
logs are deterministic. Production wiring should supply Slack/Linear tokens and
use the tested sinks in `internal/agentwatcher` to post to `#agents-bridge` and
to the violating Linear issue. Do not include raw webhook payloads or secrets in
alerts.

Slack needs `chat:write` for the bot token, and the bot must be a member of
`#agents-bridge`. Linear comment writes should use the dedicated watcher identity
from the Phase 1 substrate when available; if that identity is deferred, record
the temporary token owner in the deployment proof.

Hadto live substrate refresh on 2026-06-04:

- `#agents-bridge` exists as `C0B83H1F15K`.
- The Hermes Slack bot authenticates as `hermes` and is a member of that channel.
- `#hermes-ops` and `#denovo-ops` still require a Slack token with
  `channels:manage`; the current bot token returns `missing_scope` for channel
  creation.
- `HERMES_LINEAR_TOKEN`, `DENOVO_LINEAR_TOKEN`, and `WATCHER_LINEAR_TOKEN` are
  still absent from Doppler `lenovo_server/dev`; live Linear comment proof should
  wait for the watcher identity decision or explicitly document fallback token
  ownership.

## user-systemd

Example unit:

```ini
[Unit]
Description=Symphony Agent Watcher

[Service]
WorkingDirectory=/home/david/stacks/symphony
EnvironmentFile=%h/.config/symphony/agent-watcher.env
ExecStart=/usr/local/go/bin/go run ./tools/agent-watcher --config tools/agent-watcher/watcher.example.yaml --listen 127.0.0.1:18080 --dedupe-file %h/.local/state/symphony/agent-watcher-dedupe.json
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
```

Prefer materializing secrets through the service manager or Doppler rather than
editing them into the unit file.

Operational checks:

```sh
systemctl --user status symphony-agent-watcher --no-pager -l
journalctl --user -u symphony-agent-watcher -n 100 --no-pager
curl -fsS http://127.0.0.1:18080/healthz
```

Config is loaded at process start. To reload config, update the config or env
file, then run:

```sh
systemctl --user restart symphony-agent-watcher
curl -fsS http://127.0.0.1:18080/healthz
```

The `loaded_at` field should change after restart.

False-positive triage:

- Check the alert `reason`, `actor`, `linear_id`, and `project`.
- Confirm the actor identity in `watcher.example.yaml` matches the live Linear
  user/app/bot identity.
- Confirm the issue has exactly one `owner:*` label.
- Add a missing agent identity or forbidden-project rule only after the source
  event is understood.

## Proof

Before PR review:

```sh
git diff --check
go test ./...
go vet ./...
```

Operational proof after deploy:

- Synthetic Hermes/bot comment on a Symphony issue alerts within 60 seconds.
- Synthetic rate-limit fixture of 50 writes in 60 seconds emits one alert.
- No false positives during a 48 hour soak.

If live credentials or webhook setup are unavailable, leave the live-proof
checklist item unchecked and record the missing setup in Linear.

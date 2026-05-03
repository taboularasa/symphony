package agentwatcher

import (
	"strings"
	"testing"
)

func TestDecodeConfigValidatesRules(t *testing.T) {
	cfg, err := DecodeConfig(strings.NewReader(validConfigYAML()))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !cfg.IsForbidden("Symphony", "hermes") {
		t.Fatal("expected Symphony to be forbidden for hermes")
	}
}

func TestDecodeConfigRejectsDuplicateYAMLKeys(t *testing.T) {
	_, err := DecodeConfig(strings.NewReader(`
forbidden_for:
  - project: Symphony
    actors: [hermes]
forbidden_for:
  - project: De Novo
    actors: [hermes]
actors:
  - key: hermes
    emails: [hermes-bot@hadto.net]
rate_limits:
  actor_writes_per_minute: 30
alerts:
  slack_channel_id: C123
webhook:
  signing_secret_env: LINEAR_WEBHOOK_SECRET
  timestamp_tolerance_seconds: 60
`))
	if err == nil {
		t.Fatal("expected duplicate key error")
	}
}

func TestDecodeConfigRejectsUnknownActor(t *testing.T) {
	_, err := DecodeConfig(strings.NewReader(strings.Replace(validConfigYAML(), "actors: [hermes]", "actors: [missing]", 1)))
	if err == nil {
		t.Fatal("expected unknown actor error")
	}
}

func validConfigYAML() string {
	return `
forbidden_for:
  - project: Symphony
    actors: [hermes]
  - project: De Novo
    actors: [hermes]
actors:
  - key: hermes
    linear_user_ids: [user-hermes]
    emails: [hermes-bot@hadto.net]
watcher_actor: watcher
rate_limits:
  actor_writes_per_minute: 30
alerts:
  slack_channel_id: C123
  human_mention: "@david"
webhook:
  signing_secret_env: LINEAR_WEBHOOK_SECRET
  timestamp_tolerance_seconds: 60
`
}

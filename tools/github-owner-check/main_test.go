package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunAllowsExpectedApp(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{
		"--policy", "../../config/github-owner-backstop.yaml",
		"--repository", "taboularasa/de-novo",
		"--branch", "HAD-665/github-backstop",
		"--head-sha", "abc123",
		"--owner-label", "owner:denovo",
		"--event-sender-login", "denovo-bot[bot]",
		"--event-sender-type", "Bot",
		"--linear-token-env", "",
	}, &out)
	if err != nil {
		t.Fatalf("run() error = %v; out=%s", err, out.String())
	}
	if !strings.Contains(out.String(), `"status": "allow"`) || !strings.Contains(out.String(), `"reason_code": "allowed_app"`) || !strings.Contains(out.String(), `"head_sha": "abc123"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestRunDeniesCrossOwnerApp(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{
		"--policy", "../../config/github-owner-backstop.yaml",
		"--repository", "taboularasa/de-novo",
		"--branch", "HAD-665/github-backstop",
		"--owner-label", "owner:denovo",
		"--actor-login", "hermes-bot[bot]",
		"--actor-type", "Bot",
		"--linear-token-env", "",
	}, &out)
	if err == nil {
		t.Fatalf("run() error = nil; out=%s", out.String())
	}
	if !strings.Contains(out.String(), `"status": "deny"`) || !strings.Contains(out.String(), `"reason_code": "actor_not_allowed"`) {
		t.Fatalf("output = %s", out.String())
	}
}

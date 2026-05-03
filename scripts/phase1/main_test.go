package main

import (
	"os"
	"strings"
	"testing"
)

func TestRunRequiresSubcommand(t *testing.T) {
	err := run(nil)
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "missing subcommand") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunRejectsUnknownSubcommand(t *testing.T) {
	err := run([]string{"unknown"})
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunLabelsRequiresTokenEnv(t *testing.T) {
	t.Setenv("SYMPHONY_TEST_LINEAR_TOKEN", "")
	err := run([]string{"labels", "--token-env", "SYMPHONY_TEST_LINEAR_TOKEN"})
	if err == nil {
		t.Fatal("expected missing token error")
	}
	if !strings.Contains(err.Error(), "SYMPHONY_TEST_LINEAR_TOKEN is not set") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunBackfillRequiresTokenEnvAfterValidPolicy(t *testing.T) {
	t.Setenv("SYMPHONY_TEST_LINEAR_TOKEN", "")
	err := run([]string{
		"backfill",
		"--policy", "backfill_policy.example.json",
		"--token-env", "SYMPHONY_TEST_LINEAR_TOKEN",
	})
	if err == nil {
		t.Fatal("expected missing token error")
	}
	if !strings.Contains(err.Error(), "SYMPHONY_TEST_LINEAR_TOKEN is not set") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunBackfillRejectsBadPolicy(t *testing.T) {
	err := run([]string{
		"backfill",
		"--policy", "missing-policy.json",
		"--token-env", "SYMPHONY_TEST_LINEAR_TOKEN",
	})
	if err == nil {
		t.Fatal("expected policy error")
	}
	if !strings.Contains(err.Error(), "open policy") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunBackfillRejectsNonPositiveTimeout(t *testing.T) {
	err := run([]string{
		"backfill",
		"--policy", "backfill_policy.example.json",
		"--timeout", "0s",
		"--token-env", "SYMPHONY_TEST_LINEAR_TOKEN",
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout must be positive") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunBackfillRejectsPlanWithoutApply(t *testing.T) {
	err := run([]string{
		"backfill",
		"--plan-json", "dry-run.json",
	})
	if err == nil {
		t.Fatal("expected plan-json error")
	}
	if !strings.Contains(err.Error(), "plan-json requires --apply") {
		t.Fatalf("error = %v", err)
	}
}

func TestReadBackfillPlanRejectsPolicyMismatch(t *testing.T) {
	path := t.TempDir() + "/plan.json"
	err := os.WriteFile(path, []byte(`{
		"policy_hash":"old",
		"issue_count":1,
		"decisions":[{"issue_id":"issue-1","identifier":"HAD-1","action":"apply","applied_label":"owner:human"}]
	}`), 0o644)
	if err != nil {
		t.Fatalf("write plan: %v", err)
	}
	_, err = readBackfillPlan(path, "current")
	if err == nil {
		t.Fatal("expected policy mismatch")
	}
	if !strings.Contains(err.Error(), "does not match current policy hash") {
		t.Fatalf("error = %v", err)
	}
}

func TestModeName(t *testing.T) {
	if got := modeName(false); got != "dry-run" {
		t.Fatalf("modeName(false) = %q", got)
	}
	if got := modeName(true); got != "apply" {
		t.Fatalf("modeName(true) = %q", got)
	}
}

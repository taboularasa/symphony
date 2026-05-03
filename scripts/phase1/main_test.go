package main

import (
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

func TestModeName(t *testing.T) {
	if got := modeName(false); got != "dry-run" {
		t.Fatalf("modeName(false) = %q", got)
	}
	if got := modeName(true); got != "apply" {
		t.Fatalf("modeName(true) = %q", got)
	}
}

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/taboularasa/symphony/internal/agentwatcher"
)

func TestRunRejectsUnknownMode(t *testing.T) {
	err := run([]string{"--config", "watcher.example.yaml", "--mode", "bad"})
	if err == nil {
		t.Fatal("expected bad mode error")
	}
	if !strings.Contains(err.Error(), "mode must be") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunPollRequiresTokenEnv(t *testing.T) {
	t.Setenv("SYMPHONY_EMPTY_LINEAR_TOKEN", "")
	err := run([]string{
		"--config", "watcher.example.yaml",
		"--mode", "poll",
		"--linear-token-env", "SYMPHONY_EMPTY_LINEAR_TOKEN",
	})
	if err == nil {
		t.Fatal("expected missing token error")
	}
	if !strings.Contains(err.Error(), "SYMPHONY_EMPTY_LINEAR_TOKEN is not set") {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildAlertSinkDefaultsToStdout(t *testing.T) {
	sink, err := buildAlertSink(alertSinkOptions{
		Config: loadTestConfig(t),
		Stdout: true,
	})
	if err != nil {
		t.Fatalf("buildAlertSink() error = %v", err)
	}
	if _, ok := sink.(stdoutSink); !ok {
		t.Fatalf("sink = %T, want stdoutSink", sink)
	}
}

func TestBuildAlertSinkRequiresAtLeastOneSink(t *testing.T) {
	_, err := buildAlertSink(alertSinkOptions{
		Config: loadTestConfig(t),
	})
	if err == nil || !strings.Contains(err.Error(), "at least one alert sink") {
		t.Fatalf("buildAlertSink() error = %v, want no sink error", err)
	}
}

func TestBuildAlertSinkRequiresConfiguredSlackToken(t *testing.T) {
	t.Setenv("SYMPHONY_EMPTY_SLACK_TOKEN", "")
	_, err := buildAlertSink(alertSinkOptions{
		Config:        loadTestConfig(t),
		Stdout:        true,
		SlackTokenEnv: "SYMPHONY_EMPTY_SLACK_TOKEN",
	})
	if err == nil || !strings.Contains(err.Error(), "SYMPHONY_EMPTY_SLACK_TOKEN is not set") {
		t.Fatalf("buildAlertSink() error = %v, want missing Slack token", err)
	}
}

func TestBuildAlertSinkRequiresConfiguredLinearCommentToken(t *testing.T) {
	t.Setenv("SYMPHONY_EMPTY_LINEAR_COMMENT_TOKEN", "")
	_, err := buildAlertSink(alertSinkOptions{
		Config:                loadTestConfig(t),
		Stdout:                true,
		LinearCommentTokenEnv: "SYMPHONY_EMPTY_LINEAR_COMMENT_TOKEN",
	})
	if err == nil || !strings.Contains(err.Error(), "SYMPHONY_EMPTY_LINEAR_COMMENT_TOKEN is not set") {
		t.Fatalf("buildAlertSink() error = %v, want missing Linear comment token", err)
	}
}

func TestBuildAlertSinkCombinesConfiguredSinks(t *testing.T) {
	t.Setenv("SYMPHONY_TEST_SLACK_TOKEN", "xoxb-test")
	t.Setenv("SYMPHONY_TEST_LINEAR_COMMENT_TOKEN", "lin-test")
	sink, err := buildAlertSink(alertSinkOptions{
		Config:                loadTestConfig(t),
		Stdout:                true,
		SlackTokenEnv:         "SYMPHONY_TEST_SLACK_TOKEN",
		LinearCommentTokenEnv: "SYMPHONY_TEST_LINEAR_COMMENT_TOKEN",
	})
	if err != nil {
		t.Fatalf("buildAlertSink() error = %v", err)
	}
	multi, ok := sink.(multiAlertSink)
	if !ok {
		t.Fatalf("sink = %T, want multiAlertSink", sink)
	}
	if len(multi.sinks) != 3 {
		t.Fatalf("sink count = %d, want 3", len(multi.sinks))
	}
}

func TestHealthHandler(t *testing.T) {
	loadedAt := time.Date(2026, 5, 3, 21, 0, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	healthHandler("webhook", "watcher.example.yaml", loadedAt).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"status":"ok"`, `"mode":"webhook"`, `"loaded_at":"2026-05-03T21:00:00Z"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func loadTestConfig(t *testing.T) agentwatcher.Config {
	t.Helper()
	cfg, err := agentwatcher.LoadConfig("watcher.example.yaml")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	return cfg
}

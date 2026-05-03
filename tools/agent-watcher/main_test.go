package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

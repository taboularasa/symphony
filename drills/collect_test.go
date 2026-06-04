package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCollectSlackBridgeEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer slack-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.URL.Query().Get("channel"); got != "C0B83H1F15K" {
			t.Fatalf("channel = %q", got)
		}
		_, _ = w.Write([]byte(`{
			"ok": true,
			"messages": [
				{
					"ts": "1780570800.000000",
					"metadata": {
						"event_type": "agents_bridge_v1",
						"event_payload": {
							"v": 1,
							"from": "denovo",
							"kind": "ack",
							"linear_id": "HAD-2001"
						}
					}
				},
				{"ts": "1780570801.000000", "text": "ordinary note"}
			]
		}`))
	}))
	defer server.Close()

	events, err := collectSlackEvents(context.Background(), server.Client(), SlackCollectConfig{
		Endpoint: server.URL,
		Token:    "slack-token",
		Channel:  "C0B83H1F15K",
	}, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("collect slack: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Source != "slack" || events[0].Kind != "ack" || events[0].Actor != "denovo" || events[0].LinearID != "HAD-2001" {
		t.Fatalf("event = %#v", events[0])
	}
}

func TestCollectSlackHandlesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"missing_scope"}`))
	}))
	defer server.Close()

	_, err := collectSlackEvents(context.Background(), server.Client(), SlackCollectConfig{
		Endpoint: server.URL,
		Token:    "slack-token",
		Channel:  "C123",
	}, time.Time{}, time.Time{})
	if err == nil || !strings.Contains(err.Error(), "missing_scope") {
		t.Fatalf("err = %v", err)
	}
}

func TestCollectGitHubPREvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/repos/taboularasa/de-novo/pulls/200" {
			t.Fatalf("path = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer gh-token" {
			t.Fatalf("authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{
			"html_url": "https://github.com/taboularasa/de-novo/pull/200",
			"created_at": "2026-06-04T15:10:00Z",
			"merged_at": "2026-06-04T15:20:00Z",
			"user": {"login": "denovo"},
			"merged_by": {"login": "human"}
		}`))
	}))
	defer server.Close()

	events, err := collectGitHubEvents(context.Background(), server.Client(), GitHubCollectConfig{
		APIBase:  server.URL,
		Token:    "gh-token",
		PRURL:    "https://github.com/taboularasa/de-novo/pull/200",
		LinearID: "HAD-2001",
	})
	if err != nil {
		t.Fatalf("collect github: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Kind != "pr_opened" || events[1].Kind != "pr_merged" || events[1].Actor != "human" {
		t.Fatalf("events = %#v", events)
	}
}

func TestCollectLinearEventsFromCommentsAndSnapshots(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "linear-token" {
			t.Fatalf("authorization = %q", got)
		}
		var req struct {
			Variables map[string]string `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Variables["parent"] != "HAD-2000" || req.Variables["child"] != "HAD-2001" {
			t.Fatalf("variables = %#v", req.Variables)
		}
		_, _ = w.Write([]byte(`{
			"data": {
				"parent": {
					"identifier": "HAD-2000",
					"createdAt": "2026-06-04T15:00:00Z",
					"updatedAt": "2026-06-04T15:25:00Z",
					"state": {"name": "Done", "type": "completed"},
					"parent": null,
					"labels": {"nodes": [{"name": "owner:hermes"}]},
					"comments": {"nodes": []}
				},
				"child": {
					"identifier": "HAD-2001",
					"createdAt": "2026-06-04T15:03:00Z",
					"updatedAt": "2026-06-04T15:06:00Z",
					"state": {"name": "In Progress", "type": "started"},
					"parent": {"identifier": "HAD-2000"},
					"labels": {"nodes": [{"name": "owner:denovo"}]},
					"comments": {"nodes": [{
						"createdAt": "2026-06-04T15:05:00Z",
						"body": "\u0060\u0060\u0060symphony-drill:event\n{\"source\":\"hermes_log\",\"kind\":\"adversarial_refusal\",\"actor\":\"hermes\",\"linear_id\":\"HAD-2001\",\"owner_label\":\"owner:denovo\",\"outcome\":\"dispatch_denied\"}\n\u0060\u0060\u0060",
						"user": {"name": "david"}
					}]}
				}
			}
		}`))
	}))
	defer server.Close()

	events, err := collectLinearEvents(context.Background(), server.Client(), LinearCollectConfig{
		Endpoint: server.URL,
		Token:    "linear-token",
		ParentID: "HAD-2000",
		ChildID:  "HAD-2001",
	})
	if err != nil {
		t.Fatalf("collect linear: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Kind != "issue_snapshot" || events[1].ParentLinearID != "HAD-2000" {
		t.Fatalf("snapshots = %#v", events[:2])
	}
	got := events[2]
	if got.Source != "hermes_log" || got.Kind != "adversarial_refusal" || got.TS != "2026-06-04T15:05:00Z" {
		t.Fatalf("comment event = %#v", got)
	}
}

func TestBuildCollectConfigRequiresExplicitTokenEnv(t *testing.T) {
	t.Setenv("SLACK_TOKEN_FOR_TEST", "slack-token")
	cfg, err := buildCollectConfig(collectCLIOptions{
		slackEndpoint: "https://slack.example/history",
		slackTokenEnv: "SLACK_TOKEN_FOR_TEST",
		slackChannel:  "C123",
	})
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	if cfg.Slack.Token != "slack-token" {
		t.Fatalf("token = %q", cfg.Slack.Token)
	}
	_, err = buildCollectConfig(collectCLIOptions{slackChannel: "C123"})
	if err == nil || !strings.Contains(err.Error(), "slack-token-env") {
		t.Fatalf("err = %v", err)
	}
}

func TestCollectLiveArtifactsMergesSources(t *testing.T) {
	slack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"messages":[]}`))
	}))
	defer slack.Close()
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"html_url":"https://github.com/taboularasa/de-novo/pull/200",
			"created_at":"2026-06-04T15:10:00Z",
			"merged_at":null,
			"user":{"login":"denovo"}
		}`))
	}))
	defer github.Close()
	run, err := CollectLiveArtifacts(context.Background(), CollectConfig{
		Scenario: scenarioHandoff001,
		RunID:    "test-run",
		Client:   github.Client(),
		Slack: SlackCollectConfig{
			Endpoint: slack.URL,
			Token:    "slack-token",
			Channel:  "C123",
		},
		GitHub: GitHubCollectConfig{
			APIBase:  github.URL,
			PRURL:    "https://github.com/taboularasa/de-novo/pull/200",
			LinearID: "HAD-2001",
		},
	})
	if err != nil {
		t.Fatalf("collect live artifacts: %v", err)
	}
	if run.RunID != "test-run" || len(run.Events) != 1 || run.Events[0].Kind != "pr_opened" {
		t.Fatalf("run = %#v", run)
	}
}

func TestWriteRun(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/events.json"
	if err := writeRun(path, DrillRun{Scenario: scenarioHandoff001, Events: []Event{{TS: "2026-06-04T15:00:00Z", Source: "linear", Kind: "intake_created"}}}); err != nil {
		t.Fatalf("write run: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if !strings.Contains(string(body), "\"handoff-001\"") {
		t.Fatalf("body = %s", body)
	}
}

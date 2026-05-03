package agentwatcher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildSlackBridgeMessage(t *testing.T) {
	alert := Alert{Reason: "forbidden_project_write", LinearID: "HAD-651", Project: "Symphony", OccurredAt: "2026-05-03T21:00:00Z"}
	msg := BuildSlackBridgeMessage("C123", alert)
	if msg.Metadata.EventType != "agents_bridge_v1" {
		t.Fatalf("event type = %s", msg.Metadata.EventType)
	}
	if msg.Metadata.EventPayload.Kind != "block" || msg.Metadata.EventPayload.LinearID != "HAD-651" {
		t.Fatalf("payload = %+v", msg.Metadata.EventPayload)
	}
	if msg.Text == "" {
		t.Fatal("missing fallback text")
	}
}

func TestSlackSinkSendsMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q", got)
		}
		var payload SlackBridgeMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.Channel != "C123" || payload.Metadata.EventPayload.Kind != "block" {
			t.Fatalf("payload = %+v", payload)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	sink := SlackSink{Endpoint: server.URL, Token: "test-token", Channel: "C123", Client: server.Client()}
	if err := sink.Send(context.Background(), Alert{Reason: "forbidden_project_write", LinearID: "HAD-1", OccurredAt: "2026-05-03T21:00:00Z"}); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func TestSlackSinkHandlesMetadataFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload SlackBridgeMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.Metadata.EventType == "" {
			t.Fatal("missing metadata")
		}
		_, _ = w.Write([]byte(`{"ok":false,"error":"metadata_must_be_sent_from_app"}`))
	}))
	defer server.Close()

	sink := SlackSink{Endpoint: server.URL, Token: "test-token", Channel: "C123", Client: server.Client()}
	err := sink.Send(context.Background(), Alert{Reason: "forbidden_project_write", LinearID: "HAD-1", OccurredAt: "2026-05-03T21:00:00Z"})
	if err == nil {
		t.Fatal("expected metadata failure")
	}
}

func TestLinearCommentSinkSendsComment(t *testing.T) {
	client := &fakeCommentClient{success: true}
	sink := LinearCommentSink{Client: client, HumanMention: "@david"}
	if err := sink.Send(context.Background(), Alert{Reason: "owner_label_conflict", LinearID: "HAD-1", Actor: "hermes", Project: "Symphony"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !strings.Contains(client.query, "commentCreate") {
		t.Fatalf("query = %s", client.query)
	}
	if client.variables["issueId"] != "HAD-1" {
		t.Fatalf("variables = %#v", client.variables)
	}
}

func TestLinearCommentSinkReturnsFailure(t *testing.T) {
	client := &fakeCommentClient{err: errors.New("linear down")}
	sink := LinearCommentSink{Client: client, HumanMention: "@david"}
	if err := sink.Send(context.Background(), Alert{Reason: "owner_label_conflict", LinearID: "HAD-1"}); err == nil {
		t.Fatal("expected send failure")
	}
}

func TestBuildLinearComment(t *testing.T) {
	comment := BuildLinearComment(Alert{Reason: "owner_label_conflict", LinearID: "HAD-1", Actor: "hermes", Project: "Symphony"}, "@david")
	if comment.IssueID != "HAD-1" {
		t.Fatalf("issue = %s", comment.IssueID)
	}
	if comment.Body == "" || comment.Body[:6] != "@david" {
		t.Fatalf("body = %s", comment.Body)
	}
}

type fakeCommentClient struct {
	query     string
	variables map[string]any
	success   bool
	err       error
}

func (c *fakeCommentClient) Do(ctx context.Context, query string, variables any, out any) error {
	c.query = query
	c.variables, _ = variables.(map[string]any)
	if c.err != nil {
		return c.err
	}
	response, ok := out.(*struct {
		CommentCreate struct {
			Success bool `json:"success"`
		} `json:"commentCreate"`
	})
	if ok {
		response.CommentCreate.Success = c.success
	}
	return nil
}

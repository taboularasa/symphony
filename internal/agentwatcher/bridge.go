package agentwatcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type SlackBridgeMessage struct {
	Channel  string        `json:"channel"`
	Text     string        `json:"text"`
	Metadata SlackMetadata `json:"metadata"`
	Blocks   []SlackBlock  `json:"blocks,omitempty"`
}

type SlackMetadata struct {
	EventType    string            `json:"event_type"`
	EventPayload SlackEventPayload `json:"event_payload"`
}

type SlackEventPayload struct {
	V        int    `json:"v"`
	From     string `json:"from"`
	Kind     string `json:"kind"`
	LinearID string `json:"linear_id"`
	Reason   string `json:"reason"`
	TS       string `json:"ts"`
}

type SlackBlock struct {
	Type string          `json:"type"`
	Text *SlackBlockText `json:"text,omitempty"`
}

type SlackBlockText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func BuildSlackBridgeMessage(channelID string, alert Alert) SlackBridgeMessage {
	text := fmt.Sprintf("block %s from watcher: %s", alert.LinearID, alert.Reason)
	return SlackBridgeMessage{
		Channel: channelID,
		Text:    text,
		Metadata: SlackMetadata{
			EventType: "agents_bridge_v1",
			EventPayload: SlackEventPayload{
				V:        1,
				From:     "watcher",
				Kind:     "block",
				LinearID: alert.LinearID,
				Reason:   alert.Reason,
				TS:       alert.OccurredAt,
			},
		},
		Blocks: []SlackBlock{
			{
				Type: "section",
				Text: &SlackBlockText{
					Type: "mrkdwn",
					Text: fmt.Sprintf("*%s* on `%s` in %s\n%s", alert.Reason, alert.LinearID, alert.Project, alert.IssueURL),
				},
			},
		},
	}
}

type SlackSink struct {
	Endpoint string
	Token    string
	Channel  string
	Client   *http.Client
}

func (s SlackSink) Send(ctx context.Context, alert Alert) error {
	if strings.TrimSpace(s.Token) == "" {
		return fmt.Errorf("slack token is required")
	}
	endpoint := strings.TrimSpace(s.Endpoint)
	if endpoint == "" {
		endpoint = "https://slack.com/api/chat.postMessage"
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	payload := BuildSlackBridgeMessage(s.Channel, alert)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.Token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("slack post failed: http %d", resp.StatusCode)
	}
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if !out.OK {
		if out.Error == "metadata_must_be_sent_from_app" {
			return fmt.Errorf("slack metadata rejected: %s", out.Error)
		}
		return fmt.Errorf("slack post failed: %s", out.Error)
	}
	return nil
}

type LinearCommentSink struct {
	Client       GraphQLClient
	HumanMention string
}

func (s LinearCommentSink) Send(ctx context.Context, alert Alert) error {
	if s.Client == nil {
		return fmt.Errorf("linear comment client is required")
	}
	comment := BuildLinearComment(alert, s.HumanMention)
	var out struct {
		CommentCreate struct {
			Success bool `json:"success"`
		} `json:"commentCreate"`
	}
	if err := s.Client.Do(ctx, linearCommentMutation, map[string]any{
		"issueId": comment.IssueID,
		"body":    comment.Body,
	}, &out); err != nil {
		return err
	}
	if !out.CommentCreate.Success {
		return fmt.Errorf("linear comment create returned success=false")
	}
	return nil
}

type LinearComment struct {
	IssueID string `json:"issue_id"`
	Body    string `json:"body"`
}

func BuildLinearComment(alert Alert, humanMention string) LinearComment {
	prefix := strings.TrimSpace(humanMention)
	if prefix != "" {
		prefix += " "
	}
	return LinearComment{
		IssueID: alert.LinearID,
		Body:    fmt.Sprintf("%sAgent watcher alert: `%s` by `%s` in `%s`. Reason: `%s`.", prefix, alert.LinearID, alert.Actor, alert.Project, alert.Reason),
	}
}

const linearCommentMutation = `
mutation AgentWatcherComment($issueId: String!, $body: String!) {
  commentCreate(input: { issueId: $issueId, body: $body }) {
    success
  }
}`

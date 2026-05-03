package agentwatcher

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Event struct {
	DeliveryID string
	ActorKey   string
	ActorID    string
	ActorName  string
	ActorEmail string
	AppID      string
	BotID      string
	IssueID    string
	Identifier string
	IssueURL   string
	Project    string
	Action     string
	Labels     []string
	CreatedAt  time.Time
	Source     string
}

func NormalizeLinearWebhook(body []byte) (Event, error) {
	var payload linearWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return Event{}, fmt.Errorf("decode linear webhook payload: %w", err)
	}
	event := Event{
		DeliveryID: payload.DeliveryID,
		ActorID:    payload.Actor.ID,
		ActorName:  payload.Actor.Name,
		ActorEmail: payload.Actor.Email,
		AppID:      payload.Actor.AppID,
		BotID:      payload.Actor.BotID,
		IssueID:    payload.Data.ID,
		Identifier: payload.Data.Identifier,
		IssueURL:   payload.Data.URL,
		Project:    payload.Data.Project.Name,
		Action:     normalizeAction(payload),
		Source:     "linear_webhook",
	}
	if payload.Data.Issue.ID != "" {
		event.IssueID = payload.Data.Issue.ID
	}
	if payload.Data.Issue.Identifier != "" {
		event.Identifier = payload.Data.Issue.Identifier
	}
	if payload.Data.Issue.URL != "" {
		event.IssueURL = payload.Data.Issue.URL
	}
	if payload.Data.Issue.Project.Name != "" {
		event.Project = payload.Data.Issue.Project.Name
	}
	if !payload.CreatedAt.IsZero() {
		event.CreatedAt = payload.CreatedAt
	}
	for _, label := range payload.Data.Labels.Nodes {
		event.Labels = append(event.Labels, label.Name)
	}
	if payload.Data.Label.Name != "" {
		event.Labels = append(event.Labels, payload.Data.Label.Name)
	}
	return event, nil
}

func normalizeAction(payload linearWebhookPayload) string {
	resource := strings.ToLower(strings.TrimSpace(payload.Type))
	action := strings.ToLower(strings.TrimSpace(payload.Action))
	if resource == "" {
		resource = strings.ToLower(strings.TrimSpace(payload.WebhookType))
	}
	if action == "" {
		action = "updated"
	}
	switch resource {
	case "comment":
		return "comment"
	case "issuelabel", "issue_label":
		return "label_change"
	case "issue":
		if _, ok := payload.UpdatedFrom["assigneeId"]; ok {
			return "self_assign"
		}
		if _, ok := payload.UpdatedFrom["stateId"]; ok {
			return "state_transition"
		}
		return "issue_update"
	default:
		if resource == "" {
			return action
		}
		return resource + "_" + action
	}
}

type linearWebhookPayload struct {
	Type             string                     `json:"type"`
	WebhookType      string                     `json:"webhookType"`
	Action           string                     `json:"action"`
	DeliveryID       string                     `json:"deliveryId"`
	WebhookTimestamp linearTimestamp            `json:"webhookTimestamp"`
	CreatedAt        time.Time                  `json:"createdAt"`
	Actor            linearActor                `json:"actor"`
	Data             linearWebhookData          `json:"data"`
	UpdatedFrom      map[string]json.RawMessage `json:"updatedFrom"`
}

type linearActor struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	AppID string `json:"app_id"`
	BotID string `json:"bot_id"`
}

type linearWebhookData struct {
	ID         string         `json:"id"`
	Identifier string         `json:"identifier"`
	URL        string         `json:"url"`
	Project    linearProject  `json:"project"`
	Issue      linearIssueRef `json:"issue"`
	Label      linearLabelRef `json:"label"`
	Labels     struct {
		Nodes []linearLabelRef `json:"nodes"`
	} `json:"labels"`
}

type linearProject struct {
	Name string `json:"name"`
}

type linearIssueRef struct {
	ID         string        `json:"id"`
	Identifier string        `json:"identifier"`
	URL        string        `json:"url"`
	Project    linearProject `json:"project"`
}

type linearLabelRef struct {
	Name string `json:"name"`
}

type linearTimestamp struct {
	time.Time
}

func (t *linearTimestamp) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		parsed, parseErr := time.Parse(time.RFC3339Nano, text)
		if parseErr != nil {
			return parseErr
		}
		t.Time = parsed
		return nil
	}
	var millis int64
	if err := json.Unmarshal(data, &millis); err != nil {
		return err
	}
	t.Time = time.UnixMilli(millis).UTC()
	return nil
}

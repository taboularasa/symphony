package agentwatcher

import "testing"

func TestNormalizeLinearWebhookComment(t *testing.T) {
	event, err := NormalizeLinearWebhook([]byte(`{
		"type":"Comment",
		"deliveryId":"delivery-1",
		"actor":{"id":"user-hermes","name":"Hermes","email":"hermes-bot@hadto.net"},
		"data":{
			"id":"comment-1",
			"issue":{"id":"issue-1","identifier":"HAD-651","url":"https://linear.app/hadto/issue/HAD-651","project":{"name":"Symphony"}}
		}
	}`))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if event.Action != "comment" {
		t.Fatalf("action = %s", event.Action)
	}
	if event.Identifier != "HAD-651" || event.Project != "Symphony" {
		t.Fatalf("event = %+v", event)
	}
}

func TestNormalizeLinearWebhookIssueStateTransition(t *testing.T) {
	event, err := NormalizeLinearWebhook([]byte(`{
		"type":"Issue",
		"actor":{"id":"user-hermes"},
		"updatedFrom":{"stateId":"old-state"},
		"data":{"id":"issue-1","identifier":"HAD-651","project":{"name":"Symphony"}}
	}`))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if event.Action != "state_transition" {
		t.Fatalf("action = %s", event.Action)
	}
}

func TestNormalizeLinearWebhookSelfAssignment(t *testing.T) {
	event, err := NormalizeLinearWebhook([]byte(`{
		"type":"Issue",
		"actor":{"id":"user-hermes"},
		"updatedFrom":{"assigneeId":"old-user"},
		"data":{"id":"issue-1","identifier":"HAD-651","project":{"name":"Symphony"}}
	}`))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if event.Action != "self_assign" {
		t.Fatalf("action = %s", event.Action)
	}
}

func TestNormalizeLinearWebhookLabels(t *testing.T) {
	event, err := NormalizeLinearWebhook([]byte(`{
		"type":"Issue",
		"actor":{"id":"user-hermes"},
		"data":{
			"id":"issue-1",
			"identifier":"HAD-651",
			"project":{"name":"Symphony"},
			"labels":{"nodes":[{"name":"owner:hermes"},{"name":"owner:denovo"}]}
		}
	}`))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !conflictOwnerLabels(event.Labels) {
		t.Fatalf("expected owner conflict: %+v", event.Labels)
	}
}

func TestNormalizeLinearWebhookUnknownActorIsExplicit(t *testing.T) {
	event, err := NormalizeLinearWebhook([]byte(`{
		"type":"Comment",
		"data":{"issue":{"id":"issue-1","identifier":"HAD-651","project":{"name":"Symphony"}}}
	}`))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if event.ActorID != "" || event.ActorEmail != "" {
		t.Fatalf("expected empty explicit unknown actor fields: %+v", event)
	}
	if event.Action != "comment" {
		t.Fatalf("action = %s", event.Action)
	}
}

func TestNormalizeLinearWebhookRejectsMalformedPayload(t *testing.T) {
	if _, err := NormalizeLinearWebhook([]byte(`{`)); err == nil {
		t.Fatal("expected malformed payload error")
	}
}

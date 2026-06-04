package agentwatcher

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taboularasa/symphony/internal/linear"
)

func TestPollerUsesConservativePaginationAndUpdatedOrdering(t *testing.T) {
	client := &fakeGraphQLClient{}
	since := time.Date(2026, 5, 3, 21, 0, 0, 0, time.UTC)
	events, err := (Poller{Client: client, PageSize: 500, MaxPages: 1}).Fetch(context.Background(), since)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v", events)
	}
	if !strings.Contains(client.query, "orderBy: updatedAt") {
		t.Fatalf("query missing updated ordering:\n%s", client.query)
	}
	if !strings.Contains(client.query, "$since: DateTimeOrDuration!") {
		t.Fatalf("query missing DateTimeOrDuration variable:\n%s", client.query)
	}
	if client.variables["first"] != 100 {
		t.Fatalf("page size = %#v", client.variables["first"])
	}
	if client.variables["since"] != "2026-05-03T21:00:00Z" {
		t.Fatalf("since = %#v", client.variables["since"])
	}
}

func TestPollerNormalizesIssueAndCommentEvents(t *testing.T) {
	since := time.Date(2026, 5, 3, 21, 0, 0, 0, time.UTC)
	client := &fakeGraphQLClient{response: pollResponse{
		Issues: struct {
			Nodes    []pollIssue `json:"nodes"`
			PageInfo pageInfo    `json:"pageInfo"`
		}{
			Nodes: []pollIssue{{
				ID:         "issue-1",
				Identifier: "HAD-651",
				URL:        "https://linear.app/hadto/issue/HAD-651",
				UpdatedAt:  since.Add(time.Second),
				Project:    linearProject{Name: "Symphony"},
				Assignee:   linearActor{ID: "user-human", Email: "david@hadto.net"},
				Labels: struct {
					Nodes []linearLabelRef `json:"nodes"`
				}{Nodes: []linearLabelRef{{Name: "owner:human"}}},
				Comments: struct {
					Nodes []pollComment `json:"nodes"`
				}{Nodes: []pollComment{{
					ID:        "comment-1",
					CreatedAt: since.Add(2 * time.Second),
					User:      linearActor{ID: "user-hermes", Email: "hermes-bot@hadto.net"},
				}}},
			}},
		},
	}}
	events, err := (Poller{Client: client, PageSize: 50, MaxPages: 1}).Fetch(context.Background(), since)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Action != "issue_update" || events[0].Source != "linear_poll" {
		t.Fatalf("issue event = %+v", events[0])
	}
	if events[1].Action != "comment" || events[1].ActorID != "user-hermes" {
		t.Fatalf("comment event = %+v", events[1])
	}
}

func TestPollerReturnsRateLimitError(t *testing.T) {
	client := &fakeGraphQLClient{err: linear.GraphQLErrors{{
		Message:    "rate limited",
		Extensions: map[string]any{"code": "RATELIMITED"},
	}}}
	_, err := (Poller{Client: client, PageSize: 50, MaxPages: 1}).Fetch(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	var rateErr PollRateLimitError
	if !errors.As(err, &rateErr) {
		t.Fatalf("error type = %T %[1]v", err)
	}
}

type fakeGraphQLClient struct {
	query     string
	variables map[string]any
	response  pollResponse
	err       error
}

func (c *fakeGraphQLClient) Do(ctx context.Context, query string, variables any, out any) error {
	c.query = query
	c.variables, _ = variables.(map[string]any)
	if c.err != nil {
		return c.err
	}
	if response, ok := out.(*pollResponse); ok {
		*response = c.response
	}
	return nil
}

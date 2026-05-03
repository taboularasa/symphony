package agentwatcher

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/taboularasa/symphony/internal/linear"
)

const linearPollQuery = `
query AgentWatcherPoll($since: DateTime!, $first: Int!, $after: String) {
  issues(filter: { updatedAt: { gte: $since } }, first: $first, after: $after, orderBy: updatedAt) {
    nodes {
      id
      identifier
      url
      updatedAt
      project { name }
      assignee { id name email }
      labels { nodes { name } }
      comments(first: 20) {
        nodes {
          id
          createdAt
          user { id name email }
        }
      }
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}`

type GraphQLClient interface {
	Do(ctx context.Context, query string, variables any, out any) error
}

type Poller struct {
	Client   GraphQLClient
	PageSize int
	MaxPages int
}

type PollRateLimitError struct {
	Err error
}

func (e PollRateLimitError) Error() string {
	return "linear polling rate limited: " + e.Err.Error()
}

func (e PollRateLimitError) Unwrap() error {
	return e.Err
}

func (p Poller) Fetch(ctx context.Context, since time.Time) ([]Event, error) {
	if p.Client == nil {
		return nil, errors.New("linear polling client is required")
	}
	pageSize := p.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}
	maxPages := p.MaxPages
	if maxPages <= 0 {
		maxPages = 5
	}

	var events []Event
	var after string
	for page := 0; page < maxPages; page++ {
		var out pollResponse
		variables := map[string]any{
			"since": since.UTC().Format(time.RFC3339Nano),
			"first": pageSize,
		}
		if after != "" {
			variables["after"] = after
		}
		if err := p.Client.Do(ctx, linearPollQuery, variables, &out); err != nil {
			if isRateLimited(err) {
				return nil, PollRateLimitError{Err: err}
			}
			return nil, fmt.Errorf("poll linear issues: %w", err)
		}
		events = append(events, eventsFromPollIssues(out.Issues.Nodes, since)...)
		if !out.Issues.PageInfo.HasNextPage || strings.TrimSpace(out.Issues.PageInfo.EndCursor) == "" {
			break
		}
		after = out.Issues.PageInfo.EndCursor
	}
	return events, nil
}

func isRateLimited(err error) bool {
	var httpErr linear.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	var gqlErrs linear.GraphQLErrors
	if errors.As(err, &gqlErrs) {
		for _, gqlErr := range gqlErrs {
			if strings.EqualFold(gqlErr.Code(), "RATELIMITED") {
				return true
			}
		}
	}
	return false
}

func eventsFromPollIssues(issues []pollIssue, since time.Time) []Event {
	var events []Event
	for _, issue := range issues {
		labels := labelNames(issue.Labels.Nodes)
		if issue.UpdatedAt.IsZero() || !issue.UpdatedAt.Before(since) {
			events = append(events, Event{
				DeliveryID: "poll:issue:" + issue.ID + ":" + issue.UpdatedAt.UTC().Format(time.RFC3339Nano),
				ActorID:    issue.Assignee.ID,
				ActorName:  issue.Assignee.Name,
				ActorEmail: issue.Assignee.Email,
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				IssueURL:   issue.URL,
				Project:    issue.Project.Name,
				Action:     "issue_update",
				Labels:     labels,
				CreatedAt:  issue.UpdatedAt,
				Source:     "linear_poll",
			})
		}
		for _, comment := range issue.Comments.Nodes {
			if !comment.CreatedAt.IsZero() && comment.CreatedAt.Before(since) {
				continue
			}
			events = append(events, Event{
				DeliveryID: "poll:comment:" + comment.ID,
				ActorID:    comment.User.ID,
				ActorName:  comment.User.Name,
				ActorEmail: comment.User.Email,
				IssueID:    issue.ID,
				Identifier: issue.Identifier,
				IssueURL:   issue.URL,
				Project:    issue.Project.Name,
				Action:     "comment",
				Labels:     labels,
				CreatedAt:  comment.CreatedAt,
				Source:     "linear_poll",
			})
		}
	}
	return events
}

func labelNames(labels []linearLabelRef) []string {
	names := make([]string, 0, len(labels))
	for _, label := range labels {
		if name := strings.TrimSpace(label.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

type pollResponse struct {
	Issues struct {
		Nodes    []pollIssue `json:"nodes"`
		PageInfo pageInfo    `json:"pageInfo"`
	} `json:"issues"`
}

type pollIssue struct {
	ID         string        `json:"id"`
	Identifier string        `json:"identifier"`
	URL        string        `json:"url"`
	UpdatedAt  time.Time     `json:"updatedAt"`
	Project    linearProject `json:"project"`
	Assignee   linearActor   `json:"assignee"`
	Labels     struct {
		Nodes []linearLabelRef `json:"nodes"`
	} `json:"labels"`
	Comments struct {
		Nodes []pollComment `json:"nodes"`
	} `json:"comments"`
}

type pollComment struct {
	ID        string      `json:"id"`
	CreatedAt time.Time   `json:"createdAt"`
	User      linearActor `json:"user"`
}

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

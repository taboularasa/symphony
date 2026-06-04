package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

type collectCLIOptions struct {
	runID          string
	since          string
	until          string
	linearEndpoint string
	linearTokenEnv string
	linearParent   string
	linearChild    string
	slackEndpoint  string
	slackTokenEnv  string
	slackChannel   string
	githubAPIBase  string
	githubTokenEnv string
	githubPR       string
	githubLinearID string
}

type CollectConfig struct {
	Scenario string
	RunID    string
	Since    time.Time
	Until    time.Time
	Client   *http.Client
	Linear   LinearCollectConfig
	Slack    SlackCollectConfig
	GitHub   GitHubCollectConfig
}

type LinearCollectConfig struct {
	Endpoint string
	Token    string
	ParentID string
	ChildID  string
}

type SlackCollectConfig struct {
	Endpoint string
	Token    string
	Channel  string
}

type GitHubCollectConfig struct {
	APIBase  string
	Token    string
	PRURL    string
	LinearID string
}

func buildCollectConfig(opts collectCLIOptions) (CollectConfig, error) {
	since, err := parseOptionalRFC3339(opts.since)
	if err != nil {
		return CollectConfig{}, fmt.Errorf("since: %w", err)
	}
	until, err := parseOptionalRFC3339(opts.until)
	if err != nil {
		return CollectConfig{}, fmt.Errorf("until: %w", err)
	}
	if !since.IsZero() && !until.IsZero() && until.Before(since) {
		return CollectConfig{}, fmt.Errorf("until is before since")
	}
	cfg := CollectConfig{
		Scenario: scenarioHandoff001,
		RunID:    strings.TrimSpace(opts.runID),
		Since:    since,
		Until:    until,
	}
	if strings.TrimSpace(opts.linearTokenEnv) != "" || strings.TrimSpace(opts.linearParent) != "" || strings.TrimSpace(opts.linearChild) != "" {
		token, err := requiredTokenFromEnv(opts.linearTokenEnv, "linear-token-env")
		if err != nil {
			return CollectConfig{}, err
		}
		if strings.TrimSpace(opts.linearParent) == "" || strings.TrimSpace(opts.linearChild) == "" {
			return CollectConfig{}, fmt.Errorf("linear collection requires --linear-parent and --linear-child")
		}
		cfg.Linear = LinearCollectConfig{
			Endpoint: strings.TrimSpace(opts.linearEndpoint),
			Token:    token,
			ParentID: strings.TrimSpace(opts.linearParent),
			ChildID:  strings.TrimSpace(opts.linearChild),
		}
	}
	if strings.TrimSpace(opts.slackTokenEnv) != "" || strings.TrimSpace(opts.slackChannel) != "" {
		token, err := requiredTokenFromEnv(opts.slackTokenEnv, "slack-token-env")
		if err != nil {
			return CollectConfig{}, err
		}
		if strings.TrimSpace(opts.slackChannel) == "" {
			return CollectConfig{}, fmt.Errorf("slack collection requires --slack-channel")
		}
		cfg.Slack = SlackCollectConfig{
			Endpoint: strings.TrimSpace(opts.slackEndpoint),
			Token:    token,
			Channel:  strings.TrimSpace(opts.slackChannel),
		}
	}
	if strings.TrimSpace(opts.githubPR) != "" || strings.TrimSpace(opts.githubTokenEnv) != "" {
		token := ""
		if strings.TrimSpace(opts.githubTokenEnv) != "" {
			var err error
			token, err = requiredTokenFromEnv(opts.githubTokenEnv, "github-token-env")
			if err != nil {
				return CollectConfig{}, err
			}
		}
		if strings.TrimSpace(opts.githubPR) == "" {
			return CollectConfig{}, fmt.Errorf("github collection requires --github-pr")
		}
		cfg.GitHub = GitHubCollectConfig{
			APIBase:  strings.TrimSpace(opts.githubAPIBase),
			Token:    token,
			PRURL:    strings.TrimSpace(opts.githubPR),
			LinearID: strings.TrimSpace(opts.githubLinearID),
		}
	}
	if !cfg.Linear.configured() && !cfg.Slack.configured() && !cfg.GitHub.configured() {
		return CollectConfig{}, fmt.Errorf("no collection source configured")
	}
	return cfg, nil
}

func parseOptionalRFC3339(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

func requiredTokenFromEnv(envName, flagName string) (string, error) {
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return "", fmt.Errorf("--%s is required for this collector", flagName)
	}
	token := strings.TrimSpace(os.Getenv(envName))
	if token == "" {
		return "", fmt.Errorf("%s is empty or unset", envName)
	}
	return token, nil
}

func (c LinearCollectConfig) configured() bool {
	return c.Endpoint != "" || c.Token != "" || c.ParentID != "" || c.ChildID != ""
}
func (c SlackCollectConfig) configured() bool {
	return c.Endpoint != "" || c.Token != "" || c.Channel != ""
}
func (c GitHubCollectConfig) configured() bool {
	return c.APIBase != "" || c.Token != "" || c.PRURL != "" || c.LinearID != ""
}

func CollectLiveArtifacts(ctx context.Context, cfg CollectConfig) (DrillRun, error) {
	if cfg.Scenario == "" {
		cfg.Scenario = scenarioHandoff001
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	run := DrillRun{Scenario: cfg.Scenario, RunID: cfg.RunID}
	if cfg.Linear.configured() {
		events, err := collectLinearEvents(ctx, client, cfg.Linear)
		if err != nil {
			return DrillRun{}, err
		}
		run.Events = append(run.Events, filterEventsByWindow(events, cfg.Since, cfg.Until)...)
	}
	if cfg.Slack.configured() {
		events, err := collectSlackEvents(ctx, client, cfg.Slack, cfg.Since, cfg.Until)
		if err != nil {
			return DrillRun{}, err
		}
		run.Events = append(run.Events, filterEventsByWindow(events, cfg.Since, cfg.Until)...)
	}
	if cfg.GitHub.configured() {
		events, err := collectGitHubEvents(ctx, client, cfg.GitHub)
		if err != nil {
			return DrillRun{}, err
		}
		run.Events = append(run.Events, filterEventsByWindow(events, cfg.Since, cfg.Until)...)
	}
	return run, nil
}

func filterEventsByWindow(events []Event, since, until time.Time) []Event {
	if since.IsZero() && until.IsZero() {
		return events
	}
	out := make([]Event, 0, len(events))
	for _, event := range events {
		ts, err := time.Parse(time.RFC3339, event.TS)
		if err != nil {
			out = append(out, event)
			continue
		}
		if !since.IsZero() && ts.Before(since) {
			continue
		}
		if !until.IsZero() && ts.After(until) {
			continue
		}
		out = append(out, event)
	}
	return out
}

func collectLinearEvents(ctx context.Context, client *http.Client, cfg LinearCollectConfig) ([]Event, error) {
	var out struct {
		Parent linearIssue `json:"parent"`
		Child  linearIssue `json:"child"`
	}
	if err := doGraphQL(ctx, client, cfg.Endpoint, cfg.Token, linearDrillQuery, map[string]any{
		"parent": cfg.ParentID,
		"child":  cfg.ChildID,
	}, &out); err != nil {
		return nil, fmt.Errorf("collect linear: %w", err)
	}
	var events []Event
	for _, issue := range []linearIssue{out.Parent, out.Child} {
		if issue.Identifier == "" {
			continue
		}
		events = append(events, issue.snapshotEvent())
		for _, comment := range issue.Comments.Nodes {
			commentEvents, err := extractDrillEventsFromText(comment.Body, comment.CreatedAt, "linear", comment.User.Name)
			if err != nil {
				return nil, fmt.Errorf("parse linear drill comment on %s: %w", issue.Identifier, err)
			}
			events = append(events, commentEvents...)
		}
	}
	return events, nil
}

type linearIssue struct {
	Identifier string `json:"identifier"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	State      struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"state"`
	Parent *struct {
		Identifier string `json:"identifier"`
	} `json:"parent"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Comments struct {
		Nodes []struct {
			CreatedAt string `json:"createdAt"`
			Body      string `json:"body"`
			User      struct {
				Name string `json:"name"`
			} `json:"user"`
		} `json:"nodes"`
	} `json:"comments"`
}

func (i linearIssue) snapshotEvent() Event {
	event := Event{
		TS:         i.UpdatedAt,
		Source:     "linear",
		Kind:       "issue_snapshot",
		LinearID:   i.Identifier,
		OwnerLabel: firstOwnerLabel(i.labelNames()),
		Outcome:    i.State.Type,
	}
	if event.TS == "" {
		event.TS = i.CreatedAt
	}
	if i.Parent != nil {
		event.ParentLinearID = i.Parent.Identifier
	}
	return event
}

func (i linearIssue) labelNames() []string {
	names := make([]string, 0, len(i.Labels.Nodes))
	for _, label := range i.Labels.Nodes {
		names = append(names, label.Name)
	}
	return names
}

func firstOwnerLabel(labels []string) string {
	for _, label := range labels {
		if strings.HasPrefix(label, "owner:") {
			return label
		}
	}
	return ""
}

const linearDrillQuery = `
query HandoffDrillIssues($parent: String!, $child: String!) {
  parent: issue(id: $parent) {
    ...DrillIssueFields
  }
  child: issue(id: $child) {
    ...DrillIssueFields
  }
}

fragment DrillIssueFields on Issue {
  identifier
  createdAt
  updatedAt
  state { name type }
  parent { identifier }
  labels { nodes { name } }
  comments(first: 100) {
    nodes {
      createdAt
      body
      user { name }
    }
  }
}`

func doGraphQL(ctx context.Context, client *http.Client, endpoint, token, query string, variables map[string]any, out any) error {
	if strings.TrimSpace(endpoint) == "" {
		return fmt.Errorf("linear endpoint is required")
	}
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return err
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("graphql errors: %s", joinGraphQLErrors(envelope.Errors))
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("missing graphql data")
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return err
	}
	return nil
}

func joinGraphQLErrors(errors []struct {
	Message string `json:"message"`
}) string {
	parts := make([]string, 0, len(errors))
	for _, item := range errors {
		parts = append(parts, item.Message)
	}
	return strings.Join(parts, "; ")
}

func extractDrillEventsFromText(body, fallbackTS, fallbackSource, fallbackActor string) ([]Event, error) {
	var events []Event
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		switch {
		case strings.HasPrefix(line, "symphony-drill:event "):
			event, err := decodeOneEvent(strings.TrimSpace(strings.TrimPrefix(line, "symphony-drill:event ")))
			if err != nil {
				return nil, err
			}
			events = append(events, withEventDefaults(event, fallbackTS, fallbackSource, fallbackActor))
		case strings.HasPrefix(line, "```symphony-drill:event"):
			payload, next, err := collectFence(lines, i+1)
			if err != nil {
				return nil, err
			}
			i = next
			event, err := decodeOneEvent(payload)
			if err != nil {
				return nil, err
			}
			events = append(events, withEventDefaults(event, fallbackTS, fallbackSource, fallbackActor))
		case strings.HasPrefix(line, "```symphony-drill:events"):
			payload, next, err := collectFence(lines, i+1)
			if err != nil {
				return nil, err
			}
			i = next
			decoded, err := decodeManyEvents(payload)
			if err != nil {
				return nil, err
			}
			for _, event := range decoded {
				events = append(events, withEventDefaults(event, fallbackTS, fallbackSource, fallbackActor))
			}
		}
	}
	return events, nil
}

func collectFence(lines []string, start int) (string, int, error) {
	var body []string
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "```" {
			return strings.Join(body, "\n"), i, nil
		}
		body = append(body, lines[i])
	}
	return "", len(lines), fmt.Errorf("unterminated symphony-drill fence")
}

func decodeOneEvent(payload string) (Event, error) {
	var event Event
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return Event{}, err
	}
	return event, nil
}

func decodeManyEvents(payload string) ([]Event, error) {
	var events []Event
	if err := json.Unmarshal([]byte(payload), &events); err != nil {
		return nil, err
	}
	return events, nil
}

func withEventDefaults(event Event, fallbackTS, fallbackSource, fallbackActor string) Event {
	if event.TS == "" {
		event.TS = fallbackTS
	}
	if event.Source == "" {
		event.Source = fallbackSource
	}
	if event.Actor == "" {
		event.Actor = fallbackActor
	}
	return event
}

func collectSlackEvents(ctx context.Context, client *http.Client, cfg SlackCollectConfig, since, until time.Time) ([]Event, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("slack endpoint is required")
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("channel", cfg.Channel)
	query.Set("limit", "200")
	if !since.IsZero() {
		query.Set("oldest", slackTimestamp(since))
	}
	if !until.IsZero() {
		query.Set("latest", slackTimestamp(until))
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("slack http %d", resp.StatusCode)
	}
	var out struct {
		OK       bool           `json:"ok"`
		Error    string         `json:"error"`
		Messages []slackMessage `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.OK {
		if out.Error == "" {
			out.Error = "unknown_error"
		}
		return nil, fmt.Errorf("slack conversations.history failed: %s", out.Error)
	}
	var events []Event
	for _, msg := range out.Messages {
		if msg.Metadata.EventType != "agents_bridge_v1" {
			continue
		}
		ts, err := slackTimeToRFC3339(msg.TS)
		if err != nil {
			return nil, fmt.Errorf("slack ts %q: %w", msg.TS, err)
		}
		payload := msg.Metadata.EventPayload
		if payload.V != 1 || payload.From == "" || payload.Kind == "" || payload.LinearID == "" {
			continue
		}
		events = append(events, Event{
			TS:                ts,
			Source:            "slack",
			Kind:              payload.Kind,
			Actor:             payload.From,
			LinearID:          payload.LinearID,
			Channel:           cfg.Channel,
			MetadataEventType: msg.Metadata.EventType,
			GitHubPR:          payload.GitHubPR,
		})
	}
	return events, nil
}

type slackMessage struct {
	TS       string `json:"ts"`
	Metadata struct {
		EventType    string `json:"event_type"`
		EventPayload struct {
			V        int    `json:"v"`
			From     string `json:"from"`
			Kind     string `json:"kind"`
			LinearID string `json:"linear_id"`
			GitHubPR string `json:"github_pr"`
		} `json:"event_payload"`
	} `json:"metadata"`
}

func slackTimestamp(t time.Time) string {
	return strconv.FormatInt(t.Unix(), 10)
}

func slackTimeToRFC3339(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("empty timestamp")
	}
	parts := strings.SplitN(value, ".", 2)
	seconds, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return "", err
	}
	nanos := int64(0)
	if len(parts) == 2 {
		frac := parts[1]
		if len(frac) > 9 {
			frac = frac[:9]
		}
		for len(frac) < 9 {
			frac += "0"
		}
		nanos, err = strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return "", err
		}
	}
	return time.Unix(seconds, nanos).UTC().Format(time.RFC3339), nil
}

func collectGitHubEvents(ctx context.Context, client *http.Client, cfg GitHubCollectConfig) ([]Event, error) {
	owner, repo, number, err := parseGitHubPRURL(cfg.PRURL)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(cfg.APIBase, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	u := fmt.Sprintf("%s/repos/%s/%s/pulls/%s", base, url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(number))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if strings.TrimSpace(cfg.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("github http %d", resp.StatusCode)
	}
	var out struct {
		HTMLURL   string  `json:"html_url"`
		CreatedAt string  `json:"created_at"`
		MergedAt  *string `json:"merged_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		MergedBy *struct {
			Login string `json:"login"`
		} `json:"merged_by"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.CreatedAt == "" {
		return nil, fmt.Errorf("github PR response missing created_at")
	}
	if out.HTMLURL == "" {
		out.HTMLURL = cfg.PRURL
	}
	events := []Event{{
		TS:       out.CreatedAt,
		Source:   "github",
		Kind:     "pr_opened",
		Actor:    out.User.Login,
		LinearID: cfg.LinearID,
		GitHubPR: out.HTMLURL,
	}}
	if out.MergedAt != nil && *out.MergedAt != "" {
		actor := ""
		if out.MergedBy != nil {
			actor = out.MergedBy.Login
		}
		events = append(events, Event{
			TS:       *out.MergedAt,
			Source:   "github",
			Kind:     "pr_merged",
			Actor:    actor,
			LinearID: cfg.LinearID,
			GitHubPR: out.HTMLURL,
		})
	}
	return events, nil
}

func parseGitHubPRURL(raw string) (string, string, string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", "", err
	}
	if parsed.Scheme != "https" || parsed.Host != "github.com" {
		return "", "", "", fmt.Errorf("github PR URL must use https://github.com")
	}
	parts := strings.Split(strings.Trim(path.Clean(parsed.Path), "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" {
		return "", "", "", fmt.Errorf("github PR URL must look like https://github.com/owner/repo/pull/123")
	}
	if _, err := strconv.Atoi(parts[3]); err != nil {
		return "", "", "", fmt.Errorf("invalid PR number %q", parts[3])
	}
	return parts[0], parts[1], parts[3], nil
}

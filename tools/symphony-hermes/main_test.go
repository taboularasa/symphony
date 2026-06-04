package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taboularasa/symphony/internal/linear"
	"github.com/taboularasa/symphony/internal/workflow"
)

func TestRunRequiresHermesTokenWithoutFallback(t *testing.T) {
	t.Setenv("HERMES_LINEAR_TOKEN", "")
	t.Setenv("LINEAR_API_KEY", "human-token")

	err := run([]string{
		"--once",
		"--workflow", filepath.Join("..", "..", "hermes", "WORKFLOW.md"),
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected missing Hermes token error")
	}
	if !strings.Contains(err.Error(), "HERMES_LINEAR_TOKEN is not set") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunDryRunPollsCandidatesWithFallbackToken(t *testing.T) {
	t.Setenv("HERMES_LINEAR_TOKEN", "")
	t.Setenv("LINEAR_API_KEY", "human-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "human-token" {
			t.Fatalf("authorization = %q", got)
		}
		var req graphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch {
		case strings.Contains(req.Query, "SymphonyLinearResolveUserByLookup"):
			_, _ = w.Write([]byte(`{"data":{"users":{"nodes":[{"id":"user-hermes","name":"Hermes","displayName":"hermes","email":"hermes@example.test","active":true}]}}}`))
		case strings.Contains(req.Query, "SymphonyLinearCandidateIssues"):
			variables := req.Variables.(map[string]any)
			if variables["projectSlug"] != "override-project" {
				t.Fatalf("projectSlug = %v", variables["projectSlug"])
			}
			_, _ = w.Write([]byte(`{"data":{"issues":{"nodes":[{"id":"issue-1","identifier":"HAD-1","url":"https://linear.app/hadto/issue/HAD-1","project":{"id":"project-1","name":"Symphony","slugId":"override-project"},"state":{"name":"Todo","type":"unstarted"},"assignee":null,"labels":{"nodes":[{"id":"label-1","name":"owner:hermes"}]}}],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`))
		default:
			t.Fatalf("unexpected query: %s", req.Query)
		}
	}))
	defer server.Close()

	workflowPath := writeWorkflow(t, server.URL)
	var out bytes.Buffer
	err := run([]string{
		"--once",
		"--dry-run",
		"--allow-token-fallback",
		"--workflow", workflowPath,
		"--project-slug", "override-project",
	}, &out)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	events := decodeEvents(t, out.Bytes())
	if got := events[0]["event"]; got != "linear_config" {
		t.Fatalf("first event = %v", got)
	}
	if events[0]["token_fallback"] != true || events[0]["missing_token_env"] != "HERMES_LINEAR_TOKEN" {
		t.Fatalf("token fallback fields = %#v", events[0])
	}
	if got := eventByName(t, events, "candidate_poll")["candidate_count"]; got != float64(1) {
		t.Fatalf("candidate_count = %v", got)
	}
	decision := eventByName(t, events, "candidate_decision")
	if decision["linear_id"] != "HAD-1" || decision["code"] != "dry_run_would_claim" {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestRunCheckHookEmitsHookResult(t *testing.T) {
	t.Setenv("HERMES_LINEAR_TOKEN", "hermes-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req graphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch {
		case strings.Contains(req.Query, "SymphonyLinearResolveUserByLookup"):
			_, _ = w.Write([]byte(`{"data":{"users":{"nodes":[{"id":"user-hermes","name":"Hermes","displayName":"hermes","email":"hermes@example.test","active":true}]}}}`))
		case strings.Contains(req.Query, "SymphonyLinearCandidateIssues"):
			_, _ = w.Write([]byte(`{"data":{"issues":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`))
		default:
			t.Fatalf("unexpected query: %s", req.Query)
		}
	}))
	defer server.Close()

	workflowPath := writeWorkflow(t, server.URL)
	workspace := initAllowedWorkspace(t)
	var out bytes.Buffer
	err := run([]string{
		"--once",
		"--workflow", workflowPath,
		"--workspace", workspace,
		"--check-hook",
	}, &out)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	events := decodeEvents(t, out.Bytes())
	hook := eventByName(t, events, "hook_result")
	if hook["reason"] != "startup_check" || hook["success"] != true {
		t.Fatalf("hook event = %#v", hook)
	}
}

func TestDryRunDecisionWouldClaimHumanAssignedIssueInDelegateMode(t *testing.T) {
	r := runner{linear: workflowLinearConfigForTest()}
	code, dispatchable := r.dryRunDecision(linearCandidateForTest("HAD-2", &issueUserForTest{
		ID:   "user-human",
		Name: "David",
	}))
	if code != "dry_run_would_claim" || dispatchable {
		t.Fatalf("dry-run decision = %s dispatchable=%v, want delegate claim preview", code, dispatchable)
	}
}

func TestDryRunDecisionAllowsExistingDelegateClaim(t *testing.T) {
	r := runner{linear: workflowLinearConfigForTest()}
	candidate := linearCandidateForTest("HAD-3", &issueUserForTest{ID: "user-human", Name: "David"})
	candidate.Delegate = &linear.IssueUser{ID: "user-hermes", Name: "Hermes"}
	code, dispatchable := r.dryRunDecision(candidate)
	if code != "dry_run_would_dispatch" || !dispatchable {
		t.Fatalf("dry-run decision = %s dispatchable=%v, want dispatch preview", code, dispatchable)
	}
}

func TestFilterCandidatesForIssueMatchesIdentifierIDAndURL(t *testing.T) {
	candidates := []linear.CandidateIssue{
		{
			ID:         "issue-uuid-1",
			Identifier: "HAD-1",
			URL:        "https://linear.app/hadto/issue/HAD-1/first",
		},
		{
			ID:         "issue-uuid-2",
			Identifier: "HAD-2",
			URL:        "https://linear.app/hadto/issue/HAD-2/second",
		},
	}
	for _, tt := range []struct {
		name        string
		issueFilter string
		want        []string
	}{
		{name: "empty", issueFilter: "", want: []string{"HAD-1", "HAD-2"}},
		{name: "identifier", issueFilter: "had-2", want: []string{"HAD-2"}},
		{name: "id", issueFilter: "issue-uuid-1", want: []string{"HAD-1"}},
		{name: "url", issueFilter: "https://linear.app/hadto/issue/HAD-2/second", want: []string{"HAD-2"}},
		{name: "missing", issueFilter: "HAD-3", want: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := filterCandidatesForIssue(candidates, tt.issueFilter)
			if identifiers(got) != strings.Join(tt.want, ",") {
				t.Fatalf("filtered identifiers = %q, want %v", identifiers(got), tt.want)
			}
		})
	}
}

type graphQLRequest struct {
	Query     string `json:"query"`
	Variables any    `json:"variables"`
}

func writeWorkflow(t *testing.T, endpoint string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "WORKFLOW.md")
	body := `---
tracker:
  kind: linear
  endpoint: ` + endpoint + `
  api_key: "$HERMES_LINEAR_TOKEN"
  project_slug: 6a6a965c3d10
  owner_label: "owner:hermes"
  claim_assignee: "hermes"
  claim_target: "delegate"
  require_claim_before_dispatch: true
  active_states: ["Todo", "In Progress"]
  terminal_states: ["Done", "Canceled", "Cancelled", "Duplicate"]
hooks:
  timeout_ms: 60000
  before_run: |
    set -euo pipefail
    origin="$(git remote get-url origin 2>/dev/null || true)"
    case "$origin" in
      *taboularasa/hermes-agent*|*taboularasa/phoneitin*) ;;
      *) echo "unexpected repository origin: $origin" >&2; exit 1 ;;
    esac
---
# Hermes Execution Manager
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	return path
}

func initAllowedWorkspace(t *testing.T) string {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "hermes-agent")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	runGit(t, workspace, "init")
	runGit(t, workspace, "remote", "add", "origin", "https://github.com/taboularasa/hermes-agent.git")
	return workspace
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func decodeEvents(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	var events []map[string]any
	for {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode event: %v\n%s", err, string(data))
		}
		events = append(events, event)
	}
	return events
}

func eventByName(t *testing.T, events []map[string]any, name string) map[string]any {
	t.Helper()
	for _, event := range events {
		if event["event"] == name {
			return event
		}
	}
	t.Fatalf("event %q not found in %#v", name, events)
	return nil
}

type issueUserForTest struct {
	ID   string
	Name string
}

func workflowLinearConfigForTest() workflow.LinearConfig {
	return workflow.LinearConfig{
		ProjectSlug:                "6a6a965c3d10",
		OwnerLabel:                 "owner:hermes",
		ClaimAssigneeID:            "user-hermes",
		ClaimTarget:                "delegate",
		RequireClaimBeforeDispatch: true,
		ActiveStates:               []string{"Todo", "In Progress"},
		TerminalStates:             []string{"Done", "Canceled"},
	}
}

func linearCandidateForTest(identifier string, assignee *issueUserForTest) linear.CandidateIssue {
	var linearAssignee *linear.IssueUser
	if assignee != nil {
		linearAssignee = &linear.IssueUser{ID: assignee.ID, Name: assignee.Name}
	}
	return linear.CandidateIssue{
		ID:         "issue-" + strings.ToLower(identifier),
		Identifier: identifier,
		Project:    linear.IssueProject{Name: "Symphony", SlugID: "6a6a965c3d10"},
		State:      linear.IssueState{Name: "Todo"},
		Assignee:   linearAssignee,
		Labels:     []linear.IssueLabel{{Name: "owner:hermes"}},
	}
}

func identifiers(candidates []linear.CandidateIssue) string {
	values := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		values = append(values, candidate.Identifier)
	}
	return strings.Join(values, ",")
}

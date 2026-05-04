package workflow

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHermesWorkflowFileContract(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("HERMES_LINEAR_TOKEN", "test-hermes-token")

	def, err := Load(filepath.Join("..", "..", "hermes", "WORKFLOW.md"))
	if err != nil {
		t.Fatalf("load Hermes workflow: %v", err)
	}
	tracker := def.Settings.Tracker
	if err := tracker.ValidateOwnerClaimContract("owner:hermes", "hermes-bot", true); err != nil {
		t.Fatalf("Hermes owner/claim contract: %v", err)
	}

	config, err := tracker.ResolveLinearConfig(context.Background(), ClaimAssigneeResolverFunc(func(ctx context.Context, ref string) (ClaimAssigneeIdentity, error) {
		if ref != "hermes-bot" {
			t.Fatalf("claim ref = %q, want hermes-bot", ref)
		}
		return ClaimAssigneeIdentity{ID: "linear-user-hermes", Active: true}, nil
	}))
	if err != nil {
		t.Fatalf("resolve Hermes Linear config: %v", err)
	}
	if config.APIKey != "test-hermes-token" {
		t.Fatalf("api key was not resolved through HERMES_LINEAR_TOKEN")
	}
	if config.ProjectSlug != "shared-agents" {
		t.Fatalf("project slug = %q, want shared-agents", config.ProjectSlug)
	}
	if got, want := strings.Join(config.ActiveStates, ","), "Todo,In Progress"; got != want {
		t.Fatalf("active states = %q, want %q", got, want)
	}
	if got, want := strings.Join(config.TerminalStates, ","), "Done,Canceled,Cancelled,Duplicate"; got != want {
		t.Fatalf("terminal states = %q, want %q", got, want)
	}

	agent := requireMap(t, def.Config, "agent")
	if got := requireInt(t, agent, "max_concurrent_agents"); got != 3 {
		t.Fatalf("max_concurrent_agents = %d, want 3", got)
	}
	byState := requireMap(t, agent, "max_concurrent_agents_by_state")
	if got := requireInt(t, byState, "in progress"); got != 2 {
		t.Fatalf("in-progress concurrency = %d, want 2", got)
	}

	codex := requireMap(t, def.Config, "codex")
	if got := requireString(t, codex, "approval_policy"); got != "on-failure" {
		t.Fatalf("codex approval_policy = %q, want on-failure", got)
	}
	if got := requireString(t, codex, "thread_sandbox"); got != "workspace-write" {
		t.Fatalf("codex thread_sandbox = %q, want workspace-write", got)
	}

	workspace := requireMap(t, def.Config, "workspace")
	if got := requireString(t, workspace, "root"); got != "/home/david/stacks" {
		t.Fatalf("workspace root = %q, want /home/david/stacks", got)
	}

	hooks := requireMap(t, def.Config, "hooks")
	if got := requireInt(t, hooks, "timeout_ms"); got != 60000 {
		t.Fatalf("hook timeout = %d, want 60000", got)
	}
	if got := def.Settings.Hooks.TimeoutDuration(); got != 60*time.Second {
		t.Fatalf("typed hook timeout = %s, want 60s", got)
	}
	beforeRun := requireString(t, hooks, "before_run")
	for _, required := range []string{"ctx", "taboularasa/hermes-agent", "taboularasa/phoneitin", "nested Hermes worktree"} {
		if !strings.Contains(beforeRun, required) {
			t.Fatalf("before_run hook missing %q:\n%s", required, beforeRun)
		}
	}

	for _, required := range []string{
		"Hermes Execution Manager",
		"owner:hermes",
		"hermes-bot",
		"ctx-managed worktree",
		"Do not create a nested Hermes worktree",
		"native Slack Socket Mode",
	} {
		if !strings.Contains(def.Prompt, required) {
			t.Fatalf("workflow prompt missing %q", required)
		}
	}
	for _, forbidden := range []string{"xoxb-", "xapp-", "sk-", "HERMES_LINEAR_TOKEN="} {
		if strings.Contains(def.Prompt, forbidden) {
			t.Fatalf("workflow prompt contains forbidden secret-looking text %q", forbidden)
		}
	}
}

func TestHermesWorkflowPromptGuardrailSnapshot(t *testing.T) {
	def, err := Load(filepath.Join("..", "..", "hermes", "WORKFLOW.md"))
	if err != nil {
		t.Fatalf("load Hermes workflow: %v", err)
	}

	requiredSections := []string{
		"# Hermes Execution Manager",
		"## Operating Contract",
		"## Engineering-Manager Behavior",
		"## Workspace And ctx Contract",
		"## Runtime Boundaries",
		"## Verification And Handoff",
	}
	for _, section := range requiredSections {
		if !strings.Contains(def.Prompt, section) {
			t.Fatalf("workflow prompt missing section %q", section)
		}
	}

	requiredGuardrails := map[string]string{
		"owner label only":         "positive owner-label rule, not a project-name denial list.",
		"claim gate":               "do not launch implementation work unless the claim result is",
		"delegation uniqueness":    "spawning duplicate work for the same issue",
		"ctx source of truth":      "Use the ctx-managed worktree as the source of truth",
		"native Slack boundary":    "Hermes Slack on this host stays in native Slack Socket Mode",
		"durable evidence":         "Prefer durable artifacts over status narration",
		"no secret copying":        "Never copy raw tokens, API keys, private logs, or secret-bearing config values",
		"unavailable checks block": "If a check cannot run, leave the",
	}
	for name, fragment := range requiredGuardrails {
		if !strings.Contains(def.Prompt, fragment) {
			t.Fatalf("workflow prompt missing %s guardrail %q", name, fragment)
		}
	}

	for _, legacy := range []string{
		"Never auto-delegate or auto-assign De Novo project issues",
		"issue belongs to project `De Novo`",
		"de_novo_block",
		"DENOVO_LINEAR_TOKEN",
	} {
		if strings.Contains(def.Prompt, legacy) {
			t.Fatalf("workflow prompt kept legacy project-name ownership rule %q", legacy)
		}
	}
}

func TestHermesWorkflowRejectsMissingOwnerClaimContract(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing owner label",
			body: `---
tracker:
  kind: linear
  api_key: "$HERMES_LINEAR_TOKEN"
  project_slug: shared-agents
  claim_assignee: hermes-bot
  require_claim_before_dispatch: true
---
Prompt
`,
			want: `tracker.owner_label must be "owner:hermes"`,
		},
		{
			name: "missing claim assignee",
			body: `---
tracker:
  kind: linear
  api_key: "$HERMES_LINEAR_TOKEN"
  project_slug: shared-agents
  owner_label: owner:hermes
  require_claim_before_dispatch: false
---
Prompt
`,
			want: `tracker.claim_assignee must be "hermes-bot"`,
		},
		{
			name: "claim gate disabled",
			body: `---
tracker:
  kind: linear
  api_key: "$HERMES_LINEAR_TOKEN"
  project_slug: shared-agents
  owner_label: owner:hermes
  claim_assignee: hermes-bot
  require_claim_before_dispatch: false
---
Prompt
`,
			want: "tracker.require_claim_before_dispatch must be true",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, err := Parse([]byte(tt.body))
			if err != nil {
				t.Fatalf("parse malformed Hermes fixture: %v", err)
			}
			err = def.Settings.Tracker.ValidateOwnerClaimContract("owner:hermes", "hermes-bot", true)
			if err == nil {
				t.Fatal("expected contract validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func requireMap(t *testing.T, values map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := values[key]
	if !ok {
		t.Fatalf("missing config map %q", key)
	}
	mapped, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("config %q has type %T, want map[string]any", key, value)
	}
	return mapped
}

func requireString(t *testing.T, values map[string]any, key string) string {
	t.Helper()
	value, ok := values[key]
	if !ok {
		t.Fatalf("missing config string %q", key)
	}
	text, ok := value.(string)
	if !ok {
		t.Fatalf("config %q has type %T, want string", key, value)
	}
	return text
}

func requireInt(t *testing.T, values map[string]any, key string) int {
	t.Helper()
	value, ok := values[key]
	if !ok {
		t.Fatalf("missing config int %q", key)
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		t.Fatalf("config %q has type %T, want int", key, value)
	}
	return 0
}

func ExampleTrackerConfig_ValidateOwnerClaimContract() {
	settings, _ := DecodeSettings([]byte(`tracker:
  owner_label: owner:hermes
  claim_assignee: hermes-bot
  require_claim_before_dispatch: true
`))
	err := settings.Tracker.ValidateOwnerClaimContract("owner:hermes", "hermes-bot", true)
	fmt.Println(err == nil)
	// Output: true
}

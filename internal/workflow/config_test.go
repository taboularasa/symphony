package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseWorkflowDefaultsLegacyFields(t *testing.T) {
	def, err := Parse([]byte("Prompt only\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if def.Prompt != "Prompt only" || def.PromptTemplate != "Prompt only" {
		t.Fatalf("prompt = %q template=%q", def.Prompt, def.PromptTemplate)
	}
	tracker := def.Settings.Tracker
	if tracker.Endpoint != DefaultLinearEndpoint {
		t.Fatalf("endpoint = %q", tracker.Endpoint)
	}
	if got, want := strings.Join(tracker.ActiveStates, ","), "Todo,In Progress"; got != want {
		t.Fatalf("active states = %q", got)
	}
	if tracker.OwnerLabel.Enabled() || tracker.ClaimAssignee.Enabled() || tracker.RequireClaimBeforeDispatch {
		t.Fatalf("legacy tracker unexpectedly enabled owner/claim fields: %+v", tracker)
	}
}

func TestParseWorkflowNormalizesOwnerAndClaim(t *testing.T) {
	def, err := Parse([]byte(`---
tracker:
  kind: linear
  api_key: "$HERMES_LINEAR_TOKEN"
  project_slug: shared-agents
  owner_label: " OWNER:Hermes "
  claim_assignee: " hermes-bot "
  claim_target: " delegate "
  require_claim_before_dispatch: true
---
Run Hermes work.
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tracker := def.Settings.Tracker
	if got, want := tracker.NormalizedOwnerLabel(), "owner:hermes"; got != want {
		t.Fatalf("owner label = %q, want %q", got, want)
	}
	if got, want := tracker.NormalizedClaimAssignee(), "hermes-bot"; got != want {
		t.Fatalf("claim assignee = %q, want %q", got, want)
	}
	if got, want := tracker.NormalizedClaimTarget(), ClaimTargetDelegate; got != want {
		t.Fatalf("claim target = %q, want %q", got, want)
	}
}

func TestResolveLinearConfigValidHermesAndDeNovoExamples(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "fallback-token")
	t.Setenv("HERMES_LINEAR_TOKEN", "hermes-token")

	tests := []struct {
		name       string
		yaml       string
		wantOwner  string
		wantClaim  string
		wantTarget string
		wantUserID string
		wantToken  string
	}{
		{
			name: "Hermes",
			yaml: `tracker:
  kind: Linear
  api_key: "$HERMES_LINEAR_TOKEN"
  project_slug: symphony
  owner_label: " OWNER:Hermes "
  claim_assignee: " hermes-bot "
  claim_target: delegate
  require_claim_before_dispatch: true
`,
			wantOwner:  "owner:hermes",
			wantClaim:  "hermes-bot",
			wantTarget: ClaimTargetDelegate,
			wantUserID: "user-hermes",
			wantToken:  "hermes-token",
		},
		{
			name: "De Novo",
			yaml: `tracker:
  api_key: "$MISSING_DENOVO_TOKEN"
  project_slug: symphony
  owner_label: "owner:DeNovo"
  claim_assignee: denovo-bot
  require_claim_before_dispatch: true
`,
			wantOwner:  "owner:denovo",
			wantClaim:  "denovo-bot",
			wantTarget: ClaimTargetAssignee,
			wantUserID: "user-denovo",
			wantToken:  "fallback-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings, err := DecodeSettings([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("decode settings: %v", err)
			}
			resolver := ClaimAssigneeResolverFunc(func(ctx context.Context, ref string) (ClaimAssigneeIdentity, error) {
				if ctx == nil {
					t.Fatal("context is nil")
				}
				if ref != tt.wantClaim {
					t.Fatalf("claim ref = %q, want %q", ref, tt.wantClaim)
				}
				return ClaimAssigneeIdentity{ID: tt.wantUserID, Active: true}, nil
			})
			config, err := settings.Tracker.ResolveLinearConfig(context.Background(), resolver)
			if err != nil {
				t.Fatalf("resolve linear config: %v", err)
			}
			if config.OwnerLabel != tt.wantOwner {
				t.Fatalf("owner label = %q, want %q", config.OwnerLabel, tt.wantOwner)
			}
			if config.ClaimAssignee != tt.wantClaim {
				t.Fatalf("claim assignee = %q, want %q", config.ClaimAssignee, tt.wantClaim)
			}
			if config.ClaimAssigneeID != tt.wantUserID {
				t.Fatalf("claim user id = %q, want %q", config.ClaimAssigneeID, tt.wantUserID)
			}
			if config.ClaimTarget != tt.wantTarget {
				t.Fatalf("claim target = %q, want %q", config.ClaimTarget, tt.wantTarget)
			}
			if config.APIKey != tt.wantToken {
				t.Fatalf("resolved api key = %q, want %q", config.APIKey, tt.wantToken)
			}
		})
	}
}

func TestParseWorkflowNullOwnerAndClaimDisableExtensions(t *testing.T) {
	def, err := Parse([]byte(`---
tracker:
  owner_label: null
  claim_assignee: null
  require_claim_before_dispatch: false
---
Prompt
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if def.Settings.Tracker.OwnerLabel.Enabled() || def.Settings.Tracker.ClaimAssignee.Enabled() {
		t.Fatalf("null owner/claim should be disabled: %+v", def.Settings.Tracker)
	}
}

func TestParseWorkflowRejectsBadOwnerLabels(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "blank",
			yaml: "owner_label: '   '\n",
			want: "tracker.owner_label must not be blank",
		},
		{
			name: "unknown",
			yaml: "owner_label: owner:nope\n",
			want: `tracker.owner_label: unknown owner label "owner:nope"`,
		},
		{
			name: "list",
			yaml: "owner_label: [owner:hermes]\n",
			want: "expected string or null",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte("---\ntracker:\n  " + tt.yaml + "---\nPrompt\n"))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestParseWorkflowRejectsBadClaimGate(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "required missing claim",
			yaml: "require_claim_before_dispatch: true\n",
			want: "tracker.require_claim_before_dispatch requires tracker.claim_assignee",
		},
		{
			name: "blank claim",
			yaml: "claim_assignee: '   '\n",
			want: "tracker.claim_assignee must not be blank",
		},
		{
			name: "claim list",
			yaml: "claim_assignee: [hermes-bot]\n",
			want: "expected string or null",
		},
		{
			name: "claim map",
			yaml: "claim_assignee: {name: hermes-bot}\n",
			want: "expected string or null",
		},
		{
			name: "bad claim target",
			yaml: "claim_target: comment\n",
			want: `tracker.claim_target must be "assignee" or "delegate"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte("---\ntracker:\n  " + tt.yaml + "---\nPrompt\n"))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestResolveLinearConfigRejectsMissingTokenAndProject(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")

	settings, err := DecodeSettings([]byte(`tracker:
  project_slug: symphony
`))
	if err != nil {
		t.Fatalf("decode missing token settings: %v", err)
	}
	_, err = settings.Tracker.ResolveLinearConfig(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "tracker.api_key is required") {
		t.Fatalf("missing token error = %v", err)
	}

	settings, err = DecodeSettings([]byte(`tracker:
  api_key: token
`))
	if err != nil {
		t.Fatalf("decode missing project settings: %v", err)
	}
	_, err = settings.Tracker.ResolveLinearConfig(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "tracker.project_slug is required") {
		t.Fatalf("missing project error = %v", err)
	}
}

func TestResolveLinearConfigRejectsUnresolvedClaimAssignee(t *testing.T) {
	settings, err := DecodeSettings([]byte(`tracker:
  api_key: token
  project_slug: symphony
  claim_assignee: hermes-bot
  require_claim_before_dispatch: true
`))
	if err != nil {
		t.Fatalf("decode settings: %v", err)
	}

	tests := []struct {
		name     string
		resolver ClaimAssigneeResolver
		want     string
	}{
		{
			name:     "missing resolver",
			resolver: nil,
			want:     "tracker.claim_assignee resolver is required",
		},
		{
			name: "resolver error",
			resolver: ClaimAssigneeResolverFunc(func(ctx context.Context, ref string) (ClaimAssigneeIdentity, error) {
				return ClaimAssigneeIdentity{}, errors.New("not found")
			}),
			want: `resolve tracker.claim_assignee "hermes-bot": not found`,
		},
		{
			name: "empty id",
			resolver: ClaimAssigneeResolverFunc(func(ctx context.Context, ref string) (ClaimAssigneeIdentity, error) {
				return ClaimAssigneeIdentity{Active: true}, nil
			}),
			want: `tracker.claim_assignee "hermes-bot" did not resolve to a Linear user`,
		},
		{
			name: "inactive user",
			resolver: ClaimAssigneeResolverFunc(func(ctx context.Context, ref string) (ClaimAssigneeIdentity, error) {
				return ClaimAssigneeIdentity{ID: "user-hermes", Active: false}, nil
			}),
			want: `tracker.claim_assignee "hermes-bot" resolved to an inactive Linear user`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := settings.Tracker.ResolveLinearConfig(context.Background(), tt.resolver)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestResolvedAPIKeyUsesEnvReferencesAndFallback(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "fallback")
	t.Setenv("HERMES_LINEAR_TOKEN", "bot-token")

	settings, err := DecodeSettings([]byte(`tracker:
  api_key: "$HERMES_LINEAR_TOKEN"
`))
	if err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if got, want := settings.Tracker.ResolvedAPIKey(), "bot-token"; got != want {
		t.Fatalf("resolved api key = %q, want %q", got, want)
	}

	settings, err = DecodeSettings([]byte(`tracker:
  api_key: "$MISSING_LINEAR_TOKEN"
`))
	if err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if got, want := settings.Tracker.ResolvedAPIKey(), "fallback"; got != want {
		t.Fatalf("fallback api key = %q, want %q", got, want)
	}
}

func TestParseWorkflowRejectsDuplicateKeys(t *testing.T) {
	_, err := Parse([]byte(`---
tracker:
  kind: linear
  kind: memory
---
Prompt
`))
	if err == nil {
		t.Fatal("expected duplicate key error")
	}
	if !strings.Contains(err.Error(), `duplicate workflow yaml key "kind"`) {
		t.Fatalf("error = %v", err)
	}
}

package githubbackstop

import "testing"

func TestEvaluateAllowsExpectedAppsAndDeniesCrossOwnerApps(t *testing.T) {
	policy := testPolicy(t)
	tests := []struct {
		name   string
		input  CheckInput
		status string
		reason string
	}{
		{
			name: "hermes app to hermes repo",
			input: CheckInput{
				Repository:     "taboularasa/hermes-agent",
				LinearIssueKey: "HAD-665",
				OwnerLabel:     "owner:hermes",
				Actor:          ActorIdentity{Login: "hermes-bot[bot]", Type: "Bot"},
			},
			status: DecisionAllow,
			reason: ReasonAllowedApp,
		},
		{
			name: "hermes app to denovo issue",
			input: CheckInput{
				Repository:     "taboularasa/de-novo",
				LinearIssueKey: "HAD-665",
				OwnerLabel:     "owner:denovo",
				Actor:          ActorIdentity{Login: "hermes-bot[bot]", Type: "Bot"},
			},
			status: DecisionDeny,
			reason: ReasonActorNotAllowed,
		},
		{
			name: "denovo app to denovo repo",
			input: CheckInput{
				Repository:     "taboularasa/de-novo",
				LinearIssueKey: "HAD-665",
				OwnerLabel:     "owner:denovo",
				Actor:          ActorIdentity{Login: "denovo-bot[bot]", Type: "Bot"},
			},
			status: DecisionAllow,
			reason: ReasonAllowedApp,
		},
		{
			name: "denovo app to hermes issue",
			input: CheckInput{
				Repository:     "taboularasa/hermes-agent",
				LinearIssueKey: "HAD-665",
				OwnerLabel:     "owner:hermes",
				Actor:          ActorIdentity{Login: "denovo-bot[bot]", Type: "Bot"},
			},
			status: DecisionDeny,
			reason: ReasonActorNotAllowed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := Evaluate(policy, tt.input)
			if decision.Status != tt.status || decision.ReasonCode != tt.reason {
				t.Fatalf("Evaluate() = %#v, want status=%s reason=%s", decision, tt.status, tt.reason)
			}
		})
	}
}

func TestEvaluateDeniesRepositoryAndOwnershipProblems(t *testing.T) {
	policy := testPolicy(t)
	tests := []struct {
		name   string
		input  CheckInput
		reason string
	}{
		{
			name: "bad repository",
			input: CheckInput{
				Repository:     "de-novo",
				LinearIssueKey: "HAD-665",
				OwnerLabel:     "owner:denovo",
				Actor:          ActorIdentity{Login: "denovo-bot[bot]", Type: "Bot"},
			},
			reason: ReasonInvalidRepository,
		},
		{
			name: "missing issue key",
			input: CheckInput{
				Repository: "taboularasa/de-novo",
				OwnerLabel: "owner:denovo",
				Actor:      ActorIdentity{Login: "denovo-bot[bot]", Type: "Bot"},
			},
			reason: ReasonLinearIssueMissing,
		},
		{
			name: "missing owner",
			input: CheckInput{
				Repository:     "taboularasa/de-novo",
				LinearIssueKey: "HAD-665",
				Actor:          ActorIdentity{Login: "denovo-bot[bot]", Type: "Bot"},
			},
			reason: ReasonOwnerLabelMissing,
		},
		{
			name: "owner not in policy",
			input: CheckInput{
				Repository:     "taboularasa/de-novo",
				LinearIssueKey: "HAD-665",
				OwnerLabel:     "owner:triage",
				Actor:          ActorIdentity{Login: "denovo-bot[bot]", Type: "Bot"},
			},
			reason: ReasonOwnerPolicyMissing,
		},
		{
			name: "repo not allowed for owner",
			input: CheckInput{
				Repository:     "taboularasa/phoneitin",
				LinearIssueKey: "HAD-665",
				OwnerLabel:     "owner:denovo",
				Actor:          ActorIdentity{Login: "denovo-bot[bot]", Type: "Bot"},
			},
			reason: ReasonRepositoryNotAllowed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := Evaluate(policy, tt.input)
			if decision.Status != DecisionDeny || decision.ReasonCode != tt.reason {
				t.Fatalf("Evaluate() = %#v, want deny reason=%s", decision, tt.reason)
			}
		})
	}
}

func TestEvaluateHumanBypass(t *testing.T) {
	policy := testPolicy(t)
	allowed := Evaluate(policy, CheckInput{
		Repository:     "taboularasa/de-novo",
		LinearIssueKey: "HAD-665",
		OwnerLabel:     "owner:denovo",
		Actor:          ActorIdentity{Login: "taboularasa", Type: "User", RepoAdmin: true},
	})
	if allowed.Status != DecisionAllow || allowed.ReasonCode != ReasonHumanBypass {
		t.Fatalf("Evaluate(admin) = %#v, want human bypass allow", allowed)
	}

	denied := Evaluate(policy, CheckInput{
		Repository:     "taboularasa/de-novo",
		LinearIssueKey: "HAD-665",
		OwnerLabel:     "owner:denovo",
		Actor:          ActorIdentity{Login: "someone", Type: "User"},
	})
	if denied.Status != DecisionDeny || denied.ReasonCode != ReasonActorNotAllowed {
		t.Fatalf("Evaluate(non-admin) = %#v, want actor_not_allowed", denied)
	}
}

func TestEvaluateDeniesStaleAppID(t *testing.T) {
	policy := Policy{
		SchemaVersion: PolicySchemaVersion,
		Owners: []OwnerPolicy{
			{
				OwnerLabel:          "owner:denovo",
				AllowedRepositories: []string{"taboularasa/de-novo"},
				ExpectedApps: []AppIdentity{
					{Slug: "denovo-bot", Login: "denovo-bot[bot]", AppID: "123"},
				},
			},
		},
		HumanBypass: HumanBypass{Mode: "repo_admin_only"},
	}
	decision := Evaluate(policy, CheckInput{
		Repository:     "taboularasa/de-novo",
		LinearIssueKey: "HAD-665",
		OwnerLabel:     "owner:denovo",
		Actor:          ActorIdentity{Login: "denovo-bot[bot]", Type: "Bot", AppID: "456"},
	})
	if decision.Status != DecisionDeny || decision.ReasonCode != ReasonActorAppIDMismatch {
		t.Fatalf("Evaluate() = %#v, want app id mismatch", decision)
	}
}

func TestFirstLinearIssueKey(t *testing.T) {
	if got := FirstLinearIssueKey("feature/had-665-backstop", "body HAD-664"); got != "HAD-665" {
		t.Fatalf("FirstLinearIssueKey() = %q", got)
	}
}

func testPolicy(t *testing.T) Policy {
	t.Helper()
	policy, err := LoadPolicy("../../config/github-owner-backstop.yaml")
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	return policy
}

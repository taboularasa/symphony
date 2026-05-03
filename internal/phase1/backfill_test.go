package phase1

import (
	"bytes"
	"context"
	"encoding/csv"
	"strings"
	"testing"
)

func TestDecodeBackfillPolicyRejectsUnknownOwner(t *testing.T) {
	_, err := DecodeBackfillPolicy(strings.NewReader(`{"default_owner":"owner:nope"}`))
	if err == nil {
		t.Fatal("expected unknown owner error")
	}
}

func TestDecodeBackfillPolicyRejectsDuplicateProject(t *testing.T) {
	_, err := DecodeBackfillPolicy(strings.NewReader(`{
		"default_owner":"owner:human",
		"project_overrides":[
			{"project":"Symphony","owner":"owner:human"},
			{"project":" symphony ","owner":"owner:hermes"}
		]
	}`))
	if err == nil {
		t.Fatal("expected duplicate project error")
	}
}

func TestPlanBackfillSkipsExistingOwnerAndConflicts(t *testing.T) {
	policy := mustPolicy(t)
	decisions := PlanBackfill(policy, []BackfillIssue{
		issue("HAD-1", "Symphony", "symphony", labels("owner:human")),
		issue("HAD-2", "Hermes", "hermes", labels("owner:hermes", "owner:denovo")),
	})
	if got, want := decisions[0].Action, "skip"; got != want {
		t.Fatalf("HAD-1 action = %s", got)
	}
	if got, want := decisions[0].SkippedReason, "existing_owner"; got != want {
		t.Fatalf("HAD-1 skipped = %s", got)
	}
	if got, want := decisions[1].Action, "conflict"; got != want {
		t.Fatalf("HAD-2 action = %s", got)
	}
	if got, want := decisions[1].SkippedReason, "owner_conflict"; got != want {
		t.Fatalf("HAD-2 skipped = %s", got)
	}
}

func TestPlanBackfillAppliesOverridesAndDefaultHuman(t *testing.T) {
	policy := mustPolicy(t)
	decisions := PlanBackfill(policy, []BackfillIssue{
		issue("HAD-1", "Hermes", "hermes", nil),
		issue("HAD-2", "De Novo", "de-novo", nil),
		issue("HAD-3", "Symphony", "symphony", nil),
		issue("HAD-4", "Unknown", "unknown", nil),
	})
	want := map[string]string{
		"HAD-1": "owner:hermes",
		"HAD-2": "owner:denovo",
		"HAD-3": "owner:human",
		"HAD-4": "owner:human",
	}
	for _, decision := range decisions {
		if got := decision.AppliedLabel; got != want[decision.Identifier] {
			t.Fatalf("%s owner = %s, want %s", decision.Identifier, got, want[decision.Identifier])
		}
		if decision.Action != "apply" {
			t.Fatalf("%s action = %s", decision.Identifier, decision.Action)
		}
	}
}

func TestPlanBackfillInheritsParentOwnerUnlessOverridden(t *testing.T) {
	policy := mustPolicy(t)
	parent := issue("HAD-1", "Hermes", "hermes", nil)
	child := issue("HAD-2", "Unknown", "unknown", nil)
	child.Parent = &IssueParent{ID: parent.ID, Identifier: parent.Identifier}
	overriddenChild := issue("HAD-3", "Symphony", "symphony", nil)
	overriddenChild.Parent = &IssueParent{ID: parent.ID, Identifier: parent.Identifier}

	decisions := PlanBackfill(policy, []BackfillIssue{child, overriddenChild, parent})
	byID := map[string]BackfillDecision{}
	for _, decision := range decisions {
		byID[decision.Identifier] = decision
	}
	if got, want := byID["HAD-2"].AppliedLabel, "owner:hermes"; got != want {
		t.Fatalf("child owner = %s, want %s", got, want)
	}
	if got, want := byID["HAD-2"].DecisionReason, "parent_inheritance"; got != want {
		t.Fatalf("child reason = %s, want %s", got, want)
	}
	if got, want := byID["HAD-3"].AppliedLabel, "owner:human"; got != want {
		t.Fatalf("overridden child owner = %s, want %s", got, want)
	}
}

func TestWriteBackfillCSVSanitizesToColumns(t *testing.T) {
	var buf bytes.Buffer
	decisions := []BackfillDecision{{
		Identifier:       "HAD-1",
		Project:          "Symphony",
		ParentIssueID:    "HAD-0",
		PriorOwnerLabels: []string{"owner:human"},
		AppliedLabel:     "owner:human",
		DecisionReason:   "existing_owner",
		SkippedReason:    "existing_owner",
	}}
	if err := WriteBackfillCSV(&buf, decisions); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	records, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if got, want := len(records), 2; got != want {
		t.Fatalf("records = %d", got)
	}
	if got, want := records[1][0], "HAD-1"; got != want {
		t.Fatalf("issue id = %s", got)
	}
	if strings.Contains(buf.String(), "secret") {
		t.Fatalf("csv contains unexpected secret-like text: %s", buf.String())
	}
}

func TestResolveOwnerLabelIDsRequiresAllLabels(t *testing.T) {
	team := Team{ID: "team-1", Key: "HAD", Name: "Hadto"}
	client := &fakeGraphQLClient{
		responses: []any{
			teamsResponse([]Team{team}),
			labelsResponse([]IssueLabel{{ID: "human", Name: "owner:human", Team: &team}}),
		},
	}
	backfiller := Backfiller{Client: client}
	_, err := backfiller.ResolveOwnerLabelIDs(context.Background(), "Hadto")
	if err == nil {
		t.Fatal("expected missing label error")
	}
}

func TestApplyDecisionsAppendsLabelIDs(t *testing.T) {
	client := &fakeGraphQLClient{
		responses: []any{
			map[string]any{
				"issueUpdate": map[string]any{
					"success": true,
					"issue": map[string]any{
						"id":         "issue-1",
						"identifier": "HAD-1",
					},
				},
			},
		},
	}
	backfiller := Backfiller{Client: client}
	applied, err := backfiller.ApplyDecisions(context.Background(), []BackfillDecision{{
		IssueID:      "issue-1",
		Identifier:   "HAD-1",
		Action:       "apply",
		AppliedLabel: "owner:human",
	}}, map[string]string{"owner:human": "label-human"})
	if err != nil {
		t.Fatalf("apply decisions: %v", err)
	}
	if got, want := applied[0].Action, "applied"; got != want {
		t.Fatalf("action = %s", got)
	}
	if got, want := applied[0].AppliedLabelID, "label-human"; got != want {
		t.Fatalf("label id = %s", got)
	}
}

func mustPolicy(t *testing.T) BackfillPolicy {
	t.Helper()
	policy := BackfillPolicy{
		DefaultOwner:       "owner:human",
		InheritParentOwner: true,
		ProjectOverrides: []ProjectOwnerOverride{
			{Project: "Hermes", Owner: "owner:hermes", Reason: "historical_hermes_default"},
			{Project: "De Novo", Owner: "owner:denovo", Reason: "de_novo_project"},
			{Project: "Symphony", Owner: "owner:human", Reason: "symphony_is_human_until_model_live"},
		},
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("policy: %v", err)
	}
	return policy
}

func issue(identifier, project, slug string, labels []IssueLabel) BackfillIssue {
	return BackfillIssue{
		ID:         "issue-" + identifier,
		Identifier: identifier,
		Project: IssueProject{
			Name:   project,
			SlugID: slug,
		},
		Labels: labels,
	}
}

func labels(names ...string) []IssueLabel {
	result := make([]IssueLabel, 0, len(names))
	for _, name := range names {
		result = append(result, IssueLabel{Name: name})
	}
	return result
}

package phase1

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPlanCreatesMissingOwnerLabels(t *testing.T) {
	client := &fakeGraphQLClient{
		responses: []any{
			teamsResponse([]Team{{ID: "team-1", Key: "HAD", Name: "Hadto"}}),
			labelsResponse(nil),
		},
	}
	provisioner := LabelProvisioner{Client: client}
	plan, err := provisioner.Plan(context.Background(), "Hadto")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Team.ID != "team-1" {
		t.Fatalf("team = %+v", plan.Team)
	}
	if len(plan.Actions) != len(ReservedOwnerLabels) {
		t.Fatalf("actions = %d", len(plan.Actions))
	}
	for _, action := range plan.Actions {
		if action.Action != "create" {
			t.Fatalf("action for %s = %s", action.Name, action.Action)
		}
		if action.Color == "" || action.Description == "" {
			t.Fatalf("action missing proof fields: %+v", action)
		}
	}
}

func TestPlanSkipsExistingOwnerLabels(t *testing.T) {
	team := Team{ID: "team-1", Key: "HAD", Name: "Hadto"}
	labels := make([]IssueLabel, 0, len(ReservedOwnerLabels))
	for i, owner := range ReservedOwnerLabels {
		labels = append(labels, IssueLabel{
			ID:    "label-" + string(rune('a'+i)),
			Name:  owner.Name,
			Color: owner.Color,
			Team:  &team,
		})
	}
	client := &fakeGraphQLClient{
		responses: []any{
			teamsResponse([]Team{team}),
			labelsResponse(labels),
		},
	}
	provisioner := LabelProvisioner{Client: client}
	plan, err := provisioner.Plan(context.Background(), "HAD")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	for _, action := range plan.Actions {
		if action.Action != "exists" {
			t.Fatalf("action for %s = %s", action.Name, action.Action)
		}
	}
}

func TestPlanFailsOnAmbiguousSameNameLabels(t *testing.T) {
	target := Team{ID: "team-1", Key: "HAD", Name: "Hadto"}
	other := Team{ID: "team-2", Key: "ENG", Name: "Engineering"}
	client := &fakeGraphQLClient{
		responses: []any{
			teamsResponse([]Team{target}),
			labelsResponse([]IssueLabel{
				{ID: "label-other", Name: "owner:hermes", Team: &other},
			}),
		},
	}
	provisioner := LabelProvisioner{Client: client}
	_, err := provisioner.Plan(context.Background(), "HAD")
	if err == nil {
		t.Fatal("expected duplicate API identity error")
	}
	if !strings.Contains(err.Error(), "exists outside team") {
		t.Fatalf("error = %v", err)
	}
}

func TestPlanFailsOnUnknownTeam(t *testing.T) {
	client := &fakeGraphQLClient{
		responses: []any{
			teamsResponse([]Team{{ID: "team-1", Key: "ENG", Name: "Engineering"}}),
		},
	}
	provisioner := LabelProvisioner{Client: client}
	_, err := provisioner.Plan(context.Background(), "Hadto")
	if err == nil {
		t.Fatal("expected unknown team error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestApplyCreatesOnlyMissingLabels(t *testing.T) {
	team := Team{ID: "team-1", Key: "HAD", Name: "Hadto"}
	client := &fakeGraphQLClient{
		responses: []any{
			teamsResponse([]Team{team}),
			labelsResponse([]IssueLabel{
				{ID: "existing", Name: "owner:human", Color: "#64748B", Team: &team},
			}),
			createLabelResponse("owner:hermes", "created-hermes", &team),
			createLabelResponse("owner:denovo", "created-denovo", &team),
			createLabelResponse("owner:triage", "created-triage", &team),
		},
	}
	provisioner := LabelProvisioner{Client: client}
	plan, err := provisioner.Apply(context.Background(), "Hadto")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	var created, existing int
	for _, action := range plan.Actions {
		switch action.Action {
		case "created":
			created++
		case "exists":
			existing++
		default:
			t.Fatalf("unexpected action: %+v", action)
		}
	}
	if created != 3 || existing != 1 {
		t.Fatalf("created=%d existing=%d", created, existing)
	}
}

func TestApplySecondRunNoOpsWhenLabelsExist(t *testing.T) {
	team := Team{ID: "team-1", Key: "HAD", Name: "Hadto"}
	labels := make([]IssueLabel, 0, len(ReservedOwnerLabels))
	for i, owner := range ReservedOwnerLabels {
		labels = append(labels, IssueLabel{
			ID:    "label-" + string(rune('a'+i)),
			Name:  owner.Name,
			Color: owner.Color,
			Team:  &team,
		})
	}
	client := &fakeGraphQLClient{
		responses: []any{
			teamsResponse([]Team{team}),
			labelsResponse(labels),
		},
	}
	provisioner := LabelProvisioner{Client: client}
	plan, err := provisioner.Apply(context.Background(), "Hadto")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, action := range plan.Actions {
		if action.Action != "exists" {
			t.Fatalf("action for %s = %s", action.Name, action.Action)
		}
	}
	if len(client.responses) != 0 {
		t.Fatalf("unexpected create calls left %d fake responses", len(client.responses))
	}
}

func TestApplyPropagatesGraphQLError(t *testing.T) {
	client := &fakeGraphQLClient{
		responses: []any{
			teamsResponse([]Team{{ID: "team-1", Key: "HAD", Name: "Hadto"}}),
			errors.New("linear graphql errors: no access (FORBIDDEN)"),
		},
	}
	provisioner := LabelProvisioner{Client: client}
	_, err := provisioner.Apply(context.Background(), "Hadto")
	if err == nil {
		t.Fatal("expected error")
	}
}

type fakeGraphQLClient struct {
	responses []any
	calls     []string
}

func (f *fakeGraphQLClient) Do(ctx context.Context, query string, variables any, out any) error {
	f.calls = append(f.calls, query)
	if len(f.responses) == 0 {
		return errors.New("unexpected graphql call")
	}
	response := f.responses[0]
	f.responses = f.responses[1:]
	if err, ok := response.(error); ok {
		return err
	}
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func teamsResponse(teams []Team) any {
	return map[string]any{
		"teams": map[string]any{
			"nodes": teams,
			"pageInfo": map[string]any{
				"hasNextPage": false,
				"endCursor":   nil,
			},
		},
	}
}

func labelsResponse(labels []IssueLabel) any {
	return map[string]any{
		"issueLabels": map[string]any{
			"nodes": labels,
			"pageInfo": map[string]any{
				"hasNextPage": false,
				"endCursor":   nil,
			},
		},
	}
}

func createLabelResponse(name, id string, team *Team) any {
	return map[string]any{
		"issueLabelCreate": map[string]any{
			"success": true,
			"issueLabel": IssueLabel{
				ID:   id,
				Name: name,
				Team: team,
			},
		},
	}
}

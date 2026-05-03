package phase1

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type GraphQLClient interface {
	Do(ctx context.Context, query string, variables any, out any) error
}

type LabelProvisioner struct {
	Client GraphQLClient
}

type Team struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

func (t Team) Matches(ref string) bool {
	ref = strings.TrimSpace(strings.ToLower(ref))
	if ref == "" {
		return false
	}
	return strings.ToLower(t.ID) == ref || strings.ToLower(t.Key) == ref || strings.ToLower(t.Name) == ref
}

type IssueLabel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Team  *Team  `json:"team"`
}

type LabelPlan struct {
	Team    Team              `json:"team"`
	Actions []LabelPlanAction `json:"actions"`
}

type LabelPlanAction struct {
	Name        string `json:"name"`
	Action      string `json:"action"`
	ID          string `json:"id,omitempty"`
	Color       string `json:"color,omitempty"`
	Description string `json:"description,omitempty"`
}

func (p LabelProvisioner) Plan(ctx context.Context, teamRef string) (LabelPlan, error) {
	if p.Client == nil {
		return LabelPlan{}, errors.New("linear client is required")
	}
	team, err := p.ResolveTeam(ctx, teamRef)
	if err != nil {
		return LabelPlan{}, err
	}
	labels, err := p.ListIssueLabels(ctx)
	if err != nil {
		return LabelPlan{}, err
	}
	actions, err := planOwnerLabelActions(team, labels)
	if err != nil {
		return LabelPlan{}, err
	}
	return LabelPlan{Team: team, Actions: actions}, nil
}

func (p LabelProvisioner) Apply(ctx context.Context, teamRef string) (LabelPlan, error) {
	plan, err := p.Plan(ctx, teamRef)
	if err != nil {
		return LabelPlan{}, err
	}
	for i := range plan.Actions {
		action := &plan.Actions[i]
		if action.Action != "create" {
			continue
		}
		created, err := p.CreateIssueLabel(ctx, plan.Team.ID, *action)
		if err != nil {
			return LabelPlan{}, err
		}
		action.Action = "created"
		action.ID = created.ID
	}
	return plan, nil
}

func (p LabelProvisioner) ResolveTeam(ctx context.Context, teamRef string) (Team, error) {
	teamRef = strings.TrimSpace(teamRef)
	if teamRef == "" {
		return Team{}, errors.New("team is required")
	}
	var after *string
	var matches []Team
	for {
		var out struct {
			Teams struct {
				Nodes    []Team `json:"nodes"`
				PageInfo struct {
					HasNextPage bool    `json:"hasNextPage"`
					EndCursor   *string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"teams"`
		}
		err := p.Client.Do(ctx, listTeamsQuery, map[string]any{
			"first": 100,
			"after": after,
		}, &out)
		if err != nil {
			return Team{}, err
		}
		for _, team := range out.Teams.Nodes {
			if team.Matches(teamRef) {
				matches = append(matches, team)
			}
		}
		if !out.Teams.PageInfo.HasNextPage {
			break
		}
		after = out.Teams.PageInfo.EndCursor
	}
	switch len(matches) {
	case 0:
		return Team{}, fmt.Errorf("linear team %q not found", teamRef)
	case 1:
		return matches[0], nil
	default:
		return Team{}, fmt.Errorf("linear team %q is ambiguous", teamRef)
	}
}

func (p LabelProvisioner) ListIssueLabels(ctx context.Context) ([]IssueLabel, error) {
	var labels []IssueLabel
	var after *string
	for {
		var out struct {
			IssueLabels struct {
				Nodes    []IssueLabel `json:"nodes"`
				PageInfo struct {
					HasNextPage bool    `json:"hasNextPage"`
					EndCursor   *string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"issueLabels"`
		}
		err := p.Client.Do(ctx, listIssueLabelsQuery, map[string]any{
			"first": 250,
			"after": after,
		}, &out)
		if err != nil {
			return nil, err
		}
		labels = append(labels, out.IssueLabels.Nodes...)
		if !out.IssueLabels.PageInfo.HasNextPage {
			break
		}
		after = out.IssueLabels.PageInfo.EndCursor
	}
	return labels, nil
}

func (p LabelProvisioner) CreateIssueLabel(ctx context.Context, teamID string, action LabelPlanAction) (IssueLabel, error) {
	input := map[string]any{
		"name":        action.Name,
		"color":       action.Color,
		"description": action.Description,
		"teamId":      teamID,
	}
	var out struct {
		IssueLabelCreate struct {
			Success    bool       `json:"success"`
			IssueLabel IssueLabel `json:"issueLabel"`
		} `json:"issueLabelCreate"`
	}
	err := p.Client.Do(ctx, createIssueLabelMutation, map[string]any{"input": input}, &out)
	if err != nil {
		return IssueLabel{}, err
	}
	if !out.IssueLabelCreate.Success {
		return IssueLabel{}, fmt.Errorf("issueLabelCreate for %q returned success=false", action.Name)
	}
	return out.IssueLabelCreate.IssueLabel, nil
}

func planOwnerLabelActions(team Team, labels []IssueLabel) ([]LabelPlanAction, error) {
	actions := make([]LabelPlanAction, 0, len(ReservedOwnerLabels))
	for _, reserved := range ReservedOwnerLabels {
		var targetMatches []IssueLabel
		var otherScopeMatches []IssueLabel
		for _, label := range labels {
			if !strings.EqualFold(label.Name, reserved.Name) {
				continue
			}
			if label.Team != nil && label.Team.ID == team.ID {
				targetMatches = append(targetMatches, label)
				continue
			}
			otherScopeMatches = append(otherScopeMatches, label)
		}
		if len(targetMatches) > 1 {
			return nil, fmt.Errorf("label %q already exists more than once in team %s", reserved.Name, team.Name)
		}
		if len(targetMatches) == 1 {
			actions = append(actions, LabelPlanAction{
				Name:   reserved.Name,
				Action: "exists",
				ID:     targetMatches[0].ID,
				Color:  targetMatches[0].Color,
			})
			continue
		}
		if len(otherScopeMatches) > 0 {
			return nil, fmt.Errorf("label %q exists outside team %s; resolve duplicate API identities before provisioning", reserved.Name, team.Name)
		}
		actions = append(actions, LabelPlanAction{
			Name:        reserved.Name,
			Action:      "create",
			Color:       reserved.Color,
			Description: reserved.Description,
		})
	}
	return actions, nil
}

const listTeamsQuery = `
query SymphonyPhase1Teams($first: Int!, $after: String) {
  teams(first: $first, after: $after) {
    nodes {
      id
      key
      name
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}`

const listIssueLabelsQuery = `
query SymphonyPhase1IssueLabels($first: Int!, $after: String) {
  issueLabels(first: $first, after: $after, includeArchived: false) {
    nodes {
      id
      name
      color
      team {
        id
        key
        name
      }
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}`

const createIssueLabelMutation = `
mutation SymphonyPhase1IssueLabelCreate($input: IssueLabelCreateInput!) {
  issueLabelCreate(input: $input) {
    success
    issueLabel {
      id
      name
      color
      team {
        id
        key
        name
      }
    }
  }
}`

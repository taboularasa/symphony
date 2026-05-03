package phase1

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

type BackfillPolicy struct {
	DefaultOwner       string                 `json:"default_owner"`
	InheritParentOwner bool                   `json:"inherit_parent_owner"`
	ProjectOverrides   []ProjectOwnerOverride `json:"project_overrides"`
}

type ProjectOwnerOverride struct {
	Project string `json:"project"`
	Owner   string `json:"owner"`
	Reason  string `json:"reason,omitempty"`
}

func LoadBackfillPolicy(path string) (BackfillPolicy, error) {
	file, err := os.Open(path)
	if err != nil {
		return BackfillPolicy{}, fmt.Errorf("open policy: %w", err)
	}
	defer file.Close()
	policy, err := DecodeBackfillPolicy(file)
	if err != nil {
		return BackfillPolicy{}, err
	}
	return policy, nil
}

func DecodeBackfillPolicy(reader io.Reader) (BackfillPolicy, error) {
	var policy BackfillPolicy
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return BackfillPolicy{}, fmt.Errorf("decode policy: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return BackfillPolicy{}, err
	}
	return policy, nil
}

func (p BackfillPolicy) Validate() error {
	if err := ValidateOwnerLabel(p.DefaultOwner); err != nil {
		return fmt.Errorf("default_owner: %w", err)
	}
	seen := map[string]struct{}{}
	for _, override := range p.ProjectOverrides {
		project := normalizePolicyKey(override.Project)
		if project == "" {
			return errors.New("project override project is required")
		}
		if _, ok := seen[project]; ok {
			return fmt.Errorf("duplicate project override %q", override.Project)
		}
		seen[project] = struct{}{}
		if err := ValidateOwnerLabel(override.Owner); err != nil {
			return fmt.Errorf("project override %q: %w", override.Project, err)
		}
	}
	return nil
}

func (p BackfillPolicy) OwnerForProject(projectName, projectSlug string) (owner string, reason string, explicit bool) {
	projectNames := []string{normalizePolicyKey(projectName), normalizePolicyKey(projectSlug)}
	for _, override := range p.ProjectOverrides {
		overrideProject := normalizePolicyKey(override.Project)
		for _, project := range projectNames {
			if project != "" && project == overrideProject {
				reason := override.Reason
				if strings.TrimSpace(reason) == "" {
					reason = "project_override"
				}
				return override.Owner, reason, true
			}
		}
	}
	return p.DefaultOwner, "default_owner", false
}

type Backfiller struct {
	Client GraphQLClient
}

type BackfillIssue struct {
	ID         string
	Identifier string
	URL        string
	Project    IssueProject
	State      IssueState
	Parent     *IssueParent
	Assignee   *IssueUser
	Delegate   *IssueUser
	Labels     []IssueLabel
}

type IssueProject struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	SlugID string `json:"slugId"`
}

type IssueState struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type IssueParent struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
}

type IssueUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (b Backfiller) ListIssues(ctx context.Context, teamRef string) ([]BackfillIssue, error) {
	if b.Client == nil {
		return nil, errors.New("linear client is required")
	}
	provisioner := LabelProvisioner{Client: b.Client}
	team, err := provisioner.ResolveTeam(ctx, teamRef)
	if err != nil {
		return nil, err
	}

	var issues []BackfillIssue
	var after *string
	for {
		var out struct {
			Team struct {
				Issues struct {
					Nodes    []linearIssueNode `json:"nodes"`
					PageInfo pageInfo          `json:"pageInfo"`
				} `json:"issues"`
			} `json:"team"`
		}
		err := b.Client.Do(ctx, listTeamIssuesQuery, map[string]any{
			"teamId":        team.ID,
			"first":         100,
			"relationFirst": 50,
			"after":         after,
		}, &out)
		if err != nil {
			return nil, err
		}
		for _, node := range out.Team.Issues.Nodes {
			issues = append(issues, node.toBackfillIssue())
		}
		if !out.Team.Issues.PageInfo.HasNextPage {
			break
		}
		after = out.Team.Issues.PageInfo.EndCursor
	}
	return issues, nil
}

func (b Backfiller) ResolveOwnerLabelIDs(ctx context.Context, teamRef string) (map[string]string, error) {
	provisioner := LabelProvisioner{Client: b.Client}
	team, err := provisioner.ResolveTeam(ctx, teamRef)
	if err != nil {
		return nil, err
	}
	labels, err := provisioner.ListIssueLabels(ctx)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]string, len(ReservedOwnerLabels))
	for _, label := range labels {
		if label.Team == nil || label.Team.ID != team.ID {
			continue
		}
		if err := ValidateOwnerLabel(label.Name); err == nil {
			if existing, ok := ids[label.Name]; ok && existing != label.ID {
				return nil, fmt.Errorf("duplicate label IDs for %q in team %s", label.Name, team.Name)
			}
			ids[label.Name] = label.ID
		}
	}
	for _, owner := range ReservedOwnerLabels {
		if ids[owner.Name] == "" {
			return nil, fmt.Errorf("required owner label %q is missing for team %s", owner.Name, team.Name)
		}
	}
	return ids, nil
}

func (b Backfiller) ApplyDecisions(ctx context.Context, decisions []BackfillDecision, ownerLabelIDs map[string]string) ([]BackfillDecision, error) {
	applied := make([]BackfillDecision, len(decisions))
	copy(applied, decisions)
	for i := range applied {
		decision := &applied[i]
		if decision.Action != "apply" {
			continue
		}
		labelID := ownerLabelIDs[decision.AppliedLabel]
		if labelID == "" {
			return nil, fmt.Errorf("missing label ID for %q", decision.AppliedLabel)
		}
		var out struct {
			IssueUpdate struct {
				Success bool `json:"success"`
				Issue   struct {
					ID         string `json:"id"`
					Identifier string `json:"identifier"`
				} `json:"issue"`
			} `json:"issueUpdate"`
		}
		err := b.Client.Do(ctx, appendOwnerLabelMutation, map[string]any{
			"issueId": decision.IssueID,
			"labelId": labelID,
		}, &out)
		if err != nil {
			return nil, err
		}
		if !out.IssueUpdate.Success {
			return nil, fmt.Errorf("issueUpdate for %s returned success=false", decision.Identifier)
		}
		decision.Action = "applied"
		decision.AppliedLabelID = labelID
	}
	return applied, nil
}

type BackfillDecision struct {
	IssueID          string   `json:"issue_id"`
	Identifier       string   `json:"identifier"`
	URL              string   `json:"url,omitempty"`
	Project          string   `json:"project"`
	ProjectSlug      string   `json:"project_slug,omitempty"`
	ParentIssueID    string   `json:"parent_issue_id,omitempty"`
	PriorOwnerLabels []string `json:"prior_owner_labels,omitempty"`
	AppliedLabel     string   `json:"applied_label,omitempty"`
	AppliedLabelID   string   `json:"applied_label_id,omitempty"`
	Action           string   `json:"action"`
	DecisionReason   string   `json:"decision_reason"`
	SkippedReason    string   `json:"skipped_reason,omitempty"`
}

func PlanBackfill(policy BackfillPolicy, issues []BackfillIssue) []BackfillDecision {
	byID := make(map[string]*BackfillIssue, len(issues))
	for i := range issues {
		issue := &issues[i]
		byID[issue.ID] = issue
	}

	decisions := make([]BackfillDecision, 0, len(issues))
	decisionByID := make(map[string]BackfillDecision, len(issues))
	for _, issue := range sortedBackfillIssues(issues) {
		decision := decideIssue(policy, issue, decisionByID)
		decisions = append(decisions, decision)
		decisionByID[issue.ID] = decision
	}
	return decisions
}

func decideIssue(policy BackfillPolicy, issue BackfillIssue, decisionByID map[string]BackfillDecision) BackfillDecision {
	owners := ownerLabels(issue.Labels)
	decision := BackfillDecision{
		IssueID:          issue.ID,
		Identifier:       issue.Identifier,
		URL:              issue.URL,
		Project:          issue.Project.Name,
		ProjectSlug:      issue.Project.SlugID,
		PriorOwnerLabels: owners,
	}
	if issue.Parent != nil {
		decision.ParentIssueID = issue.Parent.Identifier
	}
	switch len(owners) {
	case 0:
	case 1:
		decision.Action = "skip"
		decision.AppliedLabel = owners[0]
		decision.DecisionReason = "existing_owner"
		decision.SkippedReason = "existing_owner"
		return decision
	default:
		decision.Action = "conflict"
		decision.DecisionReason = "owner_conflict"
		decision.SkippedReason = "owner_conflict"
		return decision
	}

	owner, reason, explicit := policy.OwnerForProject(issue.Project.Name, issue.Project.SlugID)
	if policy.InheritParentOwner && !explicit && issue.Parent != nil {
		if parentDecision, ok := decisionByID[issue.Parent.ID]; ok && parentDecision.AppliedLabel != "" && parentDecision.SkippedReason != "owner_conflict" {
			owner = parentDecision.AppliedLabel
			reason = "parent_inheritance"
		}
	}
	decision.Action = "apply"
	decision.AppliedLabel = owner
	decision.DecisionReason = reason
	return decision
}

func WriteBackfillCSV(writer io.Writer, decisions []BackfillDecision) error {
	csvWriter := csv.NewWriter(writer)
	if err := csvWriter.Write([]string{
		"issue_id",
		"project",
		"parent_issue_id",
		"prior_owner_labels",
		"applied_label",
		"decision_reason",
		"skipped_reason",
	}); err != nil {
		return err
	}
	for _, decision := range decisions {
		if err := csvWriter.Write([]string{
			decision.Identifier,
			decision.Project,
			decision.ParentIssueID,
			strings.Join(decision.PriorOwnerLabels, "|"),
			decision.AppliedLabel,
			decision.DecisionReason,
			decision.SkippedReason,
		}); err != nil {
			return err
		}
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

func ownerLabels(labels []IssueLabel) []string {
	var owners []string
	for _, label := range labels {
		name := strings.ToLower(strings.TrimSpace(label.Name))
		if strings.HasPrefix(name, "owner:") {
			owners = append(owners, name)
		}
	}
	sort.Strings(owners)
	return owners
}

func sortedBackfillIssues(issues []BackfillIssue) []BackfillIssue {
	result := make([]BackfillIssue, len(issues))
	copy(result, issues)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Parent == nil && result[j].Parent != nil && result[j].Parent.ID == result[i].ID {
			return true
		}
		if result[j].Parent == nil && result[i].Parent != nil && result[i].Parent.ID == result[j].ID {
			return false
		}
		return result[i].Identifier < result[j].Identifier
	})
	return result
}

func normalizePolicyKey(value string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	return strings.Join(fields, " ")
}

type linearIssueNode struct {
	ID         string       `json:"id"`
	Identifier string       `json:"identifier"`
	URL        string       `json:"url"`
	Project    IssueProject `json:"project"`
	State      IssueState   `json:"state"`
	Parent     *IssueParent `json:"parent"`
	Assignee   *IssueUser   `json:"assignee"`
	Delegate   *IssueUser   `json:"delegate"`
	Labels     struct {
		Nodes []IssueLabel `json:"nodes"`
	} `json:"labels"`
}

func (n linearIssueNode) toBackfillIssue() BackfillIssue {
	return BackfillIssue{
		ID:         n.ID,
		Identifier: n.Identifier,
		URL:        n.URL,
		Project:    n.Project,
		State:      n.State,
		Parent:     n.Parent,
		Assignee:   n.Assignee,
		Delegate:   n.Delegate,
		Labels:     n.Labels.Nodes,
	}
}

type pageInfo struct {
	HasNextPage bool    `json:"hasNextPage"`
	EndCursor   *string `json:"endCursor"`
}

const listTeamIssuesQuery = `
query SymphonyPhase1BackfillIssues($teamId: String!, $first: Int!, $relationFirst: Int!, $after: String) {
  team(id: $teamId) {
    issues(first: $first, after: $after, includeArchived: false) {
      nodes {
        id
        identifier
        url
        project {
          id
          name
          slugId
        }
        state {
          name
          type
        }
        parent {
          id
          identifier
        }
        assignee {
          id
          name
          email
        }
        delegate {
          id
          name
          email
        }
        labels(first: $relationFirst, includeArchived: false) {
          nodes {
            id
            name
            color
          }
        }
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
  }
}`

const appendOwnerLabelMutation = `
mutation SymphonyPhase1BackfillAddOwnerLabel($issueId: String!, $labelId: String!) {
  issueUpdate(id: $issueId, input: { addedLabelIds: [$labelId] }) {
    success
    issue {
      id
      identifier
    }
  }
}`

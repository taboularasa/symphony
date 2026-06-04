package linear

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	defaultCandidatePageSize     = 50
	defaultCandidateRelationSize = 50
)

type GraphQLClient interface {
	Do(ctx context.Context, query string, variables any, out any) error
}

type CandidatePoller struct {
	Client GraphQLClient
}

type CandidateQueryOptions struct {
	ProjectSlug   string
	ActiveStates  []string
	OwnerLabel    string
	First         int
	RelationFirst int
}

type CandidateIssue struct {
	ID         string
	Identifier string
	URL        string
	Project    IssueProject
	State      IssueState
	Assignee   *IssueUser
	Delegate   *IssueUser
	Labels     []IssueLabel
	Owner      OwnerLabelState
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

type IssueLabel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type IssueUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type OwnerLabelState struct {
	Label          string
	Labels         []string
	ConflictReason string
}

const (
	OwnerConflictMissing  = "owner_label_missing"
	OwnerConflictMismatch = "owner_label_mismatch"
	OwnerConflictMultiple = "owner_label_conflict"
)

func (i CandidateIssue) AssignedTo(userID string) bool {
	userID = strings.TrimSpace(userID)
	return userID != "" && i.Assignee != nil && i.Assignee.ID == userID
}

func (p CandidatePoller) FetchCandidateIssues(ctx context.Context, options CandidateQueryOptions) ([]CandidateIssue, error) {
	if p.Client == nil {
		return nil, errors.New("linear client is required")
	}
	normalized, err := normalizeCandidateOptions(options)
	if err != nil {
		return nil, err
	}

	var candidates []CandidateIssue
	var after *string
	for {
		query, variables := buildCandidateIssuesRequest(normalized, after)
		var out candidateIssuesResponse
		if err := p.Client.Do(ctx, query, variables, &out); err != nil {
			return nil, fmt.Errorf("fetch linear candidate issues: %w", err)
		}
		for _, node := range out.Issues.Nodes {
			issue, err := node.toCandidateIssue(normalized.OwnerLabel)
			if err != nil {
				return nil, err
			}
			if !candidateMatchesOwner(issue, normalized.OwnerLabel) {
				continue
			}
			candidates = append(candidates, issue)
		}
		if !out.Issues.PageInfo.HasNextPage {
			break
		}
		if out.Issues.PageInfo.EndCursor == nil || strings.TrimSpace(*out.Issues.PageInfo.EndCursor) == "" {
			return nil, errors.New("linear candidate issues page missing end cursor")
		}
		after = out.Issues.PageInfo.EndCursor
	}
	return candidates, nil
}

func normalizeCandidateOptions(options CandidateQueryOptions) (CandidateQueryOptions, error) {
	options.ProjectSlug = strings.TrimSpace(options.ProjectSlug)
	if options.ProjectSlug == "" {
		return CandidateQueryOptions{}, errors.New("linear project slug is required")
	}
	options.ActiveStates = normalizedStringList(options.ActiveStates)
	if len(options.ActiveStates) == 0 {
		return CandidateQueryOptions{}, errors.New("linear active states are required")
	}
	options.OwnerLabel = normalizeOwnerLabel(options.OwnerLabel)
	if options.First <= 0 {
		options.First = defaultCandidatePageSize
	}
	if options.RelationFirst <= 0 {
		options.RelationFirst = defaultCandidateRelationSize
	}
	return options, nil
}

func buildCandidateIssuesRequest(options CandidateQueryOptions, after *string) (string, map[string]any) {
	query := candidateIssuesQueryWithoutOwner
	var afterValue any
	if after != nil {
		afterValue = after
	}
	variables := map[string]any{
		"projectSlug":   options.ProjectSlug,
		"stateNames":    options.ActiveStates,
		"first":         options.First,
		"relationFirst": options.RelationFirst,
		"after":         afterValue,
	}
	if options.OwnerLabel != "" {
		query = candidateIssuesQueryWithOwner
		variables["ownerLabel"] = options.OwnerLabel
	}
	return query, variables
}

func candidateMatchesOwner(issue CandidateIssue, ownerLabel string) bool {
	if ownerLabel == "" {
		return true
	}
	return issue.Owner.ConflictReason == "" && issue.Owner.Label == ownerLabel
}

func normalizedStringList(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func normalizeOwnerLabel(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

type candidateIssuesResponse struct {
	Issues struct {
		Nodes    []candidateIssueNode `json:"nodes"`
		PageInfo pageInfo             `json:"pageInfo"`
	} `json:"issues"`
}

type candidateIssueNode struct {
	ID         string       `json:"id"`
	Identifier string       `json:"identifier"`
	URL        string       `json:"url"`
	Project    IssueProject `json:"project"`
	State      IssueState   `json:"state"`
	Assignee   *IssueUser   `json:"assignee"`
	Delegate   *IssueUser   `json:"delegate"`
	Labels     struct {
		Nodes []IssueLabel `json:"nodes"`
	} `json:"labels"`
}

func (n candidateIssueNode) toCandidateIssue(expectedOwnerLabel string) (CandidateIssue, error) {
	id := strings.TrimSpace(n.ID)
	if id == "" {
		return CandidateIssue{}, errors.New("linear candidate issue missing id")
	}
	identifier := strings.TrimSpace(n.Identifier)
	if identifier == "" {
		return CandidateIssue{}, fmt.Errorf("linear candidate issue %q missing identifier", id)
	}
	labels := normalizeLabels(n.Labels.Nodes)
	return CandidateIssue{
		ID:         id,
		Identifier: identifier,
		URL:        strings.TrimSpace(n.URL),
		Project:    normalizeProject(n.Project),
		State:      normalizeState(n.State),
		Assignee:   normalizeUser(n.Assignee),
		Delegate:   normalizeUser(n.Delegate),
		Labels:     labels,
		Owner:      normalizeOwnerState(labels, expectedOwnerLabel),
	}, nil
}

func normalizeLabels(labels []IssueLabel) []IssueLabel {
	result := make([]IssueLabel, 0, len(labels))
	for _, label := range labels {
		result = append(result, IssueLabel{
			ID:    strings.TrimSpace(label.ID),
			Name:  strings.ToLower(strings.TrimSpace(label.Name)),
			Color: strings.TrimSpace(label.Color),
		})
	}
	return result
}

func normalizeProject(project IssueProject) IssueProject {
	return IssueProject{
		ID:     strings.TrimSpace(project.ID),
		Name:   strings.TrimSpace(project.Name),
		SlugID: strings.TrimSpace(project.SlugID),
	}
}

func normalizeState(state IssueState) IssueState {
	return IssueState{
		Name: strings.TrimSpace(state.Name),
		Type: strings.TrimSpace(state.Type),
	}
}

func normalizeUser(user *IssueUser) *IssueUser {
	if user == nil {
		return nil
	}
	return &IssueUser{
		ID:    strings.TrimSpace(user.ID),
		Name:  strings.TrimSpace(user.Name),
		Email: strings.TrimSpace(user.Email),
	}
}

func normalizeOwnerState(labels []IssueLabel, expectedOwnerLabel string) OwnerLabelState {
	ownersByName := map[string]struct{}{}
	for _, label := range labels {
		if strings.HasPrefix(label.Name, "owner:") {
			ownersByName[label.Name] = struct{}{}
		}
	}

	owners := make([]string, 0, len(ownersByName))
	for owner := range ownersByName {
		owners = append(owners, owner)
	}
	sort.Strings(owners)

	state := OwnerLabelState{Labels: owners}
	if len(owners) == 1 {
		state.Label = owners[0]
	}
	if expectedOwnerLabel == "" {
		if len(owners) > 1 {
			state.ConflictReason = OwnerConflictMultiple
		}
		return state
	}

	switch len(owners) {
	case 0:
		state.ConflictReason = OwnerConflictMissing
	case 1:
		if owners[0] != expectedOwnerLabel {
			state.ConflictReason = OwnerConflictMismatch
		}
	default:
		state.ConflictReason = OwnerConflictMultiple
	}
	return state
}

type pageInfo struct {
	HasNextPage bool    `json:"hasNextPage"`
	EndCursor   *string `json:"endCursor"`
}

const candidateIssuesQueryWithOwner = `
query SymphonyLinearCandidateIssues($projectSlug: String!, $stateNames: [String!]!, $ownerLabel: String!, $first: Int!, $relationFirst: Int!, $after: String) {
  issues(
    filter: {
      project: { slugId: { eq: $projectSlug } }
      state: { name: { in: $stateNames } }
      labels: { name: { eq: $ownerLabel } }
    }
    first: $first
    after: $after
  ) {
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
        }
      }
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}`

const candidateIssuesQueryWithoutOwner = `
query SymphonyLinearCandidateIssues($projectSlug: String!, $stateNames: [String!]!, $first: Int!, $relationFirst: Int!, $after: String) {
  issues(
    filter: {
      project: { slugId: { eq: $projectSlug } }
      state: { name: { in: $stateNames } }
    }
    first: $first
    after: $after
  ) {
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
        }
      }
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}`

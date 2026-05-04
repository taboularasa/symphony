package linear

import (
	"context"
	"errors"
	"fmt"
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

type IssueLabel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
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
			issue := node.toCandidateIssue()
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
	owners := map[string]struct{}{}
	for _, label := range issue.Labels {
		name := normalizeOwnerLabel(label.Name)
		if strings.HasPrefix(name, "owner:") {
			owners[name] = struct{}{}
		}
	}
	if len(owners) != 1 {
		return false
	}
	_, ok := owners[ownerLabel]
	return ok
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
	Labels     struct {
		Nodes []IssueLabel `json:"nodes"`
	} `json:"labels"`
}

func (n candidateIssueNode) toCandidateIssue() CandidateIssue {
	return CandidateIssue{
		ID:         n.ID,
		Identifier: n.Identifier,
		URL:        n.URL,
		Project:    n.Project,
		State:      n.State,
		Labels:     n.Labels.Nodes,
	}
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

package githubbackstop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/taboularasa/symphony/internal/linear"
)

type OwnerResolver struct {
	Client linear.GraphQLClient
}

type OwnerResolution struct {
	IssueKey       string
	OwnerLabel     string
	OwnerLabels    []string
	ConflictReason string
}

func (r OwnerResolver) ResolveOwnerLabel(ctx context.Context, issueKey string) (OwnerResolution, error) {
	if r.Client == nil {
		return OwnerResolution{}, errors.New("linear client is required")
	}
	issueKey = normalizeIssueKey(issueKey)
	if issueKey == "" {
		return OwnerResolution{}, errors.New("linear issue key is required")
	}
	var out ownerIssueResponse
	if err := r.Client.Do(ctx, ownerIssueQuery, map[string]any{
		"id": issueKey,
	}, &out); err != nil {
		return OwnerResolution{}, fmt.Errorf("resolve linear issue owner: %w", err)
	}
	if strings.TrimSpace(out.Issue.Identifier) == "" {
		return OwnerResolution{}, errors.New("linear issue response missing identifier")
	}
	resolution := OwnerResolution{
		IssueKey: normalizeIssueKey(out.Issue.Identifier),
	}
	for _, label := range out.Issue.Labels.Nodes {
		name := normalizeOwnerLabel(label.Name)
		if strings.HasPrefix(name, "owner:") {
			resolution.OwnerLabels = append(resolution.OwnerLabels, name)
		}
	}
	switch len(resolution.OwnerLabels) {
	case 0:
		resolution.ConflictReason = ReasonOwnerLabelMissing
	case 1:
		resolution.OwnerLabel = resolution.OwnerLabels[0]
	default:
		resolution.ConflictReason = "owner_label_conflict"
	}
	return resolution, nil
}

type ownerIssueResponse struct {
	Issue struct {
		Identifier string `json:"identifier"`
		Labels     struct {
			Nodes []struct {
				Name string `json:"name"`
			} `json:"nodes"`
		} `json:"labels"`
	} `json:"issue"`
}

const ownerIssueQuery = `
query($id:String!){
  issue(id:$id){
    identifier
    labels(first:50){
      nodes{
        name
      }
    }
  }
}`

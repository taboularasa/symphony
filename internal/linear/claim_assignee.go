package linear

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/taboularasa/symphony/internal/workflow"
)

var linearUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type UserResolver struct {
	Client GraphQLClient
}

var _ workflow.ClaimAssigneeResolver = UserResolver{}

func (r UserResolver) ResolveClaimAssignee(ctx context.Context, ref string) (workflow.ClaimAssigneeIdentity, error) {
	if r.Client == nil {
		return workflow.ClaimAssigneeIdentity{}, errors.New("linear client is required")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return workflow.ClaimAssigneeIdentity{}, errors.New("linear claim assignee is required")
	}

	var (
		user linearUser
		err  error
	)
	switch {
	case strings.EqualFold(ref, "me"):
		user, err = r.resolveViewer(ctx)
	case linearUUIDPattern.MatchString(ref):
		user, err = r.resolveUserByID(ctx, ref)
	default:
		user, err = r.resolveUserByLookup(ctx, ref)
	}
	if err != nil {
		return workflow.ClaimAssigneeIdentity{}, fmt.Errorf("resolve linear claim_assignee %q: %w", ref, err)
	}
	return user.toClaimAssigneeIdentity(ref)
}

func (r UserResolver) resolveViewer(ctx context.Context) (linearUser, error) {
	var out struct {
		Viewer linearUser `json:"viewer"`
	}
	if err := r.Client.Do(ctx, resolveViewerQuery, nil, &out); err != nil {
		return linearUser{}, err
	}
	return out.Viewer, nil
}

func (r UserResolver) resolveUserByID(ctx context.Context, id string) (linearUser, error) {
	var out struct {
		User *linearUser `json:"user"`
	}
	if err := r.Client.Do(ctx, resolveUserByIDQuery, map[string]any{"id": id}, &out); err != nil {
		return linearUser{}, err
	}
	if out.User == nil {
		return linearUser{}, fmt.Errorf("user %q not found", id)
	}
	return *out.User, nil
}

func (r UserResolver) resolveUserByLookup(ctx context.Context, ref string) (linearUser, error) {
	var out struct {
		Users struct {
			Nodes []linearUser `json:"nodes"`
		} `json:"users"`
	}
	if err := r.Client.Do(ctx, resolveUserByLookupQuery, map[string]any{
		"ref":   ref,
		"first": 2,
	}, &out); err != nil {
		return linearUser{}, err
	}
	switch len(out.Users.Nodes) {
	case 0:
		return linearUser{}, fmt.Errorf("user %q not found", ref)
	case 1:
		return out.Users.Nodes[0], nil
	default:
		return linearUser{}, fmt.Errorf("user %q is ambiguous", ref)
	}
}

type linearUser struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Active      bool   `json:"active"`
}

func (u linearUser) toClaimAssigneeIdentity(ref string) (workflow.ClaimAssigneeIdentity, error) {
	id := strings.TrimSpace(u.ID)
	if id == "" {
		return workflow.ClaimAssigneeIdentity{}, fmt.Errorf("user %q response missing id", ref)
	}
	if !u.Active {
		return workflow.ClaimAssigneeIdentity{}, fmt.Errorf("user %q is inactive", ref)
	}
	name := strings.TrimSpace(u.DisplayName)
	if name == "" {
		name = strings.TrimSpace(u.Name)
	}
	return workflow.ClaimAssigneeIdentity{
		ID:     id,
		Name:   name,
		Email:  strings.TrimSpace(u.Email),
		Active: true,
	}, nil
}

const userFieldsFragment = `
id
name
displayName
email
active`

const resolveViewerQuery = `
query SymphonyLinearResolveViewer {
  viewer {
    ` + userFieldsFragment + `
  }
}`

const resolveUserByIDQuery = `
query SymphonyLinearResolveUserByID($id: String!) {
  user(id: $id) {
    ` + userFieldsFragment + `
  }
}`

const resolveUserByLookupQuery = `
query SymphonyLinearResolveUserByLookup($ref: String!, $first: Int!) {
  users(
    filter: {
      or: [
        { email: { eqIgnoreCase: $ref } }
        { name: { eqIgnoreCase: $ref } }
        { displayName: { eqIgnoreCase: $ref } }
      ]
    }
    includeDisabled: true
    first: $first
  ) {
    nodes {
      ` + userFieldsFragment + `
    }
  }
}`

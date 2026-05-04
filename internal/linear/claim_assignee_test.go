package linear

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUserResolverResolvesMeViaViewer(t *testing.T) {
	client := &userResolverFakeClient{
		response: map[string]any{
			"viewer": userPayload("user-self", "hermes", "Hermes Bot", "hermes-bot@hadto.net", true),
		},
	}
	resolver := UserResolver{Client: client}

	identity, err := resolver.ResolveClaimAssignee(context.Background(), " me ")
	if err != nil {
		t.Fatalf("resolve me: %v", err)
	}
	if identity.ID != "user-self" || identity.Name != "Hermes Bot" || identity.Email != "hermes-bot@hadto.net" || !identity.Active {
		t.Fatalf("identity = %+v", identity)
	}
	if len(client.calls) != 1 || !strings.Contains(client.calls[0].query, "viewer") {
		t.Fatalf("query = %s", client.calls[0].query)
	}
	if client.calls[0].variables != nil {
		t.Fatalf("viewer variables = %#v, want nil", client.calls[0].variables)
	}
}

func TestUserResolverResolvesDirectUUID(t *testing.T) {
	userID := "11111111-2222-3333-4444-555555555555"
	client := &userResolverFakeClient{
		response: map[string]any{
			"user": userPayload(userID, "Hermes", "Hermes Bot", "hermes-bot@hadto.net", true),
		},
	}
	resolver := UserResolver{Client: client}

	identity, err := resolver.ResolveClaimAssignee(context.Background(), userID)
	if err != nil {
		t.Fatalf("resolve uuid: %v", err)
	}
	if identity.ID != userID {
		t.Fatalf("identity id = %q, want %q", identity.ID, userID)
	}
	if len(client.calls) != 1 || !strings.Contains(client.calls[0].query, "user(id: $id)") {
		t.Fatalf("query = %s", client.calls[0].query)
	}
	if client.calls[0].variables["id"] != userID {
		t.Fatalf("id variable = %#v", client.calls[0].variables["id"])
	}
}

func TestUserResolverResolvesEmailNameAndDisplayNameLookup(t *testing.T) {
	tests := []string{"hermes-bot@hadto.net", "hermes-bot", "Hermes Bot"}
	for _, ref := range tests {
		t.Run(ref, func(t *testing.T) {
			client := &userResolverFakeClient{
				response: map[string]any{
					"users": map[string]any{
						"nodes": []any{userPayload("user-hermes", "hermes-bot", "Hermes Bot", "hermes-bot@hadto.net", true)},
					},
				},
			}
			resolver := UserResolver{Client: client}

			identity, err := resolver.ResolveClaimAssignee(context.Background(), ref)
			if err != nil {
				t.Fatalf("resolve lookup: %v", err)
			}
			if identity.ID != "user-hermes" || identity.Name != "Hermes Bot" {
				t.Fatalf("identity = %+v", identity)
			}
			if !strings.Contains(client.calls[0].query, "email: { eqIgnoreCase: $ref }") ||
				!strings.Contains(client.calls[0].query, "name: { eqIgnoreCase: $ref }") ||
				!strings.Contains(client.calls[0].query, "displayName: { eqIgnoreCase: $ref }") {
				t.Fatalf("lookup query = %s", client.calls[0].query)
			}
			if client.calls[0].variables["ref"] != ref || client.calls[0].variables["first"] != 2 {
				t.Fatalf("lookup variables = %#v", client.calls[0].variables)
			}
		})
	}
}

func TestUserResolverRejectsLookupFailures(t *testing.T) {
	tests := []struct {
		name     string
		response any
		want     string
	}{
		{
			name: "zero matches",
			response: map[string]any{
				"users": map[string]any{"nodes": []any{}},
			},
			want: `user "hermes-bot" not found`,
		},
		{
			name: "multiple matches",
			response: map[string]any{
				"users": map[string]any{
					"nodes": []any{
						userPayload("user-1", "hermes-bot", "Hermes Bot", "one@hadto.net", true),
						userPayload("user-2", "hermes-bot", "Hermes Bot", "two@hadto.net", true),
					},
				},
			},
			want: `user "hermes-bot" is ambiguous`,
		},
		{
			name: "inactive user",
			response: map[string]any{
				"users": map[string]any{
					"nodes": []any{userPayload("user-hermes", "hermes-bot", "Hermes Bot", "hermes-bot@hadto.net", false)},
				},
			},
			want: `user "hermes-bot" is inactive`,
		},
		{
			name: "malformed user",
			response: map[string]any{
				"users": map[string]any{
					"nodes": []any{userPayload("", "hermes-bot", "Hermes Bot", "hermes-bot@hadto.net", true)},
				},
			},
			want: `user "hermes-bot" response missing id`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &userResolverFakeClient{response: tt.response}
			resolver := UserResolver{Client: client}
			_, err := resolver.ResolveClaimAssignee(context.Background(), "hermes-bot")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestUserResolverPropagatesGraphQLErrors(t *testing.T) {
	client := &userResolverFakeClient{
		err: GraphQLErrors{{
			Message:    "schema changed",
			Extensions: map[string]any{"code": "BAD_USER_INPUT"},
		}},
	}
	resolver := UserResolver{Client: client}

	_, err := resolver.ResolveClaimAssignee(context.Background(), "hermes-bot")
	if err == nil {
		t.Fatal("expected error")
	}
	var gqlErrs GraphQLErrors
	if !errors.As(err, &gqlErrs) || gqlErrs[0].Code() != "BAD_USER_INPUT" {
		t.Fatalf("error = %T %v, want BAD_USER_INPUT GraphQLErrors", err, err)
	}
}

func TestUserResolverAuthErrorDoesNotLeakToken(t *testing.T) {
	const token = "super-secret-linear-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != token {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`unauthorized`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, token)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	resolver := UserResolver{Client: client}
	_, err = resolver.ResolveClaimAssignee(context.Background(), "me")
	if err == nil {
		t.Fatal("expected auth error")
	}
	var httpErr HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error = %T %v, want 401 HTTPError", err, err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaked token: %v", err)
	}
}

type userResolverFakeClient struct {
	calls    []userResolverCall
	response any
	err      error
}

type userResolverCall struct {
	query     string
	variables map[string]any
}

func (c *userResolverFakeClient) Do(ctx context.Context, query string, variables any, out any) error {
	var vars map[string]any
	if variables != nil {
		var ok bool
		vars, ok = variables.(map[string]any)
		if !ok {
			return errors.New("variables must be map[string]any")
		}
	}
	c.calls = append(c.calls, userResolverCall{query: query, variables: vars})
	if c.err != nil {
		return c.err
	}
	data, err := json.Marshal(c.response)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func userPayload(id, name, displayName, email string, active bool) map[string]any {
	return map[string]any{
		"id":          id,
		"name":        name,
		"displayName": displayName,
		"email":       email,
		"active":      active,
	}
}

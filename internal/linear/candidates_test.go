package linear

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestBuildCandidateIssuesRequestIncludesOwnerFilter(t *testing.T) {
	query, variables := buildCandidateIssuesRequest(CandidateQueryOptions{
		ProjectSlug:   "symphony",
		ActiveStates:  []string{"Todo", "In Progress"},
		OwnerLabel:    "owner:hermes",
		First:         25,
		RelationFirst: 50,
	}, nil)

	flatQuery := compactSpaces(query)
	wantFilter := "filter: { project: { slugId: { eq: $projectSlug } } state: { name: { in: $stateNames } } labels: { name: { eq: $ownerLabel } } }"
	if !strings.Contains(flatQuery, wantFilter) {
		t.Fatalf("query filter = %s, want %s", flatQuery, wantFilter)
	}
	if variables["projectSlug"] != "symphony" {
		t.Fatalf("projectSlug variable = %v", variables["projectSlug"])
	}
	if !reflect.DeepEqual(variables["stateNames"], []string{"Todo", "In Progress"}) {
		t.Fatalf("stateNames variable = %#v", variables["stateNames"])
	}
	if variables["ownerLabel"] != "owner:hermes" {
		t.Fatalf("ownerLabel variable = %v", variables["ownerLabel"])
	}
	if variables["first"] != 25 || variables["relationFirst"] != 50 {
		t.Fatalf("pagination variables = first:%v relationFirst:%v", variables["first"], variables["relationFirst"])
	}
}

func TestBuildCandidateIssuesRequestPreservesLegacyQueryWithoutOwner(t *testing.T) {
	query, variables := buildCandidateIssuesRequest(CandidateQueryOptions{
		ProjectSlug:   "symphony",
		ActiveStates:  []string{"Todo"},
		First:         25,
		RelationFirst: 50,
	}, nil)

	if strings.Contains(query, "ownerLabel") {
		t.Fatalf("legacy query unexpectedly references ownerLabel: %s", query)
	}
	if strings.Contains(query, "labels: { name:") {
		t.Fatalf("legacy query unexpectedly contains owner label filter: %s", query)
	}
	if _, ok := variables["ownerLabel"]; ok {
		t.Fatalf("legacy variables unexpectedly include ownerLabel: %#v", variables)
	}
}

func TestCandidatePollerFetchesPagesWithExactVariables(t *testing.T) {
	cursor := "cursor-1"
	client := &candidateFakeClient{
		responses: []candidateIssuesResponse{
			responsePage([]candidateIssueNode{
				candidateNode("HAD-1", "owner:hermes"),
			}, true, &cursor),
			responsePage([]candidateIssueNode{
				candidateNode("HAD-2", "owner:hermes"),
			}, false, nil),
		},
	}

	poller := CandidatePoller{Client: client}
	issues, err := poller.FetchCandidateIssues(context.Background(), CandidateQueryOptions{
		ProjectSlug:   " symphony ",
		ActiveStates:  []string{" Todo ", "", "In Progress"},
		OwnerLabel:    " OWNER:Hermes ",
		First:         1,
		RelationFirst: 3,
	})
	if err != nil {
		t.Fatalf("fetch candidates: %v", err)
	}
	if got, want := identifiers(issues), []string{"HAD-1", "HAD-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("identifiers = %#v, want %#v", got, want)
	}
	if len(client.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(client.calls))
	}

	firstVars := client.calls[0].variables
	if firstVars["projectSlug"] != "symphony" {
		t.Fatalf("projectSlug = %v", firstVars["projectSlug"])
	}
	if !reflect.DeepEqual(firstVars["stateNames"], []string{"Todo", "In Progress"}) {
		t.Fatalf("stateNames = %#v", firstVars["stateNames"])
	}
	if firstVars["ownerLabel"] != "owner:hermes" {
		t.Fatalf("ownerLabel = %v", firstVars["ownerLabel"])
	}
	if firstVars["after"] != nil {
		t.Fatalf("first after = %#v, want nil", firstVars["after"])
	}
	if firstVars["first"] != 1 || firstVars["relationFirst"] != 3 {
		t.Fatalf("pagination vars = first:%v relationFirst:%v", firstVars["first"], firstVars["relationFirst"])
	}

	secondAfter, ok := client.calls[1].variables["after"].(*string)
	if !ok || secondAfter == nil || *secondAfter != cursor {
		t.Fatalf("second after = %#v, want %q", client.calls[1].variables["after"], cursor)
	}
}

func TestCandidatePollerDefensivelyFiltersOwnerLabels(t *testing.T) {
	client := &candidateFakeClient{
		responses: []candidateIssuesResponse{
			responsePage([]candidateIssueNode{
				candidateNode("HAD-1", " owner:Hermes "),
				candidateNode("HAD-2"),
				candidateNode("HAD-3", "owner:denovo"),
				candidateNode("HAD-4", "owner:hermes", "owner:denovo"),
				candidateNode("HAD-5", "customer-impact"),
			}, false, nil),
		},
	}

	poller := CandidatePoller{Client: client}
	issues, err := poller.FetchCandidateIssues(context.Background(), CandidateQueryOptions{
		ProjectSlug:  "symphony",
		ActiveStates: []string{"Todo"},
		OwnerLabel:   "owner:hermes",
	})
	if err != nil {
		t.Fatalf("fetch candidates: %v", err)
	}
	if got, want := identifiers(issues), []string{"HAD-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("identifiers = %#v, want %#v", got, want)
	}
}

func TestCandidatePollerLeavesLegacyCandidatesUnfiltered(t *testing.T) {
	client := &candidateFakeClient{
		responses: []candidateIssuesResponse{
			responsePage([]candidateIssueNode{
				candidateNode("HAD-1"),
				candidateNode("HAD-2", "owner:denovo"),
			}, false, nil),
		},
	}

	poller := CandidatePoller{Client: client}
	issues, err := poller.FetchCandidateIssues(context.Background(), CandidateQueryOptions{
		ProjectSlug:  "symphony",
		ActiveStates: []string{"Todo"},
	})
	if err != nil {
		t.Fatalf("fetch candidates: %v", err)
	}
	if got, want := identifiers(issues), []string{"HAD-1", "HAD-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("identifiers = %#v, want %#v", got, want)
	}
}

func TestCandidatePollerPropagatesLinearErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		assertErr  func(*testing.T, error)
	}{
		{
			name:       "graphql error",
			statusCode: http.StatusOK,
			body:       `{"data":null,"errors":[{"message":"schema changed","extensions":{"code":"BAD_USER_INPUT"}}]}`,
			assertErr: func(t *testing.T, err error) {
				var gqlErrs GraphQLErrors
				if !errors.As(err, &gqlErrs) || gqlErrs[0].Code() != "BAD_USER_INPUT" {
					t.Fatalf("error = %T %v, want BAD_USER_INPUT GraphQLErrors", err, err)
				}
			},
		},
		{
			name:       "http 400 graphql error",
			statusCode: http.StatusBadRequest,
			body:       `{"errors":[{"message":"filter invalid","extensions":{"code":"GRAPHQL_PARSE_FAILED"}}]}`,
			assertErr: func(t *testing.T, err error) {
				var gqlErrs GraphQLErrors
				if !errors.As(err, &gqlErrs) || gqlErrs[0].Code() != "GRAPHQL_PARSE_FAILED" {
					t.Fatalf("error = %T %v, want GRAPHQL_PARSE_FAILED GraphQLErrors", err, err)
				}
			},
		},
		{
			name:       "unauthorized",
			statusCode: http.StatusUnauthorized,
			body:       `unauthorized`,
			assertErr: func(t *testing.T, err error) {
				assertHTTPStatus(t, err, http.StatusUnauthorized)
			},
		},
		{
			name:       "forbidden",
			statusCode: http.StatusForbidden,
			body:       `forbidden`,
			assertErr: func(t *testing.T, err error) {
				assertHTTPStatus(t, err, http.StatusForbidden)
			},
		},
		{
			name:       "rate limited graphql error",
			statusCode: http.StatusOK,
			body:       `{"data":null,"errors":[{"message":"rate limited","extensions":{"code":"RATELIMITED"}}]}`,
			assertErr: func(t *testing.T, err error) {
				var gqlErrs GraphQLErrors
				if !errors.As(err, &gqlErrs) || gqlErrs[0].Code() != "RATELIMITED" {
					t.Fatalf("error = %T %v, want RATELIMITED GraphQLErrors", err, err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client, err := NewClient(server.URL, "linear-key")
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			poller := CandidatePoller{Client: client}
			_, err = poller.FetchCandidateIssues(context.Background(), CandidateQueryOptions{
				ProjectSlug:  "symphony",
				ActiveStates: []string{"Todo"},
				OwnerLabel:   "owner:hermes",
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "fetch linear candidate issues") {
				t.Fatalf("error = %v, want fetch context", err)
			}
			tt.assertErr(t, err)
		})
	}
}

func TestCandidatePollerRejectsMissingEndCursor(t *testing.T) {
	client := &candidateFakeClient{
		responses: []candidateIssuesResponse{
			responsePage([]candidateIssueNode{
				candidateNode("HAD-1", "owner:hermes"),
			}, true, nil),
		},
	}

	poller := CandidatePoller{Client: client}
	_, err := poller.FetchCandidateIssues(context.Background(), CandidateQueryOptions{
		ProjectSlug:  "symphony",
		ActiveStates: []string{"Todo"},
		OwnerLabel:   "owner:hermes",
	})
	if err == nil || !strings.Contains(err.Error(), "linear candidate issues page missing end cursor") {
		t.Fatalf("error = %v", err)
	}
}

type candidateFakeClient struct {
	calls     []candidateCall
	responses []candidateIssuesResponse
	errs      []error
}

type candidateCall struct {
	query     string
	variables map[string]any
}

func (c *candidateFakeClient) Do(ctx context.Context, query string, variables any, out any) error {
	vars, ok := variables.(map[string]any)
	if !ok {
		return errors.New("variables must be map[string]any")
	}
	c.calls = append(c.calls, candidateCall{query: query, variables: vars})
	callIndex := len(c.calls) - 1
	if callIndex < len(c.errs) && c.errs[callIndex] != nil {
		return c.errs[callIndex]
	}
	if callIndex >= len(c.responses) {
		return errors.New("missing fake response")
	}
	data, err := json.Marshal(c.responses[callIndex])
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func responsePage(nodes []candidateIssueNode, hasNext bool, endCursor *string) candidateIssuesResponse {
	var response candidateIssuesResponse
	response.Issues.Nodes = nodes
	response.Issues.PageInfo.HasNextPage = hasNext
	response.Issues.PageInfo.EndCursor = endCursor
	return response
}

func candidateNode(identifier string, labels ...string) candidateIssueNode {
	node := candidateIssueNode{
		ID:         strings.ToLower(identifier),
		Identifier: identifier,
		URL:        "https://linear.app/hadto/issue/" + strings.ToLower(identifier),
		Project:    IssueProject{ID: "project-1", Name: "Symphony", SlugID: "symphony"},
		State:      IssueState{Name: "Todo", Type: "unstarted"},
	}
	for _, label := range labels {
		node.Labels.Nodes = append(node.Labels.Nodes, IssueLabel{Name: label})
	}
	return node
}

func identifiers(issues []CandidateIssue) []string {
	result := make([]string, 0, len(issues))
	for _, issue := range issues {
		result = append(result, issue.Identifier)
	}
	return result
}

func compactSpaces(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func assertHTTPStatus(t *testing.T, err error, status int) {
	t.Helper()
	var httpErr HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %T %v, want HTTPError", err, err)
	}
	if httpErr.StatusCode != status {
		t.Fatalf("status = %d, want %d", httpErr.StatusCode, status)
	}
}

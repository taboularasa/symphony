package linear

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestIssueClaimerHappyPathRequiresConfirmation(t *testing.T) {
	client := &claimFakeClient{
		responses: []any{
			claimMutationPayload(true),
			claimConfirmPayload(candidateNodeWithAssignee("HAD-1", &IssueUser{ID: "user-self", Name: "hermes-bot"}, "owner:hermes")),
		},
	}
	claimer := IssueClaimer{Client: client}

	outcome, err := claimer.ClaimIssue(context.Background(), CandidateIssue{
		ID:         "issue-1",
		Identifier: "HAD-1",
	}, ClaimOptions{SelfUserID: "user-self"})
	if err != nil {
		t.Fatalf("claim issue: %v", err)
	}
	if outcome.Code != ClaimOutcomeWin || !outcome.Dispatchable {
		t.Fatalf("outcome = %+v, want dispatchable win", outcome)
	}
	if outcome.ConfirmedAssignee == nil || outcome.ConfirmedAssignee.ID != "user-self" {
		t.Fatalf("confirmed assignee = %+v", outcome.ConfirmedAssignee)
	}
	if len(client.calls) != 2 {
		t.Fatalf("calls = %d, want mutation and confirm", len(client.calls))
	}
	if !strings.Contains(client.calls[0].query, "issueUpdate") {
		t.Fatalf("mutation query = %s", client.calls[0].query)
	}
	if client.calls[0].variables["issueId"] != "issue-1" || client.calls[0].variables["assigneeId"] != "user-self" {
		t.Fatalf("mutation variables = %#v", client.calls[0].variables)
	}
	if !strings.Contains(client.calls[1].query, "issue(id: $issueId)") {
		t.Fatalf("confirm query = %s", client.calls[1].query)
	}
}

func TestIssueClaimerAlreadySelfAssignedStillConfirms(t *testing.T) {
	client := &claimFakeClient{
		responses: []any{
			claimMutationPayload(true),
			claimConfirmPayload(candidateNodeWithAssignee("HAD-1", &IssueUser{ID: "user-self"}, "owner:hermes")),
		},
	}
	claimer := IssueClaimer{Client: client}

	outcome, err := claimer.ClaimIssue(context.Background(), CandidateIssue{
		ID:         "issue-1",
		Identifier: "HAD-1",
		Assignee:   &IssueUser{ID: "user-self"},
	}, ClaimOptions{SelfUserID: "user-self"})
	if err != nil {
		t.Fatalf("claim issue: %v", err)
	}
	if outcome.Code != ClaimOutcomeWin || !outcome.Dispatchable {
		t.Fatalf("outcome = %+v, want dispatchable win", outcome)
	}
	if len(client.calls) != 2 {
		t.Fatalf("calls = %d, want idempotent mutation and confirm", len(client.calls))
	}
}

func TestIssueClaimerDoesNotMutateObservedOtherAssignee(t *testing.T) {
	client := &claimFakeClient{}
	claimer := IssueClaimer{Client: client}

	outcome, err := claimer.ClaimIssue(context.Background(), CandidateIssue{
		ID:         "issue-1",
		Identifier: "HAD-1",
		Assignee:   &IssueUser{ID: "user-other-agent"},
	}, ClaimOptions{SelfUserID: "user-self", AgentUserIDs: []string{"user-other-agent"}})
	if err != nil {
		t.Fatalf("claim issue: %v", err)
	}
	if outcome.Code != ClaimOutcomeLossOtherAgent || outcome.Dispatchable {
		t.Fatalf("outcome = %+v, want other-agent loss", outcome)
	}
	if len(client.calls) != 0 {
		t.Fatalf("calls = %d, want no mutation", len(client.calls))
	}
}

func TestIssueClaimerClassifiesConfirmedLosses(t *testing.T) {
	tests := []struct {
		name     string
		assignee *IssueUser
		want     ClaimOutcomeCode
	}{
		{
			name:     "other agent wins",
			assignee: &IssueUser{ID: "user-other-agent", Name: "other-bot"},
			want:     ClaimOutcomeLossOtherAgent,
		},
		{
			name:     "human wins",
			assignee: &IssueUser{ID: "user-human", Name: "David"},
			want:     ClaimOutcomeLossHuman,
		},
		{
			name:     "assignee cleared",
			assignee: nil,
			want:     ClaimOutcomeCleared,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &claimFakeClient{
				responses: []any{
					claimMutationPayload(true),
					claimConfirmPayload(candidateNodeWithAssignee("HAD-1", tt.assignee, "owner:hermes")),
				},
			}
			claimer := IssueClaimer{Client: client}
			outcome, err := claimer.ClaimIssue(context.Background(), CandidateIssue{
				ID:         "issue-1",
				Identifier: "HAD-1",
			}, ClaimOptions{SelfUserID: "user-self", AgentUserIDs: []string{"user-other-agent"}})
			if err != nil {
				t.Fatalf("claim issue: %v", err)
			}
			if outcome.Code != tt.want || outcome.Dispatchable {
				t.Fatalf("outcome = %+v, want code %q and not dispatchable", outcome, tt.want)
			}
		})
	}
}

func TestIssueClaimerReturnsClaimErrorOnMutationGraphQLError(t *testing.T) {
	client := &claimFakeClient{
		errs: []error{GraphQLErrors{{
			Message:    "mutation failed",
			Extensions: map[string]any{"code": "BAD_USER_INPUT"},
		}}},
	}
	claimer := IssueClaimer{Client: client}

	outcome, err := claimer.ClaimIssue(context.Background(), CandidateIssue{ID: "issue-1"}, ClaimOptions{SelfUserID: "user-self"})
	if err == nil {
		t.Fatal("expected mutation error")
	}
	if outcome.Code != ClaimOutcomeError || outcome.Dispatchable || outcome.Retryable {
		t.Fatalf("outcome = %+v, want non-retryable claim error", outcome)
	}
	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want mutation only", len(client.calls))
	}
}

func TestIssueClaimerReturnsClaimErrorOnUnexpectedPayload(t *testing.T) {
	client := &claimFakeClient{
		responses: []any{claimMutationPayload(false)},
	}
	claimer := IssueClaimer{Client: client}

	outcome, err := claimer.ClaimIssue(context.Background(), CandidateIssue{ID: "issue-1"}, ClaimOptions{SelfUserID: "user-self"})
	if err == nil || !strings.Contains(err.Error(), "linear issueUpdate returned success=false") {
		t.Fatalf("error = %v, want success=false payload error", err)
	}
	if outcome.Code != ClaimOutcomeError || outcome.Dispatchable {
		t.Fatalf("outcome = %+v, want claim error", outcome)
	}
	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want no confirm after unexpected mutation payload", len(client.calls))
	}
}

func TestIssueClaimerMarksRateLimitErrorsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "graphql ratelimited",
			err: GraphQLErrors{{
				Message:    "rate limited",
				Extensions: map[string]any{"code": "RATELIMITED"},
			}},
		},
		{
			name: "http 429",
			err:  HTTPError{StatusCode: http.StatusTooManyRequests},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &claimFakeClient{errs: []error{tt.err}}
			claimer := IssueClaimer{Client: client}
			outcome, err := claimer.ClaimIssue(context.Background(), CandidateIssue{ID: "issue-1"}, ClaimOptions{SelfUserID: "user-self"})
			if err == nil {
				t.Fatal("expected rate limit error")
			}
			if outcome.Code != ClaimOutcomeError || !outcome.Retryable || outcome.Dispatchable {
				t.Fatalf("outcome = %+v, want retryable claim error", outcome)
			}
		})
	}
}

type claimFakeClient struct {
	calls     []claimCall
	responses []any
	errs      []error
}

type claimCall struct {
	query     string
	variables map[string]any
}

func (c *claimFakeClient) Do(ctx context.Context, query string, variables any, out any) error {
	vars, ok := variables.(map[string]any)
	if !ok {
		return errors.New("variables must be map[string]any")
	}
	c.calls = append(c.calls, claimCall{query: query, variables: vars})
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

func claimMutationPayload(success bool) map[string]any {
	return map[string]any{
		"issueUpdate": map[string]any{"success": success},
	}
}

func claimConfirmPayload(node candidateIssueNode) map[string]any {
	return map[string]any{"issue": node}
}

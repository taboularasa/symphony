package linear

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestIssueClaimerHappyPathRequiresConfirmation(t *testing.T) {
	client := &claimFakeClient{
		responses: []any{
			claimConfirmPayload(candidateNodeWithAssignee("HAD-1", nil, "owner:hermes")),
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
	if len(client.calls) != 3 {
		t.Fatalf("calls = %d, want read, mutation, and confirm", len(client.calls))
	}
	if !strings.Contains(client.calls[0].query, "issue(id: $issueId)") {
		t.Fatalf("read query = %s", client.calls[0].query)
	}
	if !strings.Contains(client.calls[1].query, "issueUpdate") {
		t.Fatalf("mutation query = %s", client.calls[1].query)
	}
	if client.calls[1].variables["issueId"] != "issue-1" || client.calls[1].variables["assigneeId"] != "user-self" {
		t.Fatalf("mutation variables = %#v", client.calls[1].variables)
	}
	if !strings.Contains(client.calls[2].query, "issue(id: $issueId)") {
		t.Fatalf("confirm query = %s", client.calls[2].query)
	}
}

func TestIssueClaimerAlreadySelfAssignedWinsWithoutMutation(t *testing.T) {
	client := &claimFakeClient{
		responses: []any{
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
	if len(client.calls) != 1 {
		t.Fatalf("calls = %d, want read only", len(client.calls))
	}
}

func TestIssueClaimerDoesNotMutateObservedOtherAssignee(t *testing.T) {
	client := &claimFakeClient{
		responses: []any{
			claimConfirmPayload(candidateNodeWithAssignee("HAD-1", &IssueUser{ID: "user-other-agent"}, "owner:hermes")),
		},
	}
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
	if len(client.calls) != 1 || strings.Contains(client.calls[0].query, "issueUpdate") {
		t.Fatalf("calls = %+v, want read only and no mutation", client.calls)
	}
}

func TestIssueClaimerDelegateClaimPreservesHumanAssignee(t *testing.T) {
	human := &IssueUser{ID: "user-human", Name: "David"}
	confirmed := candidateNodeWithAssignee("HAD-1", human, "owner:hermes")
	confirmed.Delegate = &IssueUser{ID: "user-hermes", Name: "Hermes"}
	client := &claimFakeClient{
		responses: []any{
			claimConfirmPayload(candidateNodeWithAssignee("HAD-1", human, "owner:hermes")),
			claimMutationPayload(true),
			claimConfirmPayload(confirmed),
		},
	}
	claimer := IssueClaimer{Client: client}

	outcome, err := claimer.ClaimIssue(context.Background(), CandidateIssue{
		ID:         "issue-1",
		Identifier: "HAD-1",
		Assignee:   human,
	}, ClaimOptions{SelfUserID: "user-hermes", Target: "delegate"})
	if err != nil {
		t.Fatalf("claim issue: %v", err)
	}
	if outcome.Code != ClaimOutcomeWin || !outcome.Dispatchable {
		t.Fatalf("outcome = %+v, want delegate win", outcome)
	}
	if outcome.ConfirmedIssue == nil || outcome.ConfirmedIssue.Assignee == nil || outcome.ConfirmedIssue.Assignee.ID != "user-human" {
		t.Fatalf("human assignee was not preserved: %+v", outcome.ConfirmedIssue)
	}
	if outcome.ConfirmedIssue.Delegate == nil || outcome.ConfirmedIssue.Delegate.ID != "user-hermes" {
		t.Fatalf("delegate was not confirmed: %+v", outcome.ConfirmedIssue)
	}
	if outcome.ConfirmedAssignee == nil || outcome.ConfirmedAssignee.ID != "user-hermes" {
		t.Fatalf("confirmed claim user = %+v, want delegate", outcome.ConfirmedAssignee)
	}
	if len(client.calls) != 3 {
		t.Fatalf("calls = %d, want read, mutation, and confirm", len(client.calls))
	}
	if client.calls[1].variables["assigneeId"] != "user-hermes" {
		t.Fatalf("mutation variables = %#v", client.calls[1].variables)
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
					claimConfirmPayload(candidateNodeWithAssignee("HAD-1", nil, "owner:hermes")),
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
		responses: []any{
			claimConfirmPayload(candidateNodeWithAssignee("HAD-1", nil, "owner:hermes")),
		},
		errs: []error{
			nil,
			GraphQLErrors{{
				Message:    "mutation failed",
				Extensions: map[string]any{"code": "BAD_USER_INPUT"},
			}},
		},
	}
	claimer := IssueClaimer{Client: client}

	outcome, err := claimer.ClaimIssue(context.Background(), CandidateIssue{ID: "issue-1"}, ClaimOptions{SelfUserID: "user-self"})
	if err == nil {
		t.Fatal("expected mutation error")
	}
	if outcome.Code != ClaimOutcomeError || outcome.Dispatchable || outcome.Retryable {
		t.Fatalf("outcome = %+v, want non-retryable claim error", outcome)
	}
	if len(client.calls) != 2 {
		t.Fatalf("calls = %d, want read and mutation only", len(client.calls))
	}
}

func TestIssueClaimerReturnsClaimErrorOnUnexpectedPayload(t *testing.T) {
	client := &claimFakeClient{
		responses: []any{
			claimConfirmPayload(candidateNodeWithAssignee("HAD-1", nil, "owner:hermes")),
			claimMutationPayload(false),
		},
	}
	claimer := IssueClaimer{Client: client}

	outcome, err := claimer.ClaimIssue(context.Background(), CandidateIssue{ID: "issue-1"}, ClaimOptions{SelfUserID: "user-self"})
	if err == nil || !strings.Contains(err.Error(), "linear issueUpdate returned success=false") {
		t.Fatalf("error = %v, want success=false payload error", err)
	}
	if outcome.Code != ClaimOutcomeError || outcome.Dispatchable {
		t.Fatalf("outcome = %+v, want claim error", outcome)
	}
	if len(client.calls) != 2 {
		t.Fatalf("calls = %d, want read and no confirm after unexpected mutation payload", len(client.calls))
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

func TestIssueClaimerEmitsClaimMetricsAndStructuredEvents(t *testing.T) {
	observer := &InMemoryClaimObserver{}

	winClient := &claimFakeClient{
		responses: []any{
			claimConfirmPayload(candidateNodeWithAssignee("HAD-1", nil, "owner:hermes")),
			claimMutationPayload(true),
			claimConfirmPayload(candidateNodeWithAssignee("HAD-1", &IssueUser{ID: "user-self"}, "owner:hermes")),
		},
	}
	if _, err := (IssueClaimer{Client: winClient, Observer: observer}).ClaimIssue(
		context.Background(),
		CandidateIssue{ID: "issue-1", Identifier: "HAD-1"},
		ClaimOptions{SelfUserID: "user-self"},
	); err != nil {
		t.Fatalf("win claim: %v", err)
	}

	lossClient := &claimFakeClient{
		responses: []any{
			claimConfirmPayload(candidateNodeWithAssignee("HAD-2", &IssueUser{ID: "user-human"}, "owner:hermes")),
		},
	}
	if _, err := (IssueClaimer{Client: lossClient, Observer: observer}).ClaimIssue(
		context.Background(),
		CandidateIssue{ID: "issue-2", Identifier: "HAD-2", Assignee: &IssueUser{ID: "user-human"}},
		ClaimOptions{SelfUserID: "user-self"},
	); err != nil {
		t.Fatalf("loss claim: %v", err)
	}

	errorClient := &claimFakeClient{
		errs: []error{GraphQLErrors{{
			Message:    "rate limited",
			Extensions: map[string]any{"code": "RATELIMITED"},
		}}},
	}
	if _, err := (IssueClaimer{Client: errorClient, Observer: observer}).ClaimIssue(
		context.Background(),
		CandidateIssue{ID: "issue-3", Identifier: "HAD-3"},
		ClaimOptions{SelfUserID: "user-self"},
	); err == nil {
		t.Fatal("expected error claim")
	}

	if observer.Metrics.ClaimAttempts != 3 || observer.Metrics.ClaimWins != 1 || observer.Metrics.ClaimLosses != 1 || observer.Metrics.ClaimErrors != 1 {
		t.Fatalf("metrics = %+v", observer.Metrics)
	}
	if len(observer.Events) != 3 {
		t.Fatalf("events = %d, want 3", len(observer.Events))
	}
	for _, event := range observer.Events {
		if event.LinearID == "" || event.ReasonCode == "" {
			t.Fatalf("event missing grep fields: %+v", event)
		}
		if event.ReasonCode != string(event.Outcome) {
			t.Fatalf("event reason code = %q outcome = %q", event.ReasonCode, event.Outcome)
		}
	}
	if observer.Events[0].LinearID != "HAD-1" || observer.Events[0].Outcome != ClaimOutcomeWin || !observer.Events[0].Dispatchable {
		t.Fatalf("win event = %+v", observer.Events[0])
	}
	if observer.Events[1].Outcome != ClaimOutcomeLossHuman || observer.Events[1].Dispatchable {
		t.Fatalf("loss event = %+v", observer.Events[1])
	}
	if observer.Events[2].Outcome != ClaimOutcomeError || !observer.Events[2].Retryable {
		t.Fatalf("error event = %+v", observer.Events[2])
	}
}

func TestClaimEventOmitsSensitivePayloadFields(t *testing.T) {
	event := NewClaimEvent(ClaimOutcome{
		IssueID:      "issue-1",
		Identifier:   "HAD-1",
		Code:         ClaimOutcomeError,
		Dispatchable: false,
		Retryable:    true,
	})
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	encoded := string(data)
	for _, want := range []string{`"linear_id":"HAD-1"`, `"reason_code":"claim_error"`, `"retryable":true`} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("event json = %s, want %s", encoded, want)
		}
	}
	for _, forbidden := range []string{"description", "comments", "token", "api_key", "response_body", "raw"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("event json leaked forbidden field %q: %s", forbidden, encoded)
		}
	}
}

func TestIssueClaimerContentionHarnessNoDoubleDispatch(t *testing.T) {
	for round := 0; round < 50; round++ {
		backend := newContentionBackend(fmt.Sprintf("issue-%02d", round), fmt.Sprintf("HAD-%d", round+1))
		candidate := CandidateIssue{ID: backend.issueID, Identifier: backend.identifier}
		agents := []struct {
			id      string
			claimer IssueClaimer
		}{
			{id: "user-agent-a", claimer: IssueClaimer{Client: backend}},
			{id: "user-agent-b", claimer: IssueClaimer{Client: backend}},
		}
		if round%2 == 1 {
			agents[0], agents[1] = agents[1], agents[0]
		}

		wins := 0
		for _, agent := range agents {
			outcome, err := agent.claimer.ClaimIssue(context.Background(), candidate, ClaimOptions{
				SelfUserID:   agent.id,
				AgentUserIDs: []string{"user-agent-a", "user-agent-b"},
			})
			if err != nil {
				t.Fatalf("round %d agent %s claim: %v", round, agent.id, err)
			}
			if outcome.Dispatchable {
				wins++
			}
		}
		if wins != 1 {
			t.Fatalf("round %d wins = %d, want exactly one dispatch", round, wins)
		}
	}
}

func TestLiveLinearClaimIntegrationRequiresExplicitEnv(t *testing.T) {
	if os.Getenv("SYMPHONY_LINEAR_LIVE_TEST") != "1" {
		t.Skip("set SYMPHONY_LINEAR_LIVE_TEST=1 with canary issue env vars to run live Linear claim proof")
	}
	token := os.Getenv("HERMES_LINEAR_TOKEN")
	issueID := os.Getenv("SYMPHONY_LINEAR_CANARY_ISSUE_ID")
	selfUserID := os.Getenv("SYMPHONY_LINEAR_SELF_USER_ID")
	if token == "" || issueID == "" || selfUserID == "" {
		t.Skip("live Linear claim proof requires HERMES_LINEAR_TOKEN, SYMPHONY_LINEAR_CANARY_ISSUE_ID, and SYMPHONY_LINEAR_SELF_USER_ID")
	}
	endpoint := os.Getenv("SYMPHONY_LINEAR_ENDPOINT")
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	client, err := NewClient(endpoint, token)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	outcome, err := (IssueClaimer{Client: client}).ClaimIssue(context.Background(), CandidateIssue{ID: issueID}, ClaimOptions{
		SelfUserID: selfUserID,
		Target:     os.Getenv("SYMPHONY_LINEAR_CLAIM_TARGET"),
	})
	if err != nil {
		t.Fatalf("live claim: %v", err)
	}
	if !outcome.Dispatchable || outcome.Code != ClaimOutcomeWin {
		t.Fatalf("live claim outcome = %+v, want confirmed self-assignment", outcome)
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

type contentionBackend struct {
	mu         sync.Mutex
	issueID    string
	identifier string
	assignee   *IssueUser
}

func newContentionBackend(issueID, identifier string) *contentionBackend {
	return &contentionBackend{issueID: issueID, identifier: identifier}
}

func (b *contentionBackend) Do(ctx context.Context, query string, variables any, out any) error {
	vars, ok := variables.(map[string]any)
	if !ok {
		return errors.New("variables must be map[string]any")
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	var response any
	if strings.Contains(query, "issueUpdate") {
		assigneeID, _ := vars["assigneeId"].(string)
		b.assignee = &IssueUser{ID: assigneeID, Name: assigneeID}
		response = claimMutationPayload(true)
	} else {
		node := candidateNodeWithAssignee(b.identifier, cloneIssueUser(b.assignee), "owner:hermes")
		node.ID = b.issueID
		response = claimConfirmPayload(node)
	}
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

package linear

import (
	"context"
	"errors"
	"testing"
)

func TestDispatchGateAllowsOnlyConfirmedSelfClaim(t *testing.T) {
	policy := testDispatchPolicy()
	confirmed := testDispatchIssue("HAD-1", "Todo", &IssueUser{ID: "user-self"}, "owner:hermes")
	claimer := &dispatchFakeClaimer{
		outcome: ClaimOutcome{
			Code:              ClaimOutcomeWin,
			Dispatchable:      true,
			ConfirmedIssue:    &confirmed,
			ConfirmedAssignee: confirmed.Assignee,
		},
	}
	gate := DispatchGate{Claimer: claimer}

	decision, err := gate.PreLaunch(context.Background(), testDispatchIssue("HAD-1", "Todo", nil, "owner:hermes"), policy)
	if err != nil {
		t.Fatalf("prelaunch: %v", err)
	}
	if decision.Code != DispatchDecisionAllow || !decision.Dispatchable {
		t.Fatalf("decision = %+v, want dispatch allowed", decision)
	}
	if decision.ClaimOutcome == nil || decision.ClaimOutcome.Code != ClaimOutcomeWin {
		t.Fatalf("claim outcome = %+v", decision.ClaimOutcome)
	}
	if len(claimer.calls) != 1 {
		t.Fatalf("claim calls = %d, want 1", len(claimer.calls))
	}
}

func TestDispatchGateBlocksPreLaunchAssignmentChange(t *testing.T) {
	policy := testDispatchPolicy()
	claimer := &dispatchFakeClaimer{
		outcome: ClaimOutcome{
			Code:              ClaimOutcomeLossHuman,
			Dispatchable:      false,
			ConfirmedAssignee: &IssueUser{ID: "user-human"},
		},
	}
	gate := DispatchGate{Claimer: claimer}

	decision, err := gate.PreLaunch(context.Background(), testDispatchIssue("HAD-1", "Todo", nil, "owner:hermes"), policy)
	if err != nil {
		t.Fatalf("prelaunch: %v", err)
	}
	if decision.Code != DispatchDecisionClaimLossHuman || decision.Dispatchable {
		t.Fatalf("decision = %+v, want human claim loss", decision)
	}
}

func TestDispatchGateRechecksOwnerAfterClaimConfirmation(t *testing.T) {
	policy := testDispatchPolicy()
	confirmedWithoutOwner := testDispatchIssue("HAD-1", "Todo", &IssueUser{ID: "user-self"})
	claimer := &dispatchFakeClaimer{
		outcome: ClaimOutcome{
			Code:              ClaimOutcomeWin,
			Dispatchable:      true,
			ConfirmedIssue:    &confirmedWithoutOwner,
			ConfirmedAssignee: confirmedWithoutOwner.Assignee,
		},
	}
	gate := DispatchGate{Claimer: claimer}

	decision, err := gate.PreLaunch(context.Background(), testDispatchIssue("HAD-1", "Todo", nil, "owner:hermes"), policy)
	if err != nil {
		t.Fatalf("prelaunch: %v", err)
	}
	if decision.Code != DispatchDecisionOwnerMissing || decision.Dispatchable {
		t.Fatalf("decision = %+v, want owner missing after confirmation", decision)
	}
}

func TestDispatchGatePropagatesClaimErrors(t *testing.T) {
	policy := testDispatchPolicy()
	claimer := &dispatchFakeClaimer{
		outcome: ClaimOutcome{Code: ClaimOutcomeError},
		err:     errors.New("linear down"),
	}
	gate := DispatchGate{Claimer: claimer}

	decision, err := gate.PreLaunch(context.Background(), testDispatchIssue("HAD-1", "Todo", nil, "owner:hermes"), policy)
	if err == nil {
		t.Fatal("expected claim error")
	}
	if decision.Code != DispatchDecisionClaimError || decision.Dispatchable {
		t.Fatalf("decision = %+v, want claim error", decision)
	}
}

func TestReconcileActiveIssueStopsUnsafeRuns(t *testing.T) {
	policy := testDispatchPolicy()
	tests := []struct {
		name        string
		issue       CandidateIssue
		want        DispatchDecisionCode
		wantCleanup bool
	}{
		{
			name:  "owner removed",
			issue: testDispatchIssue("HAD-1", "Todo", &IssueUser{ID: "user-self"}),
			want:  DispatchDecisionOwnerMissing,
		},
		{
			name:  "owner conflict",
			issue: testDispatchIssue("HAD-2", "Todo", &IssueUser{ID: "user-self"}, "owner:hermes", "owner:denovo"),
			want:  DispatchDecisionOwnerConflict,
		},
		{
			name:        "terminal state",
			issue:       testDispatchIssue("HAD-3", "Done", &IssueUser{ID: "user-self"}, "owner:hermes"),
			want:        DispatchDecisionTerminal,
			wantCleanup: true,
		},
		{
			name:  "non active state",
			issue: testDispatchIssue("HAD-4", "Human Review", &IssueUser{ID: "user-self"}, "owner:hermes"),
			want:  DispatchDecisionNonActive,
		},
		{
			name:  "assigned to another agent",
			issue: testDispatchIssue("HAD-5", "Todo", &IssueUser{ID: "user-other-agent"}, "owner:hermes"),
			want:  DispatchDecisionClaimLossOtherAgent,
		},
		{
			name:  "assigned to human",
			issue: testDispatchIssue("HAD-6", "Todo", &IssueUser{ID: "user-human"}, "owner:hermes"),
			want:  DispatchDecisionClaimLossHuman,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := ReconcileActiveIssue(tt.issue, policy)
			if decision.Code != tt.want || !decision.StopRunning || decision.CleanupWorkspace != tt.wantCleanup || decision.Dispatchable {
				t.Fatalf("decision = %+v, want code %q cleanup=%v stop=true", decision, tt.want, tt.wantCleanup)
			}
			if tt.name == "terminal state" && (decision.Issue.Assignee == nil || decision.Issue.Assignee.ID != "user-self") {
				t.Fatalf("terminal reconciliation must not unset assignee: %+v", decision.Issue.Assignee)
			}
		})
	}
}

func TestRestartRecoveryAllowsSelfAssignedActiveIssue(t *testing.T) {
	decision := EvaluateRestartRecovery(
		testDispatchIssue("HAD-1", "In Progress", &IssueUser{ID: "user-self"}, "owner:hermes"),
		testDispatchPolicy(),
	)
	if decision.Code != DispatchDecisionAllow || !decision.Dispatchable || decision.StopRunning {
		t.Fatalf("decision = %+v, want restart recovery allowed", decision)
	}
}

func TestTwoSchedulersCannotBothDispatchOwnerLabeledIssue(t *testing.T) {
	issue := testDispatchIssue("HAD-1", "In Progress", &IssueUser{ID: "user-agent-a"}, "owner:hermes")
	agentAPolicy := testDispatchPolicyFor("user-agent-a", "user-agent-b")
	agentBPolicy := testDispatchPolicyFor("user-agent-b", "user-agent-a")

	agentADecision := EvaluateRestartRecovery(issue, agentAPolicy)
	if agentADecision.Code != DispatchDecisionAllow || !agentADecision.Dispatchable {
		t.Fatalf("agent A decision = %+v, want dispatch allowed", agentADecision)
	}

	agentBDecision := EvaluateRestartRecovery(issue, agentBPolicy)
	if agentBDecision.Code != DispatchDecisionClaimLossOtherAgent || agentBDecision.Dispatchable {
		t.Fatalf("agent B decision = %+v, want other-agent claim loss", agentBDecision)
	}
}

func testDispatchPolicy() DispatchPolicy {
	return testDispatchPolicyFor("user-self", "user-other-agent")
}

func testDispatchPolicyFor(selfUserID string, otherAgentIDs ...string) DispatchPolicy {
	return DispatchPolicy{
		OwnerLabel:                 "owner:hermes",
		RequireClaimBeforeDispatch: true,
		ActiveStates:               []string{"Todo", "In Progress"},
		TerminalStates:             []string{"Done", "Canceled"},
		Claim: ClaimOptions{
			SelfUserID:   selfUserID,
			AgentUserIDs: otherAgentIDs,
		},
	}
}

func testDispatchIssue(identifier, state string, assignee *IssueUser, labels ...string) CandidateIssue {
	node := candidateNodeWithAssignee(identifier, assignee, labels...)
	node.State.Name = state
	issue, err := node.toCandidateIssue("owner:hermes")
	if err != nil {
		panic(err)
	}
	return issue
}

type dispatchFakeClaimer struct {
	calls   []CandidateIssue
	outcome ClaimOutcome
	err     error
}

func (c *dispatchFakeClaimer) ClaimIssue(ctx context.Context, issue CandidateIssue, options ClaimOptions) (ClaimOutcome, error) {
	c.calls = append(c.calls, issue)
	return c.outcome, c.err
}

package linear

import (
	"context"
	"errors"
	"strings"
)

type IssueClaimClient interface {
	ClaimIssue(ctx context.Context, issue CandidateIssue, options ClaimOptions) (ClaimOutcome, error)
}

type DispatchGate struct {
	Claimer IssueClaimClient
}

type DispatchPolicy struct {
	OwnerLabel                 string
	RequireClaimBeforeDispatch bool
	ActiveStates               []string
	TerminalStates             []string
	Claim                      ClaimOptions
}

type DispatchDecisionCode string

const (
	DispatchDecisionAllow                    DispatchDecisionCode = "dispatch_allowed"
	DispatchDecisionTerminal                 DispatchDecisionCode = "terminal_state"
	DispatchDecisionNonActive                DispatchDecisionCode = "non_active_state"
	DispatchDecisionOwnerMissing             DispatchDecisionCode = OwnerConflictMissing
	DispatchDecisionOwnerMismatch            DispatchDecisionCode = OwnerConflictMismatch
	DispatchDecisionOwnerConflict            DispatchDecisionCode = OwnerConflictMultiple
	DispatchDecisionClaimUnavailable         DispatchDecisionCode = "claim_unavailable"
	DispatchDecisionClaimMissingConfirmation DispatchDecisionCode = "claim_missing_confirmation"
	DispatchDecisionClaimError               DispatchDecisionCode = DispatchDecisionCode(ClaimOutcomeError)
	DispatchDecisionClaimLossOtherAgent      DispatchDecisionCode = DispatchDecisionCode(ClaimOutcomeLossOtherAgent)
	DispatchDecisionClaimLossHuman           DispatchDecisionCode = DispatchDecisionCode(ClaimOutcomeLossHuman)
	DispatchDecisionClaimCleared             DispatchDecisionCode = DispatchDecisionCode(ClaimOutcomeCleared)
)

type DispatchDecision struct {
	Code             DispatchDecisionCode
	Dispatchable     bool
	StopRunning      bool
	CleanupWorkspace bool
	Issue            CandidateIssue
	ClaimOutcome     *ClaimOutcome
}

func (g DispatchGate) PreLaunch(ctx context.Context, issue CandidateIssue, policy DispatchPolicy) (DispatchDecision, error) {
	if decision := evaluateStateAndOwner(issue, policy); decision.Code != DispatchDecisionAllow {
		return decision, nil
	}
	if !policy.RequireClaimBeforeDispatch {
		return allowDispatch(issue, nil), nil
	}
	if g.Claimer == nil {
		decision := blockDispatch(issue, DispatchDecisionClaimUnavailable)
		return decision, errors.New("linear issue claimer is required")
	}
	outcome, err := g.Claimer.ClaimIssue(ctx, issue, policy.Claim)
	if err != nil {
		decision := blockDispatch(issue, DispatchDecisionClaimError)
		decision.ClaimOutcome = &outcome
		return decision, err
	}
	if !outcome.Dispatchable {
		decision := blockDispatch(issue, dispatchCodeFromClaim(outcome.Code))
		decision.ClaimOutcome = &outcome
		return decision, nil
	}
	if outcome.ConfirmedIssue == nil {
		decision := blockDispatch(issue, DispatchDecisionClaimMissingConfirmation)
		decision.ClaimOutcome = &outcome
		return decision, nil
	}
	confirmedDecision := EvaluateRestartRecovery(*outcome.ConfirmedIssue, policy)
	if confirmedDecision.Code != DispatchDecisionAllow {
		confirmedDecision.ClaimOutcome = &outcome
		return confirmedDecision, nil
	}
	return allowDispatch(*outcome.ConfirmedIssue, &outcome), nil
}

func ReconcileActiveIssue(issue CandidateIssue, policy DispatchPolicy) DispatchDecision {
	decision := EvaluateRestartRecovery(issue, policy)
	if decision.Code != DispatchDecisionAllow {
		decision.StopRunning = true
		if decision.Code == DispatchDecisionTerminal {
			decision.CleanupWorkspace = true
		}
	}
	return decision
}

func EvaluateRestartRecovery(issue CandidateIssue, policy DispatchPolicy) DispatchDecision {
	if decision := evaluateStateAndOwner(issue, policy); decision.Code != DispatchDecisionAllow {
		return decision
	}
	if policy.RequireClaimBeforeDispatch {
		claimedUser := claimTargetUser(issue, policy.Claim)
		if claimedUser == nil || strings.TrimSpace(claimedUser.ID) == "" {
			return blockDispatch(issue, DispatchDecisionClaimCleared)
		}
		if claimedUser.ID != strings.TrimSpace(policy.Claim.SelfUserID) {
			return blockDispatch(issue, dispatchCodeFromAssignee(claimedUser, policy.Claim))
		}
	}
	return allowDispatch(issue, nil)
}

func evaluateStateAndOwner(issue CandidateIssue, policy DispatchPolicy) DispatchDecision {
	stateName := strings.TrimSpace(issue.State.Name)
	if stateIn(policy.TerminalStates, stateName) {
		decision := blockDispatch(issue, DispatchDecisionTerminal)
		decision.CleanupWorkspace = true
		return decision
	}
	if !stateIn(policy.ActiveStates, stateName) {
		return blockDispatch(issue, DispatchDecisionNonActive)
	}

	ownerLabel := normalizeOwnerLabel(policy.OwnerLabel)
	if ownerLabel == "" {
		return allowDispatch(issue, nil)
	}
	ownerState := normalizeOwnerState(issue.Labels, ownerLabel)
	switch ownerState.ConflictReason {
	case "":
		return allowDispatch(issue, nil)
	case OwnerConflictMissing:
		return blockDispatch(issue, DispatchDecisionOwnerMissing)
	case OwnerConflictMismatch:
		return blockDispatch(issue, DispatchDecisionOwnerMismatch)
	default:
		return blockDispatch(issue, DispatchDecisionOwnerConflict)
	}
}

func stateIn(states []string, state string) bool {
	state = strings.TrimSpace(state)
	if state == "" {
		return false
	}
	for _, candidate := range states {
		if strings.EqualFold(strings.TrimSpace(candidate), state) {
			return true
		}
	}
	return false
}

func dispatchCodeFromClaim(code ClaimOutcomeCode) DispatchDecisionCode {
	switch code {
	case ClaimOutcomeLossOtherAgent:
		return DispatchDecisionClaimLossOtherAgent
	case ClaimOutcomeLossHuman:
		return DispatchDecisionClaimLossHuman
	case ClaimOutcomeCleared:
		return DispatchDecisionClaimCleared
	case ClaimOutcomeError:
		return DispatchDecisionClaimError
	default:
		return DispatchDecisionClaimError
	}
}

func dispatchCodeFromAssignee(assignee *IssueUser, options ClaimOptions) DispatchDecisionCode {
	if assignee == nil || strings.TrimSpace(assignee.ID) == "" {
		return DispatchDecisionClaimCleared
	}
	if options.agentUserIDSet()[assignee.ID] {
		return DispatchDecisionClaimLossOtherAgent
	}
	return DispatchDecisionClaimLossHuman
}

func allowDispatch(issue CandidateIssue, outcome *ClaimOutcome) DispatchDecision {
	return DispatchDecision{
		Code:         DispatchDecisionAllow,
		Dispatchable: true,
		Issue:        issue,
		ClaimOutcome: outcome,
	}
}

func blockDispatch(issue CandidateIssue, code DispatchDecisionCode) DispatchDecision {
	return DispatchDecision{
		Code:  code,
		Issue: issue,
	}
}

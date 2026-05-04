package linear

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type IssueClaimer struct {
	Client   GraphQLClient
	Observer ClaimObserver
}

type ClaimOptions struct {
	SelfUserID   string
	AgentUserIDs []string
}

type ClaimOutcomeCode string

const (
	ClaimOutcomeWin            ClaimOutcomeCode = "claim_win"
	ClaimOutcomeLossOtherAgent ClaimOutcomeCode = "claim_loss_other_agent"
	ClaimOutcomeLossHuman      ClaimOutcomeCode = "claim_loss_human"
	ClaimOutcomeCleared        ClaimOutcomeCode = "claim_cleared"
	ClaimOutcomeError          ClaimOutcomeCode = "claim_error"
)

type ClaimOutcome struct {
	IssueID           string
	Identifier        string
	Code              ClaimOutcomeCode
	Dispatchable      bool
	Retryable         bool
	ConfirmedIssue    *CandidateIssue
	ConfirmedAssignee *IssueUser
}

func (c IssueClaimer) ClaimIssue(ctx context.Context, issue CandidateIssue, options ClaimOptions) (outcome ClaimOutcome, err error) {
	outcome = ClaimOutcome{IssueID: issue.ID, Identifier: issue.Identifier}
	defer func() {
		c.observeClaim(outcome)
	}()
	if c.Client == nil {
		return claimError(outcome, errors.New("linear client is required"))
	}
	selfUserID := strings.TrimSpace(options.SelfUserID)
	if selfUserID == "" {
		return claimError(outcome, errors.New("linear self user id is required"))
	}
	issueID := strings.TrimSpace(issue.ID)
	if issueID == "" {
		return claimError(outcome, errors.New("linear issue id is required"))
	}
	outcome.IssueID = issueID

	current, err := c.fetchIssue(ctx, issueID)
	if err != nil {
		return claimError(outcome, fmt.Errorf("read linear issue %s before claim: %w", issueID, err))
	}
	if outcome.Identifier == "" {
		outcome.Identifier = current.Identifier
	}
	if current.Assignee != nil && current.Assignee.ID != selfUserID {
		outcome.ConfirmedIssue = &current
		return classifyClaimOutcome(outcome, current.Assignee, options), nil
	}

	if err := c.assignIssue(ctx, issueID, selfUserID); err != nil {
		return claimError(outcome, fmt.Errorf("claim linear issue %s: %w", issueID, err))
	}
	confirmed, err := c.fetchIssue(ctx, issueID)
	if err != nil {
		return claimError(outcome, fmt.Errorf("confirm linear claim for %s: %w", issueID, err))
	}
	if outcome.Identifier == "" {
		outcome.Identifier = confirmed.Identifier
	}
	outcome.ConfirmedIssue = &confirmed
	return classifyClaimOutcome(outcome, confirmed.Assignee, options), nil
}

func (c IssueClaimer) observeClaim(outcome ClaimOutcome) {
	if c.Observer == nil {
		return
	}
	c.Observer.ObserveClaim(NewClaimEvent(outcome))
}

func (c IssueClaimer) assignIssue(ctx context.Context, issueID, assigneeID string) error {
	var out struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	if err := c.Client.Do(ctx, claimIssueMutation, map[string]any{
		"issueId":    issueID,
		"assigneeId": assigneeID,
	}, &out); err != nil {
		return err
	}
	if !out.IssueUpdate.Success {
		return errors.New("linear issueUpdate returned success=false")
	}
	return nil
}

func (c IssueClaimer) fetchIssue(ctx context.Context, issueID string) (CandidateIssue, error) {
	var out struct {
		Issue *candidateIssueNode `json:"issue"`
	}
	if err := c.Client.Do(ctx, confirmClaimQuery, map[string]any{
		"issueId":       issueID,
		"relationFirst": defaultCandidateRelationSize,
	}, &out); err != nil {
		return CandidateIssue{}, err
	}
	if out.Issue == nil {
		return CandidateIssue{}, fmt.Errorf("linear issue %q not found after claim", issueID)
	}
	return out.Issue.toCandidateIssue("")
}

func classifyClaimOutcome(outcome ClaimOutcome, assignee *IssueUser, options ClaimOptions) ClaimOutcome {
	if assignee == nil || strings.TrimSpace(assignee.ID) == "" {
		outcome.Code = ClaimOutcomeCleared
		outcome.Dispatchable = false
		outcome.ConfirmedAssignee = nil
		return outcome
	}
	outcome.ConfirmedAssignee = cloneIssueUser(assignee)
	if assignee.ID == strings.TrimSpace(options.SelfUserID) {
		outcome.Code = ClaimOutcomeWin
		outcome.Dispatchable = true
		return outcome
	}
	if options.agentUserIDSet()[assignee.ID] {
		outcome.Code = ClaimOutcomeLossOtherAgent
		return outcome
	}
	outcome.Code = ClaimOutcomeLossHuman
	return outcome
}

func (o ClaimOptions) agentUserIDSet() map[string]bool {
	agents := map[string]bool{}
	if self := strings.TrimSpace(o.SelfUserID); self != "" {
		agents[self] = true
	}
	for _, id := range o.AgentUserIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			agents[id] = true
		}
	}
	return agents
}

func cloneIssueUser(user *IssueUser) *IssueUser {
	if user == nil {
		return nil
	}
	clone := *user
	return &clone
}

func claimError(outcome ClaimOutcome, err error) (ClaimOutcome, error) {
	outcome.Code = ClaimOutcomeError
	outcome.Dispatchable = false
	outcome.Retryable = isRetryableLinearError(err)
	return outcome, err
}

func isRetryableLinearError(err error) bool {
	var httpErr HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	var gqlErrs GraphQLErrors
	if errors.As(err, &gqlErrs) {
		for _, gqlErr := range gqlErrs {
			if gqlErr.Code() == "RATELIMITED" {
				return true
			}
		}
	}
	return false
}

const claimIssueMutation = `
mutation SymphonyLinearClaimIssue($issueId: String!, $assigneeId: String!) {
  issueUpdate(id: $issueId, input: { assigneeId: $assigneeId }) {
    success
  }
}`

const confirmClaimQuery = `
query SymphonyLinearConfirmClaim($issueId: String!, $relationFirst: Int!) {
  issue(id: $issueId) {
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
    labels(first: $relationFirst, includeArchived: false) {
      nodes {
        id
        name
      }
    }
  }
}`

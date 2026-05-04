package linear

type ClaimObserver interface {
	ObserveClaim(event ClaimEvent)
}

type ClaimEvent struct {
	Event        string           `json:"event"`
	LinearID     string           `json:"linear_id,omitempty"`
	IssueID      string           `json:"issue_id,omitempty"`
	ReasonCode   string           `json:"reason_code"`
	Outcome      ClaimOutcomeCode `json:"outcome"`
	Dispatchable bool             `json:"dispatchable"`
	Retryable    bool             `json:"retryable"`
}

type ClaimMetrics struct {
	ClaimAttempts int `json:"claim_attempts"`
	ClaimWins     int `json:"claim_wins"`
	ClaimLosses   int `json:"claim_losses"`
	ClaimErrors   int `json:"claim_errors"`
}

type InMemoryClaimObserver struct {
	Metrics ClaimMetrics
	Events  []ClaimEvent
}

func NewClaimEvent(outcome ClaimOutcome) ClaimEvent {
	return ClaimEvent{
		Event:        "linear_claim_outcome",
		LinearID:     outcome.Identifier,
		IssueID:      outcome.IssueID,
		ReasonCode:   string(outcome.Code),
		Outcome:      outcome.Code,
		Dispatchable: outcome.Dispatchable,
		Retryable:    outcome.Retryable,
	}
}

func (o *InMemoryClaimObserver) ObserveClaim(event ClaimEvent) {
	if o == nil {
		return
	}
	o.Events = append(o.Events, event)
	o.Metrics.ClaimAttempts++
	switch event.Outcome {
	case ClaimOutcomeWin:
		o.Metrics.ClaimWins++
	case ClaimOutcomeError:
		o.Metrics.ClaimErrors++
	case ClaimOutcomeLossOtherAgent, ClaimOutcomeLossHuman, ClaimOutcomeCleared:
		o.Metrics.ClaimLosses++
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taboularasa/symphony/internal/linear"
	"github.com/taboularasa/symphony/internal/workflow"
)

const (
	defaultWorkflowPath = "hermes/WORKFLOW.md"
	defaultWorkspace    = "/home/david/stacks/hermes-agent"
)

type tokenResolution struct {
	Value      string
	Source     string
	MissingEnv string
	Fallback   bool
}

type runner struct {
	out         io.Writer
	client      linear.GraphQLClient
	workflow    workflow.Definition
	linear      workflow.LinearConfig
	workspace   string
	dryRun      bool
	checkHook   bool
	limit       int
	issueFilter string
	claimAgents []string
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("symphony-hermes", flag.ContinueOnError)
	workflowPath := fs.String("workflow", defaultWorkflowPath, "Hermes WORKFLOW.md path")
	workspacePath := fs.String("workspace", defaultWorkspace, "workspace path passed to the before_run hook")
	projectSlugOverride := fs.String("project-slug", "", "override Linear project slugId for canary proof")
	once := fs.Bool("once", false, "run one polling cycle and exit")
	dryRun := fs.Bool("dry-run", true, "do not mutate Linear claims or dispatch work")
	allowTokenFallback := fs.Bool("allow-token-fallback", false, "allow LINEAR_API_KEY when the workflow token env var is missing")
	checkHook := fs.Bool("check-hook", false, "run the workflow before_run hook once even when no candidate is dispatchable")
	interval := fs.Duration("interval", 30*time.Second, "poll interval when not using --once")
	timeout := fs.Duration("timeout", 2*time.Minute, "timeout for one polling cycle")
	limit := fs.Int("limit", 10, "maximum candidates to inspect per cycle")
	issueFilter := fs.String("issue", "", "only inspect a matching Linear issue identifier, id, or URL")
	agentIDs := fs.String("agent-user-ids", "", "comma-separated Linear user IDs treated as peer agents for claim-loss classification")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *interval <= 0 {
		return errors.New("interval must be positive")
	}
	if *timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if *limit < 0 {
		return errors.New("limit must not be negative")
	}

	def, err := workflow.Load(*workflowPath)
	if err != nil {
		return err
	}
	if err := def.Settings.Tracker.ValidateOwnerClaimContract("owner:hermes", "hermes", true); err != nil {
		return fmt.Errorf("Hermes workflow contract: %w", err)
	}
	if target := def.Settings.Tracker.NormalizedClaimTarget(); target != workflow.ClaimTargetDelegate {
		return fmt.Errorf("Hermes workflow contract: tracker.claim_target must be %q", workflow.ClaimTargetDelegate)
	}

	token, err := resolveTrackerToken(def.Settings.Tracker, *allowTokenFallback)
	if err != nil {
		return err
	}
	client, err := linear.NewClient(resolveEndpoint(def.Settings.Tracker.Endpoint), token.Value)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	linearConfig, err := resolveLinearConfig(ctx, def.Settings.Tracker, client, token.Value, *projectSlugOverride)
	if err != nil {
		return err
	}
	if err := emit(out, "linear_config", map[string]any{
		"project_slug":                  linearConfig.ProjectSlug,
		"owner_label":                   linearConfig.OwnerLabel,
		"claim_assignee":                linearConfig.ClaimAssignee,
		"claim_assignee_id":             linearConfig.ClaimAssigneeID,
		"claim_target":                  linearConfig.ClaimTarget,
		"require_claim_before_dispatch": linearConfig.RequireClaimBeforeDispatch,
		"token_source":                  token.Source,
		"token_fallback":                token.Fallback,
		"missing_token_env":             token.MissingEnv,
		"dry_run":                       *dryRun,
		"issue_filter":                  strings.TrimSpace(*issueFilter),
	}); err != nil {
		return err
	}

	r := runner{
		out:         out,
		client:      client,
		workflow:    def,
		linear:      linearConfig,
		workspace:   *workspacePath,
		dryRun:      *dryRun,
		checkHook:   *checkHook,
		limit:       *limit,
		issueFilter: strings.TrimSpace(*issueFilter),
		claimAgents: normalizeCSV(*agentIDs),
	}
	if *once {
		return r.runOnce(ctx)
	}

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		cycleCtx, cycleCancel := context.WithTimeout(context.Background(), *timeout)
		err := r.runOnce(cycleCtx)
		cycleCancel()
		if err != nil {
			_ = emit(out, "cycle_error", map[string]any{"error": err.Error()})
		}
		select {
		case <-context.Background().Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r runner) runOnce(ctx context.Context) error {
	if r.checkHook {
		if err := r.runHook(ctx, "startup_check"); err != nil {
			return err
		}
	}

	poller := linear.CandidatePoller{Client: r.client}
	candidates, err := poller.FetchCandidateIssues(ctx, linear.CandidateQueryOptions{
		ProjectSlug:  r.linear.ProjectSlug,
		ActiveStates: r.linear.ActiveStates,
		OwnerLabel:   r.linear.OwnerLabel,
	})
	if err != nil {
		return err
	}
	fetchedCandidateCount := len(candidates)
	candidates = filterCandidatesForIssue(candidates, r.issueFilter)
	if err := emit(r.out, "candidate_poll", map[string]any{
		"project_slug":            r.linear.ProjectSlug,
		"owner_label":             r.linear.OwnerLabel,
		"active_states":           r.linear.ActiveStates,
		"candidate_count":         len(candidates),
		"fetched_candidate_count": fetchedCandidateCount,
		"issue_filter":            strings.TrimSpace(r.issueFilter),
		"dry_run":                 r.dryRun,
	}); err != nil {
		return err
	}

	inspected := 0
	for _, candidate := range candidates {
		if r.limit > 0 && inspected >= r.limit {
			break
		}
		inspected++
		if r.dryRun {
			code, dispatchable := r.dryRunDecision(candidate)
			if err := emit(r.out, "candidate_decision", decisionFields(candidate, code, dispatchable, nil)); err != nil {
				return err
			}
			continue
		}
		policy := r.dispatchPolicy()
		decision, err := (linear.DispatchGate{
			Claimer: linear.IssueClaimer{Client: r.client},
		}).PreLaunch(ctx, candidate, policy)
		fields := decisionFields(decision.Issue, string(decision.Code), decision.Dispatchable, decision.ClaimOutcome)
		if err != nil {
			fields["error"] = err.Error()
		}
		if emitErr := emit(r.out, "candidate_decision", fields); emitErr != nil {
			return emitErr
		}
		if err != nil {
			return err
		}
		if decision.Dispatchable {
			if err := r.runHook(ctx, candidate.Identifier); err != nil {
				return err
			}
		}
	}

	return emit(r.out, "summary", map[string]any{
		"candidate_count":  len(candidates),
		"inspected":        inspected,
		"dry_run":          r.dryRun,
		"dispatch_started": false,
	})
}

func (r runner) dryRunDecision(candidate linear.CandidateIssue) (string, bool) {
	decision := linear.EvaluateRestartRecovery(candidate, r.dispatchPolicy())
	switch decision.Code {
	case linear.DispatchDecisionAllow:
		return "dry_run_would_dispatch", true
	case linear.DispatchDecisionClaimCleared:
		return "dry_run_would_claim", false
	default:
		return string(decision.Code), false
	}
}

func filterCandidatesForIssue(candidates []linear.CandidateIssue, issueFilter string) []linear.CandidateIssue {
	issueFilter = strings.TrimSpace(issueFilter)
	if issueFilter == "" {
		return candidates
	}
	filtered := make([]linear.CandidateIssue, 0, len(candidates))
	for _, candidate := range candidates {
		if candidateMatchesIssueFilter(candidate, issueFilter) {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func candidateMatchesIssueFilter(candidate linear.CandidateIssue, issueFilter string) bool {
	filter := strings.ToLower(strings.TrimSpace(issueFilter))
	if filter == "" {
		return true
	}
	identifier := strings.ToLower(strings.TrimSpace(candidate.Identifier))
	id := strings.ToLower(strings.TrimSpace(candidate.ID))
	url := strings.ToLower(strings.TrimSpace(candidate.URL))
	return filter == identifier ||
		filter == id ||
		strings.Contains(filter, "/issue/"+identifier+"/") ||
		strings.HasSuffix(filter, "/issue/"+identifier) ||
		url == filter
}

func (r runner) dispatchPolicy() linear.DispatchPolicy {
	agentIDs := append([]string{r.linear.ClaimAssigneeID}, r.claimAgents...)
	return linear.DispatchPolicy{
		OwnerLabel:                 r.linear.OwnerLabel,
		RequireClaimBeforeDispatch: r.linear.RequireClaimBeforeDispatch,
		ActiveStates:               r.linear.ActiveStates,
		TerminalStates:             r.linear.TerminalStates,
		Claim: linear.ClaimOptions{
			SelfUserID:   r.linear.ClaimAssigneeID,
			AgentUserIDs: agentIDs,
			Target:       r.linear.ClaimTarget,
		},
	}
}

func (r runner) runHook(ctx context.Context, reason string) error {
	result, err := workflow.RunBeforeRunHook(ctx, r.workflow.Settings.Hooks, r.workspace, os.Environ())
	fields := map[string]any{
		"reason":    reason,
		"workspace": r.workspace,
		"duration":  result.Duration.String(),
		"timed_out": result.TimedOut,
		"success":   err == nil,
	}
	if result.Output != "" {
		fields["output"] = result.Output
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	if emitErr := emit(r.out, "hook_result", fields); emitErr != nil {
		return emitErr
	}
	return err
}

func resolveLinearConfig(ctx context.Context, tracker workflow.TrackerConfig, client linear.GraphQLClient, apiKey, projectSlugOverride string) (workflow.LinearConfig, error) {
	if err := tracker.Validate(); err != nil {
		return workflow.LinearConfig{}, err
	}
	projectSlug := strings.TrimSpace(projectSlugOverride)
	if projectSlug == "" {
		projectSlug = strings.TrimSpace(tracker.ProjectSlug)
	}
	if projectSlug == "" {
		return workflow.LinearConfig{}, errors.New("tracker.project_slug is required")
	}
	config := workflow.LinearConfig{
		Endpoint:                   resolveEndpoint(tracker.Endpoint),
		APIKey:                     apiKey,
		ProjectSlug:                projectSlug,
		OwnerLabel:                 tracker.NormalizedOwnerLabel(),
		ClaimAssignee:              tracker.NormalizedClaimAssignee(),
		ClaimTarget:                tracker.NormalizedClaimTarget(),
		RequireClaimBeforeDispatch: tracker.RequireClaimBeforeDispatch,
		ActiveStates:               append([]string(nil), tracker.ActiveStates...),
		TerminalStates:             append([]string(nil), tracker.TerminalStates...),
	}
	if !config.RequireClaimBeforeDispatch {
		return config, nil
	}
	identity, err := (linear.UserResolver{Client: client}).ResolveClaimAssignee(ctx, config.ClaimAssignee)
	if err != nil {
		return workflow.LinearConfig{}, err
	}
	config.ClaimAssigneeID = identity.ID
	return config, nil
}

func resolveTrackerToken(tracker workflow.TrackerConfig, allowFallback bool) (tokenResolution, error) {
	value := strings.TrimSpace(tracker.APIKey)
	if value == "" {
		if fallback := strings.TrimSpace(os.Getenv("LINEAR_API_KEY")); fallback != "" {
			return tokenResolution{Value: fallback, Source: "LINEAR_API_KEY"}, nil
		}
		return tokenResolution{}, errors.New("tracker.api_key is required")
	}
	if strings.HasPrefix(value, "$") {
		envName := strings.TrimPrefix(value, "$")
		if !validEnvName(envName) {
			return tokenResolution{}, fmt.Errorf("tracker.api_key env reference %q is invalid", value)
		}
		if resolved := strings.TrimSpace(os.Getenv(envName)); resolved != "" {
			return tokenResolution{Value: resolved, Source: envName}, nil
		}
		if allowFallback {
			if fallback := strings.TrimSpace(os.Getenv("LINEAR_API_KEY")); fallback != "" {
				return tokenResolution{Value: fallback, Source: "LINEAR_API_KEY", MissingEnv: envName, Fallback: true}, nil
			}
		}
		return tokenResolution{}, fmt.Errorf("%s is not set", envName)
	}
	return tokenResolution{Value: value, Source: "literal"}, nil
}

func resolveEndpoint(endpoint string) string {
	if strings.TrimSpace(endpoint) == "" {
		return workflow.DefaultLinearEndpoint
	}
	return strings.TrimSpace(endpoint)
}

func validEnvName(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func decisionFields(issue linear.CandidateIssue, code string, dispatchable bool, outcome *linear.ClaimOutcome) map[string]any {
	fields := map[string]any{
		"linear_id":    issue.Identifier,
		"issue_id":     issue.ID,
		"url":          issue.URL,
		"project":      issue.Project.Name,
		"project_slug": issue.Project.SlugID,
		"state":        issue.State.Name,
		"code":         code,
		"dispatchable": dispatchable,
	}
	if issue.Assignee != nil {
		fields["assignee_id"] = issue.Assignee.ID
		assigneeName := strings.TrimSpace(issue.Assignee.Name)
		if assigneeName != "" {
			fields["assignee_name"] = assigneeName
		}
	}
	if issue.Delegate != nil {
		fields["delegate_id"] = issue.Delegate.ID
		delegateName := strings.TrimSpace(issue.Delegate.Name)
		if delegateName != "" {
			fields["delegate_name"] = delegateName
		}
	}
	if outcome != nil {
		fields["claim_code"] = outcome.Code
		fields["claim_retryable"] = outcome.Retryable
		if outcome.ConfirmedIssue != nil {
			if outcome.ConfirmedIssue.Assignee != nil {
				fields["confirmed_assignee_id"] = outcome.ConfirmedIssue.Assignee.ID
				fields["confirmed_assignee_name"] = outcome.ConfirmedIssue.Assignee.Name
			}
			if outcome.ConfirmedIssue.Delegate != nil {
				fields["confirmed_delegate_id"] = outcome.ConfirmedIssue.Delegate.ID
				fields["confirmed_delegate_name"] = outcome.ConfirmedIssue.Delegate.Name
			}
		}
		if outcome.ConfirmedAssignee != nil {
			fields["confirmed_claim_user_id"] = outcome.ConfirmedAssignee.ID
			fields["confirmed_claim_user_name"] = outcome.ConfirmedAssignee.Name
		}
	}
	return fields
}

func normalizeCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func emit(out io.Writer, eventType string, fields map[string]any) error {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["event"] = eventType
	fields["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	return json.NewEncoder(out).Encode(fields)
}

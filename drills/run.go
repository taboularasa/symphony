package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

const scenarioHandoff001 = "handoff-001"

type DrillRun struct {
	Scenario string  `json:"scenario"`
	RunID    string  `json:"run_id,omitempty"`
	Events   []Event `json:"events"`
}

type Event struct {
	TS                string `json:"ts"`
	Source            string `json:"source"`
	Kind              string `json:"kind"`
	Actor             string `json:"actor,omitempty"`
	LinearID          string `json:"linear_id,omitempty"`
	ParentLinearID    string `json:"parent_linear_id,omitempty"`
	OwnerLabel        string `json:"owner_label,omitempty"`
	Channel           string `json:"channel,omitempty"`
	MetadataEventType string `json:"metadata_event_type,omitempty"`
	GitHubPR          string `json:"github_pr,omitempty"`
	Outcome           string `json:"outcome,omitempty"`
	AlertCount        *int   `json:"alert_count,omitempty"`
	AlertReason       string `json:"alert_reason,omitempty"`
}

type Report struct {
	Scenario     string        `json:"scenario"`
	RunID        string        `json:"run_id,omitempty"`
	Passed       bool          `json:"passed"`
	Checks       []Check       `json:"checks"`
	Timeline     []MatchedStep `json:"timeline"`
	FirstFailure string        `json:"first_failure,omitempty"`
}

type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

type MatchedStep struct {
	Name     string `json:"name"`
	TS       string `json:"ts"`
	Source   string `json:"source"`
	Kind     string `json:"kind"`
	Actor    string `json:"actor,omitempty"`
	LinearID string `json:"linear_id,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type timedEvent struct {
	Event
	parsed time.Time
	index  int
}

type scanState struct {
	parentID    string
	childID     string
	githubPR    string
	handoffTime time.Time
}

type sequenceStep struct {
	name  string
	match func(Event, *scanState) bool
	apply func(timedEvent, *scanState)
}

func main() {
	eventsPath := flag.String("events", "", "normalized drill event JSON path, or - for stdin")
	plan := flag.Bool("plan", false, "render a live drill readiness and authorization packet")
	planOutput := flag.String("plan-output", "", "optional path to write the live drill readiness packet JSON")
	collect := flag.Bool("collect", false, "collect live drill artifacts and append them to --events input")
	collectOutput := flag.String("collect-output", "", "optional path to write the combined normalized event JSON")
	runID := flag.String("run-id", "", "optional run id for collected output")
	since := flag.String("since", "", "optional RFC3339 lower bound for live artifact collection")
	until := flag.String("until", "", "optional RFC3339 upper bound for live artifact collection")
	linearEndpoint := flag.String("linear-endpoint", "https://api.linear.app/graphql", "Linear GraphQL endpoint")
	linearTokenEnv := flag.String("linear-token-env", "", "env var containing Linear token for collection")
	linearParent := flag.String("linear-parent", "", "Linear parent issue id/key for collection")
	linearChild := flag.String("linear-child", "", "Linear child issue id/key for collection")
	slackEndpoint := flag.String("slack-endpoint", "https://slack.com/api/conversations.history", "Slack conversations.history endpoint")
	slackTokenEnv := flag.String("slack-token-env", "", "env var containing Slack bot token for collection")
	slackChannel := flag.String("slack-channel", "", "Slack channel id/name for collection")
	githubAPIBase := flag.String("github-api-base", "https://api.github.com", "GitHub API base URL")
	githubTokenEnv := flag.String("github-token-env", "", "optional env var containing GitHub token for collection")
	githubPR := flag.String("github-pr", "", "GitHub PR URL for collection")
	githubLinearID := flag.String("github-linear-id", "", "Linear issue id/key associated with --github-pr")
	authorizationRecorded := flag.Bool("authorization-recorded", false, "mark that explicit live-write authorization has been recorded")
	authorizationURL := flag.String("authorization-url", "", "Linear URL for the live-write authorization comment")
	projectSlug := flag.String("project-slug", "6a6a965c3d10", "Linear project slugId for the live drill plan")
	targetRepo := flag.String("target-repo", "taboularasa/de-novo", "target repository for the live drill plan")
	drillBranch := flag.String("drill-branch", "", "planned drill branch name")
	watcherUnit := flag.String("watcher-unit", "symphony-agent-watcher-soak.service", "watcher unit name for the live drill plan")
	drillArtifact := flag.String("drill-artifact", "", "planned normalized drill artifact path")
	drillReport := flag.String("drill-report", "", "planned drill report JSON path")
	hermesWorkflow := flag.String("hermes-workflow", "hermes/WORKFLOW.md", "Hermes workflow path for the live drill plan")
	denovoWorkflow := flag.String("denovo-workflow", "/home/david/code/de-novo/denovo/WORKFLOW.md", "De Novo workflow path for the live drill plan")
	format := flag.String("format", "text", "report format: text or json")
	flag.Parse()

	if strings.TrimSpace(*eventsPath) == "" && !*collect && !*plan {
		fmt.Fprintln(os.Stderr, "missing --events, --collect, or --plan")
		flag.Usage()
		os.Exit(2)
	}
	if *plan {
		packet := BuildDrillPlan(DrillPlanOptions{
			RunID:                 *runID,
			GeneratedAt:           time.Now().UTC(),
			AuthorizationRecorded: *authorizationRecorded,
			AuthorizationURL:      *authorizationURL,
			ProjectSlug:           *projectSlug,
			ParentLinearID:        *linearParent,
			ChildLinearID:         *linearChild,
			BridgeChannel:         *slackChannel,
			TargetRepo:            *targetRepo,
			DrillBranch:           *drillBranch,
			DrillPRURL:            *githubPR,
			WatcherUnit:           *watcherUnit,
			ArtifactPath:          *drillArtifact,
			ReportPath:            *drillReport,
			HermesWorkflow:        *hermesWorkflow,
			DeNovoWorkflow:        *denovoWorkflow,
			LookupEnv:             os.LookupEnv,
		})
		if strings.TrimSpace(*planOutput) != "" {
			if err := WriteDrillPlan(*planOutput, packet); err != nil {
				fmt.Fprintf(os.Stderr, "write plan: %v\n", err)
				os.Exit(2)
			}
		}
		switch *format {
		case "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(packet); err != nil {
				fmt.Fprintf(os.Stderr, "encode plan: %v\n", err)
				os.Exit(2)
			}
		case "text":
			PrintDrillPlan(os.Stdout, packet)
		default:
			fmt.Fprintf(os.Stderr, "unknown --format %q\n", *format)
			os.Exit(2)
		}
		if packet.Status != DrillPlanStatusReady {
			os.Exit(1)
		}
		if strings.TrimSpace(*eventsPath) == "" && !*collect {
			return
		}
	}
	run := DrillRun{Scenario: scenarioHandoff001, RunID: *runID}
	if strings.TrimSpace(*eventsPath) != "" {
		data, err := readInput(*eventsPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read events: %v\n", err)
			os.Exit(2)
		}
		inputRun, err := DecodeRun(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "decode events: %v\n", err)
			os.Exit(2)
		}
		run = mergeRuns(run, inputRun)
	}
	if *collect {
		cfg, err := buildCollectConfig(collectCLIOptions{
			runID:          *runID,
			since:          *since,
			until:          *until,
			linearEndpoint: *linearEndpoint,
			linearTokenEnv: *linearTokenEnv,
			linearParent:   *linearParent,
			linearChild:    *linearChild,
			slackEndpoint:  *slackEndpoint,
			slackTokenEnv:  *slackTokenEnv,
			slackChannel:   *slackChannel,
			githubAPIBase:  *githubAPIBase,
			githubTokenEnv: *githubTokenEnv,
			githubPR:       *githubPR,
			githubLinearID: *githubLinearID,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "configure collection: %v\n", err)
			os.Exit(2)
		}
		collected, err := CollectLiveArtifacts(context.Background(), cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "collect artifacts: %v\n", err)
			os.Exit(1)
		}
		run = mergeRuns(run, collected)
	}
	if strings.TrimSpace(*collectOutput) != "" {
		if err := writeRun(*collectOutput, run); err != nil {
			fmt.Fprintf(os.Stderr, "write collected events: %v\n", err)
			os.Exit(2)
		}
	}
	report := Evaluate(run)
	switch *format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
			os.Exit(2)
		}
	case "text":
		PrintTextReport(os.Stdout, report)
	default:
		fmt.Fprintf(os.Stderr, "unknown --format %q\n", *format)
		os.Exit(2)
	}
	if !report.Passed {
		os.Exit(1)
	}
}

func mergeRuns(base, next DrillRun) DrillRun {
	if base.Scenario == "" {
		base.Scenario = next.Scenario
	}
	if base.Scenario == "" {
		base.Scenario = scenarioHandoff001
	}
	if base.RunID == "" {
		base.RunID = next.RunID
	}
	base.Events = append(base.Events, next.Events...)
	return base
}

func writeRun(path string, run DrillRun) error {
	body, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return os.WriteFile(path, body, 0o644)
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func DecodeRun(data []byte) (DrillRun, error) {
	var run DrillRun
	if err := json.Unmarshal(data, &run); err == nil && run.Events != nil {
		return run, nil
	}
	var events []Event
	if err := json.Unmarshal(data, &events); err != nil {
		return DrillRun{}, err
	}
	return DrillRun{Scenario: scenarioHandoff001, Events: events}, nil
}

func Evaluate(run DrillRun) Report {
	report := Report{Scenario: run.Scenario, RunID: run.RunID}
	if report.Scenario == "" {
		report.Scenario = scenarioHandoff001
	}
	addCheck(&report, "scenario", report.Scenario == scenarioHandoff001, fmt.Sprintf("got %q", report.Scenario))

	events, ok, detail := parseAndSortEvents(run.Events)
	addCheck(&report, "timestamps", ok, detail)
	if !ok {
		finishReport(&report)
		return report
	}

	addCheck(&report, "no watcher or ownership alerts", hasNoAlertEvents(events), "forbidden watcher/ownership alert kinds are absent")

	state := scanState{}
	matched, missing := matchRequiredSequence(events, &state)
	report.Timeline = matched
	addCheck(&report, "required sequence", missing == "", missing)
	addCheck(&report, "no Hermes child Linear writes after handoff", hasNoHermesChildWritesAfterHandoff(events, state), "Hermes wrote no Linear event on the De Novo child after handoff")

	finishReport(&report)
	return report
}

func addCheck(report *Report, name string, passed bool, detail string) {
	check := Check{Name: name, Passed: passed}
	if detail != "" {
		check.Detail = detail
	}
	report.Checks = append(report.Checks, check)
}

func finishReport(report *Report) {
	report.Passed = true
	for _, check := range report.Checks {
		if !check.Passed {
			report.Passed = false
			if report.FirstFailure == "" {
				report.FirstFailure = check.Name
				if check.Detail != "" {
					report.FirstFailure += ": " + check.Detail
				}
			}
		}
	}
}

func parseAndSortEvents(events []Event) ([]timedEvent, bool, string) {
	if len(events) == 0 {
		return nil, false, "no events"
	}
	out := make([]timedEvent, 0, len(events))
	for i, event := range events {
		ts, err := time.Parse(time.RFC3339, event.TS)
		if err != nil {
			return nil, false, fmt.Sprintf("event %d has invalid ts %q", i, event.TS)
		}
		out = append(out, timedEvent{Event: event, parsed: ts, index: i})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].parsed.Equal(out[j].parsed) {
			return out[i].index < out[j].index
		}
		return out[i].parsed.Before(out[j].parsed)
	})
	return out, true, fmt.Sprintf("%d events", len(out))
}

func hasNoAlertEvents(events []timedEvent) bool {
	for _, event := range events {
		if event.AlertReason != "" {
			return false
		}
		if event.AlertCount != nil && *event.AlertCount > 0 {
			return false
		}
		switch event.Kind {
		case "forbidden_project_write", "owner_label_conflict", "actor_rate_limit", "double_claim", "unexpected_write", "watcher_alert":
			return false
		}
	}
	return true
}

func matchRequiredSequence(events []timedEvent, state *scanState) ([]MatchedStep, string) {
	steps := []sequenceStep{
		{
			name: "intake_created",
			match: func(event Event, state *scanState) bool {
				return event.Source == "linear" && event.Kind == "intake_created" && event.OwnerLabel == "owner:human" && event.LinearID != ""
			},
			apply: func(event timedEvent, state *scanState) {
				state.parentID = event.LinearID
			},
		},
		{
			name: "owner_label_set_owner_hermes",
			match: func(event Event, state *scanState) bool {
				return event.Source == "linear" && event.Kind == "owner_label_set" && event.LinearID == state.parentID && event.OwnerLabel == "owner:hermes"
			},
		},
		{
			name: "child_created_owner_denovo",
			match: func(event Event, state *scanState) bool {
				return event.Source == "linear" && event.Kind == "child_created" && event.Actor == "hermes" && event.ParentLinearID == state.parentID && event.LinearID != "" && event.LinearID != state.parentID && event.OwnerLabel == "owner:denovo"
			},
			apply: func(event timedEvent, state *scanState) {
				state.childID = event.LinearID
			},
		},
		{
			name: "bridge_handoff",
			match: func(event Event, state *scanState) bool {
				return isBridgeEvent(event, "handoff", "hermes", state.childID) && isAgentsBridgeChannel(event.Channel)
			},
			apply: func(event timedEvent, state *scanState) {
				state.handoffTime = event.parsed
			},
		},
		{
			name: "adversarial_refusal",
			match: func(event Event, state *scanState) bool {
				return event.Source == "hermes_log" && event.Kind == "adversarial_refusal" && event.Actor == "hermes" && event.LinearID == state.childID && event.OwnerLabel == "owner:denovo" && (event.Outcome == "claim_refused" || event.Outcome == "dispatch_denied")
			},
		},
		{
			name: "denovo_claim_win",
			match: func(event Event, state *scanState) bool {
				return event.Source == "linear" && event.Kind == "claim_win" && event.Actor == "denovo" && event.LinearID == state.childID
			},
		},
		{
			name: "bridge_ack",
			match: func(event Event, state *scanState) bool {
				return isBridgeEvent(event, "ack", "denovo", state.childID) && isAgentsBridgeChannel(event.Channel)
			},
		},
		{
			name: "github_pr_opened",
			match: func(event Event, state *scanState) bool {
				return event.Source == "github" && event.Kind == "pr_opened" && event.Actor == "denovo" && event.LinearID == state.childID && strings.HasPrefix(event.GitHubPR, "https://github.com/")
			},
			apply: func(event timedEvent, state *scanState) {
				state.githubPR = event.GitHubPR
			},
		},
		{
			name: "bridge_release",
			match: func(event Event, state *scanState) bool {
				return isBridgeEvent(event, "release", "denovo", state.childID) && isAgentsBridgeChannel(event.Channel) && (event.GitHubPR == "" || event.GitHubPR == state.githubPR)
			},
		},
		{
			name: "github_pr_merged",
			match: func(event Event, state *scanState) bool {
				return event.Source == "github" && event.Kind == "pr_merged" && event.LinearID == state.childID && event.GitHubPR == state.githubPR
			},
		},
		{
			name: "parent_closed",
			match: func(event Event, state *scanState) bool {
				return event.Source == "linear" && event.Kind == "parent_closed" && event.Actor == "hermes" && event.LinearID == state.parentID
			},
		},
		{
			name: "watcher_soak_clean",
			match: func(event Event, state *scanState) bool {
				return event.Source == "watcher" && event.Kind == "soak_clean" && event.AlertCount != nil && *event.AlertCount == 0
			},
		},
	}

	var matched []MatchedStep
	next := 0
	for _, event := range events {
		if next >= len(steps) {
			break
		}
		step := steps[next]
		if !step.match(event.Event, state) {
			continue
		}
		if step.apply != nil {
			step.apply(event, state)
		}
		matched = append(matched, MatchedStep{
			Name:     step.name,
			TS:       event.TS,
			Source:   event.Source,
			Kind:     event.Kind,
			Actor:    event.Actor,
			LinearID: event.LinearID,
			Detail:   stepDetail(event.Event),
		})
		next++
	}
	if next < len(steps) {
		return matched, "missing or out-of-order step: " + steps[next].name
	}
	return matched, ""
}

func isBridgeEvent(event Event, kind, actor, linearID string) bool {
	return event.Source == "slack" &&
		event.Kind == kind &&
		event.Actor == actor &&
		event.LinearID == linearID &&
		event.MetadataEventType == "agents_bridge_v1"
}

func isAgentsBridgeChannel(channel string) bool {
	return channel == "#agents-bridge" || channel == "C0B83H1F15K"
}

func stepDetail(event Event) string {
	switch {
	case event.GitHubPR != "":
		return event.GitHubPR
	case event.ParentLinearID != "":
		return "parent=" + event.ParentLinearID
	case event.OwnerLabel != "":
		return event.OwnerLabel
	default:
		return ""
	}
}

func hasNoHermesChildWritesAfterHandoff(events []timedEvent, state scanState) bool {
	if state.childID == "" || state.handoffTime.IsZero() {
		return true
	}
	for _, event := range events {
		if !event.parsed.After(state.handoffTime) {
			continue
		}
		if event.Source == "linear" && event.Actor == "hermes" && event.LinearID == state.childID {
			return false
		}
	}
	return true
}

func PrintTextReport(w io.Writer, report Report) {
	status := "FAIL"
	if report.Passed {
		status = "PASS"
	}
	fmt.Fprintf(w, "%s %s", status, report.Scenario)
	if report.RunID != "" {
		fmt.Fprintf(w, " %s", report.RunID)
	}
	fmt.Fprintln(w)
	for _, check := range report.Checks {
		mark := "FAIL"
		if check.Passed {
			mark = "PASS"
		}
		if check.Detail == "" {
			fmt.Fprintf(w, "%s %s\n", mark, check.Name)
			continue
		}
		fmt.Fprintf(w, "%s %s: %s\n", mark, check.Name, check.Detail)
	}
	if report.FirstFailure != "" {
		fmt.Fprintf(w, "first_failure: %s\n", report.FirstFailure)
	}
}

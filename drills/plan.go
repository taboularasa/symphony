package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	DrillPlanSchemaVersion = "hadto.symphony.handoff-drill-plan.v1"
	DrillPlanStatusReady   = "ready"
	DrillPlanStatusBlocked = "blocked"
)

type DrillPlanOptions struct {
	RunID                 string
	GeneratedAt           time.Time
	AuthorizationRecorded bool
	AuthorizationURL      string
	ProjectSlug           string
	ParentLinearID        string
	ChildLinearID         string
	BridgeChannel         string
	TargetRepo            string
	DrillBranch           string
	DrillPRURL            string
	WatcherUnit           string
	ArtifactPath          string
	ReportPath            string
	HermesWorkflow        string
	DeNovoWorkflow        string
	LookupEnv             func(string) (string, bool)
}

type DrillPlanPacket struct {
	SchemaVersion         string            `json:"schema_version"`
	Scenario              string            `json:"scenario"`
	RunID                 string            `json:"run_id"`
	Status                string            `json:"status"`
	GeneratedAt           string            `json:"generated_at"`
	Inputs                DrillPlanInputs   `json:"inputs"`
	Gates                 []DrillPlanGate   `json:"gates"`
	AuthorizationTemplate string            `json:"authorization_template"`
	Commands              DrillPlanCommands `json:"commands"`
	SecretValuesIncluded  bool              `json:"secret_values_included"`
	ReadyToRunLiveWrites  bool              `json:"ready_to_run_live_writes"`
	NextRequiredAction    string            `json:"next_required_action,omitempty"`
}

type DrillPlanInputs struct {
	RunID              string `json:"run_id"`
	ProjectSlug        string `json:"project_slug"`
	ParentLinearID     string `json:"parent_linear_id,omitempty"`
	ChildLinearID      string `json:"child_linear_id,omitempty"`
	ParentInitialOwner string `json:"parent_initial_owner"`
	ParentHermesOwner  string `json:"parent_hermes_owner"`
	ChildOwner         string `json:"child_owner"`
	BridgeChannel      string `json:"bridge_channel"`
	TargetRepo         string `json:"target_repo"`
	DrillBranch        string `json:"drill_branch,omitempty"`
	DrillPRURL         string `json:"drill_pr_url,omitempty"`
	WatcherUnit        string `json:"watcher_unit"`
	ArtifactPath       string `json:"artifact_path,omitempty"`
	ReportPath         string `json:"report_path,omitempty"`
	HermesWorkflow     string `json:"hermes_workflow"`
	DeNovoWorkflow     string `json:"denovo_workflow"`
}

type DrillPlanGate struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type DrillPlanCommands struct {
	Preflight          []string `json:"preflight"`
	HermesDryRun       []string `json:"hermes_dry_run"`
	HermesAdversarial  []string `json:"hermes_adversarial"`
	DeNovoReadOnly     []string `json:"denovo_read_only"`
	DeNovoLiveWrite    []string `json:"denovo_live_write"`
	ArtifactCollection []string `json:"artifact_collection"`
	Rollback           []string `json:"rollback"`
}

func BuildDrillPlan(opts DrillPlanOptions) DrillPlanPacket {
	opts = normalizeDrillPlanOptions(opts)
	inputs := DrillPlanInputs{
		RunID:              opts.RunID,
		ProjectSlug:        opts.ProjectSlug,
		ParentLinearID:     opts.ParentLinearID,
		ChildLinearID:      opts.ChildLinearID,
		ParentInitialOwner: "owner:human",
		ParentHermesOwner:  "owner:hermes",
		ChildOwner:         "owner:denovo",
		BridgeChannel:      opts.BridgeChannel,
		TargetRepo:         opts.TargetRepo,
		DrillBranch:        opts.DrillBranch,
		DrillPRURL:         opts.DrillPRURL,
		WatcherUnit:        opts.WatcherUnit,
		ArtifactPath:       opts.ArtifactPath,
		ReportPath:         opts.ReportPath,
		HermesWorkflow:     opts.HermesWorkflow,
		DeNovoWorkflow:     opts.DeNovoWorkflow,
	}
	gates := []DrillPlanGate{
		requiredGate("authorization_recorded", opts.AuthorizationRecorded && opts.AuthorizationURL != "", "record a HAD-667 authorization comment URL before live writes"),
		requiredGate("parent_canary_issue", opts.ParentLinearID != "", "set --linear-parent to the parent canary issue"),
		requiredGate("child_canary_issue", opts.ChildLinearID != "", "set --linear-child to the child canary issue"),
		requiredGate("drill_branch", opts.DrillBranch != "", "set --drill-branch to the planned De Novo drill branch"),
		requiredGate("artifact_paths", opts.ArtifactPath != "" && opts.ReportPath != "", "set --drill-artifact and --drill-report"),
		requiredGate("linear_api_key", envPresent(opts.LookupEnv, "LINEAR_API_KEY"), "LINEAR_API_KEY must be present for read-only collection and fallback proof"),
		requiredGate("hermes_linear_token", envPresent(opts.LookupEnv, "HERMES_LINEAR_TOKEN"), "HERMES_LINEAR_TOKEN is absent; fallback must be explicitly authorized if used"),
		requiredGate("denovo_linear_token", envPresent(opts.LookupEnv, "DENOVO_LINEAR_TOKEN"), "DENOVO_LINEAR_TOKEN is absent; fallback must be explicitly authorized if used"),
		requiredGate("watcher_linear_token", envPresent(opts.LookupEnv, "WATCHER_LINEAR_TOKEN"), "WATCHER_LINEAR_TOKEN is absent; watcher comments currently need a documented fallback"),
		requiredGate("slack_bot_token", envPresent(opts.LookupEnv, "SLACK_BOT_TOKEN"), "SLACK_BOT_TOKEN must be present for #agents-bridge evidence"),
		requiredGate("hermes_github_app_id", envPresent(opts.LookupEnv, "HERMES_GITHUB_APP_ID"), "HERMES_GITHUB_APP_ID is absent"),
		requiredGate("denovo_github_app_id", envPresent(opts.LookupEnv, "DENOVO_GITHUB_APP_ID"), "DENOVO_GITHUB_APP_ID is absent"),
		requiredGate("bridge_channel", opts.BridgeChannel != "", "set --slack-channel to #agents-bridge channel id"),
		requiredGate("target_repo", opts.TargetRepo == "taboularasa/de-novo", "target repo must be taboularasa/de-novo for owner:denovo drill child"),
		requiredGate("watcher_unit", opts.WatcherUnit != "", "set --watcher-unit"),
	}
	status := DrillPlanStatusReady
	nextAction := ""
	for _, gate := range gates {
		if gate.Status != "passed" {
			status = DrillPlanStatusBlocked
			if nextAction == "" {
				nextAction = gate.Detail
			}
		}
	}
	return DrillPlanPacket{
		SchemaVersion:         DrillPlanSchemaVersion,
		Scenario:              scenarioHandoff001,
		RunID:                 opts.RunID,
		Status:                status,
		GeneratedAt:           opts.GeneratedAt.UTC().Format(time.RFC3339),
		Inputs:                inputs,
		Gates:                 gates,
		AuthorizationTemplate: buildAuthorizationTemplate(inputs),
		Commands:              buildPlanCommands(inputs),
		SecretValuesIncluded:  false,
		ReadyToRunLiveWrites:  status == DrillPlanStatusReady,
		NextRequiredAction:    nextAction,
	}
}

func normalizeDrillPlanOptions(opts DrillPlanOptions) DrillPlanOptions {
	opts.RunID = strings.TrimSpace(opts.RunID)
	if opts.RunID == "" {
		opts.RunID = "handoff-001-YYYYMMDD-HHMMZ"
	}
	if opts.GeneratedAt.IsZero() {
		opts.GeneratedAt = time.Now().UTC()
	}
	opts.ProjectSlug = firstTrimmed(opts.ProjectSlug, "6a6a965c3d10")
	opts.BridgeChannel = firstTrimmed(opts.BridgeChannel, "C0B83H1F15K")
	opts.TargetRepo = firstTrimmed(opts.TargetRepo, "taboularasa/de-novo")
	opts.WatcherUnit = firstTrimmed(opts.WatcherUnit, "symphony-agent-watcher-soak.service")
	opts.ArtifactPath = strings.TrimSpace(opts.ArtifactPath)
	if opts.ArtifactPath == "" {
		opts.ArtifactPath = "build/drills/" + opts.RunID + ".json"
	}
	opts.ReportPath = strings.TrimSpace(opts.ReportPath)
	if opts.ReportPath == "" {
		opts.ReportPath = "build/drills/" + opts.RunID + "-report.json"
	}
	opts.HermesWorkflow = firstTrimmed(opts.HermesWorkflow, "hermes/WORKFLOW.md")
	opts.DeNovoWorkflow = firstTrimmed(opts.DeNovoWorkflow, "/home/david/code/de-novo/denovo/WORKFLOW.md")
	opts.ParentLinearID = strings.TrimSpace(opts.ParentLinearID)
	opts.ChildLinearID = strings.TrimSpace(opts.ChildLinearID)
	opts.DrillBranch = strings.TrimSpace(opts.DrillBranch)
	opts.DrillPRURL = strings.TrimSpace(opts.DrillPRURL)
	opts.AuthorizationURL = strings.TrimSpace(opts.AuthorizationURL)
	if opts.LookupEnv == nil {
		opts.LookupEnv = os.LookupEnv
	}
	return opts
}

func firstTrimmed(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}

func envPresent(lookup func(string) (string, bool), name string) bool {
	value, ok := lookup(name)
	return ok && strings.TrimSpace(value) != ""
}

func requiredGate(name string, passed bool, detail string) DrillPlanGate {
	gate := DrillPlanGate{Name: name, Status: "blocked", Detail: detail}
	if passed {
		gate.Status = "passed"
		gate.Detail = ""
	}
	return gate
}

func buildAuthorizationTemplate(inputs DrillPlanInputs) string {
	return fmt.Sprintf(`I authorize Handoff Drill 001 live writes for <time window>.

Run ID: %s
Parent canary issue: %s
Child canary issue: %s
Bridge channel: %s
Target repo/branch: %s / %s
Watcher unit: %s

Allowed live writes:
- create/cancel the named parent and child canary Linear issues in the Symphony project
- change owner labels on those canary issues only
- run Hermes against the parent/child canaries with the documented override
- run De Novo against the child canary with the documented write gate
- post handoff/ack/release envelopes to #agents-bridge
- open and merge/close the named drill PR in taboularasa/de-novo
- write sanitized drill proof comments back to HAD-667 and the canary issues

Not authorized:
- changes outside the named canary issues, #agents-bridge, and the drill PR
- changing IronClaw Slack mode
- enabling webhook/WASM Slack
- broad GitHub App installation or ruleset changes`, inputs.RunID, placeholder(inputs.ParentLinearID), placeholder(inputs.ChildLinearID), inputs.BridgeChannel, inputs.TargetRepo, placeholder(inputs.DrillBranch), inputs.WatcherUnit)
}

func placeholder(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<TBD>"
	}
	return value
}

func buildPlanCommands(inputs DrillPlanInputs) DrillPlanCommands {
	return DrillPlanCommands{
		Preflight: []string{
			"cd /home/david/stacks/symphony && git status --short --branch && go test ./... && go vet ./... && git diff --check",
			"systemctl --user status " + inputs.WatcherUnit + " --no-pager -l",
		},
		HermesDryRun: []string{
			"cd /home/david/stacks/symphony && doppler run --project lenovo_server --config dev -- go run ./tools/symphony-hermes --once --dry-run=true --allow-token-fallback --check-hook --issue \"$PARENT_LINEAR_ID\" --limit=5",
		},
		HermesAdversarial: []string{
			"cd /home/david/stacks/symphony && doppler run --project lenovo_server --config dev -- go run ./tools/symphony-hermes --once --dry-run=true --allow-token-fallback --issue \"$CHILD_LINEAR_ID\" --limit=5",
		},
		DeNovoReadOnly: []string{
			"cd /home/david/code/de-novo && doppler run --project lenovo_server --config dev -- go run ./cmd/symphony-linear-drill --max-iterations 1 --duration 0s --output /tmp/denovo-symphony-linear-drill-smoke.json",
		},
		DeNovoLiveWrite: []string{
			"cd /home/david/code/de-novo && doppler run --project lenovo_server --config dev -- bash -lc 'DENOVO_SYMPHONY_DRILL_ALLOW_LINEAR_WRITES=1 go run ./cmd/symphony-linear-drill --workflow denovo/WORKFLOW.md --output build/symphony/handoff-001-denovo-proof.json --duration 30m --interval 30s --allow-linear-writes'",
		},
		ArtifactCollection: []string{
			"cd /home/david/stacks/symphony && doppler run --project lenovo_server --config dev -- go run ./drills --collect --events \"$DRILL_ARTIFACT\" --collect-output \"$DRILL_ARTIFACT\" --run-id \"$DRILL_RUN_ID\" --linear-token-env LINEAR_API_KEY --linear-parent \"$PARENT_LINEAR_ID\" --linear-child \"$CHILD_LINEAR_ID\" --slack-token-env SLACK_BOT_TOKEN --slack-channel " + inputs.BridgeChannel + " --github-pr \"$DRILL_PR_URL\" --github-linear-id \"$CHILD_LINEAR_ID\" --since \"$DRILL_STARTED_AT\" --until \"$DRILL_FINISHED_AT\"",
			"cd /home/david/stacks/symphony && go run ./drills --events \"$DRILL_ARTIFACT\" --format json > \"$DRILL_REPORT\" && go run ./drills --events \"$DRILL_ARTIFACT\"",
		},
		Rollback: []string{
			"systemctl --user status " + inputs.WatcherUnit + " --no-pager -l",
			"cd /home/david/code/de-novo && gh pr close \"$DRILL_PR_URL\" --comment \"Closing failed/aborted HAD-667 drill PR.\"",
			"cd /home/david/code/de-novo && git push origin --delete \"$DRILL_BRANCH\"",
		},
	}
}

func WriteDrillPlan(path string, packet DrillPlanPacket) error {
	body, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return os.WriteFile(path, body, 0o644)
}

func PrintDrillPlan(w io.Writer, packet DrillPlanPacket) {
	fmt.Fprintf(w, "%s %s %s\n", strings.ToUpper(packet.Status), packet.Scenario, packet.RunID)
	for _, gate := range packet.Gates {
		if gate.Detail == "" {
			fmt.Fprintf(w, "%s %s\n", strings.ToUpper(gate.Status), gate.Name)
			continue
		}
		fmt.Fprintf(w, "%s %s: %s\n", strings.ToUpper(gate.Status), gate.Name, gate.Detail)
	}
	if packet.NextRequiredAction != "" {
		fmt.Fprintf(w, "next_required_action: %s\n", packet.NextRequiredAction)
	}
}

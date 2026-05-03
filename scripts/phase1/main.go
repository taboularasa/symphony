package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/taboularasa/symphony/internal/linear"
	"github.com/taboularasa/symphony/internal/phase1"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError("missing subcommand")
	}
	switch args[0] {
	case "labels":
		return runLabels(args[1:])
	case "backfill":
		return runBackfill(args[1:])
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return nil
	default:
		return usageError(fmt.Sprintf("unknown subcommand %q", args[0]))
	}
}

func runLabels(args []string) error {
	fs := flag.NewFlagSet("labels", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	team := fs.String("team", "Hadto", "Linear team key, name, or UUID")
	endpoint := fs.String("endpoint", linear.DefaultEndpoint, "Linear GraphQL endpoint")
	tokenEnv := fs.String("token-env", "LINEAR_API_KEY", "environment variable containing the Linear API token")
	apply := fs.Bool("apply", false, "create missing labels; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return err
	}

	token := strings.TrimSpace(os.Getenv(*tokenEnv))
	if token == "" {
		return fmt.Errorf("%s is not set", *tokenEnv)
	}
	client, err := linear.NewClient(*endpoint, token)
	if err != nil {
		return err
	}
	provisioner := phase1.LabelProvisioner{Client: client}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var plan phase1.LabelPlan
	if *apply {
		plan, err = provisioner.Apply(ctx, *team)
	} else {
		plan, err = provisioner.Plan(ctx, *team)
	}
	if err != nil {
		return err
	}
	output := struct {
		Mode string `json:"mode"`
		phase1.LabelPlan
	}{
		Mode:      modeName(*apply),
		LabelPlan: plan,
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

func runBackfill(args []string) error {
	fs := flag.NewFlagSet("backfill", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	team := fs.String("team", "Hadto", "Linear team key, name, or UUID")
	endpoint := fs.String("endpoint", linear.DefaultEndpoint, "Linear GraphQL endpoint")
	tokenEnv := fs.String("token-env", "LINEAR_API_KEY", "environment variable containing the Linear API token")
	policyPath := fs.String("policy", "scripts/phase1/backfill_policy.example.json", "JSON policy file")
	csvPath := fs.String("csv", "", "CSV audit output path; default is backfill_<timestamp>.csv")
	planJSONPath := fs.String("plan-json", "", "existing dry-run JSON plan to apply without re-listing issues")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall timeout for Linear list/apply requests")
	apply := fs.Bool("apply", false, "append missing owner labels; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if strings.TrimSpace(*planJSONPath) != "" && !*apply {
		return errors.New("plan-json requires --apply")
	}
	policy, err := phase1.LoadBackfillPolicy(*policyPath)
	if err != nil {
		return err
	}
	policyHash := fileSHA256(*policyPath)
	token := strings.TrimSpace(os.Getenv(*tokenEnv))
	if token == "" {
		return fmt.Errorf("%s is not set", *tokenEnv)
	}
	client, err := linear.NewClient(*endpoint, token)
	if err != nil {
		return err
	}
	backfiller := phase1.Backfiller{Client: client}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	var decisions []phase1.BackfillDecision
	if strings.TrimSpace(*planJSONPath) != "" {
		decisions, err = readBackfillPlan(*planJSONPath, policyHash)
		if err != nil {
			return err
		}
	} else {
		issues, err := backfiller.ListIssues(ctx, *team)
		if err != nil {
			return err
		}
		decisions = phase1.PlanBackfill(policy, issues)
	}
	if *apply {
		ownerLabelIDs, err := backfiller.ResolveOwnerLabelIDs(ctx, *team)
		if err != nil {
			return err
		}
		decisions, err = backfiller.ApplyDecisions(ctx, decisions, ownerLabelIDs)
		if err != nil {
			return err
		}
	}

	outputPath := *csvPath
	if outputPath == "" {
		outputPath = defaultBackfillCSVPath()
	}
	if err := writeBackfillCSVFile(outputPath, decisions); err != nil {
		return err
	}
	output := newBackfillOutput(modeName(*apply), policyHash, outputPath, strings.TrimSpace(*planJSONPath), decisions)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

type backfillCommandOutput struct {
	Mode          string                    `json:"mode"`
	PolicyHash    string                    `json:"policy_hash"`
	CSVPath       string                    `json:"csv_path"`
	PlanJSONPath  string                    `json:"plan_json_path,omitempty"`
	IssueCount    int                       `json:"issue_count"`
	ApplyCount    int                       `json:"apply_count"`
	SkipCount     int                       `json:"skip_count"`
	ConflictCount int                       `json:"conflict_count"`
	Decisions     []phase1.BackfillDecision `json:"decisions"`
}

func newBackfillOutput(mode, policyHash, csvPath, planJSONPath string, decisions []phase1.BackfillDecision) backfillCommandOutput {
	output := backfillCommandOutput{
		Mode:         mode,
		PolicyHash:   policyHash,
		CSVPath:      csvPath,
		PlanJSONPath: planJSONPath,
		IssueCount:   len(decisions),
		Decisions:    decisions,
	}
	for _, decision := range decisions {
		switch decision.Action {
		case "apply", "applied":
			output.ApplyCount++
		case "skip":
			output.SkipCount++
		case "conflict":
			output.ConflictCount++
		}
	}
	return output
}

func readBackfillPlan(path, expectedPolicyHash string) ([]phase1.BackfillDecision, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open plan json: %w", err)
	}
	defer file.Close()
	var plan backfillCommandOutput
	if err := json.NewDecoder(file).Decode(&plan); err != nil {
		return nil, fmt.Errorf("decode plan json: %w", err)
	}
	if strings.TrimSpace(plan.PolicyHash) == "" {
		return nil, errors.New("plan json missing policy_hash")
	}
	if plan.PolicyHash != expectedPolicyHash {
		return nil, fmt.Errorf("plan json policy hash %s does not match current policy hash %s", plan.PolicyHash, expectedPolicyHash)
	}
	if len(plan.Decisions) == 0 && plan.IssueCount > 0 {
		return nil, errors.New("plan json missing decisions")
	}
	return plan.Decisions, nil
}

func modeName(apply bool) string {
	if apply {
		return "apply"
	}
	return "dry-run"
}

func defaultBackfillCSVPath() string {
	return fmt.Sprintf("backfill_%s.csv", time.Now().UTC().Format("20060102T150405Z"))
}

func writeBackfillCSVFile(path string, decisions []phase1.BackfillDecision) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return fmt.Errorf("create csv directory: %w", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create csv: %w", err)
	}
	defer file.Close()
	if err := phase1.WriteBackfillCSV(file, decisions); err != nil {
		return fmt.Errorf("write csv: %w", err)
	}
	return nil
}

func fileSHA256(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "unavailable"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func usageError(message string) error {
	printUsage(os.Stderr)
	return errors.New(message)
}

func printUsage(out *os.File) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  go run ./scripts/phase1 labels [--team Hadto] [--token-env LINEAR_API_KEY] [--apply]")
	fmt.Fprintln(out, "  go run ./scripts/phase1 backfill [--team Hadto] [--policy scripts/phase1/backfill_policy.example.json] [--csv backfill.csv] [--plan-json dry-run.json] [--timeout 2m] [--apply]")
}

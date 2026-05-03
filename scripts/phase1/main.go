package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
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

func modeName(apply bool) string {
	if apply {
		return "apply"
	}
	return "dry-run"
}

func usageError(message string) error {
	printUsage(os.Stderr)
	return errors.New(message)
}

func printUsage(out *os.File) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  go run ./scripts/phase1 labels [--team Hadto] [--token-env LINEAR_API_KEY] [--apply]")
}

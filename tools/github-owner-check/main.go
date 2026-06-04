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

	"github.com/taboularasa/symphony/internal/githubbackstop"
	"github.com/taboularasa/symphony/internal/linear"
)

const defaultPolicyPath = "config/github-owner-backstop.yaml"

type output struct {
	githubbackstop.Decision
	LinearOwnerConflict string   `json:"linear_owner_conflict,omitempty"`
	LinearOwnerLabels   []string `json:"linear_owner_labels,omitempty"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("github-owner-check", flag.ContinueOnError)
	policyPath := fs.String("policy", defaultPolicyPath, "GitHub owner backstop policy path")
	repository := fs.String("repository", "", "GitHub repository full name, owner/repo")
	branch := fs.String("branch", "", "PR branch name")
	headSHA := fs.String("head-sha", "", "PR head SHA")
	prBody := fs.String("pr-body", "", "PR body text")
	prBodyFile := fs.String("pr-body-file", "", "file containing PR body text")
	linearIssue := fs.String("linear-issue", "", "Linear issue key override")
	ownerLabel := fs.String("owner-label", "", "owner label override; if omitted, resolve from Linear when token is present")
	prAuthorLogin := fs.String("pr-author-login", "", "GitHub PR author login")
	prAuthorType := fs.String("pr-author-type", "", "GitHub PR author type, for example Bot or User")
	eventSenderLogin := fs.String("event-sender-login", "", "GitHub event sender login")
	eventSenderType := fs.String("event-sender-type", "", "GitHub event sender type, for example Bot or User")
	actorLogin := fs.String("actor-login", "", "GitHub actor login override")
	actorType := fs.String("actor-type", "", "GitHub actor type override, for example Bot or User")
	actorAppID := fs.String("actor-app-id", "", "GitHub App ID when available")
	actorRepoAdmin := fs.Bool("actor-repo-admin", false, "treat actor as repository admin for human bypass checks")
	linearEndpoint := fs.String("linear-endpoint", linear.DefaultEndpoint, "Linear GraphQL endpoint")
	linearTokenEnv := fs.String("linear-token-env", "LINEAR_API_KEY", "env var containing Linear token; empty disables lookup")
	timeout := fs.Duration("timeout", 30*time.Second, "Linear lookup timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *timeout <= 0 {
		return errors.New("timeout must be positive")
	}

	body := *prBody
	if strings.TrimSpace(*prBodyFile) != "" {
		data, err := os.ReadFile(*prBodyFile)
		if err != nil {
			return fmt.Errorf("read pr body file: %w", err)
		}
		body = string(data)
	}

	policy, err := githubbackstop.LoadPolicy(*policyPath)
	if err != nil {
		return err
	}
	input := githubbackstop.CheckInput{
		Repository:     *repository,
		Branch:         *branch,
		PRBody:         body,
		HeadSHA:        *headSHA,
		LinearIssueKey: *linearIssue,
		OwnerLabel:     *ownerLabel,
		Actor: githubbackstop.ActorIdentity{
			Login:     *actorLogin,
			Type:      *actorType,
			AppID:     *actorAppID,
			RepoAdmin: *actorRepoAdmin,
		},
	}
	if strings.TrimSpace(input.Actor.Login) == "" {
		input.Actor.Login = firstNonEmpty(*eventSenderLogin, *prAuthorLogin)
		input.Actor.Type = firstNonEmpty(*eventSenderType, *prAuthorType)
	}
	var ownerResolution githubbackstop.OwnerResolution
	if strings.TrimSpace(input.OwnerLabel) == "" && strings.TrimSpace(*linearTokenEnv) != "" {
		issueKey := githubbackstop.FirstLinearIssueKey(input.LinearIssueKey, input.Branch, input.PRBody)
		if issueKey != "" {
			token := strings.TrimSpace(os.Getenv(*linearTokenEnv))
			if token != "" {
				client, err := linear.NewClient(*linearEndpoint, token)
				if err != nil {
					return err
				}
				ctx, cancel := context.WithTimeout(context.Background(), *timeout)
				defer cancel()
				ownerResolution, err = (githubbackstop.OwnerResolver{Client: client}).ResolveOwnerLabel(ctx, issueKey)
				if err != nil {
					return err
				}
				input.LinearIssueKey = ownerResolution.IssueKey
				input.OwnerLabel = ownerResolution.OwnerLabel
			}
		}
	}

	decision := githubbackstop.Evaluate(policy, input)
	result := output{
		Decision:            decision,
		LinearOwnerConflict: ownerResolution.ConflictReason,
		LinearOwnerLabels:   ownerResolution.OwnerLabels,
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return err
	}
	if decision.Status != githubbackstop.DecisionAllow {
		return fmt.Errorf("github owner check denied: %s", decision.ReasonCode)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

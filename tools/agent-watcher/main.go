package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/taboularasa/symphony/internal/agentwatcher"
	"github.com/taboularasa/symphony/internal/linear"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("agent-watcher", flag.ContinueOnError)
	configPath := fs.String("config", "tools/agent-watcher/watcher.example.yaml", "watcher YAML config")
	listen := fs.String("listen", "127.0.0.1:18080", "HTTP listen address")
	mode := fs.String("mode", "webhook", "ingress mode: webhook, poll, or both")
	linearEndpoint := fs.String("linear-endpoint", linear.DefaultEndpoint, "Linear GraphQL endpoint")
	linearTokenEnv := fs.String("linear-token-env", "LINEAR_API_KEY", "env var containing Linear token for polling mode")
	pollInterval := fs.Duration("poll-interval", 30*time.Second, "polling fallback interval")
	pollPageSize := fs.Int("poll-page-size", 50, "polling fallback GraphQL page size")
	dedupeFile := fs.String("dedupe-file", "", "optional JSON file for delivery dedupe across restarts")
	alertStdout := fs.Bool("alert-stdout", true, "emit alert JSON to stdout")
	slackTokenEnv := fs.String("slack-token-env", "", "env var containing Slack bot token for bridge alerts")
	linearCommentTokenEnv := fs.String("linear-comment-token-env", "", "env var containing Linear token for alert comments")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *mode != "webhook" && *mode != "poll" && *mode != "both" {
		return fmt.Errorf("mode must be webhook, poll, or both")
	}
	cfg, err := agentwatcher.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	secret := os.Getenv(cfg.Webhook.SigningSecretEnv)
	detector := agentwatcher.NewDetector(cfg)
	dedupe, err := agentwatcher.NewPersistentDedupeStore(*dedupeFile, 24*time.Hour, time.Now())
	if err != nil {
		return fmt.Errorf("load dedupe store: %w", err)
	}
	sink, err := buildAlertSink(alertSinkOptions{
		Config:                cfg,
		Stdout:                *alertStdout,
		SlackTokenEnv:         *slackTokenEnv,
		LinearCommentTokenEnv: *linearCommentTokenEnv,
		LinearEndpoint:        *linearEndpoint,
	})
	if err != nil {
		return err
	}
	handler := agentwatcher.Handler{
		Config:   cfg,
		Detector: detector,
		Sink:     sink,
		Secrets:  map[string]string{cfg.Webhook.SigningSecretEnv: secret},
		Dedupe:   dedupe,
		Async:    true,
	}
	mux := http.NewServeMux()
	loadedAt := time.Now().UTC()
	mux.HandleFunc("/healthz", healthHandler(*mode, *configPath, loadedAt))
	if *mode == "webhook" || *mode == "both" {
		mux.Handle("/webhooks/linear", handler)
	}
	if *mode == "poll" || *mode == "both" {
		token := strings.TrimSpace(os.Getenv(*linearTokenEnv))
		if token == "" {
			return fmt.Errorf("%s is not set", *linearTokenEnv)
		}
		client, err := linear.NewClient(*linearEndpoint, token)
		if err != nil {
			return err
		}
		go pollLoop(context.Background(), agentwatcher.Poller{
			Client:   client,
			PageSize: *pollPageSize,
		}, detector, dedupe, sink, *pollInterval)
	}
	log.Printf("agent watcher listening on %s in %s mode", *listen, *mode)
	return http.ListenAndServe(*listen, mux)
}

func healthHandler(mode, configPath string, loadedAt time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "ok",
			"mode":      mode,
			"config":    configPath,
			"loaded_at": loadedAt.Format(time.RFC3339),
		})
	}
}

func pollLoop(ctx context.Context, poller agentwatcher.Poller, detector *agentwatcher.Detector, dedupe *agentwatcher.DedupeStore, sink agentwatcher.AlertSink, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	since := time.Now().UTC().Add(-interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		events, err := poller.Fetch(ctx, since)
		if err != nil {
			log.Printf("linear polling failed: %v", err)
		}
		for _, event := range events {
			if event.DeliveryID == "" {
				event.DeliveryID = fmt.Sprintf("poll:%s:%s:%s", event.Identifier, event.Action, event.CreatedAt.UTC().Format(time.RFC3339Nano))
			}
			if dedupe != nil && !dedupe.Seen(event.DeliveryID, time.Now()) {
				continue
			}
			for _, alert := range detector.Evaluate(event) {
				_ = sink.Send(alert)
			}
			if event.CreatedAt.After(since) {
				since = event.CreatedAt.Add(time.Nanosecond)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type stdoutSink struct{}

func (stdoutSink) Send(alert agentwatcher.Alert) error {
	return json.NewEncoder(os.Stdout).Encode(alert)
}

type alertSinkOptions struct {
	Config                agentwatcher.Config
	Stdout                bool
	SlackTokenEnv         string
	LinearCommentTokenEnv string
	LinearEndpoint        string
}

func buildAlertSink(opts alertSinkOptions) (agentwatcher.AlertSink, error) {
	var sinks []agentwatcher.AlertSink
	if opts.Stdout {
		sinks = append(sinks, stdoutSink{})
	}
	if envName := strings.TrimSpace(opts.SlackTokenEnv); envName != "" {
		token := strings.TrimSpace(os.Getenv(envName))
		if token == "" {
			return nil, fmt.Errorf("%s is not set", envName)
		}
		sinks = append(sinks, slackAlertSink{sink: agentwatcher.SlackSink{
			Token:   token,
			Channel: opts.Config.Alerts.SlackChannelID,
		}})
	}
	if envName := strings.TrimSpace(opts.LinearCommentTokenEnv); envName != "" {
		token := strings.TrimSpace(os.Getenv(envName))
		if token == "" {
			return nil, fmt.Errorf("%s is not set", envName)
		}
		client, err := linear.NewClient(opts.LinearEndpoint, token)
		if err != nil {
			return nil, err
		}
		sinks = append(sinks, linearCommentAlertSink{sink: agentwatcher.LinearCommentSink{
			Client:       client,
			HumanMention: opts.Config.Alerts.HumanMention,
		}})
	}
	if len(sinks) == 0 {
		return nil, errors.New("at least one alert sink must be enabled")
	}
	if len(sinks) == 1 {
		return sinks[0], nil
	}
	return multiAlertSink{sinks: sinks}, nil
}

type slackAlertSink struct {
	sink agentwatcher.SlackSink
}

func (s slackAlertSink) Send(alert agentwatcher.Alert) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.sink.Send(ctx, alert)
}

type linearCommentAlertSink struct {
	sink agentwatcher.LinearCommentSink
}

func (s linearCommentAlertSink) Send(alert agentwatcher.Alert) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.sink.Send(ctx, alert)
}

type multiAlertSink struct {
	sinks []agentwatcher.AlertSink
}

func (s multiAlertSink) Send(alert agentwatcher.Alert) error {
	var errs []error
	for _, sink := range s.sinks {
		if err := sink.Send(alert); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

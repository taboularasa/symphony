package agentwatcher

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ForbiddenFor []ForbiddenRule `yaml:"forbidden_for"`
	Actors       []ActorConfig   `yaml:"actors"`
	WatcherActor string          `yaml:"watcher_actor"`
	RateLimits   RateLimits      `yaml:"rate_limits"`
	Alerts       AlertConfig     `yaml:"alerts"`
	Webhook      WebhookConfig   `yaml:"webhook"`
}

type ForbiddenRule struct {
	Project string   `yaml:"project"`
	Actors  []string `yaml:"actors"`
}

type ActorConfig struct {
	Key           string   `yaml:"key"`
	LinearUserIDs []string `yaml:"linear_user_ids"`
	Emails        []string `yaml:"emails"`
	AppIDs        []string `yaml:"app_ids"`
	BotIDs        []string `yaml:"bot_ids"`
}

type RateLimits struct {
	ActorWritesPerMinute int `yaml:"actor_writes_per_minute"`
}

type AlertConfig struct {
	SlackChannelID string `yaml:"slack_channel_id"`
	HumanMention   string `yaml:"human_mention"`
}

type WebhookConfig struct {
	SigningSecretEnv          string `yaml:"signing_secret_env"`
	TimestampToleranceSeconds int    `yaml:"timestamp_tolerance_seconds"`
}

func LoadConfig(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open watcher config: %w", err)
	}
	defer file.Close()
	return DecodeConfig(file)
}

func DecodeConfig(reader io.Reader) (Config, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return Config{}, fmt.Errorf("read watcher config: %w", err)
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode watcher config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	actorKeys := map[string]struct{}{}
	for _, actor := range c.Actors {
		key := normalizeKey(actor.Key)
		if key == "" {
			return errors.New("actor key is required")
		}
		if _, ok := actorKeys[key]; ok {
			return fmt.Errorf("duplicate actor %q", actor.Key)
		}
		actorKeys[key] = struct{}{}
		if len(actor.LinearUserIDs) == 0 && len(actor.Emails) == 0 && len(actor.AppIDs) == 0 && len(actor.BotIDs) == 0 {
			return fmt.Errorf("actor %q needs at least one identity", actor.Key)
		}
	}
	seenRules := map[string]struct{}{}
	for _, rule := range c.ForbiddenFor {
		project := normalizeKey(rule.Project)
		if project == "" {
			return errors.New("forbidden_for project is required")
		}
		for _, actor := range rule.Actors {
			actorKey := normalizeKey(actor)
			if _, ok := actorKeys[actorKey]; !ok {
				return fmt.Errorf("forbidden_for project %q references unknown actor %q", rule.Project, actor)
			}
			key := project + "\x00" + actorKey
			if _, ok := seenRules[key]; ok {
				return fmt.Errorf("duplicate forbidden_for rule for project %q actor %q", rule.Project, actor)
			}
			seenRules[key] = struct{}{}
		}
	}
	if c.RateLimits.ActorWritesPerMinute <= 0 {
		return errors.New("rate_limits.actor_writes_per_minute must be positive")
	}
	if strings.TrimSpace(c.Alerts.SlackChannelID) == "" && strings.TrimSpace(c.Alerts.HumanMention) == "" {
		return errors.New("alerts need slack_channel_id or human_mention")
	}
	if strings.TrimSpace(c.Webhook.SigningSecretEnv) == "" {
		return errors.New("webhook.signing_secret_env is required")
	}
	if c.Webhook.TimestampToleranceSeconds <= 0 {
		return errors.New("webhook.timestamp_tolerance_seconds must be positive")
	}
	return nil
}

func (c Config) ActorForEvent(event Event) (ActorConfig, bool) {
	for _, actor := range c.Actors {
		if actor.matches(event) {
			return actor, true
		}
	}
	return ActorConfig{}, false
}

func (c Config) IsForbidden(project, actorKey string) bool {
	project = normalizeKey(project)
	actorKey = normalizeKey(actorKey)
	for _, rule := range c.ForbiddenFor {
		if normalizeKey(rule.Project) != project {
			continue
		}
		for _, actor := range rule.Actors {
			if normalizeKey(actor) == actorKey {
				return true
			}
		}
	}
	return false
}

func (c Config) TimestampTolerance() time.Duration {
	return time.Duration(c.Webhook.TimestampToleranceSeconds) * time.Second
}

func (a ActorConfig) matches(event Event) bool {
	if normalizeKey(a.Key) != "" && normalizeKey(event.ActorKey) == normalizeKey(a.Key) {
		return true
	}
	for _, id := range a.LinearUserIDs {
		if strings.EqualFold(strings.TrimSpace(id), strings.TrimSpace(event.ActorID)) && strings.TrimSpace(id) != "" {
			return true
		}
	}
	for _, email := range a.Emails {
		if strings.EqualFold(strings.TrimSpace(email), strings.TrimSpace(event.ActorEmail)) && strings.TrimSpace(email) != "" {
			return true
		}
	}
	for _, appID := range a.AppIDs {
		if strings.EqualFold(strings.TrimSpace(appID), strings.TrimSpace(event.AppID)) && strings.TrimSpace(appID) != "" {
			return true
		}
	}
	for _, botID := range a.BotIDs {
		if strings.EqualFold(strings.TrimSpace(botID), strings.TrimSpace(event.BotID)) && strings.TrimSpace(botID) != "" {
			return true
		}
	}
	return false
}

func rejectDuplicateKeys(data []byte) error {
	var root yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&root); err != nil {
		return fmt.Errorf("decode watcher config: %w", err)
	}
	return rejectDuplicateKeysNode(&root)
}

func rejectDuplicateKeysNode(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.MappingNode {
		seen := map[string]struct{}{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i].Value
			if _, ok := seen[key]; ok {
				return fmt.Errorf("duplicate yaml key %q", key)
			}
			seen[key] = struct{}{}
			if err := rejectDuplicateKeysNode(node.Content[i+1]); err != nil {
				return err
			}
		}
		return nil
	}
	for _, child := range node.Content {
		if err := rejectDuplicateKeysNode(child); err != nil {
			return err
		}
	}
	return nil
}

func normalizeKey(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

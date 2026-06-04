package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/taboularasa/symphony/internal/phase1"
	"gopkg.in/yaml.v3"
)

const DefaultLinearEndpoint = "https://api.linear.app/graphql"

var envRefPattern = regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]*$`)

const (
	ClaimTargetAssignee = "assignee"
	ClaimTargetDelegate = "delegate"
)

type Definition struct {
	Config         map[string]any
	Prompt         string
	PromptTemplate string
	Settings       Settings
}

type Settings struct {
	Tracker   TrackerConfig   `yaml:"tracker"`
	Workspace WorkspaceConfig `yaml:"workspace"`
	Hooks     HooksConfig     `yaml:"hooks"`
}

type TrackerConfig struct {
	Kind                       string         `yaml:"kind"`
	Endpoint                   string         `yaml:"endpoint"`
	APIKey                     string         `yaml:"api_key"`
	ProjectSlug                string         `yaml:"project_slug"`
	OwnerLabel                 OptionalString `yaml:"owner_label"`
	ClaimAssignee              OptionalString `yaml:"claim_assignee"`
	ClaimTarget                string         `yaml:"claim_target"`
	RequireClaimBeforeDispatch bool           `yaml:"require_claim_before_dispatch"`
	ActiveStates               []string       `yaml:"active_states"`
	TerminalStates             []string       `yaml:"terminal_states"`
}

type LinearConfig struct {
	Endpoint                   string
	APIKey                     string
	ProjectSlug                string
	OwnerLabel                 string
	ClaimAssignee              string
	ClaimAssigneeID            string
	ClaimTarget                string
	RequireClaimBeforeDispatch bool
	ActiveStates               []string
	TerminalStates             []string
}

type WorkspaceConfig struct {
	Root string `yaml:"root"`
}

type HooksConfig struct {
	BeforeRun      string `yaml:"before_run"`
	TimeoutMS      int    `yaml:"timeout_ms"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

type ClaimAssigneeIdentity struct {
	ID     string
	Name   string
	Email  string
	Active bool
}

type ClaimAssigneeResolver interface {
	ResolveClaimAssignee(ctx context.Context, ref string) (ClaimAssigneeIdentity, error)
}

type ClaimAssigneeResolverFunc func(ctx context.Context, ref string) (ClaimAssigneeIdentity, error)

func (f ClaimAssigneeResolverFunc) ResolveClaimAssignee(ctx context.Context, ref string) (ClaimAssigneeIdentity, error) {
	return f(ctx, ref)
}

type OptionalString struct {
	Set   bool
	Null  bool
	Value string
}

func (s OptionalString) Enabled() bool {
	return s.Set && !s.Null && strings.TrimSpace(s.Value) != ""
}

func (s OptionalString) String() string {
	if !s.Enabled() {
		return ""
	}
	return s.Value
}

func (s *OptionalString) UnmarshalYAML(node *yaml.Node) error {
	s.Set = true
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		s.Null = true
		s.Value = ""
		return nil
	}
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("expected string or null")
	}
	s.Value = node.Value
	return nil
}

func Load(path string) (Definition, error) {
	file, err := os.Open(path)
	if err != nil {
		return Definition{}, fmt.Errorf("open workflow: %w", err)
	}
	defer file.Close()
	return Decode(file)
}

func Decode(reader io.Reader) (Definition, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return Definition{}, fmt.Errorf("read workflow: %w", err)
	}
	return Parse(data)
}

func Parse(data []byte) (Definition, error) {
	frontMatter, prompt := splitFrontMatter(string(data))
	if err := rejectDuplicateKeys([]byte(frontMatter)); err != nil {
		return Definition{}, err
	}

	var rawConfig map[string]any
	if strings.TrimSpace(frontMatter) == "" {
		rawConfig = map[string]any{}
	} else if err := yaml.Unmarshal([]byte(frontMatter), &rawConfig); err != nil {
		return Definition{}, fmt.Errorf("decode workflow front matter: %w", err)
	} else if rawConfig == nil {
		rawConfig = map[string]any{}
	}

	settings, err := DecodeSettings([]byte(frontMatter))
	if err != nil {
		return Definition{}, err
	}
	prompt = strings.TrimSpace(prompt)
	return Definition{
		Config:         rawConfig,
		Prompt:         prompt,
		PromptTemplate: prompt,
		Settings:       settings,
	}, nil
}

func DecodeSettings(frontMatter []byte) (Settings, error) {
	settings := defaultSettings()
	if strings.TrimSpace(string(frontMatter)) == "" {
		return settings, nil
	}
	decoder := yaml.NewDecoder(bytes.NewReader(frontMatter))
	decoder.KnownFields(false)
	if err := decoder.Decode(&settings); err != nil {
		return Settings{}, fmt.Errorf("decode workflow settings: %w", err)
	}
	if err := settings.Validate(); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func (s Settings) Validate() error {
	if err := s.Tracker.Validate(); err != nil {
		return err
	}
	return s.Hooks.Validate()
}

func (t TrackerConfig) Validate() error {
	if strings.TrimSpace(t.Kind) != "" && !strings.EqualFold(strings.TrimSpace(t.Kind), "linear") {
		return fmt.Errorf("tracker.kind unsupported %q", t.Kind)
	}
	if err := validateOptionalOwnerLabel(t.OwnerLabel); err != nil {
		return err
	}
	if err := validateOptionalClaimAssignee(t.ClaimAssignee); err != nil {
		return err
	}
	if err := validateClaimTarget(t.ClaimTarget); err != nil {
		return err
	}
	if t.RequireClaimBeforeDispatch && !t.ClaimAssignee.Enabled() {
		return errors.New("tracker.require_claim_before_dispatch requires tracker.claim_assignee")
	}
	return nil
}

func (t TrackerConfig) ValidateOwnerClaimContract(ownerLabel, claimAssignee string, requireClaim bool) error {
	if err := t.Validate(); err != nil {
		return err
	}
	ownerLabel = normalizeOwnerLabel(ownerLabel)
	if ownerLabel != "" && t.NormalizedOwnerLabel() != ownerLabel {
		return fmt.Errorf("tracker.owner_label must be %q", ownerLabel)
	}
	claimAssignee = strings.TrimSpace(claimAssignee)
	if claimAssignee != "" && t.NormalizedClaimAssignee() != claimAssignee {
		return fmt.Errorf("tracker.claim_assignee must be %q", claimAssignee)
	}
	if requireClaim && !t.RequireClaimBeforeDispatch {
		return errors.New("tracker.require_claim_before_dispatch must be true")
	}
	return nil
}

func (t TrackerConfig) ResolveLinearConfig(ctx context.Context, resolver ClaimAssigneeResolver) (LinearConfig, error) {
	if err := t.Validate(); err != nil {
		return LinearConfig{}, err
	}
	apiKey := t.ResolvedAPIKey()
	if apiKey == "" {
		return LinearConfig{}, errors.New("tracker.api_key is required")
	}
	projectSlug := strings.TrimSpace(t.ProjectSlug)
	if projectSlug == "" {
		return LinearConfig{}, errors.New("tracker.project_slug is required")
	}

	endpoint := strings.TrimSpace(t.Endpoint)
	if endpoint == "" {
		endpoint = DefaultLinearEndpoint
	}

	resolved := LinearConfig{
		Endpoint:                   endpoint,
		APIKey:                     apiKey,
		ProjectSlug:                projectSlug,
		OwnerLabel:                 t.NormalizedOwnerLabel(),
		ClaimAssignee:              t.NormalizedClaimAssignee(),
		ClaimTarget:                t.NormalizedClaimTarget(),
		RequireClaimBeforeDispatch: t.RequireClaimBeforeDispatch,
		ActiveStates:               append([]string(nil), t.ActiveStates...),
		TerminalStates:             append([]string(nil), t.TerminalStates...),
	}

	if !t.RequireClaimBeforeDispatch {
		return resolved, nil
	}
	if resolver == nil {
		return LinearConfig{}, errors.New("tracker.claim_assignee resolver is required")
	}
	identity, err := resolver.ResolveClaimAssignee(ctx, resolved.ClaimAssignee)
	if err != nil {
		return LinearConfig{}, fmt.Errorf("resolve tracker.claim_assignee %q: %w", resolved.ClaimAssignee, err)
	}
	if strings.TrimSpace(identity.ID) == "" {
		return LinearConfig{}, fmt.Errorf("tracker.claim_assignee %q did not resolve to a Linear user", resolved.ClaimAssignee)
	}
	if !identity.Active {
		return LinearConfig{}, fmt.Errorf("tracker.claim_assignee %q resolved to an inactive Linear user", resolved.ClaimAssignee)
	}
	resolved.ClaimAssigneeID = strings.TrimSpace(identity.ID)
	return resolved, nil
}

func (h HooksConfig) Validate() error {
	if h.TimeoutMS < 0 {
		return errors.New("hooks.timeout_ms must not be negative")
	}
	if h.TimeoutSeconds < 0 {
		return errors.New("hooks.timeout_seconds must not be negative")
	}
	return nil
}

func (h HooksConfig) TimeoutDuration() time.Duration {
	if h.TimeoutMS > 0 {
		return time.Duration(h.TimeoutMS) * time.Millisecond
	}
	if h.TimeoutSeconds > 0 {
		return time.Duration(h.TimeoutSeconds) * time.Second
	}
	return 60 * time.Second
}

func (t TrackerConfig) ResolvedAPIKey() string {
	return resolveEnvString(t.APIKey, os.Getenv("LINEAR_API_KEY"))
}

func (t TrackerConfig) NormalizedOwnerLabel() string {
	if !t.OwnerLabel.Enabled() {
		return ""
	}
	return normalizeOwnerLabel(t.OwnerLabel.Value)
}

func (t TrackerConfig) NormalizedClaimAssignee() string {
	if !t.ClaimAssignee.Enabled() {
		return ""
	}
	return strings.TrimSpace(t.ClaimAssignee.Value)
}

func (t TrackerConfig) NormalizedClaimTarget() string {
	target := strings.ToLower(strings.TrimSpace(t.ClaimTarget))
	if target == "" {
		return ClaimTargetAssignee
	}
	return target
}

func defaultSettings() Settings {
	return Settings{
		Tracker: TrackerConfig{
			Endpoint:       DefaultLinearEndpoint,
			ActiveStates:   []string{"Todo", "In Progress"},
			TerminalStates: []string{"Closed", "Cancelled", "Canceled", "Duplicate", "Done"},
		},
	}
}

func splitFrontMatter(content string) (frontMatter string, prompt string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", content
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n")
		}
	}
	return strings.Join(lines[1:], "\n"), ""
}

func validateOptionalOwnerLabel(owner OptionalString) error {
	if !owner.Set || owner.Null {
		return nil
	}
	normalized := normalizeOwnerLabel(owner.Value)
	if normalized == "" {
		return errors.New("tracker.owner_label must not be blank")
	}
	if err := phase1.ValidateOwnerLabel(normalized); err != nil {
		return fmt.Errorf("tracker.owner_label: %w", err)
	}
	return nil
}

func validateOptionalClaimAssignee(assignee OptionalString) error {
	if !assignee.Set || assignee.Null {
		return nil
	}
	if strings.TrimSpace(assignee.Value) == "" {
		return errors.New("tracker.claim_assignee must not be blank")
	}
	return nil
}

func validateClaimTarget(target string) error {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "", ClaimTargetAssignee, ClaimTargetDelegate:
		return nil
	default:
		return fmt.Errorf("tracker.claim_target must be %q or %q", ClaimTargetAssignee, ClaimTargetDelegate)
	}
}

func normalizeOwnerLabel(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func resolveEnvString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return strings.TrimSpace(fallback)
	}
	if envRefPattern.MatchString(value) {
		if envValue := os.Getenv(strings.TrimPrefix(value, "$")); envValue != "" {
			return strings.TrimSpace(envValue)
		}
		return strings.TrimSpace(fallback)
	}
	return value
}

func rejectDuplicateKeys(data []byte) error {
	if strings.TrimSpace(string(data)) == "" {
		return nil
	}
	var root yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&root); err != nil {
		return fmt.Errorf("decode workflow front matter: %w", err)
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
				return fmt.Errorf("duplicate workflow yaml key %q", key)
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

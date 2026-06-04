package githubbackstop

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/taboularasa/symphony/internal/phase1"
	"gopkg.in/yaml.v3"
)

const PolicySchemaVersion = "hadto.symphony.github-owner-backstop.v1"

var (
	envNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	repoPattern    = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

type Policy struct {
	SchemaVersion      string        `yaml:"schema_version"`
	Owners             []OwnerPolicy `yaml:"owners"`
	SharedRepositories []string      `yaml:"shared_repositories"`
	HumanBypass        HumanBypass   `yaml:"human_bypass"`
}

type OwnerPolicy struct {
	OwnerLabel          string        `yaml:"owner_label"`
	AllowedRepositories []string      `yaml:"allowed_repositories"`
	ExpectedApps        []AppIdentity `yaml:"expected_apps"`
}

type AppIdentity struct {
	Slug     string `yaml:"slug"`
	Login    string `yaml:"login"`
	AppID    string `yaml:"app_id,omitempty"`
	AppIDEnv string `yaml:"app_id_env,omitempty"`
}

type HumanBypass struct {
	Mode          string   `yaml:"mode"`
	AllowedLogins []string `yaml:"allowed_logins"`
}

func LoadPolicy(path string) (Policy, error) {
	file, err := os.Open(path)
	if err != nil {
		return Policy{}, fmt.Errorf("open GitHub backstop policy: %w", err)
	}
	defer file.Close()
	return DecodePolicy(file)
}

func DecodePolicy(reader io.Reader) (Policy, error) {
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	var policy Policy
	if err := decoder.Decode(&policy); err != nil {
		return Policy{}, fmt.Errorf("decode GitHub backstop policy: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return Policy{}, err
	}
	return policy.Normalized(), nil
}

func (p Policy) Validate() error {
	var errs []string
	if strings.TrimSpace(p.SchemaVersion) != PolicySchemaVersion {
		errs = append(errs, "schema_version must be "+PolicySchemaVersion)
	}
	if len(p.Owners) == 0 {
		errs = append(errs, "owners must not be empty")
	}

	shared := map[string]bool{}
	for _, repo := range p.SharedRepositories {
		normalized, err := normalizeRepository(repo)
		if err != nil {
			errs = append(errs, "shared_repositories: "+err.Error())
			continue
		}
		shared[normalized] = true
	}

	seenOwners := map[string]bool{}
	repoOwners := map[string]string{}
	for i, owner := range p.Owners {
		prefix := fmt.Sprintf("owners[%d]", i)
		ownerLabel := normalizeOwnerLabel(owner.OwnerLabel)
		if ownerLabel == "" {
			errs = append(errs, prefix+".owner_label must not be blank")
		} else if err := phase1.ValidateOwnerLabel(ownerLabel); err != nil {
			errs = append(errs, prefix+"."+err.Error())
		} else if seenOwners[ownerLabel] {
			errs = append(errs, prefix+".owner_label duplicates "+ownerLabel)
		}
		seenOwners[ownerLabel] = true

		if len(owner.AllowedRepositories) == 0 {
			errs = append(errs, prefix+".allowed_repositories must not be empty")
		}
		seenRepos := map[string]bool{}
		for _, repo := range owner.AllowedRepositories {
			normalized, err := normalizeRepository(repo)
			if err != nil {
				errs = append(errs, prefix+".allowed_repositories: "+err.Error())
				continue
			}
			if seenRepos[normalized] {
				errs = append(errs, prefix+".allowed_repositories duplicates "+normalized)
			}
			seenRepos[normalized] = true
			if previous, ok := repoOwners[normalized]; ok && previous != ownerLabel && !shared[normalized] {
				errs = append(errs, prefix+".allowed_repositories "+normalized+" already belongs to "+previous)
			}
			repoOwners[normalized] = ownerLabel
		}

		if len(owner.ExpectedApps) == 0 {
			errs = append(errs, prefix+".expected_apps must not be empty")
		}
		for j, app := range owner.ExpectedApps {
			appPrefix := fmt.Sprintf("%s.expected_apps[%d]", prefix, j)
			errs = append(errs, validateAppIdentity(appPrefix, app)...)
		}
	}
	errs = append(errs, validateHumanBypass(p.HumanBypass)...)
	if len(errs) > 0 {
		return fmt.Errorf("invalid GitHub backstop policy: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (p Policy) Normalized() Policy {
	p.SchemaVersion = strings.TrimSpace(p.SchemaVersion)
	for i := range p.SharedRepositories {
		p.SharedRepositories[i], _ = normalizeRepository(p.SharedRepositories[i])
	}
	slices.Sort(p.SharedRepositories)
	for i := range p.Owners {
		p.Owners[i].OwnerLabel = normalizeOwnerLabel(p.Owners[i].OwnerLabel)
		for j := range p.Owners[i].AllowedRepositories {
			p.Owners[i].AllowedRepositories[j], _ = normalizeRepository(p.Owners[i].AllowedRepositories[j])
		}
		slices.Sort(p.Owners[i].AllowedRepositories)
		for j := range p.Owners[i].ExpectedApps {
			p.Owners[i].ExpectedApps[j].Slug = strings.TrimSpace(p.Owners[i].ExpectedApps[j].Slug)
			p.Owners[i].ExpectedApps[j].Login = strings.TrimSpace(p.Owners[i].ExpectedApps[j].Login)
			p.Owners[i].ExpectedApps[j].AppID = strings.TrimSpace(p.Owners[i].ExpectedApps[j].AppID)
			p.Owners[i].ExpectedApps[j].AppIDEnv = strings.TrimSpace(p.Owners[i].ExpectedApps[j].AppIDEnv)
		}
	}
	p.HumanBypass.Mode = strings.TrimSpace(p.HumanBypass.Mode)
	for i := range p.HumanBypass.AllowedLogins {
		p.HumanBypass.AllowedLogins[i] = strings.TrimSpace(p.HumanBypass.AllowedLogins[i])
	}
	slices.Sort(p.HumanBypass.AllowedLogins)
	return p
}

func (p Policy) OwnerForLabel(label string) (OwnerPolicy, bool) {
	label = normalizeOwnerLabel(label)
	for _, owner := range p.Owners {
		if normalizeOwnerLabel(owner.OwnerLabel) == label {
			return owner, true
		}
	}
	return OwnerPolicy{}, false
}

func validateAppIdentity(prefix string, app AppIdentity) []string {
	var errs []string
	if strings.TrimSpace(app.Slug) == "" {
		errs = append(errs, prefix+".slug must not be blank")
	}
	if strings.TrimSpace(app.Login) == "" {
		errs = append(errs, prefix+".login must not be blank")
	}
	appID := strings.TrimSpace(app.AppID)
	appIDEnv := strings.TrimSpace(app.AppIDEnv)
	if appID == "" && appIDEnv == "" {
		errs = append(errs, prefix+".app_id or app_id_env must be set")
	}
	if appID != "" {
		parsed, err := strconv.ParseInt(appID, 10, 64)
		if err != nil || parsed <= 0 {
			errs = append(errs, prefix+".app_id must be a positive integer")
		}
	}
	if appIDEnv != "" && !envNamePattern.MatchString(appIDEnv) {
		errs = append(errs, prefix+".app_id_env must be an uppercase env var name")
	}
	return errs
}

func validateHumanBypass(bypass HumanBypass) []string {
	mode := strings.TrimSpace(bypass.Mode)
	switch mode {
	case "", "disabled", "repo_admin_only":
	case "explicit_logins":
		if len(bypass.AllowedLogins) == 0 {
			return []string{"human_bypass.allowed_logins must not be empty when mode is explicit_logins"}
		}
	default:
		return []string{"human_bypass.mode must be disabled, repo_admin_only, or explicit_logins"}
	}
	var errs []string
	seen := map[string]bool{}
	for _, login := range bypass.AllowedLogins {
		trimmed := strings.TrimSpace(login)
		if trimmed == "" {
			errs = append(errs, "human_bypass.allowed_logins contains blank login")
			continue
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			errs = append(errs, "human_bypass.allowed_logins duplicates "+trimmed)
		}
		seen[key] = true
	}
	return errs
}

func normalizeOwnerLabel(label string) string {
	return strings.ToLower(strings.TrimSpace(label))
}

func normalizeRepository(repo string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(repo))
	if !repoPattern.MatchString(normalized) {
		return "", fmt.Errorf("repository %q must be owner/name", strings.TrimSpace(repo))
	}
	return normalized, nil
}

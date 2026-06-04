package githubbackstop

import (
	"strings"
	"testing"
)

func TestDecodePolicyAcceptsRepositoryPolicy(t *testing.T) {
	policy, err := LoadPolicy("../../config/github-owner-backstop.yaml")
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	if policy.SchemaVersion != PolicySchemaVersion {
		t.Fatalf("schema version = %q", policy.SchemaVersion)
	}
	hermes, ok := policy.OwnerForLabel(" OWNER:HERMES ")
	if !ok {
		t.Fatal("OwnerForLabel(owner:hermes) not found")
	}
	if got := strings.Join(hermes.AllowedRepositories, ","); got != "taboularasa/hermes-agent,taboularasa/phoneitin" {
		t.Fatalf("hermes repositories = %s", got)
	}
	if len(hermes.ExpectedApps) != 1 || hermes.ExpectedApps[0].AppIDEnv != "HERMES_GITHUB_APP_ID" {
		t.Fatalf("hermes expected apps = %#v", hermes.ExpectedApps)
	}
	denovo, ok := policy.OwnerForLabel("owner:denovo")
	if !ok {
		t.Fatal("OwnerForLabel(owner:denovo) not found")
	}
	if got := strings.Join(denovo.AllowedRepositories, ","); got != "taboularasa/de-novo" {
		t.Fatalf("denovo repositories = %s", got)
	}
	human, ok := policy.OwnerForLabel("owner:human")
	if !ok {
		t.Fatal("OwnerForLabel(owner:human) not found")
	}
	if got := strings.Join(human.AllowedRepositories, ","); got != "taboularasa/symphony" {
		t.Fatalf("human repositories = %s", got)
	}
	if len(human.ExpectedApps) != 0 {
		t.Fatalf("human expected apps = %#v, want none", human.ExpectedApps)
	}
}

func TestDecodePolicyRejectsInvalidPolicy(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "unknown owner",
			yaml: validPolicyYAML("owner:unknown", "taboularasa/hermes-agent", "HERMES_GITHUB_APP_ID"),
			want: "unknown owner label",
		},
		{
			name: "duplicate owner",
			yaml: `schema_version: hadto.symphony.github-owner-backstop.v1
owners:
  - owner_label: owner:hermes
    allowed_repositories: [taboularasa/hermes-agent]
    expected_apps:
      - slug: hermes-bot
        login: hermes-bot[bot]
        app_id_env: HERMES_GITHUB_APP_ID
  - owner_label: owner:hermes
    allowed_repositories: [taboularasa/phoneitin]
    expected_apps:
      - slug: hermes-bot
        login: hermes-bot[bot]
        app_id_env: HERMES_GITHUB_APP_ID
`,
			want: "duplicates owner:hermes",
		},
		{
			name: "repository shape",
			yaml: validPolicyYAML("owner:hermes", "not-a-full-name", "HERMES_GITHUB_APP_ID"),
			want: "must be owner/name",
		},
		{
			name: "cross owner repository collision",
			yaml: `schema_version: hadto.symphony.github-owner-backstop.v1
owners:
  - owner_label: owner:hermes
    allowed_repositories: [taboularasa/shared]
    expected_apps:
      - slug: hermes-bot
        login: hermes-bot[bot]
        app_id_env: HERMES_GITHUB_APP_ID
  - owner_label: owner:denovo
    allowed_repositories: [taboularasa/shared]
    expected_apps:
      - slug: denovo-bot
        login: denovo-bot[bot]
        app_id_env: DENOVO_GITHUB_APP_ID
`,
			want: "already belongs to owner:hermes",
		},
		{
			name: "missing app id",
			yaml: `schema_version: hadto.symphony.github-owner-backstop.v1
owners:
  - owner_label: owner:hermes
    allowed_repositories: [taboularasa/hermes-agent]
    expected_apps:
      - slug: hermes-bot
        login: hermes-bot[bot]
`,
			want: "app_id or app_id_env must be set",
		},
		{
			name: "missing app for automation owner",
			yaml: `schema_version: hadto.symphony.github-owner-backstop.v1
owners:
  - owner_label: owner:hermes
    allowed_repositories: [taboularasa/hermes-agent]
    expected_apps: []
`,
			want: "expected_apps must not be empty",
		},
		{
			name: "invalid app id",
			yaml: `schema_version: hadto.symphony.github-owner-backstop.v1
owners:
  - owner_label: owner:hermes
    allowed_repositories: [taboularasa/hermes-agent]
    expected_apps:
      - slug: hermes-bot
        login: hermes-bot[bot]
        app_id: "-1"
`,
			want: "app_id must be a positive integer",
		},
		{
			name: "invalid app id env",
			yaml: validPolicyYAML("owner:hermes", "taboularasa/hermes-agent", "hermes-app-id"),
			want: "app_id_env must be an uppercase env var name",
		},
		{
			name: "ambiguous human bypass",
			yaml: `schema_version: hadto.symphony.github-owner-backstop.v1
owners:
  - owner_label: owner:hermes
    allowed_repositories: [taboularasa/hermes-agent]
    expected_apps:
      - slug: hermes-bot
        login: hermes-bot[bot]
        app_id_env: HERMES_GITHUB_APP_ID
human_bypass:
  mode: explicit_logins
`,
			want: "allowed_logins must not be empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodePolicy(strings.NewReader(tt.yaml))
			if err == nil {
				t.Fatal("DecodePolicy() error = nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("DecodePolicy() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDecodePolicyAllowsExplicitSharedRepository(t *testing.T) {
	const input = `schema_version: hadto.symphony.github-owner-backstop.v1
shared_repositories: [taboularasa/shared]
owners:
  - owner_label: owner:hermes
    allowed_repositories: [taboularasa/shared]
    expected_apps:
      - slug: hermes-bot
        login: hermes-bot[bot]
        app_id_env: HERMES_GITHUB_APP_ID
  - owner_label: owner:denovo
    allowed_repositories: [taboularasa/shared]
    expected_apps:
      - slug: denovo-bot
        login: denovo-bot[bot]
        app_id_env: DENOVO_GITHUB_APP_ID
`
	if _, err := DecodePolicy(strings.NewReader(input)); err != nil {
		t.Fatalf("DecodePolicy() error = %v", err)
	}
}

func validPolicyYAML(ownerLabel, repo, appIDEnv string) string {
	return `schema_version: hadto.symphony.github-owner-backstop.v1
owners:
  - owner_label: ` + ownerLabel + `
    allowed_repositories: [` + repo + `]
    expected_apps:
      - slug: hermes-bot
        login: hermes-bot[bot]
        app_id_env: ` + appIDEnv + `
`
}

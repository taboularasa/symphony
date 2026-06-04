package githubbackstop

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"

	ReasonAllowedApp             = "allowed_app"
	ReasonHumanBypass            = "human_bypass"
	ReasonInvalidRepository      = "invalid_repository"
	ReasonLinearIssueMissing     = "linear_issue_missing"
	ReasonOwnerLabelMissing      = "owner_label_missing"
	ReasonOwnerPolicyMissing     = "owner_policy_missing"
	ReasonRepositoryNotAllowed   = "repository_not_allowed"
	ReasonActorMissing           = "actor_missing"
	ReasonActorNotAllowed        = "actor_not_allowed"
	ReasonActorAppIDMismatch     = "actor_app_id_mismatch"
	ReasonHumanBypassUnavailable = "human_bypass_unavailable"
)

var linearIssueKeyPattern = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-[0-9]+\b`)

type CheckInput struct {
	Repository     string
	Branch         string
	PRBody         string
	HeadSHA        string
	LinearIssueKey string
	OwnerLabel     string
	Actor          ActorIdentity
}

type ActorIdentity struct {
	Login     string
	Type      string
	AppID     string
	RepoAdmin bool
}

type Decision struct {
	Status         string `json:"status"`
	ReasonCode     string `json:"reason_code"`
	Reason         string `json:"reason"`
	Repository     string `json:"repository,omitempty"`
	HeadSHA        string `json:"head_sha,omitempty"`
	LinearIssueKey string `json:"linear_issue_key,omitempty"`
	OwnerLabel     string `json:"owner_label,omitempty"`
	ActorLogin     string `json:"actor_login,omitempty"`
	ActorType      string `json:"actor_type,omitempty"`
}

func Evaluate(policy Policy, input CheckInput) Decision {
	policy = policy.Normalized()
	repository, err := normalizeRepository(input.Repository)
	if err != nil {
		return deny(ReasonInvalidRepository, err.Error(), input)
	}
	input.Repository = repository
	input.LinearIssueKey = normalizeIssueKey(input.LinearIssueKey)
	if input.LinearIssueKey == "" {
		input.LinearIssueKey = FirstLinearIssueKey(input.Branch, input.PRBody)
	}
	if input.LinearIssueKey == "" {
		return deny(ReasonLinearIssueMissing, "Linear issue key is required", input)
	}

	input.OwnerLabel = normalizeOwnerLabel(input.OwnerLabel)
	if input.OwnerLabel == "" {
		return deny(ReasonOwnerLabelMissing, "owner label is required", input)
	}
	owner, ok := policy.OwnerForLabel(input.OwnerLabel)
	if !ok {
		return deny(ReasonOwnerPolicyMissing, fmt.Sprintf("no policy for %s", input.OwnerLabel), input)
	}
	if !slices.Contains(owner.AllowedRepositories, repository) {
		return deny(ReasonRepositoryNotAllowed, fmt.Sprintf("%s is not allowed for %s", repository, input.OwnerLabel), input)
	}

	actor := input.Actor.Normalized()
	input.Actor = actor
	if actor.Login == "" {
		return deny(ReasonActorMissing, "actor login is required", input)
	}
	if appMatch, appIDMismatch := actorMatchesExpectedApp(owner, actor); appMatch {
		return allow(ReasonAllowedApp, fmt.Sprintf("%s may act for %s", actor.Login, input.OwnerLabel), input)
	} else if appIDMismatch {
		return deny(ReasonActorAppIDMismatch, "actor app id did not match policy", input)
	}
	if humanBypassAllowed(policy.HumanBypass, actor) {
		return allow(ReasonHumanBypass, fmt.Sprintf("%s is allowed by human bypass policy", actor.Login), input)
	}
	if isHumanActor(actor) && !humanBypassConfigured(policy.HumanBypass) {
		return deny(ReasonHumanBypassUnavailable, "human bypass is not configured for this actor", input)
	}
	return deny(ReasonActorNotAllowed, fmt.Sprintf("%s is not allowed for %s", actor.Login, input.OwnerLabel), input)
}

func FirstLinearIssueKey(values ...string) string {
	for _, value := range values {
		if match := linearIssueKeyPattern.FindString(strings.ToUpper(value)); match != "" {
			return match
		}
	}
	return ""
}

func (a ActorIdentity) Normalized() ActorIdentity {
	a.Login = strings.TrimSpace(a.Login)
	a.Type = strings.TrimSpace(a.Type)
	a.AppID = strings.TrimSpace(a.AppID)
	return a
}

func actorMatchesExpectedApp(owner OwnerPolicy, actor ActorIdentity) (bool, bool) {
	if !isBotActor(actor) {
		return false, false
	}
	for _, app := range owner.ExpectedApps {
		loginMatches := strings.EqualFold(actor.Login, strings.TrimSpace(app.Login)) ||
			strings.EqualFold(actor.Login, strings.TrimSpace(app.Slug)) ||
			strings.EqualFold(actor.Login, strings.TrimSpace(app.Slug)+"[bot]")
		if !loginMatches {
			continue
		}
		expectedID := expectedAppID(app)
		if actor.AppID != "" && expectedID != "" && actor.AppID != expectedID {
			return false, true
		}
		return true, false
	}
	return false, false
}

func expectedAppID(app AppIdentity) string {
	if strings.TrimSpace(app.AppID) != "" {
		return strings.TrimSpace(app.AppID)
	}
	if strings.TrimSpace(app.AppIDEnv) != "" {
		return strings.TrimSpace(os.Getenv(strings.TrimSpace(app.AppIDEnv)))
	}
	return ""
}

func humanBypassConfigured(bypass HumanBypass) bool {
	switch strings.TrimSpace(bypass.Mode) {
	case "repo_admin_only", "explicit_logins":
		return true
	default:
		return false
	}
}

func humanBypassAllowed(bypass HumanBypass, actor ActorIdentity) bool {
	if !isHumanActor(actor) {
		return false
	}
	switch strings.TrimSpace(bypass.Mode) {
	case "repo_admin_only":
		return actor.RepoAdmin
	case "explicit_logins":
		for _, login := range bypass.AllowedLogins {
			if strings.EqualFold(strings.TrimSpace(login), actor.Login) {
				return true
			}
		}
	}
	return false
}

func isBotActor(actor ActorIdentity) bool {
	actorType := strings.ToLower(strings.TrimSpace(actor.Type))
	return actorType == "bot" || actorType == "app" || strings.HasSuffix(strings.ToLower(actor.Login), "[bot]")
}

func isHumanActor(actor ActorIdentity) bool {
	actorType := strings.ToLower(strings.TrimSpace(actor.Type))
	return actorType == "" || actorType == "user"
}

func normalizeIssueKey(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func allow(code, reason string, input CheckInput) Decision {
	return decision(DecisionAllow, code, reason, input)
}

func deny(code, reason string, input CheckInput) Decision {
	return decision(DecisionDeny, code, reason, input)
}

func decision(status, code, reason string, input CheckInput) Decision {
	return Decision{
		Status:         status,
		ReasonCode:     code,
		Reason:         reason,
		Repository:     strings.TrimSpace(input.Repository),
		HeadSHA:        strings.TrimSpace(input.HeadSHA),
		LinearIssueKey: strings.TrimSpace(input.LinearIssueKey),
		OwnerLabel:     strings.TrimSpace(input.OwnerLabel),
		ActorLogin:     strings.TrimSpace(input.Actor.Login),
		ActorType:      strings.TrimSpace(input.Actor.Type),
	}
}

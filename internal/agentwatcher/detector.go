package agentwatcher

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type Detector struct {
	Config Config
	clock  func() time.Time
	mu     sync.Mutex
	writes map[string][]time.Time
}

type Alert struct {
	Kind       string `json:"kind"`
	Reason     string `json:"reason"`
	Actor      string `json:"actor"`
	LinearID   string `json:"linear_id"`
	Project    string `json:"project"`
	IssueURL   string `json:"issue_url,omitempty"`
	OccurredAt string `json:"occurred_at"`
}

func NewDetector(config Config) *Detector {
	return &Detector{
		Config: config,
		clock:  time.Now,
		writes: map[string][]time.Time{},
	}
}

func (d *Detector) Evaluate(event Event) []Alert {
	if d.clock == nil {
		d.clock = time.Now
	}
	if normalizeKey(event.ActorKey) == normalizeKey(d.Config.WatcherActor) && normalizeKey(event.ActorKey) != "" {
		return nil
	}
	actor, known := d.Config.ActorForEvent(event)
	actorKey := actor.Key
	if !known {
		actorKey = unknownActorKey(event)
	}
	event.ActorKey = actorKey
	if normalizeKey(actorKey) == normalizeKey(d.Config.WatcherActor) && normalizeKey(actorKey) != "" {
		return nil
	}

	var alerts []Alert
	if known && d.Config.IsForbidden(event.Project, actorKey) && isWriteAction(event.Action) {
		alerts = append(alerts, d.alert(event, actorKey, "forbidden_project_write"))
	}
	if conflictOwnerLabels(event.Labels) {
		alerts = append(alerts, d.alert(event, actorKey, "owner_label_conflict"))
	}
	if d.recordWrite(actorKey, event) {
		alerts = append(alerts, d.alert(event, actorKey, "actor_rate_limit"))
	}
	return alerts
}

func unknownActorKey(event Event) string {
	switch {
	case strings.TrimSpace(event.ActorID) != "":
		return "unknown:" + strings.TrimSpace(event.ActorID)
	case strings.TrimSpace(event.AppID) != "":
		return "unknown-app:" + strings.TrimSpace(event.AppID)
	case strings.TrimSpace(event.BotID) != "":
		return "unknown-bot:" + strings.TrimSpace(event.BotID)
	default:
		return "unknown"
	}
}

func (d *Detector) recordWrite(actorKey string, event Event) bool {
	if !isWriteAction(event.Action) {
		return false
	}
	now := d.clock()
	windowStart := now.Add(-1 * time.Minute)
	d.mu.Lock()
	defer d.mu.Unlock()
	values := d.writes[actorKey]
	kept := values[:0]
	for _, value := range values {
		if value.After(windowStart) {
			kept = append(kept, value)
		}
	}
	kept = append(kept, now)
	d.writes[actorKey] = kept
	return len(kept) > d.Config.RateLimits.ActorWritesPerMinute
}

func (d *Detector) alert(event Event, actorKey, reason string) Alert {
	return Alert{
		Kind:       "block",
		Reason:     reason,
		Actor:      actorKey,
		LinearID:   event.Identifier,
		Project:    event.Project,
		IssueURL:   event.IssueURL,
		OccurredAt: d.clock().UTC().Format(time.RFC3339),
	}
}

func isWriteAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "comment", "state_transition", "self_assign", "issue_update", "label_change":
		return true
	default:
		return false
	}
}

func conflictOwnerLabels(labels []string) bool {
	owners := make(map[string]struct{})
	for _, label := range labels {
		label = strings.ToLower(strings.TrimSpace(label))
		if strings.HasPrefix(label, "owner:") {
			owners[label] = struct{}{}
		}
	}
	return len(owners) > 1
}

func ownerLabelList(labels []string) []string {
	var owners []string
	for _, label := range labels {
		label = strings.ToLower(strings.TrimSpace(label))
		if strings.HasPrefix(label, "owner:") {
			owners = append(owners, label)
		}
	}
	sort.Strings(owners)
	return owners
}

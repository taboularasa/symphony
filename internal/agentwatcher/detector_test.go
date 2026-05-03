package agentwatcher

import (
	"strings"
	"testing"
	"time"
)

func TestDetectorAlertsOnForbiddenProject(t *testing.T) {
	cfg := mustConfig(t)
	detector := NewDetector(cfg)
	detector.clock = fixedClock()
	alerts := detector.Evaluate(Event{
		ActorID:    "user-hermes",
		Identifier: "HAD-651",
		Project:    "Symphony",
		Action:     "comment",
	})
	if len(alerts) != 1 {
		t.Fatalf("alerts = %+v", alerts)
	}
	if alerts[0].Reason != "forbidden_project_write" {
		t.Fatalf("reason = %s", alerts[0].Reason)
	}
}

func TestDetectorAlertsOnHermesDeNovoProject(t *testing.T) {
	cfg := mustConfig(t)
	detector := NewDetector(cfg)
	detector.clock = fixedClock()
	alerts := detector.Evaluate(Event{
		ActorID:    "user-hermes",
		Identifier: "HAD-1",
		Project:    "De Novo",
		Action:     "state_transition",
	})
	if len(alerts) != 1 || alerts[0].Reason != "forbidden_project_write" {
		t.Fatalf("alerts = %+v", alerts)
	}
}

func TestDetectorIgnoresHumanTraffic(t *testing.T) {
	cfg := mustConfig(t)
	detector := NewDetector(cfg)
	alerts := detector.Evaluate(Event{
		ActorEmail: "human@hadto.net",
		Identifier: "HAD-651",
		Project:    "Symphony",
		Action:     "comment",
	})
	if len(alerts) != 0 {
		t.Fatalf("expected human traffic to be ignored: %+v", alerts)
	}
}

func TestDetectorSupportsFutureDeNovoActor(t *testing.T) {
	cfg := mustConfig(t)
	cfg.Actors = append(cfg.Actors, ActorConfig{Key: "denovo", LinearUserIDs: []string{"user-denovo"}})
	cfg.ForbiddenFor = append(cfg.ForbiddenFor, ForbiddenRule{Project: "Symphony", Actors: []string{"denovo"}})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	detector := NewDetector(cfg)
	detector.clock = fixedClock()
	alerts := detector.Evaluate(Event{
		ActorID:    "user-denovo",
		Identifier: "HAD-651",
		Project:    "Symphony",
		Action:     "self_assign",
	})
	if len(alerts) != 1 || alerts[0].Actor != "denovo" || alerts[0].Reason != "forbidden_project_write" {
		t.Fatalf("alerts = %+v", alerts)
	}
}

func TestDetectorAlertsOnOwnerConflict(t *testing.T) {
	cfg := mustConfig(t)
	detector := NewDetector(cfg)
	alerts := detector.Evaluate(Event{
		ActorID:    "user-human",
		Identifier: "HAD-1",
		Project:    "Other",
		Action:     "label_change",
		Labels:     []string{"owner:hermes", "owner:denovo"},
	})
	if len(alerts) == 0 {
		t.Fatal("expected conflict alert")
	}
	if alerts[0].Reason != "owner_label_conflict" {
		t.Fatalf("alerts = %+v", alerts)
	}
}

func TestDetectorRateLimitCatchesFiftyWrites(t *testing.T) {
	cfg := mustConfig(t)
	cfg.RateLimits.ActorWritesPerMinute = 49
	detector := NewDetector(cfg)
	now := time.Date(2026, 5, 3, 21, 0, 0, 0, time.UTC)
	detector.clock = func() time.Time { return now }
	event := Event{ActorID: "user-hermes", Identifier: "HAD-1", Project: "Other", Action: "comment"}
	for i := 0; i < 49; i++ {
		if alerts := detector.Evaluate(event); len(alerts) != 0 {
			t.Fatalf("unexpected alert at write %d: %+v", i+1, alerts)
		}
	}
	alerts := detector.Evaluate(event)
	if len(alerts) != 1 || alerts[0].Reason != "actor_rate_limit" {
		t.Fatalf("alerts = %+v", alerts)
	}
}

func TestDetectorIgnoresWatcherIdentity(t *testing.T) {
	cfg := mustConfig(t)
	cfg.Actors = append(cfg.Actors, ActorConfig{Key: "watcher", Emails: []string{"watcher@hadto.net"}})
	detector := NewDetector(cfg)
	alerts := detector.Evaluate(Event{
		ActorEmail: "watcher@hadto.net",
		Identifier: "HAD-651",
		Project:    "Symphony",
		Action:     "comment",
	})
	if len(alerts) != 0 {
		t.Fatalf("expected watcher self event to be ignored: %+v", alerts)
	}
}

func mustConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := DecodeConfig(stringsReader(validConfigYAML()))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg
}

func fixedClock() func() time.Time {
	return func() time.Time {
		return time.Date(2026, 5, 3, 21, 0, 0, 0, time.UTC)
	}
}

func stringsReader(value string) *strings.Reader {
	return strings.NewReader(value)
}

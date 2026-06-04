package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestEvaluateHandoffRunPassesFixture(t *testing.T) {
	run := loadFixture(t)
	report := Evaluate(run)
	if !report.Passed {
		t.Fatalf("report failed: %+v", report)
	}
	if len(report.Timeline) != 12 {
		t.Fatalf("timeline length = %d", len(report.Timeline))
	}
}

func TestEvaluateRequiresAdversarialRefusal(t *testing.T) {
	run := loadFixture(t)
	run.Events = removeKind(run.Events, "adversarial_refusal")
	report := Evaluate(run)
	if report.Passed {
		t.Fatal("expected failure")
	}
	if !strings.Contains(report.FirstFailure, "adversarial_refusal") {
		t.Fatalf("first failure = %q", report.FirstFailure)
	}
}

func TestEvaluateFailsOnWatcherAlert(t *testing.T) {
	run := loadFixture(t)
	alerts := 1
	run.Events = append(run.Events, Event{
		TS:         "2026-06-04T15:12:00Z",
		Source:     "watcher",
		Kind:       "forbidden_project_write",
		Actor:      "hermes",
		LinearID:   "HAD-2001",
		AlertCount: &alerts,
	})
	report := Evaluate(run)
	if report.Passed {
		t.Fatal("expected failure")
	}
	if !strings.Contains(report.FirstFailure, "no watcher or ownership alerts") {
		t.Fatalf("first failure = %q", report.FirstFailure)
	}
}

func TestEvaluateFailsOnHermesChildWriteAfterHandoff(t *testing.T) {
	run := loadFixture(t)
	run.Events = append(run.Events, Event{
		TS:         "2026-06-04T15:04:30Z",
		Source:     "linear",
		Kind:       "comment_created",
		Actor:      "hermes",
		LinearID:   "HAD-2001",
		OwnerLabel: "owner:denovo",
	})
	report := Evaluate(run)
	if report.Passed {
		t.Fatal("expected failure")
	}
	if !strings.Contains(report.FirstFailure, "no Hermes child Linear writes") {
		t.Fatalf("first failure = %q", report.FirstFailure)
	}
}

func TestDecodeRunAcceptsTopLevelEventsArray(t *testing.T) {
	run := loadFixture(t)
	data, err := json.Marshal(run.Events)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	decoded, err := DecodeRun(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Scenario != scenarioHandoff001 || len(decoded.Events) != len(run.Events) {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestPrintTextReport(t *testing.T) {
	report := Evaluate(loadFixture(t))
	var buf bytes.Buffer
	PrintTextReport(&buf, report)
	got := buf.String()
	if !strings.Contains(got, "PASS handoff-001") {
		t.Fatalf("report text = %s", got)
	}
	if !strings.Contains(got, "required sequence") {
		t.Fatalf("report text = %s", got)
	}
}

func loadFixture(t *testing.T) DrillRun {
	t.Helper()
	data, err := os.ReadFile("fixtures/handoff-001-pass.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	run, err := DecodeRun(data)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return run
}

func removeKind(events []Event, kind string) []Event {
	out := make([]Event, 0, len(events))
	for _, event := range events {
		if event.Kind == kind {
			continue
		}
		out = append(out, event)
	}
	return out
}

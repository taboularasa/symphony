package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestBuildDrillPlanBlocksWithoutAuthorizationAndInputs(t *testing.T) {
	packet := BuildDrillPlan(DrillPlanOptions{
		RunID:       "handoff-001-test",
		GeneratedAt: time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC),
		LookupEnv:   func(string) (string, bool) { return "", false },
	})
	if packet.Status != DrillPlanStatusBlocked {
		t.Fatalf("status = %s", packet.Status)
	}
	if packet.ReadyToRunLiveWrites {
		t.Fatal("packet unexpectedly ready")
	}
	if packet.SecretValuesIncluded {
		t.Fatal("packet includes secret values")
	}
	if !strings.Contains(packet.AuthorizationTemplate, "Not authorized") {
		t.Fatalf("authorization template = %s", packet.AuthorizationTemplate)
	}
	if !hasBlockedGate(packet, "authorization_recorded") || !hasBlockedGate(packet, "parent_canary_issue") || !hasBlockedGate(packet, "hermes_linear_token") {
		t.Fatalf("gates = %#v", packet.Gates)
	}
}

func TestBuildDrillPlanReadyWhenAllInputsPresent(t *testing.T) {
	packet := BuildDrillPlan(DrillPlanOptions{
		RunID:                 "handoff-001-ready",
		GeneratedAt:           time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC),
		AuthorizationRecorded: true,
		AuthorizationURL:      "https://linear.app/hadto/issue/HAD-667#comment-auth",
		ParentLinearID:        "HAD-2000",
		ChildLinearID:         "HAD-2001",
		BridgeChannel:         "C0B83H1F15K",
		TargetRepo:            "taboularasa/de-novo",
		DrillBranch:           "had-667-handoff-drill-001-ready",
		LookupEnv: func(name string) (string, bool) {
			return "present", true
		},
	})
	if packet.Status != DrillPlanStatusReady {
		t.Fatalf("status = %s gates=%#v", packet.Status, packet.Gates)
	}
	if !packet.ReadyToRunLiveWrites {
		t.Fatal("packet not ready")
	}
	if packet.Inputs.ArtifactPath == "" || packet.Inputs.ReportPath == "" {
		t.Fatalf("inputs = %#v", packet.Inputs)
	}
	if !strings.Contains(packet.Commands.DeNovoLiveWrite[0], "DENOVO_SYMPHONY_DRILL_ALLOW_LINEAR_WRITES=1") {
		t.Fatalf("commands = %#v", packet.Commands.DeNovoLiveWrite)
	}
}

func TestWriteDrillPlan(t *testing.T) {
	path := t.TempDir() + "/plan.json"
	packet := BuildDrillPlan(DrillPlanOptions{RunID: "handoff-001-write", LookupEnv: func(string) (string, bool) { return "", false }})
	if err := WriteDrillPlan(path, packet); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if !strings.Contains(string(body), DrillPlanSchemaVersion) {
		t.Fatalf("body = %s", body)
	}
}

func hasBlockedGate(packet DrillPlanPacket, name string) bool {
	for _, gate := range packet.Gates {
		if gate.Name == name && gate.Status == "blocked" {
			return true
		}
	}
	return false
}

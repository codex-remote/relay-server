package protocol

import (
	"encoding/json"
	"testing"
)

func TestMessageRoundTrip(t *testing.T) {
	want, err := NewMessage(TypeTurnStart, "trace-1", Sender{Kind: "user", ID: "test"}, TurnStartPayload{ProjectID: "project-1", Prompt: "fix tests"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != TypeTurnStart || got.TraceID != "trace-1" {
		t.Fatalf("decoded message = %#v", got)
	}
	payload, err := PayloadAs[TurnStartPayload](got)
	if err != nil {
		t.Fatal(err)
	}
	if payload.ProjectID != "project-1" || payload.Prompt != "fix tests" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestAllowedFrom(t *testing.T) {
	if !AllowedFrom(RoleApp, TypeExecutionProfileList) || !AllowedFrom(RoleApp, TypeProjectList) || !AllowedFrom(RoleApp, TypeThreadList) || !AllowedFrom(RoleApp, TypeThreadRead) || !AllowedFrom(RoleApp, TypeTurnStart) || !AllowedFrom(RoleApp, TypeTurnAcknowledged) || AllowedFrom(RoleApp, TypeSourceRead) || AllowedFrom(RoleApp, TypeTurnOutput) {
		t.Fatal("unexpected App direction rules")
	}
	if !AllowedFrom(RoleAgent, TypeAgentCapabilities) || !AllowedFrom(RoleAgent, TypeExecutionProfileSnapshot) || !AllowedFrom(RoleAgent, TypeProjectSnapshot) || !AllowedFrom(RoleAgent, TypeThreadSnapshot) || !AllowedFrom(RoleAgent, TypeThreadDetail) || !AllowedFrom(RoleAgent, TypeSourceSnapshot) || !AllowedFrom(RoleAgent, TypeSourceReadFailed) || !AllowedFrom(RoleAgent, TypeTurnOutput) || !AllowedFrom(RoleAgent, TypeTurnItemStarted) || !AllowedFrom(RoleAgent, TypeTurnItemDelta) || !AllowedFrom(RoleAgent, TypeTurnItemDone) || AllowedFrom(RoleAgent, TypeTurnStart) {
		t.Fatal("unexpected Agent direction rules")
	}
}

func TestDecodeRejectsUnsupportedVersion(t *testing.T) {
	message, err := NewMessage(TypeTurnStart, "trace-1", Sender{Kind: "user", ID: "test"}, TurnStartPayload{ProjectID: "project-1", Prompt: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	message.SpecVersion = "1.0"
	data, _ := json.Marshal(message)
	if _, err := Decode(data); err == nil {
		t.Fatal("Decode() accepted unsupported version")
	}
}

func TestBootstrapBatchProgressRoundTrip(t *testing.T) {
	want, err := NewMessage(TypeBootstrapBatch, "sync-1", Sender{Kind: "device", ID: "mac"}, BootstrapBatchPayload{
		CommandID: "command-1", SyncID: "sync-1", SnapshotID: "snapshot-1", BatchNo: 4,
		TotalSessions: 10, ProcessedSessions: 5, ReconciliationSafe: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	message, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := PayloadAs[BootstrapBatchPayload](message)
	if err != nil {
		t.Fatal(err)
	}
	if payload.TotalSessions != 10 || payload.ProcessedSessions != 5 || !payload.ReconciliationSafe {
		t.Fatalf("unexpected bootstrap progress: %#v", payload)
	}
}

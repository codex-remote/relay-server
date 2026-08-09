package protocol

import (
	"encoding/json"
	"testing"
)

func TestMessageRoundTrip(t *testing.T) {
	want, err := NewMessage(TypeRunStart, "run-1", Sender{Kind: "user", ID: "test"}, RunStartPayload{RunID: "run-1", Prompt: "fix tests"})
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
	if got.Type != TypeRunStart || got.TraceID != "run-1" {
		t.Fatalf("decoded message = %#v", got)
	}
	payload, err := PayloadAs[RunStartPayload](got)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Prompt != "fix tests" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestAllowedFrom(t *testing.T) {
	if !AllowedFrom(RoleApp, TypeRunStart) || AllowedFrom(RoleApp, TypeRunOutput) {
		t.Fatal("unexpected App direction rules")
	}
	if !AllowedFrom(RoleAgent, TypeRunOutput) || AllowedFrom(RoleAgent, TypeRunStart) {
		t.Fatal("unexpected Agent direction rules")
	}
}

func TestDecodeRejectsUnsupportedVersion(t *testing.T) {
	message, err := NewMessage(TypeRunStart, "run-1", Sender{Kind: "user", ID: "test"}, RunStartPayload{RunID: "run-1", Prompt: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	message.SpecVersion = "2.0"
	data, _ := json.Marshal(message)
	if _, err := Decode(data); err == nil {
		t.Fatal("Decode() accepted unsupported version")
	}
}

package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApifoxContractMatchesRelayRoutes(t *testing.T) {
	root := filepath.Join("..", "..", "apifox")
	data, err := os.ReadFile(filepath.Join(root, "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version = %q", document.OpenAPI)
	}
	for _, path := range []string{"/healthz", "/status"} {
		if _, ok := document.Paths[path]["get"]; !ok {
			t.Fatalf("Apifox OpenAPI is missing GET %s", path)
		}
	}

	wantWebSockets := map[string]string{
		"app.json":   "/ws/app",
		"agent.json": "/ws/agent",
	}
	for filename, wantPath := range wantWebSockets {
		data, err := os.ReadFile(filepath.Join(root, "websockets", filename))
		if err != nil {
			t.Fatal(err)
		}
		var definition struct {
			Path     string `json:"path"`
			ModuleID int    `json:"moduleId"`
		}
		if err := json.Unmarshal(data, &definition); err != nil {
			t.Fatal(err)
		}
		if definition.Path != wantPath || definition.ModuleID != 8356476 {
			t.Fatalf("%s = %#v", filename, definition)
		}

		doc, err := os.ReadFile(filepath.Join(root, "websockets", "docs", strings.TrimSuffix(filename, ".json")+".md"))
		if err != nil {
			t.Fatal(err)
		}
		for _, heading := range []string{"## 发送 Body", "## 接收 Body"} {
			if !strings.Contains(string(doc), heading) {
				t.Errorf("%s documentation is missing %q", filename, heading)
			}
		}
	}
}

func TestProtocolFixturesDecodeAndUseAllowedDirections(t *testing.T) {
	root := filepath.Join("..", "..", "protocol", "fixtures")
	want := map[string]string{
		"agent.capabilities.json":         "agent.capabilities",
		"execution.profile.list.json":     "execution.profile.list",
		"execution.profile.snapshot.json": "execution.profile.snapshot",
		"project.list.json":               "project.list",
		"project.snapshot.json":           "project.snapshot",
		"thread.list.json":                "thread.list",
		"thread.snapshot.json":            "thread.snapshot",
		"thread.read.json":                "thread.read",
		"thread.detail.json":              "thread.detail",
		"turn.start.json":                 "turn.start",
		"turn.item.started.json":          "turn.item.started",
		"turn.item.delta.json":            "turn.item.delta",
		"turn.item.completed.json":        "turn.item.completed",
		"turn.completed.json":             "turn.completed",
	}
	for filename, messageType := range want {
		data, err := os.ReadFile(filepath.Join(root, filename))
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			SpecVersion string `json:"spec_version"`
			Type        string `json:"type"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			t.Fatalf("%s: %v", filename, err)
		}
		if envelope.SpecVersion != "2.0" || envelope.Type != messageType {
			t.Errorf("%s = %#v", filename, envelope)
		}
	}
}

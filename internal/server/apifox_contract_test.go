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
		OpenAPI  string                                `json:"openapi"`
		Paths    map[string]map[string]json.RawMessage `json:"paths"`
		Security []map[string]json.RawMessage          `json:"security"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version = %q", document.OpenAPI)
	}
	var contractDocument struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &contractDocument); err != nil {
		t.Fatal(err)
	}
	if _, ok := contractDocument.Components.Schemas["Session"].Properties["latest_run_status"]; !ok {
		t.Fatal("Apifox Session schema is missing latest_run_status")
	}
	for _, path := range []string{"/healthz", "/status"} {
		if _, ok := document.Paths[path]["get"]; !ok {
			t.Fatalf("Apifox OpenAPI is missing GET %s", path)
		}
	}
	if len(document.Security) != 1 {
		t.Fatalf("global Runtime security = %#v", document.Security)
	}
	if _, ok := document.Security[0]["RuntimeBearer"]; !ok {
		t.Fatal("OpenAPI does not require RuntimeBearer by default")
	}
	for path, method := range map[string]string{
		"/v1/auth/pairing-grants:exchange": "post",
		"/v1/auth/tokens:refresh":          "post",
		"/v1/auth/sessions/current:revoke": "post",
		"/v1/auth/me":                      "get",
	} {
		if _, ok := document.Paths[path][method]; !ok {
			t.Fatalf("Apifox OpenAPI is missing %s %s", strings.ToUpper(method), path)
		}
	}
	for path := range map[string]struct{}{
		"/v1/runtime/sessions/{session_id}/events:poll": {},
		"/v1/runtime/runs/{run_id}/events:poll":         {},
	} {
		if _, ok := document.Paths[path]["get"]; !ok {
			t.Fatalf("Apifox OpenAPI is missing GET %s", path)
		}
	}
	sourcePath := "/v1/runtime/projects/{project_id}/source:read"
	sourceOperation, ok := document.Paths[sourcePath]["post"]
	if !ok {
		t.Fatalf("Apifox OpenAPI is missing POST %s", sourcePath)
	}
	var sourceContract struct {
		Security []map[string]json.RawMessage `json:"security"`
	}
	if err := json.Unmarshal(sourceOperation, &sourceContract); err != nil {
		t.Fatal(err)
	}
	if len(sourceContract.Security) != 1 {
		t.Fatalf("POST %s security = %#v", sourcePath, sourceContract.Security)
	}
	if _, ok := sourceContract.Security[0]["RuntimeBearer"]; !ok {
		t.Fatalf("POST %s does not require RuntimeBearer", sourcePath)
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
		"source.read.json":                "source.read",
		"source.snapshot.json":            "source.snapshot",
		"source.read.failed.json":         "source.read.failed",
		"bootstrap.batch.json":            "bootstrap.batch",
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

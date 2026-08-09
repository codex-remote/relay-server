package server

import (
	"encoding/json"
	"os"
	"path/filepath"
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
		"app.json":   "ws://127.0.0.1:8080/ws/app",
		"agent.json": "ws://127.0.0.1:8080/ws/agent",
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
	}
}

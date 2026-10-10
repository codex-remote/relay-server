package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type permissionCaptureStore struct {
	Store
	profileID string
}

func (s *permissionCaptureStore) CreateRun(_ context.Context, sessionID, prompt, profileID, key, hash string) (Run, int64, bool, error) {
	s.profileID = profileID
	return Run{ID: "run-1", SessionID: sessionID, Status: "queued", CreatedAt: time.Unix(1, 0)}, 1, true, nil
}

type permissionCaptureBroker struct{ Broker }

func (permissionCaptureBroker) NotifySession(context.Context, string) error { return nil }

func TestCreateRunPassesPermissionProfileToStore(t *testing.T) {
	store := &permissionCaptureStore{}
	api := NewAPI(store, permissionCaptureBroker{})
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/runtime/sessions/session-1/runs",
		strings.NewReader(`{"prompt":"fix","permission_profile_id":":workspace"}`),
	)
	request.SetPathValue("session_id", "session-1")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "key-1")
	response := httptest.NewRecorder()

	api.createRun(response, request)

	if response.Code != http.StatusAccepted || store.profileID != ":workspace" {
		t.Fatalf("status=%d profile=%q body=%s", response.Code, store.profileID, response.Body.String())
	}
}

func TestRunStartCommandPayloadIncludesPermissionProfile(t *testing.T) {
	data := runStartCommandPayload("command-1", "run-1", "project-1", "thread-1", "fix", ":danger-full-access")
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["permission_profile_id"] != ":danger-full-access" {
		t.Fatalf("payload = %#v", payload)
	}
}

package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParsePollQueryDefaultsAndBounds(t *testing.T) {
	request, _ := http.NewRequest(http.MethodGet, "/v1/runtime/runs/run_1/events:poll", nil)
	after, wait, limit, ok := parsePollQuery(request)
	if !ok || after != 0 || wait != defaultPollWait || limit != defaultPollSize {
		t.Fatalf("defaults = after %d wait %s limit %d ok %v", after, wait, limit, ok)
	}

	request, _ = http.NewRequest(http.MethodGet, "/v1/runtime/runs/run_1/events:poll?after=12&wait_ms=0&limit=2", nil)
	after, wait, limit, ok = parsePollQuery(request)
	if !ok || after != 12 || wait != 0 || limit != 2 {
		t.Fatalf("custom query = after %d wait %s limit %d ok %v", after, wait, limit, ok)
	}

	for _, query := range []string{"after=-1", "wait_ms=15001", "limit=0", "limit=101", "wait_ms=bad"} {
		request, _ = http.NewRequest(http.MethodGet, "/v1/runtime/runs/run_1/events:poll?"+query, nil)
		if _, _, _, ok = parsePollQuery(request); ok {
			t.Errorf("invalid query accepted: %s", query)
		}
	}
}

func TestNormalizeSessionRuntimeEventAddsCursorAndType(t *testing.T) {
	event := normalizeSessionRuntimeEvent("session_1", SessionEvent{
		Sequence: 7,
		Type:     "run.created",
		Payload:  []byte(`{"run_id":"run_1","status":"queued"}`),
	})
	var fields map[string]any
	if err := json.Unmarshal(event, &fields); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"id": "7", "run_id": "run_1", "runId": "run_1", "session_id": "session_1", "status": "queued", "type": "run.created"} {
		if fields[key] != want {
			t.Errorf("field %s = %#v, want %q", key, fields[key], want)
		}
	}
}

type pollTestStore struct {
	Store
	sessionEvents []SessionEvent
	run           Run
	runEvents     []RunEvent
}

func (s pollTestStore) GetSession(context.Context, string) (Session, error) {
	return Session{ID: "session_1"}, nil
}

func (s pollTestStore) GetRun(context.Context, string, bool) (Run, error) {
	return s.run, nil
}

func (s pollTestStore) ListSessionEventsPage(context.Context, string, int64, int) ([]SessionEvent, bool, error) {
	return s.sessionEvents, false, nil
}

func (s pollTestStore) ListRunEventsPage(context.Context, string, int64, int) ([]RunEvent, bool, error) {
	return s.runEvents, false, nil
}

func TestPollHandlersReturnJSONEventBatchAndTerminalState(t *testing.T) {
	store := pollTestStore{
		sessionEvents: []SessionEvent{{Sequence: 3, Type: "run.created", Payload: []byte(`{"run_id":"run_1","status":"queued"}`)}},
		run:           Run{ID: "run_1", SessionID: "session_1", Status: "completed"},
		runEvents:     []RunEvent{{Sequence: 8, Type: "turn.completed", Payload: []byte(`{"message":"done"}`), OccurredAt: time.Now().UTC()}},
	}
	mux := http.NewServeMux()
	NewAPI(store, nil).Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/runtime/sessions/session_1/events:poll?after=2&wait_ms=0", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("session poll response = %d %q", response.Code, response.Header().Get("Content-Type"))
	}
	var sessionEnvelope struct {
		Success bool `json:"success"`
		Data    struct {
			NextCursor int              `json:"next_cursor"`
			Events     []map[string]any `json:"events"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&sessionEnvelope); err != nil {
		t.Fatal(err)
	}
	if !sessionEnvelope.Success || sessionEnvelope.Data.NextCursor != 3 || len(sessionEnvelope.Data.Events) != 1 || sessionEnvelope.Data.Events[0]["type"] != "run.created" {
		t.Fatalf("session envelope = %#v", sessionEnvelope)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/runtime/runs/run_1/events:poll?after=8&wait_ms=0", nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	var runEnvelope struct {
		Data struct {
			Terminal bool   `json:"terminal"`
			Status   string `json:"status"`
			Events   []any  `json:"events"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&runEnvelope); err != nil {
		t.Fatal(err)
	}
	if !runEnvelope.Data.Terminal || runEnvelope.Data.Status != "completed" || len(runEnvelope.Data.Events) != 1 {
		t.Fatalf("run envelope = %#v", runEnvelope)
	}
}

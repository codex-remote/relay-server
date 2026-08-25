package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ai-coding-remote/relay-server/internal/protocol"
)

type API struct {
	store  Store
	broker Broker
	source SourceProvider
}

const (
	defaultPollWait = 15 * time.Second
	maxPollWait     = 15 * time.Second
	defaultPollSize = 100
	maxPollSize     = 100
)

func NewAPI(store Store, broker Broker) *API { return &API{store: store, broker: broker} }

func NewAPIWithSource(store Store, broker Broker, source SourceProvider) *API {
	return &API{store: store, broker: broker, source: source}
}

func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/runtime/healthz", a.health)
	mux.HandleFunc("GET /v1/runtime/projects", a.projects)
	mux.HandleFunc("POST /v1/runtime/projects/{project_id}/source:read", a.projectSource)
	mux.HandleFunc("GET /v1/runtime/sessions", a.sessions)
	mux.HandleFunc("POST /v1/runtime/sessions", a.createSession)
	mux.HandleFunc("GET /v1/runtime/sessions/{session_id}", a.session)
	mux.HandleFunc("GET /v1/runtime/sessions/{session_id}/runs", a.runs)
	mux.HandleFunc("POST /v1/runtime/sessions/{session_id}/runs", a.createRun)
	mux.HandleFunc("GET /v1/runtime/runs/{run_id}", a.run)
	mux.HandleFunc("POST /v1/runtime/runs/{run_id}/cancel", a.cancel)
	mux.HandleFunc("GET /v1/runtime/sessions/{session_id}/events", a.sessionEvents)
	mux.HandleFunc("GET /v1/runtime/sessions/{session_id}/events:poll", a.sessionEventsPoll)
	mux.HandleFunc("GET /v1/runtime/runs/{run_id}/events", a.runEvents)
	mux.HandleFunc("GET /v1/runtime/runs/{run_id}/events:poll", a.runEventsPoll)
	mux.HandleFunc("POST /v1/runtime/bootstrap-syncs", a.createSync)
	mux.HandleFunc("GET /v1/runtime/bootstrap-syncs/{sync_id}", a.sync)
}

func (a *API) projectSource(w http.ResponseWriter, r *http.Request) {
	if a.source == nil {
		writeError(w, http.StatusServiceUnavailable, "SOURCE_READ_DISABLED", false)
		return
	}
	projectID := strings.TrimSpace(r.PathValue("project_id"))
	var request struct {
		Path         string `json:"path"`
		Line         int    `json:"line,omitempty"`
		ContextLines int    `json:"context_lines,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Path = strings.TrimSpace(request.Path)
	if projectID == "" || request.Path == "" || len(projectID) > 256 || len(request.Path) > 4096 {
		writeError(w, http.StatusBadRequest, "SOURCE_INVALID", false)
		return
	}
	if request.Line < 0 || request.Line > 10_000_000 {
		writeError(w, http.StatusBadRequest, "SOURCE_INVALID_LINE", false)
		return
	}
	if request.ContextLines < 0 || request.ContextLines > 500 {
		writeError(w, http.StatusBadRequest, "SOURCE_INVALID_CONTEXT", false)
		return
	}
	if request.ContextLines == 0 {
		request.ContextLines = 200
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	snapshot, err := a.source.Read(ctx, protocol.SourceReadPayload{ProjectID: projectID, Path: request.Path, FocusLine: request.Line, ContextLines: request.ContextLines})
	if err == nil {
		writeData(w, http.StatusOK, snapshot)
		return
	}
	status, code, retryable := sourceHTTPError(err)
	writeError(w, status, code, retryable)
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.store.Health(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "POSTGRES_UNAVAILABLE", true)
		return
	}
	if err := a.broker.Health(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "REDIS_UNAVAILABLE", true)
		return
	}
	writeData(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) projects(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListProjects(r.Context())
	if err != nil {
		writeInternal(w)
		return
	}
	online, lastSeen, _ := a.broker.AgentPresence(r.Context())
	writeData(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil, "agent_presence": map[bool]string{true: "online", false: "offline"}[online], "agent_last_seen_at": nullableTime(lastSeen)})
}
func (a *API) sessions(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListSessions(r.Context(), r.URL.Query().Get("project_id"))
	if err != nil {
		writeInternal(w)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}
func (a *API) createSession(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", false)
		return
	}
	var request struct {
		ProjectID string `json:"project_id"`
		Title     string `json:"title"`
	}
	if !decodeJSON(w, r, &request) || request.ProjectID == "" {
		return
	}
	item, created, err := a.store.CreateSession(r.Context(), request.ProjectID, request.Title, key, RequestHash(request))
	if errors.Is(err, ErrIdempotencyConflict) {
		writeError(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", false)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeData(w, status, item)
}
func (a *API) session(w http.ResponseWriter, r *http.Request) {
	item, err := a.store.GetSession(r.Context(), r.PathValue("session_id"))
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", false)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	runs, err := a.store.ListRuns(r.Context(), item.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	online, lastSeen, _ := a.broker.AgentPresence(r.Context())
	writeData(w, http.StatusOK, map[string]any{"session": item, "runs": runs, "snapshot_session_sequence": item.LastSessionSequence, "agent_presence": map[bool]string{true: "online", false: "offline"}[online], "agent_last_seen_at": nullableTime(lastSeen)})
}
func (a *API) runs(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListRuns(r.Context(), r.PathValue("session_id"))
	if err != nil {
		writeInternal(w)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}
func (a *API) createRun(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", false)
		return
	}
	var request struct {
		Prompt string `json:"prompt"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Prompt = strings.TrimSpace(request.Prompt)
	if request.Prompt == "" {
		writeError(w, http.StatusBadRequest, "PROMPT_REQUIRED", false)
		return
	}
	item, sequence, _, err := a.store.CreateRun(r.Context(), r.PathValue("session_id"), request.Prompt, key, RequestHash(request))
	if errors.Is(err, ErrIdempotencyConflict) {
		writeError(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", false)
		return
	}
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", false)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	_ = a.broker.NotifySession(r.Context(), item.SessionID)
	writeData(w, http.StatusAccepted, map[string]any{"run_id": item.ID, "session_id": item.SessionID, "status": item.Status, "session_sequence": sequence, "created_at": item.CreatedAt})
}
func (a *API) run(w http.ResponseWriter, r *http.Request) {
	item, err := a.store.GetRun(r.Context(), r.PathValue("run_id"), true)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "RUN_NOT_FOUND", false)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	writeData(w, http.StatusOK, item)
}
func (a *API) cancel(w http.ResponseWriter, r *http.Request) {
	item, err := a.store.RequestCancel(r.Context(), r.PathValue("run_id"))
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "RUN_NOT_FOUND", false)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	writeData(w, http.StatusAccepted, map[string]any{"run_id": item.ID, "cancel_requested": item.CancelRequestedAt != nil, "status": item.Status})
}
func (a *API) createSync(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", false)
		return
	}
	item, _, err := a.store.CreateSyncJob(r.Context(), key)
	if err != nil {
		writeInternal(w)
		return
	}
	writeData(w, http.StatusAccepted, item)
}
func (a *API) sync(w http.ResponseWriter, r *http.Request) {
	item, err := a.store.GetSyncJob(r.Context(), r.PathValue("sync_id"))
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "SYNC_NOT_FOUND", false)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	writeData(w, http.StatusOK, item)
}

func (a *API) sessionEvents(w http.ResponseWriter, r *http.Request) {
	stream(w, r, func(ctx context.Context, after int64) (int64, []sseEvent, error) {
		items, err := a.store.ListSessionEvents(ctx, r.PathValue("session_id"), after)
		events := make([]sseEvent, 0, len(items))
		for _, item := range items {
			events = append(events, sseEvent{ID: item.Sequence, Type: item.Type, Data: item.Payload})
		}
		return lastSequence(after, events), events, err
	})
}
func (a *API) runEvents(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	stream(w, r, func(ctx context.Context, after int64) (int64, []sseEvent, error) {
		persisted, err := a.store.ListRunEvents(ctx, runID, after)
		if err != nil {
			return after, nil, err
		}
		bySequence := map[int64]RunEvent{}
		for _, item := range persisted {
			bySequence[item.Sequence] = item
		}
		live, err := a.broker.ReadRunEvents(ctx, runID, after)
		if err != nil {
			return after, nil, err
		}
		for _, item := range live {
			if _, ok := bySequence[item.Sequence]; !ok {
				bySequence[item.Sequence] = item
			}
		}
		events := make([]sseEvent, 0, len(bySequence))
		for sequence := after + 1; ; sequence++ {
			item, ok := bySequence[sequence]
			if !ok {
				break
			}
			data := normalizeRuntimeEvent(runID, item)
			events = append(events, sseEvent{ID: sequence, Type: item.Type, Data: data})
		}
		return lastSequence(after, events), events, nil
	})
}

type pollResult struct {
	Events     []json.RawMessage `json:"events"`
	NextCursor int64             `json:"next_cursor"`
	HasMore    bool              `json:"has_more"`
	TimedOut   bool              `json:"timed_out"`
	Terminal   bool              `json:"terminal"`
	Status     string            `json:"status,omitempty"`
}

func (a *API) sessionEventsPoll(w http.ResponseWriter, r *http.Request) {
	after, wait, limit, ok := parsePollQuery(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "POLL_QUERY_INVALID", false)
		return
	}
	sessionID := r.PathValue("session_id")
	if _, err := a.store.GetSession(r.Context(), sessionID); errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", false)
		return
	} else if err != nil {
		writeInternal(w)
		return
	}
	deadline := time.Now().Add(wait)
	for {
		events, hasMore, err := a.store.ListSessionEventsPage(r.Context(), sessionID, after, limit)
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			writeInternal(w)
			return
		}
		if len(events) > 0 {
			items := make([]json.RawMessage, 0, len(events))
			for _, event := range events {
				items = append(items, normalizeSessionRuntimeEvent(sessionID, event))
			}
			writeData(w, http.StatusOK, pollResult{Events: items, NextCursor: events[len(events)-1].Sequence, HasMore: hasMore})
			return
		}
		if wait == 0 || time.Now().After(deadline) {
			writeData(w, http.StatusOK, pollResult{Events: []json.RawMessage{}, NextCursor: after, TimedOut: wait > 0})
			return
		}
		a.waitForPoll(r.Context(), deadline, func(ctx context.Context) error {
			if waiter, ok := a.broker.(pollWaiter); ok {
				if err := waiter.WaitForSession(ctx, sessionID); err == nil {
					return nil
				}
			}
			return waitForPollTimer(ctx)
		})
	}
}

func (a *API) runEventsPoll(w http.ResponseWriter, r *http.Request) {
	after, wait, limit, ok := parsePollQuery(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "POLL_QUERY_INVALID", false)
		return
	}
	runID := r.PathValue("run_id")
	deadline := time.Now().Add(wait)
	for {
		run, err := a.store.GetRun(r.Context(), runID, false)
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "RUN_NOT_FOUND", false)
			return
		}
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			writeInternal(w)
			return
		}
		events, hasMore, err := a.store.ListRunEventsPage(r.Context(), runID, after, limit)
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			writeInternal(w)
			return
		}
		if len(events) > 0 {
			items := make([]json.RawMessage, 0, len(events))
			for _, event := range events {
				items = append(items, normalizeRuntimeEvent(runID, event))
			}
			writeData(w, http.StatusOK, pollResult{Events: items, NextCursor: events[len(events)-1].Sequence, HasMore: hasMore, Terminal: isTerminal(run.Status), Status: run.Status})
			return
		}
		if isTerminal(run.Status) || wait == 0 || time.Now().After(deadline) {
			writeData(w, http.StatusOK, pollResult{Events: []json.RawMessage{}, NextCursor: after, TimedOut: wait > 0 && !isTerminal(run.Status), Terminal: isTerminal(run.Status), Status: run.Status})
			return
		}
		a.waitForPoll(r.Context(), deadline, func(ctx context.Context) error {
			if waiter, ok := a.broker.(pollWaiter); ok {
				if err := waiter.WaitForRun(ctx, runID); err == nil {
					return nil
				}
			}
			return waitForPollTimer(ctx)
		})
	}
}

func parsePollQuery(r *http.Request) (int64, time.Duration, int, bool) {
	after := int64(0)
	if value := r.URL.Query().Get("after"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			return 0, 0, 0, false
		}
		after = parsed
	}
	wait := defaultPollWait
	if value := r.URL.Query().Get("wait_ms"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 || parsed > maxPollWait.Milliseconds() {
			return 0, 0, 0, false
		}
		wait = time.Duration(parsed) * time.Millisecond
	}
	limit := defaultPollSize
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > maxPollSize {
			return 0, 0, 0, false
		}
		limit = parsed
	}
	return after, wait, limit, true
}

func (a *API) waitForPoll(ctx context.Context, deadline time.Time, wait func(context.Context) error) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return
	}
	waitContext, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	_ = wait(waitContext)
}

func waitForPollTimer(ctx context.Context) error {
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type sseEvent struct {
	ID   int64
	Type string
	Data json.RawMessage
}

func stream(w http.ResponseWriter, r *http.Request, read func(context.Context, int64) (int64, []sseEvent, error)) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "STREAM_UNSUPPORTED", false)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	cursor := lastEventID(r)
	ticker := time.NewTicker(400 * time.Millisecond)
	heartbeat := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			next, events, err := read(r.Context(), cursor)
			if err != nil {
				return
			}
			for _, event := range events {
				fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, event.Data)
			}
			if len(events) > 0 {
				flusher.Flush()
				cursor = next
			}
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}
func lastEventID(r *http.Request) int64 {
	value := r.Header.Get("Last-Event-ID")
	if value == "" {
		value = r.URL.Query().Get("after")
	}
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}
func lastSequence(fallback int64, events []sseEvent) int64 {
	if len(events) == 0 {
		return fallback
	}
	return events[len(events)-1].ID
}
func normalizeRuntimeEvent(runID string, event RunEvent) json.RawMessage {
	var fields map[string]any
	if json.Unmarshal(event.Payload, &fields) != nil {
		fields = map[string]any{"payload": event.Payload}
	}
	fields["id"] = strconv.FormatInt(event.Sequence, 10)
	fields["runId"] = runID
	fields["run_id"] = runID
	fields["type"] = event.Type
	if event.Type == "assistant.delta" {
		if text, ok := fields["text"]; ok {
			fields["delta"] = text
		}
	}
	data, _ := json.Marshal(fields)
	return data
}

func normalizeSessionRuntimeEvent(sessionID string, event SessionEvent) json.RawMessage {
	var fields map[string]any
	if json.Unmarshal(event.Payload, &fields) != nil {
		fields = map[string]any{"payload": event.Payload}
	}
	fields["id"] = strconv.FormatInt(event.Sequence, 10)
	fields["type"] = event.Type
	fields["session_id"] = sessionID
	if runID, ok := fields["run_id"].(string); ok && runID != "" {
		fields["runId"] = runID
	}
	data, _ := json.Marshal(fields)
	return data
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", false)
		return false
	}
	return true
}
func writeData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{"success": true, "data": data, "meta": map[string]any{"schema_version": 1}})
}
func writeError(w http.ResponseWriter, status int, code string, retryable bool) {
	writeJSON(w, status, map[string]any{"success": false, "error": map[string]any{"code": code, "message": code, "retryable": retryable}, "meta": map[string]any{"schema_version": 1}})
}
func writeInternal(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", true)
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func sourceHTTPError(err error) (int, string, bool) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "SOURCE_TIMEOUT", true
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "SOURCE_CANCELED", true
	case errors.Is(err, ErrSourceAgentOffline):
		return http.StatusServiceUnavailable, "AGENT_OFFLINE", true
	case errors.Is(err, ErrSourceUnavailable):
		return http.StatusBadGateway, "SOURCE_UNAVAILABLE", true
	case errors.Is(err, ErrSourceBusy):
		return http.StatusTooManyRequests, "SOURCE_BUSY", true
	}
	var remote *SourceRemoteError
	if !errors.As(err, &remote) {
		return http.StatusInternalServerError, "INTERNAL_ERROR", true
	}
	switch remote.Code {
	case "PROJECT_NOT_FOUND", "SOURCE_NOT_FOUND":
		return http.StatusNotFound, remote.Code, false
	case "SOURCE_FORBIDDEN":
		return http.StatusForbidden, remote.Code, false
	case "SOURCE_TOO_LARGE":
		return http.StatusRequestEntityTooLarge, remote.Code, false
	case "SOURCE_BINARY":
		return http.StatusUnsupportedMediaType, remote.Code, false
	case "SOURCE_INVALID":
		return http.StatusBadRequest, remote.Code, false
	case "SOURCE_UNAVAILABLE":
		return http.StatusServiceUnavailable, remote.Code, true
	default:
		return http.StatusBadGateway, "SOURCE_READ_FAILED", true
	}
}

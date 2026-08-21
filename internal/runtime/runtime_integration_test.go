package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-coding-remote/relay-server/internal/config"
	"github.com/ai-coding-remote/relay-server/internal/protocol"
	runtimecore "github.com/ai-coding-remote/relay-server/internal/runtime"
	"github.com/ai-coding-remote/relay-server/internal/server"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
)

const runtimeTestPostgresURL = "postgres://codexremote:codexremote@127.0.0.1:54329/codexremote?sslmode=disable"

func TestRuntimePostgresRedisAgentFlow(t *testing.T) {
	if os.Getenv("RUNTIME_INTEGRATION") != "1" {
		t.Skip("set RUNTIME_INTEGRATION=1")
	}
	ctx := context.Background()
	store, err := runtimecore.OpenPostgres(ctx, runtimeTestPostgresURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	broker, err := runtimecore.OpenRedis(ctx, "redis://default:codexremote@127.0.0.1:63799/0")
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	projectID := "project_" + protocol.NewID()
	cleanupProjectFixture(t, projectID)
	threadID := "thread_" + protocol.NewID()
	turnID := "turn_" + protocol.NewID()
	importedThreadID := "thread_" + protocol.NewID()
	importedTurnID := "turn_" + protocol.NewID()
	if err := store.UpsertProjects(ctx, []runtimecore.Project{{ID: projectID, DisplayName: "Integration"}}); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	relay := server.NewWithRuntime(config.Config{MaxMessageBytes: 256 * 1024, WriteQueueSize: 32, PingInterval: time.Second}, logger, store, broker)
	httpServer := httptest.NewServer(relay.Handler())
	defer func() { relay.CloseConnections("test complete"); httpServer.Close() }()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws/agent"
	agent, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.CloseNow()
	_ = readProtocol(t, agent)
	session := post(t, httpServer.URL+"/v1/runtime/sessions", "session-key-"+protocol.NewID(), map[string]any{"project_id": projectID, "title": "Integration"})
	sessionID := nestedString(session, "data", "session_id")
	runResponse := post(t, httpServer.URL+"/v1/runtime/sessions/"+sessionID+"/runs", "run-key-"+protocol.NewID(), map[string]any{"prompt": "test"})
	runID := nestedString(runResponse, "data", "run_id")
	start := readUntil(t, agent, protocol.TypeTurnStart)
	payload, _ := protocol.PayloadAs[protocol.TurnStartPayload](start)
	accepted, _ := protocol.NewMessage(protocol.TypeRunAccepted, runID, protocol.Sender{Kind: "device", ID: "local-mac"}, protocol.RunAcceptedPayload{CommandID: payload.CommandID, RunID: runID})
	accepted.AgentSequence = 1
	writeProtocol(t, agent, accepted)
	readUntil(t, agent, protocol.TypeRuntimeReceivedAck)
	readUntil(t, agent, protocol.TypeRuntimeDurableAck)
	started, _ := protocol.NewMessage(protocol.TypeTurnStarted, runID, protocol.Sender{Kind: "device", ID: "local-mac"}, protocol.TurnStartedPayload{ProjectID: projectID, ThreadID: threadID, TurnID: turnID, StartedAt: time.Now().UTC()})
	started.AgentSequence = 2
	writeProtocol(t, agent, started)
	readUntil(t, agent, protocol.TypeRuntimeReceivedAck)
	readUntil(t, agent, protocol.TypeRuntimeDurableAck)
	completed, _ := protocol.NewMessage(protocol.TypeTurnCompleted, runID, protocol.Sender{Kind: "device", ID: "local-mac"}, protocol.TurnCompletedPayload{ProjectID: projectID, ThreadID: threadID, TurnID: turnID, Summary: "done"})
	completed.AgentSequence = 3
	writeProtocol(t, agent, completed)
	readUntil(t, agent, protocol.TypeRuntimeReceivedAck)
	readUntil(t, agent, protocol.TypeRuntimeDurableAck)
	deadline := time.Now().Add(3 * time.Second)
	for {
		run, err := store.GetRun(ctx, runID, true)
		if err == nil && run.Status == "completed" && len(run.Events) == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run not completed: %#v %v", run, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	syncResponse := post(t, httpServer.URL+"/v1/runtime/bootstrap-syncs", "sync-key-"+protocol.NewID(), map[string]any{})
	syncID := nestedString(syncResponse, "data", "sync_id")
	cleanupSyncFixture(t, syncID)
	bootstrapStart := readUntil(t, agent, protocol.TypeBootstrapStart)
	bootstrapCommand, _ := protocol.PayloadAs[protocol.BootstrapStartPayload](bootstrapStart)
	now := time.Now().UTC()
	batch := protocol.BootstrapBatchPayload{
		CommandID: bootstrapCommand.CommandID, SyncID: syncID, SnapshotID: "snapshot-test", BatchNo: 0, Checksum: "batch-0",
		Project: &protocol.Project{ID: projectID, Name: "Integration"},
		Thread:  &protocol.ThreadDetail{ID: threadID, ProjectID: projectID, Title: "Existing", CreatedAt: now, UpdatedAt: now, Turns: []protocol.ThreadHistoryTurn{{ID: turnID, Status: "completed", StartedAt: &now, CompletedAt: &now, Items: []protocol.ThreadHistoryItem{{ID: "existing-user", Type: "userMessage", Role: "user", Text: "existing"}}}}},
	}
	batchMessage, _ := protocol.NewMessage(protocol.TypeBootstrapBatch, syncID, protocol.Sender{Kind: "device", ID: "local-mac"}, batch)
	writeProtocol(t, agent, batchMessage)
	readUntil(t, agent, protocol.TypeBootstrapDurableAck)

	batch = protocol.BootstrapBatchPayload{
		CommandID: bootstrapCommand.CommandID, SyncID: syncID, SnapshotID: "snapshot-test", BatchNo: 1, Checksum: "batch-1",
		Project: &protocol.Project{ID: projectID, Name: "Integration"},
		Thread:  &protocol.ThreadDetail{ID: importedThreadID, ProjectID: projectID, Title: "Imported", CreatedAt: now, UpdatedAt: now, Turns: []protocol.ThreadHistoryTurn{{ID: importedTurnID, Status: "completed", StartedAt: &now, CompletedAt: &now, Items: []protocol.ThreadHistoryItem{{ID: "user-1", Type: "userMessage", Role: "user", Text: "import me"}, {ID: "assistant-1", Type: "agentMessage", Role: "assistant", Text: "imported"}}}}},
	}
	batchMessage, _ = protocol.NewMessage(protocol.TypeBootstrapBatch, syncID, protocol.Sender{Kind: "device", ID: "local-mac"}, batch)
	writeProtocol(t, agent, batchMessage)
	readUntil(t, agent, protocol.TypeBootstrapDurableAck)
	batch.BatchNo = 2
	batch.Checksum = "batch-2"
	batch.Project = nil
	batch.Thread = nil
	batch.Done = true
	batchMessage, _ = protocol.NewMessage(protocol.TypeBootstrapBatch, syncID, protocol.Sender{Kind: "device", ID: "local-mac"}, batch)
	writeProtocol(t, agent, batchMessage)
	readUntil(t, agent, protocol.TypeBootstrapDurableAck)

	job, err := store.GetSyncJob(ctx, syncID)
	if err != nil || job.Status != "completed" || job.ItemCount != 2 {
		t.Fatalf("bootstrap not completed: %#v %v", job, err)
	}
	imported, err := store.GetRun(ctx, "run_"+importedTurnID, true)
	if err != nil || imported.Status != "completed" || len(imported.Events) != 3 {
		t.Fatalf("bootstrap run missing: %#v %v", imported, err)
	}
}

func TestCreateRunConcurrentIdempotency(t *testing.T) {
	if os.Getenv("RUNTIME_INTEGRATION") != "1" {
		t.Skip("set RUNTIME_INTEGRATION=1")
	}
	ctx := context.Background()
	store, err := runtimecore.OpenPostgres(ctx, runtimeTestPostgresURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	projectID := "project_" + protocol.NewID()
	cleanupProjectFixture(t, projectID)
	if err := store.UpsertProjects(ctx, []runtimecore.Project{{ID: projectID, DisplayName: "Concurrent"}}); err != nil {
		t.Fatal(err)
	}
	session, _, err := store.CreateSession(ctx, projectID, "Concurrent", "session-key-"+protocol.NewID(), runtimecore.RequestHash(map[string]string{"project_id": projectID}))
	if err != nil {
		t.Fatal(err)
	}
	key := "run-key-" + protocol.NewID()
	hash := runtimecore.RequestHash(map[string]string{"prompt": "same prompt"})
	const workers = 20
	ids := make(chan string, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			run, _, _, err := store.CreateRun(ctx, session.ID, "same prompt", key, hash)
			if err != nil {
				errs <- err
				return
			}
			ids <- run.ID
		}()
	}
	group.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var expected string
	for id := range ids {
		if expected == "" {
			expected = id
			cleanupSyncFixture(t, expected)
		}
		if id != expected {
			t.Fatalf("idempotent requests returned different runs: %s and %s", expected, id)
		}
	}
	runs, err := store.ListRuns(ctx, session.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("expected one run, got %d: %v", len(runs), err)
	}
}

func TestCreateBootstrapConcurrentIdempotency(t *testing.T) {
	if os.Getenv("RUNTIME_INTEGRATION") != "1" {
		t.Skip("set RUNTIME_INTEGRATION=1")
	}
	ctx := context.Background()
	store, err := runtimecore.OpenPostgres(ctx, runtimeTestPostgresURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	key := "sync-key-" + protocol.NewID()
	const workers = 20
	ids := make(chan string, workers)
	errs := make(chan error, workers)
	createdCount := make(chan bool, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			job, created, err := store.CreateSyncJob(ctx, key)
			if err != nil {
				errs <- err
				return
			}
			ids <- job.ID
			createdCount <- created
		}()
	}
	group.Wait()
	close(ids)
	close(errs)
	close(createdCount)
	for err := range errs {
		t.Fatal(err)
	}
	var expected string
	for id := range ids {
		if expected == "" {
			expected = id
		}
		if id != expected {
			t.Fatalf("concurrent bootstrap requests returned different jobs: %s and %s", expected, id)
		}
	}
	created := 0
	for value := range createdCount {
		if value {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("expected one created bootstrap job, got %d", created)
	}
	database, err := pgx.Connect(ctx, runtimeTestPostgresURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close(ctx)
	var commandID string
	if err := database.QueryRow(ctx, `SELECT command_id FROM runtime.command_outbox WHERE command_type='bootstrap.start' AND resource_id=$1`, expected).Scan(&commandID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyBootstrapBatch(ctx, runtimecore.BootstrapBatch{CommandID: commandID, SyncID: expected, SnapshotID: "concurrency-test", BatchNo: 0, Checksum: "done", Done: true}); err != nil {
		t.Fatal(err)
	}
	var commandStatus string
	var deliveredAt *time.Time
	if err := database.QueryRow(ctx, `SELECT status,delivered_at FROM runtime.command_outbox WHERE command_id=$1`, commandID).Scan(&commandStatus, &deliveredAt); err != nil {
		t.Fatal(err)
	}
	if commandStatus != "delivered" || deliveredAt == nil {
		t.Fatalf("bootstrap command not finalized atomically: status=%s delivered_at=%v", commandStatus, deliveredAt)
	}
}

func TestCompletedBootstrapCommandRecoveredWithoutRedispatch(t *testing.T) {
	if os.Getenv("RUNTIME_INTEGRATION") != "1" {
		t.Skip("set RUNTIME_INTEGRATION=1")
	}
	ctx := context.Background()
	store, err := runtimecore.OpenPostgres(ctx, runtimeTestPostgresURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	broker, err := runtimecore.OpenRedis(ctx, "redis://default:codexremote@127.0.0.1:63799/0")
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	job, _, err := store.CreateSyncJob(ctx, "legacy-sync-key-"+protocol.NewID())
	if err != nil {
		t.Fatal(err)
	}
	cleanupSyncFixture(t, job.ID)
	if _, err := store.ApplyBootstrapBatch(ctx, runtimecore.BootstrapBatch{SyncID: job.ID, SnapshotID: "legacy-test", BatchNo: 0, Checksum: "done", Done: true}); err != nil {
		t.Fatal(err)
	}
	database, err := pgx.Connect(ctx, runtimeTestPostgresURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close(ctx)
	var commandID string
	if err := database.QueryRow(ctx, `SELECT command_id FROM runtime.command_outbox WHERE command_type='bootstrap.start' AND resource_id=$1 AND status='pending'`, job.ID).Scan(&commandID); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	relay := server.NewWithRuntime(config.Config{MaxMessageBytes: 256 * 1024, WriteQueueSize: 32, PingInterval: time.Second}, logger, store, broker)
	httpServer := httptest.NewServer(relay.Handler())
	defer func() { relay.CloseConnections("test complete"); httpServer.Close() }()
	agent, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws/agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.CloseNow()
	_ = readProtocol(t, agent)

	deadline := time.Now().Add(3 * time.Second)
	for {
		var status string
		if err := database.QueryRow(ctx, `SELECT status FROM runtime.command_outbox WHERE command_id=$1`, commandID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "delivered" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal bootstrap command not recovered: status=%s", status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func post(t *testing.T, url, key string, body any) map[string]any {
	t.Helper()
	data, _ := json.Marshal(body)
	request, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode >= 300 {
		t.Fatalf("POST %s: %d %#v", url, response.StatusCode, result)
	}
	return result
}
func nestedString(value map[string]any, keys ...string) string {
	var current any = value
	for _, key := range keys {
		current = current.(map[string]any)[key]
	}
	return current.(string)
}
func readProtocol(t *testing.T, connection *websocket.Conn) protocol.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_, data, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	message, err := protocol.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return message
}
func readUntil(t *testing.T, connection *websocket.Conn, messageType string) protocol.Message {
	t.Helper()
	for {
		message := readProtocol(t, connection)
		if message.Type == messageType {
			return message
		}
	}
}
func writeProtocol(t *testing.T, connection *websocket.Conn, message protocol.Message) {
	t.Helper()
	data, _ := json.Marshal(message)
	if err := connection.Write(context.Background(), websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func cleanupProjectFixture(t *testing.T, projectID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		database, err := pgx.Connect(ctx, runtimeTestPostgresURL)
		if err != nil {
			t.Errorf("connect for project fixture cleanup: %v", err)
			return
		}
		defer database.Close(ctx)
		tx, err := database.Begin(ctx)
		if err != nil {
			t.Errorf("begin project fixture cleanup: %v", err)
			return
		}
		defer tx.Rollback(ctx)
		statements := []string{
			`DELETE FROM runtime.command_outbox WHERE resource_id IN (SELECT r.run_id FROM runtime.runs r JOIN runtime.sessions s ON s.session_id=r.session_id WHERE s.project_id=$1)`,
			`DELETE FROM runtime.run_events WHERE run_id IN (SELECT r.run_id FROM runtime.runs r JOIN runtime.sessions s ON s.session_id=r.session_id WHERE s.project_id=$1)`,
			`DELETE FROM runtime.session_events WHERE session_id IN (SELECT session_id FROM runtime.sessions WHERE project_id=$1)`,
			`DELETE FROM runtime.runs WHERE session_id IN (SELECT session_id FROM runtime.sessions WHERE project_id=$1)`,
			`DELETE FROM runtime.sessions WHERE project_id=$1`,
			`DELETE FROM runtime.projects WHERE project_id=$1`,
		}
		for _, statement := range statements {
			if _, err := tx.Exec(ctx, statement, projectID); err != nil {
				t.Errorf("clean project fixture %s: %v", projectID, err)
				return
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("commit project fixture cleanup %s: %v", projectID, err)
		}
	})
}

func cleanupSyncFixture(t *testing.T, syncID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		database, err := pgx.Connect(ctx, runtimeTestPostgresURL)
		if err != nil {
			t.Errorf("connect for sync fixture cleanup: %v", err)
			return
		}
		defer database.Close(ctx)
		tx, err := database.Begin(ctx)
		if err != nil {
			t.Errorf("begin sync fixture cleanup: %v", err)
			return
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `DELETE FROM runtime.command_outbox WHERE command_type='bootstrap.start' AND resource_id=$1`, syncID); err != nil {
			t.Errorf("clean sync command fixture %s: %v", syncID, err)
			return
		}
		if _, err := tx.Exec(ctx, `DELETE FROM runtime.sync_jobs WHERE sync_id=$1`, syncID); err != nil {
			t.Errorf("clean sync fixture %s: %v", syncID, err)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("commit sync fixture cleanup %s: %v", syncID, err)
		}
	})
}

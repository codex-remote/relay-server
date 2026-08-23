package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ai-coding-remote/relay-server/internal/config"
	"github.com/ai-coding-remote/relay-server/internal/protocol"
	"github.com/coder/websocket"
)

func testServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	relay := New(config.Config{MaxMessageBytes: 256 * 1024, WriteQueueSize: 16, PingInterval: time.Second}, logger)
	httpServer := httptest.NewServer(relay.Handler())
	t.Cleanup(func() {
		relay.CloseConnections("test complete")
		httpServer.Close()
	})
	return httpServer, "ws" + strings.TrimPrefix(httpServer.URL, "http")
}

func TestHealthAndStatus(t *testing.T) {
	httpServer, _ := testServer(t)
	response, err := http.Get(httpServer.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}
}

func TestRuntimeScopePolicyBelongsToServerComposition(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   string
	}{
		{http.MethodGet, "/v1/runtime/projects", "runtime:read"},
		{http.MethodHead, "/v1/runtime/projects", "runtime:read"},
		{http.MethodPost, "/v1/runtime/sessions", "runtime:write"},
		{http.MethodPost, "/v1/runtime/projects/p1/source:read", "source:read"},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, nil)
		if got := runtimeRequiredScope(request); got != test.want {
			t.Errorf("%s %s scope = %q, want %q", test.method, test.path, got, test.want)
		}
	}
}

func TestCORSUsesExplicitOriginAllowlist(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	relay := New(config.Config{MaxMessageBytes: 256 * 1024, WriteQueueSize: 16, PingInterval: time.Second, AllowedOrigin: "http://127.0.0.1:4173, http://192.168.0.108:4173"}, logger)
	t.Cleanup(func() { relay.CloseConnections("test complete") })

	for _, test := range []struct {
		origin string
		want   string
	}{{"http://127.0.0.1:4173", "http://127.0.0.1:4173"}, {"http://192.168.0.108:4173", "http://192.168.0.108:4173"}, {"https://untrusted.example", ""}} {
		request := httptest.NewRequest(http.MethodOptions, "/v1/runtime/projects", nil)
		request.Header.Set("Origin", test.origin)
		response := httptest.NewRecorder()
		relay.Handler().ServeHTTP(response, request)
		if got := response.Header().Get("Access-Control-Allow-Origin"); got != test.want {
			t.Errorf("origin %s: Access-Control-Allow-Origin = %q, want %q", test.origin, got, test.want)
		}
	}
}

func TestCORSAllowsAnyOriginWhenExplicitlyConfigured(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	relay := New(config.Config{MaxMessageBytes: 256 * 1024, WriteQueueSize: 16, PingInterval: time.Second, AllowedOrigin: "*"}, logger)
	t.Cleanup(func() { relay.CloseConnections("test complete") })

	request := httptest.NewRequest(http.MethodOptions, "/v1/runtime/projects", nil)
	request.Header.Set("Origin", "http://192.168.1.7:4174")
	response := httptest.NewRecorder()
	relay.Handler().ServeHTTP(response, request)

	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want empty", got)
	}
}

func TestWebSocketRoutesMessagesBothDirections(t *testing.T) {
	_, baseURL := testServer(t)
	ctx := context.Background()
	app, _, err := websocket.Dial(ctx, baseURL+"/ws/app", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.CloseNow()
	readMessage(t, app)
	agent, _, err := websocket.Dial(ctx, baseURL+"/ws/agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.CloseNow()
	hello, _ := protocol.NewMessage(protocol.TypeAgentHello, "agent", protocol.Sender{Kind: "device", ID: "mac"}, protocol.AgentHelloPayload{Name: "Mac", Status: "idle"})
	status, _ := protocol.NewMessage(protocol.TypeAgentStatus, "agent", protocol.Sender{Kind: "device", ID: "mac"}, protocol.AgentStatusPayload{Status: "idle"})
	writeMessage(t, agent, hello)
	writeMessage(t, agent, status)
	if readMessage(t, app).Type != protocol.TypeAgentHello || readMessage(t, app).Type != protocol.TypeAgentStatus {
		t.Fatal("App did not receive Agent state")
	}
	turnStart, _ := protocol.NewMessage(protocol.TypeTurnStart, "trace-1", protocol.Sender{Kind: "spoofed", ID: "spoofed"}, protocol.TurnStartPayload{ProjectID: "project-1", Prompt: "fix"})
	writeMessage(t, app, turnStart)
	forwarded := readMessage(t, agent)
	if forwarded.Type != protocol.TypeTurnStart || forwarded.Sender.Kind != "user" {
		t.Fatalf("forwarded to Agent = %#v", forwarded)
	}

	output, _ := protocol.NewMessage(protocol.TypeTurnOutput, "trace-1", protocol.Sender{Kind: "spoofed", ID: "spoofed"}, protocol.TurnOutputPayload{ProjectID: "project-1", ThreadID: "thread-1", TurnID: "turn-1", Stream: "stdout", Text: "done\n"})
	writeMessage(t, agent, output)
	forwarded = readMessage(t, app)
	if forwarded.Type != protocol.TypeTurnOutput || forwarded.Sender.Kind != "device" {
		t.Fatalf("forwarded to App = %#v", forwarded)
	}
}

func TestTurnAcknowledgementIsRoutedAndExposedInStatus(t *testing.T) {
	httpServer, baseURL := testServer(t)
	ctx := context.Background()
	app, _, err := websocket.Dial(ctx, baseURL+"/ws/app", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.CloseNow()
	readMessage(t, app)
	agent, _, err := websocket.Dial(ctx, baseURL+"/ws/agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.CloseNow()

	acknowledged, _ := protocol.NewMessage(
		protocol.TypeTurnAcknowledged,
		"trace-1",
		protocol.Sender{Kind: "user", ID: "iphone"},
		protocol.TurnAcknowledgedPayload{TurnID: "turn-1", Status: "completed"},
	)
	writeMessage(t, app, acknowledged)
	forwarded := readMessage(t, agent)
	if forwarded.Type != protocol.TypeTurnAcknowledged {
		t.Fatalf("forwarded acknowledgement = %#v", forwarded)
	}

	response, err := http.Get(httpServer.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var status map[string]any
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status["last_turn_acknowledged"] != "turn-1" || status["last_turn_acknowledged_status"] != "completed" {
		t.Fatalf("status = %#v", status)
	}
	if status["app_connection_id"] == nil {
		t.Fatalf("missing App connection generation: %#v", status)
	}
}

func TestWebSocketRejectsTurnWhenAgentOffline(t *testing.T) {
	_, baseURL := testServer(t)
	app, _, err := websocket.Dial(context.Background(), baseURL+"/ws/app", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.CloseNow()
	if status := readMessage(t, app); status.Type != protocol.TypeAgentStatus {
		t.Fatalf("initial message = %#v", status)
	}
	turnStart, _ := protocol.NewMessage(protocol.TypeTurnStart, "trace-1", protocol.Sender{Kind: "user", ID: "test"}, protocol.TurnStartPayload{ProjectID: "project-1", Prompt: "fix"})
	writeMessage(t, app, turnStart)
	rejection := readMessage(t, app)
	payload, _ := protocol.PayloadAs[protocol.TurnRejectedPayload](rejection)
	if rejection.Type != protocol.TypeTurnRejected || payload.Code != "AGENT_OFFLINE" {
		t.Fatalf("rejection = %#v %#v", rejection, payload)
	}
}

func TestWebSocketRejectsMalformedJSONWithoutStoppingServer(t *testing.T) {
	httpServer, baseURL := testServer(t)
	app, _, err := websocket.Dial(context.Background(), baseURL+"/ws/app", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.CloseNow()
	readMessage(t, app)
	if err := app.Write(context.Background(), websocket.MessageText, []byte("{")); err != nil {
		t.Fatal(err)
	}
	rejection := readMessage(t, app)
	if rejection.Type != protocol.TypeTurnRejected {
		t.Fatalf("rejection = %#v", rejection)
	}
	response, err := http.Get(httpServer.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}
}

func TestNewAppConnectionReplacesOldConnection(t *testing.T) {
	_, baseURL := testServer(t)
	first, _, err := websocket.Dial(context.Background(), baseURL+"/ws/app", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.CloseNow()
	readMessage(t, first)
	second, _, err := websocket.Dial(context.Background(), baseURL+"/ws/app", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.CloseNow()
	readMessage(t, second)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := first.Read(ctx); err == nil {
		t.Fatal("superseded App connection remained open")
	}
}

func TestAgentDisconnectNotifiesApp(t *testing.T) {
	_, baseURL := testServer(t)
	app, _, err := websocket.Dial(context.Background(), baseURL+"/ws/app", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.CloseNow()
	readMessage(t, app)
	agent, _, err := websocket.Dial(context.Background(), baseURL+"/ws/agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	hello, _ := protocol.NewMessage(protocol.TypeAgentHello, "agent", protocol.Sender{Kind: "device", ID: "mac"}, protocol.AgentHelloPayload{Name: "Mac", Status: "idle"})
	writeMessage(t, agent, hello)
	readMessage(t, app)
	agent.CloseNow()
	offline := readMessage(t, app)
	payload, _ := protocol.PayloadAs[protocol.AgentStatusPayload](offline)
	if offline.Type != protocol.TypeAgentStatus || payload.Status != "offline" {
		t.Fatalf("offline message = %#v %#v", offline, payload)
	}
}

func writeMessage(t *testing.T, connection *websocket.Conn, message protocol.Message) {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(context.Background(), websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func readMessage(t *testing.T, connection *websocket.Conn) protocol.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
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

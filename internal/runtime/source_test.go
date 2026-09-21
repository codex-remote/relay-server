package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codex-remote/relay-server/internal/hub"
	"github.com/codex-remote/relay-server/internal/protocol"
)

type sourceTestRegistry struct{ peer hub.Peer }

func (r sourceTestRegistry) Peer(string) hub.Peer { return r.peer }

type sourceTestPeer struct{ send func(protocol.Message) error }

func (p sourceTestPeer) ID() uint64                          { return 1 }
func (p sourceTestPeer) Role() string                        { return protocol.RoleAgent }
func (p sourceTestPeer) Send(message protocol.Message) error { return p.send(message) }
func (p sourceTestPeer) Close(string)                        {}

func TestSourceGatewayCorrelatesAgentSnapshot(t *testing.T) {
	var gateway *SourceGateway
	peer := sourceTestPeer{send: func(request protocol.Message) error {
		response, _ := protocol.NewMessage(protocol.TypeSourceSnapshot, request.TraceID, protocol.Sender{Kind: "device", ID: "mac"}, protocol.SourceSnapshotPayload{
			ProjectID: "project-1", Path: "src/main.go", Content: "package main", StartLine: 1, EndLine: 1, TotalLines: 1,
		})
		gateway.Handle(response)
		return nil
	}}
	gateway = NewSourceGateway(sourceTestRegistry{peer: peer})
	snapshot, err := gateway.Read(context.Background(), protocol.SourceReadPayload{ProjectID: "project-1", Path: "src/main.go"})
	if err != nil || snapshot.Path != "src/main.go" || snapshot.Content != "package main" {
		t.Fatalf("unexpected source result: %#v %v", snapshot, err)
	}
}

func TestSourceGatewayReturnsStructuredAgentFailure(t *testing.T) {
	var gateway *SourceGateway
	peer := sourceTestPeer{send: func(request protocol.Message) error {
		response, _ := protocol.NewMessage(protocol.TypeSourceReadFailed, request.TraceID, protocol.Sender{Kind: "device", ID: "mac"}, protocol.SourceReadFailedPayload{Code: "SOURCE_FORBIDDEN", Message: "forbidden"})
		gateway.Handle(response)
		return nil
	}}
	gateway = NewSourceGateway(sourceTestRegistry{peer: peer})
	_, err := gateway.Read(context.Background(), protocol.SourceReadPayload{ProjectID: "project-1", Path: ".env"})
	var remote *SourceRemoteError
	if !errors.As(err, &remote) || remote.Code != "SOURCE_FORBIDDEN" {
		t.Fatalf("unexpected source error: %v", err)
	}
}

func TestSourceGatewayRejectsOfflineAgent(t *testing.T) {
	gateway := NewSourceGateway(sourceTestRegistry{})
	if _, err := gateway.Read(context.Background(), protocol.SourceReadPayload{ProjectID: "project-1", Path: "main.go"}); !errors.Is(err, ErrSourceAgentOffline) {
		t.Fatalf("unexpected offline error: %v", err)
	}
}

func TestSourceGatewayBoundsConcurrentReads(t *testing.T) {
	gateway := NewSourceGateway(sourceTestRegistry{peer: sourceTestPeer{send: func(protocol.Message) error { return nil }}})
	for index := 0; index < maxPendingSourceReads; index++ {
		gateway.pending[protocol.NewID()] = make(chan sourceResult, 1)
	}
	if _, err := gateway.Read(context.Background(), protocol.SourceReadPayload{ProjectID: "project-1", Path: "main.go"}); !errors.Is(err, ErrSourceBusy) {
		t.Fatalf("unexpected concurrency error: %v", err)
	}
}

type sourceTestProvider struct {
	snapshot protocol.SourceSnapshotPayload
	err      error
	reads    *int
}

func (p sourceTestProvider) Read(context.Context, protocol.SourceReadPayload) (protocol.SourceSnapshotPayload, error) {
	if p.reads != nil {
		*p.reads = *p.reads + 1
	}
	return p.snapshot, p.err
}

func TestProjectSourceReturnsSnapshotFromProvider(t *testing.T) {
	reads := 0
	api := NewAPIWithSource(nil, nil, sourceTestProvider{reads: &reads, snapshot: protocol.SourceSnapshotPayload{
		ProjectID: "project-1", Path: "main.go", Content: "package main", StartLine: 1, EndLine: 1, TotalLines: 1,
	}})
	mux := http.NewServeMux()
	api.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/runtime/projects/project-1/source:read", strings.NewReader(`{"path":"main.go","line":1}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "package main") || reads != 1 {
		t.Fatalf("status=%d reads=%d body=%s", response.Code, reads, response.Body.String())
	}
}

func TestProjectSourceStaysDisabledWithoutSourceProvider(t *testing.T) {
	api := NewAPIWithSource(nil, nil, nil)
	mux := http.NewServeMux()
	api.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/runtime/projects/project-1/source:read", strings.NewReader(`{"path":"main.go","line":1}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "SOURCE_READ_DISABLED") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestProjectSourceReturnsAgentSnapshot(t *testing.T) {
	provider := sourceTestProvider{snapshot: protocol.SourceSnapshotPayload{
		ProjectID: "project-1", Path: "main.go", Content: "package main", StartLine: 1, EndLine: 1, TotalLines: 1,
	}}
	api := NewAPIWithSource(nil, nil, provider)
	mux := http.NewServeMux()
	api.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/runtime/projects/project-1/source:read", strings.NewReader(`{"path":"main.go","line":1}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "package main") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestProjectSourceMapsAgentFailures(t *testing.T) {
	api := NewAPIWithSource(nil, nil, sourceTestProvider{err: &SourceRemoteError{Code: "SOURCE_FORBIDDEN"}})
	mux := http.NewServeMux()
	api.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/runtime/projects/project-1/source:read", strings.NewReader(`{"path":".env"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "SOURCE_FORBIDDEN") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

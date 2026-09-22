package runtime

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/codex-remote/relay-server/internal/protocol"
)

type coordinatorProjectStore struct {
	Store
	projects []Project
	runs     map[string]Run
}

func (s *coordinatorProjectStore) UpsertProjects(_ context.Context, projects []Project) error {
	s.projects = append(s.projects, projects...)
	return nil
}

func (s *coordinatorProjectStore) GetRun(_ context.Context, id string, _ bool) (Run, error) {
	if run, ok := s.runs[id]; ok {
		return run, nil
	}
	return Run{}, ErrNotFound
}

func TestCoordinatorForwardsAppProjectSnapshots(t *testing.T) {
	store := &coordinatorProjectStore{}
	coordinator := &Coordinator{ctx: context.Background(), store: store, logger: slog.Default()}
	message, err := protocol.NewMessage(protocol.TypeProjectSnapshot, "app-request", protocol.Sender{Kind: "device", ID: "mac"}, protocol.ProjectSnapshotPayload{
		Projects: []protocol.Project{{ID: "project-1", Name: "Example"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if coordinator.AgentMessage(nil, message) {
		t.Fatal("App-requested project snapshot was consumed by the Runtime coordinator")
	}
	if len(store.projects) != 0 {
		t.Fatalf("App-requested projects were persisted: %#v", store.projects)
	}
}

func TestCoordinatorConsumesInternalProjectRefresh(t *testing.T) {
	store := &coordinatorProjectStore{}
	coordinator := &Coordinator{ctx: context.Background(), store: store, logger: slog.Default()}
	message, err := protocol.NewMessage(protocol.TypeProjectSnapshot, projectRefreshTraceID, protocol.Sender{Kind: "device", ID: "mac"}, protocol.ProjectSnapshotPayload{
		Projects: []protocol.Project{{ID: "project-1", Name: "Example"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !coordinator.AgentMessage(nil, message) {
		t.Fatal("internal project refresh was not consumed")
	}
	if len(store.projects) != 1 || store.projects[0].ID != "project-1" {
		t.Fatalf("persisted projects = %#v", store.projects)
	}
}

type coordinatorAckPeer struct{ messages []protocol.Message }

func (p *coordinatorAckPeer) ID() uint64   { return 1 }
func (p *coordinatorAckPeer) Role() string { return protocol.RoleAgent }
func (p *coordinatorAckPeer) Send(message protocol.Message) error {
	p.messages = append(p.messages, message)
	return nil
}
func (p *coordinatorAckPeer) Close(string) {}

func TestCoordinatorForwardsAndAcknowledgesLegacyWebSocketRun(t *testing.T) {
	store := &coordinatorProjectStore{runs: make(map[string]Run)}
	coordinator := &Coordinator{ctx: context.Background(), store: store, logger: slog.Default()}
	message, err := protocol.NewMessage(protocol.TypeTurnCompleted, "legacy-run", protocol.Sender{Kind: "device", ID: "mac"}, protocol.TurnCompletedPayload{})
	if err != nil {
		t.Fatal(err)
	}
	message.AgentSequence = 7
	if coordinator.AgentMessage(nil, message) {
		t.Fatal("legacy WebSocket Run event was consumed by the Runtime coordinator")
	}
	peer := &coordinatorAckPeer{}
	coordinator.AgentMessageForwarded(peer, message)
	if len(peer.messages) != 2 || peer.messages[0].Type != protocol.TypeRuntimeReceivedAck || peer.messages[1].Type != protocol.TypeRuntimeDurableAck {
		t.Fatalf("ack messages = %#v", peer.messages)
	}
}

func TestBootstrapBatchImportsOnlyTerminalTurns(t *testing.T) {
	now := time.Now().UTC()
	payload := protocol.BootstrapBatchPayload{
		Thread: &protocol.ThreadDetail{
			ID: "thread-1", ProjectID: "project-1", Title: "History", CreatedAt: now, UpdatedAt: now,
			Turns: []protocol.ThreadHistoryTurn{
				{ID: "completed", Status: "completed"},
				{ID: "running", Status: "running"},
				{ID: "unknown", Status: "mystery"},
				{ID: "failed", Status: "failed"},
				{ID: "canceled", Status: "canceled"},
			},
		},
	}

	batch := bootstrapBatch(payload)
	if len(batch.Runs) != 3 {
		t.Fatalf("terminal runs = %d, want 3: %#v", len(batch.Runs), batch.Runs)
	}
	for index, expected := range []string{"completed", "failed", "canceled"} {
		if batch.Runs[index].Status != expected {
			t.Fatalf("run %d status = %q, want %q", index, batch.Runs[index].Status, expected)
		}
	}
}

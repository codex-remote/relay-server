package runtime

import (
	"context"
	"testing"

	"github.com/codex-remote/relay-server/internal/protocol"
)

func TestExecutionProfileGatewayCorrelatesAgentSnapshot(t *testing.T) {
	var gateway *ExecutionProfileGateway
	peer := sourceTestPeer{send: func(request protocol.Message) error {
		if request.Type != protocol.TypeExecutionProfileList {
			t.Fatalf("request type = %s", request.Type)
		}
		response, _ := protocol.NewMessage(
			protocol.TypeExecutionProfileSnapshot,
			request.TraceID,
			protocol.Sender{Kind: "device", ID: "mac"},
			protocol.ExecutionProfileSnapshotPayload{
				ProjectID: "project-1", DefaultProfileID: ":workspace",
				Profiles: []protocol.ExecutionProfile{{ID: ":workspace", Allowed: true}},
			},
		)
		gateway.Handle(response)
		return nil
	}}
	gateway = NewExecutionProfileGateway(sourceTestRegistry{peer: peer})
	snapshot, err := gateway.List(context.Background(), "project-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.DefaultProfileID != ":workspace" || len(snapshot.Profiles) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestExecutionProfileGatewayRejectsOfflineAgent(t *testing.T) {
	gateway := NewExecutionProfileGateway(sourceTestRegistry{})
	if _, err := gateway.List(context.Background(), "project-1"); err != ErrExecutionProfilesAgentOffline {
		t.Fatalf("error = %v", err)
	}
}

func TestExecutionProfileGatewayLeavesLegacyResponseForRouter(t *testing.T) {
	gateway := NewExecutionProfileGateway(sourceTestRegistry{})
	message, _ := protocol.NewMessage(
		protocol.TypeExecutionProfileSnapshot,
		"legacy-app-trace",
		protocol.Sender{Kind: "device", ID: "mac"},
		protocol.ExecutionProfileSnapshotPayload{ProjectID: "project-1"},
	)
	if gateway.Handle(message) {
		t.Fatal("unmatched legacy response was consumed")
	}
}

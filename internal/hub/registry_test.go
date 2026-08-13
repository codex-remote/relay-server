package hub

import (
	"testing"

	"github.com/ai-coding-remote/relay-server/internal/protocol"
)

type fakePeer struct {
	id     uint64
	role   string
	closed bool
}

func (p *fakePeer) ID() uint64                  { return p.id }
func (p *fakePeer) Role() string                { return p.role }
func (p *fakePeer) Send(protocol.Message) error { return nil }
func (p *fakePeer) Close(string)                { p.closed = true }

func TestRegistryReplacementUsesConnectionID(t *testing.T) {
	registry := NewRegistry()
	first := &fakePeer{id: 1, role: protocol.RoleAgent}
	second := &fakePeer{id: 2, role: protocol.RoleAgent}
	if old := registry.Replace(first); old != nil {
		t.Fatalf("first Replace() old = %#v", old)
	}
	if old := registry.Replace(second); old != first {
		t.Fatalf("second Replace() old = %#v", old)
	}
	if registry.Remove(first) {
		t.Fatal("removing superseded peer removed the current connection")
	}
	if registry.Peer(protocol.RoleAgent) != second {
		t.Fatal("current Agent changed unexpectedly")
	}
	if !registry.Remove(second) || registry.Snapshot().AgentConnected {
		t.Fatal("current Agent was not removed")
	}
}

func TestRegistryCachesOnlyAgentState(t *testing.T) {
	registry := NewRegistry()
	hello, _ := protocol.NewMessage(protocol.TypeAgentHello, "agent", protocol.Sender{Kind: "device", ID: "mac"}, protocol.AgentHelloPayload{Name: "Mac"})
	status, _ := protocol.NewMessage(protocol.TypeAgentStatus, "agent", protocol.Sender{Kind: "device", ID: "mac"}, protocol.AgentStatusPayload{Status: "idle"})
	capabilities, _ := protocol.NewMessage(protocol.TypeAgentCapabilities, "agent", protocol.Sender{Kind: "device", ID: "mac"}, protocol.AgentCapabilitiesPayload{Restricted: true, SandboxMode: "workspace-write"})
	registry.RememberAgentState(hello)
	registry.RememberAgentState(status)
	registry.RememberAgentState(capabilities)
	state := registry.AgentState()
	if len(state) != 3 || state[0].Type != protocol.TypeAgentHello || state[1].Type != protocol.TypeAgentStatus || state[2].Type != protocol.TypeAgentCapabilities {
		t.Fatalf("AgentState() = %#v", state)
	}
}

func TestRegistrySnapshotIncludesAppConnectionAndLatestTurnAcknowledgement(t *testing.T) {
	registry := NewRegistry()
	app := &fakePeer{id: 42, role: protocol.RoleApp}
	registry.Replace(app)
	acknowledged, _ := protocol.NewMessage(
		protocol.TypeTurnAcknowledged,
		"trace-1",
		protocol.Sender{Kind: "user", ID: "iphone"},
		protocol.TurnAcknowledgedPayload{TurnID: "turn-1", Status: "completed"},
	)
	registry.RememberAppState(acknowledged)

	snapshot := registry.Snapshot()
	if !snapshot.AppConnected || snapshot.AppConnectionID != 42 {
		t.Fatalf("unexpected App snapshot: %#v", snapshot)
	}
	if snapshot.LastTurnAcknowledged != "turn-1" || snapshot.LastTurnAcknowledgedStatus != "completed" {
		t.Fatalf("unexpected acknowledgement snapshot: %#v", snapshot)
	}
}

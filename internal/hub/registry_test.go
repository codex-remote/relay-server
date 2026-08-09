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
	registry.RememberAgentState(hello)
	registry.RememberAgentState(status)
	state := registry.AgentState()
	if len(state) != 2 || state[0].Type != protocol.TypeAgentHello || state[1].Type != protocol.TypeAgentStatus {
		t.Fatalf("AgentState() = %#v", state)
	}
}

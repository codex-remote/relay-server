package router

import (
	"errors"
	"testing"

	"github.com/ai-coding-remote/relay-server/internal/hub"
	"github.com/ai-coding-remote/relay-server/internal/protocol"
)

type testPeer struct {
	id       uint64
	role     string
	messages []protocol.Message
	sendErr  error
	closed   bool
}

func (p *testPeer) ID() uint64   { return p.id }
func (p *testPeer) Role() string { return p.role }
func (p *testPeer) Send(m protocol.Message) error {
	p.messages = append(p.messages, m)
	return p.sendErr
}
func (p *testPeer) Close(string) { p.closed = true }

func TestRouterForwardsAndOverridesSender(t *testing.T) {
	registry := hub.NewRegistry()
	router := New(registry, nil)
	app := &testPeer{id: 1, role: protocol.RoleApp}
	agent := &testPeer{id: 2, role: protocol.RoleAgent}
	router.Connect(agent)
	router.Connect(app)
	app.messages = nil
	message, _ := protocol.NewMessage(protocol.TypeTurnStart, "trace-1", protocol.Sender{Kind: "spoofed", ID: "spoofed"}, protocol.TurnStartPayload{ProjectID: "project-1", Prompt: "fix"})
	router.Handle(app, message)
	if len(agent.messages) != 1 || agent.messages[0].Sender != (protocol.Sender{Kind: "user", ID: "local-user"}) {
		t.Fatalf("Agent messages = %#v", agent.messages)
	}
}

func TestRouterRejectsWhenAgentOffline(t *testing.T) {
	registry := hub.NewRegistry()
	router := New(registry, nil)
	app := &testPeer{id: 1, role: protocol.RoleApp}
	router.Connect(app)
	app.messages = nil
	message, _ := protocol.NewMessage(protocol.TypeTurnStart, "trace-1", protocol.Sender{Kind: "user", ID: "test"}, protocol.TurnStartPayload{ProjectID: "project-1", Prompt: "fix"})
	router.Handle(app, message)
	if len(app.messages) != 1 || app.messages[0].Type != protocol.TypeTurnRejected {
		t.Fatalf("App messages = %#v", app.messages)
	}
	payload, _ := protocol.PayloadAs[protocol.TurnRejectedPayload](app.messages[0])
	if payload.Code != "AGENT_OFFLINE" {
		t.Fatalf("rejection = %#v", payload)
	}
}

func TestRouterClosesSlowTarget(t *testing.T) {
	registry := hub.NewRegistry()
	router := New(registry, nil)
	app := &testPeer{id: 1, role: protocol.RoleApp}
	agent := &testPeer{id: 2, role: protocol.RoleAgent, sendErr: errors.New("full")}
	router.Connect(agent)
	router.Connect(app)
	message, _ := protocol.NewMessage(protocol.TypeTurnStart, "trace-1", protocol.Sender{Kind: "user", ID: "test"}, protocol.TurnStartPayload{ProjectID: "project-1", Prompt: "fix"})
	router.Handle(app, message)
	if !agent.closed {
		t.Fatal("slow Agent was not closed")
	}
}

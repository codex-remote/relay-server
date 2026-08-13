package hub

import (
	"errors"
	"sync"

	"github.com/ai-coding-remote/relay-server/internal/protocol"
)

var ErrSendQueueFull = errors.New("connection send queue is full")

type Peer interface {
	ID() uint64
	Role() string
	Send(protocol.Message) error
	Close(string)
}

type Snapshot struct {
	AppConnected               bool   `json:"app_connected"`
	AgentConnected             bool   `json:"agent_connected"`
	AppConnectionID            uint64 `json:"app_connection_id,omitempty"`
	LastTurnAcknowledged       string `json:"last_turn_acknowledged,omitempty"`
	LastTurnAcknowledgedStatus string `json:"last_turn_acknowledged_status,omitempty"`
}

type Registry struct {
	mu                sync.RWMutex
	app               Peer
	agent             Peer
	agentHello        *protocol.Message
	agentStatus       *protocol.Message
	agentCapabilities *protocol.Message
	lastTurnAck       protocol.TurnAcknowledgedPayload
}

func NewRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) Replace(peer Peer) Peer {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch peer.Role() {
	case protocol.RoleApp:
		old := r.app
		r.app = peer
		return old
	case protocol.RoleAgent:
		old := r.agent
		r.agent = peer
		r.agentHello = nil
		r.agentStatus = nil
		r.agentCapabilities = nil
		return old
	default:
		return nil
	}
}

func (r *Registry) Remove(peer Peer) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch peer.Role() {
	case protocol.RoleApp:
		if r.app == nil || r.app.ID() != peer.ID() {
			return false
		}
		r.app = nil
		return true
	case protocol.RoleAgent:
		if r.agent == nil || r.agent.ID() != peer.ID() {
			return false
		}
		r.agent = nil
		r.agentHello = nil
		r.agentStatus = nil
		r.agentCapabilities = nil
		return true
	default:
		return false
	}
}

func (r *Registry) Peer(role string) Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if role == protocol.RoleAgent {
		return r.agent
	}
	if role == protocol.RoleApp {
		return r.app
	}
	return nil
}

func (r *Registry) RememberAgentState(message protocol.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := message
	switch message.Type {
	case protocol.TypeAgentHello:
		r.agentHello = &copy
	case protocol.TypeAgentStatus:
		r.agentStatus = &copy
	case protocol.TypeAgentCapabilities:
		r.agentCapabilities = &copy
	}
}

func (r *Registry) RememberAppState(message protocol.Message) {
	if message.Type != protocol.TypeTurnAcknowledged {
		return
	}
	payload, err := protocol.PayloadAs[protocol.TurnAcknowledgedPayload](message)
	if err != nil || payload.TurnID == "" {
		return
	}
	r.mu.Lock()
	r.lastTurnAck = payload
	r.mu.Unlock()
}

func (r *Registry) AgentState() []protocol.Message {
	r.mu.RLock()
	defer r.mu.RUnlock()
	messages := make([]protocol.Message, 0, 3)
	if r.agentHello != nil {
		messages = append(messages, *r.agentHello)
	}
	if r.agentStatus != nil {
		messages = append(messages, *r.agentStatus)
	}
	if r.agentCapabilities != nil {
		messages = append(messages, *r.agentCapabilities)
	}
	return messages
}

func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot := Snapshot{
		AppConnected:               r.app != nil,
		AgentConnected:             r.agent != nil,
		LastTurnAcknowledged:       r.lastTurnAck.TurnID,
		LastTurnAcknowledgedStatus: r.lastTurnAck.Status,
	}
	if r.app != nil {
		snapshot.AppConnectionID = r.app.ID()
	}
	return snapshot
}

func (r *Registry) CloseAll(reason string) {
	r.mu.Lock()
	app := r.app
	agent := r.agent
	r.app = nil
	r.agent = nil
	r.agentHello = nil
	r.agentStatus = nil
	r.agentCapabilities = nil
	r.mu.Unlock()
	if app != nil {
		app.Close(reason)
	}
	if agent != nil {
		agent.Close(reason)
	}
}

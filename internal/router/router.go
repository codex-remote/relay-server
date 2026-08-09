package router

import (
	"log/slog"

	"github.com/ai-coding-remote/relay-server/internal/hub"
	"github.com/ai-coding-remote/relay-server/internal/protocol"
)

type Router struct {
	registry *hub.Registry
	logger   *slog.Logger
}

func New(registry *hub.Registry, logger *slog.Logger) *Router {
	if logger == nil {
		logger = slog.Default()
	}
	return &Router{registry: registry, logger: logger}
}

func (r *Router) Connect(peer hub.Peer) {
	old := r.registry.Replace(peer)
	if old != nil {
		old.Close("replaced by a newer connection")
	}
	r.logger.Info("WebSocket connected", "role", peer.Role(), "connection_id", peer.ID())
	if peer.Role() != protocol.RoleApp {
		return
	}
	if r.registry.Peer(protocol.RoleAgent) == nil {
		r.sendAgentOffline(peer, "agent")
		return
	}
	for _, message := range r.registry.AgentState() {
		if err := peer.Send(message); err != nil {
			peer.Close("send queue full")
			return
		}
	}
}

func (r *Router) Disconnect(peer hub.Peer) {
	if !r.registry.Remove(peer) {
		return
	}
	r.logger.Info("WebSocket disconnected", "role", peer.Role(), "connection_id", peer.ID())
	if peer.Role() == protocol.RoleAgent {
		if app := r.registry.Peer(protocol.RoleApp); app != nil {
			r.sendAgentOffline(app, "agent")
		}
	}
}

func (r *Router) Handle(peer hub.Peer, message protocol.Message) {
	if !protocol.AllowedFrom(peer.Role(), message.Type) {
		r.Reject(peer, message.TraceID, "MESSAGE_INVALID", "message type is not allowed from this connection")
		return
	}
	message.Sender = protocol.CanonicalSender(peer.Role())
	if peer.Role() == protocol.RoleAgent {
		r.registry.RememberAgentState(message)
	}
	targetRole := protocol.RoleAgent
	if peer.Role() == protocol.RoleAgent {
		targetRole = protocol.RoleApp
	}
	target := r.registry.Peer(targetRole)
	if target == nil {
		if peer.Role() == protocol.RoleApp {
			r.Reject(peer, message.TraceID, "AGENT_OFFLINE", "Mac Agent is not connected")
		}
		return
	}
	if err := target.Send(message); err != nil {
		r.logger.Warn("Closing slow WebSocket", "role", target.Role(), "connection_id", target.ID(), "message_type", message.Type)
		target.Close("send queue full")
		if peer.Role() == protocol.RoleApp {
			r.Reject(peer, message.TraceID, "INTERNAL_ERROR", "Mac Agent connection is unavailable")
		}
		return
	}
	r.logger.Debug("Message routed", "source", peer.Role(), "target", targetRole, "message_type", message.Type, "message_id", message.MessageID)
}

func (r *Router) Reject(peer hub.Peer, traceID, code, message string) {
	rejection, err := protocol.NewMessage(protocol.TypeTurnRejected, traceID, protocol.Sender{Kind: "relay", ID: "local-relay"}, protocol.TurnRejectedPayload{
		Code: code, Message: message,
	})
	if err != nil {
		r.logger.Error("Create rejection", "error", err)
		return
	}
	if err := peer.Send(rejection); err != nil {
		peer.Close("send queue full")
	}
}

func (r *Router) sendAgentOffline(peer hub.Peer, traceID string) {
	message, err := protocol.NewMessage(protocol.TypeAgentStatus, traceID, protocol.Sender{Kind: "relay", ID: "local-relay"}, protocol.AgentStatusPayload{Status: "offline"})
	if err != nil {
		return
	}
	if err := peer.Send(message); err != nil {
		peer.Close("send queue full")
	}
}

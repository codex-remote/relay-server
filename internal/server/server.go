package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/ai-coding-remote/relay-server/internal/config"
	"github.com/ai-coding-remote/relay-server/internal/hub"
	"github.com/ai-coding-remote/relay-server/internal/protocol"
	"github.com/ai-coding-remote/relay-server/internal/router"
	websockettransport "github.com/ai-coding-remote/relay-server/internal/websocket"
)

type Server struct {
	registry *hub.Registry
	handler  http.Handler
}

func New(config config.Config, logger *slog.Logger) *Server {
	registry := hub.NewRegistry()
	messageRouter := router.New(registry, logger)
	websocketHandler := websockettransport.NewHandler(websockettransport.Config{
		MaxMessageBytes: config.MaxMessageBytes,
		WriteQueueSize:  config.WriteQueueSize,
		PingInterval:    config.PingInterval,
	}, messageRouter, logger)
	mux := http.NewServeMux()
	server := &Server{registry: registry}
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /status", server.status)
	mux.HandleFunc("/ws/app", websocketHandler.ServeRole(protocol.RoleApp))
	mux.HandleFunc("/ws/agent", websocketHandler.ServeRole(protocol.RoleAgent))
	server.handler = securityHeaders(mux)
	return server
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) CloseConnections(reason string) {
	s.registry.CloseAll(reason)
}

func (s *Server) health(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) status(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, s.registry.Snapshot())
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(response, request)
	})
}

package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ai-coding-remote/relay-server/internal/auth"
	"github.com/ai-coding-remote/relay-server/internal/config"
	"github.com/ai-coding-remote/relay-server/internal/hub"
	"github.com/ai-coding-remote/relay-server/internal/protocol"
	"github.com/ai-coding-remote/relay-server/internal/router"
	runtimecore "github.com/ai-coding-remote/relay-server/internal/runtime"
	websockettransport "github.com/ai-coding-remote/relay-server/internal/websocket"
)

type Server struct {
	registry    *hub.Registry
	handler     http.Handler
	coordinator *runtimecore.Coordinator
	control     http.Handler
}

type RuntimeAuthModule interface {
	RegisterPublic(*http.ServeMux)
	Protect(http.Handler, func(*http.Request) string) http.Handler
	ControlHandler() http.Handler
}

func New(config config.Config, logger *slog.Logger) *Server {
	return newServer(config, logger, nil, nil, nil)
}

func NewWithRuntime(config config.Config, logger *slog.Logger, store runtimecore.Store, broker runtimecore.Broker) *Server {
	return newServer(config, logger, store, broker, nil)
}

func NewWithRuntimeAndAuth(config config.Config, logger *slog.Logger, store runtimecore.Store, broker runtimecore.Broker, authModule RuntimeAuthModule) *Server {
	return newServer(config, logger, store, broker, authModule)
}

func newServer(config config.Config, logger *slog.Logger, store runtimecore.Store, broker runtimecore.Broker, authModule RuntimeAuthModule) *Server {
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
	if store != nil && broker != nil {
		sourceGateway := runtimecore.NewSourceGateway(registry)
		runtimeMux := http.NewServeMux()
		// Runtime auth is centralized here. Individual Runtime handlers receive an authenticated Principal.
		runtimecore.NewAPIWithSource(store, broker, sourceGateway).Register(runtimeMux)
		var runtimeHandler http.Handler = runtimeMux
		if authModule != nil {
			runtimeHandler = authModule.Protect(runtimeHandler, runtimeRequiredScope)
			authModule.RegisterPublic(mux)
			server.control = authModule.ControlHandler()
		}
		mux.Handle("/v1/runtime/", runtimeHandler)
		server.coordinator = runtimecore.NewCoordinatorWithSource(store, broker, registry, logger, sourceGateway)
		messageRouter.SetObserver(server.coordinator)
	}
	server.handler = requestID(cors(config.AllowedOrigin, securityHeaders(mux)))
	return server
}

func runtimeRequiredScope(request *http.Request) string {
	if strings.HasSuffix(request.URL.Path, "/source:read") {
		return auth.ScopeSourceRead
	}
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return auth.ScopeRuntimeRead
	}
	return auth.ScopeRuntimeWrite
}

func (s *Server) ControlHandler() http.Handler { return s.control }

func cors(origin string, next http.Handler) http.Handler {
	allowed := make(map[string]struct{})
	allowAny := false
	for _, item := range strings.Split(origin, ",") {
		if value := strings.TrimSpace(item); value != "" {
			if value == "*" {
				allowAny = true
				continue
			}
			allowed[value] = struct{}{}
		}
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestOrigin := request.Header.Get("Origin")
		originAllowed := false
		if requestOrigin != "" && allowAny {
			response.Header().Set("Access-Control-Allow-Origin", "*")
			originAllowed = true
		} else if _, ok := allowed[requestOrigin]; ok {
			response.Header().Set("Access-Control-Allow-Origin", requestOrigin)
			response.Header().Add("Vary", "Origin")
			originAllowed = true
		}
		if originAllowed {
			response.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, Last-Event-ID, X-Request-Id, X-CodexRemote-Request")
			response.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			if !allowAny {
				response.Header().Set("Access-Control-Allow-Credentials", "true")
			}
		}
		if request.Method == http.MethodOptions {
			response.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		id := strings.TrimSpace(request.Header.Get("X-Request-Id"))
		if id == "" || len(id) > 128 {
			id = protocol.NewID()
		}
		response.Header().Set("X-Request-Id", id)
		next.ServeHTTP(response, request)
	})
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) CloseConnections(reason string) {
	s.registry.CloseAll(reason)
	if s.coordinator != nil {
		s.coordinator.Close()
	}
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

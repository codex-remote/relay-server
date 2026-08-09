package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ai-coding-remote/relay-server/internal/hub"
	"github.com/ai-coding-remote/relay-server/internal/protocol"
	"github.com/ai-coding-remote/relay-server/internal/router"
	ws "github.com/coder/websocket"
)

type Config struct {
	MaxMessageBytes int64
	WriteQueueSize  int
	PingInterval    time.Duration
}

type Handler struct {
	config Config
	router *router.Router
	logger *slog.Logger
	nextID atomic.Uint64
}

func NewHandler(config Config, messageRouter *router.Router, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{config: config, router: messageRouter, logger: logger}
}

func (h *Handler) ServeRole(role string) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		connection, err := ws.Accept(response, request, &ws.AcceptOptions{CompressionMode: ws.CompressionDisabled})
		if err != nil {
			h.logger.Warn("WebSocket upgrade rejected", "role", role, "error", err)
			return
		}
		connection.SetReadLimit(h.config.MaxMessageBytes)
		ctx, cancel := context.WithCancel(context.Background())
		peer := &peer{
			id: h.nextID.Add(1), role: role, connection: connection, context: ctx, cancel: cancel,
			outbound: make(chan protocol.Message, h.config.WriteQueueSize),
		}
		go peer.writeLoop(h.config.MaxMessageBytes, h.config.PingInterval, h.logger)
		h.router.Connect(peer)
		defer func() {
			peer.Close("connection ended")
			h.router.Disconnect(peer)
		}()

		for {
			messageType, data, err := connection.Read(ctx)
			if err != nil {
				if ctx.Err() == nil && ws.CloseStatus(err) != ws.StatusNormalClosure && ws.CloseStatus(err) != ws.StatusGoingAway {
					h.logger.Debug("WebSocket read ended", "role", role, "connection_id", peer.ID(), "error", err)
				}
				return
			}
			if messageType != ws.MessageText {
				h.router.Reject(peer, "message", "MESSAGE_INVALID", "only WebSocket text messages are supported")
				continue
			}
			message, err := protocol.Decode(data)
			if err != nil {
				h.logger.Warn("Invalid WebSocket message", "role", role, "connection_id", peer.ID(), "size", len(data), "error", err)
				h.router.Reject(peer, "message", "MESSAGE_INVALID", err.Error())
				continue
			}
			h.router.Handle(peer, message)
		}
	}
}

type peer struct {
	id         uint64
	role       string
	connection *ws.Conn
	context    context.Context
	cancel     context.CancelFunc
	outbound   chan protocol.Message
	closeOnce  sync.Once
}

func (p *peer) ID() uint64   { return p.id }
func (p *peer) Role() string { return p.role }

func (p *peer) Send(message protocol.Message) error {
	select {
	case p.outbound <- message:
		return nil
	case <-p.context.Done():
		return p.context.Err()
	default:
		return hub.ErrSendQueueFull
	}
}

func (p *peer) Close(reason string) {
	p.closeOnce.Do(func() {
		p.cancel()
		go func() {
			_ = p.connection.Close(ws.StatusGoingAway, reason)
		}()
	})
}

func (p *peer) writeLoop(maxMessageBytes int64, pingInterval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case message := <-p.outbound:
			data, err := json.Marshal(message)
			if err != nil {
				logger.Error("Encode outbound message", "role", p.role, "message_type", message.Type, "error", err)
				continue
			}
			if int64(len(data)) > maxMessageBytes {
				logger.Warn("Outbound message exceeds limit", "role", p.role, "message_type", message.Type, "size", len(data))
				continue
			}
			writeContext, cancel := context.WithTimeout(p.context, pingInterval)
			err = p.connection.Write(writeContext, ws.MessageText, data)
			cancel()
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					logger.Debug("WebSocket write ended", "role", p.role, "connection_id", p.id, "error", err)
				}
				p.Close("write failed")
				return
			}
		case <-ticker.C:
			pingContext, cancel := context.WithTimeout(p.context, pingInterval/2)
			err := p.connection.Ping(pingContext)
			cancel()
			if err != nil {
				p.Close("ping failed")
				return
			}
		case <-p.context.Done():
			return
		}
	}
}

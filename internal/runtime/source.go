package runtime

import (
	"context"
	"errors"
	"sync"

	"github.com/codex-remote/relay-server/internal/protocol"
)

var (
	ErrSourceAgentOffline = errors.New("source agent offline")
	ErrSourceUnavailable  = errors.New("source transport unavailable")
	ErrSourceBusy         = errors.New("too many source requests")
)

const maxPendingSourceReads = 16

type SourceProvider interface {
	Read(context.Context, protocol.SourceReadPayload) (protocol.SourceSnapshotPayload, error)
}

type SourceRemoteError struct {
	Code    string
	Message string
}

func (e *SourceRemoteError) Error() string { return e.Code }

type sourceResult struct {
	snapshot protocol.SourceSnapshotPayload
	err      error
}

type SourceGateway struct {
	registry AgentRegistry
	mu       sync.Mutex
	pending  map[string]chan sourceResult
}

func NewSourceGateway(registry AgentRegistry) *SourceGateway {
	return &SourceGateway{registry: registry, pending: make(map[string]chan sourceResult)}
}

func (g *SourceGateway) Read(ctx context.Context, request protocol.SourceReadPayload) (protocol.SourceSnapshotPayload, error) {
	peer := g.registry.Peer(protocol.RoleAgent)
	if peer == nil {
		return protocol.SourceSnapshotPayload{}, ErrSourceAgentOffline
	}
	traceID := protocol.NewID()
	waiter := make(chan sourceResult, 1)
	g.mu.Lock()
	if len(g.pending) >= maxPendingSourceReads {
		g.mu.Unlock()
		return protocol.SourceSnapshotPayload{}, ErrSourceBusy
	}
	g.pending[traceID] = waiter
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.pending, traceID)
		g.mu.Unlock()
	}()

	message, err := protocol.NewMessage(protocol.TypeSourceRead, traceID, protocol.Sender{Kind: "relay", ID: "run-server"}, request)
	if err != nil || peer.Send(message) != nil {
		return protocol.SourceSnapshotPayload{}, ErrSourceUnavailable
	}
	select {
	case <-ctx.Done():
		return protocol.SourceSnapshotPayload{}, ctx.Err()
	case result := <-waiter:
		return result.snapshot, result.err
	}
}

func (g *SourceGateway) Handle(message protocol.Message) bool {
	if message.Type != protocol.TypeSourceSnapshot && message.Type != protocol.TypeSourceReadFailed {
		return false
	}
	result := sourceResult{}
	if message.Type == protocol.TypeSourceSnapshot {
		payload, err := protocol.PayloadAs[protocol.SourceSnapshotPayload](message)
		if err != nil {
			result.err = ErrSourceUnavailable
		} else {
			result.snapshot = payload
		}
	} else {
		payload, err := protocol.PayloadAs[protocol.SourceReadFailedPayload](message)
		if err != nil {
			result.err = ErrSourceUnavailable
		} else {
			result.err = &SourceRemoteError{Code: payload.Code, Message: payload.Message}
		}
	}
	g.mu.Lock()
	waiter := g.pending[message.TraceID]
	g.mu.Unlock()
	if waiter != nil {
		select {
		case waiter <- result:
		default:
		}
	}
	return true
}

func (g *SourceGateway) FailPending(err error) {
	g.mu.Lock()
	waiters := make([]chan sourceResult, 0, len(g.pending))
	for _, waiter := range g.pending {
		waiters = append(waiters, waiter)
	}
	g.mu.Unlock()
	for _, waiter := range waiters {
		select {
		case waiter <- sourceResult{err: err}:
		default:
		}
	}
}

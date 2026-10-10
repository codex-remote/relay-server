package runtime

import (
	"context"
	"errors"
	"sync"

	"github.com/codex-remote/relay-server/internal/protocol"
)

var (
	ErrExecutionProfilesAgentOffline = errors.New("execution profiles agent offline")
	ErrExecutionProfilesUnavailable  = errors.New("execution profiles transport unavailable")
	ErrExecutionProfilesBusy         = errors.New("too many execution profile requests")
)

const maxPendingExecutionProfileRequests = 16

type ExecutionProfileProvider interface {
	List(context.Context, string) (protocol.ExecutionProfileSnapshotPayload, error)
}

type executionProfileResult struct {
	snapshot protocol.ExecutionProfileSnapshotPayload
	err      error
}

type ExecutionProfileGateway struct {
	registry AgentRegistry
	mu       sync.Mutex
	pending  map[string]chan executionProfileResult
}

func NewExecutionProfileGateway(registry AgentRegistry) *ExecutionProfileGateway {
	return &ExecutionProfileGateway{registry: registry, pending: make(map[string]chan executionProfileResult)}
}

func (g *ExecutionProfileGateway) List(ctx context.Context, projectID string) (protocol.ExecutionProfileSnapshotPayload, error) {
	peer := g.registry.Peer(protocol.RoleAgent)
	if peer == nil {
		return protocol.ExecutionProfileSnapshotPayload{}, ErrExecutionProfilesAgentOffline
	}
	traceID := protocol.NewID()
	waiter := make(chan executionProfileResult, 1)
	g.mu.Lock()
	if len(g.pending) >= maxPendingExecutionProfileRequests {
		g.mu.Unlock()
		return protocol.ExecutionProfileSnapshotPayload{}, ErrExecutionProfilesBusy
	}
	g.pending[traceID] = waiter
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.pending, traceID)
		g.mu.Unlock()
	}()

	message, err := protocol.NewMessage(
		protocol.TypeExecutionProfileList,
		traceID,
		protocol.Sender{Kind: "relay", ID: "run-server"},
		protocol.ExecutionProfileListPayload{ProjectID: projectID},
	)
	if err != nil || peer.Send(message) != nil {
		return protocol.ExecutionProfileSnapshotPayload{}, ErrExecutionProfilesUnavailable
	}
	select {
	case <-ctx.Done():
		return protocol.ExecutionProfileSnapshotPayload{}, ctx.Err()
	case result := <-waiter:
		return result.snapshot, result.err
	}
}

func (g *ExecutionProfileGateway) Handle(message protocol.Message) bool {
	if message.Type != protocol.TypeExecutionProfileSnapshot {
		return false
	}
	g.mu.Lock()
	waiter := g.pending[message.TraceID]
	g.mu.Unlock()
	if waiter == nil {
		return false
	}
	payload, err := protocol.PayloadAs[protocol.ExecutionProfileSnapshotPayload](message)
	result := executionProfileResult{snapshot: payload, err: err}
	if err != nil {
		result.err = ErrExecutionProfilesUnavailable
	}
	select {
	case waiter <- result:
	default:
	}
	return true
}

func (g *ExecutionProfileGateway) FailPending(err error) {
	g.mu.Lock()
	waiters := make([]chan executionProfileResult, 0, len(g.pending))
	for _, waiter := range g.pending {
		waiters = append(waiters, waiter)
	}
	g.mu.Unlock()
	for _, waiter := range waiters {
		select {
		case waiter <- executionProfileResult{err: err}:
		default:
		}
	}
}

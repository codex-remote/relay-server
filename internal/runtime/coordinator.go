package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/ai-coding-remote/relay-server/internal/hub"
	"github.com/ai-coding-remote/relay-server/internal/protocol"
)

type AgentRegistry interface {
	Peer(string) hub.Peer
}

type Coordinator struct {
	ctx      context.Context
	cancel   context.CancelFunc
	store    Store
	broker   Broker
	registry AgentRegistry
	logger   *slog.Logger
	workerID string
}

func NewCoordinator(store Store, broker Broker, registry AgentRegistry, logger *slog.Logger) *Coordinator {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Coordinator{ctx: ctx, cancel: cancel, store: store, broker: broker, registry: registry, logger: logger, workerID: fmt.Sprintf("relay-%d", time.Now().UnixNano())}
	go c.dispatchLoop()
	return c
}

func (c *Coordinator) Close() {
	c.cancel()
	_ = c.broker.SetAgentPresence(context.Background(), false)
}

func (c *Coordinator) AgentConnected(peer hub.Peer) {
	_ = c.broker.SetAgentPresence(c.ctx, true)
	message, _ := protocol.NewMessage(protocol.TypeProjectList, "project-refresh", protocol.Sender{Kind: "relay", ID: "run-server"}, protocol.ProjectListPayload{})
	_ = peer.Send(message)
}

func (c *Coordinator) AgentDisconnected(hub.Peer) {
	_ = c.broker.SetAgentPresence(context.Background(), false)
}

func (c *Coordinator) AgentMessage(peer hub.Peer, message protocol.Message) bool {
	switch message.Type {
	case protocol.TypeAgentHello, protocol.TypeAgentStatus, protocol.TypeAgentCapabilities:
		_ = c.broker.SetAgentPresence(c.ctx, true)
		return true
	case protocol.TypeProjectSnapshot:
		payload, err := protocol.PayloadAs[protocol.ProjectSnapshotPayload](message)
		if err != nil {
			return true
		}
		projects := make([]Project, 0, len(payload.Projects))
		for _, item := range payload.Projects {
			projects = append(projects, Project{ID: item.ID, DisplayName: item.Name})
		}
		if err := c.store.UpsertProjects(c.ctx, projects); err != nil {
			c.logger.Error("Persist projects", "error", err)
		}
		return true
	case protocol.TypeBootstrapBatch:
		payload, err := protocol.PayloadAs[protocol.BootstrapBatchPayload](message)
		if err != nil || payload.CommandID == "" || payload.SyncID == "" || payload.Checksum == "" {
			return true
		}
		batch := bootstrapBatch(payload)
		if _, err := c.store.ApplyBootstrapBatch(c.ctx, batch); err != nil {
			c.logger.Error("Persist bootstrap batch", "sync_id", payload.SyncID, "batch_no", payload.BatchNo, "error", err)
			return true
		}
		ack, err := protocol.NewMessage(protocol.TypeBootstrapDurableAck, payload.SyncID, protocol.Sender{Kind: "relay", ID: "run-server"}, protocol.BootstrapAckPayload{SyncID: payload.SyncID, BatchNo: payload.BatchNo, Checksum: payload.Checksum})
		if err == nil {
			_ = peer.Send(ack)
		}
		if payload.Done {
			_ = c.store.MarkCommandDelivered(c.ctx, payload.CommandID)
		}
		return true
	case protocol.TypeRunAccepted, protocol.TypeTurnStarted, protocol.TypeTurnOutput, protocol.TypeTurnItemStarted, protocol.TypeTurnItemDelta, protocol.TypeTurnItemDone, protocol.TypeTurnCompleted, protocol.TypeTurnFailed, protocol.TypeTurnInterrupted, protocol.TypeTurnRejected:
		if message.AgentSequence < 1 {
			return true
		}
		runID := message.TraceID
		event := RunEvent{Sequence: message.AgentSequence, Type: runtimeEventType(message.Type), Payload: message.Payload, OccurredAt: message.OccurredAt}
		if err := c.broker.AppendRunEvent(c.ctx, runID, event); err != nil {
			c.logger.Error("Append Redis run event", "run_id", runID, "sequence", message.AgentSequence, "error", err)
			return true
		}
		c.sendAck(peer, protocol.TypeRuntimeReceivedAck, runID, message.AgentSequence)
		run, err := c.store.AppendAgentEvent(c.ctx, runID, message.AgentSequence, event.Type, event.Payload, message.OccurredAt.UTC().Format(time.RFC3339Nano))
		if err != nil {
			c.logger.Error("Persist run event", "run_id", runID, "sequence", message.AgentSequence, "error", err)
			return true
		}
		_ = c.broker.NotifySession(c.ctx, run.SessionID)
		c.sendAck(peer, protocol.TypeRuntimeDurableAck, runID, message.AgentSequence)
		if message.Type == protocol.TypeRunAccepted {
			payload, _ := protocol.PayloadAs[protocol.RunAcceptedPayload](message)
			if payload.CommandID != "" {
				_ = c.store.MarkCommandDelivered(c.ctx, payload.CommandID)
			}
		}
		return true
	default:
		return false
	}
}

func (c *Coordinator) sendAck(peer hub.Peer, messageType, runID string, sequence int64) {
	message, err := protocol.NewMessage(messageType, runID, protocol.Sender{Kind: "relay", ID: "run-server"}, protocol.RuntimeAckPayload{RunID: runID, AgentSequence: sequence})
	if err == nil {
		_ = peer.Send(message)
	}
}

func (c *Coordinator) dispatchLoop() {
	ticker := time.NewTicker(300 * time.Millisecond)
	presenceTicker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer presenceTicker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.dispatch()
		case <-presenceTicker.C:
			if c.registry.Peer(protocol.RoleAgent) != nil {
				_ = c.broker.SetAgentPresence(c.ctx, true)
			}
		}
	}
}

func (c *Coordinator) dispatch() {
	peer := c.registry.Peer(protocol.RoleAgent)
	if peer == nil {
		return
	}
	commands, err := c.store.ClaimCommands(c.ctx, c.workerID, 8)
	if err != nil {
		c.logger.Error("Claim Runtime commands", "error", err)
		return
	}
	for _, command := range commands {
		if err := c.sendCommand(peer, command); err != nil {
			_ = c.store.ReleaseCommand(c.ctx, command.ID, "AGENT_SEND_FAILED")
		}
	}
}

func (c *Coordinator) sendCommand(peer hub.Peer, command Command) error {
	switch command.Type {
	case "run.start":
		var payload protocol.TurnStartPayload
		if err := json.Unmarshal(command.Payload, &payload); err != nil {
			return err
		}
		message, err := protocol.NewMessage(protocol.TypeTurnStart, command.ResourceID, protocol.Sender{Kind: "relay", ID: "run-server"}, payload)
		if err != nil {
			return err
		}
		return peer.Send(message)
	case "run.cancel":
		var payload struct {
			ThreadID string `json:"thread_id"`
			TurnID   string `json:"turn_id"`
		}
		if err := json.Unmarshal(command.Payload, &payload); err != nil {
			return err
		}
		message, err := protocol.NewMessage(protocol.TypeTurnInterrupt, command.ResourceID, protocol.Sender{Kind: "relay", ID: "run-server"}, protocol.TurnInterruptPayload{ThreadID: payload.ThreadID, TurnID: payload.TurnID})
		if err != nil {
			return err
		}
		if err := peer.Send(message); err != nil {
			return err
		}
		return c.store.MarkCommandDelivered(c.ctx, command.ID)
	case "bootstrap.start":
		job, err := c.store.GetSyncJob(c.ctx, command.ResourceID)
		if err != nil {
			return err
		}
		if job.Status == "completed" || job.Status == "failed" {
			return c.store.MarkCommandDelivered(c.ctx, command.ID)
		}
		var payload protocol.BootstrapStartPayload
		if err := json.Unmarshal(command.Payload, &payload); err != nil {
			return err
		}
		message, err := protocol.NewMessage(protocol.TypeBootstrapStart, command.ResourceID, protocol.Sender{Kind: "relay", ID: "run-server"}, payload)
		if err != nil {
			return err
		}
		return peer.Send(message)
	default:
		return fmt.Errorf("unsupported command %s", command.Type)
	}
}

func bootstrapBatch(payload protocol.BootstrapBatchPayload) BootstrapBatch {
	batch := BootstrapBatch{
		CommandID: payload.CommandID, SyncID: payload.SyncID, SnapshotID: payload.SnapshotID, BatchNo: payload.BatchNo,
		Checksum: payload.Checksum, Done: payload.Done,
	}
	if payload.Project != nil {
		batch.Projects = append(batch.Projects, BootstrapProject{ID: payload.Project.ID, Name: payload.Project.Name})
	}
	if payload.Thread == nil {
		return batch
	}

	thread := payload.Thread
	sessionID := "session_" + thread.ID
	batch.Sessions = append(batch.Sessions, BootstrapSession{
		ID: sessionID, ProjectID: thread.ProjectID, CodexThreadID: thread.ID, Title: thread.Title,
	})
	for _, turn := range thread.Turns {
		startedAt := turn.StartedAt
		if startedAt == nil {
			startedAt = &thread.CreatedAt
		}
		finishedAt := turn.CompletedAt
		if finishedAt == nil {
			finishedAt = &thread.UpdatedAt
		}
		run := BootstrapRun{
			ID: "run_" + turn.ID, SessionID: sessionID, CodexTurnID: turn.ID,
			Status: turn.Status, StartedAt: startedAt, FinishedAt: finishedAt,
		}
		sequence := int64(1)
		for _, item := range turn.Items {
			if run.Prompt == "" && (item.Role == "user" || item.Type == "userMessage") {
				run.Prompt = item.Text
			}
			data, _ := json.Marshal(map[string]any{"item": item})
			run.Events = append(run.Events, BootstrapEvent{Sequence: sequence, Type: "item.completed", Payload: data, OccurredAt: *startedAt})
			sequence++
		}
		terminalType := "turn.completed"
		if turn.Status == "failed" {
			terminalType = "turn.failed"
		} else if turn.Status == "interrupted" || turn.Status == "canceled" || turn.Status == "cancelled" {
			terminalType = "turn.interrupted"
		}
		data, _ := json.Marshal(map[string]any{"thread_id": thread.ID, "turn_id": turn.ID, "status": turn.Status, "error": turn.Error})
		run.Events = append(run.Events, BootstrapEvent{Sequence: sequence, Type: terminalType, Payload: data, OccurredAt: *finishedAt})
		batch.Runs = append(batch.Runs, run)
	}
	return batch
}

func runtimeEventType(messageType string) string {
	switch messageType {
	case protocol.TypeTurnOutput:
		return "assistant.delta"
	case protocol.TypeTurnItemStarted:
		return "item.started"
	case protocol.TypeTurnItemDelta:
		return "item.delta"
	case protocol.TypeTurnItemDone:
		return "item.completed"
	case protocol.TypeTurnRejected:
		return "turn.failed"
	default:
		return messageType
	}
}

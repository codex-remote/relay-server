package runtime

import (
	"encoding/json"
	"time"
)

type Project struct {
	ID          string `json:"project_id"`
	DisplayName string `json:"display_name"`
}

type Session struct {
	ID                  string    `json:"session_id"`
	ProjectID           string    `json:"project_id"`
	CodexThreadID       string    `json:"codex_thread_id,omitempty"`
	Title               string    `json:"title"`
	LastSessionSequence int64     `json:"last_session_sequence"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type Run struct {
	ID                       string     `json:"run_id"`
	SessionID                string     `json:"session_id"`
	CodexTurnID              string     `json:"codex_turn_id,omitempty"`
	Status                   string     `json:"status"`
	StateVersion             int64      `json:"state_version"`
	Prompt                   string     `json:"prompt,omitempty"`
	CancelRequestedAt        *time.Time `json:"cancel_requested_at,omitempty"`
	PersistedThroughSequence int64      `json:"persisted_through_sequence"`
	FinalSequence            *int64     `json:"final_sequence,omitempty"`
	ErrorCode                string     `json:"error_code,omitempty"`
	ErrorMessage             string     `json:"error_message,omitempty"`
	CreatedAt                time.Time  `json:"created_at"`
	StartedAt                *time.Time `json:"started_at,omitempty"`
	FinishedAt               *time.Time `json:"finished_at,omitempty"`
	UpdatedAt                time.Time  `json:"updated_at"`
	Events                   []RunEvent `json:"events,omitempty"`
}

type SessionEvent struct {
	Sequence  int64           `json:"session_sequence"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type RunEvent struct {
	Sequence   int64           `json:"agent_sequence"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
	OccurredAt time.Time       `json:"occurred_at"`
}

type Command struct {
	ID         string
	Type       string
	ResourceID string
	Payload    json.RawMessage
}

type SyncJob struct {
	ID                   string    `json:"sync_id"`
	Status               string    `json:"status"`
	SnapshotID           string    `json:"snapshot_id,omitempty"`
	LastCommittedBatchNo int64     `json:"last_committed_batch_no"`
	ItemCount            int64     `json:"item_count"`
	ErrorCode            string    `json:"error_code,omitempty"`
	ErrorMessage         string    `json:"error_message,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type BootstrapProject struct {
	ID   string `json:"project_id"`
	Name string `json:"display_name"`
}

type BootstrapSession struct {
	ID            string `json:"session_id"`
	ProjectID     string `json:"project_id"`
	CodexThreadID string `json:"codex_thread_id"`
	Title         string `json:"title"`
}

type BootstrapRun struct {
	ID          string           `json:"run_id"`
	SessionID   string           `json:"session_id"`
	CodexTurnID string           `json:"codex_turn_id"`
	Status      string           `json:"status"`
	Prompt      string           `json:"prompt,omitempty"`
	StartedAt   *time.Time       `json:"started_at,omitempty"`
	FinishedAt  *time.Time       `json:"finished_at,omitempty"`
	Events      []BootstrapEvent `json:"events"`
}

type BootstrapEvent struct {
	Sequence   int64           `json:"agent_sequence"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
	OccurredAt time.Time       `json:"occurred_at"`
}

type BootstrapBatch struct {
	CommandID  string             `json:"command_id"`
	SyncID     string             `json:"sync_id"`
	SnapshotID string             `json:"snapshot_id"`
	BatchNo    int64              `json:"batch_no"`
	Checksum   string             `json:"checksum"`
	Projects   []BootstrapProject `json:"projects"`
	Sessions   []BootstrapSession `json:"sessions"`
	Runs       []BootstrapRun     `json:"runs"`
	Done       bool               `json:"done"`
}

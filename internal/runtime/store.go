package runtime

import (
	"context"
	"encoding/json"
)

var (
	ErrNotFound            = errorString("runtime resource not found")
	ErrIdempotencyConflict = errorString("idempotency key reused with a different request")
)

type errorString string

func (e errorString) Error() string { return string(e) }

type Store interface {
	Health(context.Context) error
	UpsertProjects(context.Context, []Project) error
	ListProjects(context.Context) ([]Project, error)
	CreateSession(context.Context, string, string, string, string) (Session, bool, error)
	ListSessions(context.Context, string) ([]Session, error)
	GetSession(context.Context, string) (Session, error)
	CreateRun(context.Context, string, string, string, string) (Run, int64, bool, error)
	ListRuns(context.Context, string) ([]Run, error)
	GetRun(context.Context, string, bool) (Run, error)
	RequestCancel(context.Context, string) (Run, error)
	ClaimCommands(context.Context, string, int) ([]Command, error)
	MarkCommandDelivered(context.Context, string) error
	ReleaseCommand(context.Context, string, string) error
	AppendAgentEvent(context.Context, string, int64, string, json.RawMessage, string) (Run, error)
	ListRunEvents(context.Context, string, int64) ([]RunEvent, error)
	ListSessionEvents(context.Context, string, int64) ([]SessionEvent, error)
	CreateSyncJob(context.Context, string) (SyncJob, bool, error)
	GetSyncJob(context.Context, string) (SyncJob, error)
	ApplyBootstrapBatch(context.Context, BootstrapBatch) (SyncJob, error)
	Close()
}

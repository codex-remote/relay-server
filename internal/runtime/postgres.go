package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type PostgresStore struct {
	pool *pgxpool.Pool
}

const syncJobSelect = `SELECT sync_id,status,COALESCE(snapshot_id,''),last_committed_batch_no,item_count,total_sessions,processed_sessions,archived_sessions,archived_projects,reconciliation_applied,COALESCE(error_code,''),COALESCE(error_message,''),created_at,updated_at FROM runtime.sync_jobs`

func OpenPostgres(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	store := &PostgresStore{pool: pool}
	if err := store.Health(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		data, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		if _, err := s.pool.Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("apply runtime migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (s *PostgresStore) Health(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *PostgresStore) Close()                           { s.pool.Close() }

func (s *PostgresStore) UpsertProjects(ctx context.Context, projects []Project) error {
	if len(projects) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, project := range projects {
		if strings.TrimSpace(project.ID) == "" || strings.TrimSpace(project.DisplayName) == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO runtime.projects(project_id,display_name) VALUES($1,$2)
			ON CONFLICT(project_id) DO UPDATE SET display_name=EXCLUDED.display_name,archived_at=NULL,updated_at=now()`, project.ID, project.DisplayName); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.pool.Query(ctx, `SELECT project_id,display_name FROM runtime.projects WHERE archived_at IS NULL ORDER BY display_name,project_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Project, 0)
	for rows.Next() {
		var item Project
		if err := rows.Scan(&item.ID, &item.DisplayName); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *PostgresStore) CreateSession(ctx context.Context, projectID, title, key, hash string) (Session, bool, error) {
	if existing, err := s.sessionByKey(ctx, key); err == nil {
		if hash != existing.hash {
			return Session{}, false, ErrIdempotencyConflict
		}
		return existing.session, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Session{}, false, err
	}
	id := newID("session")
	var item Session
	err := s.pool.QueryRow(ctx, `INSERT INTO runtime.sessions(session_id,project_id,title,idempotency_key,request_hash)
		SELECT $1,$2,$3,$4,$5 FROM runtime.projects WHERE project_id=$2 AND archived_at IS NULL ON CONFLICT DO NOTHING
		RETURNING session_id,project_id,COALESCE(codex_thread_id,''),COALESCE(title,''),last_session_sequence,created_at,updated_at`, id, projectID, title, key, hash).
		Scan(&item.ID, &item.ProjectID, &item.CodexThreadID, &item.Title, &item.LastSessionSequence, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, lookupErr := s.sessionByKey(ctx, key)
		if lookupErr != nil {
			return Session{}, false, lookupErr
		}
		if hash != existing.hash {
			return Session{}, false, ErrIdempotencyConflict
		}
		return existing.session, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	return item, true, nil
}

type keyedSession struct {
	session Session
	hash    string
}

func (s *PostgresStore) sessionByKey(ctx context.Context, key string) (keyedSession, error) {
	var value keyedSession
	err := s.pool.QueryRow(ctx, `SELECT s.session_id,s.project_id,COALESCE(s.codex_thread_id,''),COALESCE(s.title,''),s.last_session_sequence,s.created_at,s.updated_at,s.request_hash
		FROM runtime.sessions s JOIN runtime.projects p ON p.project_id=s.project_id
		WHERE s.idempotency_key=$1 AND s.archived_at IS NULL AND p.archived_at IS NULL`, key).
		Scan(&value.session.ID, &value.session.ProjectID, &value.session.CodexThreadID, &value.session.Title, &value.session.LastSessionSequence, &value.session.CreatedAt, &value.session.UpdatedAt, &value.hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return keyedSession{}, ErrNotFound
	}
	return value, err
}

func (s *PostgresStore) ListSessions(ctx context.Context, projectID string) ([]Session, error) {
	query := `SELECT s.session_id,s.project_id,COALESCE(s.codex_thread_id,''),COALESCE(s.title,''),s.last_session_sequence,
		COALESCE((SELECT r.status FROM runtime.runs r WHERE r.session_id=s.session_id ORDER BY r.created_at DESC,r.run_id DESC LIMIT 1),''),
		s.created_at,s.updated_at
		FROM runtime.sessions s JOIN runtime.projects p ON p.project_id=s.project_id WHERE s.archived_at IS NULL AND p.archived_at IS NULL`
	args := []any{}
	if projectID != "" {
		query += ` AND s.project_id=$1`
		args = append(args, projectID)
	}
	query += ` ORDER BY s.updated_at DESC,s.session_id DESC`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Session, 0)
	for rows.Next() {
		var item Session
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.CodexThreadID, &item.Title, &item.LastSessionSequence, &item.LatestRunStatus, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *PostgresStore) GetSession(ctx context.Context, id string) (Session, error) {
	var item Session
	err := s.pool.QueryRow(ctx, `SELECT s.session_id,s.project_id,COALESCE(s.codex_thread_id,''),COALESCE(s.title,''),s.last_session_sequence,s.created_at,s.updated_at
		FROM runtime.sessions s JOIN runtime.projects p ON p.project_id=s.project_id
		WHERE s.session_id=$1 AND s.archived_at IS NULL AND p.archived_at IS NULL`, id).
		Scan(&item.ID, &item.ProjectID, &item.CodexThreadID, &item.Title, &item.LastSessionSequence, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return item, err
}

func (s *PostgresStore) CreateRun(ctx context.Context, sessionID, prompt, key, hash string) (Run, int64, bool, error) {
	var existing Run
	err := s.pool.QueryRow(ctx, `SELECT run_id FROM runtime.runs WHERE session_id=$1 AND idempotency_key=$2`, sessionID, key).Scan(&existing.ID)
	if err == nil {
		var storedHash string
		if err := s.pool.QueryRow(ctx, `SELECT request_hash FROM runtime.runs WHERE run_id=$1`, existing.ID).Scan(&storedHash); err != nil {
			return Run{}, 0, false, err
		}
		if storedHash != hash {
			return Run{}, 0, false, ErrIdempotencyConflict
		}
		item, err := s.GetRun(ctx, existing.ID, false)
		return item, 0, false, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Run{}, 0, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, 0, false, err
	}
	defer tx.Rollback(ctx)
	var projectID, threadID string
	if err := tx.QueryRow(ctx, `SELECT s.project_id,COALESCE(s.codex_thread_id,'') FROM runtime.sessions s
		JOIN runtime.projects p ON p.project_id=s.project_id
		WHERE s.session_id=$1 AND s.archived_at IS NULL AND p.archived_at IS NULL FOR UPDATE OF s,p`, sessionID).Scan(&projectID, &threadID); errors.Is(err, pgx.ErrNoRows) {
		return Run{}, 0, false, ErrNotFound
	} else if err != nil {
		return Run{}, 0, false, err
	}
	var existingID, storedHash string
	err = tx.QueryRow(ctx, `SELECT run_id,request_hash FROM runtime.runs WHERE session_id=$1 AND idempotency_key=$2`, sessionID, key).Scan(&existingID, &storedHash)
	if err == nil {
		if storedHash != hash {
			return Run{}, 0, false, ErrIdempotencyConflict
		}
		if err := tx.Rollback(ctx); err != nil {
			return Run{}, 0, false, err
		}
		item, err := s.GetRun(ctx, existingID, false)
		return item, 0, false, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Run{}, 0, false, err
	}
	runID, commandID := newID("run"), newID("cmd")
	var item Run
	err = tx.QueryRow(ctx, `INSERT INTO runtime.runs(run_id,session_id,status,idempotency_key,request_hash,prompt_text) VALUES($1,$2,'queued',$3,$4,$5)
		RETURNING run_id,session_id,COALESCE(codex_turn_id,''),status,state_version,COALESCE(prompt_text,''),cancel_requested_at,persisted_through_sequence,final_sequence,COALESCE(error_code,''),COALESCE(error_message,''),created_at,started_at,finished_at,updated_at`, runID, sessionID, key, hash, prompt).
		Scan(&item.ID, &item.SessionID, &item.CodexTurnID, &item.Status, &item.StateVersion, &item.Prompt, &item.CancelRequestedAt, &item.PersistedThroughSequence, &item.FinalSequence, &item.ErrorCode, &item.ErrorMessage, &item.CreatedAt, &item.StartedAt, &item.FinishedAt, &item.UpdatedAt)
	if err != nil {
		return Run{}, 0, false, err
	}
	var sequence int64
	if err := tx.QueryRow(ctx, `UPDATE runtime.sessions SET last_session_sequence=last_session_sequence+1,updated_at=now() WHERE session_id=$1 RETURNING last_session_sequence`, sessionID).Scan(&sequence); err != nil {
		return Run{}, 0, false, err
	}
	payload, _ := json.Marshal(map[string]any{"schema_version": 1, "session_id": sessionID, "run_id": runID, "status": "queued", "occurred_at": item.CreatedAt})
	if _, err := tx.Exec(ctx, `INSERT INTO runtime.session_events(session_id,session_sequence,event_type,payload) VALUES($1,$2,'run.created',$3)`, sessionID, sequence, payload); err != nil {
		return Run{}, 0, false, err
	}
	commandPayload, _ := json.Marshal(map[string]any{"command_id": commandID, "run_id": runID, "project_id": projectID, "thread_id": threadID, "prompt": prompt})
	if _, err := tx.Exec(ctx, `INSERT INTO runtime.command_outbox(command_id,command_type,resource_id,payload) VALUES($1,'run.start',$2,$3)`, commandID, runID, commandPayload); err != nil {
		return Run{}, 0, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, 0, false, err
	}
	return item, sequence, true, nil
}

func (s *PostgresStore) ListRuns(ctx context.Context, sessionID string) ([]Run, error) {
	rows, err := s.pool.Query(ctx, runSelect+` WHERE session_id=$1 ORDER BY created_at,run_id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Run, 0)
	for rows.Next() {
		item, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

const runSelect = `SELECT run_id,session_id,COALESCE(codex_turn_id,''),status,state_version,COALESCE(prompt_text,''),cancel_requested_at,persisted_through_sequence,final_sequence,COALESCE(error_code,''),COALESCE(error_message,''),created_at,started_at,finished_at,updated_at FROM runtime.runs`

type rowScanner interface{ Scan(...any) error }

func scanRun(row rowScanner) (Run, error) {
	var item Run
	err := row.Scan(&item.ID, &item.SessionID, &item.CodexTurnID, &item.Status, &item.StateVersion, &item.Prompt, &item.CancelRequestedAt, &item.PersistedThroughSequence, &item.FinalSequence, &item.ErrorCode, &item.ErrorMessage, &item.CreatedAt, &item.StartedAt, &item.FinishedAt, &item.UpdatedAt)
	return item, err
}

func (s *PostgresStore) GetRun(ctx context.Context, id string, withEvents bool) (Run, error) {
	item, err := scanRun(s.pool.QueryRow(ctx, runSelect+` WHERE run_id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	if withEvents {
		item.Events, err = s.ListRunEvents(ctx, id, 0)
	}
	return item, err
}

func (s *PostgresStore) RequestCancel(ctx context.Context, id string) (Run, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(ctx)
	item, err := scanRun(tx.QueryRow(ctx, runSelect+` WHERE run_id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	if item.CancelRequestedAt != nil || isTerminal(item.Status) {
		return item, tx.Commit(ctx)
	}
	var threadID, turnID string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(s.codex_thread_id,''),COALESCE(r.codex_turn_id,'') FROM runtime.runs r JOIN runtime.sessions s ON s.session_id=r.session_id WHERE r.run_id=$1`, id).Scan(&threadID, &turnID); err != nil {
		return Run{}, err
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE runtime.runs SET cancel_requested_at=$2,state_version=state_version+1,updated_at=$2 WHERE run_id=$1`, id, now); err != nil {
		return Run{}, err
	}
	payload, _ := json.Marshal(map[string]string{"run_id": id, "thread_id": threadID, "turn_id": turnID})
	if _, err := tx.Exec(ctx, `INSERT INTO runtime.command_outbox(command_id,command_type,resource_id,payload) VALUES($1,'run.cancel',$2,$3) ON CONFLICT(command_type,resource_id) DO NOTHING`, newID("cmd"), id, payload); err != nil {
		return Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, err
	}
	return s.GetRun(ctx, id, false)
}

func (s *PostgresStore) ClaimCommands(ctx context.Context, worker string, limit int) ([]Command, error) {
	if limit < 1 {
		limit = 16
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT command_id,command_type,resource_id,payload FROM runtime.command_outbox WHERE (status='pending' OR (status='claimed' AND claim_expires_at<now())) AND next_attempt_at<=now() ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	var result []Command
	for rows.Next() {
		var item Command
		if err := rows.Scan(&item.ID, &item.Type, &item.ResourceID, &item.Payload); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, item := range result {
		if _, err := tx.Exec(ctx, `UPDATE runtime.command_outbox SET status='claimed',claimed_by=$2,claim_expires_at=now()+interval '15 seconds',attempt_count=attempt_count+1 WHERE command_id=$1`, item.ID, worker); err != nil {
			return nil, err
		}
		if item.Type == "run.start" {
			_, _ = tx.Exec(ctx, `UPDATE runtime.runs SET status='dispatching',state_version=state_version+1,updated_at=now() WHERE run_id=$1 AND status IN ('queued','waiting_agent')`, item.ResourceID)
		}
	}
	return result, tx.Commit(ctx)
}
func (s *PostgresStore) MarkCommandDelivered(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE runtime.command_outbox SET status='delivered',delivered_at=now(),claimed_by=NULL,claim_expires_at=NULL WHERE command_id=$1`, id)
	return err
}
func (s *PostgresStore) ReleaseCommand(ctx context.Context, id, code string) error {
	_, err := s.pool.Exec(ctx, `UPDATE runtime.command_outbox SET status='pending',last_error_code=$2,next_attempt_at=now()+interval '1 second',claimed_by=NULL,claim_expires_at=NULL WHERE command_id=$1`, id, code)
	return err
}

func (s *PostgresStore) AppendAgentEvent(ctx context.Context, runID string, sequence int64, eventType string, payload json.RawMessage, occurred string) (Run, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(ctx)
	when, err := time.Parse(time.RFC3339Nano, occurred)
	if err != nil {
		when = time.Now().UTC()
	}
	result, err := tx.Exec(ctx, `INSERT INTO runtime.run_events(run_id,agent_sequence,event_type,payload,occurred_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(run_id,agent_sequence) DO NOTHING`, runID, sequence, eventType, payload, when)
	if err != nil {
		return Run{}, err
	}
	if result.RowsAffected() == 0 {
		if err := tx.Rollback(ctx); err != nil {
			return Run{}, err
		}
		return s.GetRun(ctx, runID, false)
	}
	status, terminal := statusForEvent(eventType)
	sets := `persisted_through_sequence=GREATEST(persisted_through_sequence,$2),updated_at=now()`
	args := []any{runID, sequence}
	if status != "" {
		sets += `,status=$3,state_version=state_version+1`
		args = append(args, status)
	}
	if eventType == "turn.started" {
		var p struct {
			ThreadID string `json:"thread_id"`
			TurnID   string `json:"turn_id"`
		}
		_ = json.Unmarshal(payload, &p)
		if _, err := tx.Exec(ctx, `UPDATE runtime.sessions SET codex_thread_id=COALESCE(codex_thread_id,$2),updated_at=now() WHERE session_id=(SELECT session_id FROM runtime.runs WHERE run_id=$1)`, runID, p.ThreadID); err != nil {
			return Run{}, err
		}
		sets += fmt.Sprintf(`,codex_turn_id=$%d,started_at=COALESCE(started_at,now())`, len(args)+1)
		args = append(args, p.TurnID)
	}
	if terminal {
		sets += fmt.Sprintf(`,final_sequence=$%d,finished_at=now()`, len(args)+1)
		args = append(args, sequence)
	}
	if _, err := tx.Exec(ctx, `UPDATE runtime.runs SET `+sets+` WHERE run_id=$1`, args...); err != nil {
		return Run{}, err
	}
	var sessionID string
	if err := tx.QueryRow(ctx, `SELECT session_id FROM runtime.runs WHERE run_id=$1`, runID).Scan(&sessionID); err != nil {
		return Run{}, err
	}
	if status != "" {
		var sessionSequence int64
		if err := tx.QueryRow(ctx, `UPDATE runtime.sessions SET last_session_sequence=last_session_sequence+1,updated_at=now() WHERE session_id=$1 RETURNING last_session_sequence`, sessionID).Scan(&sessionSequence); err != nil {
			return Run{}, err
		}
		sessionType := "run.status.changed"
		if status == "completed" {
			sessionType = "run.completed"
		}
		sp, _ := json.Marshal(map[string]any{"schema_version": 1, "session_id": sessionID, "run_id": runID, "status": status})
		if _, err := tx.Exec(ctx, `INSERT INTO runtime.session_events(session_id,session_sequence,event_type,payload) VALUES($1,$2,$3,$4)`, sessionID, sessionSequence, sessionType, sp); err != nil {
			return Run{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, err
	}
	return s.GetRun(ctx, runID, false)
}

func statusForEvent(event string) (string, bool) {
	switch event {
	case "run.accepted":
		return "accepted", false
	case "turn.started":
		return "running", false
	case "turn.completed":
		return "completed", true
	case "turn.failed":
		return "failed", true
	case "turn.interrupted":
		return "canceled", true
	}
	return "", false
}
func isTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "canceled"
}

func (s *PostgresStore) ListRunEvents(ctx context.Context, runID string, after int64) ([]RunEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT agent_sequence,event_type,payload,occurred_at FROM runtime.run_events WHERE run_id=$1 AND agent_sequence>$2 ORDER BY agent_sequence`, runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RunEvent, 0)
	for rows.Next() {
		var item RunEvent
		if err := rows.Scan(&item.Sequence, &item.Type, &item.Payload, &item.OccurredAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
func (s *PostgresStore) ListSessionEvents(ctx context.Context, sessionID string, after int64) ([]SessionEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT session_sequence,event_type,payload,created_at FROM runtime.session_events WHERE session_id=$1 AND session_sequence>$2 ORDER BY session_sequence`, sessionID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]SessionEvent, 0)
	for rows.Next() {
		var item SessionEvent
		if err := rows.Scan(&item.Sequence, &item.Type, &item.Payload, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *PostgresStore) CreateSyncJob(ctx context.Context, key string) (SyncJob, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SyncJob{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('runtime-bootstrap-sync'))`); err != nil {
		return SyncJob{}, false, err
	}
	var active SyncJob
	err = tx.QueryRow(ctx, syncJobSelect+` WHERE idempotency_key=$1`, key).Scan(&active.ID, &active.Status, &active.SnapshotID, &active.LastCommittedBatchNo, &active.ItemCount, &active.TotalSessions, &active.ProcessedSessions, &active.ArchivedSessions, &active.ArchivedProjects, &active.ReconciliationApplied, &active.ErrorCode, &active.ErrorMessage, &active.CreatedAt, &active.UpdatedAt)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return SyncJob{}, false, err
		}
		return active, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SyncJob{}, false, err
	}
	err = tx.QueryRow(ctx, syncJobSelect+` WHERE status IN ('queued','running') LIMIT 1`).Scan(&active.ID, &active.Status, &active.SnapshotID, &active.LastCommittedBatchNo, &active.ItemCount, &active.TotalSessions, &active.ProcessedSessions, &active.ArchivedSessions, &active.ArchivedProjects, &active.ReconciliationApplied, &active.ErrorCode, &active.ErrorMessage, &active.CreatedAt, &active.UpdatedAt)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return SyncJob{}, false, err
		}
		return active, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SyncJob{}, false, err
	}
	id, commandID := newID("sync"), newID("cmd")
	payload, _ := json.Marshal(map[string]string{"sync_id": id, "command_id": commandID})
	if _, err := tx.Exec(ctx, `INSERT INTO runtime.sync_jobs(sync_id,idempotency_key) VALUES($1,$2)`, id, key); err != nil {
		return SyncJob{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO runtime.command_outbox(command_id,command_type,resource_id,payload) VALUES($1,'bootstrap.start',$2,$3)`, commandID, id, payload); err != nil {
		return SyncJob{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SyncJob{}, false, err
	}
	item, err := s.GetSyncJob(ctx, id)
	return item, true, err
}
func (s *PostgresStore) GetSyncJob(ctx context.Context, id string) (SyncJob, error) {
	var item SyncJob
	err := s.pool.QueryRow(ctx, syncJobSelect+` WHERE sync_id=$1`, id).Scan(&item.ID, &item.Status, &item.SnapshotID, &item.LastCommittedBatchNo, &item.ItemCount, &item.TotalSessions, &item.ProcessedSessions, &item.ArchivedSessions, &item.ArchivedProjects, &item.ReconciliationApplied, &item.ErrorCode, &item.ErrorMessage, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SyncJob{}, ErrNotFound
	}
	return item, err
}

func (s *PostgresStore) ApplyBootstrapBatch(ctx context.Context, b BootstrapBatch) (SyncJob, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SyncJob{}, err
	}
	defer tx.Rollback(ctx)
	var last int64
	var checksum string
	if err := tx.QueryRow(ctx, `SELECT last_committed_batch_no,COALESCE(last_batch_checksum,'') FROM runtime.sync_jobs WHERE sync_id=$1 FOR UPDATE`, b.SyncID).Scan(&last, &checksum); errors.Is(err, pgx.ErrNoRows) {
		return SyncJob{}, ErrNotFound
	} else if err != nil {
		return SyncJob{}, err
	}
	if b.BatchNo == last && b.Checksum == checksum {
		if err := tx.Rollback(ctx); err != nil {
			return SyncJob{}, err
		}
		return s.GetSyncJob(ctx, b.SyncID)
	}
	if b.BatchNo != last+1 {
		return SyncJob{}, fmt.Errorf("bootstrap batch %d is not next after %d", b.BatchNo, last)
	}
	for _, p := range b.Projects {
		if _, err := tx.Exec(ctx, `INSERT INTO runtime.projects(project_id,display_name,last_seen_snapshot_id) VALUES($1,$2,$3)
			ON CONFLICT(project_id) DO UPDATE SET display_name=EXCLUDED.display_name,last_seen_snapshot_id=EXCLUDED.last_seen_snapshot_id,archived_at=NULL,updated_at=now()`, p.ID, p.Name, b.SnapshotID); err != nil {
			return SyncJob{}, err
		}
	}
	sessionIDs := make(map[string]string, len(b.Sessions))
	for _, v := range b.Sessions {
		if _, err := tx.Exec(ctx, `INSERT INTO runtime.sessions(session_id,project_id,codex_thread_id,title) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, v.ID, v.ProjectID, v.CodexThreadID, v.Title); err != nil {
			return SyncJob{}, err
		}
		storedSessionID := v.ID
		if v.CodexThreadID != "" {
			if err := tx.QueryRow(ctx, `SELECT session_id FROM runtime.sessions WHERE session_id=$1 OR codex_thread_id=$2 ORDER BY (session_id=$1) DESC LIMIT 1`, v.ID, v.CodexThreadID).Scan(&storedSessionID); err != nil {
				return SyncJob{}, err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE runtime.sessions SET project_id=$2,title=$3,last_seen_snapshot_id=$4,archived_at=NULL WHERE session_id=$1`, storedSessionID, v.ProjectID, v.Title, b.SnapshotID); err != nil {
			return SyncJob{}, err
		}
		sessionIDs[v.ID] = storedSessionID
	}
	count := int64(0)
	for _, r := range b.Runs {
		status, terminal := normalizeImportedStatus(r.Status)
		if !terminal {
			continue
		}
		sessionID := r.SessionID
		if storedSessionID, ok := sessionIDs[r.SessionID]; ok {
			sessionID = storedSessionID
		}
		storedRunID := r.ID
		var storedSessionID, storedStatus, storedPrompt string
		var storedStartedAt, storedFinishedAt *time.Time
		var storedPersisted int64
		var storedFinal *int64
		found := false
		if r.CodexTurnID != "" {
			err := tx.QueryRow(ctx, `SELECT run_id,session_id,status,COALESCE(prompt_text,''),started_at,finished_at,persisted_through_sequence,final_sequence
				FROM runtime.runs WHERE run_id=$1 OR codex_turn_id=$2 ORDER BY (run_id=$1) DESC LIMIT 1 FOR UPDATE`, r.ID, r.CodexTurnID).
				Scan(&storedRunID, &storedSessionID, &storedStatus, &storedPrompt, &storedStartedAt, &storedFinishedAt, &storedPersisted, &storedFinal)
			if err == nil {
				found = true
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return SyncJob{}, err
			}
		} else {
			err := tx.QueryRow(ctx, `SELECT run_id,session_id,status,COALESCE(prompt_text,''),started_at,finished_at,persisted_through_sequence,final_sequence
				FROM runtime.runs WHERE run_id=$1 FOR UPDATE`, r.ID).
				Scan(&storedRunID, &storedSessionID, &storedStatus, &storedPrompt, &storedStartedAt, &storedFinishedAt, &storedPersisted, &storedFinal)
			if err == nil {
				found = true
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return SyncJob{}, err
			}
		}

		changed := !found
		if found {
			eventsMatch, err := importedEventsMatch(ctx, tx, storedRunID, r.Events)
			if err != nil {
				return SyncJob{}, err
			}
			expectedFinal := int64(len(r.Events))
			changed = storedSessionID != sessionID || storedStatus != status || storedPrompt != r.Prompt ||
				!sameOptionalTime(storedStartedAt, r.StartedAt) || !sameOptionalTime(storedFinishedAt, r.FinishedAt) ||
				storedPersisted != expectedFinal || storedFinal == nil || *storedFinal != expectedFinal || !eventsMatch
		}
		if !changed {
			count++
			continue
		}

		finalSequence := int64(len(r.Events))
		if found {
			if _, err := tx.Exec(ctx, `UPDATE runtime.runs SET session_id=$2,codex_turn_id=NULLIF($3,''),status=$4,state_version=state_version+1,
				prompt_text=$5,cancel_requested_at=NULL,persisted_through_sequence=$6,final_sequence=$6,error_code=NULL,error_message=NULL,
				created_at=COALESCE($7,created_at),started_at=$7,finished_at=$8,updated_at=now() WHERE run_id=$1`,
				storedRunID, sessionID, r.CodexTurnID, status, r.Prompt, finalSequence, r.StartedAt, r.FinishedAt); err != nil {
				return SyncJob{}, err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM runtime.run_events WHERE run_id=$1`, storedRunID); err != nil {
				return SyncJob{}, err
			}
		} else {
			if _, err := tx.Exec(ctx, `INSERT INTO runtime.runs(run_id,session_id,codex_turn_id,status,prompt_text,created_at,started_at,finished_at,persisted_through_sequence,final_sequence)
				VALUES($1,$2,NULLIF($3,''),$4,$5,COALESCE($6,now()),$6,$7,$8,$8)`, r.ID, sessionID, r.CodexTurnID, status, r.Prompt, r.StartedAt, r.FinishedAt, finalSequence); err != nil {
				return SyncJob{}, err
			}
		}
		for _, e := range r.Events {
			if _, err := tx.Exec(ctx, `INSERT INTO runtime.run_events(run_id,agent_sequence,event_type,payload,occurred_at) VALUES($1,$2,$3,$4,$5)`, storedRunID, e.Sequence, e.Type, e.Payload, e.OccurredAt); err != nil {
				return SyncJob{}, err
			}
		}
		var sessionSequence int64
		if err := tx.QueryRow(ctx, `UPDATE runtime.sessions SET last_session_sequence=last_session_sequence+1,updated_at=now() WHERE session_id=$1 RETURNING last_session_sequence`, sessionID).Scan(&sessionSequence); err != nil {
			return SyncJob{}, err
		}
		sessionEventType := "run.status.changed"
		if status == "completed" {
			sessionEventType = "run.completed"
		}
		payload, _ := json.Marshal(map[string]any{"schema_version": 1, "session_id": sessionID, "run_id": storedRunID, "status": status})
		if _, err := tx.Exec(ctx, `INSERT INTO runtime.session_events(session_id,session_sequence,event_type,payload) VALUES($1,$2,$3,$4)`, sessionID, sessionSequence, sessionEventType, payload); err != nil {
			return SyncJob{}, err
		}
		count++
	}
	status := "running"
	if b.Done {
		status = "completed"
	}
	archivedSessions := int64(0)
	archivedProjects := int64(0)
	if b.Done && b.ReconciliationSafe && b.SnapshotID != "" {
		result, err := tx.Exec(ctx, `UPDATE runtime.sessions AS s SET archived_at=now()
			WHERE s.codex_thread_id IS NOT NULL
			  AND s.archived_at IS NULL
			  AND s.last_seen_snapshot_id IS DISTINCT FROM $2
			  AND s.updated_at <= (SELECT created_at FROM runtime.sync_jobs WHERE sync_id=$1)
			  AND NOT EXISTS (
				SELECT 1 FROM runtime.runs AS r
				WHERE r.session_id=s.session_id
				  AND r.status IN ('queued','dispatching','accepted','running','waiting_agent','recovering','finalizing')
			  )`, b.SyncID, b.SnapshotID)
		if err != nil {
			return SyncJob{}, err
		}
		archivedSessions = result.RowsAffected()
		result, err = tx.Exec(ctx, `UPDATE runtime.projects AS p SET archived_at=now(),updated_at=now()
			WHERE p.archived_at IS NULL
			  AND p.last_seen_snapshot_id IS DISTINCT FROM $2
			  AND p.updated_at <= (SELECT created_at FROM runtime.sync_jobs WHERE sync_id=$1)
			  AND NOT EXISTS (
				SELECT 1 FROM runtime.sessions AS s JOIN runtime.runs AS r ON r.session_id=s.session_id
				WHERE s.project_id=p.project_id
				  AND r.status IN ('queued','dispatching','accepted','running','waiting_agent','recovering','finalizing')
			  )`, b.SyncID, b.SnapshotID)
		if err != nil {
			return SyncJob{}, err
		}
		archivedProjects = result.RowsAffected()
	}
	if _, err := tx.Exec(ctx, `UPDATE runtime.sync_jobs SET status=$2,snapshot_id=$3,last_committed_batch_no=$4,last_batch_checksum=$5,item_count=item_count+$6,total_sessions=GREATEST(total_sessions,$7),processed_sessions=GREATEST(processed_sessions,$8),archived_sessions=archived_sessions+$9,archived_projects=archived_projects+$10,reconciliation_applied=reconciliation_applied OR $11,updated_at=now() WHERE sync_id=$1`, b.SyncID, status, b.SnapshotID, b.BatchNo, b.Checksum, count, b.TotalSessions, b.ProcessedSessions, archivedSessions, archivedProjects, b.Done && b.ReconciliationSafe); err != nil {
		return SyncJob{}, err
	}
	if b.Done && b.CommandID != "" {
		if _, err := tx.Exec(ctx, `UPDATE runtime.command_outbox SET status='delivered',delivered_at=COALESCE(delivered_at,now()),claimed_by=NULL,claim_expires_at=NULL WHERE command_id=$1 AND command_type='bootstrap.start' AND resource_id=$2`, b.CommandID, b.SyncID); err != nil {
			return SyncJob{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return SyncJob{}, err
	}
	return s.GetSyncJob(ctx, b.SyncID)
}

func normalizeImportedStatus(value string) (string, bool) {
	switch value {
	case "completed":
		return "completed", true
	case "failed":
		return "failed", true
	case "interrupted", "canceled", "cancelled":
		return "canceled", true
	default:
		return "", false
	}
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.UTC().Truncate(time.Microsecond).Equal(right.UTC().Truncate(time.Microsecond))
}

func importedEventsMatch(ctx context.Context, tx pgx.Tx, runID string, expected []BootstrapEvent) (bool, error) {
	rows, err := tx.Query(ctx, `SELECT agent_sequence,event_type,payload,occurred_at FROM runtime.run_events WHERE run_id=$1 ORDER BY agent_sequence`, runID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		if index >= len(expected) {
			return false, nil
		}
		var sequence int64
		var eventType string
		var payload json.RawMessage
		var occurredAt time.Time
		if err := rows.Scan(&sequence, &eventType, &payload, &occurredAt); err != nil {
			return false, err
		}
		want := expected[index]
		if sequence != want.Sequence || eventType != want.Type ||
			!occurredAt.UTC().Truncate(time.Microsecond).Equal(want.OccurredAt.UTC().Truncate(time.Microsecond)) ||
			!jsonPayloadEqual(payload, want.Payload) {
			return false, nil
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return index == len(expected), nil
}

func jsonPayloadEqual(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
func RequestHash(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func newID(prefix string) string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(data[:])
}

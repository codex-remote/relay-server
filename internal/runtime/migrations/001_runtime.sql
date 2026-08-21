CREATE SCHEMA IF NOT EXISTS runtime;

CREATE TABLE IF NOT EXISTS runtime.projects (
    project_id text PRIMARY KEY,
    display_name text NOT NULL
);

CREATE TABLE IF NOT EXISTS runtime.sessions (
    session_id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES runtime.projects(project_id),
    codex_thread_id text,
    title text,
    last_session_sequence bigint NOT NULL DEFAULT 0 CHECK (last_session_sequence >= 0),
    idempotency_key text,
    request_hash text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((idempotency_key IS NULL) = (request_hash IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS sessions_codex_thread_uidx ON runtime.sessions(codex_thread_id) WHERE codex_thread_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS sessions_idempotency_uidx ON runtime.sessions(idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS sessions_project_idx ON runtime.sessions(project_id);

CREATE TABLE IF NOT EXISTS runtime.runs (
    run_id text PRIMARY KEY,
    session_id text NOT NULL REFERENCES runtime.sessions(session_id),
    codex_turn_id text,
    status text NOT NULL CHECK (status IN ('queued','dispatching','accepted','running','waiting_agent','recovering','finalizing','completed','failed','canceled')),
    state_version bigint NOT NULL DEFAULT 1 CHECK (state_version > 0),
    idempotency_key text,
    request_hash text,
    prompt_text text,
    cancel_requested_at timestamptz,
    persisted_through_sequence bigint NOT NULL DEFAULT 0 CHECK (persisted_through_sequence >= 0),
    final_sequence bigint CHECK (final_sequence > 0),
    error_code text,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((idempotency_key IS NULL) = (request_hash IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS runs_session_idempotency_uidx ON runtime.runs(session_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS runs_codex_turn_uidx ON runtime.runs(codex_turn_id) WHERE codex_turn_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS runs_session_cursor_idx ON runtime.runs(session_id, created_at DESC, run_id DESC);

CREATE TABLE IF NOT EXISTS runtime.session_events (
    session_id text NOT NULL REFERENCES runtime.sessions(session_id),
    session_sequence bigint NOT NULL CHECK (session_sequence > 0),
    event_type text NOT NULL CHECK (event_type IN ('run.created','run.status.changed','run.completed')),
    schema_version integer NOT NULL DEFAULT 1,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, session_sequence)
);

CREATE TABLE IF NOT EXISTS runtime.run_events (
    run_id text NOT NULL REFERENCES runtime.runs(run_id),
    agent_sequence bigint NOT NULL CHECK (agent_sequence > 0),
    event_type text NOT NULL,
    schema_version integer NOT NULL DEFAULT 1,
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    persisted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, agent_sequence)
);

CREATE TABLE IF NOT EXISTS runtime.command_outbox (
    command_id text PRIMARY KEY,
    command_type text NOT NULL CHECK (command_type IN ('run.start','run.cancel','bootstrap.start')),
    resource_id text NOT NULL,
    schema_version integer NOT NULL DEFAULT 1,
    payload jsonb NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','claimed','delivered','dead')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    claimed_by text,
    claim_expires_at timestamptz,
    delivered_at timestamptz,
    last_error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (command_type, resource_id),
    CHECK (status <> 'delivered' OR delivered_at IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS command_outbox_claim_idx ON runtime.command_outbox(status, next_attempt_at, created_at);

CREATE TABLE IF NOT EXISTS runtime.sync_jobs (
    sync_id text PRIMARY KEY,
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','completed','failed')),
    idempotency_key text NOT NULL UNIQUE,
    snapshot_id text,
    last_committed_batch_no bigint NOT NULL DEFAULT -1 CHECK (last_committed_batch_no >= -1),
    last_batch_checksum text,
    item_count bigint NOT NULL DEFAULT 0 CHECK (item_count >= 0),
    error_code text,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS sync_jobs_one_active_uidx ON runtime.sync_jobs((true)) WHERE status IN ('queued','running');

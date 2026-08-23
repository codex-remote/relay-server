ALTER TABLE runtime.projects
    ADD COLUMN IF NOT EXISTS last_seen_snapshot_id text,
    ADD COLUMN IF NOT EXISTS archived_at timestamptz,
    ADD COLUMN IF NOT EXISTS created_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS projects_visible_name_idx
    ON runtime.projects(display_name, project_id)
    WHERE archived_at IS NULL;

ALTER TABLE runtime.sessions
    ADD COLUMN IF NOT EXISTS last_seen_snapshot_id text,
    ADD COLUMN IF NOT EXISTS archived_at timestamptz;

CREATE INDEX IF NOT EXISTS sessions_visible_project_idx
    ON runtime.sessions(project_id, updated_at DESC)
    WHERE archived_at IS NULL;

ALTER TABLE runtime.sync_jobs
    ADD COLUMN IF NOT EXISTS total_sessions bigint NOT NULL DEFAULT 0 CHECK (total_sessions >= 0),
    ADD COLUMN IF NOT EXISTS processed_sessions bigint NOT NULL DEFAULT 0 CHECK (processed_sessions >= 0),
    ADD COLUMN IF NOT EXISTS archived_sessions bigint NOT NULL DEFAULT 0 CHECK (archived_sessions >= 0),
    ADD COLUMN IF NOT EXISTS archived_projects bigint NOT NULL DEFAULT 0 CHECK (archived_projects >= 0),
    ADD COLUMN IF NOT EXISTS reconciliation_applied boolean NOT NULL DEFAULT false;

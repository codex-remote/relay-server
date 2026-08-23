CREATE SCHEMA IF NOT EXISTS auth;

CREATE TABLE IF NOT EXISTS auth.clients (
    client_id text PRIMARY KEY,
    display_name text NOT NULL,
    scopes text[] NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz,
    revoked_at timestamptz
);

CREATE TABLE IF NOT EXISTS auth.pairing_grants (
    grant_id text PRIMARY KEY,
    code_hash bytea NOT NULL UNIQUE,
    display_name text NOT NULL,
    scopes text[] NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    consumed_at timestamptz,
    client_id text REFERENCES auth.clients(client_id)
);

CREATE INDEX IF NOT EXISTS pairing_grants_expires_idx
    ON auth.pairing_grants(expires_at) WHERE consumed_at IS NULL;

CREATE TABLE IF NOT EXISTS auth.refresh_sessions (
    session_id text PRIMARY KEY,
    client_id text NOT NULL REFERENCES auth.clients(client_id) ON DELETE CASCADE,
    current_token_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    rotated_at timestamptz,
    revoked_at timestamptz,
    revoke_reason text
);

CREATE INDEX IF NOT EXISTS refresh_sessions_client_idx
    ON auth.refresh_sessions(client_id);

CREATE TABLE IF NOT EXISTS auth.refresh_token_history (
    token_hash bytea PRIMARY KEY,
    session_id text NOT NULL REFERENCES auth.refresh_sessions(session_id) ON DELETE CASCADE,
    used_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS auth.access_tokens (
    token_id text PRIMARY KEY,
    session_id text NOT NULL REFERENCES auth.refresh_sessions(session_id) ON DELETE CASCADE,
    client_id text NOT NULL REFERENCES auth.clients(client_id) ON DELETE CASCADE,
    secret_hash bytea NOT NULL,
    scopes text[] NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);

CREATE INDEX IF NOT EXISTS access_tokens_session_idx
    ON auth.access_tokens(session_id);
CREATE INDEX IF NOT EXISTS access_tokens_expiry_idx
    ON auth.access_tokens(expires_at) WHERE revoked_at IS NULL;

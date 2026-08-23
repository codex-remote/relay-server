package auth

import (
	"context"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
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

func OpenPostgres(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open auth PostgreSQL: %w", err)
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
			return fmt.Errorf("apply auth migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (s *PostgresStore) CreatePairingGrant(ctx context.Context, grant PairingGrant) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO auth.pairing_grants
		(grant_id,code_hash,display_name,scopes,expires_at,created_at)
		VALUES($1,$2,$3,$4,$5,$6)`, grant.ID, grant.CodeHash, grant.Name, grant.Scopes, grant.ExpiresAt, grant.CreatedAt)
	return err
}

func (s *PostgresStore) ExchangePairingGrant(ctx context.Context, codeHash []byte, now time.Time, material CredentialMaterial) (Client, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Client{}, err
	}
	defer tx.Rollback(ctx)

	var grantID, name string
	var scopes []string
	var expiresAt time.Time
	var consumedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT grant_id,display_name,scopes,expires_at,consumed_at
		FROM auth.pairing_grants WHERE code_hash=$1 FOR UPDATE`, codeHash).
		Scan(&grantID, &name, &scopes, &expiresAt, &consumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Client{}, ErrInvalidCredential
	}
	if err != nil {
		return Client{}, err
	}
	if consumedAt != nil {
		return Client{}, ErrConsumedGrant
	}
	if !expiresAt.After(now) {
		return Client{}, ErrExpiredCredential
	}

	if _, err := tx.Exec(ctx, `INSERT INTO auth.clients(client_id,display_name,scopes,created_at)
		VALUES($1,$2,$3,$4)`, material.ClientID, name, scopes, now); err != nil {
		return Client{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO auth.refresh_sessions
		(session_id,client_id,current_token_hash,expires_at,created_at)
		VALUES($1,$2,$3,$4,$5)`, material.SessionID, material.ClientID, material.RefreshHash, material.RefreshExpires, now); err != nil {
		return Client{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO auth.access_tokens
		(token_id,session_id,client_id,secret_hash,scopes,expires_at,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, material.AccessTokenID, material.SessionID, material.ClientID, material.AccessHash, scopes, material.AccessExpires, now); err != nil {
		return Client{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE auth.pairing_grants SET consumed_at=$2,client_id=$3 WHERE grant_id=$1`, grantID, now, material.ClientID); err != nil {
		return Client{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Client{}, err
	}
	return Client{ID: material.ClientID, Name: name, Scopes: scopes, CreatedAt: now}, nil
}

func (s *PostgresStore) AuthenticateAccess(ctx context.Context, tokenID string, presentedHash []byte, now time.Time) (Principal, error) {
	var principal Principal
	var storedHash []byte
	var expiresAt time.Time
	var tokenRevoked, sessionRevoked, clientRevoked *time.Time
	err := s.pool.QueryRow(ctx, `SELECT t.secret_hash,t.expires_at,t.revoked_at,s.revoked_at,c.revoked_at,
		c.client_id,c.display_name,s.session_id,t.scopes
		FROM auth.access_tokens t
		JOIN auth.refresh_sessions s ON s.session_id=t.session_id
		JOIN auth.clients c ON c.client_id=t.client_id
		WHERE t.token_id=$1`, tokenID).Scan(
		&storedHash, &expiresAt, &tokenRevoked, &sessionRevoked, &clientRevoked,
		&principal.ClientID, &principal.ClientName, &principal.SessionID, &principal.Scopes,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrInvalidCredential
	}
	if err != nil {
		return Principal{}, err
	}
	if subtle.ConstantTimeCompare(storedHash, presentedHash) != 1 {
		return Principal{}, ErrInvalidCredential
	}
	if tokenRevoked != nil || sessionRevoked != nil || clientRevoked != nil {
		return Principal{}, ErrRevoked
	}
	if !expiresAt.After(now) {
		return Principal{}, ErrExpiredCredential
	}
	_, _ = s.pool.Exec(ctx, `UPDATE auth.clients SET last_seen_at=$2 WHERE client_id=$1 AND (last_seen_at IS NULL OR last_seen_at < $2 - interval '1 minute')`, principal.ClientID, now)
	return principal, nil
}

func (s *PostgresStore) RotateRefresh(ctx context.Context, sessionID string, presentedHash []byte, now time.Time, material RotatedMaterial) (Client, time.Time, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Client{}, time.Time{}, err
	}
	defer tx.Rollback(ctx)

	var currentHash []byte
	var refreshExpiry time.Time
	var sessionRevoked, clientRevoked *time.Time
	var client Client
	err = tx.QueryRow(ctx, `SELECT s.current_token_hash,s.expires_at,s.revoked_at,c.revoked_at,
		c.client_id,c.display_name,c.scopes,c.created_at,c.last_seen_at,c.revoked_at
		FROM auth.refresh_sessions s JOIN auth.clients c ON c.client_id=s.client_id
		WHERE s.session_id=$1 FOR UPDATE OF s,c`, sessionID).Scan(
		&currentHash, &refreshExpiry, &sessionRevoked, &clientRevoked,
		&client.ID, &client.Name, &client.Scopes, &client.CreatedAt, &client.LastSeenAt, &client.RevokedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Client{}, time.Time{}, ErrInvalidCredential
	}
	if err != nil {
		return Client{}, time.Time{}, err
	}
	if sessionRevoked != nil || clientRevoked != nil {
		return Client{}, time.Time{}, ErrRevoked
	}
	if !refreshExpiry.After(now) {
		return Client{}, time.Time{}, ErrExpiredCredential
	}
	if subtle.ConstantTimeCompare(currentHash, presentedHash) != 1 {
		var replay bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM auth.refresh_token_history WHERE token_hash=$1 AND session_id=$2)`, presentedHash, sessionID).Scan(&replay); err != nil {
			return Client{}, time.Time{}, err
		}
		if replay {
			if err := revokeSessionTx(ctx, tx, sessionID, now, "refresh_replay"); err != nil {
				return Client{}, time.Time{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return Client{}, time.Time{}, err
			}
			return Client{}, time.Time{}, ErrRefreshReplay
		}
		return Client{}, time.Time{}, ErrInvalidCredential
	}

	if _, err := tx.Exec(ctx, `INSERT INTO auth.refresh_token_history(token_hash,session_id,used_at) VALUES($1,$2,$3)`, currentHash, sessionID, now); err != nil {
		return Client{}, time.Time{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE auth.refresh_sessions SET current_token_hash=$2,rotated_at=$3 WHERE session_id=$1`, sessionID, material.RefreshHash, now); err != nil {
		return Client{}, time.Time{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO auth.access_tokens
		(token_id,session_id,client_id,secret_hash,scopes,expires_at,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, material.AccessTokenID, sessionID, client.ID, material.AccessHash, client.Scopes, material.AccessExpires, now); err != nil {
		return Client{}, time.Time{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE auth.clients SET last_seen_at=$2 WHERE client_id=$1`, client.ID, now); err != nil {
		return Client{}, time.Time{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Client{}, time.Time{}, err
	}
	client.LastSeenAt = &now
	return client, refreshExpiry, nil
}

func (s *PostgresStore) RevokeSession(ctx context.Context, sessionID string, presentedHash []byte, now time.Time, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentHash []byte
	if err := tx.QueryRow(ctx, `SELECT current_token_hash FROM auth.refresh_sessions WHERE session_id=$1 FOR UPDATE`, sessionID).Scan(&currentHash); errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidCredential
	} else if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(currentHash, presentedHash) != 1 {
		return ErrInvalidCredential
	}
	if err := revokeSessionTx(ctx, tx, sessionID, now, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func revokeSessionTx(ctx context.Context, tx pgx.Tx, sessionID string, now time.Time, reason string) error {
	if _, err := tx.Exec(ctx, `UPDATE auth.refresh_sessions SET revoked_at=COALESCE(revoked_at,$2),revoke_reason=COALESCE(revoke_reason,$3) WHERE session_id=$1`, sessionID, now, reason); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE auth.access_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE session_id=$1`, sessionID, now)
	return err
}

func (s *PostgresStore) ListClients(ctx context.Context) ([]Client, error) {
	rows, err := s.pool.Query(ctx, `SELECT client_id,display_name,scopes,created_at,last_seen_at,revoked_at FROM auth.clients ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	clients := make([]Client, 0)
	for rows.Next() {
		var client Client
		if err := rows.Scan(&client.ID, &client.Name, &client.Scopes, &client.CreatedAt, &client.LastSeenAt, &client.RevokedAt); err != nil {
			return nil, err
		}
		clients = append(clients, client)
	}
	return clients, rows.Err()
}

func (s *PostgresStore) RevokeClient(ctx context.Context, clientID string, now time.Time, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE auth.clients SET revoked_at=COALESCE(revoked_at,$2) WHERE client_id=$1`, clientID, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE auth.refresh_sessions SET revoked_at=COALESCE(revoked_at,$2),revoke_reason=COALESCE(revoke_reason,$3) WHERE client_id=$1`, clientID, now, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE auth.access_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE client_id=$1`, clientID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) Health(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *PostgresStore) Close()                           { s.pool.Close() }

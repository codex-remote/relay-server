package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

type Config struct {
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	PairingTTL time.Duration
}

type Service struct {
	store Store
	now   func() time.Time
	conf  Config
}

func NewService(store Store, conf Config) *Service {
	if conf.AccessTTL <= 0 {
		conf.AccessTTL = 15 * time.Minute
	}
	if conf.RefreshTTL <= 0 {
		conf.RefreshTTL = 30 * 24 * time.Hour
	}
	if conf.PairingTTL <= 0 {
		conf.PairingTTL = 10 * time.Minute
	}
	return &Service{store: store, now: time.Now, conf: conf}
}

func (s *Service) CreatePairingGrant(ctx context.Context, name string, scopes []string) (PairingLink, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 {
		return PairingLink{}, fmt.Errorf("client name must be 1-120 characters")
	}
	normalized, err := normalizeScopes(scopes)
	if err != nil {
		return PairingLink{}, err
	}
	code, err := randomSecret(32)
	if err != nil {
		return PairingLink{}, err
	}
	now := s.now().UTC()
	grantID, err := randomID("grant")
	if err != nil {
		return PairingLink{}, err
	}
	grant := PairingGrant{
		ID:        grantID,
		CodeHash:  tokenHash(code),
		Name:      name,
		Scopes:    normalized,
		CreatedAt: now,
		ExpiresAt: now.Add(s.conf.PairingTTL),
	}
	if err := s.store.CreatePairingGrant(ctx, grant); err != nil {
		return PairingLink{}, err
	}
	return PairingLink{Code: code, ExpiresAt: grant.ExpiresAt}, nil
}

func (s *Service) ExchangePairingGrant(ctx context.Context, code string) (TokenPair, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 512 {
		return TokenPair{}, ErrInvalidCredential
	}
	now := s.now().UTC()
	accessID, accessSecret, accessToken, err := newToken("at")
	if err != nil {
		return TokenPair{}, err
	}
	sessionID, refreshSecret, refreshToken, err := newToken("rt")
	if err != nil {
		return TokenPair{}, err
	}
	clientID, err := randomID("client")
	if err != nil {
		return TokenPair{}, err
	}
	material := CredentialMaterial{
		ClientID:       clientID,
		SessionID:      sessionID,
		AccessTokenID:  accessID,
		AccessHash:     tokenHash(accessSecret),
		RefreshHash:    tokenHash(refreshSecret),
		AccessExpires:  now.Add(s.conf.AccessTTL),
		RefreshExpires: now.Add(s.conf.RefreshTTL),
	}
	client, err := s.store.ExchangePairingGrant(ctx, tokenHash(code), now, material)
	if err != nil {
		return TokenPair{}, err
	}
	return s.tokenPair(accessToken, refreshToken, material.AccessExpires, material.RefreshExpires, client, now), nil
}

func (s *Service) Authenticate(ctx context.Context, bearer string) (Principal, error) {
	id, secret, err := parseToken(bearer, "at")
	if err != nil {
		return Principal{}, ErrInvalidCredential
	}
	return s.store.AuthenticateAccess(ctx, id, tokenHash(secret), s.now().UTC())
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	sessionID, secret, err := parseToken(refreshToken, "rt")
	if err != nil {
		return TokenPair{}, ErrInvalidCredential
	}
	now := s.now().UTC()
	accessID, accessSecret, accessToken, err := newToken("at")
	if err != nil {
		return TokenPair{}, err
	}
	_, refreshSecret, nextRefreshToken, err := newTokenWithID("rt", sessionID)
	if err != nil {
		return TokenPair{}, err
	}
	material := RotatedMaterial{
		AccessTokenID: accessID,
		AccessHash:    tokenHash(accessSecret),
		RefreshHash:   tokenHash(refreshSecret),
		AccessExpires: now.Add(s.conf.AccessTTL),
	}
	client, refreshExpires, err := s.store.RotateRefresh(ctx, sessionID, tokenHash(secret), now, material)
	if err != nil {
		return TokenPair{}, err
	}
	return s.tokenPair(accessToken, nextRefreshToken, material.AccessExpires, refreshExpires, client, now), nil
}

func (s *Service) RevokeSession(ctx context.Context, refreshToken, reason string) error {
	sessionID, secret, err := parseToken(refreshToken, "rt")
	if err != nil {
		return ErrInvalidCredential
	}
	return s.store.RevokeSession(ctx, sessionID, tokenHash(secret), s.now().UTC(), reason)
}

func (s *Service) ListClients(ctx context.Context) ([]Client, error) {
	return s.store.ListClients(ctx)
}

func (s *Service) RevokeClient(ctx context.Context, clientID, reason string) error {
	if strings.TrimSpace(clientID) == "" {
		return ErrNotFound
	}
	return s.store.RevokeClient(ctx, clientID, s.now().UTC(), reason)
}

func (s *Service) tokenPair(access, refresh string, accessExpiry, refreshExpiry time.Time, client Client, now time.Time) TokenPair {
	return TokenPair{
		AccessToken: access, TokenType: "Bearer", ExpiresIn: int64(accessExpiry.Sub(now).Seconds()),
		AccessExpiresAt: accessExpiry, RefreshToken: refresh, RefreshExpires: refreshExpiry, Client: client,
	}
}

func normalizeScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		scopes = DefaultScopes
	}
	allowed := map[string]bool{ScopeRuntimeRead: true, ScopeRuntimeWrite: true, ScopeSourceRead: true}
	seen := make(map[string]bool)
	result := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if !allowed[scope] {
			return nil, fmt.Errorf("unsupported scope %q", scope)
		}
		if !seen[scope] {
			seen[scope] = true
			result = append(result, scope)
		}
	}
	return result, nil
}

func randomID(prefix string) (string, error) {
	secret, err := randomSecret(18)
	if err != nil {
		return "", err
	}
	return prefix + "_" + secret, nil
}

func newToken(prefix string) (string, string, string, error) {
	id, err := randomID(prefix)
	if err != nil {
		return "", "", "", err
	}
	return newTokenWithID(prefix, id)
}

func newTokenWithID(prefix, id string) (string, string, string, error) {
	secret, err := randomSecret(32)
	if err != nil {
		return "", "", "", err
	}
	return id, secret, id + "." + secret, nil
}

func randomSecret(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func tokenHash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func parseToken(value, prefix string) (string, string, error) {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, ".")
	if len(parts) != 2 || !strings.HasPrefix(parts[0], prefix+"_") || len(parts[1]) < 32 {
		return "", "", ErrInvalidCredential
	}
	return parts[0], parts[1], nil
}

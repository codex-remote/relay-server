package auth

import (
	"errors"
	"time"
)

var (
	ErrInvalidCredential = errors.New("invalid credential")
	ErrExpiredCredential = errors.New("expired credential")
	ErrConsumedGrant     = errors.New("pairing grant already consumed")
	ErrRefreshReplay     = errors.New("refresh token replay detected")
	ErrRevoked           = errors.New("credential revoked")
	ErrNotFound          = errors.New("not found")
)

const (
	ScopeRuntimeRead  = "runtime:read"
	ScopeRuntimeWrite = "runtime:write"
	ScopeSourceRead   = "source:read"
)

var DefaultScopes = []string{ScopeRuntimeRead, ScopeRuntimeWrite, ScopeSourceRead}

type Principal struct {
	ClientID   string   `json:"client_id"`
	ClientName string   `json:"client_name"`
	SessionID  string   `json:"-"`
	Scopes     []string `json:"scopes"`
}

func (p Principal) HasScope(scope string) bool {
	for _, candidate := range p.Scopes {
		if candidate == scope {
			return true
		}
	}
	return false
}

type Client struct {
	ID         string     `json:"client_id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type PairingGrant struct {
	ID        string
	CodeHash  []byte
	Name      string
	Scopes    []string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type CredentialMaterial struct {
	ClientID       string
	SessionID      string
	AccessTokenID  string
	AccessHash     []byte
	RefreshHash    []byte
	AccessExpires  time.Time
	RefreshExpires time.Time
}

type RotatedMaterial struct {
	AccessTokenID string
	AccessHash    []byte
	RefreshHash   []byte
	AccessExpires time.Time
}

type TokenPair struct {
	AccessToken     string    `json:"access_token"`
	TokenType       string    `json:"token_type"`
	ExpiresIn       int64     `json:"expires_in"`
	AccessExpiresAt time.Time `json:"access_expires_at"`
	RefreshToken    string    `json:"-"`
	RefreshExpires  time.Time `json:"-"`
	Client          Client    `json:"client"`
}

type PairingLink struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

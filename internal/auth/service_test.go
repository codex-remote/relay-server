package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type memorySession struct {
	clientID string
	current  []byte
	history  [][]byte
	expires  time.Time
	revoked  bool
}

type memoryAccess struct {
	principal Principal
	hash      []byte
	expires   time.Time
	revoked   bool
}

type memoryStore struct {
	mu       sync.Mutex
	grant    PairingGrant
	consumed bool
	clients  map[string]Client
	sessions map[string]*memorySession
	access   map[string]*memoryAccess
}

func newMemoryStore() *memoryStore {
	return &memoryStore{clients: map[string]Client{}, sessions: map[string]*memorySession{}, access: map[string]*memoryAccess{}}
}

func (s *memoryStore) CreatePairingGrant(_ context.Context, grant PairingGrant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.grant = grant
	s.consumed = false
	return nil
}

func (s *memoryStore) ExchangePairingGrant(_ context.Context, hash []byte, now time.Time, material CredentialMaterial) (Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !bytes.Equal(hash, s.grant.CodeHash) {
		return Client{}, ErrInvalidCredential
	}
	if s.consumed {
		return Client{}, ErrConsumedGrant
	}
	if !s.grant.ExpiresAt.After(now) {
		return Client{}, ErrExpiredCredential
	}
	s.consumed = true
	client := Client{ID: material.ClientID, Name: s.grant.Name, Scopes: s.grant.Scopes, CreatedAt: now}
	s.clients[client.ID] = client
	s.sessions[material.SessionID] = &memorySession{clientID: client.ID, current: material.RefreshHash, expires: material.RefreshExpires}
	s.access[material.AccessTokenID] = &memoryAccess{principal: Principal{ClientID: client.ID, ClientName: client.Name, SessionID: material.SessionID, Scopes: client.Scopes}, hash: material.AccessHash, expires: material.AccessExpires}
	return client, nil
}

func (s *memoryStore) AuthenticateAccess(_ context.Context, id string, hash []byte, now time.Time) (Principal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	access := s.access[id]
	if access == nil || !bytes.Equal(access.hash, hash) {
		return Principal{}, ErrInvalidCredential
	}
	client := s.clients[access.principal.ClientID]
	session := s.sessions[access.principal.SessionID]
	if access.revoked || client.RevokedAt != nil || session.revoked {
		return Principal{}, ErrRevoked
	}
	if !access.expires.After(now) {
		return Principal{}, ErrExpiredCredential
	}
	return access.principal, nil
}

func (s *memoryStore) RotateRefresh(_ context.Context, id string, hash []byte, now time.Time, material RotatedMaterial) (Client, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[id]
	if session == nil {
		return Client{}, time.Time{}, ErrInvalidCredential
	}
	if session.revoked {
		return Client{}, time.Time{}, ErrRevoked
	}
	if !session.expires.After(now) {
		return Client{}, time.Time{}, ErrExpiredCredential
	}
	if !bytes.Equal(session.current, hash) {
		for _, used := range session.history {
			if bytes.Equal(used, hash) {
				session.revoked = true
				for _, access := range s.access {
					if access.principal.SessionID == id {
						access.revoked = true
					}
				}
				return Client{}, time.Time{}, ErrRefreshReplay
			}
		}
		return Client{}, time.Time{}, ErrInvalidCredential
	}
	session.history = append(session.history, session.current)
	session.current = material.RefreshHash
	client := s.clients[session.clientID]
	s.access[material.AccessTokenID] = &memoryAccess{principal: Principal{ClientID: client.ID, ClientName: client.Name, SessionID: id, Scopes: client.Scopes}, hash: material.AccessHash, expires: material.AccessExpires}
	return client, session.expires, nil
}

func (s *memoryStore) RevokeSession(_ context.Context, id string, hash []byte, _ time.Time, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[id]
	if session == nil || !bytes.Equal(session.current, hash) {
		return ErrInvalidCredential
	}
	session.revoked = true
	return nil
}

func (s *memoryStore) ListClients(context.Context) ([]Client, error) {
	result := make([]Client, 0, len(s.clients))
	for _, client := range s.clients {
		result = append(result, client)
	}
	return result, nil
}

func (s *memoryStore) RevokeClient(_ context.Context, id string, now time.Time, _ string) error {
	client, ok := s.clients[id]
	if !ok {
		return ErrNotFound
	}
	client.RevokedAt = &now
	s.clients[id] = client
	for _, session := range s.sessions {
		if session.clientID == id {
			session.revoked = true
		}
	}
	return nil
}
func (s *memoryStore) Health(context.Context) error { return nil }
func (s *memoryStore) Close()                       {}

func pairedService(t *testing.T) (*Service, *memoryStore, PairingLink, TokenPair) {
	t.Helper()
	store := newMemoryStore()
	service := NewService(store, Config{AccessTTL: time.Minute, RefreshTTL: time.Hour, PairingTTL: time.Minute})
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	grant, err := service.CreatePairingGrant(context.Background(), "My iPhone", nil)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := service.ExchangePairingGrant(context.Background(), grant.Code)
	if err != nil {
		t.Fatal(err)
	}
	return service, store, grant, pair
}

func TestPairingGrantIsOneTimeAndExpires(t *testing.T) {
	service, store, grant, _ := pairedService(t)
	if _, err := service.ExchangePairingGrant(context.Background(), grant.Code); !errors.Is(err, ErrConsumedGrant) {
		t.Fatalf("second exchange error = %v", err)
	}
	store.consumed = false
	service.now = func() time.Time { return grant.ExpiresAt }
	if _, err := service.ExchangePairingGrant(context.Background(), grant.Code); !errors.Is(err, ErrExpiredCredential) {
		t.Fatalf("expired exchange error = %v", err)
	}
}

func TestAccessAuthenticationAndScopeMiddleware(t *testing.T) {
	service, _, _, pair := pairedService(t)
	principal, err := service.Authenticate(context.Background(), pair.AccessToken)
	if err != nil || !principal.HasScope(ScopeRuntimeWrite) {
		t.Fatalf("principal = %#v, error = %v", principal, err)
	}
	handler := service.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := PrincipalFromContext(r.Context()); !ok {
			t.Fatal("principal missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	}), func(*http.Request) string { return ScopeRuntimeWrite })
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/runtime/projects", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	authorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/runtime/sessions", nil)
	request.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	handler.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusNoContent {
		t.Fatalf("authorized status = %d", authorized.Code)
	}
	forbiddenHandler := service.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler ran without the caller-required scope")
	}), func(*http.Request) string { return "admin:write" })
	forbidden := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/unrelated-business-route", nil)
	request.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	forbiddenHandler.ServeHTTP(forbidden, request)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("caller-required scope status = %d", forbidden.Code)
	}
}

func TestRefreshRotationAndReplayRevokesSession(t *testing.T) {
	service, _, _, first := pairedService(t)
	second, err := service.Refresh(context.Background(), first.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), first.AccessToken); err != nil {
		t.Fatalf("old access should remain valid until expiry: %v", err)
	}
	if _, err := service.Authenticate(context.Background(), second.AccessToken); err != nil {
		t.Fatalf("new access error = %v", err)
	}
	if _, err := service.Refresh(context.Background(), first.RefreshToken); !errors.Is(err, ErrRefreshReplay) {
		t.Fatalf("replay error = %v", err)
	}
	if _, err := service.Authenticate(context.Background(), second.AccessToken); !errors.Is(err, ErrRevoked) {
		t.Fatalf("access after replay error = %v", err)
	}
}

func TestRevokeClientInvalidatesAccess(t *testing.T) {
	service, _, _, pair := pairedService(t)
	if err := service.RevokeClient(context.Background(), pair.Client.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), pair.AccessToken); !errors.Is(err, ErrRevoked) {
		t.Fatalf("authentication after revoke = %v", err)
	}
}

func TestPublicAPIRequiresBrowserHeaderAndSetsProtectedRefreshCookie(t *testing.T) {
	store := newMemoryStore()
	service := NewService(store, Config{})
	grant, err := service.CreatePairingGrant(context.Background(), "Browser", nil)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewPublicAPI(service, HTTPConfig{}).Register(mux)
	body := `{"code":"` + grant.Code + `"}`

	missingHeader := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/pairing-grants:exchange", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(missingHeader, request)
	if missingHeader.Code != http.StatusForbidden {
		t.Fatalf("missing browser header status = %d", missingHeader.Code)
	}

	paired := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/v1/auth/pairing-grants:exchange", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(CSRFHeader, "1")
	mux.ServeHTTP(paired, request)
	if paired.Code != http.StatusOK {
		t.Fatalf("pairing status = %d body=%s", paired.Code, paired.Body.String())
	}
	cookie := paired.Header().Get("Set-Cookie")
	for _, attribute := range []string{"codexremote_refresh=", "Path=/v1/auth", "HttpOnly", "SameSite=Strict"} {
		if !strings.Contains(cookie, attribute) {
			t.Errorf("refresh cookie %q is missing %q", cookie, attribute)
		}
	}

	control := httptest.NewRecorder()
	mux.ServeHTTP(control, httptest.NewRequest(http.MethodPost, "/v1/auth-control/pairing-grants", nil))
	if control.Code != http.StatusNotFound {
		t.Fatalf("Auth Control leaked into public API: %d", control.Code)
	}
}

package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const CSRFHeader = "X-CodexRemote-Request"

type HTTPConfig struct {
	CookieName   string
	CookieSecure bool
}

type PublicAPI struct {
	service *Service
	config  HTTPConfig
}

func NewPublicAPI(service *Service, config HTTPConfig) *PublicAPI {
	if strings.TrimSpace(config.CookieName) == "" {
		config.CookieName = "codexremote_refresh"
	}
	return &PublicAPI{service: service, config: config}
}

func (a *PublicAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/pairing-grants:exchange", a.exchange)
	mux.HandleFunc("POST /v1/auth/tokens:refresh", a.refresh)
	mux.HandleFunc("POST /v1/auth/sessions/current:revoke", a.revoke)
	mux.HandleFunc("GET /v1/auth/me", a.me)
}

func (a *PublicAPI) exchange(w http.ResponseWriter, r *http.Request) {
	if !checkBrowserRequest(w, r) {
		return
	}
	var request struct {
		Code string `json:"code"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	pair, err := a.service.ExchangePairingGrant(r.Context(), request.Code)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	a.setRefreshCookie(w, pair.RefreshToken, pair.RefreshExpires)
	writeData(w, http.StatusOK, pair)
}

func (a *PublicAPI) refresh(w http.ResponseWriter, r *http.Request) {
	if !checkBrowserRequest(w, r) {
		return
	}
	cookie, err := r.Cookie(a.config.CookieName)
	if err != nil {
		writeAuthError(w, http.StatusUnauthorized, "AUTH_REFRESH_REQUIRED")
		return
	}
	pair, err := a.service.Refresh(r.Context(), cookie.Value)
	if err != nil {
		a.clearRefreshCookie(w)
		writeServiceError(w, err)
		return
	}
	a.setRefreshCookie(w, pair.RefreshToken, pair.RefreshExpires)
	writeData(w, http.StatusOK, pair)
}

func (a *PublicAPI) revoke(w http.ResponseWriter, r *http.Request) {
	if !checkBrowserRequest(w, r) {
		return
	}
	cookie, err := r.Cookie(a.config.CookieName)
	if err == nil {
		err = a.service.RevokeSession(r.Context(), cookie.Value, "client_logout")
		if err != nil && !errors.Is(err, ErrInvalidCredential) && !errors.Is(err, ErrRevoked) {
			writeServiceError(w, err)
			return
		}
	}
	a.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *PublicAPI) me(w http.ResponseWriter, r *http.Request) {
	principal, err := a.service.Authenticate(r.Context(), bearerToken(r.Header.Get("Authorization")))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, principal)
}

func (a *PublicAPI) setRefreshCookie(w http.ResponseWriter, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: a.config.CookieName, Value: value, Path: "/v1/auth", Expires: expires,
		MaxAge: int(time.Until(expires).Seconds()), HttpOnly: true, Secure: a.config.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (a *PublicAPI) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: a.config.CookieName, Value: "", Path: "/v1/auth", Expires: time.Unix(1, 0),
		MaxAge: -1, HttpOnly: true, Secure: a.config.CookieSecure, SameSite: http.SameSiteStrictMode,
	})
}

type ControlAPI struct {
	service *Service
}

func NewControlAPI(service *Service) *ControlAPI { return &ControlAPI{service: service} }

func (a *ControlAPI) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/auth-control/pairing-grants", a.createGrant)
	mux.HandleFunc("GET /v1/auth-control/clients", a.clients)
	mux.HandleFunc("POST /v1/auth-control/clients/{client_id}/revoke", a.revokeClient)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func (a *ControlAPI) createGrant(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	grant, err := a.service.CreatePairingGrant(r.Context(), request.Name, request.Scopes)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusCreated, grant)
}

func (a *ControlAPI) clients(w http.ResponseWriter, r *http.Request) {
	clients, err := a.service.ListClients(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"items": clients})
}

func (a *ControlAPI) revokeClient(w http.ResponseWriter, r *http.Request) {
	if err := a.service.RevokeClient(r.Context(), r.PathValue("client_id"), "control_revoke"); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func checkBrowserRequest(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get(CSRFHeader) != "1" {
		writeAuthError(w, http.StatusForbidden, "AUTH_BROWSER_HEADER_REQUIRED")
		return false
	}
	return true
}

func decodeRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	if contentType := r.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		writeAuthError(w, http.StatusUnsupportedMediaType, "JSON_CONTENT_TYPE_REQUIRED")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAuthError(w, http.StatusBadRequest, "INVALID_JSON")
		return false
	}
	return true
}

func writeData(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": value, "meta": map[string]any{"schema_version": 1}})
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrExpiredCredential):
		writeAuthError(w, http.StatusUnauthorized, "AUTH_CREDENTIAL_EXPIRED")
	case errors.Is(err, ErrConsumedGrant):
		writeAuthError(w, http.StatusConflict, "AUTH_PAIRING_ALREADY_USED")
	case errors.Is(err, ErrRefreshReplay):
		writeAuthError(w, http.StatusUnauthorized, "AUTH_REFRESH_REPLAY")
	case errors.Is(err, ErrInvalidCredential):
		writeAuthError(w, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIAL")
	case errors.Is(err, ErrRevoked):
		writeAuthError(w, http.StatusUnauthorized, "AUTH_SESSION_REVOKED")
	case errors.Is(err, ErrNotFound):
		writeAuthError(w, http.StatusNotFound, "AUTH_CLIENT_NOT_FOUND")
	default:
		writeAuthError(w, http.StatusInternalServerError, "AUTH_INTERNAL_ERROR")
	}
}

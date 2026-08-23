package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type principalContextKey struct{}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

func (s *Service) Middleware(next http.Handler, requiredScope func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r.Header.Get("Authorization"))
		principal, err := s.Authenticate(r.Context(), token)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="codex-remote"`)
			writeAuthError(w, http.StatusUnauthorized, credentialErrorCode(err))
			return
		}
		required := requiredScope(r)
		if !principal.HasScope(required) {
			writeAuthError(w, http.StatusForbidden, "AUTH_SCOPE_REQUIRED")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal)))
	})
}

func bearerToken(value string) string {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func credentialErrorCode(err error) string {
	switch err {
	case ErrExpiredCredential:
		return "AUTH_ACCESS_EXPIRED"
	case ErrRevoked:
		return "AUTH_SESSION_REVOKED"
	default:
		return "AUTH_INVALID_CREDENTIAL"
	}
}

func writeAuthError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": map[string]any{"code": code, "message": code, "retryable": false}, "meta": map[string]any{"schema_version": 1}})
}

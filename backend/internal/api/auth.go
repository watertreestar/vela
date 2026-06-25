package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"packaging-platform/backend/internal/domain"
)

const (
	roleAdmin    = "admin"
	roleOperator = "operator"
	roleViewer   = "viewer"
	roleRunner   = "runner"
)

type AuthConfig struct {
	Enabled          bool
	AdminToken       string
	OperatorToken    string
	ViewerToken      string
	RunnerToken      string
	OIDCIssuer       string
	OIDCAudience     string
	OIDCJWKSURL      string
	OIDCTokenURL     string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRoleClaim    string
	OIDCSubClaim     string
	OIDCAdminRole    string
	OIDCOperatorRole string
	OIDCViewerRole   string
	SessionHMACKey   string
	SessionTTL       time.Duration
	SessionStore     bool
}

type authPrincipal struct {
	ID   string
	Role string
}

type authPrincipalContextKey struct{}

type logoutResponse struct {
	Status    string `json:"status"`
	SessionID string `json:"sessionId,omitempty"`
	Revoked   bool   `json:"revoked"`
}

func (s Server) withAuth(handler http.HandlerFunc, roles ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := s.requireAuth(w, r, roles...)
		if !ok {
			return
		}
		handler(w, r.WithContext(context.WithValue(r.Context(), authPrincipalContextKey{}, principal)))
	}
}

func (s Server) requireAuth(w http.ResponseWriter, r *http.Request, roles ...string) (authPrincipal, bool) {
	principal, authenticated := s.authenticate(r)
	if !authenticated {
		writeAPIError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "", nil)
		return authPrincipal{}, false
	}
	if !principalCan(principal, roles...) {
		writeAPIError(w, http.StatusForbidden, "AUTH_FORBIDDEN", "", nil)
		return authPrincipal{}, false
	}
	return principal, true
}

func (s Server) authenticate(r *http.Request) (authPrincipal, bool) {
	if !s.auth.Enabled {
		return authPrincipal{ID: "system", Role: roleAdmin}, true
	}
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		return authPrincipal{}, false
	}
	switch token {
	case s.auth.AdminToken:
		if s.auth.AdminToken != "" {
			return authPrincipal{ID: "admin", Role: roleAdmin}, true
		}
	case s.auth.OperatorToken:
		if s.auth.OperatorToken != "" {
			return authPrincipal{ID: "operator", Role: roleOperator}, true
		}
	case s.auth.ViewerToken:
		if s.auth.ViewerToken != "" {
			return authPrincipal{ID: "viewer", Role: roleViewer}, true
		}
	case s.auth.RunnerToken:
		if s.auth.RunnerToken != "" {
			return authPrincipal{ID: "runner", Role: roleRunner}, true
		}
	}
	if principal, ok := s.authenticateOIDC(r, token); ok {
		return principal, true
	}
	if principal, ok := s.authenticateSessionToken(token, time.Now().UTC()); ok {
		return principal, true
	}
	return authPrincipal{}, false
}

func (s Server) logout(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r.Header.Get("Authorization"))
	sessionID, revoked := "", false
	if strings.HasPrefix(strings.TrimSpace(token), "pps_") && strings.TrimSpace(s.auth.SessionHMACKey) != "" {
		if claims, err := verifySessionToken(token, s.auth.SessionHMACKey, time.Now().UTC()); err == nil {
			sessionID = claims.SessionID
			if s.auth.SessionStore && sessionID != "" && s.store != nil {
				if _, err := s.store.RevokeAuthSession(sessionID, time.Now().UTC()); err == nil {
					revoked = true
				}
			}
		}
	}
	principal := authPrincipalFromRequest(r)
	s.recordAudit(r, domain.AuditEvent{
		Action:       "auth.logout",
		ActorID:      principal.ID,
		ActorRole:    principal.Role,
		ResourceType: "auth_session",
		ResourceID:   firstNonEmpty(sessionID, principal.ID),
		Status:       "success",
		Summary:      "auth session logout requested",
		Metadata: map[string]string{
			"revoked": strconv.FormatBool(revoked),
		},
	})
	writeJSON(w, http.StatusOK, logoutResponse{Status: "ok", SessionID: sessionID, Revoked: revoked})
}

func bearerToken(header string) string {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(fields[1])
}

func principalCan(principal authPrincipal, roles ...string) bool {
	if len(roles) == 0 {
		return true
	}
	for _, role := range roles {
		if principal.Role == role {
			return true
		}
		if principal.Role == roleAdmin {
			return true
		}
		if principal.Role == roleOperator && role == roleViewer {
			return true
		}
	}
	return false
}

func authActor(r *http.Request) string {
	principal, ok := r.Context().Value(authPrincipalContextKey{}).(authPrincipal)
	if !ok || principal.ID == "" {
		return "system"
	}
	return principal.ID
}

func authPrincipalFromRequest(r *http.Request) authPrincipal {
	principal, ok := r.Context().Value(authPrincipalContextKey{}).(authPrincipal)
	if !ok || principal.ID == "" {
		return authPrincipal{ID: "system", Role: roleAdmin}
	}
	return principal
}

type sessionTokenClaims struct {
	Subject   string `json:"sub"`
	Role      string `json:"role"`
	SessionID string `json:"sid,omitempty"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

func (s Server) issueSessionToken(principal authPrincipal, now time.Time) (string, string, time.Time, error) {
	ttl := s.auth.SessionTTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	expiresAt := now.Add(ttl).UTC()
	claims := sessionTokenClaims{
		Subject:   principal.ID,
		Role:      principal.Role,
		IssuedAt:  now.Unix(),
		ExpiresAt: expiresAt.Unix(),
	}
	if s.auth.SessionStore {
		if s.store == nil {
			return "", "", time.Time{}, errors.New("session store is not configured")
		}
		claims.SessionID = "ses_" + randomID()
		if err := s.store.SaveAuthSession(domain.AuthSession{
			ID:        claims.SessionID,
			Subject:   principal.ID,
			Role:      principal.Role,
			IssuedAt:  now.UTC(),
			ExpiresAt: expiresAt,
		}); err != nil {
			return "", "", time.Time{}, err
		}
	}
	token, err := signSessionToken(claims, s.auth.SessionHMACKey)
	return token, claims.SessionID, expiresAt, err
}

func (s Server) authenticateSessionToken(token string, now time.Time) (authPrincipal, bool) {
	if strings.TrimSpace(s.auth.SessionHMACKey) == "" {
		return authPrincipal{}, false
	}
	claims, err := verifySessionToken(token, s.auth.SessionHMACKey, now)
	if err != nil {
		return authPrincipal{}, false
	}
	if s.auth.SessionStore {
		if claims.SessionID == "" || s.store == nil {
			return authPrincipal{}, false
		}
		session, err := s.store.ReadAuthSession(claims.SessionID)
		if err != nil ||
			session.Subject != claims.Subject ||
			session.Role != claims.Role ||
			session.RevokedAt != nil ||
			!session.ExpiresAt.After(now) {
			return authPrincipal{}, false
		}
	}
	return authPrincipal{ID: claims.Subject, Role: claims.Role}, true
}

func signSessionToken(claims sessionTokenClaims, key string) (string, error) {
	header := map[string]string{"alg": "HS256", "typ": "PPS"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature := hmacSHA256([]byte(signingInput), []byte(key))
	return "pps_" + signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func verifySessionToken(token string, key string, now time.Time) (sessionTokenClaims, error) {
	token = strings.TrimSpace(strings.TrimPrefix(token, "pps_"))
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return sessionTokenClaims{}, errInvalidSessionToken
	}
	signingInput := parts[0] + "." + parts[1]
	expected := hmacSHA256([]byte(signingInput), []byte(key))
	actual, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(actual, expected) {
		return sessionTokenClaims{}, errInvalidSessionToken
	}
	var claims sessionTokenClaims
	if err := decodeJWTJSON(parts[1], &claims); err != nil {
		return sessionTokenClaims{}, err
	}
	if claims.Subject == "" || claims.Role == "" || claims.ExpiresAt <= now.Unix() {
		return sessionTokenClaims{}, errInvalidSessionToken
	}
	return claims, nil
}

var errInvalidSessionToken = &sessionTokenError{}

type sessionTokenError struct{}

func (e *sessionTokenError) Error() string {
	return "invalid session token"
}

func hmacSHA256(payload []byte, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

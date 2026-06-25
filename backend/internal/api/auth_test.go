package api

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthenticateOIDCJWT(t *testing.T) {
	key := newTestRSAKey(t)
	jwks := newTestJWKSServer(t, key, "oidc-key")
	defer jwks.Close()
	auth := testOIDCAuthConfig(jwks.URL)
	token := signTestJWT(t, key, "oidc-key", map[string]any{
		"iss":    auth.OIDCIssuer,
		"aud":    []string{auth.OIDCAudience, "other-service"},
		"sub":    "alice",
		"groups": []string{"packaging-operator"},
		"exp":    time.Now().Add(time.Hour).Unix(),
		"nbf":    time.Now().Add(-time.Minute).Unix(),
	})

	principal, ok := Server{auth: auth}.authenticate(httptest.NewRequest(http.MethodGet, "/api/audit-events", nil).WithContext(t.Context()))
	if ok || principal.ID != "" {
		t.Fatalf("expected request without token to be unauthenticated, got %#v", principal)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/audit-events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	principal, ok = Server{auth: auth}.authenticate(request)
	if !ok {
		t.Fatalf("expected OIDC token to authenticate")
	}
	if principal.ID != "oidc:alice" || principal.Role != roleOperator {
		t.Fatalf("unexpected OIDC principal: %#v", principal)
	}
}

func TestOIDCJWTRejectsInvalidSignature(t *testing.T) {
	jwksKey := newTestRSAKey(t)
	signingKey := newTestRSAKey(t)
	jwks := newTestJWKSServer(t, jwksKey, "oidc-key")
	defer jwks.Close()
	auth := testOIDCAuthConfig(jwks.URL)
	token := signTestJWT(t, signingKey, "oidc-key", map[string]any{
		"iss":    auth.OIDCIssuer,
		"aud":    auth.OIDCAudience,
		"sub":    "mallory",
		"groups": []string{"packaging-admin"},
		"exp":    time.Now().Add(time.Hour).Unix(),
	})

	request := httptest.NewRequest(http.MethodGet, "/api/audit-events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	principal, ok := Server{auth: auth}.authenticate(request)
	if ok || principal.ID != "" {
		t.Fatalf("expected bad signature to be rejected, got %#v", principal)
	}
}

func TestOIDCJWTRespectsRouteRoles(t *testing.T) {
	key := newTestRSAKey(t)
	jwks := newTestJWKSServer(t, key, "oidc-key")
	defer jwks.Close()
	auth := testOIDCAuthConfig(jwks.URL)
	viewerToken := signTestJWT(t, key, "oidc-key", map[string]any{
		"iss":    auth.OIDCIssuer,
		"aud":    auth.OIDCAudience,
		"sub":    "reader",
		"groups": []string{"packaging-viewer"},
		"exp":    time.Now().Add(time.Hour).Unix(),
	})
	operatorToken := signTestJWT(t, key, "oidc-key", map[string]any{
		"iss":    auth.OIDCIssuer,
		"aud":    auth.OIDCAudience,
		"sub":    "operator",
		"groups": []string{"packaging-operator"},
		"exp":    time.Now().Add(time.Hour).Unix(),
	})
	handler := Server{auth: auth}.withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, roleOperator)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+viewerToken)
	handler(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected viewer token to be forbidden, got %d: %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+operatorToken)
	handler(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected operator token to pass, got %d: %s", response.Code, response.Body.String())
	}
}

func TestStaticRunnerTokenStillWorksWithOIDCEnabled(t *testing.T) {
	key := newTestRSAKey(t)
	jwks := newTestJWKSServer(t, key, "oidc-key")
	defer jwks.Close()
	auth := testOIDCAuthConfig(jwks.URL)
	auth.RunnerToken = "runner-token"
	request := httptest.NewRequest(http.MethodPost, "/api/runner/heartbeat", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer runner-token")

	principal, ok := Server{auth: auth}.authenticate(request)
	if !ok {
		t.Fatalf("expected static runner token to authenticate")
	}
	if principal.ID != "runner" || principal.Role != roleRunner {
		t.Fatalf("unexpected runner principal: %#v", principal)
	}
}

func TestOIDCTokenBrokerIssuesPlatformSessionToken(t *testing.T) {
	key := newTestRSAKey(t)
	auth := testOIDCAuthConfig("")
	auth.OIDCClientID = "packaging-admin"
	auth.OIDCClientSecret = "client-secret"
	auth.SessionHMACKey = "session-secret"
	auth.SessionTTL = time.Hour
	token := signTestJWT(t, key, "oidc-key", map[string]any{
		"iss":    auth.OIDCIssuer,
		"aud":    auth.OIDCAudience,
		"sub":    "alice",
		"groups": []string{"packaging-admin"},
		"exp":    time.Now().Add(time.Hour).Unix(),
	})
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jwks":
			writeTestJWKS(t, w, key, "oidc-key")
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("grant_type") != "authorization_code" ||
				r.FormValue("client_id") != "packaging-admin" ||
				r.FormValue("client_secret") != "client-secret" ||
				r.FormValue("code") != "code-1" ||
				r.FormValue("code_verifier") != "verifier-1" ||
				r.FormValue("redirect_uri") != "https://packaging.example.test/callback" {
				t.Fatalf("unexpected token form: %#v", r.Form)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"` + token + `","token_type":"Bearer"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer idp.Close()
	auth.OIDCJWKSURL = idp.URL + "/jwks"
	auth.OIDCTokenURL = idp.URL + "/token"
	server := Server{auth: auth}

	body := bytes.NewBufferString(`{"code":"code-1","codeVerifier":"verifier-1","redirectUri":"https://packaging.example.test/callback"}`)
	response := httptest.NewRecorder()
	server.oidcTokenBroker(response, httptest.NewRequest(http.MethodPost, "/api/auth/oidc/token", body))
	if response.Code != http.StatusOK {
		t.Fatalf("expected broker success, got %d: %s", response.Code, response.Body.String())
	}
	var payload oidcTokenBrokerResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Token == "" || payload.Role != roleAdmin || payload.Subject != "oidc:alice" {
		t.Fatalf("unexpected broker payload: %#v", payload)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/audit-events", nil)
	request.Header.Set("Authorization", "Bearer "+payload.Token)
	principal, ok := server.authenticate(request)
	if !ok || principal.ID != "oidc:alice" || principal.Role != roleAdmin {
		t.Fatalf("session token did not authenticate: %#v ok=%v", principal, ok)
	}
}

func TestOIDCTokenBrokerSessionStoreLogoutRevokesToken(t *testing.T) {
	key := newTestRSAKey(t)
	auth := testOIDCAuthConfig("")
	auth.OIDCClientID = "packaging-admin"
	auth.OIDCClientSecret = "client-secret"
	auth.SessionHMACKey = "session-secret"
	auth.SessionTTL = time.Hour
	auth.SessionStore = true
	token := signTestJWT(t, key, "oidc-key", map[string]any{
		"iss":    auth.OIDCIssuer,
		"aud":    auth.OIDCAudience,
		"sub":    "alice",
		"groups": []string{"packaging-admin"},
		"exp":    time.Now().Add(time.Hour).Unix(),
	})
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jwks":
			writeTestJWKS(t, w, key, "oidc-key")
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"` + token + `","token_type":"Bearer"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer idp.Close()
	auth.OIDCJWKSURL = idp.URL + "/jwks"
	auth.OIDCTokenURL = idp.URL + "/token"
	store, _ := newTestServerStore(t)
	server := NewServerWithAuth(store, nil, nil, auth).Routes()

	response := doRequest(t, server, http.MethodPost, "/api/auth/oidc/token", `{"code":"code-1","codeVerifier":"verifier-1","redirectUri":"https://packaging.example.test/callback"}`)
	assertStatus(t, response, http.StatusOK)
	var payload oidcTokenBrokerResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Token == "" || payload.SessionID == "" {
		t.Fatalf("expected broker payload with persisted session id, got %#v", payload)
	}
	session, err := store.ReadAuthSession(payload.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.Subject != "oidc:alice" || session.Role != roleAdmin || session.RevokedAt != nil {
		t.Fatalf("unexpected persisted auth session: %#v", session)
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/audit-events", "", map[string]string{
		"Authorization": "Bearer " + payload.Token,
	})
	assertStatus(t, response, http.StatusOK)

	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/auth/logout", "", map[string]string{
		"Authorization": "Bearer " + payload.Token,
	})
	assertStatus(t, response, http.StatusOK)
	var logout logoutResponse
	if err := json.Unmarshal(response.Body.Bytes(), &logout); err != nil {
		t.Fatal(err)
	}
	if !logout.Revoked || logout.SessionID != payload.SessionID {
		t.Fatalf("expected logout to revoke persisted session, got %#v", logout)
	}
	session, err = store.ReadAuthSession(payload.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.RevokedAt == nil {
		t.Fatalf("expected stored session to be revoked, got %#v", session)
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/audit-events", "", map[string]string{
		"Authorization": "Bearer " + payload.Token,
	})
	assertStatus(t, response, http.StatusUnauthorized)
}

func testOIDCAuthConfig(jwksURL string) AuthConfig {
	return AuthConfig{
		Enabled:          true,
		OIDCIssuer:       "https://issuer.example.test",
		OIDCAudience:     "packaging-platform",
		OIDCJWKSURL:      jwksURL,
		OIDCRoleClaim:    "groups",
		OIDCSubClaim:     "sub",
		OIDCAdminRole:    "packaging-admin",
		OIDCOperatorRole: "packaging-operator",
		OIDCViewerRole:   "packaging-viewer",
	}
}

func newTestRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func newTestJWKSServer(t *testing.T, key *rsa.PrivateKey, kid string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestJWKS(t, w, key, kid)
	}))
}

func writeTestJWKS(t *testing.T, w http.ResponseWriter, key *rsa.PrivateKey, kid string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": kid,
			"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func signTestJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

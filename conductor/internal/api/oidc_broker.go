package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"vela/conductor/internal/domain"
)

type oidcTokenBrokerRequest struct {
	Code         string `json:"code"`
	CodeVerifier string `json:"codeVerifier"`
	RedirectURI  string `json:"redirectUri"`
}

type oidcTokenBrokerResponse struct {
	Token     string    `json:"token"`
	TokenType string    `json:"tokenType"`
	Role      string    `json:"role"`
	Subject   string    `json:"subject"`
	SessionID string    `json:"sessionId,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type oidcTokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

func (s Server) oidcTokenBroker(w http.ResponseWriter, r *http.Request) {
	if !s.oidcBrokerEnabled() {
		writeAPIError(w, http.StatusNotFound, "AUTH_BROKER_NOT_CONFIGURED", "OIDC token broker is not configured", nil)
		return
	}
	var input oidcTokenBrokerRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeAPIError(w, http.StatusBadRequest, "VALIDATION_INVALID_REQUEST", "invalid OIDC token broker request", nil)
		return
	}
	input.Code = strings.TrimSpace(input.Code)
	input.CodeVerifier = strings.TrimSpace(input.CodeVerifier)
	input.RedirectURI = strings.TrimSpace(input.RedirectURI)
	if input.Code == "" || input.CodeVerifier == "" || input.RedirectURI == "" {
		writeAPIError(w, http.StatusBadRequest, "VALIDATION_REQUIRED_FIELD", "code, codeVerifier, and redirectUri are required", nil)
		return
	}
	token, err := exchangeOIDCCodeForToken(r, s.auth.oidcConfig(), input)
	if err != nil {
		writeAPIErrorWithCause(w, http.StatusBadGateway, "OIDC_TOKEN_EXCHANGE_FAILED", "OIDC token exchange failed", nil, err)
		return
	}
	upstreamToken := token.AccessToken
	if upstreamToken == "" {
		upstreamToken = token.IDToken
	}
	if upstreamToken == "" {
		writeAPIError(w, http.StatusBadGateway, "OIDC_TOKEN_EXCHANGE_FAILED", "OIDC token response did not contain an access token or id token", nil)
		return
	}
	principal, err := verifyOIDCJWT(r.Context(), upstreamToken, s.auth.oidcConfig(), time.Now().UTC())
	if err != nil {
		writeAPIErrorWithCause(w, http.StatusUnauthorized, "AUTH_FORBIDDEN", "OIDC token was rejected by platform policy", nil, err)
		return
	}
	sessionToken, sessionID, expiresAt, err := s.issueSessionToken(principal, time.Now().UTC())
	if err != nil {
		writeAPIErrorWithCause(w, http.StatusInternalServerError, "REQUEST_FAILED", "failed to issue platform session token", nil, err)
		return
	}
	s.recordAudit(r, domain.AuditEvent{
		Action:       "auth.session.issue",
		ActorID:      principal.ID,
		ActorRole:    principal.Role,
		ResourceType: "auth_session",
		ResourceID:   firstNonEmpty(sessionID, principal.ID),
		Status:       "success",
		Summary:      "platform session token issued",
		Metadata: map[string]string{
			"sessionStore": strconv.FormatBool(s.auth.SessionStore),
		},
	})
	writeJSON(w, http.StatusOK, oidcTokenBrokerResponse{
		Token:     sessionToken,
		TokenType: "Bearer",
		Role:      principal.Role,
		Subject:   principal.ID,
		SessionID: sessionID,
		ExpiresAt: expiresAt,
	})
}

func (s Server) oidcBrokerEnabled() bool {
	config := s.auth.oidcConfig()
	return s.auth.Enabled &&
		config.enabled() &&
		config.tokenURL != "" &&
		config.clientID != "" &&
		strings.TrimSpace(s.auth.SessionHMACKey) != ""
}

func exchangeOIDCCodeForToken(r *http.Request, config oidcConfig, input oidcTokenBrokerRequest) (oidcTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", config.clientID)
	form.Set("code", input.Code)
	form.Set("code_verifier", input.CodeVerifier)
	form.Set("redirect_uri", input.RedirectURI)
	if config.clientSecret != "" {
		form.Set("client_secret", config.clientSecret)
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, config.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oidcTokenResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return oidcTokenResponse{}, err
	}
	defer response.Body.Close()
	var payload oidcTokenResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return oidcTokenResponse{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if payload.Description != "" {
			return oidcTokenResponse{}, errors.New(payload.Description)
		}
		if payload.Error != "" {
			return oidcTokenResponse{}, errors.New(payload.Error)
		}
		return oidcTokenResponse{}, errors.New("OIDC token endpoint returned non-success")
	}
	return payload, nil
}

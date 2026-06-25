package api

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"
)

const oidcHTTPTimeout = 5 * time.Second

type oidcJWTHeader struct {
	Alg string `json:"alg"`
	KID string `json:"kid"`
}

type oidcJWKS struct {
	Keys []oidcJWK `json:"keys"`
}

type oidcJWK struct {
	KTY string `json:"kty"`
	KID string `json:"kid"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (s Server) authenticateOIDC(r *http.Request, token string) (authPrincipal, bool) {
	config := s.auth.oidcConfig()
	if !config.enabled() {
		return authPrincipal{}, false
	}
	principal, err := verifyOIDCJWT(r.Context(), token, config, time.Now().UTC())
	if err != nil {
		return authPrincipal{}, false
	}
	return principal, true
}

type oidcConfig struct {
	issuer       string
	audience     string
	jwksURL      string
	tokenURL     string
	clientID     string
	clientSecret string
	roleClaim    string
	subClaim     string
	adminRole    string
	operatorRole string
	viewerRole   string
}

func (c AuthConfig) oidcConfig() oidcConfig {
	return oidcConfig{
		issuer:       strings.TrimSpace(c.OIDCIssuer),
		audience:     strings.TrimSpace(c.OIDCAudience),
		jwksURL:      strings.TrimSpace(c.OIDCJWKSURL),
		tokenURL:     strings.TrimSpace(c.OIDCTokenURL),
		clientID:     strings.TrimSpace(c.OIDCClientID),
		clientSecret: strings.TrimSpace(c.OIDCClientSecret),
		roleClaim:    defaultString(c.OIDCRoleClaim, "roles"),
		subClaim:     defaultString(c.OIDCSubClaim, "sub"),
		adminRole:    defaultString(c.OIDCAdminRole, roleAdmin),
		operatorRole: defaultString(c.OIDCOperatorRole, roleOperator),
		viewerRole:   defaultString(c.OIDCViewerRole, roleViewer),
	}
}

func (c oidcConfig) enabled() bool {
	return c.issuer != "" && c.audience != "" && c.jwksURL != ""
}

func verifyOIDCJWT(ctx context.Context, token string, config oidcConfig, now time.Time) (authPrincipal, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return authPrincipal{}, errors.New("invalid jwt segment count")
	}
	var header oidcJWTHeader
	if err := decodeJWTJSON(parts[0], &header); err != nil {
		return authPrincipal{}, err
	}
	if header.Alg != "RS256" {
		return authPrincipal{}, errors.New("unsupported jwt algorithm")
	}
	key, err := fetchOIDCJWK(ctx, config.jwksURL, header.KID)
	if err != nil {
		return authPrincipal{}, err
	}
	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return authPrincipal{}, err
	}
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return authPrincipal{}, err
	}

	var claims map[string]any
	if err := decodeJWTJSON(parts[1], &claims); err != nil {
		return authPrincipal{}, err
	}
	if !claimEquals(claims["iss"], config.issuer) {
		return authPrincipal{}, errors.New("jwt issuer mismatch")
	}
	if !audienceContains(claims["aud"], config.audience) {
		return authPrincipal{}, errors.New("jwt audience mismatch")
	}
	if !timeClaimAfter(claims["exp"], now) {
		return authPrincipal{}, errors.New("jwt expired")
	}
	if nbf, ok := claims["nbf"]; ok && !timeClaimBeforeOrEqual(nbf, now) {
		return authPrincipal{}, errors.New("jwt not yet valid")
	}
	role, ok := mapOIDCRole(claims[config.roleClaim], config)
	if !ok {
		return authPrincipal{}, errors.New("jwt role not allowed")
	}
	subject, ok := stringClaim(claims[config.subClaim])
	if !ok || subject == "" {
		return authPrincipal{}, errors.New("jwt subject missing")
	}
	return authPrincipal{ID: "oidc:" + subject, Role: role}, nil
}

func fetchOIDCJWK(ctx context.Context, jwksURL string, kid string) (*rsa.PublicKey, error) {
	requestCtx, cancel := context.WithTimeout(ctx, oidcHTTPTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("jwks endpoint returned non-success")
	}
	var jwks oidcJWKS
	if err := json.NewDecoder(response.Body).Decode(&jwks); err != nil {
		return nil, err
	}
	for _, key := range jwks.Keys {
		if key.KTY != "RSA" {
			continue
		}
		if key.Use != "" && key.Use != "sig" {
			continue
		}
		if key.Alg != "" && key.Alg != "RS256" {
			continue
		}
		if kid != "" && key.KID != kid {
			continue
		}
		return rsaKeyFromJWK(key)
	}
	return nil, errors.New("jwks key not found")
}

func rsaKeyFromJWK(key oidcJWK) (*rsa.PublicKey, error) {
	modulusBytes, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		return nil, err
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		return nil, err
	}
	exponent := new(big.Int).SetBytes(exponentBytes).Int64()
	if exponent <= 0 {
		return nil, errors.New("invalid jwk exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(modulusBytes), E: int(exponent)}, nil
}

func decodeJWTJSON(segment string, target any) error {
	payload, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, target)
}

func audienceContains(value any, expected string) bool {
	if claimEquals(value, expected) {
		return true
	}
	values, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range values {
		if claimEquals(item, expected) {
			return true
		}
	}
	return false
}

func claimEquals(value any, expected string) bool {
	actual, ok := stringClaim(value)
	return ok && actual == expected
}

func stringClaim(value any) (string, bool) {
	actual, ok := value.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(actual), true
}

func timeClaimAfter(value any, now time.Time) bool {
	seconds, ok := numericClaim(value)
	return ok && time.Unix(seconds, 0).After(now)
}

func timeClaimBeforeOrEqual(value any, now time.Time) bool {
	seconds, ok := numericClaim(value)
	return ok && !time.Unix(seconds, 0).After(now)
}

func numericClaim(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func mapOIDCRole(value any, config oidcConfig) (string, bool) {
	for _, role := range roleClaimValues(value) {
		switch role {
		case config.adminRole:
			return roleAdmin, true
		case config.operatorRole:
			return roleOperator, true
		case config.viewerRole:
			return roleViewer, true
		}
	}
	return "", false
}

func roleClaimValues(value any) []string {
	switch typed := value.(type) {
	case string:
		fields := strings.FieldsFunc(typed, func(r rune) bool {
			return r == ' ' || r == ','
		})
		if len(fields) == 0 && strings.TrimSpace(typed) != "" {
			return []string{strings.TrimSpace(typed)}
		}
		return fields
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			if value, ok := stringClaim(item); ok && value != "" {
				values = append(values, value)
			}
		}
		return values
	default:
		return nil
	}
}

func defaultString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"vela/conductor/internal/domain"
)

const executionCapabilityType = "vela-runner-execution"
const executionCapabilityAudience = "vela-runner"

type executionCapabilityPayload struct {
	Type      string `json:"typ"`
	Audience  string `json:"aud"`
	BuildID   string `json:"buildId"`
	Attempt   int    `json:"attempt"`
	Target    string `json:"target"`
	RunnerID  string `json:"runnerId"`
	JTI       string `json:"jti"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

func issueExecutionCapability(key string, claims domain.ExecutionClaims) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("RUNNER_EXECUTION_HMAC_KEY is required")
	}
	payload, err := json.Marshal(executionCapabilityPayload{executionCapabilityType, executionCapabilityAudience, claims.BuildID, claims.Attempt, claims.Target, claims.RunnerID, claims.JTI, claims.IssuedAt.Unix(), claims.ExpiresAt.Unix()})
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func verifyExecutionCapability(key, token string, now time.Time) (domain.ExecutionClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return domain.ExecutionClaims{}, fmt.Errorf("invalid execution capability")
	}
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(parts[0]))
	actual, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(actual, mac.Sum(nil)) {
		return domain.ExecutionClaims{}, fmt.Errorf("invalid execution capability signature")
	}
	var payload executionCapabilityPayload
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(body, &payload) != nil {
		return domain.ExecutionClaims{}, fmt.Errorf("invalid execution capability payload")
	}
	if strings.TrimSpace(key) == "" || payload.Type != executionCapabilityType || payload.Audience != executionCapabilityAudience || strings.TrimSpace(payload.BuildID) == "" || payload.Attempt <= 0 || strings.TrimSpace(payload.Target) == "" || strings.TrimSpace(payload.RunnerID) == "" || strings.TrimSpace(payload.JTI) == "" || payload.IssuedAt <= 0 || payload.ExpiresAt <= payload.IssuedAt || payload.ExpiresAt <= now.Unix() || payload.IssuedAt > now.Unix() {
		return domain.ExecutionClaims{}, fmt.Errorf("invalid or expired execution capability")
	}
	return domain.ExecutionClaims{Type: payload.Type, Audience: payload.Audience, BuildID: payload.BuildID, Attempt: payload.Attempt, Target: payload.Target, RunnerID: payload.RunnerID, JTI: payload.JTI, IssuedAt: time.Unix(payload.IssuedAt, 0).UTC(), ExpiresAt: time.Unix(payload.ExpiresAt, 0).UTC()}, nil
}

func newExecutionJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

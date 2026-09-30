package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"vela/conductor/internal/domain"
)

func executionClaims(now time.Time) domain.ExecutionClaims {
	return domain.ExecutionClaims{
		BuildID: "build-1", Attempt: 1, Target: "linux", RunnerID: "nomad-build-1-a1", JTI: "jti-1",
		IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute),
	}
}

func TestExecutionCapabilityRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	token, err := issueExecutionCapability("key", executionClaims(now))
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifyExecutionCapability("key", token, now)
	if err != nil || claims.BuildID != "build-1" || claims.Attempt != 1 || claims.Target != "linux" {
		t.Fatalf("unexpected verified claims=%#v err=%v", claims, err)
	}
}

func TestExecutionCapabilityRejectsInvalidClaims(t *testing.T) {
	now := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		mutate func(*domain.ExecutionClaims)
	}{
		{"empty build", func(c *domain.ExecutionClaims) { c.BuildID = "" }},
		{"zero attempt", func(c *domain.ExecutionClaims) { c.Attempt = 0 }},
		{"negative attempt", func(c *domain.ExecutionClaims) { c.Attempt = -1 }},
		{"empty target", func(c *domain.ExecutionClaims) { c.Target = "" }},
		{"empty runner", func(c *domain.ExecutionClaims) { c.RunnerID = "" }},
		{"empty jti", func(c *domain.ExecutionClaims) { c.JTI = "" }},
		{"expired", func(c *domain.ExecutionClaims) {
			c.IssuedAt = now.Add(-2 * time.Minute)
			c.ExpiresAt = now.Add(-time.Minute)
		}},
		{"future iat", func(c *domain.ExecutionClaims) {
			c.IssuedAt = now.Add(time.Minute)
			c.ExpiresAt = now.Add(2 * time.Minute)
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			claims := executionClaims(now)
			test.mutate(&claims)
			token, err := issueExecutionCapability("key", claims)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := verifyExecutionCapability("key", token, now); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func signedExecutionPayload(t *testing.T, key string, payload executionCapabilityPayload) string {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestExecutionCapabilityRejectsTamperingKeyTypeAndAudience(t *testing.T) {
	now := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	token, err := issueExecutionCapability("key", executionClaims(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyExecutionCapability("wrong-key", token, now); err == nil {
		t.Fatal("expected wrong key rejection")
	}
	if _, err := verifyExecutionCapability("key", token+"x", now); err == nil {
		t.Fatal("expected tamper rejection")
	}
	for _, field := range []string{"type", "audience"} {
		t.Run(field, func(t *testing.T) {
			payload := executionCapabilityPayload{Type: executionCapabilityType, Audience: executionCapabilityAudience, BuildID: "build-1", Attempt: 1, Target: "linux", RunnerID: "runner", JTI: "jti", IssuedAt: now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(time.Minute).Unix()}
			if field == "type" {
				payload.Type = "wrong"
			} else {
				payload.Audience = "wrong"
			}
			if _, err := verifyExecutionCapability("key", signedExecutionPayload(t, "key", payload), now); err == nil {
				t.Fatal("expected semantic rejection")
			}
		})
	}
}

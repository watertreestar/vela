package api

import (
	"net/http"
	"testing"
	"time"

	"vela/conductor/internal/domain"
)

func TestAuthDisabledLegacyClaimAcceptsNoAuthorizationOrCapability(t *testing.T) {
	store, _ := newTestServerStore(t)
	now := time.Now().UTC()
	build := domain.Build{ID: "legacy-local", ProductID: "lsap", Target: "linux", Status: domain.BuildQueued, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}

	response := doRequest(t, NewServer(store, &fakeScheduler{}, nil).Routes(), http.MethodPost, "/api/runner/tasks/claim", `{"runnerId":"local-runner","target":"linux"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("expected auth-disabled legacy claim without authorization or capability, got %d: %s", response.Code, response.Body.String())
	}
	var claimed domain.Build
	decodeJSON(t, response, &claimed)
	if claimed.ID != build.ID || claimed.RunnerID != "local-runner" || claimed.Status != domain.BuildRunning {
		t.Fatalf("unexpected legacy claim result: %#v", claimed)
	}
}

func TestAuthEnabledExecutionAuthorizationFencesBuildAndClaims(t *testing.T) {
	store, _ := newTestServerStore(t)
	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	first := domain.Build{ID: "execution-a", ProductID: "lsap", Target: "linux", Status: domain.BuildQueued, Attempt: 1, RunnerID: "nomad-execution-a-a1", ExecutionJTI: "jti-a", ExecutionExpiresAt: &expires, CreatedAt: now, UpdatedAt: now}
	second := first
	second.ID, second.RunnerID, second.ExecutionJTI = "execution-b", "nomad-execution-b-a1", "jti-b"
	for _, build := range []domain.Build{first, second} {
		if err := store.SaveBuild(build); err != nil {
			t.Fatal(err)
		}
	}
	server := NewServerWithAuth(store, &fakeScheduler{}, nil, testAuthConfig()).Routes()
	issue := func(build domain.Build) string {
		token, err := issueExecutionCapability("test-runner-execution-key", domain.ExecutionClaims{BuildID: build.ID, Attempt: build.Attempt, Target: build.Target, RunnerID: build.RunnerID, JTI: build.ExecutionJTI, IssuedAt: now.Add(-time.Minute), ExpiresAt: expires})
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	headers := bearer("runner-token")
	headers["X-Runner-Execution"] = issue(first)
	response := doRequestWithHeaders(t, server, http.MethodPost, "/api/runner/tasks/execution-b/claim", `{}`, headers)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected cross-build capability rejection, got %d: %s", response.Code, response.Body.String())
	}
	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/runner/tasks/execution-a/claim", `{"runnerId":"untrusted","target":"windows"}`, headers)
	if response.Code != http.StatusOK {
		t.Fatalf("expected exact capability claim, got %d: %s", response.Code, response.Body.String())
	}
	var claimed domain.Build
	decodeJSON(t, response, &claimed)
	if claimed.RunnerID != first.RunnerID || claimed.Status != domain.BuildRunning {
		t.Fatalf("claim trusted body instead of capability: %#v", claimed)
	}
	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/builds/execution-a/logs", `{"source":"forged","message":"ok"}`, headers)
	if response.Code != http.StatusOK {
		t.Fatalf("expected authorized log, got %d: %s", response.Code, response.Body.String())
	}
	updated, err := store.ReadBuild(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Logs[len(updated.Logs)-1].Source != first.RunnerID {
		t.Fatalf("log source trusted request body: %#v", updated.Logs)
	}
	updated.ExecutionJTI = "fenced"
	if err := store.SaveBuild(updated); err != nil {
		t.Fatal(err)
	}
	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/runner/tasks/execution-a/heartbeat", `{}`, headers)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected JTI fence rejection, got %d: %s", response.Code, response.Body.String())
	}
}

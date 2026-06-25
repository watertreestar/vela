package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"packaging-platform/backend/internal/artifact"
	"packaging-platform/backend/internal/config"
	"packaging-platform/backend/internal/domain"
	"packaging-platform/backend/internal/nomad"
)

type fakeScheduler struct {
	submitted []string
	stopped   []string
	submitErr error
	stopErr   error
}

func (s *fakeScheduler) Submit(build domain.Build, _ domain.Product) (string, string, error) {
	s.submitted = append(s.submitted, build.ID)
	if s.submitErr != nil {
		return "", "scheduler failed", s.submitErr
	}
	return "job-" + build.ID, "submitted " + build.ID, nil
}

func (s *fakeScheduler) Inspect(_ string) (nomad.JobStatus, error) {
	return nomad.JobStatus{State: nomad.JobStateRunning}, nil
}

func (s *fakeScheduler) Stop(jobID string) error {
	s.stopped = append(s.stopped, jobID)
	if s.stopErr != nil {
		return s.stopErr
	}
	return nil
}

func TestAPIHealthProductsUploadsAndRunnerWorkflow(t *testing.T) {
	store, productsDir := newTestServerStore(t)
	dataDir := t.TempDir()
	artifacts, err := artifact.NewStore(config.Config{
		DataDir:       dataDir,
		APIBaseURL:    "http://api.test",
		ArtifactStore: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduler := &fakeScheduler{}
	server := NewServer(store, scheduler, artifacts).Routes()

	assertStatus(t, doRequest(t, server, http.MethodGet, "/api/health", ""), http.StatusOK)

	response := doRequest(t, server, http.MethodGet, "/api/products", "")
	assertStatus(t, response, http.StatusOK)
	var products []domain.Product
	decodeJSON(t, response, &products)
	if !hasProduct(products, "lsap") {
		t.Fatalf("unexpected products: %#v", products)
	}

	uploadResponse := uploadRequestWithFields(t, server, "license.lic", "license-data", map[string]string{
		"downloadPolicy":     "restricted",
		"retentionDays":      "7",
		"metadata.productId": "lsap",
		"metadata.purpose":   "license",
	})
	assertStatus(t, uploadResponse, http.StatusCreated)
	var upload domain.UploadRecord
	decodeJSON(t, uploadResponse, &upload)
	if upload.ID == "" || upload.SHA256 == "" {
		t.Fatalf("unexpected upload: %#v", upload)
	}
	if upload.DownloadPolicy != "restricted" || upload.RetentionDays != 7 || upload.RetentionStatus != "active" || upload.Metadata["purpose"] != "license" {
		t.Fatalf("upload metadata was not persisted: %#v", upload)
	}

	buildBody := `{
	  "productId":"lsap",
	  "target":"linux",
	  "profile":"qa",
	  "version":"1.0.0",
	  "releaseChannel":"preview",
	  "componentOverrides":{"ui":{"kind":"manual-upload","uploadId":"` + upload.ID + `"}},
	  "configOverrides":{"ports":{"web":49000}}
	}`
	response = doRequest(t, server, http.MethodPost, "/api/builds", buildBody)
	assertStatus(t, response, http.StatusAccepted)
	var build domain.Build
	decodeJSON(t, response, &build)
	if build.Status != domain.BuildPendingApproval || build.BuildLock.Target != "linux" {
		t.Fatalf("unexpected created build: %#v", build)
	}
	if build.Components[0].URI != upload.URI {
		t.Fatalf("component upload override not resolved: %#v", build.Components)
	}

	response = doRequest(t, server, http.MethodPost, "/api/builds/"+build.ID+"/approve", "")
	assertStatus(t, response, http.StatusOK)
	decodeJSON(t, response, &build)
	if build.Status != domain.BuildQueued || build.NomadJobID == "" || len(scheduler.submitted) != 1 {
		t.Fatalf("approval did not queue build: %#v scheduler=%#v", build, scheduler)
	}

	response = doRequest(t, server, http.MethodPost, "/api/runner/tasks/claim", `{"runnerId":"runner-1","target":"windows"}`)
	assertStatus(t, response, http.StatusNoContent)

	response = doRequest(t, server, http.MethodPost, "/api/runner/tasks/claim", `{"runnerId":"runner-1","target":"linux"}`)
	assertStatus(t, response, http.StatusOK)
	decodeJSON(t, response, &build)
	if build.Status != domain.BuildRunning || build.RunnerID != "runner-1" {
		t.Fatalf("claim did not mark build running: %#v", build)
	}

	response = doRequest(t, server, http.MethodPost, "/api/builds/"+build.ID+"/logs", `{"source":"runner-1","message":"downloaded components"}`)
	assertStatus(t, response, http.StatusOK)

	response = doRequest(t, server, http.MethodPost, "/api/runner/heartbeat", `{"runnerId":"runner-1","buildId":"`+build.ID+`","message":"active"}`)
	assertStatus(t, response, http.StatusOK)

	response = doRequest(t, server, http.MethodGet, "/api/runners", "")
	assertStatus(t, response, http.StatusOK)
	var runners []domain.RunnerStatus
	decodeJSON(t, response, &runners)
	if len(runners) != 1 || runners[0].RunnerID != "runner-1" || runners[0].Status != "busy" || runners[0].LastHeartbeatAt == nil {
		t.Fatalf("unexpected runner observability: %#v", runners)
	}

	response = doRequest(t, server, http.MethodPost, "/api/runner/tasks/"+build.ID+"/complete", `{"status":"succeeded","artifactUri":"local-upload://artifact/package.tar.gz","downloadUrl":"http://api.test/api/uploads/`+upload.ID+`/download"}`)
	assertStatus(t, response, http.StatusOK)
	decodeJSON(t, response, &build)
	if build.Status != domain.BuildSucceeded || build.ArtifactURI == "" {
		t.Fatalf("complete did not mark build succeeded: %#v", build)
	}

	response = doRequest(t, server, http.MethodGet, "/api/builds/"+build.ID+"/download", "")
	assertStatus(t, response, http.StatusFound)
	if location := response.Header().Get("Location"); !strings.Contains(location, "/api/uploads/") {
		t.Fatalf("unexpected download redirect: %s", location)
	}

	response = doRequest(t, server, http.MethodGet, "/api/builds/"+build.ID+"/logs", "")
	assertStatus(t, response, http.StatusOK)
	var logs []domain.BuildLog
	decodeJSON(t, response, &logs)
	if len(logs) < 4 {
		t.Fatalf("expected workflow logs, got %#v", logs)
	}
	if logs[0].Sequence == 0 {
		t.Fatalf("expected sequenced logs, got %#v", logs)
	}

	response = doRequest(t, server, http.MethodGet, "/api/builds/"+build.ID+"/logs/stream", "")
	assertStatus(t, response, http.StatusOK)
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("expected event stream content type, got %s", contentType)
	}
	if !strings.Contains(response.Body.String(), "event: log") || !strings.Contains(response.Body.String(), "downloaded components") {
		t.Fatalf("unexpected log stream body: %s", response.Body.String())
	}

	if _, err := os.Stat(filepath.Join(productsDir, "lsap", "product.json")); err != nil {
		t.Fatalf("test product manifest missing: %v", err)
	}
}

func TestAPIRetryCancelAndUploadDownload(t *testing.T) {
	store, _ := newTestServerStore(t)
	dataDir := t.TempDir()
	artifacts, err := artifact.NewStore(config.Config{
		DataDir:       dataDir,
		APIBaseURL:    "http://api.test",
		ArtifactStore: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduler := &fakeScheduler{}
	server := NewServer(store, scheduler, artifacts).Routes()

	uploadResponse := uploadRequest(t, server, "package.txt", "package-data")
	assertStatus(t, uploadResponse, http.StatusCreated)
	var upload domain.UploadRecord
	decodeJSON(t, uploadResponse, &upload)

	response := doRequest(t, server, http.MethodGet, "/api/uploads/"+upload.ID+"/download", "")
	assertStatus(t, response, http.StatusOK)
	if strings.TrimSpace(response.Body.String()) != "package-data" {
		t.Fatalf("unexpected download body: %q", response.Body.String())
	}

	expiredAt := time.Now().UTC().Add(-time.Hour)
	upload.RetentionDays = 1
	upload.ExpiresAt = &expiredAt
	upload.RetentionStatus = "expired"
	if err := store.SaveUploadRecord(upload); err != nil {
		t.Fatal(err)
	}
	response = doRequest(t, server, http.MethodGet, "/api/uploads/"+upload.ID+"/download", "")
	assertStatus(t, response, http.StatusGone)

	now := time.Now().UTC()
	failed := domain.Build{
		ID:        "failed-build",
		ProductID: "lsap",
		Target:    "windows",
		Profile:   "qa",
		Version:   "1.0.0",
		Status:    domain.BuildFailed,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveBuild(failed); err != nil {
		t.Fatal(err)
	}

	response = doRequest(t, server, http.MethodPost, "/api/builds/failed-build/retry", "")
	assertStatus(t, response, http.StatusOK)
	var retried domain.Build
	decodeJSON(t, response, &retried)
	if retried.Status != domain.BuildQueued || retried.NomadJobID == "" {
		t.Fatalf("retry did not queue failed build: %#v", retried)
	}

	response = doRequest(t, server, http.MethodPost, "/api/builds/failed-build/cancel", "")
	assertStatus(t, response, http.StatusOK)
	var canceled domain.Build
	decodeJSON(t, response, &canceled)
	if canceled.Status != domain.BuildCanceled || len(scheduler.stopped) != 1 {
		t.Fatalf("cancel did not stop queued build: %#v scheduler=%#v", canceled, scheduler)
	}
}

func TestAPIRejectBuild(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()
	now := time.Now().UTC()
	pending := domain.Build{
		ID:        "pending-reject",
		ProductID: "lsap",
		Target:    "linux",
		Profile:   "qa",
		Version:   "1.0.0",
		Status:    domain.BuildPendingApproval,
		CreatedAt: now,
		UpdatedAt: now,
	}
	queued := domain.Build{
		ID:        "queued-reject",
		ProductID: "lsap",
		Target:    "linux",
		Profile:   "qa",
		Version:   "1.0.0",
		Status:    domain.BuildQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveBuild(pending); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveBuild(queued); err != nil {
		t.Fatal(err)
	}

	response := doRequest(t, server, http.MethodPost, "/api/builds/pending-reject/reject", "")
	assertStatus(t, response, http.StatusOK)
	var rejected domain.Build
	decodeJSON(t, response, &rejected)
	if rejected.Status != domain.BuildRejected {
		t.Fatalf("reject did not mark build rejected: %#v", rejected)
	}
	if len(rejected.Logs) == 0 || rejected.Logs[len(rejected.Logs)-1].Message != "build rejected" {
		t.Fatalf("reject did not append audit log: %#v", rejected.Logs)
	}

	response = doRequest(t, server, http.MethodPost, "/api/builds/queued-reject/reject", "")
	assertStatus(t, response, http.StatusConflict)
	apiErr := decodeAPIError(t, response)
	if apiErr.Error.Code != "BUILD_STATE_CONFLICT" {
		t.Fatalf("expected state conflict error, got %#v", apiErr)
	}
}

func TestAPISchedulerFailureBranches(t *testing.T) {
	store, _ := newTestServerStore(t)
	now := time.Now().UTC()
	pending := domain.Build{
		ID:        "approve-fails",
		ProductID: "lsap",
		Target:    "linux",
		Profile:   "qa",
		Version:   "1.0.0",
		Status:    domain.BuildPendingApproval,
		CreatedAt: now,
		UpdatedAt: now,
	}
	failed := domain.Build{
		ID:        "retry-fails",
		ProductID: "lsap",
		Target:    "linux",
		Profile:   "qa",
		Version:   "1.0.0",
		Status:    domain.BuildFailed,
		CreatedAt: now,
		UpdatedAt: now,
	}
	queued := domain.Build{
		ID:         "cancel-fails",
		ProductID:  "lsap",
		Target:     "linux",
		Profile:    "qa",
		Version:    "1.0.0",
		Status:     domain.BuildQueued,
		NomadJobID: "job-cancel-fails",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	for _, build := range []domain.Build{pending, failed, queued} {
		if err := store.SaveBuild(build); err != nil {
			t.Fatal(err)
		}
	}
	scheduler := &fakeScheduler{
		submitErr: errors.New("nomad submit unavailable"),
		stopErr:   errors.New("nomad stop unavailable"),
	}
	server := NewServer(store, scheduler, nil).Routes()

	response := doRequest(t, server, http.MethodPost, "/api/builds/approve-fails/approve", "")
	assertStatus(t, response, http.StatusBadGateway)
	apiErr := decodeAPIError(t, response)
	if apiErr.Error.Code != "SCHEDULER_SUBMIT_FAILED" || apiErr.Error.Status != http.StatusBadGateway || !apiErr.Error.Retryable {
		t.Fatalf("expected scheduler submit error, got %#v", apiErr)
	}
	approved, err := store.ReadBuild("approve-fails")
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != domain.BuildFailed || approved.Error != "nomad submit unavailable" {
		t.Fatalf("approval failure did not mark build failed: %#v", approved)
	}

	response = doRequest(t, server, http.MethodPost, "/api/builds/retry-fails/retry", "")
	assertStatus(t, response, http.StatusBadGateway)
	apiErr = decodeAPIError(t, response)
	if apiErr.Error.Code != "SCHEDULER_SUBMIT_FAILED" {
		t.Fatalf("expected scheduler submit error for retry failure, got %#v", apiErr)
	}

	response = doRequest(t, server, http.MethodPost, "/api/builds/cancel-fails/cancel", "")
	assertStatus(t, response, http.StatusBadGateway)
	apiErr = decodeAPIError(t, response)
	if apiErr.Error.Code != "SCHEDULER_STOP_FAILED" {
		t.Fatalf("expected scheduler stop error for cancel failure, got %#v", apiErr)
	}
	if len(scheduler.stopped) != 1 || scheduler.stopped[0] != "job-cancel-fails" {
		t.Fatalf("expected scheduler stop attempt, got %#v", scheduler.stopped)
	}
}

func TestAPIRunnerAndUploadErrorBranches(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()
	now := time.Now().UTC()
	running := domain.Build{
		ID:        "runner-fails",
		ProductID: "lsap",
		Target:    "linux",
		Profile:   "qa",
		Version:   "1.0.0",
		Status:    domain.BuildRunning,
		RunnerID:  "runner-1",
		CreatedAt: now,
		UpdatedAt: now,
	}
	succeeded := running
	succeeded.ID = "already-succeeded"
	succeeded.Status = domain.BuildSucceeded
	for _, build := range []domain.Build{running, succeeded} {
		if err := store.SaveBuild(build); err != nil {
			t.Fatal(err)
		}
	}

	response := doRequest(t, server, http.MethodPost, "/api/runner/tasks/claim", `{`)
	assertStatus(t, response, http.StatusBadRequest)

	response = doRequest(t, server, http.MethodPost, "/api/runner/tasks/claim", `{"target":"linux"}`)
	assertStatus(t, response, http.StatusBadRequest)

	response = doRequest(t, server, http.MethodPost, "/api/runner/heartbeat", `{"buildId":"runner-fails"}`)
	assertStatus(t, response, http.StatusBadRequest)

	response = doRequest(t, server, http.MethodPost, "/api/runner/heartbeat", `{"runnerId":"runner-1","buildId":"missing"}`)
	assertStatus(t, response, http.StatusNotFound)

	response = doRequest(t, server, http.MethodPost, "/api/runner/heartbeat", `{"runnerId":"runner-1"}`)
	assertStatus(t, response, http.StatusOK)

	response = doRequest(t, server, http.MethodPost, "/api/runner/tasks/runner-fails/complete", `{`)
	assertStatus(t, response, http.StatusBadRequest)

	response = doRequest(t, server, http.MethodPost, "/api/runner/tasks/already-succeeded/complete", `{"status":"succeeded"}`)
	assertStatus(t, response, http.StatusConflict)
	apiErr := decodeAPIError(t, response)
	if apiErr.Error.Code != "BUILD_STATE_CONFLICT" {
		t.Fatalf("expected complete state conflict, got %#v", apiErr)
	}

	response = doRequest(t, server, http.MethodPost, "/api/runner/tasks/runner-fails/complete", `{"status":"failed","error":"unit test failure"}`)
	assertStatus(t, response, http.StatusOK)
	var failed domain.Build
	decodeJSON(t, response, &failed)
	if failed.Status != domain.BuildFailed || failed.Error != "unit test failure" {
		t.Fatalf("expected failed completion, got %#v", failed)
	}

	response = doRequest(t, server, http.MethodPost, "/api/uploads", `{}`)
	assertStatus(t, response, http.StatusBadRequest)
}

func TestNormalizeLegacyUploadRecordDefaultsDownloadPolicy(t *testing.T) {
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	record := normalizeUploadRecord(domain.UploadRecord{
		ID:              "legacy-upload",
		FileName:        "legacy.tar.gz",
		RetentionDays:   0,
		RetentionStatus: "",
	}, now)

	if record.DownloadPolicy != "platform" {
		t.Fatalf("expected default platform policy, got %#v", record)
	}
	if record.RetentionStatus != "permanent" {
		t.Fatalf("expected legacy zero retention to remain permanent, got %#v", record)
	}
}

func uploadRequest(t *testing.T, handler http.Handler, filename string, content string) *httptest.ResponseRecorder {
	return uploadRequestWithFields(t, handler, filename, content, nil)
}

func uploadRequestWithFields(t *testing.T, handler http.Handler, filename string, content string, fields map[string]string) *httptest.ResponseRecorder {
	return uploadRequestWithFieldsAndHeaders(t, handler, filename, content, fields, nil)
}

func uploadRequestWithFieldsAndHeaders(t *testing.T, handler http.Handler, filename string, content string, fields map[string]string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/uploads", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertStatus(t *testing.T, response *httptest.ResponseRecorder, expected int) {
	t.Helper()
	if response.Code != expected {
		t.Fatalf("expected status %d, got %d: %s", expected, response.Code, response.Body.String())
	}
}

func decodeJSON(t *testing.T, response *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func hasProduct(products []domain.Product, id string) bool {
	for _, product := range products {
		if product.ID == id {
			return true
		}
	}
	return false
}

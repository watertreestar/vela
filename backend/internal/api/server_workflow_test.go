package api

import (
	"bytes"
	"encoding/json"
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
}

func (s *fakeScheduler) Submit(build domain.Build, _ domain.Product) (string, string, error) {
	s.submitted = append(s.submitted, build.ID)
	return "job-" + build.ID, "submitted " + build.ID, nil
}

func (s *fakeScheduler) Inspect(_ string) (nomad.JobStatus, error) {
	return nomad.JobStatus{State: nomad.JobStateRunning}, nil
}

func (s *fakeScheduler) Stop(jobID string) error {
	s.stopped = append(s.stopped, jobID)
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

	uploadResponse := uploadRequest(t, server, "license.lic", "license-data")
	assertStatus(t, uploadResponse, http.StatusCreated)
	var upload domain.UploadRecord
	decodeJSON(t, uploadResponse, &upload)
	if upload.ID == "" || upload.SHA256 == "" {
		t.Fatalf("unexpected upload: %#v", upload)
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

func uploadRequest(t *testing.T, handler http.Handler, filename string, content string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
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

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"packaging-platform/backend/internal/domain"
	storepkg "packaging-platform/backend/internal/store"
)

func TestProductOverlayRoutes(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()

	response := doRequest(t, server, http.MethodGet, "/api/products/lsap/overlays/profiles", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected list overlays status 200, got %d: %s", response.Code, response.Body.String())
	}
	var records []storepkg.OverlayRecord
	if err := json.NewDecoder(response.Body).Decode(&records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Name != "qa" {
		t.Fatalf("unexpected overlay list: %#v", records)
	}

	response = doRequest(
		t,
		server,
		http.MethodPut,
		"/api/products/lsap/overlays/customers/acme",
		`{"config":{"customer":{"name":"ACME"}}}`,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("expected write overlay status 200, got %d: %s", response.Code, response.Body.String())
	}
	var written storepkg.OverlayRecord
	if err := json.NewDecoder(response.Body).Decode(&written); err != nil {
		t.Fatal(err)
	}
	if written.Name != "acme" || written.Kind != "customers" {
		t.Fatalf("unexpected written overlay: %#v", written)
	}

	response = doRequest(t, server, http.MethodGet, "/api/products/lsap/overlays/customers/acme", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected read overlay status 200, got %d: %s", response.Code, response.Body.String())
	}
	var read storepkg.OverlayRecord
	if err := json.NewDecoder(response.Body).Decode(&read); err != nil {
		t.Fatal(err)
	}
	config := read.Data["config"].(map[string]any)
	customer := config["customer"].(map[string]any)
	if customer["name"] != "ACME" {
		t.Fatalf("unexpected read overlay data: %#v", read.Data)
	}
}

func TestProductOverlayRoutesRejectInvalidInput(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()

	response := doRequest(t, server, http.MethodGet, "/api/products/lsap/overlays/bad-kind", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid kind status 400, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequest(t, server, http.MethodPut, "/api/products/lsap/overlays/profiles/bad%20name", `{}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid name status 400, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequest(t, server, http.MethodPut, "/api/products/lsap/overlays/profiles/dev", `[]`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected non-object JSON status 400, got %d: %s", response.Code, response.Body.String())
	}
}

func TestManifestRoutesValidateDryRunAndRestore(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()

	response := doRequest(t, server, http.MethodPut, "/api/products/lsap/manifest", `{"id":"lsap","name":"Broken"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid manifest status 400, got %d: %s", response.Code, response.Body.String())
	}

	updated := `{
	  "id":"lsap",
	  "name":"LSAP Updated",
	  "targets":["linux","windows"],
	  "targetConfig":{
	    "linux":{"installDir":"/opt/lsap","packageName":"lsap-linux"},
	    "windows":{"installDir":"C:\\\\LSAP","packageName":"lsap-windows"}
	  },
	  "profiles":["qa","prod"],
	  "versions":["1.0.0"],
	  "components":[{"id":"ui","type":"archive","required":true,"source":{"kind":"fixed-url","url":"https://example.test/ui.zip"}}],
	  "workloads":[{"id":"ui","runtime":"docker","component":"ui"}],
	  "config":{"modules":{"map":true}}
	}`
	response = doRequest(t, server, http.MethodPut, "/api/products/lsap/manifest", updated)
	if response.Code != http.StatusOK {
		t.Fatalf("expected valid manifest status 200, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequest(t, server, http.MethodPost, "/api/products/lsap/manifest/dry-run", `{"target":"linux","profile":"qa","version":"1.0.0"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("expected dry run status 200, got %d: %s", response.Code, response.Body.String())
	}
	var dryRun domain.ManifestDryRunResult
	if err := json.NewDecoder(response.Body).Decode(&dryRun); err != nil {
		t.Fatal(err)
	}
	if !dryRun.Valid || dryRun.BuildLock.Target != "linux" || len(dryRun.Components) != 1 {
		t.Fatalf("unexpected dry run result: %#v", dryRun)
	}

	response = doRequest(t, server, http.MethodGet, "/api/products/lsap/manifest/revisions", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected revisions status 200, got %d: %s", response.Code, response.Body.String())
	}
	var revisions []domain.ManifestRevision
	if err := json.NewDecoder(response.Body).Decode(&revisions); err != nil {
		t.Fatal(err)
	}
	if len(revisions) == 0 || revisions[0].Manifest != nil {
		t.Fatalf("unexpected revisions: %#v", revisions)
	}

	response = doRequest(t, server, http.MethodPost, "/api/products/lsap/manifest/revisions/"+revisions[0].ID+"/restore", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected restore status 200, got %d: %s", response.Code, response.Body.String())
	}
}

func TestAPIUsesRichFixtureProductsOverlaysAndBuildData(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()

	response := doRequest(t, server, http.MethodGet, "/api/products", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected products status 200, got %d: %s", response.Code, response.Body.String())
	}
	var products []domain.Product
	if err := json.NewDecoder(response.Body).Decode(&products); err != nil {
		t.Fatal(err)
	}
	if len(products) != 2 {
		t.Fatalf("expected rich fixture to expose 2 products, got %#v", products)
	}

	response = doRequest(t, server, http.MethodGet, "/api/products/vision-edge/overlays/customers/city-bank", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected customer overlay status 200, got %d: %s", response.Code, response.Body.String())
	}
	var overlay storepkg.OverlayRecord
	if err := json.NewDecoder(response.Body).Decode(&overlay); err != nil {
		t.Fatal(err)
	}
	config := overlay.Data["config"].(map[string]any)
	customer := config["customer"].(map[string]any)
	if customer["name"] != "City Bank" {
		t.Fatalf("unexpected customer overlay: %#v", overlay)
	}

	dryRunBody := `{
	  "target":"windows",
	  "profile":"prod",
	  "customer":"city-bank",
	  "version":"2026.05.28",
	  "releaseChannel":"preview",
	  "configOverrides":{"ports":{"web":51000}},
	  "componentOverrides":{
	    "camera-model":{"kind":"fixed-url","url":"https://artifacts.example.test/camera-model-2026.05.28.zip"}
	  }
	}`
	response = doRequest(t, server, http.MethodPost, "/api/products/vision-edge/manifest/dry-run", dryRunBody)
	if response.Code != http.StatusOK {
		t.Fatalf("expected dry-run status 200, got %d: %s", response.Code, response.Body.String())
	}
	var dryRun domain.ManifestDryRunResult
	if err := json.NewDecoder(response.Body).Decode(&dryRun); err != nil {
		t.Fatal(err)
	}
	if !dryRun.Valid || dryRun.BuildLock.Target != "windows" || dryRun.BuildLock.Customer != "city-bank" {
		t.Fatalf("unexpected dry run lock: %#v", dryRun)
	}
	if len(dryRun.Components) != 3 {
		t.Fatalf("expected 3 resolved components, got %#v", dryRun.Components)
	}
	if !hasResolvedComponentURI(dryRun.Components, "camera-model", "https://artifacts.example.test/camera-model-2026.05.28.zip") {
		t.Fatalf("component override was not resolved: %#v", dryRun.Components)
	}

	now := time.Now().UTC()
	seedBuilds := []domain.Build{
		releaseReadyBuild("vision-release-ready", "vision-edge", "windows", "prod", "2026.05.28", now),
		{
			ID:             "vision-running",
			ProductID:      "vision-edge",
			Target:         "linux",
			Profile:        "qa",
			Version:        "latest",
			ReleaseChannel: "preview",
			Status:         domain.BuildRunning,
			RunnerID:       "linux-runner-01",
			CreatedAt:      now.Add(-2 * time.Hour),
			UpdatedAt:      now.Add(-90 * time.Minute),
		},
	}
	for _, build := range seedBuilds {
		if err := store.SaveBuild(build); err != nil {
			t.Fatal(err)
		}
	}

	response = doRequest(t, server, http.MethodGet, "/api/builds", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected builds status 200, got %d: %s", response.Code, response.Body.String())
	}
	var builds []domain.Build
	if err := json.NewDecoder(response.Body).Decode(&builds); err != nil {
		t.Fatal(err)
	}
	if len(builds) != 2 {
		t.Fatalf("expected 2 seeded builds, got %#v", builds)
	}
	if !hasBuildStatus(builds, "vision-release-ready", domain.BuildPreviewValidated) {
		t.Fatalf("expected release-ready build in list, got %#v", builds)
	}
	if !hasBuildStatus(builds, "vision-running", domain.BuildRunning) {
		t.Fatalf("expected running build in list, got %#v", builds)
	}

	response = doRequest(t, server, http.MethodGet, "/api/builds/vision-release-ready/release-check", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected release check status 200, got %d: %s", response.Code, response.Body.String())
	}
	var check domain.ReleaseCheckResult
	if err := json.NewDecoder(response.Body).Decode(&check); err != nil {
		t.Fatal(err)
	}
	if !check.ReleaseReady || check.BlockingCount != 0 {
		t.Fatalf("expected seeded release build to be ready, got %#v", check)
	}
}

func TestPreviewValidationRequiredBeforeRelease(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()
	now := time.Now().UTC()
	build := domain.Build{
		ID:             "build-preview",
		ProductID:      "lsap",
		Target:         "linux",
		Profile:        "qa",
		Version:        "1.0.0",
		ReleaseChannel: "preview",
		Status:         domain.BuildSucceeded,
		ArtifactURI:    "local-upload://preview/lsap.tar.gz",
		DownloadURL:    "http://example.test/preview.tar.gz",
		BuildLock: domain.BuildLock{
			BuildID:        "build-preview",
			ProductID:      "lsap",
			Target:         "linux",
			Profile:        "qa",
			Version:        "1.0.0",
			ReleaseChannel: "preview",
			Components: []domain.ResolvedComponent{{
				ID:       "ui",
				Version:  "1.0.0",
				URI:      "minio://artifact/Publish/ui/1.0.0/ui.zip",
				Required: true,
			}},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}

	response := doRequest(t, server, http.MethodPost, "/api/builds/build-preview/publish", `{}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected publish before validation status 400, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequest(t, server, http.MethodPost, "/api/builds/build-preview/validate-preview", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected validate preview status 200, got %d: %s", response.Code, response.Body.String())
	}
	var validated domain.Build
	if err := json.NewDecoder(response.Body).Decode(&validated); err != nil {
		t.Fatal(err)
	}
	if validated.Status != domain.BuildPreviewValidated || validated.ValidatedAt == nil || validated.ReleaseChannel != "preview" {
		t.Fatalf("unexpected validated build: %#v", validated)
	}

	response = doRequest(t, server, http.MethodPost, "/api/builds/build-preview/publish", `{}`)
	if response.Code != http.StatusOK {
		t.Fatalf("expected publish after validation status 200, got %d: %s", response.Code, response.Body.String())
	}
	var published domain.Build
	if err := json.NewDecoder(response.Body).Decode(&published); err != nil {
		t.Fatal(err)
	}
	if published.Status != domain.BuildPublished || published.ReleaseChannel != "release" || published.PublishedAt == nil {
		t.Fatalf("unexpected published build: %#v", published)
	}
}

func TestReleaseCheckReportsPreviewAndReleaseBlockingChecks(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()
	now := time.Now().UTC()
	build := domain.Build{
		ID:             "build-check",
		ProductID:      "lsap",
		Target:         "linux",
		Profile:        "qa",
		Version:        "latest",
		ReleaseChannel: "preview",
		Status:         domain.BuildSucceeded,
		ArtifactURI:    "local-upload://preview/lsap.tar.gz",
		DownloadURL:    "http://example.test/preview.tar.gz",
		BuildLock: domain.BuildLock{
			BuildID:        "build-check",
			ProductID:      "lsap",
			Target:         "linux",
			Profile:        "qa",
			Version:        "latest",
			ReleaseChannel: "preview",
			Components: []domain.ResolvedComponent{{
				ID:       "ui",
				Version:  "latest",
				URI:      "minio://artifact/Publish/ui/latest/*.zip",
				Required: true,
			}},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}

	response := doRequest(t, server, http.MethodGet, "/api/builds/build-check/release-check", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected release check status 200, got %d: %s", response.Code, response.Body.String())
	}
	var check domain.ReleaseCheckResult
	if err := json.NewDecoder(response.Body).Decode(&check); err != nil {
		t.Fatal(err)
	}
	if !check.PreviewReady || check.ReleaseReady || check.BlockingCount == 0 {
		t.Fatalf("unexpected release check readiness: %#v", check)
	}
	if !hasReleaseCheck(check.Checks, "release.fixed-version", "blocked") {
		t.Fatalf("expected fixed version release blocker: %#v", check.Checks)
	}
	if !hasReleaseCheck(check.Checks, "release.exact-component-uris", "blocked") {
		t.Fatalf("expected exact uri release blocker: %#v", check.Checks)
	}
}

func TestValidatePreviewRejectsMismatchedBuildLock(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()
	now := time.Now().UTC()
	build := domain.Build{
		ID:             "build-mismatch",
		ProductID:      "lsap",
		Target:         "linux",
		Profile:        "qa",
		Version:        "1.0.0",
		ReleaseChannel: "preview",
		Status:         domain.BuildSucceeded,
		ArtifactURI:    "local-upload://preview/lsap.tar.gz",
		DownloadURL:    "http://example.test/preview.tar.gz",
		BuildLock: domain.BuildLock{
			BuildID:        "build-mismatch",
			ProductID:      "lsap",
			Target:         "windows",
			Profile:        "qa",
			Version:        "1.0.0",
			ReleaseChannel: "preview",
			Components: []domain.ResolvedComponent{{
				ID:       "ui",
				Version:  "1.0.0",
				URI:      "minio://artifact/Publish/ui/1.0.0/ui.zip",
				Required: true,
			}},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}

	response := doRequest(t, server, http.MethodPost, "/api/builds/build-mismatch/validate-preview", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected mismatched lock status 400, got %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Build Lock") {
		t.Fatalf("unexpected error: %s", response.Body.String())
	}
}

func TestCreateBuildRejectsDirectReleaseChannel(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()

	response := doRequest(
		t,
		server,
		http.MethodPost,
		"/api/builds",
		`{"productId":"lsap","target":"linux","version":"1.0.0","releaseChannel":"release"}`,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected direct release create status 400, got %d: %s", response.Code, response.Body.String())
	}
}

func TestPublishRejectsNonReproducibleRelease(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()
	now := time.Now().UTC()
	build := domain.Build{
		ID:             "build-latest",
		ProductID:      "lsap",
		Target:         "windows",
		Profile:        "qa",
		Version:        "latest",
		ReleaseChannel: "preview",
		Status:         domain.BuildPreviewValidated,
		ArtifactURI:    "local-upload://preview/lsap.exe",
		DownloadURL:    "http://example.test/preview.exe",
		BuildLock: domain.BuildLock{
			BuildID:        "build-latest",
			ProductID:      "lsap",
			Target:         "windows",
			Profile:        "qa",
			Version:        "latest",
			ReleaseChannel: "preview",
			Components: []domain.ResolvedComponent{{
				ID:       "ui",
				Version:  "latest",
				URI:      "minio://artifact/Publish/ui/latest/*.zip",
				Required: true,
			}},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}

	response := doRequest(t, server, http.MethodPost, "/api/builds/build-latest/publish", `{}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected non-reproducible publish status 400, got %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "fixed build version") {
		t.Fatalf("unexpected error: %s", response.Body.String())
	}
}

func hasReleaseCheck(checks []domain.ReleaseCheckItem, id string, status string) bool {
	for _, check := range checks {
		if check.ID == id && check.Status == status {
			return true
		}
	}
	return false
}

func hasResolvedComponentURI(components []domain.ResolvedComponent, id string, uri string) bool {
	for _, component := range components {
		if component.ID == id && component.URI == uri {
			return true
		}
	}
	return false
}

func hasBuildStatus(builds []domain.Build, id string, status string) bool {
	for _, build := range builds {
		if build.ID == id && build.Status == status {
			return true
		}
	}
	return false
}

func releaseReadyBuild(id string, productID string, target string, profile string, version string, now time.Time) domain.Build {
	components := []domain.ResolvedComponent{
		{
			ID:       "vision-api",
			Type:     "docker-image",
			Version:  version,
			URI:      "minio://artifact/vision-api/" + version + "/vision-api.tar.gz",
			Required: true,
		},
		{
			ID:       "vision-ui",
			Type:     "archive",
			Version:  version,
			URI:      "https://artifacts.example.test/vision-ui-" + version + ".zip",
			Required: true,
		},
	}
	lock := domain.BuildLock{
		BuildID:        id,
		ProductID:      productID,
		Target:         target,
		Profile:        profile,
		Customer:       "city-bank",
		Version:        version,
		ReleaseChannel: "preview",
		Config: map[string]any{
			"installDir": "C:\\Program Files\\VisionEdge",
			"ports": map[string]any{
				"web": float64(51000),
			},
		},
		Components: components,
		Workloads: []domain.ResolvedWorkload{
			{ID: "vision-api", Runtime: "docker", Component: "vision-api", Enabled: true},
			{ID: "vision-worker", Runtime: "systemd", Component: "vision-api", Enabled: true},
		},
	}
	return domain.Build{
		ID:             id,
		ProductID:      productID,
		Target:         target,
		Profile:        profile,
		Customer:       "city-bank",
		Version:        version,
		ReleaseChannel: "preview",
		Status:         domain.BuildPreviewValidated,
		ArtifactURI:    "minio://artifact/packages/" + productID + "/" + version + "/" + target + "/setup.exe",
		DownloadURL:    "https://artifacts.example.test/packages/" + productID + "/" + version + "/" + target + "/setup.exe",
		FinalConfig:    lock.Config,
		Components:     components,
		Workloads:      lock.Workloads,
		BuildLock:      lock,
		CreatedAt:      now.Add(-3 * time.Hour),
		UpdatedAt:      now,
		ValidatedAt:    &now,
	}
}

func newTestServerStore(t *testing.T) (*storepkg.JSONStore, string) {
	t.Helper()
	dataDir := t.TempDir()
	productsDir := t.TempDir()
	productDir := filepath.Join(productsDir, "lsap")
	if err := os.MkdirAll(filepath.Join(productDir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(productDir, "customers"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
	  "id":"lsap",
	  "name":"LSAP",
	  "targets":["linux","windows"],
	  "targetConfig":{
	    "linux":{"installDir":"/opt/lsap","packageName":"lsap-linux"},
	    "windows":{"installDir":"C:\\\\LSAP","packageName":"lsap-windows"}
	  },
	  "profiles":["qa","prod"],
	  "versions":["latest","1.0.0"],
	  "components":[{"id":"ui","type":"archive","required":true,"source":{"kind":"fixed-url","url":"https://example.test/ui.zip"}}],
	  "config":{"modules":{"map":true}}
	}`
	if err := os.WriteFile(filepath.Join(productDir, "product.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(productDir, "profiles", "qa.json"), []byte(`{"config":{"ports":{"web":48001}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(productDir, "customers", "acme.json"), []byte(`{"config":{"customer":{"name":"ACME"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	visionDir := filepath.Join(productsDir, "vision-edge")
	if err := os.MkdirAll(filepath.Join(visionDir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(visionDir, "customers"), 0o755); err != nil {
		t.Fatal(err)
	}
	visionManifest := `{
	  "id":"vision-edge",
	  "name":"Vision Edge",
	  "description":"Multi-target camera analytics installer fixture.",
	  "targets":["linux","windows"],
	  "targetConfig":{
	    "linux":{"installDir":"/opt/vision-edge","packageName":"vision-edge-linux"},
	    "windows":{"installDir":"C:\\\\Program Files\\\\VisionEdge","packageName":"vision-edge-setup"}
	  },
	  "profiles":["qa","prod","preview"],
	  "versions":["latest","2026.05.28","2026.06.01"],
	  "components":[
	    {
	      "id":"vision-api",
	      "type":"docker-image",
	      "required":true,
	      "source":{"kind":"artifact-repository","repository":"minio","path":"artifact/vision-api/{{version}}/vision-api-*.tar.gz"}
	    },
	    {
	      "id":"vision-ui",
	      "type":"archive",
	      "required":true,
	      "source":{"kind":"fixed-url","url":"https://artifacts.example.test/vision-ui-{{version}}.zip"}
	    },
	    {
	      "id":"camera-model",
	      "type":"model",
	      "required":false,
	      "source":{"kind":"manual-upload","accept":[".zip",".onnx"]}
	    }
	  ],
	  "workloads":[
	    {"id":"vision-api","runtime":"docker","component":"vision-api","module":"camera","docker":{"service":"vision-api"}},
	    {"id":"vision-worker","runtime":"systemd","component":"vision-api","module":"push","systemd":{"unitName":"vision-worker.service","execStart":"/opt/vision-edge/bin/worker"}}
	  ],
	  "config":{
	    "ports":{"web":50080,"grpc":50081},
	    "modules":{"camera":true,"push":true,"map":false},
	    "customer":{"name":"Default Vision Customer","license":"enterprise"},
	    "database":{"passwordPolicy":"strong"}
	  }
	}`
	if err := os.WriteFile(filepath.Join(visionDir, "product.json"), []byte(visionManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(visionDir, "profiles", "prod.json"), []byte(`{"config":{"ports":{"web":50090},"modules":{"map":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(visionDir, "customers", "city-bank.json"), []byte(`{"config":{"customer":{"name":"City Bank","license":"platinum"},"ports":{"web":50100}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := storepkg.NewJSONStore(dataDir, productsDir)
	if err != nil {
		t.Fatal(err)
	}
	return store, productsDir
}

func doRequest(t *testing.T, handler http.Handler, method string, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

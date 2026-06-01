package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"packaging-platform/backend/internal/artifact"
	"packaging-platform/backend/internal/domain"
	manifestvalidator "packaging-platform/backend/internal/manifest"
	"packaging-platform/backend/internal/nomad"
	"packaging-platform/backend/internal/resolver"
	"packaging-platform/backend/internal/store"
)

type Server struct {
	store     store.Store
	scheduler nomad.Scheduler
	artifacts *artifact.Store
}

func NewServer(store store.Store, scheduler nomad.Scheduler, artifacts *artifact.Store) Server {
	return Server{store: store, scheduler: scheduler, artifacts: artifacts}
}

func (s Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/products", s.listProducts)
	mux.HandleFunc("GET /api/products/{id}", s.getProduct)
	mux.HandleFunc("GET /api/products/{id}/manifest", s.getProductManifest)
	mux.HandleFunc("PUT /api/products/{id}/manifest", s.updateProductManifest)
	mux.HandleFunc("POST /api/products/{id}/manifest/dry-run", s.dryRunProductManifest)
	mux.HandleFunc("GET /api/products/{id}/manifest/revisions", s.listProductManifestRevisions)
	mux.HandleFunc("POST /api/products/{id}/manifest/revisions/{revision}/restore", s.restoreProductManifestRevision)
	mux.HandleFunc("GET /api/products/{id}/overlays/{kind}", s.listProductOverlays)
	mux.HandleFunc("GET /api/products/{id}/overlays/{kind}/{name}", s.getProductOverlay)
	mux.HandleFunc("PUT /api/products/{id}/overlays/{kind}/{name}", s.updateProductOverlay)
	mux.HandleFunc("GET /api/products/{id}/versions", s.getProductVersions)
	mux.HandleFunc("GET /api/builds", s.listBuilds)
	mux.HandleFunc("POST /api/builds", s.createBuild)
	mux.HandleFunc("GET /api/builds/{id}", s.getBuild)
	mux.HandleFunc("GET /api/builds/{id}/lock", s.getBuildLock)
	mux.HandleFunc("GET /api/builds/{id}/release-check", s.getBuildReleaseCheck)
	mux.HandleFunc("GET /api/builds/{id}/download", s.downloadBuild)
	mux.HandleFunc("POST /api/builds/{id}/approve", s.approveBuild)
	mux.HandleFunc("POST /api/builds/{id}/reject", s.rejectBuild)
	mux.HandleFunc("POST /api/builds/{id}/validate-preview", s.validatePreviewBuild)
	mux.HandleFunc("POST /api/builds/{id}/publish", s.publishBuild)
	mux.HandleFunc("POST /api/builds/{id}/retry", s.retryBuild)
	mux.HandleFunc("POST /api/builds/{id}/cancel", s.cancelBuild)
	mux.HandleFunc("GET /api/builds/{id}/logs", s.getBuildLogs)
	mux.HandleFunc("POST /api/builds/{id}/logs", s.appendBuildLog)
	mux.HandleFunc("POST /api/runner/tasks/claim", s.claimRunnerTask)
	mux.HandleFunc("POST /api/runner/tasks/{id}/complete", s.completeRunnerTask)
	mux.HandleFunc("POST /api/runner/heartbeat", s.runnerHeartbeat)
	mux.HandleFunc("POST /api/uploads", s.uploadArtifact)
	mux.HandleFunc("GET /api/uploads", s.listUploads)
	mux.HandleFunc("GET /api/uploads/{id}/download", s.downloadUpload)
	return cors(mux)
}

func (s Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s Server) listProducts(w http.ResponseWriter, r *http.Request) {
	products, err := s.store.ListProducts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, products)
}

func (s Server) getProduct(w http.ResponseWriter, r *http.Request) {
	product, err := s.store.ReadProduct(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, product)
}

func (s Server) getProductManifest(w http.ResponseWriter, r *http.Request) {
	manifest, err := s.store.ReadProductManifest(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeRawJSON(w, http.StatusOK, manifest)
}

func (s Server) updateProductManifest(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, errors := manifestvalidator.ValidateProduct(raw); len(errors) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "manifest validation failed", "errors": errors})
		return
	}
	product, err := s.store.WriteProductManifest(r.PathValue("id"), raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, product)
}

func (s Server) dryRunProductManifest(w http.ResponseWriter, r *http.Request) {
	var request domain.ManifestDryRunRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(request.Manifest) == 0 {
		raw, err := s.store.ReadProductManifest(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		request.Manifest = raw
	}
	product, errors := manifestvalidator.ValidateProduct(request.Manifest)
	if len(errors) > 0 {
		writeJSON(w, http.StatusOK, domain.ManifestDryRunResult{Valid: false, Errors: errors})
		return
	}
	if product.ID == "" {
		product.ID = r.PathValue("id")
	}
	buildRequest := domain.BuildRequest{
		ProductID:          product.ID,
		Target:             firstNonEmpty(request.Target, firstString(product.Targets, "linux")),
		Profile:            firstNonEmpty(request.Profile, firstString(product.Profiles, "")),
		Customer:           request.Customer,
		Version:            firstNonEmpty(request.Version, firstString(product.Versions, "latest")),
		ReleaseChannel:     firstNonEmpty(request.ReleaseChannel, "preview"),
		Variables:          request.Variables,
		ConfigOverrides:    request.ConfigOverrides,
		ComponentOverrides: request.ComponentOverrides,
	}
	config, components, lock, err := resolver.ResolveBuild(product, buildRequest, "dry-run", s.store)
	if err != nil {
		writeJSON(w, http.StatusOK, domain.ManifestDryRunResult{Valid: false, Errors: []string{err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, domain.ManifestDryRunResult{
		Valid:      true,
		Errors:     []string{},
		BuildLock:  lock,
		Config:     config,
		Components: components,
	})
}

func (s Server) listProductManifestRevisions(w http.ResponseWriter, r *http.Request) {
	revisions, err := s.store.ListManifestRevisions(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, revisions)
}

func (s Server) restoreProductManifestRevision(w http.ResponseWriter, r *http.Request) {
	product, err := s.store.RestoreManifestRevision(r.PathValue("id"), r.PathValue("revision"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, product)
}

func (s Server) listProductOverlays(w http.ResponseWriter, r *http.Request) {
	records, err := s.store.ListOverlays(r.PathValue("id"), r.PathValue("kind"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, records)
}

func (s Server) getProductOverlay(w http.ResponseWriter, r *http.Request) {
	overlay, err := s.store.ReadOverlay(r.PathValue("id"), r.PathValue("kind"), r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, store.OverlayRecord{
		Name: r.PathValue("name"),
		Kind: r.PathValue("kind"),
		Data: overlay,
	})
}

func (s Server) updateProductOverlay(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	record, err := s.store.WriteOverlay(r.PathValue("id"), r.PathValue("kind"), r.PathValue("name"), raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (s Server) getProductVersions(w http.ResponseWriter, r *http.Request) {
	product, err := s.store.ReadProduct(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	versions := product.Versions
	if len(versions) == 0 {
		versions = []string{"latest"}
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

func (s Server) listBuilds(w http.ResponseWriter, r *http.Request) {
	builds, err := s.store.ListBuilds()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, builds)
}

func (s Server) getBuild(w http.ResponseWriter, r *http.Request) {
	build, err := s.store.ReadBuild(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, build)
}

func (s Server) downloadBuild(w http.ResponseWriter, r *http.Request) {
	build, err := s.store.ReadBuild(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if build.DownloadURL != "" {
		http.Redirect(w, r, build.DownloadURL, http.StatusFound)
		return
	}
	if build.ArtifactURI == "" {
		writeErrorText(w, http.StatusNotFound, "build artifact is not available")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"artifactUri": build.ArtifactURI})
}

func (s Server) createBuild(w http.ResponseWriter, r *http.Request) {
	var request domain.BuildRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(request.ProductID) == "" || strings.TrimSpace(request.Target) == "" {
		writeErrorText(w, http.StatusBadRequest, "productId and target are required")
		return
	}

	product, err := s.store.ReadProduct(request.ProductID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	now := time.Now().UTC()
	build := domain.Build{
		ID:             randomID(),
		ProductID:      request.ProductID,
		Target:         request.Target,
		Profile:        request.Profile,
		Customer:       request.Customer,
		Version:        request.Version,
		ReleaseChannel: request.ReleaseChannel,
		Status:         domain.BuildPendingApproval,
		Variables:      request.Variables,
		CreatedAt:      now,
		UpdatedAt:      now,
		Logs: []domain.BuildLog{{
			Time:    now,
			Source:  "system",
			Message: "build request created and waiting for approval",
		}},
	}
	if build.Profile == "" && len(product.Profiles) > 0 {
		build.Profile = product.Profiles[0]
	}
	if build.Version == "" && len(product.Versions) > 0 {
		build.Version = product.Versions[0]
	}
	if build.ReleaseChannel == "" {
		build.ReleaseChannel = "preview"
	}
	if build.ReleaseChannel == "release" {
		writeErrorText(w, http.StatusBadRequest, "release builds must be promoted from validated preview builds")
		return
	}
	request.Profile = build.Profile
	request.Version = build.Version
	request.ReleaseChannel = build.ReleaseChannel

	finalConfig, components, lock, err := resolver.ResolveBuild(product, request, build.ID, s.store)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	build.FinalConfig = finalConfig
	build.Components = components
	build.BuildLock = lock
	build.Workloads = lock.Workloads
	build.Logs = append(build.Logs, logEntry("resolver", "final config and component lock generated"))

	if err := s.store.SaveBuild(build); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusAccepted, build)
}

func (s Server) getBuildLock(w http.ResponseWriter, r *http.Request) {
	build, err := s.store.ReadBuild(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, build.BuildLock)
}

func (s Server) getBuildReleaseCheck(w http.ResponseWriter, r *http.Request) {
	build, err := s.store.ReadBuild(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, buildReleaseCheck(build))
}

func (s Server) approveBuild(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	current, err := s.store.ReadBuild(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if current.Status != domain.BuildPendingApproval && current.Status != domain.BuildRejected {
		writeErrorText(w, http.StatusBadRequest, "only pending or rejected builds can be approved")
		return
	}
	product, err := s.store.ReadProduct(current.ProductID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	jobID, schedulerLog, err := s.scheduler.Submit(current, product)
	if err != nil {
		updated, _ := s.store.UpdateBuild(id, func(build *domain.Build) error {
			build.Status = domain.BuildFailed
			build.Error = err.Error()
			build.SchedulerLog = schedulerLog
			build.UpdatedAt = time.Now().UTC()
			build.Logs = append(build.Logs, logEntry("system", "approval failed: "+err.Error()))
			return nil
		})
		writeJSON(w, http.StatusBadGateway, updated)
		return
	}

	now := time.Now().UTC()
	updated, err := s.store.UpdateBuild(id, func(build *domain.Build) error {
		build.Status = domain.BuildQueued
		build.NomadJobID = jobID
		build.SchedulerLog = schedulerLog
		build.ApprovedAt = &now
		build.UpdatedAt = now
		build.Logs = append(build.Logs, logEntry("system", "approved and queued for runner"))
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s Server) rejectBuild(w http.ResponseWriter, r *http.Request) {
	updated, err := s.store.UpdateBuild(r.PathValue("id"), func(build *domain.Build) error {
		if build.Status != domain.BuildPendingApproval {
			return errText("only pending builds can be rejected")
		}
		build.Status = domain.BuildRejected
		build.UpdatedAt = time.Now().UTC()
		build.Logs = append(build.Logs, logEntry("system", "build rejected"))
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s Server) validatePreviewBuild(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	updated, err := s.store.UpdateBuild(r.PathValue("id"), func(build *domain.Build) error {
		if build.Status != domain.BuildSucceeded {
			return errText("only succeeded preview builds can be validated")
		}
		check := buildReleaseCheck(*build)
		if !check.PreviewReady {
			return firstBlockedCheck(check.Checks, "preview")
		}
		build.Status = domain.BuildPreviewValidated
		build.ReleaseChannel = "preview"
		build.ValidatedAt = &now
		build.UpdatedAt = now
		build.Logs = append(build.Logs, logEntry("system", "preview artifact validated"))
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s Server) publishBuild(w http.ResponseWriter, r *http.Request) {
	var request domain.RunnerCompleteRequest
	_ = json.NewDecoder(r.Body).Decode(&request)

	now := time.Now().UTC()
	updated, err := s.store.UpdateBuild(r.PathValue("id"), func(build *domain.Build) error {
		if build.Status != domain.BuildPreviewValidated && build.Status != domain.BuildPublished {
			return errText("only validated preview builds can be published")
		}
		check := buildReleaseCheck(*build)
		if !check.ReleaseReady {
			return firstBlockedCheck(check.Checks, "")
		}
		if request.ArtifactURI != "" {
			build.ArtifactURI = request.ArtifactURI
		}
		if request.DownloadURL != "" {
			build.DownloadURL = request.DownloadURL
		}
		build.Status = domain.BuildPublished
		build.ReleaseChannel = "release"
		build.PublishedAt = &now
		build.UpdatedAt = now
		build.Logs = append(build.Logs, logEntry("system", "validated preview promoted to release"))
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s Server) retryBuild(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	current, err := s.store.ReadBuild(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if current.Status != domain.BuildFailed && current.Status != domain.BuildCanceled {
		writeErrorText(w, http.StatusBadRequest, "only failed or canceled builds can be retried")
		return
	}
	product, err := s.store.ReadProduct(current.ProductID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	jobID, schedulerLog, err := s.scheduler.Submit(current, product)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	updated, err := s.store.UpdateBuild(id, func(build *domain.Build) error {
		build.Status = domain.BuildQueued
		build.Error = ""
		build.RunnerID = ""
		build.NomadJobID = jobID
		build.SchedulerLog = schedulerLog
		build.UpdatedAt = time.Now().UTC()
		build.Logs = append(build.Logs, logEntry("system", "build queued for retry"))
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s Server) cancelBuild(w http.ResponseWriter, r *http.Request) {
	current, err := s.store.ReadBuild(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if current.NomadJobID != "" && (current.Status == domain.BuildQueued || current.Status == domain.BuildRunning) {
		if err := s.scheduler.Stop(current.NomadJobID); err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
	}
	updated, err := s.store.CancelBuild(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s Server) getBuildLogs(w http.ResponseWriter, r *http.Request) {
	build, err := s.store.ReadBuild(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, build.Logs)
}

func (s Server) appendBuildLog(w http.ResponseWriter, r *http.Request) {
	var request domain.BuildLogRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.Source == "" {
		request.Source = "runner"
	}
	updated, err := s.store.UpdateBuild(r.PathValue("id"), func(build *domain.Build) error {
		build.Logs = append(build.Logs, logEntry(request.Source, request.Message))
		build.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, updated.Logs)
}

func (s Server) claimRunnerTask(w http.ResponseWriter, r *http.Request) {
	var request domain.RunnerClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.RunnerID == "" {
		writeErrorText(w, http.StatusBadRequest, "runnerId is required")
		return
	}
	build, ok, err := s.store.ClaimBuild(request.Target, request.RunnerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusNoContent, nil)
		return
	}
	writeJSON(w, http.StatusOK, build)
}

func (s Server) completeRunnerTask(w http.ResponseWriter, r *http.Request) {
	var request domain.RunnerCompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status := domain.BuildSucceeded
	if request.Status == domain.BuildFailed {
		status = domain.BuildFailed
	}

	updated, err := s.store.UpdateBuild(r.PathValue("id"), func(build *domain.Build) error {
		if build.Status != domain.BuildRunning {
			return errText("only running builds can be completed")
		}
		build.Status = status
		build.ArtifactURI = request.ArtifactURI
		build.DownloadURL = request.DownloadURL
		build.Error = request.Error
		build.UpdatedAt = time.Now().UTC()
		if status == domain.BuildSucceeded {
			build.Logs = append(build.Logs, logEntry("runner", "build completed successfully"))
		} else {
			build.Logs = append(build.Logs, logEntry("runner", "build failed: "+request.Error))
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s Server) runnerHeartbeat(w http.ResponseWriter, r *http.Request) {
	var request domain.RunnerHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.RunnerID == "" {
		writeErrorText(w, http.StatusBadRequest, "runnerId is required")
		return
	}
	message := "heartbeat"
	if request.Message != "" {
		message = "heartbeat: " + request.Message
	}
	if request.BuildID != "" {
		_, err := s.store.UpdateBuild(request.BuildID, func(build *domain.Build) error {
			build.UpdatedAt = time.Now().UTC()
			build.Logs = append(build.Logs, logEntry(request.RunnerID, message))
			return nil
		})
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s Server) listUploads(w http.ResponseWriter, r *http.Request) {
	records, err := s.store.ListUploads()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, records)
}

func (s Server) uploadArtifact(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(512 << 20); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	defer file.Close()

	record, err := s.artifacts.SaveUpload(file, header)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.store.SaveUploadRecord(record); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, record)
}

func (s Server) downloadUpload(w http.ResponseWriter, r *http.Request) {
	if err := s.artifacts.ServeUpload(w, r, r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, err)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status == http.StatusNoContent {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func writeRawJSON(w http.ResponseWriter, status int, payload json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeErrorText(w, status, err.Error())
}

func writeErrorText(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func buildReleaseCheck(build domain.Build) domain.ReleaseCheckResult {
	components := build.BuildLock.Components
	if len(components) == 0 {
		components = build.Components
	}
	checks := []domain.ReleaseCheckItem{
		releaseCheckItem("preview.succeeded", "preview", "Preview 构建已成功", previewStatusReady(build), "only succeeded preview builds can be validated"),
		releaseCheckItem("preview.channel", "preview", "构建通道为 Preview", build.ReleaseChannel == "preview" || build.Status == domain.BuildPublished, "preview validation requires preview channel"),
		releaseCheckItem("preview.artifact", "preview", "Preview 制品可下载", build.ArtifactURI != "" || build.DownloadURL != "", "preview artifact is not available"),
		releaseCheckItem("preview.build-lock", "preview", "Build Lock 已生成", build.BuildLock.BuildID != "", "Build Lock is not available"),
		releaseCheckItem("preview.lock-identity", "preview", "Build Lock 与构建参数一致", buildLockMatchesBuild(build), "Build Lock does not match build target, profile, or version"),
		releaseCheckItem("preview.required-components", "preview", "必需组件已锁定 URI", requiredComponentsLocked(components), "preview requires locked required component URIs"),
		releaseCheckItem("release.preview-validated", "release", "Preview 已验证", build.Status == domain.BuildPreviewValidated || build.Status == domain.BuildPublished, "only validated preview builds can be published"),
		releaseCheckItem("release.fixed-version", "release", "构建版本固定", build.Version != "" && build.Version != "latest", "release requires a fixed build version"),
		releaseCheckItem("release.exact-component-uris", "release", "组件 URI 精确", componentURIsExact(components), "release requires exact component URIs without wildcards"),
		releaseCheckItem("release.fixed-component-versions", "release", "组件版本固定", componentVersionsFixed(components), "release requires fixed component versions"),
		releaseCheckItem("release.artifact", "release", "Release 制品可下载", build.ArtifactURI != "" || build.DownloadURL != "", "release artifact is not available"),
		releaseCheckItem("release.no-build-error", "release", "构建无错误记录", strings.TrimSpace(build.Error) == "", "release requires a build without errors"),
	}
	previewReady := true
	releaseReady := true
	blockingCount := 0
	for _, check := range checks {
		if check.Status != "blocked" {
			continue
		}
		blockingCount++
		if check.Stage == "preview" {
			previewReady = false
		}
		releaseReady = false
	}
	stage := "preview"
	if build.Status == domain.BuildPreviewValidated || build.Status == domain.BuildPublished {
		stage = "release"
	}
	return domain.ReleaseCheckResult{
		BuildID:       build.ID,
		Stage:         stage,
		PreviewReady:  previewReady,
		ReleaseReady:  releaseReady,
		BlockingCount: blockingCount,
		Checks:        checks,
	}
}

func releaseCheckItem(id string, stage string, title string, ok bool, reason string) domain.ReleaseCheckItem {
	item := domain.ReleaseCheckItem{ID: id, Stage: stage, Title: title, Status: "pass"}
	if !ok {
		item.Status = "blocked"
		item.Reason = reason
	}
	return item
}

func firstBlockedCheck(checks []domain.ReleaseCheckItem, stage string) error {
	for _, check := range checks {
		if check.Status == "blocked" && (stage == "" || check.Stage == stage) {
			return errText(check.Reason)
		}
	}
	return errText("release check failed")
}

func previewStatusReady(build domain.Build) bool {
	return build.Status == domain.BuildSucceeded ||
		build.Status == domain.BuildPreviewValidated ||
		build.Status == domain.BuildPublished
}

func buildLockMatchesBuild(build domain.Build) bool {
	lock := build.BuildLock
	if lock.BuildID == "" {
		return false
	}
	if lock.Target != "" && lock.Target != build.Target {
		return false
	}
	if lock.Profile != "" && lock.Profile != build.Profile {
		return false
	}
	if lock.Version != "" && lock.Version != build.Version {
		return false
	}
	return true
}

func requiredComponentsLocked(components []domain.ResolvedComponent) bool {
	for _, component := range components {
		if component.Required && strings.TrimSpace(component.URI) == "" {
			return false
		}
	}
	return true
}

func componentURIsExact(components []domain.ResolvedComponent) bool {
	for _, component := range components {
		if strings.Contains(component.URI, "*") {
			return false
		}
	}
	return true
}

func componentVersionsFixed(components []domain.ResolvedComponent) bool {
	for _, component := range components {
		if component.Version == "latest" {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstString(values []string, fallback string) string {
	if len(values) == 0 {
		return fallback
	}
	return values[0]
}

type errText string

func (e errText) Error() string {
	return string(e)
}

func logEntry(source string, message string) domain.BuildLog {
	return domain.BuildLog{Time: time.Now().UTC(), Source: source, Message: message}
}

func randomID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000")))
	}
	return hex.EncodeToString(b[:])
}

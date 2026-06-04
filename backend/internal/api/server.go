package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"packaging-platform/backend/internal/apperr"
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
	mux.HandleFunc("GET /api/builds/{id}/logs/stream", s.streamBuildLogs)
	mux.HandleFunc("POST /api/builds/{id}/logs", s.appendBuildLog)
	mux.HandleFunc("POST /api/runner/tasks/claim", s.claimRunnerTask)
	mux.HandleFunc("POST /api/runner/tasks/{id}/complete", s.completeRunnerTask)
	mux.HandleFunc("POST /api/runner/heartbeat", s.runnerHeartbeat)
	mux.HandleFunc("GET /api/runners", s.listRunners)
	mux.HandleFunc("POST /api/uploads", s.uploadArtifact)
	mux.HandleFunc("GET /api/uploads", s.listUploads)
	mux.HandleFunc("GET /api/uploads/{id}/download", s.downloadUpload)
	return requestContext(cors(mux))
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
		writeAPIError(w, http.StatusBadRequest, "VALIDATION_INVALID_JSON", "", nil)
		return
	}
	if _, errors := manifestvalidator.ValidateProduct(raw); len(errors) > 0 {
		writeAPIError(w, http.StatusBadRequest, "MANIFEST_SCHEMA_INVALID", "", errorDetails(errors))
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
		writeAPIError(w, http.StatusBadRequest, "VALIDATION_INVALID_JSON", "", nil)
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
	writeJSON(w, http.StatusOK, withReleaseCheckSummaries(builds))
}

func (s Server) getBuild(w http.ResponseWriter, r *http.Request) {
	build, err := s.store.ReadBuild(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, withReleaseCheckSummary(build))
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
		writeAPIError(w, http.StatusNotFound, "BUILD_ARTIFACT_NOT_AVAILABLE", "", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"artifactUri": build.ArtifactURI})
}

func (s Server) createBuild(w http.ResponseWriter, r *http.Request) {
	var request domain.BuildRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "VALIDATION_INVALID_JSON", "", nil)
		return
	}
	if strings.TrimSpace(request.ProductID) == "" || strings.TrimSpace(request.Target) == "" {
		writeAPIError(w, http.StatusBadRequest, "VALIDATION_REQUIRED_FIELD", "productId and target are required", []domain.APIErrorDetail{
			{Field: "productId", Message: "productId is required"},
			{Field: "target", Message: "target is required"},
		})
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
		writeAPIError(w, http.StatusBadRequest, "BUILD_CREATE_INVALID_RELEASE_CHANNEL", "", nil)
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

	writeJSON(w, http.StatusAccepted, withReleaseCheckSummary(build))
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
		writeAPIError(w, http.StatusConflict, "BUILD_STATE_CONFLICT", "only pending or rejected builds can be approved", nil)
		return
	}
	product, err := s.store.ReadProduct(current.ProductID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	jobID, schedulerLog, err := s.scheduler.Submit(current, product)
	if err != nil {
		_, _ = s.store.UpdateBuild(id, func(build *domain.Build) error {
			build.Status = domain.BuildFailed
			build.Error = err.Error()
			build.SchedulerLog = schedulerLog
			build.UpdatedAt = time.Now().UTC()
			build.Logs = append(build.Logs, logEntry("system", "approval failed: "+err.Error()))
			return nil
		})
		writeAPIErrorWithCause(w, http.StatusBadGateway, "SCHEDULER_SUBMIT_FAILED", "", []domain.APIErrorDetail{
			{Message: "scheduler failed to submit build"},
		}, err)
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
	writeJSON(w, http.StatusOK, withReleaseCheckSummary(updated))
}

func (s Server) rejectBuild(w http.ResponseWriter, r *http.Request) {
	updated, err := s.store.UpdateBuild(r.PathValue("id"), func(build *domain.Build) error {
		if build.Status != domain.BuildPendingApproval {
			return buildStateConflict("only pending builds can be rejected")
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
	writeJSON(w, http.StatusOK, withReleaseCheckSummary(updated))
}

func (s Server) validatePreviewBuild(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	updated, err := s.store.UpdateBuild(r.PathValue("id"), func(build *domain.Build) error {
		if build.Status != domain.BuildSucceeded {
			return buildStateConflict("only succeeded preview builds can be validated")
		}
		check := buildReleaseCheck(*build)
		if !check.PreviewReady {
			return releaseGateError("BUILD_PREVIEW_NOT_READY", "", check, "preview")
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
	writeJSON(w, http.StatusOK, withReleaseCheckSummary(updated))
}

func (s Server) publishBuild(w http.ResponseWriter, r *http.Request) {
	var request domain.RunnerCompleteRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "VALIDATION_INVALID_JSON", "", nil)
			return
		}
	}

	now := time.Now().UTC()
	updated, err := s.store.UpdateBuild(r.PathValue("id"), func(build *domain.Build) error {
		if build.Status != domain.BuildPreviewValidated && build.Status != domain.BuildPublished {
			return buildStateConflict("only validated preview builds can be published")
		}
		check := buildReleaseCheck(*build)
		if !check.ReleaseReady {
			return releaseGateError("BUILD_RELEASE_GATE_BLOCKED", "", check, "")
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
	writeJSON(w, http.StatusOK, withReleaseCheckSummary(updated))
}

func (s Server) retryBuild(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	current, err := s.store.ReadBuild(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if current.Status != domain.BuildFailed && current.Status != domain.BuildCanceled {
		writeAPIError(w, http.StatusConflict, "BUILD_STATE_CONFLICT", "only failed or canceled builds can be retried", nil)
		return
	}
	product, err := s.store.ReadProduct(current.ProductID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	jobID, schedulerLog, err := s.scheduler.Submit(current, product)
	if err != nil {
		writeAPIErrorWithCause(w, http.StatusBadGateway, "SCHEDULER_SUBMIT_FAILED", "", []domain.APIErrorDetail{
			{Message: "scheduler failed to submit build"},
		}, err)
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
	writeJSON(w, http.StatusOK, withReleaseCheckSummary(updated))
}

func (s Server) cancelBuild(w http.ResponseWriter, r *http.Request) {
	current, err := s.store.ReadBuild(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if current.NomadJobID != "" && (current.Status == domain.BuildQueued || current.Status == domain.BuildRunning) {
		if err := s.scheduler.Stop(current.NomadJobID); err != nil {
			writeAPIErrorWithCause(w, http.StatusBadGateway, "SCHEDULER_STOP_FAILED", "", []domain.APIErrorDetail{
				{Message: "scheduler failed to stop build"},
			}, err)
			return
		}
	}
	updated, err := s.store.CancelBuild(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, withReleaseCheckSummary(updated))
}

func (s Server) getBuildLogs(w http.ResponseWriter, r *http.Request) {
	build, err := s.store.ReadBuild(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, logsWithSequence(build.Logs))
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
		build.Logs = appendBuildLog(build.Logs, request.Source, request.Message)
		build.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, updated.Logs)
}

func (s Server) streamBuildLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErrorText(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	sent := 0
	sendNew := func() error {
		build, err := s.store.ReadBuild(id)
		if err != nil {
			return err
		}
		logs := logsWithSequence(build.Logs)
		for sent < len(logs) {
			payload, err := json.Marshal(logs[sent])
			if err != nil {
				return err
			}
			_, _ = w.Write([]byte("event: log\n"))
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(payload)
			_, _ = w.Write([]byte("\n\n"))
			sent++
		}
		flusher.Flush()
		return nil
	}

	if err := sendNew(); err != nil {
		writeSSEError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", err.Error())
		flusher.Flush()
		return
	}
	if r.URL.Query().Get("follow") != "true" {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := sendNew(); err != nil {
				writeSSEError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", err.Error())
				flusher.Flush()
				return
			}
		}
	}
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
	writeJSON(w, http.StatusOK, withReleaseCheckSummary(build))
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
			return buildStateConflict("only running builds can be completed")
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
	writeJSON(w, http.StatusOK, withReleaseCheckSummary(updated))
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
			build.Logs = appendBuildLog(build.Logs, request.RunnerID, message)
			return nil
		})
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s Server) listRunners(w http.ResponseWriter, r *http.Request) {
	builds, err := s.store.ListBuilds()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, runnerStatuses(builds, time.Now().UTC()))
}

func (s Server) listUploads(w http.ResponseWriter, r *http.Request) {
	records, err := s.store.ListUploads()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	now := time.Now().UTC()
	for index := range records {
		records[index] = normalizeUploadRecord(records[index], now)
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
	record = applyUploadMetadata(record, r)
	if err := s.store.SaveUploadRecord(record); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, record)
}

func (s Server) downloadUpload(w http.ResponseWriter, r *http.Request) {
	record, err := s.store.ReadUpload(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	now := time.Now().UTC()
	record = normalizeUploadRecord(record, now)
	if uploadRetentionStatus(record, now) == "expired" {
		writeAPIError(w, http.StatusGone, "ARTIFACT_EXPIRED", "", nil)
		return
	}
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
	var appErr *apperr.AppError
	if errors.As(err, &appErr) {
		status = appErrorStatus(appErr, status)
		code := firstNonEmpty(appErr.Code, defaultErrorCode(status))
		logAPIError(w, status, code, err)
		writeAPIError(w, status, code, appErr.Message, appErrorDetails(appErr))
		return
	}
	code := defaultErrorCode(status)
	logAPIError(w, status, code, err)
	writeAPIError(w, status, code, "", nil)
}

func writeErrorText(w http.ResponseWriter, status int, message string) {
	writeAPIError(w, status, defaultErrorCode(status), message, nil)
}

func writeAPIErrorWithCause(w http.ResponseWriter, status int, code string, message string, details []domain.APIErrorDetail, cause error) {
	logAPIError(w, status, code, cause)
	writeAPIError(w, status, code, message, details)
}

func writeAPIError(w http.ResponseWriter, status int, code string, message string, details []domain.APIErrorDetail) {
	spec := errorSpecFor(code, status)
	if strings.TrimSpace(message) == "" {
		message = spec.Message
	}
	if code == "" {
		code = spec.Code
	}
	details = enrichErrorDetails(details)
	requestID := w.Header().Get("X-Request-Id")
	if requestID == "" {
		requestID = "req_" + randomID()
		w.Header().Set("X-Request-Id", requestID)
	}
	correlationID := w.Header().Get("X-Correlation-Id")
	if correlationID == "" {
		correlationID = "corr_" + randomID()
		w.Header().Set("X-Correlation-Id", correlationID)
	}
	response := domain.APIErrorResponse{
		Error: domain.APIError{
			Code:          code,
			Status:        status,
			MessageKey:    spec.MessageKey,
			Message:       message,
			Severity:      spec.Severity,
			Category:      spec.Category,
			Retryable:     spec.Retryable,
			UserActionKey: spec.UserActionKey,
			UserAction:    spec.UserAction,
			RequestID:     requestID,
			CorrelationID: correlationID,
			Details:       details,
		},
	}
	for _, detail := range details {
		response.Errors = append(response.Errors, detail.Message)
	}
	writeJSON(w, status, response)
}

func logAPIError(w http.ResponseWriter, status int, code string, err error) {
	if err == nil || !shouldLogAPIError(status) {
		return
	}
	slog.Error("api request failed",
		"error", err,
		"status", status,
		"error_code", code,
		"request_id", w.Header().Get("X-Request-Id"),
		"correlation_id", w.Header().Get("X-Correlation-Id"),
	)
}

func shouldLogAPIError(status int) bool {
	return status >= http.StatusInternalServerError ||
		status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout
}

func writeSSEError(w http.ResponseWriter, status int, code string, message string) {
	spec := errorSpecFor(code, status)
	requestID := w.Header().Get("X-Request-Id")
	if requestID == "" {
		requestID = "req_" + randomID()
	}
	correlationID := w.Header().Get("X-Correlation-Id")
	if correlationID == "" {
		correlationID = "corr_" + strings.TrimPrefix(requestID, "req_")
	}
	payload := domain.APIErrorResponse{
		Error: domain.APIError{
			Code:          spec.Code,
			Status:        status,
			MessageKey:    spec.MessageKey,
			Message:       firstNonEmpty(message, spec.Message),
			Severity:      spec.Severity,
			Category:      spec.Category,
			Retryable:     spec.Retryable,
			RequestID:     requestID,
			CorrelationID: correlationID,
		},
	}
	data, _ := json.Marshal(payload)
	_, _ = w.Write([]byte("event: error\n"))
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n\n"))
}

type errorSpec struct {
	Code          string
	MessageKey    string
	Message       string
	Severity      string
	Category      string
	Retryable     bool
	UserActionKey string
	UserAction    string
}

var errorSpecs = map[string]errorSpec{
	"VALIDATION_INVALID_JSON": {
		Code:       "VALIDATION_INVALID_JSON",
		MessageKey: "errors.validation.invalidJson",
		Message:    "request JSON cannot be parsed",
		Severity:   "error",
		Category:   "validation",
	},
	"VALIDATION_REQUIRED_FIELD": {
		Code:       "VALIDATION_REQUIRED_FIELD",
		MessageKey: "errors.validation.requiredField",
		Message:    "required fields are missing",
		Severity:   "error",
		Category:   "validation",
	},
	"VALIDATION_INVALID_REQUEST": {
		Code:       "VALIDATION_INVALID_REQUEST",
		MessageKey: "errors.validation.invalidRequest",
		Message:    "request is invalid",
		Severity:   "error",
		Category:   "validation",
	},
	"MANIFEST_SCHEMA_INVALID": {
		Code:          "MANIFEST_SCHEMA_INVALID",
		MessageKey:    "errors.manifest.schemaInvalid",
		Message:       "manifest validation failed",
		Severity:      "error",
		Category:      "validation",
		UserActionKey: "actions.fixManifestSchema",
		UserAction:    "fix manifest validation errors before saving",
	},
	"BUILD_STATE_CONFLICT": {
		Code:       "BUILD_STATE_CONFLICT",
		MessageKey: "errors.build.stateConflict",
		Message:    "build state does not allow this action",
		Severity:   "warning",
		Category:   "business",
	},
	"BUILD_CREATE_INVALID_RELEASE_CHANNEL": {
		Code:          "BUILD_CREATE_INVALID_RELEASE_CHANNEL",
		MessageKey:    "errors.build.invalidReleaseChannel",
		Message:       "release builds must be promoted from validated preview builds",
		Severity:      "error",
		Category:      "business",
		UserActionKey: "actions.createPreviewFirst",
		UserAction:    "create a preview build and validate it before publishing a release",
	},
	"BUILD_RELEASE_GATE_BLOCKED": {
		Code:          "BUILD_RELEASE_GATE_BLOCKED",
		MessageKey:    "errors.build.releaseGateBlocked",
		Message:       "release gate blocked",
		Severity:      "warning",
		Category:      "business",
		UserActionKey: "actions.fixReleaseGate",
		UserAction:    "fix release gate blockers before publishing",
	},
	"BUILD_PREVIEW_NOT_READY": {
		Code:          "BUILD_PREVIEW_NOT_READY",
		MessageKey:    "errors.build.previewNotReady",
		Message:       "preview validation gate blocked",
		Severity:      "warning",
		Category:      "business",
		UserActionKey: "actions.validatePreview",
		UserAction:    "complete preview build validation before continuing",
	},
	"BUILD_ARTIFACT_NOT_AVAILABLE": {
		Code:       "BUILD_ARTIFACT_NOT_AVAILABLE",
		MessageKey: "errors.build.artifactNotAvailable",
		Message:    "build artifact is not available",
		Severity:   "error",
		Category:   "resource",
	},
	"ARTIFACT_EXPIRED": {
		Code:       "ARTIFACT_EXPIRED",
		MessageKey: "errors.artifact.expired",
		Message:    "artifact retention period has expired",
		Severity:   "error",
		Category:   "resource",
	},
	"RESOURCE_NOT_FOUND": {
		Code:       "RESOURCE_NOT_FOUND",
		MessageKey: "errors.resource.notFound",
		Message:    "resource not found",
		Severity:   "error",
		Category:   "resource",
	},
	"SCHEDULER_SUBMIT_FAILED": {
		Code:       "SCHEDULER_SUBMIT_FAILED",
		MessageKey: "errors.scheduler.submitFailed",
		Message:    "scheduler submit failed",
		Severity:   "critical",
		Category:   "system",
		Retryable:  true,
	},
	"SCHEDULER_STOP_FAILED": {
		Code:       "SCHEDULER_STOP_FAILED",
		MessageKey: "errors.scheduler.stopFailed",
		Message:    "scheduler stop failed",
		Severity:   "critical",
		Category:   "system",
		Retryable:  true,
	},
	"UPSTREAM_UNAVAILABLE": {
		Code:       "UPSTREAM_UNAVAILABLE",
		MessageKey: "errors.upstream.unavailable",
		Message:    "upstream dependency is unavailable",
		Severity:   "critical",
		Category:   "system",
		Retryable:  true,
	},
	"SYSTEM_INTERNAL_ERROR": {
		Code:       "SYSTEM_INTERNAL_ERROR",
		MessageKey: "errors.system.internal",
		Message:    "internal server error",
		Severity:   "critical",
		Category:   "system",
		Retryable:  true,
	},
	"REQUEST_FAILED": {
		Code:       "REQUEST_FAILED",
		MessageKey: "errors.request.failed",
		Message:    "request failed",
		Severity:   "error",
		Category:   "system",
	},
}

func errorSpecFor(code string, status int) errorSpec {
	if spec, ok := errorSpecs[code]; ok {
		return spec
	}
	fallbackCode := code
	if fallbackCode == "" {
		fallbackCode = defaultErrorCode(status)
	}
	spec := errorSpecs[fallbackCode]
	if spec.Code == "" {
		spec = errorSpecs["REQUEST_FAILED"]
	}
	spec.Code = fallbackCode
	spec.Severity = firstNonEmpty(spec.Severity, errorSeverity(status))
	spec.Category = firstNonEmpty(spec.Category, errorCategory(status, fallbackCode))
	spec.Retryable = spec.Retryable || errorRetryable(status)
	return spec
}

var errorDetailKeys = map[string]struct {
	TitleKey   string
	MessageKey string
}{
	"preview.succeeded":                {"release.check.previewSucceeded.title", "release.check.previewSucceeded.reason"},
	"preview.channel":                  {"release.check.previewChannel.title", "release.check.previewChannel.reason"},
	"preview.artifact":                 {"release.check.previewArtifact.title", "release.check.previewArtifact.reason"},
	"preview.build-lock":               {"release.check.previewBuildLock.title", "release.check.previewBuildLock.reason"},
	"preview.lock-identity":            {"release.check.previewLockIdentity.title", "release.check.previewLockIdentity.reason"},
	"preview.required-components":      {"release.check.previewRequiredComponents.title", "release.check.previewRequiredComponents.reason"},
	"release.preview-validated":        {"release.check.previewValidated.title", "release.check.previewValidated.reason"},
	"release.fixed-version":            {"release.check.fixedVersion.title", "release.check.fixedVersion.reason"},
	"release.exact-component-uris":     {"release.check.exactComponentUris.title", "release.check.exactComponentUris.reason"},
	"release.fixed-component-versions": {"release.check.fixedComponentVersions.title", "release.check.fixedComponentVersions.reason"},
	"release.artifact":                 {"release.check.releaseArtifact.title", "release.check.releaseArtifact.reason"},
	"release.no-build-error":           {"release.check.noBuildError.title", "release.check.noBuildError.reason"},
}

func enrichErrorDetails(details []domain.APIErrorDetail) []domain.APIErrorDetail {
	for index := range details {
		if details[index].Code == "" {
			continue
		}
		keys, ok := errorDetailKeys[details[index].Code]
		if !ok {
			continue
		}
		if details[index].TitleKey == "" {
			details[index].TitleKey = keys.TitleKey
		}
		if details[index].MessageKey == "" {
			details[index].MessageKey = keys.MessageKey
		}
	}
	return details
}

func appErrorStatus(err *apperr.AppError, fallback int) int {
	switch err.Kind {
	case apperr.KindValidation:
		return http.StatusBadRequest
	case apperr.KindNotFound:
		return http.StatusNotFound
	case apperr.KindConflict:
		return http.StatusConflict
	case apperr.KindBusiness:
		return http.StatusUnprocessableEntity
	case apperr.KindResource:
		if fallback == http.StatusGone {
			return http.StatusGone
		}
		return http.StatusNotFound
	case apperr.KindDependency:
		return http.StatusBadGateway
	case apperr.KindSystem:
		return http.StatusInternalServerError
	default:
		return fallback
	}
}

func appErrorDetails(err *apperr.AppError) []domain.APIErrorDetail {
	if len(err.Details) > 0 {
		return err.Details
	}
	if err.Field == "" && len(err.Params) == 0 {
		return nil
	}
	return []domain.APIErrorDetail{{
		Code:    err.Code,
		Field:   err.Field,
		Message: err.Message,
		Params:  err.Params,
	}}
}

func releaseGateError(code string, message string, check domain.ReleaseCheckResult, stage string) error {
	details := []domain.APIErrorDetail{}
	for _, item := range check.Checks {
		if item.Status != "blocked" || (stage != "" && item.Stage != stage) {
			continue
		}
		details = append(details, domain.APIErrorDetail{
			Code:    item.ID,
			Stage:   item.Stage,
			Title:   item.Title,
			Message: item.Reason,
		})
	}
	return apperr.Business(code, message).WithDetails(details)
}

func buildStateConflict(message string) error {
	return apperr.Conflict("BUILD_STATE_CONFLICT", message)
}

func errorDetails(messages []string) []domain.APIErrorDetail {
	details := make([]domain.APIErrorDetail, 0, len(messages))
	for _, message := range messages {
		details = append(details, domain.APIErrorDetail{Message: message})
	}
	return details
}

func defaultErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "VALIDATION_INVALID_REQUEST"
	case http.StatusNotFound:
		return "RESOURCE_NOT_FOUND"
	case http.StatusConflict:
		return "BUILD_STATE_CONFLICT"
	case http.StatusGone:
		return "ARTIFACT_EXPIRED"
	case http.StatusUnprocessableEntity:
		return "BUSINESS_RULE_FAILED"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "UPSTREAM_UNAVAILABLE"
	default:
		if status >= 500 {
			return "SYSTEM_INTERNAL_ERROR"
		}
		return "REQUEST_FAILED"
	}
}

func errorSeverity(status int) string {
	if status == http.StatusConflict || status == http.StatusUnprocessableEntity {
		return "warning"
	}
	if status >= 500 {
		return "critical"
	}
	return "error"
}

func errorCategory(status int, code string) string {
	if strings.HasPrefix(code, "BUILD_") {
		return "business"
	}
	if status >= 500 || status == http.StatusBadGateway || status == http.StatusServiceUnavailable {
		return "system"
	}
	return "validation"
}

func errorRetryable(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout ||
		status >= 500
}

func errorUserAction(code string) string {
	switch code {
	case "BUILD_RELEASE_GATE_BLOCKED":
		return "请先修复阻塞项后再发布"
	case "BUILD_PREVIEW_NOT_READY":
		return "请先完成 Preview 构建并确认制品可下载"
	case "MANIFEST_SCHEMA_INVALID":
		return "请修复 Manifest 校验错误后再保存"
	case "BUILD_CREATE_INVALID_RELEASE_CHANNEL":
		return "请先创建 Preview 构建，验证通过后再转 Release"
	default:
		return ""
	}
}

func applyUploadMetadata(record domain.UploadRecord, r *http.Request) domain.UploadRecord {
	record.DownloadPolicy = normalizedDownloadPolicy(firstNonEmpty(r.FormValue("downloadPolicy"), record.DownloadPolicy, "platform"))
	record.RetentionDays = parseRetentionDays(r.FormValue("retentionDays"), record.RetentionDays)
	if record.RetentionDays == 0 {
		record.ExpiresAt = nil
	} else {
		expiresAt := record.CreatedAt.Add(time.Duration(record.RetentionDays) * 24 * time.Hour)
		record.ExpiresAt = &expiresAt
	}
	record.RetentionStatus = uploadRetentionStatus(record, time.Now().UTC())
	if record.Metadata == nil {
		record.Metadata = map[string]string{}
	}
	for key, values := range r.MultipartForm.Value {
		if !strings.HasPrefix(key, "metadata.") || len(values) == 0 {
			continue
		}
		metadataKey := strings.TrimPrefix(key, "metadata.")
		if metadataKey == "" {
			continue
		}
		record.Metadata[metadataKey] = values[0]
	}
	return record
}

func normalizeUploadRecord(record domain.UploadRecord, now time.Time) domain.UploadRecord {
	record.DownloadPolicy = normalizedDownloadPolicy(firstNonEmpty(record.DownloadPolicy, "platform"))
	if record.RetentionDays < 0 {
		record.RetentionDays = 30
	}
	record.RetentionStatus = uploadRetentionStatus(record, now)
	return record
}

func normalizedDownloadPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "public", "platform", "restricted":
		return strings.ToLower(strings.TrimSpace(policy))
	default:
		return "platform"
	}
}

func parseRetentionDays(value string, fallback int) int {
	if strings.TrimSpace(value) == "" {
		if fallback >= 0 {
			return fallback
		}
		return 30
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < 0 {
		return 30
	}
	if days > 3650 {
		return 3650
	}
	return days
}

func uploadRetentionStatus(record domain.UploadRecord, now time.Time) string {
	if record.RetentionDays == 0 {
		return "permanent"
	}
	if record.ExpiresAt != nil && now.After(*record.ExpiresAt) {
		return "expired"
	}
	return "active"
}

func runnerStatuses(builds []domain.Build, now time.Time) []domain.RunnerStatus {
	runners := map[string]*domain.RunnerStatus{}
	for _, build := range builds {
		if build.RunnerID != "" {
			status := runnerStatusFor(runners, build.RunnerID)
			mergeRunnerBuild(status, build)
		}
		for _, log := range logsWithSequence(build.Logs) {
			if log.Source == "" || log.Source == "system" || log.Source == "resolver" {
				continue
			}
			status := runnerStatusFor(runners, log.Source)
			if status.Target == "" {
				status.Target = build.Target
			}
			if status.LastLogAt == nil || log.Time.After(*status.LastLogAt) {
				logTime := log.Time
				status.LastLogAt = &logTime
			}
			if strings.HasPrefix(log.Message, "heartbeat") {
				logTime := log.Time
				if status.LastHeartbeatAt == nil || logTime.After(*status.LastHeartbeatAt) {
					status.LastHeartbeatAt = &logTime
				}
			}
			if strings.Contains(log.Message, "claimed by runner") {
				logTime := log.Time
				if status.LastClaimAt == nil || logTime.After(*status.LastClaimAt) {
					status.LastClaimAt = &logTime
				}
			}
			status.RecentMessages = append(status.RecentMessages, log)
		}
	}

	result := make([]domain.RunnerStatus, 0, len(runners))
	for _, status := range runners {
		sort.Slice(status.RecentMessages, func(i, j int) bool {
			return status.RecentMessages[i].Time.After(status.RecentMessages[j].Time)
		})
		if len(status.RecentMessages) > 5 {
			status.RecentMessages = status.RecentMessages[:5]
		}
		status.Status = runnerAvailability(*status, now)
		result = append(result, *status)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].RunnerID < result[j].RunnerID
	})
	return result
}

func runnerStatusFor(runners map[string]*domain.RunnerStatus, runnerID string) *domain.RunnerStatus {
	status, ok := runners[runnerID]
	if ok {
		return status
	}
	status = &domain.RunnerStatus{RunnerID: runnerID, Status: "unknown"}
	runners[runnerID] = status
	return status
}

func mergeRunnerBuild(status *domain.RunnerStatus, build domain.Build) {
	if status.Target == "" {
		status.Target = build.Target
	}
	switch build.Status {
	case domain.BuildRunning:
		status.RunningBuilds++
		status.CurrentBuildID = build.ID
		status.CurrentBuildState = build.Status
	case domain.BuildSucceeded, domain.BuildPreviewValidated, domain.BuildPublished:
		status.CompletedBuilds++
	case domain.BuildFailed:
		status.FailedBuilds++
	}
}

func runnerAvailability(status domain.RunnerStatus, now time.Time) string {
	last := status.LastHeartbeatAt
	if last == nil {
		last = status.LastLogAt
	}
	if last == nil {
		if status.RunningBuilds > 0 {
			return "unknown"
		}
		return "idle"
	}
	if now.Sub(*last) > 5*time.Minute {
		if status.RunningBuilds > 0 {
			return "stale"
		}
		return "offline"
	}
	if status.RunningBuilds > 0 {
		return "busy"
	}
	return "online"
}

func logsWithSequence(logs []domain.BuildLog) []domain.BuildLog {
	next := make([]domain.BuildLog, len(logs))
	for index, log := range logs {
		next[index] = log
		if next[index].Sequence == 0 {
			next[index].Sequence = index + 1
		}
	}
	return next
}

func appendBuildLog(logs []domain.BuildLog, source string, message string) []domain.BuildLog {
	entry := logEntry(source, message)
	entry.Sequence = len(logs) + 1
	return append(logs, entry)
}

func requestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		requestID := strings.TrimSpace(r.Header.Get("X-Request-Id"))
		if requestID == "" {
			requestID = "req_" + randomID()
		}
		correlationID := strings.TrimSpace(r.Header.Get("X-Correlation-Id"))
		if correlationID == "" {
			correlationID = requestID
			if !strings.HasPrefix(correlationID, "corr_") {
				correlationID = "corr_" + strings.TrimPrefix(requestID, "req_")
			}
		}
		w.Header().Set("X-Request-Id", requestID)
		w.Header().Set("X-Correlation-Id", correlationID)
		recorder := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		slog.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration_ms", time.Since(startedAt).Milliseconds(),
			"request_id", requestID,
			"correlation_id", correlationID,
		)
	})
}

type statusResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,X-Request-Id,X-Correlation-Id")
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

func withReleaseCheckSummaries(builds []domain.Build) []domain.Build {
	for index := range builds {
		builds[index] = withReleaseCheckSummary(builds[index])
	}
	return builds
}

func withReleaseCheckSummary(build domain.Build) domain.Build {
	summary := releaseCheckSummary(buildReleaseCheck(build))
	build.ReleaseCheckSummary = &summary
	return build
}

func releaseCheckSummary(check domain.ReleaseCheckResult) domain.ReleaseCheckSummary {
	summary := domain.ReleaseCheckSummary{
		BuildID:       check.BuildID,
		Stage:         check.Stage,
		PreviewReady:  check.PreviewReady,
		ReleaseReady:  check.ReleaseReady,
		BlockingCount: check.BlockingCount,
	}
	for _, item := range check.Checks {
		if item.Status != "blocked" {
			continue
		}
		summary.FirstBlockedID = item.ID
		summary.FirstBlockedStage = item.Stage
		summary.FirstBlockedTitle = item.Title
		summary.FirstBlockedReason = item.Reason
		break
	}
	return summary
}

func releaseCheckItem(id string, stage string, title string, ok bool, reason string) domain.ReleaseCheckItem {
	item := domain.ReleaseCheckItem{ID: id, Stage: stage, Title: title, Status: "pass"}
	if !ok {
		item.Status = "blocked"
		item.Reason = reason
	}
	return item
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

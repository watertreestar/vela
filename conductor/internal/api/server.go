package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"vela/conductor/internal/apperr"
	"vela/conductor/internal/domain"
	manifestvalidator "vela/conductor/internal/manifest"
	"vela/conductor/internal/nomad"
	"vela/conductor/internal/resolver"
	"vela/conductor/internal/secret"
	"vela/conductor/internal/store"
)

type Server struct {
	store                  store.Store
	scheduler              nomad.Scheduler
	artifacts              artifactStore
	auth                   AuthConfig
	auditKey               string
	auditKeyID             string
	executionHMACKey       string
	executionCapabilityTTL time.Duration
}

type resolveDependencies struct {
	store     store.Store
	artifacts artifactResolver
}

type artifactResolver interface {
	ResolveArtifactSource(source domain.ComponentSource, version string) (domain.ArtifactResolution, bool, error)
}

type artifactStore interface {
	artifactResolver
	SaveUpload(file multipart.File, header *multipart.FileHeader) (domain.UploadRecord, error)
	ServeUpload(w http.ResponseWriter, r *http.Request, id string) error
	DeleteUpload(id string) error
	AuditArchiveURI(digest string) (string, error)
	AuditArchiveStoragePolicy(now time.Time) domain.AuditArchiveStoragePolicy
	SaveAuditArchive(digest string, payload []byte, policy domain.AuditArchiveStoragePolicy) (string, error)
	ReleaseEvidenceArchiveURI(digest string) (string, error)
	SaveReleaseEvidenceArchive(digest string, payload []byte, policy domain.AuditArchiveStoragePolicy) (string, error)
}

type ServerOptions struct {
	Auth                   AuthConfig
	AuditArchiveHMACKey    string
	AuditArchiveHMACKeyID  string
	RunnerExecutionHMACKey string
	RunnerCapabilityTTL    time.Duration
}

func NewServer(store store.Store, scheduler nomad.Scheduler, artifacts artifactStore) Server {
	return Server{store: store, scheduler: scheduler, artifacts: artifacts}
}

func NewServerWithAuth(store store.Store, scheduler nomad.Scheduler, artifacts artifactStore, auth AuthConfig) Server {
	return NewServerWithOptions(store, scheduler, artifacts, ServerOptions{
		Auth:                   auth,
		RunnerExecutionHMACKey: "test-runner-execution-key",
		RunnerCapabilityTTL:    15 * time.Minute,
	})
}

func NewServerWithOptions(store store.Store, scheduler nomad.Scheduler, artifacts artifactStore, options ServerOptions) Server {
	return Server{
		store:                  store,
		scheduler:              scheduler,
		artifacts:              artifacts,
		auth:                   options.Auth,
		auditKey:               options.AuditArchiveHMACKey,
		auditKeyID:             options.AuditArchiveHMACKeyID,
		executionHMACKey:       options.RunnerExecutionHMACKey,
		executionCapabilityTTL: options.RunnerCapabilityTTL,
	}
}

func (s Server) resolveDependencies() resolveDependencies {
	return resolveDependencies{store: s.store, artifacts: s.artifacts}
}

func (d resolveDependencies) ReadOverlay(productID string, kind string, name string) (map[string]any, error) {
	return d.store.ReadOverlay(productID, kind, name)
}

func (d resolveDependencies) ReadUpload(id string) (domain.UploadRecord, error) {
	return d.store.ReadUpload(id)
}

func (d resolveDependencies) ResolveArtifactSource(source domain.ComponentSource, version string) (domain.ArtifactResolution, bool, error) {
	if d.artifacts == nil {
		return domain.ArtifactResolution{}, false, nil
	}
	return d.artifacts.ResolveArtifactSource(source, version)
}

func (s Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("POST /api/auth/oidc/token", s.oidcTokenBroker)
	mux.HandleFunc("POST /api/auth/logout", s.withAuth(s.logout, roleViewer, roleOperator, roleAdmin))
	mux.HandleFunc("GET /metrics", s.withAuth(s.metrics, roleViewer, roleOperator, roleAdmin))
	mux.HandleFunc("GET /api/products", s.listProducts)
	mux.HandleFunc("GET /api/products/{id}", s.getProduct)
	mux.HandleFunc("GET /api/products/{id}/manifest", s.getProductManifest)
	mux.HandleFunc("PUT /api/products/{id}/manifest", s.withAuth(s.updateProductManifest, roleOperator, roleAdmin))
	mux.HandleFunc("POST /api/products/{id}/manifest/dry-run", s.dryRunProductManifest)
	mux.HandleFunc("GET /api/products/{id}/manifest/revisions", s.listProductManifestRevisions)
	mux.HandleFunc("POST /api/products/{id}/manifest/revisions/{revision}/restore", s.withAuth(s.restoreProductManifestRevision, roleOperator, roleAdmin))
	mux.HandleFunc("GET /api/products/{id}/overlays/{kind}", s.listProductOverlays)
	mux.HandleFunc("GET /api/products/{id}/overlays/{kind}/{name}", s.getProductOverlay)
	mux.HandleFunc("PUT /api/products/{id}/overlays/{kind}/{name}", s.withAuth(s.updateProductOverlay, roleOperator, roleAdmin))
	mux.HandleFunc("GET /api/products/{id}/versions", s.getProductVersions)
	mux.HandleFunc("GET /api/builds", s.listBuilds)
	mux.HandleFunc("POST /api/builds", s.withAuth(s.createBuild, roleOperator, roleAdmin))
	mux.HandleFunc("GET /api/builds/{id}", s.getBuild)
	mux.HandleFunc("GET /api/builds/{id}/lock", s.getBuildLock)
	mux.HandleFunc("GET /api/builds/{id}/release-check", s.getBuildReleaseCheck)
	mux.HandleFunc("GET /api/builds/{id}/reproducibility-report", s.getBuildReproducibilityReport)
	mux.HandleFunc("GET /api/builds/{id}/runner-e2e-evidence", s.withAuth(s.getRunnerE2EEvidence, roleOperator, roleAdmin))
	mux.HandleFunc("GET /api/builds/{id}/release-evidence/archive", s.withAuth(s.exportBuildReleaseEvidenceArchive, roleViewer, roleOperator, roleAdmin))
	mux.HandleFunc("GET /api/builds/{id}/download", s.downloadBuild)
	mux.HandleFunc("POST /api/builds/{id}/approve", s.withAuth(s.approveBuild, roleOperator, roleAdmin))
	mux.HandleFunc("POST /api/builds/{id}/reject", s.withAuth(s.rejectBuild, roleOperator, roleAdmin))
	mux.HandleFunc("POST /api/builds/{id}/validate-preview", s.withAuth(s.validatePreviewBuild, roleOperator, roleAdmin))
	mux.HandleFunc("POST /api/builds/{id}/publish", s.withAuth(s.publishBuild, roleAdmin))
	mux.HandleFunc("POST /api/builds/{id}/retry", s.withAuth(s.retryBuild, roleOperator, roleAdmin))
	mux.HandleFunc("POST /api/builds/{id}/cancel", s.withAuth(s.cancelBuild, roleOperator, roleAdmin))
	mux.HandleFunc("GET /api/builds/{id}/logs", s.getBuildLogs)
	mux.HandleFunc("GET /api/builds/{id}/logs/stream", s.streamBuildLogs)
	mux.HandleFunc("POST /api/builds/{id}/logs", s.withAuth(s.appendBuildLog, roleRunner, roleAdmin))
	mux.HandleFunc("POST /api/runner/tasks/claim", s.withAuth(s.claimRunnerTask, roleRunner, roleAdmin)) // auth-disabled compatibility only
	mux.HandleFunc("POST /api/runner/tasks/{id}/claim", s.withAuth(s.claimRunnerTask, roleRunner, roleAdmin))
	mux.HandleFunc("POST /api/runner/tasks/{id}/complete", s.withAuth(s.completeRunnerTask, roleRunner, roleAdmin))
	mux.HandleFunc("POST /api/runner/tasks/{id}/artifacts/final", s.withAuth(s.uploadRunnerFinal, roleRunner, roleAdmin))
	mux.HandleFunc("POST /api/runner/tasks/{id}/artifacts/checksum", s.withAuth(s.uploadRunnerChecksum, roleRunner, roleAdmin))
	mux.HandleFunc("POST /api/runner/tasks/{id}/heartbeat", s.withAuth(s.runnerHeartbeat, roleRunner, roleAdmin))
	mux.HandleFunc("GET /api/runner/tasks/{id}/inputs/{uploadId}/download", s.withAuth(s.downloadRunnerInput, roleRunner, roleAdmin))
	mux.HandleFunc("POST /api/runner/heartbeat", s.withAuth(s.runnerHeartbeat, roleRunner, roleAdmin)) // auth-disabled compatibility only
	mux.HandleFunc("GET /api/runners", s.listRunners)
	mux.HandleFunc("POST /api/uploads", s.withAuth(s.uploadArtifact, roleOperator, roleAdmin))
	mux.HandleFunc("GET /api/uploads", s.withAuth(s.listUploads, roleViewer, roleOperator, roleAdmin))
	mux.HandleFunc("GET /api/uploads/{id}/download", s.downloadUpload)
	mux.HandleFunc("GET /api/audit-events", s.withAuth(s.listAuditEvents, roleViewer, roleOperator, roleAdmin))
	mux.HandleFunc("GET /api/audit-events/archive", s.withAuth(s.exportAuditArchive, roleViewer, roleOperator, roleAdmin))
	mux.HandleFunc("GET /api/notifications", s.withAuth(s.listNotifications, roleViewer, roleOperator, roleAdmin))
	mux.HandleFunc("POST /api/notifications/{id}/read", s.withAuth(s.markNotificationRead, roleViewer, roleOperator, roleAdmin))
	mux.HandleFunc("POST /api/notifications/read-all", s.withAuth(s.markAllNotificationsRead, roleViewer, roleOperator, roleAdmin))
	return requestContext(cors(mux))
}

func (s Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s Server) metrics(w http.ResponseWriter, r *http.Request) {
	builds, err := s.store.ListBuilds()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	uploads, err := s.store.ListUploads()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	body := buildMetrics(builds, uploads, time.Now().UTC())
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
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
	s.recordAudit(r, domain.AuditEvent{
		Action:       "manifest.update",
		ResourceType: "product",
		ResourceID:   product.ID,
		ProductID:    product.ID,
		Status:       "success",
		Summary:      "product manifest updated",
	})
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
	config, components, lock, err := resolver.ResolveBuild(product, buildRequest, "dry-run", s.resolveDependencies())
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
	s.recordAudit(r, domain.AuditEvent{
		Action:       "manifest.restore",
		ResourceType: "product",
		ResourceID:   product.ID,
		ProductID:    product.ID,
		Status:       "success",
		Summary:      "product manifest revision restored",
		Metadata: map[string]string{
			"revision": r.PathValue("revision"),
		},
	})
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
	s.recordAudit(r, domain.AuditEvent{
		Action:       "overlay.update",
		ResourceType: "overlay",
		ResourceID:   r.PathValue("id") + "/" + record.Kind + "/" + record.Name,
		ProductID:    r.PathValue("id"),
		Status:       "success",
		Summary:      "product overlay updated",
		Metadata: map[string]string{
			"kind": record.Kind,
			"name": record.Name,
		},
	})
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
	var linkedUpload domain.UploadRecord
	if uploadID := build.ArtifactUploadID; uploadID != "" {
		linkedUpload, err = s.store.ReadUpload(uploadID)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		now := time.Now().UTC()
		linkedUpload = normalizeUploadRecord(linkedUpload, now)
		if uploadRetentionStatus(linkedUpload, now) == "expired" {
			writeAPIError(w, http.StatusGone, "ARTIFACT_EXPIRED", "", nil)
			return
		}
		if !s.authorizeUploadDownload(w, r, linkedUpload) {
			return
		}
	} else if s.auth.Enabled {
		if _, ok := s.requireAuth(w, r, roleViewer, roleOperator, roleAdmin); !ok {
			return
		}
	}
	if build.DownloadURL != "" {
		s.recordBuildDownloadAudit(r, build, linkedUpload)
		http.Redirect(w, r, build.DownloadURL, http.StatusFound)
		return
	}
	if build.ArtifactURI == "" {
		writeAPIError(w, http.StatusNotFound, "BUILD_ARTIFACT_NOT_AVAILABLE", "", nil)
		return
	}
	s.recordBuildDownloadAudit(r, build, linkedUpload)
	writeJSON(w, http.StatusOK, map[string]string{"artifactUri": build.ArtifactURI})
}

func (s Server) recordBuildDownloadAudit(r *http.Request, build domain.Build, linkedUpload domain.UploadRecord) {
	metadata := map[string]string{
		"artifactUri":  build.ArtifactURI,
		"downloadUrl":  build.DownloadURL,
		"uploadPolicy": linkedUpload.DownloadPolicy,
		"retention":    linkedUpload.RetentionStatus,
	}
	s.recordAudit(r, domain.AuditEvent{
		Action:       "build.download",
		ResourceType: "build",
		ResourceID:   build.ID,
		BuildID:      build.ID,
		ProductID:    firstNonEmpty(build.ProductID, linkedUpload.Metadata["productId"]),
		UploadID:     linkedUpload.ID,
		Status:       "success",
		Summary:      "build artifact download requested",
		Metadata:     metadata,
	})
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
		Logs:           []domain.BuildLog{logEntryForRequest(r, "system", "build request created and waiting for approval")},
	}
	build.Logs[0].Time = now
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

	finalConfig, components, lock, err := resolver.ResolveBuild(product, request, build.ID, s.resolveDependencies())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	build.FinalConfig = finalConfig
	build.Components = components
	build.BuildLock = lock
	build.Workloads = lock.Workloads
	build.Logs = append(build.Logs, logEntryForRequest(r, "resolver", "final config and component lock generated"))

	if err := s.store.SaveBuild(build); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.recordAudit(r, domain.AuditEvent{
		Action:       "build.create",
		ResourceType: "build",
		ResourceID:   build.ID,
		BuildID:      build.ID,
		ProductID:    build.ProductID,
		Status:       "success",
		Summary:      "build request created",
		Metadata: map[string]string{
			"target":         build.Target,
			"profile":        build.Profile,
			"version":        build.Version,
			"releaseChannel": build.ReleaseChannel,
		},
	})

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

func (s Server) getBuildReproducibilityReport(w http.ResponseWriter, r *http.Request) {
	build, err := s.store.ReadBuild(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, s.reproducibilityReport(build, time.Now().UTC()))
}

// getRunnerE2EEvidence exposes the minimum, non-secret control-plane evidence
// required by the auth-enabled Runner live gate. It deliberately does not expose
// execution credentials, scheduler logs, or raw runner logs.
func (s Server) getRunnerE2EEvidence(w http.ResponseWriter, r *http.Request) {
	build, err := s.store.ReadBuild(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	final, checksum := domain.UploadRecord{}, domain.UploadRecord{}
	if build.ArtifactUploadID != "" {
		final, err = s.store.ReadUpload(build.ArtifactUploadID)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
	}
	if build.ChecksumUploadID != "" {
		checksum, err = s.store.ReadUpload(build.ChecksumUploadID)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, runnerE2EEvidence(build, final, checksum, s.auth.Enabled))
}

func (s Server) exportBuildReleaseEvidenceArchive(w http.ResponseWriter, r *http.Request) {
	build, err := s.store.ReadBuild(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	generatedAt := time.Now().UTC()
	report := s.reproducibilityReport(build, generatedAt)
	archive, err := buildReleaseEvidenceArchive(report, s.auditKey, s.auditKeyID, generatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if s.artifacts != nil {
		archiveURI, err := s.artifacts.ReleaseEvidenceArchiveURI(archive.Digest)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		storedAt := time.Now().UTC()
		policy := s.artifacts.AuditArchiveStoragePolicy(storedAt)
		archive.ArchiveURI = archiveURI
		archive.ArchiveStoredAt = &storedAt
		archive.ArchiveRetentionMode = policy.RetentionMode
		archive.ArchiveRetainUntil = policy.RetainUntil
		archive.ArchiveLegalHold = policy.LegalHold
		payload, err := json.Marshal(archive)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if _, err := s.artifacts.SaveReleaseEvidenceArchive(archive.Digest, payload, policy); err != nil {
			s.recordStorageNotification("release_evidence_archive", build.ID, build.ProductID, "Release 证据归档存储失败", "Release 可复现证据无法写入制品存储，需要检查本地磁盘或对象存储。", err)
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	s.recordAudit(r, domain.AuditEvent{
		Action:       "release.evidence.archive.export",
		ResourceType: "release_evidence",
		ResourceID:   archive.Digest,
		BuildID:      build.ID,
		ProductID:    build.ProductID,
		Status:       "success",
		Summary:      "release evidence archive exported",
		Metadata: map[string]string{
			"digest":          archive.Digest,
			"digestAlgorithm": archive.DigestAlgorithm,
			"archiveUri":      archive.ArchiveURI,
			"retentionMode":   archive.ArchiveRetentionMode,
			"legalHold":       archive.ArchiveLegalHold,
			"reproducible":    strconv.FormatBool(archive.Reproducible),
		},
	})
	w.Header().Set("Content-Disposition", `attachment; filename="packaging-release-evidence.json"`)
	writeJSON(w, http.StatusOK, archive)
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

	secretRefs := secret.SecretRefs(current)
	prepared, capability, err := s.prepareDispatch(id, current.Status)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	jobID, schedulerLog, err := s.scheduler.Submit(prepared, product, capability)
	if err != nil {
		s.recordSecretResolutionAudit(r, current, secretRefs, "approve", "failed")
		failedBuild, _ := s.failDispatch(id, prepared, schedulerLog, err, "approval failed")
		if failedBuild.ID != "" {
			s.recordBuildNotification("build.scheduler_failed", "critical", "构建调度失败",
				buildNotificationMessage(failedBuild, "审批通过后提交 Runner 调度失败，需要检查调度器或 Runner 配置。"),
				failedBuild, map[string]string{"error": err.Error()})
		}
		writeAPIErrorWithCause(w, http.StatusBadGateway, "SCHEDULER_SUBMIT_FAILED", "", []domain.APIErrorDetail{
			{Message: "scheduler failed to submit build"},
		}, err)
		return
	}
	s.recordSecretResolutionAudit(r, current, secretRefs, "approve", "success")

	now := time.Now().UTC()
	updated, err := s.store.UpdateBuild(id, func(build *domain.Build) error {
		if build.Status != domain.BuildDispatching || build.Attempt != prepared.Attempt || build.ExecutionJTI != prepared.ExecutionJTI {
			return buildStateConflict("dispatch was superseded")
		}
		build.Status = domain.BuildQueued
		build.NomadJobID = jobID
		build.SchedulerLog = schedulerLog
		build.ApprovedAt = &now
		build.UpdatedAt = now
		build.Logs = append(build.Logs, logEntryForRequest(r, "system", "approved and queued for runner"))
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.recordAudit(r, domain.AuditEvent{
		Action:       "build.approve",
		ResourceType: "build",
		ResourceID:   updated.ID,
		BuildID:      updated.ID,
		ProductID:    updated.ProductID,
		Status:       "success",
		Summary:      "build approved and queued",
		Metadata: map[string]string{
			"nomadJobId": updated.NomadJobID,
			"target":     updated.Target,
		},
	})
	writeJSON(w, http.StatusOK, withReleaseCheckSummary(updated))
}

func (s Server) rejectBuild(w http.ResponseWriter, r *http.Request) {
	updated, err := s.store.UpdateBuild(r.PathValue("id"), func(build *domain.Build) error {
		if build.Status != domain.BuildPendingApproval {
			return buildStateConflict("only pending builds can be rejected")
		}
		build.Status = domain.BuildRejected
		build.UpdatedAt = time.Now().UTC()
		build.Logs = append(build.Logs, logEntryForRequest(r, "system", "build rejected"))
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.recordAudit(r, domain.AuditEvent{
		Action:       "build.reject",
		ResourceType: "build",
		ResourceID:   updated.ID,
		BuildID:      updated.ID,
		ProductID:    updated.ProductID,
		Status:       "success",
		Summary:      "build rejected",
	})
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
		build.Logs = append(build.Logs, logEntryForRequest(r, "system", "preview artifact validated"))
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.recordAudit(r, domain.AuditEvent{
		Action:       "build.validate_preview",
		ResourceType: "build",
		ResourceID:   updated.ID,
		BuildID:      updated.ID,
		ProductID:    updated.ProductID,
		Status:       "success",
		Summary:      "preview artifact validated",
	})
	s.recordBuildNotification("build.preview_validated", "info", "Preview 已验证",
		buildNotificationMessage(updated, "该构建已通过 Preview 验证，可以进入 Release 门禁检查。"),
		updated, nil)
	writeJSON(w, http.StatusOK, withReleaseCheckSummary(updated))
}

func (s Server) publishBuild(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil && r.ContentLength != 0 {
		var request struct {
			ArtifactURI string `json:"artifactUri"`
			DownloadURL string `json:"downloadUrl"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "VALIDATION_INVALID_JSON", "", nil)
			return
		}
		if strings.TrimSpace(request.ArtifactURI) != "" || strings.TrimSpace(request.DownloadURL) != "" {
			writeAPIError(w, http.StatusBadRequest, "VALIDATION_INVALID_REQUEST", "published artifact is immutable", nil)
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
		build.Status = domain.BuildPublished
		build.ReleaseChannel = "release"
		build.PublishedAt = &now
		build.UpdatedAt = now
		build.Logs = append(build.Logs, logEntryForRequest(r, "system", "validated preview promoted to release"))
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.recordAudit(r, domain.AuditEvent{
		Action:       "build.publish",
		ResourceType: "build",
		ResourceID:   updated.ID,
		BuildID:      updated.ID,
		ProductID:    updated.ProductID,
		Status:       "success",
		Summary:      "validated preview promoted to release",
		Metadata: map[string]string{
			"artifactUri": updated.ArtifactURI,
			"downloadUrl": updated.DownloadURL,
		},
	})
	s.recordBuildNotification("build.published", "success", "Release 已发布",
		buildNotificationMessage(updated, "验证通过的 Preview 已提升为 Release。"),
		updated, map[string]string{"artifactUri": updated.ArtifactURI, "downloadUrl": updated.DownloadURL})
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
	secretRefs := secret.SecretRefs(current)
	prepared, capability, err := s.prepareDispatch(id, current.Status)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	jobID, schedulerLog, err := s.scheduler.Submit(prepared, product, capability)
	if err != nil {
		s.recordSecretResolutionAudit(r, current, secretRefs, "retry", "failed")
		_, _ = s.failDispatch(id, prepared, schedulerLog, err, "retry submission failed")
		writeAPIErrorWithCause(w, http.StatusBadGateway, "SCHEDULER_SUBMIT_FAILED", "", []domain.APIErrorDetail{
			{Message: "scheduler failed to submit build"},
		}, err)
		return
	}
	s.recordSecretResolutionAudit(r, current, secretRefs, "retry", "success")
	updated, err := s.store.UpdateBuild(id, func(build *domain.Build) error {
		if build.Status != domain.BuildDispatching || build.Attempt != prepared.Attempt || build.ExecutionJTI != prepared.ExecutionJTI {
			return buildStateConflict("dispatch was superseded")
		}
		build.Status = domain.BuildQueued
		build.Error = ""
		build.NomadJobID = jobID
		build.SchedulerLog = schedulerLog
		build.UpdatedAt = time.Now().UTC()
		build.Logs = append(build.Logs, logEntryForRequest(r, "system", "build queued for retry"))
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.recordAudit(r, domain.AuditEvent{
		Action:       "build.retry",
		ResourceType: "build",
		ResourceID:   updated.ID,
		BuildID:      updated.ID,
		ProductID:    updated.ProductID,
		Status:       "success",
		Summary:      "build queued for retry",
	})
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
	now := time.Now().UTC()
	updated, err := s.store.UpdateBuild(r.PathValue("id"), func(build *domain.Build) error {
		switch build.Status {
		case domain.BuildDispatching, domain.BuildQueued, domain.BuildRunning, domain.BuildPendingApproval:
			build.Status = domain.BuildCanceled
			build.CanceledAt = &now
			build.ExecutionJTI = ""
			build.ExecutionExpiresAt = nil
			build.LastHeartbeatAt = nil
			build.UpdatedAt = now
			build.Logs = appendBuildLogForRequest(build.Logs, r, "system", "build canceled")
			return nil
		default:
			return buildStateConflict("only pending, dispatching, queued, or running builds can be canceled")
		}
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.recordAudit(r, domain.AuditEvent{
		Action:       "build.cancel",
		ResourceType: "build",
		ResourceID:   updated.ID,
		BuildID:      updated.ID,
		ProductID:    updated.ProductID,
		Status:       "success",
		Summary:      "build canceled",
	})
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
	id := r.PathValue("id")
	claims := domain.ExecutionClaims{}
	if s.auth.Enabled {
		var err error
		claims, err = s.authorizeExecution(r, id, domain.BuildRunning)
		if err != nil {
			writeErrorText(w, http.StatusForbidden, err.Error())
			return
		}
	}
	var request domain.BuildLogRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.Source == "" {
		request.Source = "runner"
	}
	if s.auth.Enabled {
		request.Source = claims.RunnerID
	}
	updated, err := s.store.UpdateBuild(r.PathValue("id"), func(build *domain.Build) error {
		build.Logs = appendBuildLogForRequest(build.Logs, r, request.Source, request.Message)
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

func (s Server) prepareDispatch(id, expectedState string) (domain.Build, string, error) {
	if s.auth.Enabled && strings.TrimSpace(s.executionHMACKey) == "" {
		return domain.Build{}, "", errors.New("RUNNER_EXECUTION_HMAC_KEY is required when AUTH_ENABLED=true")
	}
	var prepared domain.Build
	now := time.Now().UTC()
	ttl := s.executionCapabilityTTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	jti, err := newExecutionJTI()
	if err != nil {
		return domain.Build{}, "", err
	}
	prepared, err = s.store.UpdateBuild(id, func(build *domain.Build) error {
		if build.Status != expectedState {
			return buildStateConflict("build is no longer dispatchable")
		}
		build.Attempt++
		build.Status = domain.BuildDispatching
		build.RunnerID = fmt.Sprintf("nomad-%s-a%d", build.ID, build.Attempt)
		build.NomadJobID = ""
		build.DispatchStartedAt = &now
		build.ExecutionJTI = jti
		expiry := now.Add(ttl)
		build.ExecutionExpiresAt = &expiry
		build.LastHeartbeatAt = nil
		build.UpdatedAt = now
		build.Logs = append(build.Logs, domain.BuildLog{Time: now, Source: "system", Message: "dispatch prepared"})
		return nil
	})
	if err != nil {
		return domain.Build{}, "", err
	}
	if !s.auth.Enabled {
		return prepared, "", nil
	}
	capability, err := issueExecutionCapability(s.executionHMACKey, domain.ExecutionClaims{BuildID: prepared.ID, Attempt: prepared.Attempt, Target: prepared.Target, RunnerID: prepared.RunnerID, JTI: prepared.ExecutionJTI, IssuedAt: now, ExpiresAt: *prepared.ExecutionExpiresAt})
	return prepared, capability, err
}

func (s Server) failDispatch(id string, prepared domain.Build, schedulerLog string, submitErr error, message string) (domain.Build, error) {
	return s.store.UpdateBuild(id, func(build *domain.Build) error {
		if build.Status != domain.BuildDispatching || build.Attempt != prepared.Attempt || build.ExecutionJTI != prepared.ExecutionJTI {
			return buildStateConflict("dispatch was superseded")
		}
		build.Status = domain.BuildFailed
		build.Error = submitErr.Error()
		build.SchedulerLog = schedulerLog
		build.NomadJobID = ""
		build.DispatchStartedAt = nil
		build.ExecutionJTI = ""
		build.ExecutionExpiresAt = nil
		build.LastHeartbeatAt = nil
		build.UpdatedAt = time.Now().UTC()
		build.Logs = append(build.Logs, domain.BuildLog{Time: build.UpdatedAt, Source: "system", Message: message + ": " + submitErr.Error()})
		return nil
	})
}

func (s Server) authorizeExecution(r *http.Request, id, requiredState string) (domain.ExecutionClaims, error) {
	if !s.auth.Enabled {
		return domain.ExecutionClaims{}, nil
	}
	claims, err := verifyExecutionCapability(s.executionHMACKey, r.Header.Get("X-Runner-Execution"), time.Now().UTC())
	if err != nil {
		return domain.ExecutionClaims{}, err
	}
	if claims.BuildID != id {
		return domain.ExecutionClaims{}, errors.New("execution capability build mismatch")
	}
	build, err := s.store.ReadBuild(id)
	if err != nil {
		return domain.ExecutionClaims{}, err
	}
	if build.Status != requiredState || build.Attempt != claims.Attempt || build.Target != claims.Target || build.RunnerID != claims.RunnerID || build.ExecutionJTI != claims.JTI || build.ExecutionExpiresAt == nil || build.ExecutionExpiresAt.Unix() != claims.ExpiresAt.Unix() {
		return domain.ExecutionClaims{}, errors.New("execution capability is fenced")
	}
	return claims, nil
}

func (s Server) claimRunnerTask(w http.ResponseWriter, r *http.Request) {
	var request domain.RunnerClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id := r.PathValue("id")
	if id == "" { // The target-wide endpoint remains only for auth-disabled local acceptance.
		if request.RunnerID == "" {
			writeErrorText(w, http.StatusBadRequest, "runnerId is required")
			return
		}
		if s.auth.Enabled {
			writeErrorText(w, http.StatusNotFound, "build-scoped claim is required")
			return
		}
		requestID, correlationID := requestIDs(r)
		build, ok, err := s.store.ClaimBuild(request.Target, request.RunnerID, requestID, correlationID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if !ok {
			writeJSON(w, http.StatusNoContent, nil)
			return
		}
		writeJSON(w, http.StatusOK, withReleaseCheckSummary(build))
		return
	}
	claims, err := s.authorizeExecution(r, id, domain.BuildQueued)
	if err != nil {
		writeErrorText(w, http.StatusForbidden, err.Error())
		return
	}
	updated, err := s.store.UpdateBuild(id, func(build *domain.Build) error {
		if build.RunnerID != claims.RunnerID || build.Status != domain.BuildQueued {
			return buildStateConflict("execution no longer claimable")
		}
		now := time.Now().UTC()
		build.Status = domain.BuildRunning
		build.LastHeartbeatAt = &now
		build.UpdatedAt = now
		build.Logs = append(build.Logs, logEntryForRequest(r, claims.RunnerID, "claimed by runner"))
		return nil
	})
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, withReleaseCheckSummary(updated))
}

func (s Server) uploadRunnerFinal(w http.ResponseWriter, r *http.Request) {
	s.uploadRunnerOutput(w, r, "final")
}

func (s Server) uploadRunnerChecksum(w http.ResponseWriter, r *http.Request) {
	s.uploadRunnerOutput(w, r, "checksum")
}

func (s Server) uploadRunnerOutput(w http.ResponseWriter, r *http.Request, kind string) {
	id := r.PathValue("id")
	claims, err := s.authorizeExecution(r, id, domain.BuildRunning)
	if err != nil {
		writeErrorText(w, http.StatusForbidden, err.Error())
		return
	}
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
	if kind == "checksum" && header.Size > 4096 {
		writeErrorText(w, http.StatusBadRequest, "checksum exceeds 4 KiB")
		return
	}
	if s.artifacts == nil {
		writeErrorText(w, http.StatusInternalServerError, "artifact store is not configured")
		return
	}
	build, err := s.store.ReadBuild(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if build.Status != domain.BuildRunning || build.Attempt != claims.Attempt {
		writeErrorText(w, http.StatusConflict, "execution no longer accepts output")
		return
	}
	if (kind == "final" && build.ArtifactUploadID != "") || (kind == "checksum" && build.ChecksumUploadID != "") {
		writeErrorText(w, http.StatusConflict, kind+" artifact already uploaded")
		return
	}
	var final domain.UploadRecord
	if kind == "checksum" {
		if build.ArtifactUploadID == "" {
			writeErrorText(w, http.StatusConflict, "final artifact must be uploaded first")
			return
		}
		final, err = s.store.ReadUpload(build.ArtifactUploadID)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
	}
	record, err := s.artifacts.SaveUpload(file, header)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	cleanup := func(cause error, recordSaved bool) {
		if recordSaved {
			if deleteErr := s.store.DeleteUploadRecord(record.ID); deleteErr != nil {
				s.recordAudit(r, domain.AuditEvent{Action: "runner.output.orphaned", ResourceType: "upload", ResourceID: record.ID, BuildID: id, UploadID: record.ID, Status: "failure", Summary: "runner output rejected but binding record cleanup failed", Metadata: map[string]string{"kind": kind, "cause": cause.Error(), "cleanupError": deleteErr.Error()}})
				return
			}
		}
		if deleteErr := s.artifacts.DeleteUpload(record.ID); deleteErr != nil {
			s.recordAudit(r, domain.AuditEvent{Action: "runner.output.orphaned", ResourceType: "upload", ResourceID: record.ID, BuildID: id, UploadID: record.ID, Status: "failure", Summary: "runner output rejected and object cleanup failed", Metadata: map[string]string{"kind": kind, "cause": cause.Error(), "cleanupError": deleteErr.Error()}})
			return
		}
		s.recordAudit(r, domain.AuditEvent{Action: "runner.output.rejected", ResourceType: "upload", ResourceID: record.ID, BuildID: id, UploadID: record.ID, Status: "failure", Summary: "runner output rejected and object removed", Metadata: map[string]string{"kind": kind, "cause": cause.Error()}})
	}
	record.BuildID, record.Attempt, record.RunnerID, record.Kind = id, claims.Attempt, claims.RunnerID, kind
	if kind == "checksum" {
		content, readErr := s.readArtifactUpload(record.ID)
		if readErr != nil || !validChecksum(content, final) {
			cause := errors.New("checksum must exactly match final artifact sha256 and filename")
			cleanup(cause, false)
			writeErrorText(w, http.StatusBadRequest, cause.Error())
			return
		}
		record.ParentUploadID = final.ID
	}
	if err := s.store.SaveUploadRecord(record); err != nil {
		cleanup(err, false)
		writeError(w, http.StatusConflict, err)
		return
	}
	_, err = s.store.UpdateBuild(id, func(build *domain.Build) error {
		if build.Status != domain.BuildRunning || build.Attempt != claims.Attempt {
			return buildStateConflict("execution no longer accepts output")
		}
		if kind == "final" {
			if build.ArtifactUploadID != "" {
				return buildStateConflict("final artifact already uploaded")
			}
			build.ArtifactUploadID = record.ID
		} else {
			if build.ChecksumUploadID != "" {
				return buildStateConflict("checksum already uploaded")
			}
			build.ChecksumUploadID = record.ID
		}
		build.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		cleanup(err, true)
		writeError(w, http.StatusConflict, err)
		return
	}
	s.recordAudit(r, domain.AuditEvent{Action: "runner.output.bind", ResourceType: "upload", ResourceID: record.ID, BuildID: id, UploadID: record.ID, Status: "success", Summary: "runner output bound to build attempt", Metadata: map[string]string{"kind": kind, "attempt": strconv.Itoa(claims.Attempt)}})
	writeJSON(w, http.StatusCreated, record)
}

func validChecksum(content []byte, final domain.UploadRecord) bool {
	line := strings.TrimSuffix(string(content), "\n")
	return line == final.SHA256+"  "+final.FileName
}

func (s Server) readArtifactUpload(id string) ([]byte, error) {
	if reader, ok := s.artifacts.(interface{ ReadUpload(string) ([]byte, error) }); ok {
		return reader.ReadUpload(id)
	}
	return nil, errors.New("artifact store does not support readback verification")
}

func (s Server) verifyRunnerOutputs(buildID string) error {
	build, err := s.store.ReadBuild(buildID)
	if err != nil {
		return err
	}
	if build.ArtifactUploadID == "" || build.ChecksumUploadID == "" {
		return errors.New("successful completion requires final artifact and checksum")
	}
	final, err := s.store.ReadUpload(build.ArtifactUploadID)
	if err != nil {
		return err
	}
	checksum, err := s.store.ReadUpload(build.ChecksumUploadID)
	if err != nil {
		return err
	}
	if final.BuildID != build.ID || final.Attempt != build.Attempt || final.RunnerID != build.RunnerID || final.Kind != "final" || checksum.BuildID != build.ID || checksum.Attempt != build.Attempt || checksum.RunnerID != build.RunnerID || checksum.Kind != "checksum" || checksum.ParentUploadID != final.ID {
		return errors.New("output ownership does not match current build attempt")
	}
	payload, err := s.readArtifactUpload(final.ID)
	if err != nil {
		return errors.New("cannot read final artifact from artifact store")
	}
	digest := sha256.Sum256(payload)
	if int64(len(payload)) != final.Size || !strings.EqualFold(hex.EncodeToString(digest[:]), final.SHA256) {
		return errors.New("final artifact readback integrity mismatch")
	}
	checksumPayload, err := s.readArtifactUpload(checksum.ID)
	if err != nil {
		return errors.New("cannot read checksum from artifact store")
	}
	if !validChecksum(checksumPayload, final) {
		return errors.New("checksum readback does not match final artifact")
	}
	return nil
}

func (s Server) completeRunnerTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.auth.Enabled {
		if _, err := s.authorizeExecution(r, id, domain.BuildRunning); err != nil {
			writeErrorText(w, http.StatusForbidden, err.Error())
			return
		}
	}
	var request domain.RunnerCompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status := domain.BuildSucceeded
	if request.Status == domain.BuildFailed {
		status = domain.BuildFailed
	}
	var verifiedAt time.Time
	var verifiedFinal domain.UploadRecord
	if status == domain.BuildSucceeded && s.auth.Enabled {
		if err := s.verifyRunnerOutputs(id); err != nil {
			writeErrorText(w, http.StatusConflict, err.Error())
			return
		}
		build, err := s.store.ReadBuild(id)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		verifiedFinal, err = s.store.ReadUpload(build.ArtifactUploadID)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		verifiedAt = time.Now().UTC()
	}

	updated, err := s.store.UpdateBuild(id, func(build *domain.Build) error {
		if build.Status != domain.BuildRunning {
			return buildStateConflict("only running builds can be completed")
		}
		build.Status = status
		build.Error = request.Error
		build.UpdatedAt = time.Now().UTC()
		if status == domain.BuildSucceeded {
			if s.auth.Enabled {
				// verifyRunnerOutputs and the final record were read before this store
				// update, avoiding nested JSONStore locking in the mutation callback.
				build.ArtifactURI = verifiedFinal.URI
				build.DownloadURL = verifiedFinal.DownloadURL
				build.ArtifactSHA256 = verifiedFinal.SHA256
				build.ArtifactSize = verifiedFinal.Size
				build.ArtifactVerifiedAt = &verifiedAt
				build.ArtifactVerificationStatus = "verified"
				build.Logs = append(build.Logs, logEntryForRequest(r, build.RunnerID, "build completed successfully"))
			} else {
				// Legacy development compatibility only; release gates remain verified-binding only.
				build.ArtifactURI, build.DownloadURL = request.ArtifactURI, request.DownloadURL
				build.ArtifactVerificationStatus = "legacy-unverified"
				build.Logs = append(build.Logs, logEntryForRequest(r, "runner", "build completed successfully"))
			}
		} else {
			build.Logs = append(build.Logs, logEntryForRequest(r, firstNonEmpty(build.RunnerID, "runner"), "build failed: "+request.Error))
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if updated.Status == domain.BuildSucceeded {
		s.recordBuildNotification("build.preview_ready", "info", "Preview 已生成",
			buildNotificationMessage(updated, "Runner 已生成 Preview 制品，等待人工验证。"),
			updated, map[string]string{"artifactUri": updated.ArtifactURI, "downloadUrl": updated.DownloadURL})
	}
	if updated.Status == domain.BuildFailed {
		s.recordBuildNotification("build.failed", "critical", "构建失败",
			buildNotificationMessage(updated, firstNonEmpty(updated.Error, "Runner 报告构建失败。")),
			updated, map[string]string{"error": updated.Error})
	}
	writeJSON(w, http.StatusOK, withReleaseCheckSummary(updated))
}

func (s Server) downloadRunnerInput(w http.ResponseWriter, r *http.Request) {
	id, uploadID := r.PathValue("id"), r.PathValue("uploadId")
	if s.auth.Enabled {
		if _, err := s.authorizeExecution(r, id, domain.BuildRunning); err != nil {
			writeErrorText(w, http.StatusForbidden, err.Error())
			return
		}
	}
	build, err := s.store.ReadBuild(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	var locked *domain.ResolvedComponent
	for index := range build.BuildLock.Components {
		component := &build.BuildLock.Components[index]
		if component.Source.UploadID == uploadID && component.URI == "local-upload://"+uploadID && strings.TrimSpace(component.SHA256) != "" {
			locked = component
			break
		}
	}
	if locked == nil {
		writeErrorText(w, http.StatusForbidden, "upload is not locked for this build")
		return
	}
	upload, err := s.store.ReadUpload(uploadID)
	if err != nil || upload.URI != locked.URI || !strings.EqualFold(upload.SHA256, locked.SHA256) {
		writeErrorText(w, http.StatusForbidden, "locked upload integrity mismatch")
		return
	}
	if s.artifacts == nil {
		writeErrorText(w, http.StatusNotFound, "artifact store unavailable")
		return
	}
	if err := s.artifacts.ServeUpload(w, r, uploadID); err != nil {
		writeError(w, http.StatusNotFound, err)
	}
}

func (s Server) runnerHeartbeat(w http.ResponseWriter, r *http.Request) {
	var request domain.RunnerHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		if s.auth.Enabled {
			writeErrorText(w, http.StatusNotFound, "build-scoped heartbeat is required")
			return
		}
		id = request.BuildID
	}
	if id == "" {
		writeErrorText(w, http.StatusBadRequest, "build id is required")
		return
	}
	claims := domain.ExecutionClaims{}
	if s.auth.Enabled {
		var err error
		claims, err = s.authorizeExecution(r, id, domain.BuildRunning)
		if err != nil {
			writeErrorText(w, http.StatusForbidden, err.Error())
			return
		}
	}
	message := "heartbeat"
	if request.Message != "" {
		message += ": " + request.Message
	}
	_, err := s.store.UpdateBuild(id, func(build *domain.Build) error {
		now := time.Now().UTC()
		build.LastHeartbeatAt = &now
		build.Logs = appendBuildLogForRequest(build.Logs, r, firstNonEmpty(claims.RunnerID, request.RunnerID), message)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
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

func (s Server) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.ListAuditEvents(auditEventFilterFromRequest(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s Server) exportAuditArchive(w http.ResponseWriter, r *http.Request) {
	filter := auditEventFilterFromRequest(r)
	events, err := s.store.ListAuditEvents(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	archive, err := buildAuditArchive(filter, events, s.auditKey, s.auditKeyID, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if s.artifacts != nil {
		archiveURI, err := s.artifacts.AuditArchiveURI(archive.Digest)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		storedAt := time.Now().UTC()
		policy := s.artifacts.AuditArchiveStoragePolicy(storedAt)
		archive.ArchiveURI = archiveURI
		archive.ArchiveStoredAt = &storedAt
		archive.ArchiveRetentionMode = policy.RetentionMode
		archive.ArchiveRetainUntil = policy.RetainUntil
		archive.ArchiveLegalHold = policy.LegalHold
		payload, err := json.Marshal(archive)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if _, err := s.artifacts.SaveAuditArchive(archive.Digest, payload, policy); err != nil {
			s.recordStorageNotification("audit_archive", "", "", "审计归档存储失败", "审计归档无法写入制品存储，需要检查本地磁盘或对象存储。", err)
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	s.recordAudit(r, domain.AuditEvent{
		Action:       "audit.archive.export",
		ResourceType: "audit_archive",
		ResourceID:   archive.Digest,
		Status:       "success",
		Summary:      "audit archive exported",
		Metadata: map[string]string{
			"digest":          archive.Digest,
			"eventCount":      strconv.Itoa(archive.EventCount),
			"digestAlgorithm": archive.DigestAlgorithm,
			"archiveUri":      archive.ArchiveURI,
			"retentionMode":   archive.ArchiveRetentionMode,
			"legalHold":       archive.ArchiveLegalHold,
		},
	})
	w.Header().Set("Content-Disposition", `attachment; filename="packaging-audit-archive.json"`)
	writeJSON(w, http.StatusOK, archive)
}

func auditEventFilterFromRequest(r *http.Request) domain.AuditEventFilter {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return domain.AuditEventFilter{
		ResourceType: r.URL.Query().Get("resourceType"),
		ResourceID:   r.URL.Query().Get("resourceId"),
		BuildID:      r.URL.Query().Get("buildId"),
		ProductID:    r.URL.Query().Get("productId"),
		UploadID:     r.URL.Query().Get("uploadId"),
		Action:       r.URL.Query().Get("action"),
		Limit:        limit,
	}
}

func buildAuditArchive(filter domain.AuditEventFilter, events []domain.AuditEvent, hmacKey string, hmacKeyID string, generatedAt time.Time) (domain.AuditArchive, error) {
	payload := struct {
		SchemaVersion string                  `json:"schemaVersion"`
		GeneratedAt   time.Time               `json:"generatedAt"`
		EventCount    int                     `json:"eventCount"`
		Filter        domain.AuditEventFilter `json:"filter"`
		Events        []domain.AuditEvent     `json:"events"`
	}{
		SchemaVersion: "packaging.audit.archive/v1",
		GeneratedAt:   generatedAt,
		EventCount:    len(events),
		Filter:        filter,
		Events:        events,
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return domain.AuditArchive{}, err
	}
	sum := sha256.Sum256(canonical)
	digest := hex.EncodeToString(sum[:])
	archive := domain.AuditArchive{
		SchemaVersion:   payload.SchemaVersion,
		GeneratedAt:     payload.GeneratedAt,
		EventCount:      payload.EventCount,
		Filter:          payload.Filter,
		Events:          payload.Events,
		DigestAlgorithm: "sha256",
		Digest:          digest,
	}
	if hmacKey != "" {
		mac := hmac.New(sha256.New, []byte(hmacKey))
		_, _ = mac.Write(canonical)
		archive.SignatureAlgorithm = "hmac-sha256"
		archive.SignatureKeyID = strings.TrimSpace(hmacKeyID)
		archive.Signature = hex.EncodeToString(mac.Sum(nil))
	}
	return archive, nil
}

func buildReleaseEvidenceArchive(report domain.ReproducibilityReport, hmacKey string, hmacKeyID string, generatedAt time.Time) (domain.ReleaseEvidenceArchive, error) {
	payload := struct {
		SchemaVersion string                       `json:"schemaVersion"`
		GeneratedAt   time.Time                    `json:"generatedAt"`
		BuildID       string                       `json:"buildId"`
		ProductID     string                       `json:"productId"`
		Reproducible  bool                         `json:"reproducible"`
		Report        domain.ReproducibilityReport `json:"report"`
	}{
		SchemaVersion: "packaging.release.evidence/v1",
		GeneratedAt:   generatedAt,
		BuildID:       report.BuildID,
		ProductID:     report.ProductID,
		Reproducible:  report.Reproducible,
		Report:        report,
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return domain.ReleaseEvidenceArchive{}, err
	}
	sum := sha256.Sum256(canonical)
	digest := hex.EncodeToString(sum[:])
	archive := domain.ReleaseEvidenceArchive{
		SchemaVersion:   payload.SchemaVersion,
		GeneratedAt:     payload.GeneratedAt,
		BuildID:         payload.BuildID,
		ProductID:       payload.ProductID,
		Reproducible:    payload.Reproducible,
		Report:          payload.Report,
		DigestAlgorithm: "sha256",
		Digest:          digest,
	}
	if hmacKey != "" {
		mac := hmac.New(sha256.New, []byte(hmacKey))
		_, _ = mac.Write(canonical)
		archive.SignatureAlgorithm = "hmac-sha256"
		archive.SignatureKeyID = strings.TrimSpace(hmacKeyID)
		archive.Signature = hex.EncodeToString(mac.Sum(nil))
	}
	return archive, nil
}

func (s Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	notifications, err := s.store.ListNotifications(domain.NotificationFilter{
		Type:         r.URL.Query().Get("type"),
		Severity:     r.URL.Query().Get("severity"),
		ResourceType: r.URL.Query().Get("resourceType"),
		ResourceID:   r.URL.Query().Get("resourceId"),
		BuildID:      r.URL.Query().Get("buildId"),
		ProductID:    r.URL.Query().Get("productId"),
		UploadID:     r.URL.Query().Get("uploadId"),
		RunnerID:     r.URL.Query().Get("runnerId"),
		ReadStatus:   normalizedReadStatus(r.URL.Query().Get("read")),
		Limit:        limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, notifications)
}

func (s Server) markNotificationRead(w http.ResponseWriter, r *http.Request) {
	notification, err := s.store.MarkNotificationRead(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, notification)
}

func (s Server) markAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	notifications, err := s.store.MarkAllNotificationsRead()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, notifications)
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

	if s.artifacts == nil {
		err := errors.New("artifact store is not configured")
		s.recordStorageNotification("upload", "", "", "制品存储未配置", "上传制品前需要配置可用的制品存储。", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	record, err := s.artifacts.SaveUpload(file, header)
	if err != nil {
		s.recordStorageNotification("upload", "", "", "制品上传失败", "制品无法写入存储，需要检查本地磁盘或对象存储。", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	record = applyUploadMetadata(record, r)
	if err := s.store.SaveUploadRecord(record); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.recordAudit(r, domain.AuditEvent{
		Action:       "upload.create",
		ResourceType: "upload",
		ResourceID:   record.ID,
		UploadID:     record.ID,
		ProductID:    record.Metadata["productId"],
		Status:       "success",
		Summary:      "artifact uploaded",
		Metadata: map[string]string{
			"fileName":       record.FileName,
			"sha256":         record.SHA256,
			"downloadPolicy": record.DownloadPolicy,
		},
	})
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
	if !s.authorizeUploadDownload(w, r, record) {
		return
	}
	if s.artifacts == nil {
		err := errors.New("artifact store is not configured")
		s.recordStorageNotification("download", r.PathValue("id"), record.Metadata["productId"], "制品存储未配置", "下载制品前需要配置可用的制品存储。", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.recordAudit(r, domain.AuditEvent{
		Action:       "upload.download",
		ResourceType: "upload",
		ResourceID:   record.ID,
		UploadID:     record.ID,
		ProductID:    record.Metadata["productId"],
		Status:       "success",
		Summary:      "artifact downloaded",
		Metadata: map[string]string{
			"fileName":       record.FileName,
			"downloadPolicy": record.DownloadPolicy,
			"retention":      record.RetentionStatus,
		},
	})
	if err := s.artifacts.ServeUpload(w, r, r.PathValue("id")); err != nil {
		s.recordStorageNotification("download", r.PathValue("id"), record.Metadata["productId"], "制品下载失败", "制品无法从存储读取，需要检查本地磁盘或对象存储。", err)
		writeError(w, http.StatusNotFound, err)
	}
}

func (s Server) authorizeUploadDownload(w http.ResponseWriter, r *http.Request, record domain.UploadRecord) bool {
	if !s.auth.Enabled {
		return true
	}
	switch normalizedDownloadPolicy(record.DownloadPolicy) {
	case "public":
		return true
	case "restricted":
		_, ok := s.requireAuth(w, r, roleOperator, roleAdmin)
		return ok
	default:
		_, ok := s.requireAuth(w, r, roleViewer, roleOperator, roleAdmin)
		return ok
	}
}

func (s Server) recordAudit(r *http.Request, event domain.AuditEvent) {
	if s.store == nil {
		return
	}
	if event.ID == "" {
		event.ID = "aud_" + randomID()
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	principal := s.auditPrincipal(r)
	if event.ActorID == "" {
		event.ActorID = principal.ID
	}
	if event.ActorRole == "" {
		event.ActorRole = principal.Role
	}
	if event.Status == "" {
		event.Status = "success"
	}
	if event.RequestID == "" {
		event.RequestID = r.Header.Get("X-Request-Id")
	}
	if event.CorrelationID == "" {
		event.CorrelationID = r.Header.Get("X-Correlation-Id")
	}
	if event.Metadata == nil {
		event.Metadata = map[string]string{}
	}
	if err := s.store.SaveAuditEvent(event); err != nil {
		slog.Warn("audit event write failed",
			"error", err,
			"action", event.Action,
			"resource_type", event.ResourceType,
			"resource_id", event.ResourceID,
			"request_id", event.RequestID,
			"correlation_id", event.CorrelationID,
		)
	}
}

func (s Server) recordSecretResolutionAudit(r *http.Request, build domain.Build, refs []string, stage string, status string) {
	if len(refs) == 0 {
		return
	}
	s.recordAudit(r, domain.AuditEvent{
		Action:       "secret.resolve",
		ResourceType: "secret_resolution",
		ResourceID:   build.ID,
		BuildID:      build.ID,
		ProductID:    build.ProductID,
		Status:       status,
		Summary:      "build secret references resolved for runner submission",
		Metadata: map[string]string{
			"secretCount": strconv.Itoa(len(refs)),
			"secretRefs":  strings.Join(refs, ","),
			"stage":       stage,
			"target":      build.Target,
		},
	})
}

func (s Server) recordBuildNotification(notificationType string, severity string, title string, message string, build domain.Build, metadata map[string]string) {
	notification := domain.Notification{
		ID:           "ntf_" + randomID(),
		CreatedAt:    time.Now().UTC(),
		Type:         notificationType,
		Severity:     severity,
		Title:        title,
		Message:      message,
		ResourceType: "build",
		ResourceID:   build.ID,
		ProductID:    build.ProductID,
		BuildID:      build.ID,
		ActionURL:    "/builds/" + build.ID,
		Metadata: map[string]string{
			"target":         build.Target,
			"profile":        build.Profile,
			"version":        build.Version,
			"releaseChannel": build.ReleaseChannel,
			"status":         build.Status,
		},
	}
	for key, value := range metadata {
		if value != "" {
			notification.Metadata[key] = value
		}
	}
	if err := s.store.SaveNotification(notification); err != nil {
		slog.Warn("notification write failed",
			"error", err,
			"type", notification.Type,
			"severity", notification.Severity,
			"resource_type", notification.ResourceType,
			"resource_id", notification.ResourceID,
		)
	}
}

func (s Server) recordStorageNotification(operation string, resourceID string, productID string, title string, message string, cause error) {
	notification := domain.Notification{
		ID:           "ntf_" + randomID(),
		CreatedAt:    time.Now().UTC(),
		Type:         "storage.unavailable",
		Severity:     "critical",
		Title:        title,
		Message:      message,
		ResourceType: "artifact_store",
		ResourceID:   resourceID,
		ProductID:    productID,
		UploadID:     resourceID,
		ActionURL:    "/uploads",
		Metadata: map[string]string{
			"operation": operation,
			"error":     cause.Error(),
		},
	}
	if err := s.store.SaveNotification(notification); err != nil {
		slog.Warn("storage notification write failed",
			"error", err,
			"operation", operation,
			"resource_id", resourceID,
		)
	}
}

func buildNotificationMessage(build domain.Build, suffix string) string {
	parts := []string{build.ProductID, build.Target, build.Profile, build.Version}
	compact := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			compact = append(compact, part)
		}
	}
	if len(compact) == 0 {
		return suffix
	}
	return strings.Join(compact, " / ") + "：" + suffix
}

func normalizedReadStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "read", "true":
		return "read"
	case "unread", "false":
		return "unread"
	default:
		return ""
	}
}

func (s Server) auditPrincipal(r *http.Request) authPrincipal {
	if principal, ok := r.Context().Value(authPrincipalContextKey{}).(authPrincipal); ok && principal.ID != "" {
		return principal
	}
	if !s.auth.Enabled {
		return authPrincipal{ID: "system", Role: roleAdmin}
	}
	if principal, ok := s.authenticate(r); ok {
		return principal
	}
	return authPrincipal{ID: "anonymous", Role: ""}
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
	"AUTH_REQUIRED": {
		Code:          "AUTH_REQUIRED",
		MessageKey:    "errors.auth.required",
		Message:       "authentication is required",
		Severity:      "error",
		Category:      "security",
		UserActionKey: "actions.signIn",
		UserAction:    "sign in before continuing",
	},
	"AUTH_FORBIDDEN": {
		Code:          "AUTH_FORBIDDEN",
		MessageKey:    "errors.auth.forbidden",
		Message:       "permission denied",
		Severity:      "warning",
		Category:      "security",
		UserActionKey: "actions.requestPermission",
		UserAction:    "request the required permission before continuing",
	},
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
	case http.StatusUnauthorized:
		return "AUTH_REQUIRED"
	case http.StatusForbidden:
		return "AUTH_FORBIDDEN"
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
	if strings.HasPrefix(code, "AUTH_") || status == http.StatusUnauthorized || status == http.StatusForbidden {
		return "security"
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
	record.RequestID, record.CorrelationID = requestIDs(r)
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

func buildMetrics(builds []domain.Build, uploads []domain.UploadRecord, now time.Time) string {
	builder := &strings.Builder{}
	writeMetricHelp(builder, "packaging_builds_total", "Build count by status, target, and release channel.")
	writeMetricType(builder, "packaging_builds_total", "gauge")
	buildCounts := map[string]int{}
	for _, build := range builds {
		key := metricKey(
			metricLabel{"status", firstNonEmpty(build.Status, "unknown")},
			metricLabel{"target", firstNonEmpty(build.Target, "unknown")},
			metricLabel{"release_channel", firstNonEmpty(build.ReleaseChannel, "unknown")},
		)
		buildCounts[key]++
	}
	writeMetricIntMap(builder, "packaging_builds_total", buildCounts)

	writeMetricHelp(builder, "packaging_release_gate_blockers_total", "Blocked release gate checks by stage.")
	writeMetricType(builder, "packaging_release_gate_blockers_total", "gauge")
	gateBlockers := map[string]int{}
	releaseReady := 0
	previewReady := 0
	for _, build := range builds {
		check := buildReleaseCheck(build)
		if check.PreviewReady {
			previewReady++
		}
		if check.ReleaseReady {
			releaseReady++
		}
		for _, item := range check.Checks {
			if item.Status == "blocked" {
				gateBlockers[metricKey(metricLabel{"stage", firstNonEmpty(item.Stage, "unknown")})]++
			}
		}
	}
	writeMetricIntMap(builder, "packaging_release_gate_blockers_total", gateBlockers)
	writeMetricHelp(builder, "packaging_preview_ready_builds_total", "Builds currently satisfying preview validation gates.")
	writeMetricType(builder, "packaging_preview_ready_builds_total", "gauge")
	writeMetricInt(builder, "packaging_preview_ready_builds_total", nil, previewReady)
	writeMetricHelp(builder, "packaging_release_ready_builds_total", "Builds currently satisfying release gates.")
	writeMetricType(builder, "packaging_release_ready_builds_total", "gauge")
	writeMetricInt(builder, "packaging_release_ready_builds_total", nil, releaseReady)

	writeMetricHelp(builder, "packaging_uploads_total", "Upload count by retention status and download policy.")
	writeMetricType(builder, "packaging_uploads_total", "gauge")
	uploadCounts := map[string]int{}
	uploadBytes := map[string]int64{}
	for _, upload := range uploads {
		upload = normalizeUploadRecord(upload, now)
		key := metricKey(
			metricLabel{"retention_status", firstNonEmpty(upload.RetentionStatus, "unknown")},
			metricLabel{"download_policy", normalizedDownloadPolicy(upload.DownloadPolicy)},
		)
		uploadCounts[key]++
		uploadBytes[key] += upload.Size
	}
	writeMetricIntMap(builder, "packaging_uploads_total", uploadCounts)
	writeMetricHelp(builder, "packaging_upload_bytes_total", "Total uploaded artifact bytes by retention status and download policy.")
	writeMetricType(builder, "packaging_upload_bytes_total", "gauge")
	writeMetricInt64Map(builder, "packaging_upload_bytes_total", uploadBytes)

	writeMetricHelp(builder, "packaging_runners_total", "Runner count by availability status and target.")
	writeMetricType(builder, "packaging_runners_total", "gauge")
	runners := runnerStatuses(builds, now)
	runnerCounts := map[string]int{}
	for _, runner := range runners {
		key := metricKey(
			metricLabel{"status", firstNonEmpty(runner.Status, "unknown")},
			metricLabel{"target", firstNonEmpty(runner.Target, "unknown")},
		)
		runnerCounts[key]++
	}
	writeMetricIntMap(builder, "packaging_runners_total", runnerCounts)
	writeMetricHelp(builder, "packaging_runner_running_builds", "Running build count by runner.")
	writeMetricType(builder, "packaging_runner_running_builds", "gauge")
	writeMetricHelp(builder, "packaging_runner_last_heartbeat_age_seconds", "Seconds since runner last heartbeat or log.")
	writeMetricType(builder, "packaging_runner_last_heartbeat_age_seconds", "gauge")
	for _, runner := range runners {
		labels := []metricLabel{
			{"runner_id", runner.RunnerID},
			{"status", firstNonEmpty(runner.Status, "unknown")},
			{"target", firstNonEmpty(runner.Target, "unknown")},
		}
		writeMetricInt(builder, "packaging_runner_running_builds", labels, runner.RunningBuilds)
		if last := runner.LastHeartbeatAt; last != nil {
			writeMetricInt(builder, "packaging_runner_last_heartbeat_age_seconds", labels, int(now.Sub(*last).Seconds()))
		} else if last := runner.LastLogAt; last != nil {
			writeMetricInt(builder, "packaging_runner_last_heartbeat_age_seconds", labels, int(now.Sub(*last).Seconds()))
		}
	}
	writeRunnerCacheMetrics(builder, builds)
	return builder.String()
}

type runnerCacheMetric struct {
	RunnerID  string
	Target    string
	Stage     string
	CacheName string
	Status    string
	Bytes     int64
	Files     int
	Truncated bool
	LogTime   time.Time
}

func writeRunnerCacheMetrics(builder *strings.Builder, builds []domain.Build) {
	writeMetricHelp(builder, "packaging_runner_cache_entries", "Runner cache entry status by runner, target, stage, and cache name.")
	writeMetricType(builder, "packaging_runner_cache_entries", "gauge")
	writeMetricHelp(builder, "packaging_runner_cache_bytes", "Runner cache bytes parsed from latest cache heartbeat or log summary.")
	writeMetricType(builder, "packaging_runner_cache_bytes", "gauge")
	writeMetricHelp(builder, "packaging_runner_cache_files", "Runner cache file count parsed from latest cache heartbeat or log summary.")
	writeMetricType(builder, "packaging_runner_cache_files", "gauge")
	writeMetricHelp(builder, "packaging_runner_cache_scan_truncated", "Whether runner cache scan hit RUNNER_CACHE_SCAN_FILE_LIMIT.")
	writeMetricType(builder, "packaging_runner_cache_scan_truncated", "gauge")

	entries := map[string]int{}
	bytesByCache := map[string]int64{}
	filesByCache := map[string]int{}
	truncatedByCache := map[string]int{}
	for _, metric := range latestRunnerCacheMetrics(builds) {
		baseLabels := []metricLabel{
			{"runner_id", metric.RunnerID},
			{"target", firstNonEmpty(metric.Target, "unknown")},
			{"stage", metric.Stage},
			{"cache", metric.CacheName},
		}
		statusLabels := append([]metricLabel{}, baseLabels...)
		statusLabels = append(statusLabels, metricLabel{"status", metric.Status})
		entries[metricKey(statusLabels...)] = 1
		key := metricKey(baseLabels...)
		bytesByCache[key] = metric.Bytes
		filesByCache[key] = metric.Files
		if metric.Truncated {
			truncatedByCache[key] = 1
		} else {
			truncatedByCache[key] = 0
		}
	}
	writeMetricIntMap(builder, "packaging_runner_cache_entries", entries)
	writeMetricInt64Map(builder, "packaging_runner_cache_bytes", bytesByCache)
	writeMetricIntMap(builder, "packaging_runner_cache_files", filesByCache)
	writeMetricIntMap(builder, "packaging_runner_cache_scan_truncated", truncatedByCache)
}

func latestRunnerCacheMetrics(builds []domain.Build) []runnerCacheMetric {
	latest := map[string]runnerCacheMetric{}
	for _, build := range builds {
		for _, log := range logsWithSequence(build.Logs) {
			if log.Source == "" || log.Source == "system" || log.Source == "resolver" {
				continue
			}
			for _, metric := range parseRunnerCacheLog(log.Message) {
				metric.RunnerID = log.Source
				metric.Target = build.Target
				metric.LogTime = log.Time
				key := strings.Join([]string{metric.RunnerID, metric.Target, metric.Stage, metric.CacheName}, "\x00")
				if current, ok := latest[key]; !ok || metric.LogTime.After(current.LogTime) {
					latest[key] = metric
				}
			}
		}
	}
	result := make([]runnerCacheMetric, 0, len(latest))
	for _, metric := range latest {
		result = append(result, metric)
	}
	sort.Slice(result, func(i, j int) bool {
		left := strings.Join([]string{result[i].RunnerID, result[i].Target, result[i].Stage, result[i].CacheName}, "\x00")
		right := strings.Join([]string{result[j].RunnerID, result[j].Target, result[j].Stage, result[j].CacheName}, "\x00")
		return left < right
	})
	return result
}

func parseRunnerCacheLog(message string) []runnerCacheMetric {
	index := strings.Index(message, "cache ")
	if index < 0 {
		return nil
	}
	rest := message[index+len("cache "):]
	stageEnd := strings.Index(rest, ":")
	if stageEnd <= 0 {
		return nil
	}
	stage := strings.TrimSpace(rest[:stageEnd])
	if stage == "" || strings.ContainsAny(stage, " \t\r\n") {
		return nil
	}
	summary := rest[stageEnd+1:]
	metrics := []runnerCacheMetric{}
	for _, part := range strings.Split(summary, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, rawValue, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		rawValue = strings.TrimSpace(rawValue)
		if name == "" || rawValue == "" {
			continue
		}
		metric := runnerCacheMetric{Stage: stage, CacheName: name, Status: "ok"}
		switch {
		case strings.HasPrefix(rawValue, "unconfigured"):
			metric.Status = "unconfigured"
		case strings.HasPrefix(rawValue, "missing"):
			metric.Status = "missing"
		default:
			metric.Status = "ok"
			if strings.Contains(rawValue, " error=") {
				metric.Status = "error"
			}
			value := strings.Fields(rawValue)
			if len(value) == 0 {
				continue
			}
			sizeText, fileText, ok := strings.Cut(value[0], "/")
			if !ok {
				continue
			}
			bytes, ok := parseCacheBytes(sizeText)
			if !ok {
				continue
			}
			metric.Bytes = bytes
			if strings.HasSuffix(fileText, "+") {
				metric.Truncated = true
				fileText = strings.TrimSuffix(fileText, "+")
			}
			files, err := strconv.Atoi(fileText)
			if err != nil {
				continue
			}
			metric.Files = files
		}
		metrics = append(metrics, metric)
	}
	return metrics
}

func parseCacheBytes(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	multipliers := []struct {
		suffix     string
		multiplier float64
	}{
		{"GB", 1024 * 1024 * 1024},
		{"MB", 1024 * 1024},
		{"KB", 1024},
		{"B", 1},
	}
	for _, entry := range multipliers {
		if strings.HasSuffix(value, entry.suffix) {
			number := strings.TrimSuffix(value, entry.suffix)
			parsed, err := strconv.ParseFloat(number, 64)
			if err != nil {
				return 0, false
			}
			return int64(parsed*entry.multiplier + 0.5), true
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

type metricLabel struct {
	name  string
	value string
}

func writeMetricHelp(builder *strings.Builder, name string, help string) {
	builder.WriteString("# HELP ")
	builder.WriteString(name)
	builder.WriteByte(' ')
	builder.WriteString(help)
	builder.WriteByte('\n')
}

func writeMetricType(builder *strings.Builder, name string, metricType string) {
	builder.WriteString("# TYPE ")
	builder.WriteString(name)
	builder.WriteByte(' ')
	builder.WriteString(metricType)
	builder.WriteByte('\n')
}

func writeMetricIntMap(builder *strings.Builder, name string, values map[string]int) {
	keys := sortedMetricKeys(values)
	for _, key := range keys {
		writeMetricLine(builder, name, key, strconv.Itoa(values[key]))
	}
}

func writeMetricInt64Map(builder *strings.Builder, name string, values map[string]int64) {
	keys := sortedMetricKeys(values)
	for _, key := range keys {
		writeMetricLine(builder, name, key, strconv.FormatInt(values[key], 10))
	}
}

func writeMetricInt(builder *strings.Builder, name string, labels []metricLabel, value int) {
	writeMetricLine(builder, name, metricKey(labels...), strconv.Itoa(value))
}

func writeMetricLine(builder *strings.Builder, name string, labels string, value string) {
	builder.WriteString(name)
	if labels != "" {
		builder.WriteByte('{')
		builder.WriteString(labels)
		builder.WriteByte('}')
	}
	builder.WriteByte(' ')
	builder.WriteString(value)
	builder.WriteByte('\n')
}

func metricKey(labels ...metricLabel) string {
	if len(labels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(labels))
	for _, label := range labels {
		parts = append(parts, label.name+"=\""+escapeMetricLabel(label.value)+"\"")
	}
	return strings.Join(parts, ",")
}

func escapeMetricLabel(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"")
	return replacer.Replace(value)
}

func sortedMetricKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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

func appendBuildLogForRequest(logs []domain.BuildLog, r *http.Request, source string, message string) []domain.BuildLog {
	entry := logEntryForRequest(r, source, message)
	entry.Sequence = len(logs) + 1
	return append(logs, entry)
}

func requestIDs(r *http.Request) (string, string) {
	if r == nil {
		return "", ""
	}
	return r.Header.Get("X-Request-Id"), r.Header.Get("X-Correlation-Id")
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
		r.Header.Set("X-Request-Id", requestID)
		r.Header.Set("X-Correlation-Id", correlationID)
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
		releaseCheckItem("preview.artifact", "preview", "Preview 制品已验证", build.ArtifactUploadID != "" && build.ChecksumUploadID != "" && build.ArtifactVerifiedAt != nil, "preview requires a verified runner artifact binding"),
		releaseCheckItem("preview.build-lock", "preview", "Build Lock 已生成", build.BuildLock.BuildID != "", "Build Lock is not available"),
		releaseCheckItem("preview.lock-identity", "preview", "Build Lock 与构建参数一致", buildLockMatchesBuild(build), "Build Lock does not match build target, profile, or version"),
		releaseCheckItem("preview.required-components", "preview", "必需组件已锁定 URI", requiredComponentsLocked(components), "preview requires locked required component URIs"),
		releaseCheckItem("release.preview-validated", "release", "Preview 已验证", build.Status == domain.BuildPreviewValidated || build.Status == domain.BuildPublished, "only validated preview builds can be published"),
		releaseCheckItem("release.fixed-version", "release", "构建版本固定", build.Version != "" && build.Version != "latest", "release requires a fixed build version"),
		releaseCheckItem("release.exact-component-uris", "release", "组件 URI 精确", componentURIsExact(components), "release requires exact component URIs without wildcards"),
		releaseCheckItem("release.fixed-component-versions", "release", "组件版本固定", componentVersionsFixed(components), "release requires fixed component versions"),
		releaseCheckItem("release.artifact", "release", "Release 制品已验证", build.ArtifactUploadID != "" && build.ChecksumUploadID != "" && build.ArtifactVerifiedAt != nil, "release requires a verified runner artifact binding"),
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

func (s Server) reproducibilityReport(build domain.Build, generatedAt time.Time) domain.ReproducibilityReport {
	check := buildReleaseCheck(build)
	components := build.BuildLock.Components
	if len(components) == 0 {
		components = build.Components
	}
	sourceArtifacts := make([]domain.ReproducibilitySourceArtifact, 0, len(components))
	for _, component := range components {
		sourceArtifacts = append(sourceArtifacts, domain.ReproducibilitySourceArtifact{
			ComponentID: component.ID,
			Type:        component.Type,
			Version:     component.Version,
			SourceKind:  component.Source.Kind,
			URI:         component.URI,
			SHA256:      component.SHA256,
			Required:    component.Required,
		})
	}
	output := domain.ReproducibilityArtifact{
		URI:                build.ArtifactURI,
		DownloadURL:        build.DownloadURL,
		VerificationStatus: verificationStatus(build),
	}
	if uploadID := build.ArtifactUploadID; uploadID != "" {
		if record, err := s.store.ReadUpload(uploadID); err == nil {
			now := generatedAt
			record = normalizeUploadRecord(record, now)
			output.UploadID = record.ID
			output.SHA256 = record.SHA256
			output.Size = record.Size
			output.ContentType = record.ContentType
			output.DownloadPolicy = record.DownloadPolicy
			output.RetentionStatus = record.RetentionStatus
			createdAt := record.CreatedAt
			output.CreatedAt = &createdAt
		}
	}
	return domain.ReproducibilityReport{
		GeneratedAt:     generatedAt,
		BuildID:         build.ID,
		ProductID:       build.ProductID,
		Target:          build.Target,
		Profile:         build.Profile,
		Customer:        build.Customer,
		Version:         build.Version,
		ReleaseChannel:  build.ReleaseChannel,
		Status:          build.Status,
		Reproducible:    check.ReleaseReady,
		ReleaseCheck:    check,
		BuildLock:       build.BuildLock,
		SourceArtifacts: sourceArtifacts,
		OutputArtifact:  output,
		Runner: domain.ReproducibilityRunner{
			RunnerID:   build.RunnerID,
			NomadJobID: build.NomadJobID,
		},
		Lifecycle: domain.ReproducibilityLifecycle{
			CreatedAt:   build.CreatedAt,
			ApprovedAt:  build.ApprovedAt,
			ValidatedAt: build.ValidatedAt,
			PublishedAt: build.PublishedAt,
			UpdatedAt:   build.UpdatedAt,
		},
	}
}

func verificationStatus(build domain.Build) string {
	if build.ArtifactUploadID != "" && build.ChecksumUploadID != "" && build.ArtifactVerifiedAt != nil {
		return "verified"
	}
	if build.ArtifactURI != "" || build.DownloadURL != "" {
		return "legacy-unverified"
	}
	return "missing"
}

func runnerE2EEvidence(build domain.Build, final domain.UploadRecord, checksum domain.UploadRecord, authEnabled bool) domain.RunnerE2EEvidence {
	return domain.RunnerE2EEvidence{
		BuildID:              build.ID,
		Status:               build.Status,
		AuthEnabled:          authEnabled,
		Attempt:              build.Attempt,
		Target:               build.Target,
		RunnerID:             build.RunnerID,
		NomadJobID:           build.NomadJobID,
		DispatchStartedAt:    build.DispatchStartedAt,
		ArtifactUploadID:     build.ArtifactUploadID,
		ChecksumUploadID:     build.ChecksumUploadID,
		ArtifactSHA256:       build.ArtifactSHA256,
		ArtifactVerifiedAt:   build.ArtifactVerifiedAt,
		VerificationStatus:   verificationStatus(build),
		FinalBuildID:         final.BuildID,
		FinalAttempt:         final.Attempt,
		FinalRunnerID:        final.RunnerID,
		FinalKind:            final.Kind,
		ChecksumBuildID:      checksum.BuildID,
		ChecksumAttempt:      checksum.Attempt,
		ChecksumRunnerID:     checksum.RunnerID,
		ChecksumKind:         checksum.Kind,
		ChecksumParentUpload: checksum.ParentUploadID,
	}
}

// buildOutputUploadID is retained for compatibility with callers that need the
// authoritative runner-output relation. URI and download URL values are never
// interpreted as ownership evidence because legacy values are client controlled.
func buildOutputUploadID(build domain.Build) string {
	return build.ArtifactUploadID
}

func withReleaseCheckSummaries(builds []domain.Build) []domain.Build {
	for index := range builds {
		builds[index] = withReleaseCheckSummary(builds[index])
	}
	return builds
}

func withReleaseCheckSummary(build domain.Build) domain.Build {
	build.ArtifactVerificationStatus = verificationStatus(build)
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

func logEntryForRequest(r *http.Request, source string, message string) domain.BuildLog {
	requestID, correlationID := requestIDs(r)
	entry := logEntry(source, message)
	entry.RequestID = requestID
	entry.CorrelationID = correlationID
	return entry
}

func randomID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000")))
	}
	return hex.EncodeToString(b[:])
}

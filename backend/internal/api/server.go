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
	mux.HandleFunc("GET /api/products/{id}/versions", s.getProductVersions)
	mux.HandleFunc("GET /api/builds", s.listBuilds)
	mux.HandleFunc("POST /api/builds", s.createBuild)
	mux.HandleFunc("GET /api/builds/{id}", s.getBuild)
	mux.HandleFunc("GET /api/builds/{id}/lock", s.getBuildLock)
	mux.HandleFunc("GET /api/builds/{id}/download", s.downloadBuild)
	mux.HandleFunc("POST /api/builds/{id}/approve", s.approveBuild)
	mux.HandleFunc("POST /api/builds/{id}/reject", s.rejectBuild)
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
	product, err := s.store.WriteProductManifest(r.PathValue("id"), raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, product)
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
		build.ReleaseChannel = build.Profile
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

func (s Server) publishBuild(w http.ResponseWriter, r *http.Request) {
	var request domain.RunnerCompleteRequest
	_ = json.NewDecoder(r.Body).Decode(&request)

	now := time.Now().UTC()
	updated, err := s.store.UpdateBuild(r.PathValue("id"), func(build *domain.Build) error {
		if build.Status != domain.BuildSucceeded && build.Status != domain.BuildPublished {
			return errText("only succeeded builds can be published")
		}
		if request.ArtifactURI != "" {
			build.ArtifactURI = request.ArtifactURI
		}
		if request.DownloadURL != "" {
			build.DownloadURL = request.DownloadURL
		}
		build.Status = domain.BuildPublished
		build.PublishedAt = &now
		build.UpdatedAt = now
		channel := build.ReleaseChannel
		if channel == "" {
			channel = build.Profile
		}
		build.Logs = append(build.Logs, logEntry("system", "artifact published to channel "+channel))
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

package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vela/conductor/internal/domain"
	"vela/conductor/internal/nomad"
	"vela/conductor/internal/store"
)

type fakeScheduler struct {
	statuses map[string]nomad.JobStatus
	err      error
}

func (s fakeScheduler) Submit(domain.Build, domain.Product, string) (string, string, error) {
	return "", "", nil
}

func (s fakeScheduler) Inspect(jobID string) (nomad.JobStatus, error) {
	if s.err != nil {
		return nomad.JobStatus{}, s.err
	}
	if status, ok := s.statuses[jobID]; ok {
		return status, nil
	}
	return nomad.JobStatus{State: nomad.JobStateRunning}, nil
}

func (s fakeScheduler) Stop(string) error {
	return nil
}

func TestReconcileMarksNomadFailedAllocationFailed(t *testing.T) {
	st := newLifecycleStore(t)
	build := lifecycleBuild("nomad-failed", domain.BuildRunning, "job-failed")
	heartbeat := time.Now().UTC()
	build.LastHeartbeatAt = &heartbeat
	saveLifecycleBuild(t, st, build)

	monitor := NewMonitor(st, fakeScheduler{statuses: map[string]nomad.JobStatus{
		"job-failed": {State: nomad.JobStateFailed, Description: "alloc lost"},
	}}, time.Second, time.Hour)

	if err := monitor.ReconcileOnce(); err != nil {
		t.Fatal(err)
	}
	updated := readLifecycleBuild(t, st, build.ID)
	if updated.Status != domain.BuildFailed {
		t.Fatalf("expected failed build, got %#v", updated)
	}
	if !strings.Contains(updated.Error, "alloc lost") || !hasLifecycleLog(updated, "nomad allocation failed") {
		t.Fatalf("expected nomad failure evidence, got %#v", updated)
	}
}

func TestReconcileMarksPrematureNomadCompleteFailed(t *testing.T) {
	st := newLifecycleStore(t)
	build := lifecycleBuild("nomad-complete", domain.BuildQueued, "job-complete")
	saveLifecycleBuild(t, st, build)

	monitor := NewMonitor(st, fakeScheduler{statuses: map[string]nomad.JobStatus{
		"job-complete": {State: nomad.JobStateComplete, Description: "all allocations completed"},
	}}, time.Second, time.Hour)

	if err := monitor.ReconcileOnce(); err != nil {
		t.Fatal(err)
	}
	updated := readLifecycleBuild(t, st, build.ID)
	if updated.Status != domain.BuildFailed {
		t.Fatalf("expected failed build, got %#v", updated)
	}
	if !strings.Contains(updated.Error, "completed before runner reported success") {
		t.Fatalf("expected premature complete error, got %#v", updated)
	}
}

func TestReconcileMarksHeartbeatTimeoutFailed(t *testing.T) {
	st := newLifecycleStore(t)
	build := lifecycleBuild("heartbeat-timeout", domain.BuildRunning, "job-running")
	build.RunnerID = "runner-timeout-01"
	build.UpdatedAt = time.Now().UTC()
	lastHeartbeat := time.Now().UTC().Add(-10 * time.Minute)
	build.LastHeartbeatAt = &lastHeartbeat
	saveLifecycleBuild(t, st, build)

	monitor := NewMonitor(st, fakeScheduler{statuses: map[string]nomad.JobStatus{
		"job-running": {State: nomad.JobStateRunning, Description: "running"},
	}}, time.Second, time.Minute)

	if err := monitor.ReconcileOnce(); err != nil {
		t.Fatal(err)
	}
	updated := readLifecycleBuild(t, st, build.ID)
	if updated.Status != domain.BuildFailed {
		t.Fatalf("expected failed build, got %#v", updated)
	}
	if !strings.Contains(updated.Error, "runner heartbeat timeout") {
		t.Fatalf("expected heartbeat timeout error, got %#v", updated)
	}
	notifications, err := st.ListNotifications(domain.NotificationFilter{Type: "runner.offline", RunnerID: "runner-timeout-01"})
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 {
		t.Fatalf("expected one runner offline notification, got %#v", notifications)
	}
	notification := notifications[0]
	if notification.Severity != "critical" || notification.BuildID != build.ID || notification.ResourceType != "runner" {
		t.Fatalf("unexpected runner offline notification: %#v", notification)
	}
	if notification.Metadata["timeout"] != time.Minute.String() || notification.Metadata["target"] != "linux" {
		t.Fatalf("unexpected runner offline metadata: %#v", notification.Metadata)
	}
}

func TestReconcileLeavesHealthyAndTerminalBuildsUnchanged(t *testing.T) {
	st := newLifecycleStore(t)
	healthy := lifecycleBuild("healthy-running", domain.BuildRunning, "job-running")
	heartbeat := time.Now().UTC()
	healthy.LastHeartbeatAt = &heartbeat
	terminal := lifecycleBuild("already-succeeded", domain.BuildSucceeded, "job-failed")
	saveLifecycleBuild(t, st, healthy)
	saveLifecycleBuild(t, st, terminal)

	monitor := NewMonitor(st, fakeScheduler{statuses: map[string]nomad.JobStatus{
		"job-running": {State: nomad.JobStateRunning, Description: "running"},
		"job-failed":  {State: nomad.JobStateFailed, Description: "should be ignored"},
	}}, time.Second, time.Hour)

	if err := monitor.ReconcileOnce(); err != nil {
		t.Fatal(err)
	}
	if updated := readLifecycleBuild(t, st, healthy.ID); updated.Status != domain.BuildRunning {
		t.Fatalf("healthy build changed: %#v", updated)
	}
	if updated := readLifecycleBuild(t, st, terminal.ID); updated.Status != domain.BuildSucceeded {
		t.Fatalf("terminal build changed: %#v", updated)
	}
}

func TestReconcilePrunesOnlyOldReadNotifications(t *testing.T) {
	st := newLifecycleStore(t)
	now := time.Now().UTC()
	oldReadAt := now.Add(-10 * 24 * time.Hour)
	recentReadAt := now.Add(-1 * time.Hour)
	notifications := []domain.Notification{
		{
			ID:        "old-read",
			CreatedAt: oldReadAt,
			ReadAt:    &oldReadAt,
			Type:      "build.failed",
			Severity:  "critical",
			Title:     "old read",
			Message:   "old read",
		},
		{
			ID:        "old-unread",
			CreatedAt: oldReadAt,
			Type:      "runner.offline",
			Severity:  "critical",
			Title:     "old unread",
			Message:   "old unread",
		},
		{
			ID:        "recent-read",
			CreatedAt: recentReadAt,
			ReadAt:    &recentReadAt,
			Type:      "build.published",
			Severity:  "success",
			Title:     "recent read",
			Message:   "recent read",
		},
	}
	for _, notification := range notifications {
		if err := st.SaveNotification(notification); err != nil {
			t.Fatal(err)
		}
	}

	monitor := NewMonitor(st, fakeScheduler{}, time.Second, time.Hour).
		WithNotificationRetention(48 * time.Hour)
	if err := monitor.ReconcileOnce(); err != nil {
		t.Fatal(err)
	}

	all, err := st.ListNotifications(domain.NotificationFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if hasNotificationID(all, "old-read") {
		t.Fatalf("old read notification should have been pruned: %#v", all)
	}
	if !hasNotificationID(all, "old-unread") || !hasNotificationID(all, "recent-read") {
		t.Fatalf("unread or recent notification was pruned unexpectedly: %#v", all)
	}
}

func TestReconcileReturnsInspectErrorsAfterProcessingBuilds(t *testing.T) {
	st := newLifecycleStore(t)
	build := lifecycleBuild("inspect-error", domain.BuildRunning, "job-error")
	heartbeat := time.Now().UTC()
	build.LastHeartbeatAt = &heartbeat
	saveLifecycleBuild(t, st, build)

	monitor := NewMonitor(st, fakeScheduler{err: errors.New("nomad unavailable")}, time.Second, time.Hour)

	err := monitor.ReconcileOnce()
	if err == nil || !strings.Contains(err.Error(), "nomad unavailable") {
		t.Fatalf("expected inspect error, got %v", err)
	}
	updated := readLifecycleBuild(t, st, "inspect-error")
	if updated.Status != domain.BuildRunning {
		t.Fatalf("inspect error should not fail build directly: %#v", updated)
	}
}

func newLifecycleStore(t *testing.T) *store.JSONStore {
	t.Helper()
	dataDir := t.TempDir()
	productsDir := filepath.Join(dataDir, "products")
	if err := os.MkdirAll(productsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewJSONStore(filepath.Join(dataDir, "data"), productsDir)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func lifecycleBuild(id string, status string, jobID string) domain.Build {
	now := time.Now().UTC()
	return domain.Build{
		ID:             id,
		ProductID:      "lsap",
		Target:         "linux",
		Profile:        "qa",
		Version:        "1.0.0",
		ReleaseChannel: "preview",
		Status:         status,
		NomadJobID:     jobID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func saveLifecycleBuild(t *testing.T, st *store.JSONStore, build domain.Build) {
	t.Helper()
	if err := st.SaveBuild(build); err != nil {
		t.Fatal(err)
	}
}

func readLifecycleBuild(t *testing.T, st *store.JSONStore, id string) domain.Build {
	t.Helper()
	build, err := st.ReadBuild(id)
	if err != nil {
		t.Fatal(err)
	}
	return build
}

func hasLifecycleLog(build domain.Build, pattern string) bool {
	for _, log := range build.Logs {
		if log.Source == "lifecycle" && strings.Contains(log.Message, pattern) {
			return true
		}
	}
	return false
}

func hasNotificationID(notifications []domain.Notification, id string) bool {
	for _, notification := range notifications {
		if notification.ID == id {
			return true
		}
	}
	return false
}

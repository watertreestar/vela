package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"packaging-platform/backend/internal/domain"
	"packaging-platform/backend/internal/nomad"
	"packaging-platform/backend/internal/store"
)

type Monitor struct {
	store                 store.Store
	scheduler             nomad.Scheduler
	interval              time.Duration
	runTimeout            time.Duration
	notificationRetention time.Duration
}

func NewMonitor(store store.Store, scheduler nomad.Scheduler, interval time.Duration, runTimeout time.Duration) Monitor {
	return Monitor{store: store, scheduler: scheduler, interval: interval, runTimeout: runTimeout}
}

func (m Monitor) WithNotificationRetention(retention time.Duration) Monitor {
	m.notificationRetention = retention
	return m
}

func (m Monitor) Run(ctx context.Context) {
	if m.interval <= 0 {
		m.interval = 30 * time.Second
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		if err := m.ReconcileOnce(); err != nil {
			log.Printf("lifecycle reconcile: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m Monitor) ReconcileOnce() error {
	builds, err := m.store.ListBuilds()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	reconcileErrors := []error{}
	for _, build := range builds {
		if err := m.reconcileBuild(build, now); err != nil {
			log.Printf("reconcile build %s: %v", build.ID, err)
			reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile build %s: %w", build.ID, err))
		}
	}
	if err := m.pruneNotifications(now); err != nil {
		log.Printf("prune notifications: %v", err)
		reconcileErrors = append(reconcileErrors, fmt.Errorf("prune notifications: %w", err))
	}
	return errors.Join(reconcileErrors...)
}

func (m Monitor) pruneNotifications(now time.Time) error {
	if m.notificationRetention <= 0 {
		return nil
	}
	pruned, err := m.store.PruneReadNotificationsBefore(now.Add(-m.notificationRetention))
	if err != nil {
		return err
	}
	if pruned > 0 {
		log.Printf("pruned %d read notification(s)", pruned)
	}
	return nil
}

func (m Monitor) reconcileBuild(build domain.Build, now time.Time) error {
	switch build.Status {
	case domain.BuildQueued, domain.BuildRunning:
	default:
		return nil
	}

	if build.Status == domain.BuildRunning && m.runTimeout > 0 && now.Sub(build.UpdatedAt) > m.runTimeout {
		message := fmt.Sprintf("runner heartbeat timeout after %s", m.runTimeout)
		if err := m.failBuild(build.ID, message); err != nil {
			return err
		}
		m.recordRunnerOfflineNotification(build, now, message)
		return nil
	}

	if build.NomadJobID == "" {
		return nil
	}
	status, err := m.scheduler.Inspect(build.NomadJobID)
	if err != nil {
		return err
	}
	switch status.State {
	case nomad.JobStateFailed:
		return m.failBuild(build.ID, "nomad allocation failed: "+status.Description)
	case nomad.JobStateComplete:
		if build.Status == domain.BuildQueued || build.Status == domain.BuildRunning {
			return m.failBuild(build.ID, "nomad allocation completed before runner reported success")
		}
	}
	return nil
}

func (m Monitor) failBuild(id string, message string) error {
	_, err := m.store.UpdateBuild(id, func(build *domain.Build) error {
		if build.Status != domain.BuildQueued && build.Status != domain.BuildRunning {
			return nil
		}
		build.Status = domain.BuildFailed
		build.Error = message
		build.UpdatedAt = time.Now().UTC()
		build.Logs = append(build.Logs, domain.BuildLog{
			Time:    build.UpdatedAt,
			Source:  "lifecycle",
			Message: message,
		})
		return nil
	})
	return err
}

func (m Monitor) recordRunnerOfflineNotification(build domain.Build, now time.Time, message string) {
	runnerID := build.RunnerID
	if runnerID == "" {
		runnerID = "unknown"
	}
	notification := domain.Notification{
		ID:           fmt.Sprintf("ntf_runner_offline_%s_%d", build.ID, now.UnixNano()),
		CreatedAt:    now,
		Type:         "runner.offline",
		Severity:     "critical",
		Title:        "Runner 心跳超时",
		Message:      fmt.Sprintf("%s / %s / %s：Runner %s 超过 %s 未上报心跳，构建已标记失败。", build.ProductID, build.Target, build.Version, runnerID, m.runTimeout),
		ResourceType: "runner",
		ResourceID:   runnerID,
		ProductID:    build.ProductID,
		BuildID:      build.ID,
		RunnerID:     runnerID,
		ActionURL:    "/builds/" + build.ID,
		Metadata: map[string]string{
			"target":          build.Target,
			"profile":         build.Profile,
			"version":         build.Version,
			"releaseChannel":  build.ReleaseChannel,
			"timeout":         m.runTimeout.String(),
			"lastHeartbeatAt": build.UpdatedAt.Format(time.RFC3339),
			"error":           message,
		},
	}
	if err := m.store.SaveNotification(notification); err != nil {
		log.Printf("runner offline notification: %v", err)
	}
}

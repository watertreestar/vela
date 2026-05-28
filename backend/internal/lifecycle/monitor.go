package lifecycle

import (
	"context"
	"fmt"
	"log"
	"time"

	"packaging-platform/backend/internal/domain"
	"packaging-platform/backend/internal/nomad"
	"packaging-platform/backend/internal/store"
)

type Monitor struct {
	store      store.Store
	scheduler  nomad.Scheduler
	interval   time.Duration
	runTimeout time.Duration
}

func NewMonitor(store store.Store, scheduler nomad.Scheduler, interval time.Duration, runTimeout time.Duration) Monitor {
	return Monitor{store: store, scheduler: scheduler, interval: interval, runTimeout: runTimeout}
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
	for _, build := range builds {
		if err := m.reconcileBuild(build, now); err != nil {
			log.Printf("reconcile build %s: %v", build.ID, err)
		}
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
		return m.failBuild(build.ID, fmt.Sprintf("runner heartbeat timeout after %s", m.runTimeout))
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

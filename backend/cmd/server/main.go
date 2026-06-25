package main

import (
	"context"
	"log"
	"net/http"

	"packaging-platform/backend/internal/api"
	"packaging-platform/backend/internal/artifact"
	"packaging-platform/backend/internal/config"
	"packaging-platform/backend/internal/lifecycle"
	"packaging-platform/backend/internal/nomad"
	"packaging-platform/backend/internal/store"
)

func main() {
	cfg := config.Load()

	st, err := store.New(cfg)
	if err != nil {
		log.Fatalf("init store: %v", err)
	}

	scheduler, err := nomad.NewScheduler(cfg)
	if err != nil {
		log.Fatalf("init scheduler: %v", err)
	}
	artifacts, err := artifact.NewStore(cfg)
	if err != nil {
		log.Fatalf("init artifact store: %v", err)
	}

	go lifecycle.NewMonitor(st, scheduler, cfg.ReconcileInterval, cfg.RunnerTimeout).
		WithNotificationRetention(cfg.NotificationRetention).
		Run(context.Background())

	server := api.NewServerWithOptions(st, scheduler, artifacts, api.ServerOptions{
		Auth: api.AuthConfig{
			Enabled:          cfg.AuthEnabled,
			AdminToken:       cfg.AuthAdminToken,
			OperatorToken:    cfg.AuthOperatorToken,
			ViewerToken:      cfg.AuthViewerToken,
			RunnerToken:      cfg.AuthRunnerToken,
			OIDCIssuer:       cfg.OIDCIssuer,
			OIDCAudience:     cfg.OIDCAudience,
			OIDCJWKSURL:      cfg.OIDCJWKSURL,
			OIDCTokenURL:     cfg.OIDCTokenURL,
			OIDCClientID:     cfg.OIDCClientID,
			OIDCClientSecret: cfg.OIDCClientSecret,
			OIDCRoleClaim:    cfg.OIDCRoleClaim,
			OIDCSubClaim:     cfg.OIDCSubjectClaim,
			OIDCAdminRole:    cfg.OIDCAdminRole,
			OIDCOperatorRole: cfg.OIDCOperatorRole,
			OIDCViewerRole:   cfg.OIDCViewerRole,
			SessionHMACKey:   cfg.AuthSessionHMACKey,
			SessionTTL:       cfg.AuthSessionTTL,
			SessionStore:     cfg.AuthSessionStoreEnabled,
		},
		AuditArchiveHMACKey:   cfg.AuditArchiveHMACKey,
		AuditArchiveHMACKeyID: cfg.AuditArchiveHMACKeyID,
	})
	addr := ":" + cfg.Port
	log.Printf("packaging api listening on %s", addr)
	if err := http.ListenAndServe(addr, server.Routes()); err != nil {
		log.Fatal(err)
	}
}

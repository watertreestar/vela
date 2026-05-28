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

	go lifecycle.NewMonitor(st, scheduler, cfg.ReconcileInterval, cfg.RunnerTimeout).Run(context.Background())

	server := api.NewServer(st, scheduler, artifacts)
	addr := ":" + cfg.Port
	log.Printf("packaging api listening on %s", addr)
	if err := http.ListenAndServe(addr, server.Routes()); err != nil {
		log.Fatal(err)
	}
}

# Packaging Platform Implementation Plan

## Goal

Build an internal packaging system that manages product packaging configuration and delegates builds to controlled runners.

The system uses:

- Go backend as the control plane API.
- React, HeroUI, and Tailwind CSS for the web console.
- Nomad as the execution scheduler for Linux and Windows builder nodes.
- Dagger as the packaging workflow engine executed by runner jobs.
- MinIO as the artifact source and final installer storage.

## Phase 1 - MVP Control Plane

Deliverables:

- Product manifest model.
- Build request API.
- Build listing and detail API.
- Dry-run Nomad submission mode for local development.
- JSON file storage for initial persistence.
- Product detail and build request UI.

Scope:

- Linux and Windows targets are represented in the model.
- Actual Windows builder execution is deferred to later phases.
- MinIO is represented in build metadata and runner environment, not fully proxied by the API yet.

## Phase 2 - Runner Integration

Deliverables:

- Nomad job template per target.
- Linux runner image with Bun, Docker CLI, MinIO client, and Dagger.
- Windows runner with Bun, Inno Setup, MinIO client, and signing tools.
- Build log collection.
- Artifact upload metadata.

## Phase 3 - Product Matrix

Deliverables:

- Multiple products under `products/<product>`.
- Profile and customer overlays.
- Manifest schema validation.
- Build variables preview before submission.
- Version pinning and latest resolution.

## Phase 4 - Release Governance

Deliverables:

- Approval workflow.
- Release channels such as QA, staging, and production.
- Checksums and signatures.
- Build reproducibility report.
- Retention policy.

## Phase 5 - Production Hardening

Deliverables:

- PostgreSQL storage.
- Authentication and authorization.
- Secret management integration.
- Audit log.
- Runner capacity and queue monitoring.
- Retry, cancel, and timeout policies.

## MVP Local Flow

1. Start the Go API.
2. Start the frontend.
3. Create a build from the UI.
4. Backend persists the build record.
5. Backend submits a Nomad job or records a dry-run payload when `DRY_RUN=true`.

## Production Flow

1. User selects product, target, profile, customer, and version.
2. API validates manifest and creates a build request.
3. API submits a Nomad batch job.
4. Nomad schedules a Linux or Windows runner.
5. Runner executes Dagger workflow.
6. Dagger downloads source artifacts from MinIO, renders templates, builds the installer, and uploads outputs to MinIO.
7. API records final artifact metadata.

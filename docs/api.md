# Packaging Platform API

Base URL:

```text
http://localhost:8080
```

## Authentication

Local development can keep `AUTH_ENABLED=false`. When `AUTH_ENABLED=true`, protected APIs require `Authorization: Bearer <token>`.

Supported token types:

- Static platform tokens: `AUTH_ADMIN_TOKEN`, `AUTH_OPERATOR_TOKEN`, `AUTH_VIEWER_TOKEN`, and `AUTH_RUNNER_TOKEN`.
- OIDC/JWKS bearer JWT for human users when `OIDC_ISSUER`, `OIDC_AUDIENCE`, and `OIDC_JWKS_URL` are configured.

OIDC tokens currently require RS256 and must pass JWKS signature verification, issuer check, audience check, `exp`, and optional `nbf`. The role claim defaults to `roles`; external role values are mapped through `OIDC_ADMIN_ROLE`, `OIDC_OPERATOR_ROLE`, and `OIDC_VIEWER_ROLE`. The audit actor id is `oidc:<subject>`, where subject defaults to the `sub` claim. Runner APIs should use `AUTH_RUNNER_TOKEN`.

The admin frontend can obtain that bearer token either from an implicit/bearer callback or through Authorization Code + PKCE when `VITE_OIDC_RESPONSE_TYPE=code` and `VITE_OIDC_TOKEN_URL` are configured. The API boundary remains `Authorization: Bearer <token>`. By default that token is the IdP token; with the optional broker below it becomes a short-lived platform session token.

When `OIDC_TOKEN_URL`, `OIDC_CLIENT_ID`, and `AUTH_SESSION_HMAC_KEY` are configured, the API also exposes an optional broker:

```http
POST /api/auth/oidc/token
```

Request:

```json
{
  "code": "authorization-code",
  "codeVerifier": "pkce-code-verifier",
  "redirectUri": "https://packaging.example.com/"
}
```

The broker exchanges the code with the configured IdP token endpoint, validates the returned OIDC JWT with the same JWKS/issuer/audience/role policy, and returns a platform bearer session token:

```json
{
  "token": "pps_...",
  "tokenType": "Bearer",
  "role": "operator",
  "subject": "oidc:alice",
  "sessionId": "ses_...",
  "expiresAt": "2026-06-24T12:00:00Z"
}
```

The returned platform token is accepted by protected APIs through `Authorization: Bearer <token>`. By default it is stateless and expires according to `AUTH_SESSION_TTL_SECONDS`. Set `AUTH_SESSION_STORE_ENABLED=true` to persist issued platform sessions server-side; then the token contains a session id and protected APIs reject the token when that session is revoked or expired in the store.

```http
POST /api/auth/logout
Authorization: Bearer <platform-session-token>
```

Logout revokes the persisted platform session when `AUTH_SESSION_STORE_ENABLED=true`. For static tokens or raw IdP access tokens it is a no-op for the server and can still be used by the frontend as a unified "clear local token" action.

## Operations

```http
GET /metrics
```

Returns Prometheus text exposition. When `AUTH_ENABLED=true`, this endpoint requires viewer, operator, or admin bearer token.

Key metric families:

- `packaging_builds_total`, `packaging_preview_ready_builds_total`, `packaging_release_ready_builds_total`
- `packaging_release_gate_blockers_total`
- `packaging_uploads_total`, `packaging_upload_bytes_total`
- `packaging_runners_total`, `packaging_runner_running_builds`, `packaging_runner_last_heartbeat_age_seconds`
- `packaging_runner_cache_entries`, `packaging_runner_cache_bytes`, `packaging_runner_cache_files`, `packaging_runner_cache_scan_truncated`

Runner cache metrics are parsed from the latest runner `cache <stage>:` heartbeat/log summaries. The same snapshots are still written by the runner under `context/rendered/runner-cache-<stage>.json`.

## Products

```http
GET /api/products
GET /api/products/{id}
GET /api/products/{id}/manifest
PUT /api/products/{id}/manifest
POST /api/products/{id}/manifest/dry-run
GET /api/products/{id}/manifest/revisions
POST /api/products/{id}/manifest/revisions/{revision}/restore
GET /api/products/{id}/versions
GET /api/products/{id}/overlays/profiles
GET /api/products/{id}/overlays/profiles/{name}
PUT /api/products/{id}/overlays/profiles/{name}
GET /api/products/{id}/overlays/customers
GET /api/products/{id}/overlays/customers/{name}
PUT /api/products/{id}/overlays/customers/{name}
```

`PUT /api/products/{id}/manifest` accepts the full product manifest JSON. The API validates the manifest shape before saving and rejects invalid targets, component source kinds, missing target defaults, invalid workload runtime, and workload references to unknown components.

`POST /api/products/{id}/manifest/dry-run` validates a draft manifest and resolves it with build parameters without creating a build:

```json
{
  "manifest": {},
  "target": "linux",
  "profile": "qa",
  "customer": "example-customer",
  "version": "1.0.0",
  "releaseChannel": "preview",
  "configOverrides": {},
  "componentOverrides": {},
  "variables": {}
}
```

Response:

```json
{
  "valid": true,
  "errors": [],
  "buildLock": {},
  "config": {},
  "components": []
}
```

Every manifest save creates a revision. Revision list responses omit the full manifest body; restore promotes a revision back to the current manifest and creates a new save revision.

Overlay endpoints read and write JSON files under:

```text
products/{id}/profiles/{name}.json
products/{id}/customers/{name}.json
```

Overlay names may contain letters, numbers, dot, underscore, and hyphen.

## Builds

```http
GET /api/builds
POST /api/builds
GET /api/builds/{id}
GET /api/builds/{id}/download
GET /api/builds/{id}/logs
GET /api/builds/{id}/logs/stream
GET /api/builds/{id}/release-check
POST /api/builds/{id}/logs
POST /api/builds/{id}/approve
POST /api/builds/{id}/reject
POST /api/builds/{id}/validate-preview
POST /api/builds/{id}/publish
POST /api/builds/{id}/cancel
POST /api/builds/{id}/retry
```

`GET /api/builds/{id}/download` returns a redirect when the build has `downloadUrl`, or a JSON `artifactUri` fallback when only `artifactUri` is available. When `downloadUrl` points to the platform upload endpoint `/api/uploads/{uploadId}/download`, the build download endpoint first reuses that upload's retention and `downloadPolicy` checks. With `AUTH_ENABLED=true`, linked `restricted` uploads require operator/admin, linked `platform` uploads require viewer/operator/admin, and linked `public` uploads remain anonymous. Successful build download requests record a `build.download` audit event; if the client follows the upload redirect, the upload endpoint records a separate `upload.download` audit event.

Create build request:

```json
{
  "productId": "lsap",
  "target": "linux",
  "profile": "qa",
  "customer": "customer-a",
  "version": "latest",
  "releaseChannel": "preview",
  "configOverrides": {
    "installDir": "/opt/customer-lsap",
    "ports": {
      "web": 49000
    },
    "customer": {
      "name": "Customer A",
      "license": "enterprise"
    },
    "database": {
      "passwordPolicy": "strong"
    },
    "modules": {
      "camera": false,
      "push": true
    }
  },
  "componentOverrides": {
    "ui": {
      "kind": "fixed-url",
      "url": "https://artifacts.example.com/ui/dist.zip"
    }
  },
  "variables": {
    "defaultPort": "48000"
  }
}
```

Build states:

```text
pending_approval -> queued -> running -> succeeded -> preview_validated -> published
pending_approval -> rejected
running -> failed
pending_approval|queued|running -> canceled
failed|canceled -> queued
```

Release publishing is gated:

- create builds as `releaseChannel=preview`
- validate the generated preview installer with `POST /api/builds/{id}/validate-preview`
- publish with `POST /api/builds/{id}/publish`, which promotes the validated preview to `release`
- release publishing rejects `latest` builds and wildcard component URIs so the release can be reproduced from the build lock

`GET /api/builds/{id}/release-check` returns the shared gate used by preview validation and release publishing:

`GET /api/builds`, `GET /api/builds/{id}`, and build state mutation responses include `releaseCheckSummary` so the admin UI can use the same backend gate for list filters and Release button availability.

```json
{
  "buildId": "build-id",
  "stage": "preview",
  "previewReady": true,
  "releaseReady": false,
  "blockingCount": 1,
  "checks": [
    {
      "id": "release.fixed-version",
      "stage": "release",
      "title": "构建版本固定",
      "status": "blocked",
      "reason": "release requires a fixed build version"
    }
  ]
}
```

Preview validation requires a succeeded preview build, a downloadable artifact, a generated Build Lock whose target/profile/version match the build, and locked URIs for required components. Release publishing additionally requires a validated preview, fixed build and component versions, exact component URIs without wildcards, a downloadable artifact, and no build error.

`GET /api/builds/{id}/logs/stream` returns `text/event-stream` records:

```text
event: log
data: {"time":"...","source":"runner-1","message":"downloaded components","sequence":5}
```

Without query parameters the endpoint emits the current log snapshot and closes. With `?follow=true`, it keeps the connection open and polls for new log records.

## Error Handling

API errors use the structured contract described in [error-handling.md](error-handling.md). New clients should read `error.code`, `error.message`, `error.severity`, and `error.details`; `errors` remains as a compatibility list for validation details.

## Reproducibility Report

```http
GET /api/builds/{id}/reproducibility-report
```

Returns a release evidence document generated from the build record, Build Lock, release gate checks, component locks, final artifact metadata, runner identity, and lifecycle timestamps. The report is read-only and does not change build state.

Key fields:

- `reproducible`: mirrors the current release gate readiness.
- `releaseCheck`: the same gate result returned by `/api/builds/{id}/release-check`.
- `buildLock`: the immutable build configuration and resolved component set.
- `sourceArtifacts`: component id, type, version, source kind, resolved URI, sha256 when available, and required flag.
- `outputArtifact`: final artifact URI/download URL plus upload sha256, size, retention, and policy when the artifact came from the platform upload store.
- `runner`: runner id and Nomad job id.
- `lifecycle`: created, approved, validated, published, and updated timestamps when present.

```http
GET /api/builds/{id}/release-evidence/archive
```

Exports the reproducibility report as a signed release evidence archive. When auth is enabled, viewer/operator/admin can export it. The response includes:

- `schemaVersion`: currently `packaging.release.evidence/v1`.
- `report`: the same reproducibility report returned by `/api/builds/{id}/reproducibility-report`.
- `digestAlgorithm` and `digest`: SHA-256 of the canonical evidence payload.
- `signatureAlgorithm`, `signatureKeyId`, and `signature`: present when `AUDIT_ARCHIVE_HMAC_KEY` is configured; the same key settings are used for audit archives and release evidence archives.
- `archiveUri`, `archiveStoredAt`, `archiveRetentionMode`, `archiveRetainUntil`, and `archiveLegalHold`: storage and object-lock evidence when an artifact store is configured.

The archive is persisted under `release-evidence/sha256/<digest>.json`. Local storage returns `local-release-evidence://sha256/<digest>.json`; MinIO-compatible storage returns `minio://<bucket>/release-evidence/sha256/<digest>.json`. Exporting an archive writes a `release.evidence.archive.export` audit event.

## Audit Events

```http
GET /api/audit-events
GET /api/audit-events/archive
```

Returns independent audit events ordered by newest first. When `AUTH_ENABLED=true`, the endpoint requires viewer, operator, or admin bearer token.

Query filters:

- `buildId`
- `productId`
- `uploadId`
- `resourceType`
- `resourceId`
- `action`
- `limit`, default `100`, maximum `200`

Current audited actions:

- `manifest.update`
- `manifest.restore`
- `overlay.update`
- `build.create`
- `build.approve`
- `build.reject`
- `build.validate_preview`
- `build.publish`
- `build.retry`
- `build.cancel`
- `secret.resolve`
- `upload.create`
- `upload.download`
- `audit.archive.export`
- `release.evidence.archive.export`

Each event includes `actorId`, `actorRole`, `resourceType`, `resourceId`, `requestId`, `correlationId`, action-specific ids such as `buildId` or `uploadId`, and optional metadata.

`GET /api/audit-events/archive` accepts the same filters and returns a portable archive:

- `schemaVersion`: currently `packaging.audit.archive/v1`.
- `generatedAt`: archive creation time.
- `archiveUri`: persisted archive location when the API has an artifact store.
- `archiveStoredAt`: time the archive payload was persisted.
- `eventCount`: exported event count.
- `filter`: effective filter.
- `events`: exported audit events.
- `digestAlgorithm`: currently `sha256`.
- `digest`: SHA-256 of the canonical archive payload.
- `signatureAlgorithm` and `signature`: present when `AUDIT_ARCHIVE_HMAC_KEY` is configured; signature is `hmac-sha256` over the same canonical payload.
- `signatureKeyId`: optional non-secret HMAC key identifier from `AUDIT_ARCHIVE_HMAC_KEY_ID`, used to prove which rotated signing key was active.
- `archiveRetentionMode`, `archiveRetainUntil`, and `archiveLegalHold`: present when the MinIO/RustFS artifact store applies object-lock retention or legal hold to the archived object.

Exporting an archive records a separate `audit.archive.export` event. The export event is not included in the archive being generated because it is written after the archive payload is computed.

When artifact storage is configured, archive payloads are persisted under `audit-archives/sha256/<digest>.json`. Local storage returns `local-audit-archive://sha256/<digest>.json`; MinIO-compatible storage returns `minio://<bucket>/audit-archives/sha256/<digest>.json`. MinIO/RustFS WORM retention requires a bucket created with object lock enabled; otherwise S3-compatible storage will reject object-lock headers.

## Notifications

```http
GET /api/notifications
POST /api/notifications/{id}/read
POST /api/notifications/read-all
```

Returns durable admin notifications ordered by newest first. When `AUTH_ENABLED=true`, these endpoints require viewer, operator, or admin bearer token.

Read notifications are pruned by the lifecycle monitor after `NOTIFICATION_RETENTION_DAYS` days, default `90`. Unread notifications are not pruned automatically.

Query filters:

- `read`: `unread`, `read`, `false`, or `true`
- `type`
- `severity`
- `buildId`
- `productId`
- `uploadId`
- `resourceType`
- `resourceId`
- `runnerId`
- `limit`, default `100`, maximum `200`

Current generated notification types:

- `build.scheduler_failed`
- `build.preview_ready`
- `build.failed`
- `build.preview_validated`
- `build.published`
- `runner.offline` with metadata `timeout` and `lastHeartbeatAt`
- `storage.unavailable` with metadata `operation=upload|download|audit_archive`

Notification payload:

```json
{
  "id": "ntf_xxx",
  "createdAt": "2026-06-24T00:00:00Z",
  "readAt": "2026-06-24T00:05:00Z",
  "type": "build.preview_ready",
  "severity": "info",
  "title": "Preview 已生成",
  "message": "lsap / linux / qa / 1.0.0：Runner 已生成 Preview 制品，等待人工验证。",
  "resourceType": "build",
  "resourceId": "build-id",
  "productId": "lsap",
  "buildId": "build-id",
  "actionUrl": "/builds/build-id",
  "metadata": {
    "target": "linux",
    "profile": "qa",
    "version": "1.0.0",
    "status": "succeeded"
  }
}
```

## Uploads

```http
POST /api/uploads
GET /api/uploads
GET /api/uploads/{id}/download
```

`POST /api/uploads` accepts multipart field `file` and returns an upload record:

Optional multipart fields:

- `downloadPolicy`: `platform`, `restricted`, or `public`. When `AUTH_ENABLED=false`, downloads keep the local/dev behavior and are not identity-gated. When `AUTH_ENABLED=true`, `public` can be downloaded anonymously, `platform` requires viewer/operator/admin, and `restricted` requires operator/admin.
- `retentionDays`: number of days to keep the artifact downloadable. `0` means permanent retention.
- `metadata.{key}`: arbitrary string metadata, for example `metadata.productId=lsap` or `metadata.purpose=license`.

```json
{
  "id": "upload-id",
  "fileName": "license.lic",
  "uri": "local-upload://upload-id/license.lic",
  "downloadUrl": "http://127.0.0.1:8080/api/uploads/upload-id/download",
  "sha256": "...",
  "size": 2048,
  "contentType": "application/octet-stream",
  "downloadPolicy": "restricted",
  "retentionDays": 7,
  "expiresAt": "2026-06-10T00:00:00Z",
  "retentionStatus": "active",
  "requestId": "req_upload_create",
  "correlationId": "corr_build_flow",
  "metadata": {
    "productId": "lsap",
    "purpose": "license"
  },
  "createdAt": "2026-06-03T00:00:00Z"
}
```

`GET /api/uploads` recalculates `retentionStatus` at read time. Upload records keep the `requestId` and `correlationId` from the upload creation request. `GET /api/uploads/{id}/download` records the download request context in the `upload.download` audit event and returns `410 Gone` with `ARTIFACT_EXPIRED` when the artifact is past `expiresAt`, `401 AUTH_REQUIRED` when a protected download has no valid bearer token, or `403 AUTH_FORBIDDEN` when the token role is insufficient.

## Runner

Runner observability:

```http
GET /api/runners
```

Response is derived from build ownership and job logs:

```json
[
  {
    "runnerId": "linux-builder-01",
    "target": "linux",
    "status": "busy",
    "currentBuildId": "build-id",
    "currentBuildState": "running",
    "lastHeartbeatAt": "2026-06-03T00:00:00Z",
    "lastClaimAt": "2026-06-03T00:00:00Z",
    "lastLogAt": "2026-06-03T00:00:00Z",
    "runningBuilds": 1,
    "completedBuilds": 3,
    "failedBuilds": 1,
    "recentMessages": []
  }
]
```

Runner status values:

```text
busy      running build and heartbeat/log is fresh
online    no running build but heartbeat/log is fresh
idle      no heartbeat/log but no active build
stale     running build without a fresh heartbeat/log
offline   no active build and heartbeat/log is stale
unknown   running build without any runner log
```

Runner claims queued tasks:

```http
POST /api/runner/tasks/claim
```

```json
{
  "runnerId": "linux-builder-01",
  "target": "linux"
}
```

Runner writes logs:

```http
POST /api/builds/{id}/logs
```

```json
{
  "source": "linux-builder-01",
  "message": "downloading artifacts"
}
```

Runner completes task:

```http
POST /api/runner/tasks/{id}/complete
POST /api/runner/heartbeat
```

```json
{
  "status": "succeeded",
  "artifactUri": "minio://packaging-artifacts/products/lsap/qa/latest/linux/build-id",
  "downloadUrl": "https://artifact.example.com/lsap/latest/linux.tar.gz"
}
```

Failure:

```json
{
  "status": "failed",
  "error": "docker load failed"
}
```

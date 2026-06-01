# Packaging Platform API

Base URL:

```text
http://localhost:8080
```

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
GET /api/builds/{id}/release-check
POST /api/builds/{id}/logs
POST /api/builds/{id}/approve
POST /api/builds/{id}/reject
POST /api/builds/{id}/validate-preview
POST /api/builds/{id}/publish
POST /api/builds/{id}/cancel
POST /api/builds/{id}/retry
```

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

## Uploads

```http
POST /api/uploads
GET /api/uploads
GET /api/uploads/{id}/download
```

`POST /api/uploads` accepts multipart field `file` and returns an upload record:

```json
{
  "id": "upload-id",
  "fileName": "license.lic",
  "uri": "local-upload://upload-id/license.lic",
  "downloadUrl": "http://127.0.0.1:8080/api/uploads/upload-id/download",
  "sha256": "..."
}
```

## Runner

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

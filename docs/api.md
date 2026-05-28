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
GET /api/products/{id}/versions
```

`PUT /api/products/{id}/manifest` accepts the full product manifest JSON.

## Builds

```http
GET /api/builds
POST /api/builds
GET /api/builds/{id}
GET /api/builds/{id}/download
GET /api/builds/{id}/logs
POST /api/builds/{id}/logs
POST /api/builds/{id}/approve
POST /api/builds/{id}/reject
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
  "releaseChannel": "qa",
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
pending_approval -> queued -> running -> succeeded -> published
pending_approval -> rejected
running -> failed
pending_approval|queued|running -> canceled
failed|canceled -> queued
```

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

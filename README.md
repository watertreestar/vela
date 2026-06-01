# Packaging Platform

Internal packaging control plane for Linux and Windows installer builds.

## Structure

- `backend/` - Go API service.
- `frontend/` - React, Radix Themes, and Tailwind CSS web console.
- `products/` - Product manifests.
- `infra/nomad/` - Nomad job templates and examples.
- `infra/dagger/` - Dagger packaging workflow example.
- `infra/minio/` - MinIO bucket and artifact conventions.
- `docs/` - Architecture and phase plans.

Key docs:

- [Architecture](docs/architecture.md)
- [Operation Guide](docs/operation-guide.md)
- [Core Feature Plan](docs/core-feature-plan.md)
- [API](docs/api.md)
- [Nomad Runner Lifecycle](docs/nomad-runner-lifecycle.md)
- [lsapctl Runtime Contract](docs/lsapctl-runtime.md)
- [Development Summary](docs/development-summary.md)
- [Implementation Plan](docs/implementation-plan.md)

## Local Development

Implemented MVP features:

- product list
- manifest/config management
- version selection
- build parameters
- build records
- approval and publish actions
- preview validation before release promotion
- download URL tracking
- build log display
- runner task claim and completion APIs
- component source selection from repository, upload, fixed URL, or previous build URI
- typed config overrides for port, service version, install directory, customer, license, password policy, and module flags
- cancel/retry and runner heartbeat

Runner responsibility boundary:

- claim approved build tasks
- download source artifacts
- compile `lsapctl`
- package Linux `tar.gz`
- package Windows `setup.exe`
- upload final artifacts
- report logs and final artifact metadata

lsapctl:

```bash
cd lsapctl
go build -o ../dist/lsapctl .
./lsapctl --help
./lsapctl completion bash
```

Linux packaging should compile `lsapctl` into the package context root through `LSAPCTL_BUILD_CMD`.

Runner:

```bash
cd runner
bun package-runner.js
```

Useful runner variables:

- `PACKAGING_API_URL` - API URL, default `http://localhost:8080`.
- `RUNNER_ID` - runner identity.
- `TARGET` - optional target filter, for example `linux` or `windows`.
- `ARTIFACT_ROOT` - local root for resolving `minio://...` or repository URIs during local tests.
- `MINIO_PUBLIC_URL` - public HTTP base for downloading `minio://...` objects.
- `LSAPCTL_BUILD_CMD` - command run before packaging; receives `OUTPUT_DIR`.
- `LINUX_PACKAGER_CMD` - custom Linux packager command; receives `OUTPUT`.
- `WINDOWS_PACKAGER_CMD` - custom Windows setup command; receives `OUTPUT`.

Backend:

```bash
docker run -d --name packaging-postgres \
  -e POSTGRES_USER=packaging \
  -e POSTGRES_PASSWORD=packaging \
  -e POSTGRES_DB=packaging \
  -p 5432:5432 postgres:16

cd backend
go run ./cmd/server
```

Frontend:

```bash
cd frontend
npm install
npm run dev
```

Frontend verification:

```bash
cd frontend
npm test -- --run
npm run test:e2e
npm run build
```

The E2E suite uses Playwright with mocked API data and runs the admin console flows in desktop Chromium and a mobile Chromium viewport.

The backend defaults to PostgreSQL storage and dry-run Nomad scheduling. Set `DRY_RUN=false`, `NOMAD_ADDR`, and `PACKAGING_RUNNER_IMAGE` to submit real Nomad jobs.

Nomad runner image:

```bash
cd runner
docker build -t packaging-runner:local .
```

## Environment

Backend variables:

- `PORT` - API port, default `8080`.
- `DATA_DIR` - JSON state directory, default `./data`.
- `PRODUCTS_DIR` - product manifest directory, default `../products`.
- `STORE_DRIVER` - `postgres` by default; set `json` only for local fallback.
- `DATABASE_URL` - default `postgres://packaging:packaging@127.0.0.1:5432/packaging?sslmode=disable`.
- `DRY_RUN` - default `true`.
- `NOMAD_ADDR` - Nomad API address, default `http://127.0.0.1:4646`.
- `NOMAD_REGION` - Nomad region, default `global`.
- `NOMAD_NAMESPACE` - Nomad namespace, default `default`.
- `RUNNER_TIMEOUT_SECONDS` - marks stale running builds failed, default `1800`.
- `RECONCILE_INTERVAL_SECONDS` - Nomad lifecycle sync interval, default `30`.
- `PACKAGING_RUNNER_IMAGE` - Nomad Docker image, default `packaging-runner:local`.
- `PACKAGING_RUNNER_COMMAND` - Nomad runner command, default `bun /app/package-runner.js`.
- `DAGGER_RUNNER_HOST` - optional remote Dagger engine.
- `DAGGER_DOCKER_SOCKET` - Docker socket mounted into Linux runner, default `/var/run/docker.sock`.
- `RUNNER_CACHE_DIR` - base cache directory, default `/var/cache/packaging`.
- `RUNNER_BUN_CACHE_DIR` - host path mounted as Bun cache in Linux runner, default `/var/cache/packaging/bun`.
- `RUNNER_DAGGER_CACHE_DIR` - host path mounted as Dagger cache in Linux runner, default `/var/cache/packaging/dagger`.
- `NOMAD_LINUX_NODE_CLASS` - default `linux-packager`.
- `NOMAD_WINDOWS_NODE_CLASS` - default `windows-packager`.
- `WINDOWS_RUNNER_COMMAND` - default `bun C:\packaging-runner\package-runner.js`.
- `ARTIFACT_STORE` - `local` or `minio`; defaults to `minio` when `MINIO_ENDPOINT` is set, otherwise `local`.
- `MINIO_ENDPOINT`
- `MINIO_PUBLIC_URL` - HTTP base used by runner to download `minio://...` components.
- `MINIO_ACCESS_KEY`
- `MINIO_SECRET_KEY`
- `MINIO_BUCKET`
- `ARTIFACT_ROOT` - local artifact mirror path for test runners.
- `LSAPCTL_BUILD_CMD`
- `DAGGER_LINUX_BASE_IMAGE`
- `DAGGER_LINUX_INSTALL_TOOLS`
- `DAGGER_LINUX_PACKAGE_CMD`
- `LINUX_PACKAGER_CMD`
- `WINDOWS_PACKAGER_CMD`
- `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` and lowercase variants are forwarded to runner tasks.

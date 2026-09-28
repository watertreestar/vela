# Vela

Internal packaging control plane for Linux and Windows installer builds.

## Structure

- `conductor/` - Go API service.
- `studio/` - React, Radix Themes, and Tailwind CSS web console.
- `products/` - Product manifests.
- `infra/nomad/` - Nomad job templates and examples.
- `infra/dagger/` - Dagger packaging workflow example.
- `infra/minio/` - MinIO bucket and artifact conventions.
- `docs/` - Architecture and phase plans.

Key docs:

- [Architecture](docs/architecture.md)
- [Operation Guide](docs/operation-guide.md)
- [Local Acceptance](docs/local-acceptance.md)
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
- `WINDOWS_SIGNING_CMD` - optional Windows signing command; runs after `WINDOWS_PACKAGER_CMD` and before checksum/upload, receives `OUTPUT`, `ARTIFACT`, `BUILD_ID`, `PRODUCT_ID`, `TARGET`, and `VERSION`.

Conductor:

```bash
docker run -d --name packaging-postgres \
  -e POSTGRES_USER=packaging \
  -e POSTGRES_PASSWORD=packaging \
  -e POSTGRES_DB=packaging \
  -p 5432:5432 postgres:16

cd conductor
go run ./cmd/server
```

Studio:

```bash
cd studio
npm install
npm run dev
```

Studio verification:

```bash
cd studio
npm test -- --run
npm run test:e2e
npm run build
```

The E2E suite uses Playwright with mocked API data and runs the admin console flows in desktop Chromium and a mobile Chromium viewport.

One-command local acceptance:

```bash
python scripts/acceptance_cli.py run
```

This command starts the local API if it is not already healthy, checks Docker, builds or reuses `packaging-runner:local`, uploads local component artifacts, creates and approves a Linux Preview build, runs the Docker runner through Dagger, verifies the uploaded package download, validates Preview, and checks the Release gate. PostgreSQL must be reachable through `DATABASE_URL`; Docker must be able to run Linux containers and mount `/var/run/docker.sock`.

By default the script starts the API as a Docker container on `packaging-net` so the runner can reach it reliably. It connects the existing `packaging-postgres` container to that network and publishes the API on `http://127.0.0.1:8080`.

Detailed usage: [Local Acceptance](docs/local-acceptance.md).

Useful variants:

```bash
python scripts/acceptance_cli.py check
python scripts/acceptance_cli.py run --rebuild-runner
python scripts/acceptance_cli.py run --api-mode host --runner-api-url http://host.docker.internal:8080
python scripts/acceptance_cli.py run --api-url http://127.0.0.1:8081 --api-container packaging-api-docker --runner-api-url http://packaging-api-docker:8080
python scripts/acceptance_cli.py run --linux-packager-fixture --rebuild-runner
python scripts/acceptance_cli.py run --artifact-store minio --minio-container packaging-rustfs --minio-endpoint 127.0.0.1:9000 --minio-access-key rustfsadmin --minio-secret-key rustfsadmin
python scripts/acceptance_cli.py run --target windows --rebuild-runner
python scripts/windows_runner_check_cli.py check --packager-command "iscc C:\\packaging-runner\\context\\rendered\\setup.iss" --signing-command "signtool sign /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 %OUTPUT%"
```

RustFS / MinIO-compatible storage check:

```bash
python scripts/rustfs_check.py
python scripts/rustfs_check.py --object-lock --object-lock-bucket packaging-audit-lock
python scripts/rustfs_check.py --production --skip-container-check --endpoint rustfs.example.com:9000 --access-key "$MINIO_ACCESS_KEY" --secret-key "$MINIO_SECRET_KEY" --use-ssl --object-lock --object-lock-bucket packaging-audit-lock --retention-mode COMPLIANCE --retention-days 3650 --legal-hold
```

The RustFS check expects a local container named `packaging-rustfs` by default, endpoint `127.0.0.1:9000`, and credentials `rustfsadmin/rustfsadmin`. It connects the container to Docker `bridge` when needed and runs the conductor integration test that verifies wildcard artifact resolution and `.sha256` sidecar locking.
Use `--production --skip-container-check --object-lock --object-lock-bucket ... --retention-mode ... --retention-days ...` for a managed production-like RustFS/MinIO-compatible bucket; production mode refuses to pass without an explicit object-lock bucket and valid retention policy.

Observability checks:

```bash
python scripts/monitor_cli.py check
python scripts/observability_rules_cli.py check
python scripts/alertmanager_config_cli.py check
python scripts/lifecycle_cli.py check
python scripts/fixture_golden_cli.py check
python scripts/production_readiness_cli.py check
python scripts/artifact_retention_cli.py check --api-url "$PACKAGING_API_URL"
python scripts/notification_routing_cli.py check --api-url "$PACKAGING_API_URL" --route critical=https://alert-router.internal/packaging/critical --route error=https://alert-router.internal/packaging/default
python scripts/correlation_trace_cli.py trace --api-url "$PACKAGING_API_URL" --correlation-id "$PACKAGING_CORRELATION_ID"
python scripts/nomad_check_cli.py check --production --address "$NOMAD_ADDR" --require-linux-node --require-windows-node
python scripts/gitlab_package_check_cli.py check --production --base-url "$GITLAB_PACKAGE_BASE_URL" --token "$GITLAB_PACKAGE_TOKEN" --path "42/engine/1.2.3/engine.tar.gz" --expected-sha256 "$GITLAB_PACKAGE_EXPECTED_SHA256"
python scripts/oidc_check_cli.py check --issuer https://idp.example.test --audience vela --jwks-url https://idp.example.test/certs --token "$OIDC_CHECK_TOKEN" --api-url http://127.0.0.1:8080
python scripts/oidc_check_cli.py check --production --issuer "$OIDC_ISSUER" --audience "$OIDC_AUDIENCE" --jwks-url "$OIDC_JWKS_URL" --discovery-url "$OIDC_ISSUER/.well-known/openid-configuration" --authorization-url "$OIDC_AUTHORIZATION_URL" --token-url "$OIDC_TOKEN_URL" --expected-role "$OIDC_OPERATOR_ROLE" --token "$OIDC_CHECK_TOKEN" --api-url https://packaging-api.example.com
python scripts/oidc_check_cli.py check --api-url https://packaging-api.example.com --broker-live --broker-code "$OIDC_BROKER_CODE" --broker-code-verifier "$OIDC_BROKER_CODE_VERIFIER" --broker-redirect-uri "$OIDC_REDIRECT_URI" --broker-logout --require-session-store
```

`production_readiness_cli.py` is the aggregate gate runner. By default it runs local governance checks, including runner fixture golden artifact checks, and warns for live gates that require external systems. Use `--require-idp-live`, `--require-object-lock-live`, `--require-gitlab-live`, `--require-windows-live`, `--require-nomad-live`, `--require-artifact-retention-live`, and `--require-notification-routing-live` in release readiness automation so skipped live gates fail instead of passing as warnings.
If the optional server-side session/token broker is enabled in production, run the aggregate gate with `--idp-live --broker-live --require-broker`; `--require-broker` fails unless the broker live check is actually executed through the production IdP gate.
For CI/release evidence, use `--json` to print a machine-readable `packaging.production_readiness.report/v1` report, or `--report-file readiness.json` to keep normal confirmation lights while writing the same JSON report. The report includes generated time, aggregate status, exit code, summary counts, and per-gate details with secret-like values redacted.

Prometheus alert rules are stored at `infra/prometheus/vela-rules.yml`.
Alertmanager routing example is stored at `infra/prometheus/alertmanager-vela.yml`.
The `/metrics` endpoint also exports runner cache status, bytes, file counts, and scan truncation indicators parsed from runner cache heartbeats.
`correlation_trace_cli.py` is a read-only incident/debugging helper. It queries audit events, builds, build logs, uploads, and notifications by requestId/correlationId, and can emit `packaging.correlation_trace.report/v1` JSON with `--json`.
Production identity and object-lock acceptance details are documented in `docs/production-readiness.md`.

The conductor defaults to PostgreSQL storage and dry-run Nomad scheduling. Set `DRY_RUN=false`, `NOMAD_ADDR`, and `PACKAGING_RUNNER_IMAGE` to submit real Nomad jobs. When `AUTH_ENABLED=true`, real scheduling also requires `AUTH_RUNNER_TOKEN`; Conductor stores it only in a task-owned Nomad Variable and the allocation template renders it under `$NOMAD_SECRETS_DIR`, never in task environment variables or logs.

Nomad runner image:

```bash
cd runner
docker build -t packaging-runner:local .
```

## Development Conventions

Naming style:

- Go identifiers use MixedCaps, for example `RequestID`, `CorrelationID`, and `BuildLock`.
- API JSON request and response fields use camelCase, for example `requestId`, `correlationId`, `productId`, and `messageKey`.
- Structured log and monitoring fields use snake_case, for example `request_id`, `correlation_id`, `error_code`, and `duration_ms`.
- Error codes use SCREAMING_SNAKE_CASE, for example `BUILD_STATE_CONFLICT` and `RESOURCE_NOT_FOUND`.
- Release gate and error detail IDs use dotted kebab-case, for example `release.fixed-version` and `preview.build-lock`.

Go code should not use snake_case for ordinary identifiers. Keep snake_case for logs, metrics, database-style fields, or external systems that expect it. The current public API contract uses camelCase JSON and should remain stable for studio and client compatibility.

## Environment

Conductor variables:

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
- `NOTIFICATION_RETENTION_DAYS` - read notification retention in days, default `90`; set `0` to disable pruning.
- `PACKAGING_RUNNER_IMAGE` - Nomad Docker image, default `packaging-runner:local`.
- `PACKAGING_RUNNER_COMMAND` - Nomad runner command, default `bun /app/package-runner.js`.
- `DAGGER_RUNNER_HOST` - optional remote Dagger engine.
- `DAGGER_DOCKER_SOCKET` - Docker socket mounted into Linux runner, default `/var/run/docker.sock`.
- `RUNNER_CACHE_DIR` - base cache directory, default `/var/cache/packaging`.
- `RUNNER_BUN_CACHE_DIR` - host path mounted as Bun cache in Linux runner, default `/var/cache/packaging/bun`.
- `RUNNER_DAGGER_CACHE_DIR` - host path mounted as Dagger cache in Linux runner, default `/var/cache/packaging/dagger`.
- `RUNNER_CPU` - Nomad runner task CPU MHz, default `2000`.
- `RUNNER_MEMORY_MB` - Nomad runner task memory MB, default `4096`.
- `RUNNER_MEMORY_MAX_MB` - optional Nomad memory max MB; `0` disables the field.
- `RUNNER_DISK_MB` - Nomad runner task disk and ephemeral disk MB, default `4096`.
- `RUNNER_LOG_MAX_FILES` - Nomad allocation log files retained per stream, default `10`.
- `RUNNER_LOG_MAX_FILE_MB` - Nomad allocation log file size MB, default `20`.
- `RUNNER_KILL_TIMEOUT_SECONDS` - graceful stop timeout, default `30`.
- `RUNNER_MAX_RUN_SECONDS` - hard Nomad allocation run duration, default `3600`.
- `RUNNER_RESTART_ATTEMPTS` - Nomad restart attempts, default `0`.
- `RUNNER_RESCHEDULE_ATTEMPTS` - Nomad reschedule attempts, default `0`.
- `RUNNER_DOCKER_PRIVILEGED` - set to `true` only when a Linux packaging workload requires privileged Docker mode.
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
- `GITLAB_PACKAGE_BASE_URL` - optional GitLab base URL, for example `https://gitlab.example.com`, used to resolve and download GitLab Generic Package Registry component sources.
- `GITLAB_PACKAGE_TOKEN` - optional GitLab token sent as `PRIVATE-TOKEN` when resolving package sources and when runner downloads matching GitLab package URLs.
- `LSAPCTL_BUILD_CMD`
- `DAGGER_LINUX_BASE_IMAGE`
- `DAGGER_LINUX_INSTALL_TOOLS`
- `DAGGER_LINUX_PACKAGE_CMD`
- `LINUX_PACKAGER_CMD`
- `WINDOWS_PACKAGER_CMD`
- `WINDOWS_SIGNING_CMD`
- `AUTH_ENABLED` - default `false`; set to `true` to require bearer tokens on protected write/action/runner/download routes.
- `AUTH_ADMIN_TOKEN` - admin token; can publish releases and perform all protected operations.
- `AUTH_OPERATOR_TOKEN` - operator token; can create/approve/reject/validate/retry/cancel builds, update manifests/overlays, upload artifacts, and download restricted artifacts.
- `AUTH_VIEWER_TOKEN` - viewer token; can download `platform` artifacts when auth is enabled.
- `AUTH_RUNNER_TOKEN` - runner token; required for runner claim/complete/heartbeat/log APIs when auth is enabled and `DRY_RUN=false`. It stays in the Conductor environment and is rendered only into dispatched allocation secrets files.
- `PACKAGING_API_TOKEN_FILE` - safe relative allocation-secrets file name for the runner bearer, default `vela-runner-auth`.
- `RUNNER_CAPABILITY_TTL_SECONDS`, `RUNNER_EXECUTION_HMAC_KEY` - reserved independent capability configuration; the HMAC key stays Conductor-only.
- `OIDC_ISSUER`, `OIDC_AUDIENCE`, `OIDC_JWKS_URL` - optional OIDC/JWKS bearer JWT verification for human users when `AUTH_ENABLED=true`; RS256 tokens must pass issuer, audience, expiry, and signature checks.
- `OIDC_TOKEN_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` - optional server-side OIDC Authorization Code + PKCE token broker settings. `OIDC_CLIENT_SECRET` is only required for confidential clients.
- `OIDC_ROLE_CLAIM` - role claim name, default `roles`.
- `OIDC_SUBJECT_CLAIM` - subject claim used for audit actor id, default `sub`.
- `OIDC_ADMIN_ROLE`, `OIDC_OPERATOR_ROLE`, `OIDC_VIEWER_ROLE` - external role values mapped to platform `admin`, `operator`, and `viewer`; defaults are the same names.
- `AUTH_SESSION_HMAC_KEY`, `AUTH_SESSION_TTL_SECONDS` - optional platform session token signing key and TTL for the server-side OIDC broker. Set a long random key in production.
- `AUTH_SESSION_STORE_ENABLED` - optional `true`/`false`; when `true`, broker-issued platform sessions are persisted in the conductor store and can be revoked through `POST /api/auth/logout`.
- Studio OIDC login uses `VITE_OIDC_AUTHORIZATION_URL` and `VITE_OIDC_CLIENT_ID`; optional browser-side mapping variables are `VITE_OIDC_SCOPE`, `VITE_OIDC_RESPONSE_TYPE`, `VITE_OIDC_TOKEN_URL`, `VITE_OIDC_BROKER_URL`, `VITE_OIDC_REDIRECT_URI`, `VITE_OIDC_ROLE_CLAIM`, `VITE_OIDC_ADMIN_ROLE`, `VITE_OIDC_OPERATOR_ROLE`, and `VITE_OIDC_VIEWER_ROLE`. `VITE_OIDC_RESPONSE_TYPE=code` enables browser Authorization Code + PKCE and requires either `VITE_OIDC_TOKEN_URL` for browser token exchange or `VITE_OIDC_BROKER_URL=/api/auth/oidc/token` for server-side exchange and platform session tokens; `token` keeps the simpler bearer-token callback flow for local or legacy IdP setups.
- `AUDIT_ARCHIVE_HMAC_KEY` - optional key used to add `hmac-sha256` signatures to `/api/audit-events/archive` responses.
- `AUDIT_ARCHIVE_HMAC_KEY_ID` - optional non-secret key identifier included in signed audit archive responses for HMAC key rotation evidence.
- `AUDIT_ARCHIVE_RETENTION_MODE` - optional MinIO/RustFS object-lock mode for audit archives, `GOVERNANCE` or `COMPLIANCE`.
- `AUDIT_ARCHIVE_RETENTION_DAYS` - retention duration for audit archive object lock; applies only with MinIO/RustFS and a retention mode.
- `AUDIT_ARCHIVE_LEGAL_HOLD` - set to `true` to write audit archives with object legal hold enabled when the bucket supports object lock.
- `SECRET_BACKEND` - `env`, `file`, `sops`, `vault`, or `nomad`; defaults to `env`. Resolves `secret:<name>` / `secret://<name>` references only when submitting runner jobs.
- `SECRET_ENV_PREFIX` - environment variable prefix for `SECRET_BACKEND=env`, default `PACKAGING_SECRET_`; `secret:db/root-password` maps to `PACKAGING_SECRET_DB_ROOT_PASSWORD`.
- `SECRET_FILE` - JSON file for `SECRET_BACKEND=file`, for example `{ "db/root-password": "..." }`.
- `SOPS_FILE`, `SOPS_COMMAND`, `SOPS_SECRET_FIELD` - SOPS encrypted JSON backend settings when `SECRET_BACKEND=sops`; defaults to command `sops` and field `value`.
- `VAULT_ADDR`, `VAULT_TOKEN`, `VAULT_NAMESPACE`, `VAULT_KV_MOUNT`, `VAULT_SECRET_FIELD` - Vault KV v2 backend settings. `secret:db/root-password` reads `/v1/<mount>/data/db/root-password` and defaults to field `value`.
- `NOMAD_TOKEN`, `NOMAD_VARIABLE_PATH_PREFIX`, `NOMAD_VARIABLE_ITEM_KEY` - Nomad Variables backend settings when `SECRET_BACKEND=nomad`; uses `NOMAD_ADDR`, `NOMAD_NAMESPACE`, and `NOMAD_REGION`, reads `/v1/var/<prefix>/<secret-ref>`, and defaults to item `value`.
- `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` and lowercase variants are forwarded to runner tasks.

When conductor auth is enabled, use the admin console "访问令牌" dialog to store either a static platform token, an OIDC access token, or a platform session token issued by the optional server-side OIDC broker. The studio injects `Authorization` headers for API requests and protected downloads. Clearing the token calls `POST /api/auth/logout`; persisted broker sessions are revoked when `AUTH_SESSION_STORE_ENABLED=true`, while static/OIDC tokens are treated as client-side logout. Local/dev without a token still works when `AUTH_ENABLED=false`. Runner APIs should continue to use `AUTH_RUNNER_TOKEN`; OIDC roles are intended for human admin/operator/viewer identities.

Secret references stay as references in Manifest and Build Lock. The API resolves them at Nomad submit time and injects `PACKAGING_SECRET_*` environment variables into the runner; the runner replaces references while rendering the packaging workspace. Approve/retry writes a `secret.resolve` audit event with referenced secret names and counts, never secret values.

Audit archive exports are persisted through the configured artifact store under `audit-archives/sha256/<digest>.json`; release evidence exports are persisted under `release-evidence/sha256/<digest>.json`. MinIO-compatible storage returns a `minio://...` archive URI, while local development returns `local-audit-archive://...` or `local-release-evidence://...`. For production WORM retention, create or use a RustFS/MinIO bucket with object lock enabled, then configure `AUDIT_ARCHIVE_RETENTION_MODE`, `AUDIT_ARCHIVE_RETENTION_DAYS`, and optionally `AUDIT_ARCHIVE_LEGAL_HOLD=true`.

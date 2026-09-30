# Vela Plans

Last updated: 2026-06-25

This file is the current execution plan and status board. It reflects the repository state after the latest frontend, Manifest management, release gate, API automation, runner, AuthN/AuthZ, storage, and secret-management work.

## Phase 7 PR-4 evidence

Implemented automatic auth-enabled runner protocol fixture (`packaging.runner_e2e.report/v1`), aggregate readiness skipped/require live gate, and Windows raw_exec secret-directory/readiness diagnostics. Fixture evidence is deliberately a warning rather than deployed Nomad success; real Linux/Windows Nomad allocation validation remains an external live gate and must use the documented human operator/admin-token command. Reports redact bearer/capability/header/query-secret material.

## Current Focus

Make the admin console usable for configuring product manifests, creating preview builds, validating preview installers, and promoting validated builds to release without changing build scripts. P1 governance explicitly includes AuthN/AuthZ, access control, auditability, notifications, metrics, and secret handling.

## Status Summary

| Area | Status | Notes |
|---|---|---|
| Repository initialization | Done | Repository has been initialized and committed. `.idea` should stay untracked. |
| Frontend UI baseline | Done | Admin console now uses local shadcn/Radix-style primitives and compact dashboard styling. |
| Manifest JSON editing | Done | Full JSON editor, validation, save, revision list, restore, diff, dirty-state guard. |
| Manifest structured editing | Done | Product fields, targets, package names, templates, config defaults, modules, components, source kinds, and workloads are editable. |
| Component source configuration | Done | Supports `artifact-repository`, `manual-upload`, `fixed-url`, and `previous-build` in UI/API. |
| Manifest validation alignment | Done | Frontend now blocks missing target `packageName`, matching backend validation. |
| Manifest dry run | Done | Draft Manifest can be resolved before saving/building, with gate messaging. |
| Overlay management | Done | Profile/customer overlay list, edit, save, create, dirty-state guards. |
| Build creation UI | Done | Step-based form with target/profile/customer/version/config/component overrides. |
| Preview/release gate | Done | Release promotion requires Preview validation and backend release gate checks. |
| Artifact metadata | Done | Upload metadata, checksum, download policy, retention status, and download URL are shown. |
| Upload storage | Done | Local storage and MinIO-compatible storage are supported by backend configuration. |
| Runner logs and observability | Done | Build logs, runner heartbeat/status, current build state, and recent messages are visible. |
| AuthN/AuthZ | Partial | Local/dev remains unauthenticated by default; production token mode, OIDC/JWKS bearer JWT verification MVP, optional server-side OIDC token broker, persisted platform session store/logout, role checks, runner token checks, frontend token state, OIDC Authorization Code + PKCE UX, structured 401/403, download policy enforcement, and strict live-check tooling are implemented as a P1 foundation. |
| API error contract | Done | Structured API errors, frontend translation, toast/alert/panel surface rules documented and implemented for main flows. |
| API automation | Done | Backend API tests cover core workflows, release gates, uploads, retention, logs, and errors. |
| Frontend automation | Done | Unit tests and Playwright E2E tests cover dashboard, release gates, upload history, build creation, token storage/header injection, and viewer permission gating. |
| Local acceptance flow | Done | Python acceptance CLI documents and runs local API -> runner -> Dagger -> artifact -> Preview gate flow. |
| Runner deployment docs | Done | Linux/Windows runner and Nomad lifecycle documentation exists. |
| Observability rules | Done MVP | Prometheus alert rules, Alertmanager routing examples, and Python validators cover API, build queue, release gate, runner heartbeat, runner availability, runner cache scan truncation, and artifact retention. |
| Operation documentation | Done | Detailed operation guide and `.http` request examples exist under `docs/`. |

## Feature Milestones

| Milestone | Status | Completion | Acceptance criteria | Current evidence | Remaining work |
|---|---|---:|---|---|---|
| Admin dashboard baseline | Done | 100% | PC admin console uses compact dashboard UI, Chinese labels, working navigation, no fake toolbar actions, and no Radix Themes remnants. | Frontend unit tests, Playwright E2E, and desktop visual regression screenshots pass; UI migrated to local shadcn/Radix-style primitives. | Keep screenshot baselines updated when intentional UI changes land. |
| Manifest self-configuration | Done | 90% | User can configure product id/name, targets, package names, templates, profiles, versions, modules, components, source kinds, workloads, and defaults without changing build scripts. | JSON editor, structured editor, validation, dry-run, revision restore, diff, dirty guards, and studio/backend packageName validation alignment exist. | Add structured controls for uncommon advanced Docker/systemd nested fields if they become frequent. |
| Component source resolution | In progress | 90% | `artifact-repository`, `manual-upload`, `fixed-url`, and `previous-build` resolve into Build Lock entries; repository sources become exact URI + sha256 before release. | Manual upload returns URI/SHA; `ARTIFACT_ROOT` exact/wildcard resolver done; RustFS/MinIO exact/wildcard resolver done; `.sha256` sidecar supported; GitLab Generic Package Registry exact-file resolver and runner token download support exist; RustFS live check passed; `scripts/gitlab_package_check_cli.py` provides exact-file GitLab live acceptance and is wired into `production_readiness_cli.py`. | Run live GitLab package registry acceptance against production source packages and decide whether GitLab wildcard/listing support is required. |
| Build creation and approval flow | Done | 95% | User can create Preview builds, approve/reject/cancel/retry, inspect build details, and runner can claim/complete tasks. | Build API, admin UI, runner claim/complete APIs, cancel/retry, logs, acceptance CLI, and Nomad production job policy defaults exist. | Harden lifecycle reconciliation with a dedicated Nomad acceptance scenario. |
| Preview validation and Release gate | Done | 97% | Release cannot be published until Preview is validated; release gate blocks latest version, wildcard component URI, missing artifact, build error, and inconsistent Build Lock. | Shared backend release-check powers UI, `validate-preview`, and `publish`; API tests cover blocked release cases; reproducibility report endpoint/UI/export and signed release evidence archive exist. | Run production release evidence export against the final object-lock bucket and signing key policy. |
| Artifact metadata and retention | Done | 92% | Uploads persist URI, download URL, sha256, size, content type, download policy, retention days/status, and expired artifacts cannot be downloaded. | Upload API, retention handling, UI metadata display, monitor checks, `artifact_retention_cli.py` live API behavior check, role-aware upload download policy enforcement, build download linked-upload policy enforcement, and build/upload download audit tests exist. | Run artifact retention live acceptance against staging/prod API and tune expired-artifact thresholds. |
| AuthN/AuthZ and access control | Partial | 95% | Local/dev can run without auth; production mode requires authenticated identity; roles cover admin/operator/viewer/runner; build approve/publish/cancel/retry, Manifest writes, uploads, downloads, and runner APIs enforce permissions; frontend can store a bearer token and disables actions by role; API returns structured 401/403 errors; audit events include actor identity; production IdP/broker checks must be live, not config-only. | `AUTH_ENABLED` token mode, OIDC/JWKS RS256 JWT verification, configurable issuer/audience/JWKS/role claim mapping, optional `/api/auth/oidc/token` server-side code+PKCE broker with signed platform session tokens, optional `AUTH_SESSION_STORE_ENABLED` persisted session store, `POST /api/auth/logout` session revocation, route-level role checks, runner token checks, structured `AUTH_REQUIRED`/`AUTH_FORBIDDEN`, frontend i18n, frontend token storage/request header injection/logout call, configurable frontend OIDC login redirect/callback UX, Authorization Code + PKCE token exchange, broker exchange support, permission-aware action buttons, authenticated download fetch, restricted/platform/public download enforcement for upload and linked build downloads, actor-aware audit events, conductor/frontend unit tests, Playwright auth token/header, OIDC callback, and viewer gating scenarios, and `scripts/oidc_check_cli.py` production OIDC discovery/configuration/live-token checks plus strict endpoint/expected-role/JWT-time-window checks and optional broker live exchange/logout/revocation checks exist. | Run live production identity provider acceptance and decide whether refresh-token rotation is required beyond short-lived persisted platform sessions. |
| RustFS / MinIO-compatible storage | Done MVP | 98% | Local RustFS/MinIO-compatible object store can be used for source artifact resolution, final artifact upload, and optional audit archive object-lock verification. | Backend supports `ARTIFACT_STORE=minio`; `scripts/rustfs_check.py` verifies local RustFS endpoint, wildcard source resolution, exact URI, sidecar sha256, optional `--object-lock` audit archive retention/legal-hold evidence, and `--production --skip-container-check` managed bucket validation requiring an explicit object-lock bucket plus retention mode/days policy; `acceptance_cli.py run --artifact-store minio` live-verified API -> runner -> upload -> Preview gate flow with final `minio://` artifact URI. | Run scheduled/live object-lock acceptance against a production-like bucket and record final retention policy values from compliance/operations. |
| Linux runner packaging | Done MVP | 90% | Linux runner downloads locked components, renders config, runs Dagger, packages tar.gz, uploads artifact and checksum, and reports logs/cache snapshots. | Docker runner, Dagger pipeline, local acceptance CLI, runner docs, cache snapshot logs, heartbeat cache summaries, Python cache report/cleanup CLI, `/metrics` runner cache metrics, and fixture golden CLI exist. | Add real product archive matrix after production sample packages are standardized. |
| Windows runner packaging | Partial | 76% | Windows target can produce a non-placeholder setup artifact for Preview/control-plane acceptance, and the runner exposes a signing hook before checksum/upload, while production release still requires a real Windows packager host and certificate chain. | Windows raw_exec path, `WINDOWS_PACKAGER_CMD` hook, `WINDOWS_SIGNING_CMD` hook, deterministic Docker runner setup/signing fixtures, marker/build-id artifact verification, `scripts/windows_runner_check_cli.py` production host readiness checks, and script tests exist; fallback placeholder remains clearly marked for control-plane testing. | Run Windows live acceptance on a production-like host with Inno Setup or WiX and real signing certificate. |
| Error handling and user feedback | Done | 96% | Backend returns structured errors; frontend maps codes to Chinese messages and chooses toast/alert/panel surfaces consistently. | Error-handling docs, `apperr`, API error responses, frontend i18n, tests for key errors, request/correlation IDs persisted in audit events/build logs/upload metadata, and `scripts/correlation_trace_cli.py` can query cross-resource traces. | Keep adding code-specific i18n messages as new API error codes are introduced. |
| API automation | Done | 85% | Core API workflows, release gates, uploads, retention, logs, and errors are covered by automated tests. | `go test ./...` passes from `conductor/`. | Add live RustFS acceptance to regular optional CI job. |
| Frontend automation | Done | 95% | Admin console key flows are covered by unit and E2E tests, including auth token storage/header injection, OIDC callback handling, OIDC Code + PKCE helper coverage, logout call coverage, viewer permission gating, audit center archive evidence, and desktop screenshot regression. | 67 frontend unit tests, 17 Playwright E2E tests plus 1 skipped mobile-only visual case, desktop screenshot baselines, and frontend production build passed on 2026-06-25. | Add more page-specific screenshots when complex Manifest drawers/panels are expanded. |
| Operations documentation | Done | 85% | User can follow docs for local flow, API examples, runner deployment, storage, and troubleshooting. | README, operation guide, local acceptance doc, platform `.http`, runner lifecycle docs. | Keep docs synced after RustFS full acceptance and reproducibility report. |
| Production governance | Partial | 98% | AuthN/AuthZ, enforced download permissions, independent audit events, audit archive digest/signing/storage/object-lock metadata, signed release evidence archives, correlation IDs, notifications, secret management, Prometheus alert rules, Alertmanager routing examples, and an aggregate production readiness gate are implemented. | Token-based auth mode, OIDC/JWKS JWT auth MVP, strict production OIDC discovery/readiness checker, optional server-side OIDC token broker with platform session tokens, persisted platform session store/logout/revocation live check, role checks, frontend OIDC bearer callback plus Authorization Code + PKCE UX, auth-gated upload/build download policy, artifact retention live API check, notification severity routing dry-run/send check, independent audit event store/API, frontend audit center with filter/export/evidence UI, HMAC-signed audit archive export with key id, signed `release-evidence/sha256/<digest>.json` archive export with UI button and audit event, digest-addressed archive persistence to local/MinIO-compatible artifact store, optional MinIO/RustFS object-lock retention/legal-hold evidence and production-mode live check with explicit retention policy, GitLab package exact-file live check, Nomad leader/node-class live check, notification store/API/UI MVP, runner offline notifications, storage unavailable notifications, read notification retention, env/file/SOPS/Vault/Nomad Variables secret resolver, secret resolution audit events, request/correlation IDs, build logs, metrics endpoint, Prometheus alert rules, Alertmanager routing examples, `scripts/production_readiness_cli.py` aggregate gate runner, and `docs/production-readiness.md` exist. | Execute live production IdP acceptance, execute live object-lock bucket acceptance, execute live GitLab package acceptance if GitLab is a production source, execute live Nomad cluster, artifact retention, and notification routing acceptance against the final environment, set final production retention policy values, and add native cloud KMS backend only if the production key-management standard requires it. |

## Recently Completed

- Completed structured Manifest component source editing:
  - `artifact-repository.repository`
  - `artifact-repository.path`
  - `manual-upload.uploadId`
  - `manual-upload.uri`
  - `manual-upload.accept`
- Aligned frontend Manifest validation with backend validation for target `packageName`.
- Started real artifact repository resolution:
  - resolver now supports an optional artifact source resolver.
  - local `ARTIFACT_ROOT` can resolve exact files and wildcard artifact paths.
  - resolved artifact URI and sha256 are captured into the Build Lock.
  - RustFS/MinIO-compatible resolver path resolves exact objects and wildcard objects.
  - RustFS live integration check verifies wildcard resolution and `.sha256` sidecar locking.
  - `scripts/rustfs_check.py` provides a Python check CLI for local RustFS verification.
- Completed Secret management MVP:
  - Manifest and Build Lock keep `secret:<name>` / `secret://<name>` references instead of plaintext.
  - API resolves env/file/SOPS/Vault KV v2/Nomad Variables secret backends at Nomad submit time and injects `PACKAGING_SECRET_*` environment variables.
  - Runner replaces secret references inside final config, variables, and workloads before rendering packaging workspace.
  - Missing secret references fail before packaging starts in real runner execution.
  - Approve/retry records `secret.resolve` audit events with secret refs/count/stage/target but never secret values.
- Added Prometheus alert rules and validation:
  - `infra/prometheus/vela-rules.yml` covers API availability, runner availability, runner heartbeat, build queue backlog, failed build volume, release gate blockage, and expired artifacts.
  - `scripts/observability_rules_cli.py check` validates required alerts and metric references.
- Added Alertmanager routing examples:
  - `infra/prometheus/alertmanager-vela.yml` routes critical, runner, release-gate, artifact, and build alerts to separate receivers.
  - `scripts/alertmanager_config_cli.py check` validates required receivers, matchers, webhook URLs, and receiver references.
- Added Audit archive/signing MVP:
  - `GET /api/audit-events/archive` exports filtered audit events with a canonical SHA-256 digest.
  - `AUDIT_ARCHIVE_HMAC_KEY` enables `hmac-sha256` signatures on archive responses.
  - Archive payloads are persisted under `audit-archives/sha256/<digest>.json` in local or MinIO-compatible artifact storage.
  - Archive exports write an `audit.archive.export` audit event after the archive payload is generated.
- Hardened Nomad production job policy:
  - API-created Nomad jobs now include task resources, ephemeral disk, allocation log rotation, kill timeout, max run duration, restart policy, and reschedule policy.
  - Defaults favor explicit platform retry: `RUNNER_RESTART_ATTEMPTS=0` and `RUNNER_RESCHEDULE_ATTEMPTS=0`.
  - `infra/nomad/packaging-runner.nomad.hcl`, README, and runner lifecycle docs describe the same policy knobs.
- Added lifecycle reconciliation coverage:
  - Backend lifecycle tests cover Nomad failed allocation, premature Nomad complete, runner heartbeat timeout, terminal-build safety, and scheduler inspect errors.
  - Runner heartbeat timeout reconciliation records durable `runner.offline` notifications with runner/build/timeout metadata.
  - `scripts/lifecycle_cli.py check` provides a Python confirmation-light entry point for local/CI lifecycle reconciliation validation.
- Added Linux Dagger fixture/golden path:
  - `runner/fixtures/linux_package_packager.py` creates a deterministic `.tar.gz` with marker, build metadata, component/workload counts, and context checksums.
  - `acceptance_cli.py run --linux-packager-fixture` injects the fixture command into Dagger and verifies downloaded marker/build id.
  - Runner copies the Linux fixture into the Dagger context so it executes inside the Dagger container.
  - `scripts/fixture_golden_cli.py check` verifies Linux/Windows fixture artifact determinism, metadata, checksums, and signing evidence without starting the API or Docker runner.
- Added Windows signing hook contract:
  - Runner executes `WINDOWS_SIGNING_CMD` after Windows setup creation and before checksum/upload.
  - API/Nomad scheduling reads and forwards `WINDOWS_SIGNING_CMD` to Windows runner jobs.
  - `runner/fixtures/windows_signing_fixture.py` appends deterministic signing evidence for local/CI acceptance.
  - `acceptance_cli.py run --target windows` injects and verifies both setup and signing fixture markers by default.
- Added Windows production host readiness checks:
  - `scripts/windows_runner_check_cli.py check` validates Windows host readiness, Packaging API and node class config, runner command, production `WINDOWS_PACKAGER_CMD`, fixture rejection, signing requirements, and Inno/WiX/SignTool availability.
  - README, local acceptance docs, runner docs, and operation guide describe running the check before treating a Windows Preview as a production Release candidate.
- Added storage unavailable notifications:
  - Upload, download, and audit archive storage failures create durable `storage.unavailable` notifications.
  - Notification metadata records the failed operation and error summary for operations triage.
- Added notification retention:
  - Lifecycle reconciliation prunes read notifications older than `NOTIFICATION_RETENTION_DAYS`, default 90.
  - Unread notifications are retained until an operator marks them read.
- Verified:
  - backend Go tests: `go test ./...` passed from `conductor/` on 2026-06-25
  - Python script tests: `python -m unittest discover scripts "*_test.py"` passed, 41 tests on 2026-06-25
  - frontend unit tests: `npm run test -- --run` passed, 64 tests on 2026-06-25
  - Playwright E2E: `npm run test:e2e` passed, 17 tests plus 1 skipped mobile-only visual case on 2026-06-25
  - frontend production build: `npm run build` passed on 2026-06-25
  - runner entry build check: `bun build package-runner.js --target=bun` passed on 2026-06-25
  - `git diff --check`: passed on 2026-06-25 with only existing LF/CRLF warnings in `docs/platform-api.http`, `scripts/acceptance_cli.py`, and `scripts/acceptance_cli_test.py`
  - RustFS live check passed with `python scripts/rustfs_check.py`
  - Windows fixture acceptance passed with `python scripts/acceptance_cli.py run --target windows --store-driver json --postgres-container "" --no-pull-missing-images --runner-timeout 600 --namespace acceptance-windows-fixture-20260624`

## Open Work

### P0 - Core Flow Hardening

| Task | Status | Why it matters | Next action |
|---|---|---|---|
| Real artifact repository resolution | In progress | `artifact-repository` must resolve into exact immutable Build Lock entries before preview/release. | Run `scripts/gitlab_package_check_cli.py check --production ...` against production GitLab package sources if GitLab will be a production source, and keep UI source-path conventions current. |
| Windows real setup build | Partial | Windows runner supports `WINDOWS_PACKAGER_CMD` and `WINDOWS_SIGNING_CMD`; local/CI acceptance now defaults to deterministic setup/signing fixtures instead of the placeholder; production host readiness can be checked with `windows_runner_check_cli.py`, while live production acceptance still needs a real Windows host, installer tooling, and certificate chain. | Run a real Inno Setup or WiX pipeline on a Windows host and perform live signing acceptance. |
| Release reproducibility report | Done | Build Lock, release gate, source artifacts, final artifact metadata, runner identity, lifecycle timestamps, signed digest, optional HMAC signature/key id, archive URI, and object-lock metadata are exposed as release evidence. | Run production export with final retention/signing policy. |
| End-to-end RustFS/MinIO acceptance | Done MVP | The automated local acceptance path can start the API with MinIO-compatible storage, run the Docker runner, upload final artifacts through the API, and assert a `minio://` final artifact URI. Live run passed against local `packaging-rustfs`. | Add scheduled/live CI run against a managed RustFS/MinIO-compatible service. |

### Core Task Breakdown

#### 1. Real artifact repository resolution

| Subtask | Status | Notes |
|---|---|---|
| Define resolver boundary | Done | `resolver.ArtifactSourceResolver` keeps build resolution independent from storage implementation. |
| Preserve legacy URI fallback | Done | When no artifact resolver backend is configured, `artifact-repository` still builds the old `repo://path` URI. |
| Resolve local `ARTIFACT_ROOT` exact files | Done | Backend can stat local mirrored artifacts and compute sha256. |
| Resolve local `ARTIFACT_ROOT` wildcard files | Done | Backend selects newest matching file and stores exact URI + sha256. |
| Capture resolved URI/SHA in Build Lock | Done | `ResolvedComponent.URI` and `ResolvedComponent.SHA256` are populated by resolver output. |
| MinIO/RustFS stat exact object | Done | Uses configured MinIO client and returns `minio://bucket/object`. |
| MinIO/RustFS wildcard list object | Done | Lists by prefix and chooses newest matching object. |
| MinIO/RustFS checksum source | Done | Reads `sha256` metadata first, then falls back to `.sha256` sidecar. |
| GitLab package registry resolver | Done MVP | Supports exact GitLab Generic Package Registry file resolution through `GITLAB_PACKAGE_BASE_URL` and optional `GITLAB_PACKAGE_TOKEN`; API computes sha256 and runner sends `PRIVATE-TOKEN` only to matching GitLab package URLs. `scripts/gitlab_package_check_cli.py` can live-check exact package availability and expected sha256. |
| UI convention help for repository/path | Done MVP | Manifest component source fields now show concise MinIO/RustFS and GitLab path hints; operation docs describe repository/path conventions and wildcard limits. |

#### 2. Windows real setup build

| Subtask | Status | Notes |
|---|---|---|
| Windows runner raw_exec lifecycle | Partial | Nomad raw_exec path and docs exist. |
| `WINDOWS_PACKAGER_CMD` hook | Done | Runner delegates real setup creation when configured. |
| Placeholder fallback clearly marked | Done | Useful for control-plane tests only. |
| Deterministic Docker runner fixture | Done MVP | `runner/fixtures/windows_setup_packager.py` writes a reproducible `setup.exe` fixture with marker, build id, metadata, and context checksums. |
| Fixture-backed Windows acceptance path | Done MVP | `acceptance_cli.py run --target windows` injects setup and signing fixture commands and verifies downloaded artifact markers/build id. |
| Production Inno Setup or WiX pipeline | Not done | Needs a Windows runner host with real installer tooling. |
| Windows runner host readiness check | Done MVP | `scripts/windows_runner_check_cli.py` checks Windows host, API/node-class/runner env, production packager command, fixture rejection, signing requirement, and Inno/WiX/SignTool availability. |
| Windows live acceptance test | Partial | Script/unit coverage and host readiness checks exist; a scheduled Windows-host acceptance job is still needed. |
| Signing hook contract | Done MVP | Runner supports `WINDOWS_SIGNING_CMD`, API/Nomad forwards it, local fixture verifies hook order before checksum/upload; real certificate storage/rotation remains production hardening. |

#### 3. Release reproducibility report

| Subtask | Status | Notes |
|---|---|---|
| Build Lock generation | Done | Captures config, components, workloads, target/profile/version. |
| Release gate uses Build Lock | Done | Release blocks latest, wildcard URI, missing artifact, and failed build. |
| Report API endpoint | Done | `GET /api/builds/{id}/reproducibility-report` returns Build Lock, release check, source artifacts, final artifact metadata, runner, and lifecycle timestamps. |
| Report UI panel/export | Done | Build detail includes a 可复现报告 tab with gate summary, output artifact evidence, source component locks, lifecycle, runner info, JSON download, and signed evidence export. |
| Report `.json` download | Done | UI downloads the generated report JSON as release evidence. |
| Signed release evidence archive | Done | `GET /api/builds/{id}/release-evidence/archive` returns `packaging.release.evidence/v1`, digest, optional HMAC signature/key id, archive URI, object-lock metadata, and writes `release.evidence.archive.export`. |

#### 4. RustFS/MinIO local acceptance

| Subtask | Status | Notes |
|---|---|---|
| MinIO-compatible artifact store | Done | Backend supports `ARTIFACT_STORE=minio`. |
| Local RustFS container recipe | Done | `scripts/rustfs_check.py` validates the default local `packaging-rustfs` container. |
| Seed source artifacts into bucket | Done MVP | RustFS integration test uploads deterministic component fixtures; acceptance CLI MinIO mode verifies final artifact upload to RustFS/MinIO-compatible storage. |
| Acceptance mode flag | Done | `python scripts/acceptance_cli.py run --artifact-store minio` configures API/runner MinIO env and validates final `minio://` artifact URI. |
| Validate resolved component SHA | Done for resolver integration | RustFS integration test asserts exact URI and sha256; full runner acceptance validates final artifact storage URI. |

### P1 - Production Governance

| Task | Status | Why it matters | Next action |
|---|---|---|---|
| Authentication and authorization | Partial | Current local/dev flow can still skip auth, while production token mode, OIDC/JWKS bearer JWT mode, optional server-side OIDC code+PKCE broker, persisted platform session store, and logout/revoke protect high-risk APIs; the frontend can launch OIDC login, process bearer-token callbacks, exchange Authorization Code + PKCE callbacks through browser token endpoint or backend broker, attach bearer tokens, call logout, and disable actions by role; Playwright covers token/header, OIDC callback, and viewer-gating flows; `oidc_check_cli.py` can check discovery, JWKS, token claims, JWT time window, HTTPS readiness, expected role, backend token acceptance, and optional broker live exchange/logout/revocation. | Run live production IdP acceptance and decide whether refresh-token rotation is required beyond short-lived persisted platform sessions. |
| Download permissions | Done MVP | `downloadPolicy` is persisted, displayed, and enforced for upload downloads when auth is enabled; build download reuses linked upload policy and records `build.download` audit events before redirecting. | Extend policy checks if future non-upload build download proxy endpoints are added. |
| Independent audit events | Done MVP | Separate audit event store/API records actor, role, request/correlation IDs, resources, and action metadata for critical operations; frontend audit center can filter events and export archives with digest/signature/object-lock evidence; archive export returns digest, optional HMAC signature/key id, persisted archive URI, and optional MinIO/RustFS object-lock evidence. | Run live object-lock bucket acceptance in production-like storage, set production retention policy, and add broader runner/system events. |
| Correlation ID persistence | Done MVP | API middleware normalizes request/correlation IDs; audit events, build/runner request logs, and upload artifact metadata persist both fields for create/approve/reject/validate/publish/retry/cancel/claim/log/heartbeat/complete/upload/download flows; `correlation_trace_cli.py` queries audit/build/upload/notification records by requestId/correlationId. | Add frontend visual trace explorer only if operators need UI-based incident triage. |
| Notification center | Done MVP | Durable notifications now cover scheduler submit failure, build failure, Preview ready, Preview validated, Release published, Runner offline heartbeat timeout, storage unavailable events, read/unread state, configurable read-notification retention, and Python-based severity routing to external webhook receivers. | Run notification routing live acceptance with final webhook receivers and decide whether backend-native async dispatch is required. |
| Secret management | Done MVP | Manifest and Build Lock keep `secret:<name>` references; API resolves env/file/SOPS/Vault KV v2/Nomad Variables secrets at Nomad submit time; runner replaces references inside the packaging workspace; approve/retry writes secret access audit events without values. | Add native cloud KMS backend only if production standard requires it; document final rotation runbook. |

#### 5. AuthN/AuthZ and access control

| Subtask | Status | Notes |
|---|---|---|
| Define auth mode configuration | Done | `AUTH_ENABLED=false` keeps local/dev open; `AUTH_ENABLED=true` enables token checks. |
| Define principals and roles | Done MVP | Token roles cover `admin`, `operator`, `viewer`, and `runner`; runner identity is separate from human roles. |
| API identity extraction | Done MVP | Bearer token extraction exists for static tokens, OIDC/JWKS RS256 JWTs, and signed platform session tokens from the optional OIDC broker; `oidc_check_cli.py` validates discovery, JWKS shape, token claims, JWT expiry/nbf, HTTPS production readiness, expected role, backend acceptance, broker live exchange, persisted session id, logout, and revoked-token rejection. |
| Structured auth errors | Done | `AUTH_REQUIRED` maps to 401 and `AUTH_FORBIDDEN` maps to 403 with frontend Chinese messages. |
| Protect Manifest and overlay writes | Done MVP | Manifest save/restore and overlay writes require operator/admin in auth mode. |
| Protect build actions | Done MVP | Create, approve, reject, validate-preview, retry, and cancel require operator/admin; publish requires admin. |
| Protect artifact uploads and downloads | Done MVP | Upload requires operator/admin; `public/platform/restricted` download policies are enforced for upload downloads and linked build downloads when auth is enabled. |
| Protect runner APIs | Done MVP | Claim, complete, heartbeat, and log append require runner/admin in auth mode. |
| Attach actor to audit trail | Done MVP | Manifest/Overlay writes, build actions, uploads, upload downloads, and build download requests persist actor id/role plus request/correlation IDs. |
| Frontend auth state and permission UI | Done MVP | Admin console stores bearer token locally, can launch configured OIDC login, processes OIDC bearer token callbacks, browser Authorization Code + PKCE callbacks, and backend broker callbacks, injects Authorization headers, shows current frontend role, disables protected actions by role, and downloads artifacts through authenticated fetch when needed. |
| Automated tests | Partial | Backend tests cover missing token 401, wrong role 403, OIDC JWT verification, invalid OIDC signatures, runner token boundary, server-side OIDC broker session token issuance, restricted upload download policy, and linked build download policy/audit; Python tests cover OIDC check CLI discovery/production/broker checks; frontend unit tests cover auth i18n, auth header injection, OIDC URL/callback helpers, browser PKCE token exchange, broker exchange, token UI, viewer permission gating, and upload auth headers; Playwright covers token save/header injection, OIDC callback storage, and viewer protected-action gating. |

#### 6. Audit archive and signing

| Subtask | Status | Notes |
|---|---|---|
| Filtered archive export API | Done MVP | `GET /api/audit-events/archive` accepts the same filters as `/api/audit-events`. |
| Canonical digest | Done | Archive response includes `digestAlgorithm=sha256` and `digest` over the canonical payload. |
| Optional signature | Done MVP | `AUDIT_ARCHIVE_HMAC_KEY` enables `signatureAlgorithm=hmac-sha256` and `signature`; `AUDIT_ARCHIVE_HMAC_KEY_ID` records a non-secret key id for rotation evidence. |
| Export audit event | Done | Exporting an archive records `audit.archive.export` after payload generation. |
| Automated tests | Done MVP | Backend test verifies auth requirement, event filtering, digest recomputation, HMAC signature, and export audit event. |
| Digest-addressed archive storage | Done MVP | Exported archive payloads are persisted to local or MinIO-compatible artifact storage as `audit-archives/sha256/<digest>.json`; response and export audit event include `archiveUri`. |
| Immutable external archive storage | Partial | Digest-addressed MinIO/RustFS object persistence exists; optional object-lock retention mode/days and legal hold are applied and returned as archive evidence when configured; `scripts/rustfs_check.py --object-lock` can live-verify retention/legal hold, and `--production --skip-container-check` validates an explicitly named production-like bucket plus explicit retention mode/days policy. |
| Key management and rotation | Partial | Archive responses can include `signatureKeyId` so rotated HMAC keys are identifiable; HMAC key material should still move to Vault/SOPS/Nomad Variables/cloud KMS with a documented rotation policy. |

#### 7. Secret management

| Subtask | Status | Notes |
|---|---|---|
| Secret reference format | Done | Supports `secret:<name>` and `secret://<name>` in Manifest-derived config, variables, workloads, and Build Lock fields. |
| Preserve references in persisted artifacts | Done MVP | Manifest, Build Lock, audit, and reproducibility evidence should retain references instead of plaintext secret values. |
| Env secret backend | Done | `SECRET_BACKEND=env` maps refs to `PACKAGING_SECRET_*` variables, for example `secret:db/root-password` -> `PACKAGING_SECRET_DB_ROOT_PASSWORD`. |
| File secret backend | Done | `SECRET_BACKEND=file` reads a JSON object from `SECRET_FILE`. |
| SOPS secret backend | Done MVP | `SECRET_BACKEND=sops` decrypts `SOPS_FILE` through `SOPS_COMMAND --decrypt --output-type json`, supports exact keys and nested JSON fields, and keeps plaintext only in process memory before runner env injection. |
| Vault KV v2 backend | Done MVP | `SECRET_BACKEND=vault` reads `GET /v1/<VAULT_KV_MOUNT>/data/<secret-ref>` with `VAULT_TOKEN` and optional `VAULT_NAMESPACE`, using `VAULT_SECRET_FIELD` default `value`. |
| Nomad submit integration | Done | Real Nomad job creation resolves refs and injects secret values into runner task environment. |
| Runner workspace replacement | Done | Runner resolves refs from environment before rendering final config, variables, and workloads into the packaging workspace. |
| Missing secret failure | Done | Missing secret config fails job creation or runner startup before packaging. |
| Production backend | Done MVP | Vault KV v2, Nomad Variables, and SOPS encrypted file backends exist; native cloud KMS backend remains optional production hardening if required by the target environment. |
| Secret access audit | Done MVP | Approve/retry records `secret.resolve` events with actor, build/product, stage, target, secret count, and referenced secret names; values and runner env values are not recorded. |

### P2 - Runner and Operations

| Task | Status | Why it matters | Next action |
|---|---|---|---|
| Nomad production job policy | Done MVP | Scheduler submits jobs with kernel/node-class constraints, cache mounts, resource limits, ephemeral disk, log rotation, kill timeout, max run duration, and explicit restart/reschedule policy. | Run `nomad_check_cli.py` and one real preview job against the final production node pools. |
| Dagger fixture-based testing | Done MVP | Local acceptance can run Dagger with a deterministic Linux fixture package and validate marker/build id; Windows has deterministic setup/signing fixture coverage; `fixture_golden_cli.py` validates deterministic artifacts, metadata, checksums, and signing evidence as a local governance gate. | Add broader matrix for multiple base images and real product golden archives after production samples are standardized. |
| Runner cache observability | Done MVP | Runner writes cache snapshots to build logs, heartbeat summaries, workspace JSON, host-side Python CLI reports, and Prometheus cache metrics for entries, bytes, files, and scan truncation. | Tune cache thresholds once production runner cache paths and expected sizes are known. |
| Metrics endpoint and alert rules | Done MVP | Prometheus-style `/metrics` endpoint exposes build states, release gate blockers, upload retention/policy, runner availability, running builds, heartbeat age, and runner cache metrics; alert rules and Alertmanager routing examples cover API, runner, runner cache truncation, build, release gate, and artifact retention risks. | Add production-specific threshold tuning and real receiver integration after topology is fixed. |
| Lifecycle reconciliation coverage | Done MVP | Nomad lifecycle monitor is covered by deterministic fake-scheduler tests and a Python confirmation-light check script for local/CI validation; `nomad_check_cli.py` validates live leader/ready nodes/node classes; `production_readiness_cli.py` can run lifecycle, observability, IdP, object-lock, GitLab, Windows, and Nomad readiness gates as one release-readiness command. | Execute live Nomad cluster acceptance after production topology is fixed. |

### P3 - Frontend Polish

| Task | Status | Why it matters | Next action |
|---|---|---|---|
| Manifest editor coverage for every advanced schema field | Partial | Common fields are covered; raw JSON remains the full-fidelity path for uncommon nested fields. | Add structured editors for advanced Docker/systemd fields if users need them often. |
| Large-form ergonomics | Partial | Build creation is step-based; Manifest/product forms could use more drawer/dialog split flows. | Split complex Manifest editing into focused panels or drawers. |
| Mobile polish | Deferred | User explicitly prioritized PC desktop. | Revisit after desktop flow stabilizes. |
| Visual regression testing | Done MVP | Playwright captures desktop screenshots for overview, build list, create-build component step, and audit center. | Extend screenshot coverage when new critical desktop pages or drawers are added. |

## Current Task Check

The current task is to harden the core release/build flow and track milestone completion. Result:

- Done: documented feature milestones with status, completion estimate, acceptance criteria, current evidence, and remaining work.
- Done: confirmed the admin console can self-configure core Manifest fields without raw JSON for common workflows.
- Done: confirmed studio/backend Manifest validation now agree on target package names.
- Done: implemented and live-verified RustFS/MinIO-compatible artifact source resolution for exact/wildcard object paths and `.sha256` sidecar checksums.
- Done: implemented and live-verified `acceptance_cli.py run --artifact-store minio` against local `packaging-rustfs`; final artifact URI was `minio://...` and Release gate passed.
- Done: implemented and live-verified Windows fixture acceptance; final artifact was a `*-setup.exe` fixture, marker/build-id download verification passed, Preview validation passed, and Release gate blocking count was `0`.
- Done: implemented Windows signing hook contract and deterministic signing fixture; local/CI Windows acceptance can now verify setup marker, signing marker, build id, Preview validation, and Release gate.
- Done: implemented AuthN/AuthZ foundation, OIDC/JWKS verification, optional server-side OIDC code+PKCE broker, persisted platform session store/logout, route-level role checks, download policy enforcement, and auth-aware frontend state.
- Done: implemented independent audit events, audit archive digest/HMAC signing, digest-addressed archive storage, optional RustFS/MinIO object-lock evidence, correlation ID persistence, notification center, metrics endpoint, Prometheus rules, Alertmanager examples, and Secret management MVP with env/file/SOPS/Vault KV v2/Nomad Variables backends.
- Done: added `scripts/production_readiness_cli.py` as an aggregate production readiness gate runner for local governance checks plus explicit IdP, object-lock, GitLab package, and Windows runner live gates.
- Done: added machine-readable `packaging.production_readiness.report/v1` output to `production_readiness_cli.py` with `--json`, `--report-file`, summary counts, per-gate details, exit code, and secret-like detail redaction for release evidence archival.
- Done: added `scripts/nomad_check_cli.py` for live Nomad leader, ready-node, and Linux/Windows node-class readiness, and wired it into `production_readiness_cli.py` through `--nomad-live` / `--require-nomad-live`.
- Done: added `scripts/artifact_retention_cli.py` to validate upload metadata contracts and expired artifact download rejection, and wired it into `production_readiness_cli.py` through `--artifact-retention-live` / `--require-artifact-retention-live`.
- Done: added `scripts/notification_routing_cli.py` to validate notification severity routing and optionally dispatch matching notifications to webhook receivers, and wired it into `production_readiness_cli.py` through `--notification-routing-live` / `--require-notification-routing-live`.
- Done: added `scripts/correlation_trace_cli.py` to trace requestId/correlationId across audit events, builds, build logs, uploads, and notifications, with confirmation lights and `packaging.correlation_trace.report/v1` JSON output.
- Done: added `scripts/fixture_golden_cli.py` and wired it into production readiness local governance to verify deterministic Linux package, Windows setup, and Windows signing fixture artifacts.
- Done: hardened `--require-broker` so the aggregate gate fails unless broker live acceptance is actually run with `--idp-live --broker-live`.
- Done: added signed Release evidence archive export, persisted under `release-evidence/sha256/<digest>.json`, with frontend export button and audit event.
- Done: fixed the Playwright visual regression gate and added desktop screenshot baselines for overview, builds, create-build component step, and audit center.
- Done: added runner cache Prometheus metrics from cache heartbeat/log summaries and a `VelaRunnerCacheScanTruncated` alert.
- Done: refreshed automated verification on 2026-06-25: backend Go tests, Python script tests, frontend unit tests, Playwright E2E, frontend build, runner entry build, aggregate local production-readiness governance, and diff check all passed. Playwright E2E passed 17 tests with the existing mobile-only visual regression case skipped by test policy.
- Not committed yet: current P0/P1/P2 implementation, docs, tests, plans update, and generated/supporting scripts remain in the working tree.
- Remaining: production Windows Inno/WiX packaging on a Windows host with real certificate signing, live production IdP acceptance, optional refresh-token rotation beyond short-lived persisted platform sessions, native cloud KMS secret backend if required by production standards, production object-lock/WORM retention policy and key rotation, and live external acceptance for GitLab/object-lock/Nomad/notification receivers where production infrastructure is required.

## Suggested Next Step

Next best local step: add a real product archive matrix once production sample packages are standardized, or implement native cloud KMS backend if production standards require it. Production Windows Inno/WiX signing, production IdP acceptance, live object-lock bucket acceptance, and live Nomad/GitLab acceptance still require external systems.

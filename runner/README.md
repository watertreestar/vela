# Runner

The runner is intentionally thin. It pulls approved build tasks from the Packaging API and reports logs/results back.

Responsibilities:

- claim queued build tasks
- download service artifacts
- compile `lsapctl`
- package Linux `tar.gz`
- package Windows `setup.exe`
- upload final artifacts
- report logs and completion status

## Run Modes

### 1. Run Once Locally

Use this mode to debug one queued build without Nomad.

```bash
PACKAGING_API_URL=http://localhost:8080 \
TARGET=linux \
MINIO_PUBLIC_URL=http://localhost:9000 \
bun package-runner.js
```

The runner claims one queued task. If no task is available it exits with `no queued build`.

Local smoke test checklist:

1. Start the API with `DRY_RUN=true` and a local artifact store.
2. Create and approve one preview build from the admin console.
3. Run the command above with `TARGET=linux` or `TARGET=windows`.
4. Confirm the build moves from `queued` to `running` to `succeeded`.
5. Open the build detail page and check logs, artifact URI, download URL, and release gate status.

### 2. Linux Runner Through Nomad

Linux production topology:

```text
Packaging API -> Nomad server -> Linux Nomad client -> Docker runner image -> Dagger -> API upload endpoint
```

Host prerequisites:

- Linux host registered as a Nomad client.
- Docker installed and reachable by the Nomad task.
- Docker socket mounted into the runner when using Dagger's default Docker engine driver.
- Dagger CLI and Docker CLI available in the runner image or through the configured runner command.
- Network access from the runner to `PACKAGING_API_URL` and `MINIO_PUBLIC_URL`.
- Stable cache directories for Bun, Dagger, and mirrored artifacts.

Build the image on every Linux packager node or push it to an internal registry:

```bash
docker build -t packaging-runner:local .
```

Prepare cache directories on each Linux runner host:

```bash
sudo mkdir -p /var/cache/packaging/{bun,dagger,artifacts}
sudo chmod -R 0777 /var/cache/packaging
```

The Packaging API submits a Nomad Docker task for Linux builds. The task runs:

```bash
bun /app/package-runner.js
```

The API controls this with:

```bash
PACKAGING_RUNNER_IMAGE=packaging-runner:local
PACKAGING_RUNNER_COMMAND='bun /app/package-runner.js'
NOMAD_LINUX_NODE_CLASS=linux-packager
DAGGER_DOCKER_SOCKET=/var/run/docker.sock
```

The Nomad Linux client must have Docker and either a mounted Docker socket for Dagger or a reachable remote Dagger engine.

Recommended Linux install sequence:

```bash
# On the runner host
sudo nomad agent -config /path/to/infra/nomad/linux-client.hcl

# On the API host
export DRY_RUN=false
export NOMAD_ADDR=http://<nomad-server>:4646
export PACKAGING_API_URL=http://<api-host>:8080
export PACKAGING_RUNNER_IMAGE=packaging-runner:local
export NOMAD_LINUX_NODE_CLASS=linux-packager
export RUNNER_BUN_CACHE_DIR=/var/cache/packaging/bun
export RUNNER_DAGGER_CACHE_DIR=/var/cache/packaging/dagger
export ARTIFACT_ROOT=/var/cache/packaging/artifacts
export DAGGER_DOCKER_SOCKET=/var/run/docker.sock
```

For production Linux packages, set `LSAPCTL_SOURCE_DIR` and `LSAPCTL_BUILD_CMD` so the final tarball includes `lsapctl` at the package root:

```bash
export LSAPCTL_SOURCE_DIR=/opt/vela/lsapctl
export LSAPCTL_BUILD_CMD='cd /workspace/context/lsapctl-src && GOOS=linux GOARCH=amd64 go build -o "$OUTPUT_DIR/lsapctl" .'
```

### 3. Windows Runner Through Nomad

Windows builds run through Nomad `raw_exec`, not Docker. This lets the runner use host-installed Inno Setup, WiX, SignTool, certificates, and vendor tools.

Windows production topology:

```text
Packaging API -> Nomad server -> Windows Nomad client raw_exec -> Bun runner -> Inno Setup/WiX/SignTool -> API upload endpoint
```

Host prerequisites:

- Windows Server or a dedicated Windows workstation.
- Nomad client running as Administrator.
- Bun installed and available in `PATH`.
- Inno Setup or WiX installed for real `setup.exe` output.
- Optional SignTool and signing certificate configured on the host.
- Network access to the Packaging API and MinIO public URL.

Prepare the Windows runner host:

```powershell
powershell -ExecutionPolicy Bypass -File infra\windows\prepare-runner.ps1 `
  -RunnerDir C:\packaging-runner `
  -ApiUrl http://<api-host>:8080 `
  -MinioPublicUrl http://<minio-host>:9000 `
  -WindowsPackagerCmd 'iscc C:\packaging-runner\context\rendered\setup.iss'
```

The API controls the Windows Nomad task with:

```bash
NOMAD_WINDOWS_NODE_CLASS=windows-packager
WINDOWS_RUNNER_COMMAND='bun C:\packaging-runner\package-runner.js'
```

Recommended Windows install sequence:

```powershell
New-Item -ItemType Directory -Force -Path C:\packaging-cache\bun | Out-Null
New-Item -ItemType Directory -Force -Path C:\packaging-cache\artifacts | Out-Null

[Environment]::SetEnvironmentVariable("BUN_INSTALL_CACHE_DIR", "C:\packaging-cache\bun", "Machine")
[Environment]::SetEnvironmentVariable("ARTIFACT_ROOT", "C:\packaging-cache\artifacts", "Machine")
[Environment]::SetEnvironmentVariable("HTTP_PROXY", "", "Machine")
[Environment]::SetEnvironmentVariable("HTTPS_PROXY", "", "Machine")
[Environment]::SetEnvironmentVariable("NO_PROXY", "localhost,127.0.0.1,<api-host>,<minio-host>,<nomad-host>", "Machine")
```

Then start the Windows Nomad client from an Administrator PowerShell:

```powershell
powershell -ExecutionPolicy Bypass -File infra\windows\start-nomad-client.ps1 `
  -NomadExe C:\tools\nomad\nomad.exe `
  -ServerAddr <nomad-server-ip>:4647
```

Production Windows builds should set `WINDOWS_PACKAGER_CMD`. Without it, the runner only creates a clearly marked placeholder file for local control-plane testing. If release candidates require code signing, set `WINDOWS_SIGNING_CMD` as well. The signing command runs after the setup file is created and before checksum/upload, so the uploaded digest always describes the signed artifact.

Example Inno Setup command:

```powershell
WINDOWS_PACKAGER_CMD='iscc C:\packaging-runner\context\rendered\setup.iss'
WINDOWS_SIGNING_CMD='signtool sign /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 "$env:OUTPUT"'
```

Before using a Windows host for Release candidates, run the Python readiness check on that host:

```powershell
python scripts\windows_runner_check_cli.py check `
  --api-url http://<api-host>:8080 `
  --packager-command "iscc C:\packaging-runner\context\rendered\setup.iss" `
  --signing-command "signtool sign /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 %OUTPUT%"
```

The check fails if production packaging would fall back to the fixture packager/signing command, if `WINDOWS_PACKAGER_CMD` is missing, if signing is required but missing, or if the expected Inno/WiX/SignTool executables are not visible from the runner host. Use `--no-require-signing` only for an explicitly unsigned Preview environment; Release candidate hosts should keep signing required.

For local or CI acceptance without a Windows packager host, the Docker runner image includes a deterministic fixture packager:

```bash
WINDOWS_PACKAGER_CMD='python3 /app/fixtures/windows_setup_packager.py'
WINDOWS_SIGNING_CMD='python3 /app/fixtures/windows_signing_fixture.py'
```

The packager fixture reads the generated runner workspace, writes a `*-setup.exe` fixture artifact, and embeds the marker `PACKAGING_PLATFORM_WINDOWS_SETUP_FIXTURE_V1` plus build metadata and context checksums. The signing fixture appends marker `PACKAGING_PLATFORM_WINDOWS_SIGNING_FIXTURE_V1` plus deterministic signing metadata. They are intended for Preview/control-plane validation only; production Release builds should use the real Inno/WiX/signing toolchain.

Linux builds use Dagger by default. The runner generates `context/rendered/pipeline-plan.json` from the build lock, final config, and resolved components, then runs:

```bash
dagger run -- bun runner/dagger/linux-package.mjs ...
```

Set `USE_DAGGER_FOR_LINUX=false` for local fallback packaging.

For local or CI acceptance of the Linux Dagger path, the Docker runner image includes a deterministic Linux package fixture. The runner copies it into the Dagger context, so the command runs inside the Dagger container:

```bash
DAGGER_LINUX_PACKAGE_CMD='python3 /workspace/context/rendered/linux_package_packager.py'
```

The fixture writes a `.tar.gz` artifact containing marker `PACKAGING_PLATFORM_LINUX_PACKAGE_FIXTURE_V1`, build metadata, component/workload counts, and context checksums. With the default Alpine base image, install Python for the fixture with:

```bash
DAGGER_LINUX_INSTALL_TOOLS='apk add --no-cache python3 tar gzip coreutils bash'
```

Fixture golden checks can be run without starting the API or Docker runner:

```bash
python scripts/fixture_golden_cli.py check
```

The check runs the Linux package fixture, Windows setup fixture, and Windows signing fixture twice against deterministic workspaces, then verifies artifact digests, markers, metadata, component/workload counts, and signing evidence.

It is intended for Dagger/control-plane validation only; production Release builds should use the real product packaging command.

In production, Nomad starts this runner on Linux or Windows builder nodes. The API submits a batch job whose Docker image is controlled by `PACKAGING_RUNNER_IMAGE` and whose command defaults to `bun /app/package-runner.js`.

## Environment

Core:

- `PACKAGING_API_URL` - Packaging API URL.
- `RUNNER_ID` - stable runner identifier.
- `TARGET` - `linux` or `windows`.
- `ARTIFACT_ROOT` - local artifact mirror for test runners.
- `MINIO_PUBLIC_URL` - HTTP base URL used to download `minio://...` component URIs.
- `GITLAB_PACKAGE_BASE_URL` - optional GitLab base URL; runner only attaches GitLab auth headers to downloads under this URL.
- `GITLAB_PACKAGE_TOKEN` - optional token sent as `PRIVATE-TOKEN` for GitLab Generic Package Registry component downloads.
- `HEARTBEAT_INTERVAL_MS` - runner heartbeat interval, default `30000`.
- `PACKAGING_API_TOKEN_FILE` - safe relative file name in `$NOMAD_SECRETS_DIR` containing the Runner bearer; default `vela-runner-auth`. The bearer is never an environment variable.
- `RUNNER_AUTH_REQUIRED` - set to `true` by auth-enabled Nomad jobs. The runner fails before any API call if the secret file is absent or empty. Omit it for the auth-disabled local mode.

Auth-enabled Nomad allocations receive the bearer through a task-owned Nomad Variable, workload identity, and a `template` rendered under the allocation secrets directory. Linux uses template mode `0400`; Windows relies on Nomad's allocation secrets-directory ACL. Do not add `AUTH_RUNNER_TOKEN` to `runner.env`, host environment variables, logs, or workspace files.
- `LSAPCTL_BUILD_CMD` - optional `lsapctl` compile command. Linux runs it inside the Dagger container; Windows runs it on the Windows runner host before `WINDOWS_PACKAGER_CMD`.
- `LSAPCTL_SOURCE_DIR` - optional local source directory copied into the build context as `lsapctl-src`.

Linux Dagger:

- `USE_DAGGER_FOR_LINUX` - default `true`; set `false` for local fallback packaging.
- `DAGGER_RUNNER_HOST` - optional remote Dagger engine.
- `DAGGER_LINUX_BASE_IMAGE_SOURCE` - `registry` by default; set `host` to use a Docker image already present on the runner host, or set `DAGGER_LINUX_BASE_IMAGE_TAR`.
- `DAGGER_LINUX_BASE_IMAGE` - default `alpine:3.20`.
- `DAGGER_LINUX_BASE_IMAGE_TAR` - optional OCI/Docker image tar path visible to the Dagger client; avoids registry pulls for offline/reproducible builds.
- `DAGGER_LINUX_BASE_IMAGE_TAR_TAG` - optional tag to select when the tar contains multiple image tags.
- `DAGGER_LINUX_INSTALL_TOOLS` - default installs `tar`, `gzip`, `coreutils`, and `bash`; set `false`, `none`, or `skip` when the base image already contains required tools.
- `DAGGER_LINUX_PACKAGE_CMD` - optional product-specific command inside the Dagger container.
- `LINUX_PACKAGER_CMD` - optional custom Linux packaging command.

Windows:

- `WINDOWS_PACKAGER_CMD` - optional custom Windows packaging command, usually an Inno Setup or WiX invocation.
- `OUTPUT` - set by the runner when invoking `WINDOWS_PACKAGER_CMD`; the packager must write the final `setup.exe` to this path.
- `WINDOWS_SIGNING_CMD` - optional Windows signing command, usually a SignTool invocation. It runs after packaging and before checksum/upload.
- `OUTPUT` / `ARTIFACT` - set by the runner when invoking `WINDOWS_SIGNING_CMD`; both point to the setup file that should be signed in place or replaced at the same path.
- `BUILD_ID`, `PRODUCT_ID`, `TARGET`, and `VERSION` - also set for `WINDOWS_SIGNING_CMD` so signing wrappers can write deterministic evidence logs without reading runner internals.

Secret references:

- Manifest values may contain `secret:<name>` or `secret://<name>`.
- The API resolves these references before Nomad submission and injects `PACKAGING_SECRET_*` environment variables into the runner.
- The runner replaces secret references in `finalConfig`, `variables`, and `workloads` before rendering runtime files and package context.
- Example: `secret:db/root-password` is read from `PACKAGING_SECRET_DB_ROOT_PASSWORD`.
- If the conductor uses a custom `SECRET_ENV_PREFIX`, Nomad forwards that prefix to the runner and the runner reads the matching environment variable names.
- Missing secret env values fail the build before packaging starts.

Proxy variables are supported by normal process inheritance and are passed to `fetch`, `bun`, Dagger, and child commands:

- `HTTP_PROXY`
- `HTTPS_PROXY`
- `NO_PROXY`
- lowercase variants: `http_proxy`, `https_proxy`, `no_proxy`

Recommended `NO_PROXY` values:

```text
localhost,127.0.0.1,<packaging-api-host>,<minio-host>,<nomad-host>
```

Cache variables:

- `BUN_INSTALL_CACHE_DIR` - Bun dependency cache.
- `XDG_CACHE_HOME` - common Linux cache root.
- `DAGGER_CACHE_DIR` - optional local directory you can mount/use for Dagger-related cache metadata.
- `ARTIFACT_ROOT` - local component mirror for offline/dev builds.
- `RUNNER_CACHE_SCAN_FILE_LIMIT` - maximum files scanned per cache path when reporting cache status, default `5000`.

For Nomad Linux Docker tasks, cache is most useful when the host mounts stable directories into the runner container. Example paths:

```text
/var/cache/packaging/bun
/var/cache/packaging/dagger
/var/cache/packaging/artifacts
```

For Windows raw_exec, use persistent host paths:

```text
C:\packaging-cache\bun
C:\packaging-cache\artifacts
```

Runtime cache observability:

- The runner records cache snapshots at `before`, `components`, and `after` stages.
- Each snapshot is appended to the build log as `cache <stage>: bun=...; dagger=...; artifacts=...; components=...`.
- The same summary is sent through heartbeat messages so `/api/runners` recent messages show cache health.
- The API `/metrics` endpoint parses the latest cache summaries into `packaging_runner_cache_entries`, `packaging_runner_cache_bytes`, `packaging_runner_cache_files`, and `packaging_runner_cache_scan_truncated`.
- Full JSON snapshots are written into the runner workspace under `context/rendered/runner-cache-<stage>.json`.
- Missing or unconfigured cache paths are reported as `missing` or `unconfigured`; large paths are capped by `RUNNER_CACHE_SCAN_FILE_LIMIT` and marked with `+ files`.

Host-side cache inspection and cleanup use the Python CLI:

```bash
python scripts/runner_cache_cli.py report \
  --path bun=/var/cache/packaging/bun \
  --path dagger=/var/cache/packaging/dagger \
  --path artifacts=/var/cache/packaging/artifacts

# Dry-run cleanup; prints what would be deleted.
python scripts/runner_cache_cli.py cleanup --path artifacts=/var/cache/packaging/artifacts

# Actual cleanup; deletes children of the cache directory, not the directory itself.
python scripts/runner_cache_cli.py cleanup --apply --path artifacts=/var/cache/packaging/artifacts
```

Cleanup refuses paths that do not look like cache or packaging paths, to reduce accidental deletion risk.

Default behavior:

- Linux creates a `tar.gz` from the generated build context. Production packages should set `LSAPCTL_SOURCE_DIR` and `LSAPCTL_BUILD_CMD` so `lsapctl` is written to the context root before packaging.
- Windows creates a placeholder setup file unless `WINDOWS_PACKAGER_CMD` is configured.
- Local/CI Windows acceptance can set `WINDOWS_PACKAGER_CMD='python3 /app/fixtures/windows_setup_packager.py'` and `WINDOWS_SIGNING_CMD='python3 /app/fixtures/windows_signing_fixture.py'` to generate a deterministic signed fixture instead of the placeholder.
- Both targets upload the resulting file through the Packaging API upload endpoint.

## Deployment Verification

After deploying a runner, verify the full control-plane loop:

1. Confirm the Nomad client appears with the expected node class:
   - Linux: `linux-packager`
   - Windows: `windows-packager`
2. Create a preview build for the matching target.
3. Approve the build from the admin console.
4. Confirm the build receives a `nomadJobId`.
5. Confirm runner logs show task claim, component downloads, packaging, checksum upload, and artifact upload.
6. Confirm the build reaches `succeeded`.
7. Run Preview validation in the admin console and inspect the release gate checks.
8. For release candidates, confirm the version and component URIs are fixed before promoting to Release.

Useful checks:

```bash
nomad node status
nomad job status <job-id>
nomad alloc logs <allocation-id>
```

Windows runner local check:

```powershell
powershell -ExecutionPolicy Bypass -File C:\packaging-runner\run-once.ps1
```

## Upgrade and Rollback

Linux Docker runner:

1. Build and push a new image tag, for example `packaging-runner:2026-05-28`.
2. Update `PACKAGING_RUNNER_IMAGE` on the API host.
3. Restart the API so newly approved builds use the new image.
4. Keep the previous image tag available for rollback.

Windows raw_exec runner:

1. Re-run `infra\windows\prepare-runner.ps1` with the new runner files.
2. Keep a copy of the previous `C:\packaging-runner` directory.
3. Run `run-once.ps1` against a test queued build.
4. Restart the Nomad client only after the local check succeeds.

Rollback means restoring the previous image tag or previous `C:\packaging-runner` directory, then retrying failed or canceled builds from the admin console.

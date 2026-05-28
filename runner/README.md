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

### 2. Linux Runner Through Nomad

Build the image on every Linux packager node or push it to an internal registry:

```bash
docker build -t packaging-runner:local .
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

### 3. Windows Runner Through Nomad

Windows builds run through Nomad `raw_exec`, not Docker. This lets the runner use host-installed Inno Setup, WiX, SignTool, certificates, and vendor tools.

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

Linux builds use Dagger by default. The runner generates `context/rendered/pipeline-plan.json` from the build lock, final config, and resolved components, then runs:

```bash
dagger run -- bun runner/dagger/linux-package.mjs ...
```

Set `USE_DAGGER_FOR_LINUX=false` for local fallback packaging.

In production, Nomad starts this runner on Linux or Windows builder nodes. The API submits a batch job whose Docker image is controlled by `PACKAGING_RUNNER_IMAGE` and whose command defaults to `bun /app/package-runner.js`.

## Environment

Core:

- `PACKAGING_API_URL` - Packaging API URL.
- `RUNNER_ID` - stable runner identifier.
- `TARGET` - `linux` or `windows`.
- `ARTIFACT_ROOT` - local artifact mirror for test runners.
- `MINIO_PUBLIC_URL` - HTTP base URL used to download `minio://...` component URIs.
- `HEARTBEAT_INTERVAL_MS` - runner heartbeat interval, default `30000`.
- `LSAPCTL_BUILD_CMD` - optional `lsapctl` compile command. Linux runs it inside the Dagger container; Windows runs it on the Windows runner host before `WINDOWS_PACKAGER_CMD`.
- `LSAPCTL_SOURCE_DIR` - optional local source directory copied into the build context as `lsapctl-src`.

Linux Dagger:

- `USE_DAGGER_FOR_LINUX` - default `true`; set `false` for local fallback packaging.
- `DAGGER_RUNNER_HOST` - optional remote Dagger engine.
- `DAGGER_LINUX_BASE_IMAGE` - default `alpine:3.20`.
- `DAGGER_LINUX_INSTALL_TOOLS` - default installs `tar`, `gzip`, `coreutils`, and `bash`.
- `DAGGER_LINUX_PACKAGE_CMD` - optional product-specific command inside the Dagger container.
- `LINUX_PACKAGER_CMD` - optional custom Linux packaging command.

Windows:

- `WINDOWS_PACKAGER_CMD` - optional custom Windows packaging command, usually an Inno Setup or WiX invocation.

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

Default behavior:

- Linux creates a `tar.gz` from the generated build context. Production packages should set `LSAPCTL_SOURCE_DIR` and `LSAPCTL_BUILD_CMD` so `lsapctl` is written to the context root before packaging.
- Windows creates a placeholder setup file unless `WINDOWS_PACKAGER_CMD` is configured.
- Both targets upload the resulting file through the Packaging API upload endpoint.

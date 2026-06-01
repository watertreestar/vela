# Packaging Platform Architecture

## Architecture

```mermaid
flowchart LR
  User[Product / Release User] --> UI[Radix Web Console]
  UI --> API[Go Packaging API]

  API --> Store[(PostgreSQL<br/>builds, uploads, manifests)]
  API --> ProductRepo[(Product Manifests<br/>products/*)]
  API --> Nomad[Nomad Scheduler]
  API --> MinIO[(MinIO<br/>artifacts + installers)]

  Nomad --> LinuxRunner[Linux Runner<br/>Docker + Bun + Dagger]
  Nomad --> WindowsRunner[Windows Runner<br/>Bun + Inno Setup + SignTool]

  LinuxRunner --> API
  WindowsRunner --> API
  LinuxRunner --> MinIO
  WindowsRunner --> MinIO

  SourceRepos[Service Repos / CI Artifacts] --> MinIO
  Upload[Manual Uploads] --> MinIO
```

## Data Flow

```mermaid
sequenceDiagram
  participant User
  participant UI
  participant API
  participant Store
  participant Nomad
  participant Runner
  participant MinIO

  User->>UI: select product, target, version, profile, config overrides
  UI->>API: POST /api/builds
  API->>Store: save build as pending_approval
  API-->>UI: build record

  User->>UI: approve build
  UI->>API: POST /api/builds/{id}/approve
  API->>Nomad: submit batch job
  API->>Store: status=queued, nomadJobId=...

  Nomad->>Runner: start Linux or Windows runner
  Runner->>API: POST /api/runner/tasks/claim
  API->>Store: status=running, runnerId=...
  API-->>Runner: build lock + final config + component URIs

  Runner->>API: POST /api/runner/heartbeat
  Runner->>MinIO: download selected components or manual uploads
  Runner->>Runner: render config and generate Dagger plan
  Runner->>Runner: Dagger packages Linux tar.gz / Windows raw_exec builds setup.exe
  Runner->>API: POST /api/uploads for package + checksum
  API->>MinIO: store package when ARTIFACT_STORE=minio
  Runner->>API: POST /api/runner/tasks/{id}/complete
  API->>Store: status=succeeded, artifactUri, downloadUrl

  User->>UI: validate preview installer
  UI->>API: POST /api/builds/{id}/validate-preview
  API->>Store: status=preview_validated
  User->>UI: promote to release
  UI->>API: POST /api/builds/{id}/publish
  API->>Store: status=published, releaseChannel=release
```

## Build State Machine

```mermaid
stateDiagram-v2
  [*] --> pending_approval
  pending_approval --> queued: approve
  pending_approval --> rejected: reject
  pending_approval --> canceled: cancel
  queued --> running: runner claim
  queued --> canceled: cancel
  running --> canceled: cancel
  running --> succeeded: runner complete
  running --> failed: runner failed
  succeeded --> preview_validated: validate preview
  preview_validated --> published: promote release
  failed --> queued: retry
  canceled --> queued: retry
  queued --> failed: nomad allocation failed
  running --> failed: heartbeat timeout / allocation failed
  rejected --> [*]
  published --> [*]
```

## Configuration Resolution

```mermaid
flowchart TD
  Base[Product Base Manifest] --> Merge[Config Resolver]
  Target[Target Defaults<br/>linux/windows] --> Merge
  Profile[Profile Overlay<br/>qa/prod] --> Merge
  Customer[Customer Overlay] --> Merge
  Pipeline[Build Variables] --> Merge
  Manual[UI Overrides] --> Merge

  Merge --> Final[Final Build Manifest]
  Final --> Linux[Render Linux manifest<br/>compose.yml, config files]
  Final --> Windows[Render Windows setup<br/>ISS/WiX, config files]
  Final --> Runner[Runner Execution Context]
```

Recommended precedence:

```text
UI overrides > pipeline variables > customer overlay > profile overlay > target defaults > product base
```

## Component Source Flow

```mermaid
flowchart LR
  Component[Component Definition] --> Source{Source Type}
  Source --> Repo[Repository Artifact<br/>GitLab/MinIO latest or version]
  Source --> Upload[Manual Upload]
  Source --> Fixed[Fixed URL / Object Path]

  Repo --> Resolve[Version Resolver]
  Upload --> Resolve
  Fixed --> Resolve
  Resolve --> Lock[build-lock.json]
  Lock --> Package[Installer Build]
```

The lock file is critical. Every build should persist the exact resolved component versions and artifact object paths.

## Workload Rendering Flow

```mermaid
flowchart TD
  Manifest[Product Manifest workloads] --> Resolver[Build Resolver]
  Config[Final Config modules/customer/ports/installDir] --> Resolver
  Resolver --> Lock[Build Lock workloads with effective enabled flags]
  Lock --> Plan[pipeline-plan.json]
  Plan --> Docker[Render docker-compose.yml]
  Plan --> Systemd[Render systemd/*.service]
  Plan --> Runtime[Render lsapctl-runtime.json]
  Docker --> Package[Linux tar.gz]
  Systemd --> Package
  Runtime --> Package
```

Components define where artifacts come from. Workloads define how artifacts run after installation. Docker and systemd are both first-class workload runtimes.
`lsapctl` is the only package lifecycle manager; generated runtime files are inputs to `lsapctl`, not competing operational scripts.

## Current MVP Coverage

| Requirement | Current status | Notes |
|---|---:|---|
| Product list | Implemented | `GET /api/products`, UI product list |
| Manifest/config management | Implemented | JSON editor, schema validation, dry-run resolver preview, revisions and restore |
| Version selection | Implemented basic | Product manifest has `versions` |
| Build parameters | Implemented | target/profile/customer/channel/version/config/modules/component sources |
| Build records | Implemented | PostgreSQL default storage, JSON fallback available |
| Preview/release gate | Implemented | preview artifact must be validated before release promotion |
| Download address | Implemented | `artifactUri`, `downloadUrl`, build download endpoint |
| Logs | Implemented | runner/system logs and heartbeat |
| Runner claims task | Implemented | `POST /api/runner/tasks/claim` |
| Linux packaging | Implemented MVP | Runner downloads components, renders config, runs optional `LSAPCTL_BUILD_CMD`, creates `tar.gz` |
| Windows packaging | Implemented MVP | Runner supports `WINDOWS_PACKAGER_CMD`; fallback marks placeholder setup output |
| Component repo selection | Implemented MVP | UI and API support repository/manual/fixed/previous-build source kinds |
| Manual upload | Implemented MVP | Upload endpoint, PostgreSQL upload records, local file storage adapter |
| Config override without script changes | Implemented MVP | Resolver merges target/profile/customer/build overrides into build lock |

Release gate enforcement is centralized through the build release check API. The same check set powers the admin dashboard, `validate-preview`, and `publish`, so the UI can show exactly why a build is blocked before the backend rejects the action. Preview validation checks artifact availability, Build Lock presence, Build Lock target/profile/version consistency, and required component locks. Release publishing adds fixed build versions, fixed component versions, exact component URIs without wildcards, artifact availability, and no recorded build error.

## Target Capability

The core goal is that users can change the following without changing build scripts:

- ports
- service versions
- default install directory
- customer name
- license
- database password policy
- module enablement
- component artifact source
- Linux/Windows packaging target

This requires a declarative packaging manifest and renderer pipeline, not hardcoded build scripts.

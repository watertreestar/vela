# Dagger Packaging Pipeline

Linux packaging is executed by the Bun runner through Dagger:

```text
Nomad -> package-runner.js -> generated pipeline-plan.json -> dagger run -> runner/dagger/linux-package.mjs
```

The runner still owns API lifecycle actions:

- claim task
- write logs and heartbeat
- upload final artifact and checksum
- complete task

Dagger owns the Linux build sandbox:

- mount generated build context
- validate the generated plan
- render Docker Compose from `docker` workloads
- render systemd unit files from `systemd` workloads
- render `lsapctl-runtime.json` as the single runtime management manifest
- install packaging tools in the Dagger container
- run optional `LSAPCTL_BUILD_CMD`
- run optional product package command
- default to `tar.gz` packaging when no product command is provided
- export the generated package back to the runner output directory

`lsapctl` is the only supported lifecycle manager inside the package. Dagger must not generate independent `install.sh`, `start.sh`, `stop.sh`, or `uninstall.sh` scripts because those would bypass `lsapctl` and split operational behavior.

## Generated Plan

The runner generates:

```text
context/rendered/pipeline-plan.json
```

from the API build record:

- `buildLock`
- `finalConfig`
- resolved component files
- build identity and release channel
- optional environment commands

Plan fields:

```json
{
  "schemaVersion": "packaging.dagger/v1",
  "buildId": "abc123",
  "productId": "lsap",
  "target": "linux",
  "version": "latest",
  "packageName": "lsap-linux-latest-abc123.tar.gz",
  "baseImage": "alpine:3.20",
  "installToolsCommand": "apk add --no-cache tar gzip coreutils bash",
  "lsapctlBuildCommand": "",
  "packageCommand": "",
  "components": [],
  "workloads": [],
  "config": {}
}
```

## Commands

Default:

```bash
dagger run -- bun /app/dagger/linux-package.mjs \
  --context /tmp/packaging-build/context \
  --output-dir /tmp/packaging-build/output \
  --plan /tmp/packaging-build/context/rendered/pipeline-plan.json
```

Configuration:

- `USE_DAGGER_FOR_LINUX=false` disables Dagger and uses the old local fallback.
- `DAGGER_LINUX_BASE_IMAGE` changes the pipeline image.
- `DAGGER_LINUX_INSTALL_TOOLS` changes tool installation.
- `DAGGER_LINUX_PACKAGE_CMD` overrides the default `tar.gz` step.
- `LSAPCTL_BUILD_CMD` runs inside the Dagger container before packaging.

The Linux Nomad runner container needs access to a Dagger engine. The default deployment mounts Docker socket:

```text
/var/run/docker.sock:/var/run/docker.sock
```

Alternatively configure `DAGGER_RUNNER_HOST`.

# Phase 7 live acceptance — 2026-09-29

- **Status:** pass for the controlled Ubuntu-a/Linux and local Windows raw_exec acceptance environment.
- **Scope:** `phase7/runner-integrity` working-tree snapshot, including uncommitted PR-4 and RustFS test-harness changes.
- **Deliberate test boundary:** Nomad ACL/mTLS were not enabled on the ephemeral acceptance cluster. Application-level Runner auth, execution capability fencing, Nomad Variable file rendering, exact Build/attempt/Runner binding, server-side output verification, and RustFS object-lock behavior were exercised. This is not evidence of a production Nomad ACL/mTLS rollout.

## Environment

| Role | Host | Evidence |
|---|---|---|
| Linux Docker/Nomad/Conductor/Postgres/RustFS | `ubuntu-a` / `airocov` | Nomad Linux node, Docker driver, RustFS and PostgreSQL running |
| Windows raw_exec Runner | local Windows host | Nomad Windows node, raw_exec driver, allocation secrets files |
| Artifact store | RustFS | S3-compatible backend under the legacy `ARTIFACT_STORE=minio` adapter name |

No secret values are recorded in this report.

## Linux auth-enabled Docker Runner E2E

A fresh approved Linux Build was executed as Nomad batch job `pkg-e2e-linux-90e809e549b8-a1` on Ubuntu-a:

- `AUTH_ENABLED=true`, `DRY_RUN=false`.
- Runner claimed the exact Build using a task-owned bearer/capability rendered from a Nomad Variable to two non-environment secret files.
- Docker Runner completed `final → checksum → complete`.
- Conductor reported server-verified final artifact SHA-256 and output ownership binding.
- `runner_e2e_cli.py --live` returned **ok** for protected evidence, Build/attempt/Runner/Job binding, final/checksum verification, immutable Job metadata, Job summary, and the single current completed allocation.

The acceptance used `USE_DAGGER_FOR_LINUX=false` solely because the constrained ephemeral Runner image did not include Dagger. It validates the real Docker Runner auth/output lifecycle, but not the Dagger packaging engine itself.

## RustFS trusted artifact and Release evidence

RustFS was started on Ubuntu-a and was used as the actual S3-compatible artifact service.

Passed checks:

- RustFS wildcard artifact resolution and SHA-256 sidecar locking.
- RustFS object-lock retention and legal hold readback.
- Linux Runner output final/checksum upload into RustFS.
- Preview validation and `release-evidence/archive` export. The archive URI was a `minio://` compatibility URI backed by RustFS.

## Windows raw_exec acceptance

Fresh Windows Build `79fd5732cf97` was dispatched as Nomad batch job `pkg-e2e-linux-79fd5732cf97-a1` to the Windows raw_exec node.

Passed evidence:

- Windows node registered with `kernel.name=windows`, `node_class=windows-packager`, and a healthy raw_exec driver.
- Runner consumed Nomad-rendered `vela-runner-auth` and `vela-runner-execution` secret files; no Runner credential was placed in `runner.env`.
- Runner completed exact claim, final output upload, checksum upload, server-side readback verification, and completion.
- Protected Conductor evidence tied the completed Build, attempt, Runner, Job, final upload, checksum upload, and SHA-256 together.
- `windows_runner_check_cli.py --live` returned **ok** for allocation identity, secret-file presence, effective ACL analysis, runner.env scan, Nomad allocation/Job/build binding, and protected operator evidence.

The Windows installer command was a controlled test packager and signing was intentionally disabled. This proves the raw_exec/auth/secrets/output path; it is not a release-signing or Inno/WiX production certification.

## Independent review follow-up

The final independent review initially found obsolete examples in [`runner/README.md`](../../runner/README.md): they showed unsupported `--operator-token` flags for the Runner E2E and Windows readiness tools. The examples were corrected to the environment-only credential contract and the Windows live-gate inputs. This report’s scope and production limitations were otherwise accepted by that review.

## Remaining production rollout requirements

1. Enable Nomad ACL and mTLS; grant only task-owned Variable read/write/delete permissions and re-run the same E2E.
2. Use production CA/client credentials and a low-privilege Nomad read token rather than the ephemeral no-ACL cluster.
3. Use a production Runner image with Dagger and run the Linux Dagger packaging path.
4. Configure an actual Windows installer toolchain and signing certificate policy, then run the Windows live gate with signing required.
5. Use non-default RustFS credentials, durable storage, and production object-lock retention policy.

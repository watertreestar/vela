# Auth-enabled Runner E2E

`runner_e2e_cli.py` reports `packaging.runner_e2e.report/v1`. It accepts the human operator/admin bearer only through `PACKAGING_OPERATOR_TOKEN` and the low-privilege Nomad read token only through `NOMAD_TOKEN`; neither credential is accepted on the command line. It never accepts, reads, constructs, prints, or reports `AUTH_RUNNER_TOKEN`, `RUNNER_EXECUTION_HMAC_KEY`, or execution capabilities.

```bash
# Deterministic local protocol contract only — this always reports warn, never live success.
python scripts/runner_e2e_cli.py check --fixture --json

# Run after an approved, auth-enabled Nomad batch allocation has completed.
# Use a restricted control-plane environment; do not place either token in the Runner allocation.
PACKAGING_OPERATOR_TOKEN=... NOMAD_TOKEN=... \
python scripts/runner_e2e_cli.py check --live \
  --api-url https://packaging.example \
  --nomad-url https://nomad.example:4646 \
  --build-id <build-id> --nomad-job-id <pkg-job-id> --json
```

Live mode requires the API URL, human operator/admin bearer, build ID, Nomad job ID, Nomad API URL, and a **low-privilege Nomad read token**. It fails unless all of the following independently agree:

- protected Conductor `runner-e2e-evidence` confirms an auth-enabled succeeded build, exact build/attempt/target/Runner ID/Nomad job ID, and server-verified final artifact/checksum binding;
- final and checksum records are bound to the same build, attempt, and Runner, with the checksum parent pointing at the final upload;
- the final SHA-256 has server verification evidence; and
- Nomad independently returns the named batch job whose immutable metadata matches the build, attempt, and Runner; its `builder/package` summary must have no failed/lost allocations and its single current allocation must show a completed, non-failed run. Historical stopped allocations are excluded from this current-dispatch check.

The report projects only non-secret identifiers and verification outcomes. It excludes execution JTIs, expiry, Runner bearer/capability values, raw logs, scheduler logs, artifact URLs, and raw HTTP payloads. A fixture warning is intentionally not a Nomad deployment proof.

Linux Nomad templates render runner bearer and capability through secure Nomad Variables into `$NOMAD_SECRETS_DIR` with `0400`; Windows `raw_exec` relies on allocation secrets-directory ACLs instead. Both must keep secrets out of `Task.Env`, `runner.env`, logs, reports, and package workspaces. Capability expiry, cancel, failure, and completion fence the attempt; rotate the Conductor-held bearer/HMAC key through the secret manager and dispatch a fresh attempt. Legacy/unverified artifacts remain downloadable where supported but cannot satisfy preview/release verification.

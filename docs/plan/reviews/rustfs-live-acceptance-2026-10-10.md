# RustFS live acceptance — 2026-10-10

## Scope

This production-like check used the existing RustFS and Vela API services on
`ubuntu-a`. It verifies the live S3-compatible resolver, object-retention
evidence, and the API artifact-retention metadata contract. It is not a
production compliance certification.

## RustFS object-lock evidence

The check used an isolated bucket named
`vela-object-lock-gate-20261010-1509` with these settings:

- retention mode: `GOVERNANCE`;
- retention period: 1 day;
- legal hold: disabled;
- transport: HTTP inside the controlled test network.

Passed evidence:

- the RustFS S3 endpoint was reachable;
- wildcard artifact resolution and the SHA-256 sidecar check passed;
- an audit archive object was written to an object-lock-enabled bucket;
- retention mode and retain-until metadata were read back successfully;
- legal-hold state was read back as disabled, matching the requested policy.

The check emitted a warning because the environment still uses the default
local RustFS credentials. The retained test object cannot be removed before
its governance retention period expires without an authorized governance
bypass.

## Artifact-retention API evidence

The live API exposed six artifact uploads. All six contained valid `sha256`,
`downloadUrl`, `downloadPolicy`, and `retentionStatus` fields. All were active,
so the configured expired-artifact threshold of zero passed.

There was no expired upload available to sample. Consequently, this run did
not independently exercise the HTTP 410 expired-download rejection path; that
behavior remains covered by automated tests and needs a future live expired
fixture before production sign-off.

## Tooling correction

The first object-lock run exposed a remote-workspace collision in
`scripts/rustfs_check.py`: sequential Go integration tests reused a temporary
path containing a read-only module cache. The script now creates a unique
remote workspace for every test and performs writable best-effort cleanup.
Regression coverage verifies workspace uniqueness and the cleanup command.

## Remaining production requirements

1. Replace the default RustFS credentials.
2. Enable TLS and validate the production certificate chain.
3. Confirm the compliance-approved retention mode and duration.
4. Run against the intended production bucket policy.
5. Seed an expired artifact and verify its live download endpoint returns
   HTTP 410.

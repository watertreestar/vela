# Windows Nomad live acceptance — 2026-10-11

## Scope

This controlled live acceptance combined the previously separate Windows
toolchain and Nomad Runner checks. An isolated auth-enabled Conductor built
from the current source submitted a real Windows batch job to the Nomad
Server on `ubuntu-a`. A temporary Windows client registered with
`node_class=windows-packager` and executed the job through `raw_exec`.

The allocation used Inno Setup 6.7.3 to compile a real PE installer and
SignTool from the Windows 10 SDK to apply a SHA-256 Authenticode signature.
The signing identity was a temporary self-signed certificate in the current
user's certificate store.

## Passed evidence

- Build: `c24954b792a8`.
- Nomad job: `pkg-lsap-c24954b792a8-a1`.
- Allocation: `2913f62d-d402-144d-a24b-25a557297c2b`.
- Temporary Windows node: `be2d117e-45e4-5756-41dd-f23e6cf7ea99`.
- The Windows node registered ready with a healthy `raw_exec` driver and the
  required Windows node class.
- Conductor created a task-owned Nomad Variable and Nomad rendered the Runner
  bearer and execution capability into separate allocation secret files.
- The Runner completed exact Build/attempt/Runner claim, protected component
  downloads, Inno compilation, SignTool signing, final/checksum upload,
  server-side readback, and completion.
- The allocation-internal Windows live gate passed allocation identity,
  restricted secret-file ACL, runner environment leak scan, real Inno and
  SignTool discovery, Nomad Job/build binding, and protected operator evidence.
- `runner_e2e_cli.py --live` passed Conductor reachability, operator-authenticated
  evidence, auth-enabled completion, exact output binding, immutable Nomad Job
  metadata, batch summary, and the single successful allocation.
- The downloaded installer retained the exact temporary signer thumbprint and
  did not report `NotSigned` or `HashMismatch`.
- Preview validation completed and the Release gate reported ready.

## Defect found during acceptance

The first protected local-component download returned HTTP 403. Local upload
URIs include the filename (`local-upload://<id>/<filename>`), while the Runner
input authorization check only accepted `local-upload://<id>`. The check now
requires the correct ID-scoped URI prefix and still cross-checks the complete
URI and SHA-256 against the stored upload record. Regression coverage exercises
an auth-enabled download using a filename-bearing local upload URI.

The local acceptance client also stopped forcing a raw local upload URI into
component overrides. It now supplies the upload ID and lets Conductor resolve
and lock the URI and SHA-256 itself. MinIO acceptance keeps its explicit HTTP
Runner download URL behavior.

## Cleanup evidence

After the final run:

- the isolated Conductor and Windows Nomad processes were stopped;
- the temporary Nomad Job, credential Variable, and Windows node were purged;
- the temporary certificate/private key was removed;
- the Conductor data, component inputs, downloaded installer, Nomad client
  data, allocation files, and logs were removed;
- no `vela-windows-nomad-live-*` temporary directory remained.

## Remaining production requirements

1. Enable Nomad ACL and mTLS. This controlled cluster accepted an arbitrary
   non-empty read-token value because ACL enforcement is disabled and emitted
   the existing mTLS warning.
2. Replace the self-signed identity with the production signing service,
   trusted certificate chain, RFC 3161 timestamping, and key-access audit
   evidence. `Get-AuthenticodeSignature` reports `UnknownError` for the
   intentionally untrusted test root.
3. Run the Windows Nomad client as its dedicated production service identity
   and validate that identity's installer, certificate-key, cache, and
   allocation ACL permissions.
4. Repeat with durable PostgreSQL and the production artifact backend instead
   of the isolated JSON/local stores used by this acceptance harness.

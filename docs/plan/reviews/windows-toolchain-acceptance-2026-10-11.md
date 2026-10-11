# Windows toolchain acceptance — 2026-10-11

## Scope

This production-like acceptance ran on the Windows runner host and exercised
the native installer and Authenticode toolchain. It used Inno Setup 6.7.3 to
compile a real `setup.exe`, SignTool from the Windows 10 SDK to sign and verify
it, and the installer itself to perform a silent installation.

The signing identity was a temporary self-signed code-signing certificate in
the current user's `My` store. The script intentionally did not add it to the
trusted root store because Windows requires an interactive security decision
for that operation. The certificate and private key were removed afterward;
no certificate, installer, or installed payload is retained.

## Passed evidence

- `ISCC.exe` compiled `infra/windows/acceptance/setup.iss` into a real PE
  installer.
- SignTool signed the installer using SHA-256 and exercised the default
  Authenticode verification policy. Verification reached the expected
  untrusted-root result for the isolated self-signed identity rather than a
  hash, format, or signer mismatch.
- PowerShell `Get-AuthenticodeSignature` returned the exact temporary signer
  thumbprint and did not report `HashMismatch`.
- The installer completed with `/VERYSILENT /SUPPRESSMSGBOXES /NORESTART`.
- The installed payload SHA-256 matched the repository `README.md` source.
- `windows_runner_check_cli.py` accepted the real Inno Setup and SignTool
  paths and rejected neither command as a fixture.
- The Bun cache, artifact root, secrets directory, installer output, and
  installed files were isolated under a unique temporary directory and
  removed after the run.

## Reproduction

Run from a Windows PowerShell session on the runner host:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\windows_toolchain_acceptance.ps1
```

The script requires Inno Setup, SignTool, Python, Bun, and a reachable Vela
API health endpoint. Tool and API paths can be overridden with script
parameters.

## Remaining production requirements

1. Replace the temporary self-signed identity with the production
   certificate-backed signing service, including timestamping and key-access
   audit evidence.
2. Run the same toolchain inside a dispatched Nomad Windows `raw_exec`
   allocation and pass `windows_runner_check_cli.py --live` with real
   allocation, secret ACL, build, job, and protected operator evidence.
3. Build and sign an actual product manifest rather than the isolated
   acceptance payload.
4. Validate certificate renewal, revocation, timestamp availability, and
   failure behavior when the signing identity cannot be accessed.

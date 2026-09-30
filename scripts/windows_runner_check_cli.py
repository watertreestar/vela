#!/usr/bin/env python3
"""Validate Windows Runner readiness; --live is only valid inside a real allocation."""
from __future__ import annotations

import argparse
import json
import os
import platform
import shlex
import shutil
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import asdict, dataclass
from pathlib import Path, PureWindowsPath
from typing import Any, Callable

WINDOWS_FIXTURE_MARKERS = (
    "windows_setup_packager.py",
    "windows_signing_fixture.py",
    "PACKAGING_PLATFORM_WINDOWS_SETUP_FIXTURE_V1",
    "PACKAGING_PLATFORM_WINDOWS_SIGNING_FIXTURE_V1",
)
RUNNER_SECRET_ENV_KEYS = (
    "AUTH_RUNNER_TOKEN",
    "RUNNER_EXECUTION_HMAC_KEY",
    "RUNNER_EXECUTION_CAPABILITY",
    "X_RUNNER_EXECUTION",
)
BROAD_READ_SIDS = ("S-1-1-0", "S-1-5-11", "S-1-5-32-545", "S-1-5-32-546")
ADMINISTRATOR_SIDS = ("S-1-5-18", "S-1-5-32-544")


@dataclass
class CheckResult:
    name: str
    status: str
    detail: str


def light(status: str) -> str:
    return "[OK]" if status == "ok" else ("[WARN]" if status == "warn" else "[FAIL]")


def env_or(value: str | None, key: str, fallback: str = "") -> str:
    return value or os.environ.get(key, fallback)


def split_command(command: str) -> list[str]:
    if not command.strip():
        return []
    try:
        return shlex.split(command, posix=False)
    except ValueError:
        return command.split()


def command_executable(command: str) -> str:
    parts = split_command(command)
    return parts[0].strip("\"'") if parts else ""


def is_fixture_command(command: str) -> bool:
    lowered = command.lower()
    return any(marker.lower() in lowered for marker in WINDOWS_FIXTURE_MARKERS)


def detect_packager_kind(command: str, explicit: str = "auto") -> str:
    if explicit and explicit != "auto":
        return explicit
    lowered = command.lower()
    executable = Path(command_executable(command)).name.lower()
    if executable in {"iscc", "iscc.exe"} or "iscc " in lowered:
        return "inno"
    if executable in {"wix", "wix.exe", "candle", "candle.exe", "light", "light.exe"} or "wix build" in lowered or "candle " in lowered or "light " in lowered:
        return "wix"
    return "custom"


def resolve_executable(name: str, which: Callable[[str], str | None] = shutil.which) -> str:
    return which(name) or ""


def toolchain_checks(packager_command: str, signing_command: str, packager_kind: str, which: Callable[[str], str | None] = shutil.which) -> list[CheckResult]:
    results: list[CheckResult] = []
    if packager_kind == "inno":
        path = resolve_executable(command_executable(packager_command) or "iscc", which)
        results.append(CheckResult("Inno Setup compiler", "ok" if path else "fail", path or "iscc not found in PATH"))
    elif packager_kind == "wix":
        executable = Path(command_executable(packager_command)).name.lower()
        candidates = [command_executable(packager_command)] if executable in {"wix.exe", "wix", "candle.exe", "candle", "light.exe", "light"} else ["wix", "candle", "light"]
        found = [candidate for candidate in candidates if resolve_executable(candidate, which)]
        results.append(CheckResult("WiX toolchain", "ok" if found else "fail", ",".join(found) if found else "wix/candle/light not found in PATH"))
    else:
        executable = command_executable(packager_command)
        path = resolve_executable(executable, which) if executable else ""
        results.append(CheckResult("Custom packager executable", "ok" if path or Path(executable).exists() else "warn", path or executable or "custom command not detected in PATH; verify on host"))
    if signing_command and (Path(command_executable(signing_command)).name.lower() in {"signtool", "signtool.exe"} or "signtool " in signing_command.lower()):
        path = resolve_executable(command_executable(signing_command) or "signtool", which)
        results.append(CheckResult("SignTool", "ok" if path else "fail", path or "signtool not found in PATH"))
    return results


def safe_basename(value: str) -> bool:
    return bool(value) and value not in {".", ".."} and "/" not in value and "\\" not in value and PureWindowsPath(value).name == value


def runner_env_check(runner_env: str, runner_env_file: str) -> CheckResult:
    values = [runner_env] if runner_env else []
    if runner_env_file:
        try:
            values.append(Path(runner_env_file).read_text(encoding="utf-8"))
        except OSError:
            return CheckResult("Runner environment secret leak", "fail", "runner.env could not be inspected")
    combined = "\n".join(values).upper()
    leaked = any(key in combined for key in RUNNER_SECRET_ENV_KEYS)
    return CheckResult(
        "Runner environment secret leak",
        "fail" if leaked else "ok",
        "runner.env must not contain runner bearer, HMAC key, or execution capability",
    )


def safe_sid(value: str) -> bool:
    return bool(value) and value.upper().startswith("S-1-") and all(part.isdigit() for part in value.split("-")[2:])


def inspect_windows_acls(paths: list[Path], task_sid: str) -> list[dict[str, Any]]:
    """Return ACL verdicts only; never emit identities or secret-file content."""
    serialized_paths = ",".join(json.dumps(str(path)) for path in paths)
    serialized_broad_sids = ",".join(json.dumps(sid) for sid in BROAD_READ_SIDS)
    serialized_admin_sids = ",".join(json.dumps(sid) for sid in ADMINISTRATOR_SIDS)
    script = f"""
$ErrorActionPreference = 'Stop'
$targets = @({serialized_paths})
$broadSids = @({serialized_broad_sids})
$allowedSids = @({json.dumps(task_sid)},{serialized_admin_sids})
$taskSid = {json.dumps(task_sid)}
$results = @()
foreach ($target in $targets) {{
  $item = Get-Item -LiteralPath $target -Force
  $security = $item.GetAccessControl('Access')
  $rules = $security.GetAccessRules($true, $true, [System.Security.Principal.SecurityIdentifier])
  $broadAccess = 0
  $unexpectedAccess = 0
  $taskRead = 0
  try {{ $ownerSid = $security.GetOwner([System.Security.Principal.SecurityIdentifier]).Value }} catch {{ $ownerSid = '' }}
  foreach ($rule in $rules) {{
    if ($rule.AccessControlType -ne [System.Security.AccessControl.AccessControlType]::Allow) {{ continue }}
    try {{ $sid = $rule.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value }} catch {{ $sid = '' }}
    $rights = [Int64]$rule.FileSystemRights
    $sensitiveRights = [Int64]([System.Security.AccessControl.FileSystemRights]::ReadData -bor [System.Security.AccessControl.FileSystemRights]::WriteData -bor [System.Security.AccessControl.FileSystemRights]::AppendData -bor [System.Security.AccessControl.FileSystemRights]::Delete -bor [System.Security.AccessControl.FileSystemRights]::ChangePermissions -bor [System.Security.AccessControl.FileSystemRights]::TakeOwnership -bor [System.Security.AccessControl.FileSystemRights]::FullControl)
    if (($rights -band $sensitiveRights) -eq 0) {{ continue }}
    if ($sid -eq $taskSid -and (($rights -band [Int64][System.Security.AccessControl.FileSystemRights]::ReadData) -ne 0)) {{ $taskRead++ }}
    if ($broadSids -contains $sid) {{ $broadAccess++; continue }}
    if ($allowedSids -notcontains $sid) {{ $unexpectedAccess++ }}
  }}
  $results += [PSCustomObject]@{{
    reparse = [bool]($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint)
    nullDacl = ($null -eq $security.GetAccessRules($true, $true, [System.Security.Principal.SecurityIdentifier]))
    ownerAllowed = if ($ownerSid -eq '') {{ $null }} elseif ($allowedSids -contains $ownerSid) {{ $true }} else {{ $null }}
    broadAccess = $broadAccess
    unexpectedAccess = $unexpectedAccess
    taskRead = $taskRead
  }}
}}
$results | ConvertTo-Json -Compress
"""
    completed = subprocess.run(
        ["powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script],
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=30,
    )
    if completed.returncode != 0:
        raise RuntimeError("could not inspect Windows secrets ACL")
    try:
        decoded = json.loads(completed.stdout)
    except json.JSONDecodeError as exc:
        raise RuntimeError("could not parse Windows secrets ACL verdict") from exc
    values = decoded if isinstance(decoded, list) else [decoded]
    if len(values) != len(paths) or not all(isinstance(item, dict) for item in values):
        raise RuntimeError("Windows secrets ACL verdict was incomplete")
    return values


def live_secret_checks(secrets_dir: str, token_filename: str, execution_filename: str, task_sid: str, acl_inspector: Callable[[list[Path], str], list[dict[str, Any]]] = inspect_windows_acls) -> list[CheckResult]:
    if not safe_sid(task_sid):
        return [CheckResult("Nomad allocation secrets", "fail", "NOMAD_TASK_SID must be a Windows SID for live ACL verification")]
    if not secrets_dir or not os.path.isabs(secrets_dir):
        return [CheckResult("Nomad allocation secrets", "fail", "live gate requires an absolute NOMAD_SECRETS_DIR")]
    if not safe_basename(token_filename) or not safe_basename(execution_filename) or token_filename == execution_filename:
        return [CheckResult("Nomad allocation secrets", "fail", "credential filenames must be distinct safe basenames")]
    root = Path(secrets_dir)
    if not root.is_dir():
        return [CheckResult("Nomad allocation secrets", "fail", "NOMAD_SECRETS_DIR does not exist")]
    try:
        canonical_root = root.resolve(strict=True)
        files = [root / token_filename, root / execution_filename]
        for path in files:
            if path.is_symlink() or not path.is_file() or path.stat().st_size <= 0:
                return [CheckResult("Nomad allocation secrets", "fail", "runner bearer and execution capability must be non-empty regular files")]
            canonical_file = path.resolve(strict=True)
            if os.path.commonpath([str(canonical_root), str(canonical_file)]) != str(canonical_root):
                return [CheckResult("Nomad allocation secrets", "fail", "credential file resolves outside NOMAD_SECRETS_DIR")]
        acl_verdicts = acl_inspector([root, *files], task_sid)
    except (OSError, RuntimeError):
        return [CheckResult("Nomad allocation secrets", "fail", "could not verify allocation secrets directory, files, and ACLs")]
    secure = all(
        not verdict.get("reparse")
        and not verdict.get("nullDacl")
        and (verdict.get("ownerAllowed") is not False)
        and int(verdict.get("broadAccess", 0)) == 0
        and int(verdict.get("unexpectedAccess", 0)) == 0
        and int(verdict.get("taskRead", 0)) > 0
        for verdict in acl_verdicts
    )
    return [
        CheckResult("Nomad allocation secrets", "ok" if secure else "fail", "runner bearer and execution capability exist under restricted allocation ACLs" if secure else "allocation secrets ACL does not restrict access to the declared task identity"),
    ]


def static_secrets_checks(secrets_dir: str, token_filename: str, execution_filename: str) -> list[CheckResult]:
    results = [CheckResult("Nomad allocation secrets", "warn" if secrets_dir else "fail", "not live-verified; run this gate inside a Windows allocation with --live")]
    valid = safe_basename(token_filename) and safe_basename(execution_filename) and token_filename != execution_filename
    results.append(CheckResult("Runner credential filenames", "ok" if valid else "fail", "two distinct safe basenames are required"))
    return results


def api_connectivity(api_url: str, live: bool) -> CheckResult:
    if not api_url:
        return CheckResult("Packaging API reachability", "fail" if live else "warn", "PACKAGING_API_URL missing")
    try:
        request = urllib.request.Request(api_url.rstrip("/") + "/api/health")
        with urllib.request.urlopen(request, timeout=15) as response:
            return CheckResult("Packaging API reachability", "ok" if response.status < 400 else "fail", f"HTTP {response.status}")
    except (urllib.error.URLError, urllib.error.HTTPError):
        return CheckResult("Packaging API reachability", "fail", "API request failed")


def protected_evidence_check(api_url: str, token: str, build_id: str, nomad_job_id: str, allocation_id: str, live: bool) -> CheckResult:
    if not token:
        return CheckResult("Human operator authentication", "fail" if live else "warn", "not checked; PACKAGING_OPERATOR_TOKEN is required for live verification")
    if not api_url or not build_id or not nomad_job_id:
        return CheckResult("Human operator authentication", "fail" if live else "warn", "not checked; API URL, build ID, and Nomad job ID are required")
    path = f"/api/builds/{urllib.parse.quote(build_id, safe='')}/runner-e2e-evidence"
    try:
        request = urllib.request.Request(api_url.rstrip("/") + path, headers={"Authorization": f"Bearer {token}", "Accept": "application/json"})
        with urllib.request.urlopen(request, timeout=15) as response:
            payload = json.loads(response.read().decode("utf-8"))
    except (urllib.error.URLError, urllib.error.HTTPError, json.JSONDecodeError):
        return CheckResult("Human operator authentication", "fail", "protected evidence request failed")
    valid = (
        isinstance(payload, dict)
        and payload.get("buildId") == build_id
        and payload.get("nomadJobId") == nomad_job_id
        and payload.get("status") == "succeeded"
        and payload.get("authEnabled") is True
        and payload.get("verificationStatus") == "verified"
        and bool(payload.get("dispatchStartedAt"))
    )
    return CheckResult("Human operator authentication", "ok" if valid else "fail", "protected operator/admin evidence matches the allocation-bound Windows dispatch" if valid else "protected evidence did not match the requested completed dispatch")


def nomad_allocation_check(nomad_url: str, nomad_token: str, allocation_id: str, build_id: str, nomad_job_id: str) -> CheckResult:
    if not nomad_url or not nomad_token or not allocation_id:
        return CheckResult("Nomad allocation dispatch binding", "fail", "NOMAD_ADDR, NOMAD_TOKEN, and current allocation ID are required")
    try:
        request = urllib.request.Request(
            nomad_url.rstrip("/") + f"/v1/allocation/{urllib.parse.quote(allocation_id, safe='')}",
            headers={"X-Nomad-Token": nomad_token, "Accept": "application/json"},
        )
        with urllib.request.urlopen(request, timeout=15) as response:
            allocation = json.loads(response.read().decode("utf-8"))
    except (urllib.error.URLError, urllib.error.HTTPError, json.JSONDecodeError):
        return CheckResult("Nomad allocation dispatch binding", "fail", "Nomad allocation query failed")
    job = allocation.get("Job") if isinstance(allocation, dict) and isinstance(allocation.get("Job"), dict) else {}
    metadata = job.get("Meta") if isinstance(job.get("Meta"), dict) else {}
    task_states = allocation.get("TaskStates") if isinstance(allocation, dict) and isinstance(allocation.get("TaskStates"), dict) else {}
    package = task_states.get("package") if isinstance(task_states.get("package"), dict) else {}
    valid = (
        isinstance(allocation, dict)
        and allocation.get("ID") == allocation_id
        and allocation.get("JobID") == nomad_job_id
        and allocation.get("TaskGroup") == "builder"
        and allocation.get("DesiredStatus") == "run"
        and metadata.get("packaging.build_id") == build_id
        and metadata.get("packaging.attempt", "").isdigit()
        and bool(metadata.get("packaging.runner_id"))
        and package.get("Failed") is not True
    )
    return CheckResult("Nomad allocation dispatch binding", "ok" if valid else "fail", "current allocation belongs to the requested build/job dispatch" if valid else "current allocation does not match the requested build/job dispatch")


def configured_path_check(name: str, raw_path: str) -> CheckResult:
    if not raw_path:
        return CheckResult(name, "warn", "not configured")
    path = Path(raw_path)
    return CheckResult(name, "ok" if path.exists() else "warn", str(path) if path.exists() else f"path does not exist yet: {path}")


def live_input_checks(allocation_id: str, build_id: str, nomad_job_id: str, runner_env_file: str, task_sid: str) -> list[CheckResult]:
    current_allocation = os.environ.get("NOMAD_ALLOC_ID", "")
    allocation_ok = bool(allocation_id) and allocation_id == current_allocation
    evidence_ok = bool(build_id) and bool(nomad_job_id)
    env_ok = bool(runner_env_file)
    task_sid_ok = safe_sid(task_sid)
    return [
        CheckResult("Nomad allocation identity", "ok" if allocation_ok else "fail", "matches current NOMAD_ALLOC_ID" if allocation_ok else "--allocation-id must match current NOMAD_ALLOC_ID"),
        CheckResult("Windows dispatch evidence inputs", "ok" if evidence_ok else "fail", "build ID and Nomad job ID supplied" if evidence_ok else "--build-id and --nomad-job-id are required"),
        CheckResult("Runner environment inspection", "ok" if env_ok else "fail", "runner.env file supplied" if env_ok else "--runner-env-file is required in live mode"),
        CheckResult("Windows task SID", "ok" if task_sid_ok else "fail", "task SID supplied" if task_sid_ok else "NOMAD_TASK_SID is required for live ACL verification"),
    ]


def validate_readiness(args: argparse.Namespace, which: Callable[[str], str | None] = shutil.which, acl_inspector: Callable[[list[Path]], list[dict[str, Any]]] = inspect_windows_acls) -> list[CheckResult]:
    packager_command = env_or(args.packager_command, "WINDOWS_PACKAGER_CMD")
    signing_command = env_or(args.signing_command, "WINDOWS_SIGNING_CMD")
    runner_command = env_or(args.runner_command, "WINDOWS_RUNNER_COMMAND", "bun C:\\packaging-runner\\package-runner.js")
    api_url = env_or(args.api_url, "PACKAGING_API_URL")
    node_class = env_or(args.node_class, "NOMAD_WINDOWS_NODE_CLASS", "windows-packager")
    bun_cache = env_or(args.bun_cache_dir, "BUN_INSTALL_CACHE_DIR")
    artifact_root = env_or(args.artifact_root, "ARTIFACT_ROOT")
    secrets_dir = env_or(args.secrets_dir, "NOMAD_SECRETS_DIR")
    token_filename = env_or(args.token_filename, "PACKAGING_API_TOKEN_FILE", "vela-runner-auth")
    execution_filename = env_or(args.execution_filename, "RUNNER_EXECUTION_FILE", "vela-runner-execution")
    task_sid = args.task_sid or os.environ.get("NOMAD_TASK_SID", "")
    operator_token = os.environ.get("PACKAGING_OPERATOR_TOKEN", "")
    nomad_url = os.environ.get("NOMAD_ADDR", "")
    nomad_token = os.environ.get("NOMAD_TOKEN", "")
    is_windows = platform.system().lower() == "windows"
    host_status = "ok" if is_windows else ("warn" if args.allow_non_windows and not args.live else "fail")

    results = [
        CheckResult("Windows host", host_status, platform.system()),
        CheckResult("Packaging API URL", "ok" if api_url else "fail", api_url or "PACKAGING_API_URL missing"),
        CheckResult("Nomad Windows node class", "ok" if node_class else "fail", node_class or "NOMAD_WINDOWS_NODE_CLASS missing"),
        CheckResult("Windows runner command", "ok" if runner_command else "fail", runner_command or "WINDOWS_RUNNER_COMMAND missing"),
        configured_path_check("Bun cache directory", bun_cache),
        configured_path_check("Artifact root", artifact_root),
    ]

    if not packager_command:
        results.append(CheckResult("Windows packager command", "fail", "WINDOWS_PACKAGER_CMD is required for production setup.exe"))
        packager_kind = "custom"
    elif is_fixture_command(packager_command):
        results.append(CheckResult("Windows packager command", "fail", "fixture packager is not allowed for production release"))
        packager_kind = "custom"
    else:
        packager_kind = detect_packager_kind(packager_command, args.packager_kind)
        results.append(CheckResult("Windows packager command", "ok", f"{packager_kind}: {packager_command}"))

    if args.require_signing:
        if not signing_command:
            results.append(CheckResult("Windows signing command", "fail", "WINDOWS_SIGNING_CMD is required when --require-signing is enabled"))
        elif is_fixture_command(signing_command):
            results.append(CheckResult("Windows signing command", "fail", "fixture signing command is not allowed for production release"))
        else:
            results.append(CheckResult("Windows signing command", "ok", signing_command))
    elif signing_command:
        results.append(CheckResult("Windows signing command", "fail" if is_fixture_command(signing_command) else "ok", signing_command if not is_fixture_command(signing_command) else "fixture signing command is not allowed"))
    else:
        results.append(CheckResult("Windows signing command", "warn", "not configured; generated setup.exe will be unsigned"))

    if packager_command and not is_fixture_command(packager_command):
        results.extend(toolchain_checks(packager_command, signing_command, packager_kind, which))
    if args.live:
        results.extend(live_input_checks(args.allocation_id, args.build_id, args.nomad_job_id, args.runner_env_file, task_sid))
        if is_windows:
            results.extend(live_secret_checks(secrets_dir, token_filename, execution_filename, task_sid, acl_inspector))
        else:
            results.append(CheckResult("Nomad allocation secrets", "fail", "live secrets and ACL verification must run on the Windows allocation host"))
    else:
        results.extend(static_secrets_checks(secrets_dir, token_filename, execution_filename))
    results.append(runner_env_check(args.runner_env, args.runner_env_file))
    results.append(api_connectivity(api_url, args.live) if is_windows else CheckResult("Packaging API reachability", "warn", "not run away from a Windows host; this is not live host success"))
    if is_windows:
        results.append(nomad_allocation_check(nomad_url, nomad_token, args.allocation_id, args.build_id, args.nomad_job_id) if args.live else CheckResult("Nomad allocation dispatch binding", "warn", "not live-verified"))
        results.append(protected_evidence_check(api_url, operator_token, args.build_id, args.nomad_job_id, args.allocation_id, args.live))
    else:
        results.append(CheckResult("Nomad allocation dispatch binding", "warn" if not args.live else "fail", "not run away from a Windows host"))
        results.append(CheckResult("Human operator authentication", "warn" if not args.live else "fail", "not run away from a Windows host"))
    return results


def result_exit_code(results: list[CheckResult]) -> int:
    return 1 if any(result.status == "fail" for result in results) else 0


def print_results(results: list[CheckResult]) -> None:
    print("Confirmation lights:")
    for result in results:
        print(f"  {light(result.status)} {result.name}: {result.detail}")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Validate production Windows Runner packaging readiness.")
    subparsers = parser.add_subparsers(dest="command")
    check = subparsers.add_parser("check", help="check Windows Runner host, packager, and signing readiness")
    check.add_argument("--live", action="store_true", help="require real Windows allocation, secret-file ACL, and protected API evidence")
    check.add_argument("--packager-command", help="defaults to WINDOWS_PACKAGER_CMD")
    check.add_argument("--signing-command", help="defaults to WINDOWS_SIGNING_CMD")
    check.add_argument("--runner-command", help="defaults to WINDOWS_RUNNER_COMMAND")
    check.add_argument("--api-url", help="defaults to PACKAGING_API_URL")
    check.add_argument("--node-class", help="defaults to NOMAD_WINDOWS_NODE_CLASS or windows-packager")
    check.add_argument("--bun-cache-dir", help="defaults to BUN_INSTALL_CACHE_DIR")
    check.add_argument("--artifact-root", help="defaults to ARTIFACT_ROOT")
    check.add_argument("--secrets-dir", help="Nomad allocation secrets directory; defaults to NOMAD_SECRETS_DIR")
    check.add_argument("--token-filename", help="runner bearer basename; defaults to PACKAGING_API_TOKEN_FILE or vela-runner-auth")
    check.add_argument("--execution-filename", help="execution capability basename; defaults to RUNNER_EXECUTION_FILE or vela-runner-execution")
    check.add_argument("--runner-env", default="", help="non-live test-only runner.env content; do not pass secrets")
    check.add_argument("--runner-env-file", default="", help="runner.env path to inspect without printing its content")
    check.add_argument("--allocation-id", default="", help="must match NOMAD_ALLOC_ID in --live mode")
    check.add_argument("--build-id", default="", help="completed build ID required in --live mode")
    check.add_argument("--nomad-job-id", default="", help="completed Nomad job ID required in --live mode")
    check.add_argument("--task-sid", default="", help="Windows task SID for allocation secrets ACL allowlist; defaults to NOMAD_TASK_SID")
    check.add_argument("--packager-kind", choices=["auto", "inno", "wix", "custom"], default="auto")
    check.add_argument("--require-signing", action=argparse.BooleanOptionalAction, default=True)
    check.add_argument("--allow-non-windows", action="store_true", help="non-live preflight only: warn instead of fail away from Windows")
    check.add_argument("--json", action="store_true", help="print machine-readable JSON")
    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args(["check"] if len(sys.argv) == 1 else None)
    if args.command != "check":
        parser.error(f"unknown command: {args.command}")
    results = validate_readiness(args)
    if args.json:
        print(json.dumps([asdict(result) for result in results], indent=2))
    else:
        print_results(results)
    return result_exit_code(results)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception:
        print(f"{light('fail')} Windows Runner readiness check failed unexpectedly", file=sys.stderr)
        sys.exit(1)

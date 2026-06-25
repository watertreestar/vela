#!/usr/bin/env python3
"""Validate production Windows runner packaging readiness."""

from __future__ import annotations

import argparse
import json
import os
import platform
import shlex
import shutil
import sys
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Callable


WINDOWS_FIXTURE_MARKERS = (
    "windows_setup_packager.py",
    "windows_signing_fixture.py",
    "PACKAGING_PLATFORM_WINDOWS_SETUP_FIXTURE_V1",
    "PACKAGING_PLATFORM_WINDOWS_SIGNING_FIXTURE_V1",
)


@dataclass
class CheckResult:
    name: str
    status: str
    detail: str


def light(status: str) -> str:
    if status == "ok":
        return "[OK]"
    if status == "warn":
        return "[WARN]"
    return "[FAIL]"


def env_or(value: str | None, key: str, fallback: str = "") -> str:
    if value:
        return value
    return os.environ.get(key, fallback)


def split_command(command: str) -> list[str]:
    if not command.strip():
        return []
    try:
        return shlex.split(command, posix=False)
    except ValueError:
        return command.split()


def command_executable(command: str) -> str:
    parts = split_command(command)
    if not parts:
        return ""
    return parts[0].strip("\"'")


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
    if executable in {"wix", "wix.exe", "candle", "candle.exe", "light", "light.exe"}:
        return "wix"
    if "wix build" in lowered or "candle " in lowered or "light " in lowered:
        return "wix"
    return "custom"


def resolve_executable(name: str, which: Callable[[str], str | None] = shutil.which) -> str:
    path = which(name)
    return path or ""


def toolchain_checks(
    packager_command: str,
    signing_command: str,
    packager_kind: str,
    which: Callable[[str], str | None] = shutil.which,
) -> list[CheckResult]:
    results: list[CheckResult] = []
    if packager_kind == "inno":
        path = resolve_executable(command_executable(packager_command) or "iscc", which)
        results.append(CheckResult("Inno Setup compiler", "ok" if path else "fail", path or "iscc not found in PATH"))
    elif packager_kind == "wix":
        executable = Path(command_executable(packager_command)).name.lower()
        candidates = [command_executable(packager_command)] if executable in {"wix.exe", "wix", "candle.exe", "candle", "light.exe", "light"} else ["wix", "candle", "light"]
        found = [candidate for candidate in candidates if resolve_executable(candidate, which)]
        results.append(
            CheckResult(
                "WiX toolchain",
                "ok" if found else "fail",
                ",".join(found) if found else "wix/candle/light not found in PATH",
            )
        )
    else:
        executable = command_executable(packager_command)
        path = resolve_executable(executable, which) if executable else ""
        results.append(
            CheckResult(
                "Custom packager executable",
                "ok" if path or Path(executable).exists() else "warn",
                path or executable or "custom command not detected in PATH; verify on host",
            )
        )

    if signing_command:
        executable = command_executable(signing_command)
        if Path(executable).name.lower() in {"signtool", "signtool.exe"} or "signtool " in signing_command.lower():
            path = resolve_executable(executable or "signtool", which)
            results.append(CheckResult("SignTool", "ok" if path else "fail", path or "signtool not found in PATH"))
    return results


def configured_path_check(name: str, raw_path: str) -> CheckResult:
    if not raw_path:
        return CheckResult(name, "warn", "not configured")
    path = Path(raw_path)
    if path.exists():
        return CheckResult(name, "ok", str(path))
    return CheckResult(name, "warn", f"path does not exist yet: {path}")


def validate_readiness(args: argparse.Namespace, which: Callable[[str], str | None] = shutil.which) -> list[CheckResult]:
    packager_command = env_or(args.packager_command, "WINDOWS_PACKAGER_CMD")
    signing_command = env_or(args.signing_command, "WINDOWS_SIGNING_CMD")
    runner_command = env_or(args.runner_command, "WINDOWS_RUNNER_COMMAND", "bun C:\\packaging-runner\\package-runner.js")
    api_url = env_or(args.api_url, "PACKAGING_API_URL")
    node_class = env_or(args.node_class, "NOMAD_WINDOWS_NODE_CLASS", "windows-packager")
    bun_cache = env_or(args.bun_cache_dir, "BUN_INSTALL_CACHE_DIR")
    artifact_root = env_or(args.artifact_root, "ARTIFACT_ROOT")

    results = [
        CheckResult(
            "Windows host",
            "ok" if platform.system().lower() == "windows" else ("warn" if args.allow_non_windows else "fail"),
            platform.system(),
        ),
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
        status = "fail" if is_fixture_command(signing_command) else "ok"
        results.append(CheckResult("Windows signing command", status, signing_command if status == "ok" else "fixture signing command is not allowed"))
    else:
        results.append(CheckResult("Windows signing command", "warn", "not configured; generated setup.exe will be unsigned"))

    if packager_command and not is_fixture_command(packager_command):
        results.extend(toolchain_checks(packager_command, signing_command, packager_kind, which))

    return results


def result_exit_code(results: list[CheckResult]) -> int:
    return 1 if any(result.status == "fail" for result in results) else 0


def print_results(results: list[CheckResult]) -> None:
    print("Confirmation lights:")
    for result in results:
        print(f"  {light(result.status)} {result.name}: {result.detail}")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Validate production Windows runner packaging readiness.")
    subparsers = parser.add_subparsers(dest="command")
    check = subparsers.add_parser("check", help="check Windows runner host, packager, and signing readiness")
    check.add_argument("--packager-command", help="defaults to WINDOWS_PACKAGER_CMD")
    check.add_argument("--signing-command", help="defaults to WINDOWS_SIGNING_CMD")
    check.add_argument("--runner-command", help="defaults to WINDOWS_RUNNER_COMMAND")
    check.add_argument("--api-url", help="defaults to PACKAGING_API_URL")
    check.add_argument("--node-class", help="defaults to NOMAD_WINDOWS_NODE_CLASS or windows-packager")
    check.add_argument("--bun-cache-dir", help="defaults to BUN_INSTALL_CACHE_DIR")
    check.add_argument("--artifact-root", help="defaults to ARTIFACT_ROOT")
    check.add_argument("--packager-kind", choices=["auto", "inno", "wix", "custom"], default="auto")
    check.add_argument("--require-signing", action=argparse.BooleanOptionalAction, default=True)
    check.add_argument("--allow-non-windows", action="store_true", help="warn instead of fail when running away from a Windows host")
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
    except Exception as exc:
        print(f"{light('fail')} {exc}", file=sys.stderr)
        sys.exit(1)

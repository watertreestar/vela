#!/usr/bin/env python3
"""Run Vela lifecycle reconciliation checks."""

from __future__ import annotations

import argparse
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path


@dataclass
class CheckResult:
    name: str
    status: str
    detail: str


def repo_root() -> Path:
    return Path(__file__).resolve().parents[1]


def light(status: str) -> str:
    if status == "ok":
        return "[OK]"
    if status == "warn":
        return "[WARN]"
    return "[FAIL]"


def run_command(command: list[str], cwd: Path, timeout: int = 120) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        command,
        cwd=cwd,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=timeout,
    )


def lifecycle_go_tests(root: Path) -> CheckResult:
    result = run_command(["go", "test", "./internal/lifecycle"], root / "conductor")
    output = "\n".join(part for part in [result.stdout, result.stderr] if part).strip()
    if result.returncode == 0:
        return CheckResult("Lifecycle reconciliation Go tests", "ok", "conductor/internal/lifecycle passed")
    return CheckResult("Lifecycle reconciliation Go tests", "fail", output or f"go test exited {result.returncode}")


def run_checks(args: argparse.Namespace) -> list[CheckResult]:
    root = repo_root()
    results = [lifecycle_go_tests(root)]
    if args.full_conductor:
        result = run_command(["go", "test", "./..."], root / "conductor", timeout=180)
        output = "\n".join(part for part in [result.stdout, result.stderr] if part).strip()
        results.append(
            CheckResult(
                "Conductor regression suite",
                "ok" if result.returncode == 0 else "fail",
                "go test ./... passed" if result.returncode == 0 else output,
            )
        )
    return results


def print_results(results: list[CheckResult]) -> None:
    print("Confirmation lights:")
    for result in results:
        print(f"  {light(result.status)} {result.name}: {result.detail}")


def result_exit_code(results: list[CheckResult]) -> int:
    return 1 if any(result.status == "fail" for result in results) else 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Vela lifecycle reconciliation checks.")
    subparsers = parser.add_subparsers(dest="command")
    check_parser = subparsers.add_parser("check", help="run lifecycle reconciliation checks")
    check_parser.add_argument("--full-conductor", action="store_true", help="also run conductor go test ./...")
    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args(["check"] if len(sys.argv) == 1 else None)
    if args.command != "check":
        parser.error(f"unknown command: {args.command}")
    results = run_checks(args)
    print_results(results)
    return result_exit_code(results)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(f"{light('fail')} {exc}", file=sys.stderr)
        sys.exit(1)


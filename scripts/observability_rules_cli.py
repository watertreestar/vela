#!/usr/bin/env python3
"""Validate Packaging Platform Prometheus alert rule files."""

from __future__ import annotations

import argparse
import re
import sys
from dataclasses import dataclass
from pathlib import Path


DEFAULT_RULES = Path("infra/prometheus/packaging-platform-rules.yml")
REQUIRED_ALERTS = {
    "PackagingPlatformAPIDown",
    "PackagingPlatformRunnerUnavailable",
    "PackagingPlatformActiveRunnerHeartbeatStale",
    "PackagingPlatformRunnerCacheScanTruncated",
    "PackagingPlatformBuildQueueBacklog",
    "PackagingPlatformFailedBuildVolume",
    "PackagingPlatformReleaseGateBlocked",
    "PackagingPlatformExpiredArtifactsPresent",
}
KNOWN_METRICS = {
    "up",
    "packaging_builds_total",
    "packaging_release_gate_blockers_total",
    "packaging_uploads_total",
    "packaging_runners_total",
    "packaging_runner_running_builds",
    "packaging_runner_last_heartbeat_age_seconds",
    "packaging_runner_cache_entries",
    "packaging_runner_cache_bytes",
    "packaging_runner_cache_files",
    "packaging_runner_cache_scan_truncated",
}


@dataclass
class RuleCheck:
    name: str
    status: str
    detail: str


def light(status: str) -> str:
    if status == "ok":
        return "[OK]"
    if status == "warn":
        return "[WARN]"
    return "[FAIL]"


def read_rules(path: Path) -> str:
    if not path.exists():
        raise FileNotFoundError(f"rules file not found: {path}")
    return path.read_text(encoding="utf-8")


def alert_names(content: str) -> set[str]:
    return set(re.findall(r"^\s*-\s+alert:\s*([A-Za-z0-9_]+)\s*$", content, flags=re.MULTILINE))


def expression_blocks(content: str) -> list[str]:
    return re.findall(r"^\s*expr:\s*(.+?)\s*$", content, flags=re.MULTILINE)


def validate_rules(content: str) -> list[RuleCheck]:
    checks: list[RuleCheck] = []
    names = alert_names(content)
    expressions = expression_blocks(content)
    missing_alerts = sorted(REQUIRED_ALERTS - names)

    checks.append(
        RuleCheck(
            "Prometheus rule groups",
            "ok" if "groups:" in content and "rules:" in content else "fail",
            "groups/rules sections present" if "groups:" in content and "rules:" in content else "missing groups or rules section",
        )
    )
    checks.append(
        RuleCheck(
            "Required alerts",
            "ok" if not missing_alerts else "fail",
            f"{len(names)} alert(s), missing={','.join(missing_alerts) or 'none'}",
        )
    )
    checks.append(
        RuleCheck(
            "Alert expressions",
            "ok" if len(expressions) >= len(REQUIRED_ALERTS) and all(expr.strip() for expr in expressions) else "fail",
            f"{len(expressions)} expression(s)",
        )
    )
    checks.append(
        RuleCheck(
            "No tab indentation",
            "ok" if "\t" not in content else "fail",
            "tabs=0" if "\t" not in content else "tabs found",
        )
    )

    referenced_metrics = {metric for metric in KNOWN_METRICS if re.search(rf"\b{re.escape(metric)}\b", content)}
    missing_metrics = sorted(KNOWN_METRICS - referenced_metrics)
    checks.append(
        RuleCheck(
            "Known metrics referenced",
            "ok" if not missing_metrics else "fail",
            f"missing={','.join(missing_metrics) or 'none'}",
        )
    )
    return checks


def result_exit_code(results: list[RuleCheck]) -> int:
    return 1 if any(result.status == "fail" for result in results) else 0


def print_results(results: list[RuleCheck]) -> None:
    print("Confirmation lights:")
    for result in results:
        print(f"  {light(result.status)} {result.name}: {result.detail}")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Validate Packaging Platform Prometheus alert rules.")
    parser.add_argument("command", choices=["check"], nargs="?", default="check")
    parser.add_argument("--rules", type=Path, default=DEFAULT_RULES)
    return parser


def main() -> int:
    args = build_parser().parse_args()
    content = read_rules(args.rules)
    results = validate_rules(content)
    print_results(results)
    return result_exit_code(results)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(f"{light('fail')} {exc}", file=sys.stderr)
        sys.exit(1)

#!/usr/bin/env python3
"""Validate Packaging Platform Alertmanager routing examples."""

from __future__ import annotations

import argparse
import re
import sys
from dataclasses import dataclass
from pathlib import Path


DEFAULT_CONFIG = Path("infra/prometheus/alertmanager-packaging-platform.yml")
REQUIRED_RECEIVERS = {
    "packaging-default",
    "packaging-critical-oncall",
    "packaging-runner-ops",
    "packaging-release-managers",
    "packaging-artifact-ops",
    "packaging-build-ops",
}
REQUIRED_MATCHERS = {
    'severity="critical"',
    'category="runner"',
    'category="release-gate"',
    'category="artifact"',
    'category="build"',
}


@dataclass
class ConfigCheck:
    name: str
    status: str
    detail: str


def light(status: str) -> str:
    if status == "ok":
        return "[OK]"
    if status == "warn":
        return "[WARN]"
    return "[FAIL]"


def read_config(path: Path) -> str:
    if not path.exists():
        raise FileNotFoundError(f"alertmanager config not found: {path}")
    return path.read_text(encoding="utf-8")


def receiver_names(content: str) -> set[str]:
    return set(re.findall(r"^\s*-\s+name:\s*([A-Za-z0-9_-]+)\s*$", content, flags=re.MULTILINE))


def receiver_references(content: str) -> set[str]:
    return set(re.findall(r"^\s*receiver:\s*([A-Za-z0-9_-]+)\s*$", content, flags=re.MULTILINE))


def matcher_values(content: str) -> set[str]:
    return set(re.findall(r"^\s*-\s+([A-Za-z_]+=\"[A-Za-z0-9_-]+\")\s*$", content, flags=re.MULTILINE))


def webhook_urls(content: str) -> list[str]:
    return re.findall(r"^\s*-\s+url:\s*(\S+)\s*$", content, flags=re.MULTILINE)


def validate_config(content: str) -> list[ConfigCheck]:
    checks: list[ConfigCheck] = []
    receivers = receiver_names(content)
    references = receiver_references(content)
    matchers = matcher_values(content)
    urls = webhook_urls(content)
    missing_receivers = sorted(REQUIRED_RECEIVERS - receivers)
    undefined_references = sorted(references - receivers)
    missing_matchers = sorted(REQUIRED_MATCHERS - matchers)

    checks.append(
        ConfigCheck(
            "Alertmanager sections",
            "ok" if "route:" in content and "receivers:" in content else "fail",
            "route/receivers sections present" if "route:" in content and "receivers:" in content else "missing route or receivers",
        )
    )
    checks.append(
        ConfigCheck(
            "Required receivers",
            "ok" if not missing_receivers else "fail",
            f"{len(receivers)} receiver(s), missing={','.join(missing_receivers) or 'none'}",
        )
    )
    checks.append(
        ConfigCheck(
            "Route receiver references",
            "ok" if not undefined_references else "fail",
            f"undefined={','.join(undefined_references) or 'none'}",
        )
    )
    checks.append(
        ConfigCheck(
            "Required matchers",
            "ok" if not missing_matchers else "fail",
            f"missing={','.join(missing_matchers) or 'none'}",
        )
    )
    checks.append(
        ConfigCheck(
            "Webhook receivers",
            "ok" if len(urls) >= len(REQUIRED_RECEIVERS) and all(url.startswith(("http://", "https://")) for url in urls) else "fail",
            f"{len(urls)} webhook URL(s)",
        )
    )
    checks.append(
        ConfigCheck(
            "No tab indentation",
            "ok" if "\t" not in content else "fail",
            "tabs=0" if "\t" not in content else "tabs found",
        )
    )
    checks.append(
        ConfigCheck(
            "Resolved alerts enabled",
            "ok" if content.count("send_resolved: true") >= len(REQUIRED_RECEIVERS) else "warn",
            f"send_resolved={content.count('send_resolved: true')}",
        )
    )
    return checks


def result_exit_code(results: list[ConfigCheck]) -> int:
    return 1 if any(result.status == "fail" for result in results) else 0


def print_results(results: list[ConfigCheck]) -> None:
    print("Confirmation lights:")
    for result in results:
        print(f"  {light(result.status)} {result.name}: {result.detail}")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Validate Packaging Platform Alertmanager routing examples.")
    parser.add_argument("command", choices=["check"], nargs="?", default="check")
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG)
    return parser


def main() -> int:
    args = build_parser().parse_args()
    content = read_config(args.config)
    results = validate_config(content)
    print_results(results)
    return result_exit_code(results)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(f"{light('fail')} {exc}", file=sys.stderr)
        sys.exit(1)


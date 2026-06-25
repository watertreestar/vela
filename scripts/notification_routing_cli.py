#!/usr/bin/env python3
"""Validate and optionally dispatch Packaging Platform notifications by severity."""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from typing import Any


DEFAULT_API_URL = "http://127.0.0.1:8080"
VALID_SEVERITIES = {"info", "success", "warning", "error", "critical"}
DEFAULT_REQUIRED_SEVERITIES = ("critical", "error")


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


def normalize_api_url(api_url: str) -> str:
    return api_url.rstrip("/")


def bearer_token(args: argparse.Namespace) -> str:
    return args.token or os.environ.get("PACKAGING_API_TOKEN") or os.environ.get("AUTH_ADMIN_TOKEN") or ""


def auth_headers(token: str) -> dict[str, str]:
    if not token:
        return {}
    return {"Authorization": f"Bearer {token}"}


def request_json(api_url: str, method: str, path: str, token: str, timeout: int) -> Any:
    request = urllib.request.Request(
        f"{normalize_api_url(api_url)}{path}",
        headers={"Accept": "application/json", **auth_headers(token)},
        method=method,
    )
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            payload = response.read()
            if not payload:
                return None
            return json.loads(payload.decode("utf-8"))
    except urllib.error.HTTPError as exc:
        payload = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{method} {path} failed: HTTP {exc.code}: {payload}") from exc
    except urllib.error.URLError as exc:
        raise RuntimeError(f"{method} {path} failed: {exc.reason}") from exc


def post_json(url: str, payload: dict[str, Any], timeout: int) -> int:
    request = urllib.request.Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json", "Accept": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            response.read()
            return response.status
    except urllib.error.HTTPError as exc:
        exc.read()
        return exc.code
    except urllib.error.URLError as exc:
        raise RuntimeError(str(exc.reason)) from exc


def parse_route(raw: str) -> tuple[str, str]:
    if "=" not in raw:
        raise ValueError(f"route must be severity=url: {raw}")
    severity, url = raw.split("=", 1)
    severity = severity.strip().lower()
    url = url.strip()
    if severity not in VALID_SEVERITIES:
        raise ValueError(f"invalid notification severity in route: {severity}")
    if not (url.startswith("http://") or url.startswith("https://")):
        raise ValueError(f"route URL must be http(s): {raw}")
    return severity, url


def routes_from_args(args: argparse.Namespace) -> dict[str, str]:
    routes: dict[str, str] = {}
    for raw in args.route:
        severity, url = parse_route(raw)
        routes[severity] = url
    return routes


def required_severities(args: argparse.Namespace) -> list[str]:
    values = args.require_severity or list(DEFAULT_REQUIRED_SEVERITIES)
    return [value.strip().lower() for value in values if value.strip()]


def notification_id(notification: dict[str, Any]) -> str:
    return str(notification.get("id") or "<unknown>")


def notification_severity(notification: dict[str, Any]) -> str:
    return str(notification.get("severity") or "").strip().lower()


def check_severity_contract(notifications: list[dict[str, Any]]) -> CheckResult:
    invalid = [notification_id(item) for item in notifications if notification_severity(item) not in VALID_SEVERITIES]
    return CheckResult(
        "Notification severity contract",
        "fail" if invalid else "ok",
        f"invalid={','.join(invalid) or 'none'}",
    )


def check_route_coverage(routes: dict[str, str], required: list[str]) -> CheckResult:
    missing = [severity for severity in required if severity not in routes]
    return CheckResult(
        "Notification route coverage",
        "fail" if missing else "ok",
        f"routes={','.join(sorted(routes)) or 'none'}, missing={','.join(missing) or 'none'}",
    )


def dispatch_payload(notification: dict[str, Any]) -> dict[str, Any]:
    return {
        "schema": "packaging.notification.dispatch/v1",
        "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "notification": notification,
    }


def dispatch_notifications(args: argparse.Namespace, notifications: list[dict[str, Any]], routes: dict[str, str]) -> CheckResult:
    routed = [notification for notification in notifications if notification_severity(notification) in routes]
    if not args.send:
        return CheckResult("Notification dispatch", "ok", f"dryRun={len(routed)}, routes={len(routes)}")
    failures: list[str] = []
    sent = 0
    for notification in routed:
        severity = notification_severity(notification)
        try:
            status = post_json(routes[severity], dispatch_payload(notification), args.timeout)
        except Exception as exc:
            failures.append(f"{notification_id(notification)}=error:{exc}")
            continue
        if 200 <= status < 300:
            sent += 1
        else:
            failures.append(f"{notification_id(notification)}=HTTP {status}")
    if failures:
        return CheckResult("Notification dispatch", "fail", "; ".join(failures))
    return CheckResult("Notification dispatch", "ok", f"sent={sent}, routes={len(routes)}")


def run_checks(args: argparse.Namespace) -> list[CheckResult]:
    token = bearer_token(args)
    try:
        routes = routes_from_args(args)
    except ValueError as exc:
        return [CheckResult("Notification routes", "fail", str(exc))]
    try:
        payload = request_json(args.api_url, "GET", f"/api/notifications?limit={args.limit}", token, args.timeout)
    except Exception as exc:
        return [CheckResult("Notification API", "fail", str(exc))]
    if not isinstance(payload, list):
        return [CheckResult("Notification API", "fail", f"unexpected response: {payload!r}")]
    notifications = [item for item in payload if isinstance(item, dict)]
    results = [
        CheckResult("Notification API", "ok", f"{len(notifications)} notification(s)"),
        check_severity_contract(notifications),
        check_route_coverage(routes, required_severities(args)),
    ]
    if results[-1].status != "fail" and results[1].status != "fail":
        results.append(dispatch_notifications(args, notifications, routes))
    return results


def result_exit_code(results: list[CheckResult], fail_on_warning: bool) -> int:
    if any(result.status == "fail" for result in results):
        return 1
    if fail_on_warning and any(result.status == "warn" for result in results):
        return 1
    return 0


def print_results(results: list[CheckResult]) -> None:
    print("Confirmation lights:")
    for result in results:
        print(f"  {light(result.status)} {result.name}: {result.detail}")


def print_json(results: list[CheckResult]) -> None:
    payload = {
        "status": "fail"
        if any(result.status == "fail" for result in results)
        else "warn"
        if any(result.status == "warn" for result in results)
        else "ok",
        "checks": [asdict(result) for result in results],
    }
    print(json.dumps(payload, ensure_ascii=False, indent=2))


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Validate and optionally dispatch Packaging Platform notifications by severity.")
    subparsers = parser.add_subparsers(dest="command")
    check = subparsers.add_parser("check", help="check notification routing and optionally send webhooks")
    check.add_argument("--api-url", default=DEFAULT_API_URL)
    check.add_argument("--token", help="defaults to PACKAGING_API_TOKEN or AUTH_ADMIN_TOKEN")
    check.add_argument("--route", action="append", default=[], help="severity=webhookUrl; repeat for multiple severities")
    check.add_argument("--require-severity", action="append", default=[], help="severity that must have a configured route; defaults to critical and error")
    check.add_argument("--send", action="store_true", help="actually POST matching notifications to configured webhook routes")
    check.add_argument("--limit", type=int, default=50)
    check.add_argument("--timeout", type=int, default=20)
    check.add_argument("--fail-on-warning", action="store_true")
    check.add_argument("--json", action="store_true")
    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args(["check"] if len(sys.argv) == 1 else None)
    if args.command != "check":
        parser.error(f"unknown command: {args.command}")
    results = run_checks(args)
    if args.json:
        print_json(results)
    else:
        print_results(results)
    return result_exit_code(results, args.fail_on_warning)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(f"{light('fail')} {exc}", file=sys.stderr)
        sys.exit(1)

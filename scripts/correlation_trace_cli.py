#!/usr/bin/env python3
"""Trace request/correlation IDs across Packaging Platform resources."""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from typing import Any


DEFAULT_API_URL = "http://127.0.0.1:8080"
REQUEST_ID_KEYS = ("requestId", "request_id")
CORRELATION_ID_KEYS = ("correlationId", "correlation_id")


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


def values_for_keys(value: Any, keys: tuple[str, ...]) -> set[str]:
    found: set[str] = set()
    if isinstance(value, dict):
        for key, nested in value.items():
            if key in keys and nested is not None:
                found.add(str(nested))
            if isinstance(nested, (dict, list)):
                found.update(values_for_keys(nested, keys))
    elif isinstance(value, list):
        for item in value:
            if isinstance(item, (dict, list)):
                found.update(values_for_keys(item, keys))
    return found


def matches_trace(value: dict[str, Any], request_id: str, correlation_id: str) -> bool:
    if request_id and request_id not in values_for_keys(value, REQUEST_ID_KEYS):
        return False
    if correlation_id and correlation_id not in values_for_keys(value, CORRELATION_ID_KEYS):
        return False
    return bool(request_id or correlation_id)


def as_list(payload: Any, endpoint: str) -> list[dict[str, Any]]:
    if not isinstance(payload, list):
        raise RuntimeError(f"{endpoint} returned unexpected response: {payload!r}")
    return [item for item in payload if isinstance(item, dict)]


def short_text(value: Any, max_len: int = 180) -> str:
    text = str(value or "")
    if len(text) <= max_len:
        return text
    return text[: max_len - 3] + "..."


def compact_audit(event: dict[str, Any]) -> dict[str, Any]:
    return {
        "id": event.get("id"),
        "action": event.get("action"),
        "resourceType": event.get("resourceType"),
        "resourceId": event.get("resourceId"),
        "buildId": event.get("buildId"),
        "productId": event.get("productId"),
        "uploadId": event.get("uploadId"),
        "actorId": event.get("actorId"),
        "actorRole": event.get("actorRole"),
        "requestId": event.get("requestId"),
        "correlationId": event.get("correlationId"),
        "createdAt": event.get("createdAt"),
    }


def compact_build(build: dict[str, Any]) -> dict[str, Any]:
    return {
        "id": build.get("id"),
        "productId": build.get("productId"),
        "target": build.get("target"),
        "profile": build.get("profile"),
        "customer": build.get("customer"),
        "version": build.get("version"),
        "releaseChannel": build.get("releaseChannel"),
        "status": build.get("status"),
        "runnerId": build.get("runnerId"),
        "artifactUri": build.get("artifactUri"),
        "downloadUrl": build.get("downloadUrl"),
        "updatedAt": build.get("updatedAt"),
    }


def compact_build_log(build_id: str, log: dict[str, Any]) -> dict[str, Any]:
    return {
        "buildId": build_id,
        "sequence": log.get("sequence"),
        "time": log.get("time"),
        "source": log.get("source"),
        "message": short_text(log.get("message")),
        "requestId": log.get("requestId"),
        "correlationId": log.get("correlationId"),
    }


def compact_upload(upload: dict[str, Any]) -> dict[str, Any]:
    return {
        "id": upload.get("id"),
        "fileName": upload.get("fileName"),
        "uri": upload.get("uri"),
        "downloadUrl": upload.get("downloadUrl"),
        "sha256": upload.get("sha256"),
        "downloadPolicy": upload.get("downloadPolicy"),
        "retentionStatus": upload.get("retentionStatus"),
        "requestId": upload.get("requestId"),
        "correlationId": upload.get("correlationId"),
        "createdAt": upload.get("createdAt"),
    }


def compact_notification(notification: dict[str, Any]) -> dict[str, Any]:
    return {
        "id": notification.get("id"),
        "type": notification.get("type"),
        "severity": notification.get("severity"),
        "title": notification.get("title"),
        "resourceType": notification.get("resourceType"),
        "resourceId": notification.get("resourceId"),
        "buildId": notification.get("buildId"),
        "productId": notification.get("productId"),
        "uploadId": notification.get("uploadId"),
        "runnerId": notification.get("runnerId"),
        "createdAt": notification.get("createdAt"),
        "readAt": notification.get("readAt"),
    }


def matching_logs(
    args: argparse.Namespace,
    api_url: str,
    token: str,
    builds: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    matched: list[dict[str, Any]] = []
    seen: set[tuple[str, str, str, str]] = set()
    for build in builds:
        build_id = str(build.get("id") or "")
        logs = [item for item in build.get("logs", []) if isinstance(item, dict)]
        if args.scan_build_log_endpoints and build_id:
            payload = request_json(api_url, "GET", f"/api/builds/{urllib.parse.quote(build_id)}/logs", token, args.timeout)
            logs = as_list(payload, f"/api/builds/{build_id}/logs")
        for log in logs:
            if not matches_trace(log, args.request_id, args.correlation_id):
                continue
            compact = compact_build_log(build_id, log)
            identity = (
                str(compact.get("buildId") or ""),
                str(compact.get("sequence") or ""),
                str(compact.get("requestId") or ""),
                str(compact.get("correlationId") or ""),
            )
            if identity in seen:
                continue
            seen.add(identity)
            matched.append(compact)
    return matched


def build_checks(load_errors: list[str], summary: dict[str, int]) -> list[CheckResult]:
    total_matches = sum(summary.values())
    checks = [
        CheckResult("Trace inputs", "ok", "requestId/correlationId provided"),
        CheckResult("Trace matches", "warn" if total_matches == 0 else "ok", f"matches={total_matches}"),
    ]
    for error in load_errors:
        checks.append(CheckResult("Trace source load", "fail", error))
    return checks


def report_status(checks: list[CheckResult]) -> str:
    if any(check.status == "fail" for check in checks):
        return "fail"
    if any(check.status == "warn" for check in checks):
        return "warn"
    return "ok"


def build_trace_report(args: argparse.Namespace) -> dict[str, Any]:
    if not args.request_id and not args.correlation_id:
        raise ValueError("pass --request-id and/or --correlation-id")

    api_url = normalize_api_url(args.api_url)
    token = bearer_token(args)
    load_errors: list[str] = []
    matches: dict[str, list[dict[str, Any]]] = {
        "auditEvents": [],
        "builds": [],
        "buildLogs": [],
        "uploads": [],
        "notifications": [],
    }

    try:
        audit_payload = request_json(api_url, "GET", f"/api/audit-events?limit={args.limit}", token, args.timeout)
        audit_events = as_list(audit_payload, "/api/audit-events")
        matches["auditEvents"] = [compact_audit(event) for event in audit_events if matches_trace(event, args.request_id, args.correlation_id)]
    except Exception as exc:
        load_errors.append(str(exc))

    builds: list[dict[str, Any]] = []
    try:
        build_payload = request_json(api_url, "GET", "/api/builds", token, args.timeout)
        builds = as_list(build_payload, "/api/builds")
        matches["buildLogs"] = matching_logs(args, api_url, token, builds)
        matches["builds"] = [
            compact_build(build)
            for build in builds
            if matches_trace(build, args.request_id, args.correlation_id)
            or any(log.get("buildId") == build.get("id") for log in matches["buildLogs"])
        ]
    except Exception as exc:
        load_errors.append(str(exc))

    try:
        upload_payload = request_json(api_url, "GET", "/api/uploads", token, args.timeout)
        uploads = as_list(upload_payload, "/api/uploads")
        matches["uploads"] = [compact_upload(upload) for upload in uploads if matches_trace(upload, args.request_id, args.correlation_id)]
    except Exception as exc:
        load_errors.append(str(exc))

    try:
        notification_payload = request_json(api_url, "GET", f"/api/notifications?limit={args.limit}", token, args.timeout)
        notifications = as_list(notification_payload, "/api/notifications")
        matches["notifications"] = [
            compact_notification(notification)
            for notification in notifications
            if matches_trace(notification, args.request_id, args.correlation_id)
        ]
    except Exception as exc:
        load_errors.append(str(exc))

    summary = {key: len(value) for key, value in matches.items()}
    checks = build_checks(load_errors, summary)
    return {
        "schema": "packaging.correlation_trace.report/v1",
        "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "apiUrl": api_url,
        "query": {
            "requestId": args.request_id,
            "correlationId": args.correlation_id,
            "limit": args.limit,
            "scanBuildLogEndpoints": bool(args.scan_build_log_endpoints),
        },
        "status": report_status(checks),
        "summary": summary,
        "checks": [asdict(check) for check in checks],
        "matches": matches,
    }


def report_exit_code(report: dict[str, Any], fail_on_warning: bool) -> int:
    if report.get("status") == "fail":
        return 1
    if fail_on_warning and report.get("status") == "warn":
        return 1
    return 0


def print_results(report: dict[str, Any]) -> None:
    print("Confirmation lights:")
    for check in report["checks"]:
        print(f"  {light(check['status'])} {check['name']}: {check['detail']}")
    summary = report["summary"]
    print(
        "Trace summary: "
        f"auditEvents={summary['auditEvents']}, builds={summary['builds']}, buildLogs={summary['buildLogs']}, "
        f"uploads={summary['uploads']}, notifications={summary['notifications']}"
    )
    for name, records in report["matches"].items():
        if not records:
            continue
        print(f"\n{name}:")
        for record in records:
            identity = record.get("id") or record.get("buildId") or "<unknown>"
            request_id = record.get("requestId") or ""
            correlation_id = record.get("correlationId") or ""
            print(f"  - {identity} requestId={request_id} correlationId={correlation_id}")


def print_json(report: dict[str, Any]) -> None:
    print(json.dumps(report, ensure_ascii=False, indent=2))


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Trace request/correlation IDs across Packaging Platform resources.")
    subparsers = parser.add_subparsers(dest="command")
    trace = subparsers.add_parser("trace", help="query audit/build/upload/notification records by request or correlation ID")
    trace.add_argument("--api-url", default=DEFAULT_API_URL)
    trace.add_argument("--token", help="defaults to PACKAGING_API_TOKEN or AUTH_ADMIN_TOKEN")
    trace.add_argument("--request-id", default="")
    trace.add_argument("--correlation-id", default="")
    trace.add_argument("--limit", type=int, default=100)
    trace.add_argument("--scan-build-log-endpoints", action="store_true")
    trace.add_argument("--timeout", type=int, default=20)
    trace.add_argument("--fail-on-warning", action="store_true")
    trace.add_argument("--json", action="store_true")
    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args(["trace"] if len(sys.argv) == 1 else None)
    if args.command != "trace":
        parser.error(f"unknown command: {args.command}")
    report = build_trace_report(args)
    if args.json:
        print_json(report)
    else:
        print_results(report)
    return report_exit_code(report, args.fail_on_warning)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(f"{light('fail')} {exc}", file=sys.stderr)
        sys.exit(1)

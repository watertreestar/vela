#!/usr/bin/env python3
"""Monitor Vela API, artifacts, runners, and release gates."""

from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.error
import urllib.request
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from typing import Any


DEFAULT_API_URL = "http://127.0.0.1:8080"
UNHEALTHY_RUNNER_STATUSES = {"stale", "offline", "unknown"}


@dataclass
class CheckResult:
    name: str
    status: str
    detail: str


class Progress:
    def __init__(self, total: int) -> None:
        self.total = total
        self.current = 0

    def step(self, message: str) -> None:
        self.current += 1
        print(f"[{self.current:02d}/{self.total:02d}] {message}")


def light(status: str) -> str:
    if status == "ok":
        return "[OK]"
    if status == "warn":
        return "[WARN]"
    return "[FAIL]"


def normalize_api_url(api_url: str) -> str:
    return api_url.rstrip("/")


def request_json(api_url: str, method: str, path: str, body: dict[str, Any] | None = None) -> Any:
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(f"{normalize_api_url(api_url)}{path}", data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            payload = response.read()
            if not payload:
                return None
            return json.loads(payload.decode("utf-8"))
    except urllib.error.HTTPError as exc:
        payload = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{method} {path} failed: HTTP {exc.code}: {payload}") from exc
    except urllib.error.URLError as exc:
        raise RuntimeError(f"{method} {path} failed: {exc.reason}") from exc


def request_text(api_url: str, method: str, path: str) -> str:
    request = urllib.request.Request(f"{normalize_api_url(api_url)}{path}", method=method)
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            return response.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as exc:
        payload = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{method} {path} failed: HTTP {exc.code}: {payload}") from exc
    except urllib.error.URLError as exc:
        raise RuntimeError(f"{method} {path} failed: {exc.reason}") from exc


def parse_time(value: Any) -> datetime | None:
    if not isinstance(value, str) or not value:
        return None
    normalized = value.replace("Z", "+00:00")
    try:
        parsed = datetime.fromisoformat(normalized)
    except ValueError:
        return None
    if parsed.tzinfo is None:
        return parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc)


def age_minutes(value: Any, now: datetime) -> float | None:
    parsed = parse_time(value)
    if parsed is None:
        return None
    return (now - parsed).total_seconds() / 60


def count_by(items: list[dict[str, Any]], key: str) -> dict[str, int]:
    counts: dict[str, int] = {}
    for item in items:
        value = str(item.get(key) or "unknown")
        counts[value] = counts.get(value, 0) + 1
    return counts


def format_counts(counts: dict[str, int]) -> str:
    if not counts:
        return "none"
    return ", ".join(f"{key}={value}" for key, value in sorted(counts.items()))


def check_health(api_url: str) -> CheckResult:
    try:
        health = request_json(api_url, "GET", "/api/health")
        ok = isinstance(health, dict) and health.get("status") == "ok"
        return CheckResult("Packaging API health", "ok" if ok else "fail", f"{normalize_api_url(api_url)}/api/health")
    except Exception as exc:
        return CheckResult("Packaging API health", "fail", str(exc))


def check_products(api_url: str) -> CheckResult:
    try:
        products = request_json(api_url, "GET", "/api/products")
        if not isinstance(products, list):
            return CheckResult("Product manifests", "fail", f"unexpected response: {products!r}")
        return CheckResult("Product manifests", "ok" if products else "warn", f"{len(products)} product(s) loaded")
    except Exception as exc:
        return CheckResult("Product manifests", "fail", str(exc))


def load_builds(api_url: str) -> tuple[list[dict[str, Any]], CheckResult]:
    try:
        builds = request_json(api_url, "GET", "/api/builds")
        if not isinstance(builds, list):
            return [], CheckResult("Build API", "fail", f"unexpected response: {builds!r}")
        typed = [item for item in builds if isinstance(item, dict)]
        return typed, CheckResult("Build API", "ok", f"{len(typed)} build(s), status: {format_counts(count_by(typed, 'status'))}")
    except Exception as exc:
        return [], CheckResult("Build API", "fail", str(exc))


def check_build_backlog(builds: list[dict[str, Any]], max_queued: int, max_failed_builds: int) -> list[CheckResult]:
    queued = [build for build in builds if build.get("status") == "queued"]
    running_without_runner = [
        build for build in builds if build.get("status") == "running" and not build.get("runnerId")
    ]
    failed = [build for build in builds if build.get("status") == "failed"]

    queue_status = "warn" if len(queued) > max_queued else "ok"
    runner_status = "warn" if running_without_runner else "ok"
    failed_status = "warn" if len(failed) > max_failed_builds else "ok"

    return [
        CheckResult("Build queue backlog", queue_status, f"queued={len(queued)}, threshold={max_queued}"),
        CheckResult("Running build ownership", runner_status, f"running without runner={len(running_without_runner)}"),
        CheckResult("Failed build volume", failed_status, f"failed={len(failed)}, threshold={max_failed_builds}"),
    ]


def check_release_gates(builds: list[dict[str, Any]]) -> CheckResult:
    summaries = [
        build.get("releaseCheckSummary")
        for build in builds
        if isinstance(build.get("releaseCheckSummary"), dict)
    ]
    release_ready = [summary for summary in summaries if summary.get("releaseReady")]
    blocked = [summary for summary in summaries if int(summary.get("blockingCount") or 0) > 0]
    status = "warn" if blocked else "ok"
    return CheckResult(
        "Release gate summary",
        status,
        f"releaseReady={len(release_ready)}, blocked={len(blocked)}",
    )


def check_log_stream(api_url: str, builds: list[dict[str, Any]]) -> CheckResult:
    sampled = next((build for build in builds if build.get("id") and build.get("logs")), None)
    if sampled is None:
        return CheckResult("Build log stream", "ok", "no logged build to sample")
    build_id = sampled["id"]
    try:
        stream = request_text(api_url, "GET", f"/api/builds/{build_id}/logs/stream")
        ok = "event: log" in stream and "data:" in stream
        return CheckResult("Build log stream", "ok" if ok else "fail", f"sample build={build_id}")
    except Exception as exc:
        return CheckResult("Build log stream", "fail", str(exc))


def load_uploads(api_url: str) -> tuple[list[dict[str, Any]], CheckResult]:
    try:
        uploads = request_json(api_url, "GET", "/api/uploads")
        if not isinstance(uploads, list):
            return [], CheckResult("Upload API", "fail", f"unexpected response: {uploads!r}")
        typed = [item for item in uploads if isinstance(item, dict)]
        return typed, CheckResult("Upload API", "ok", f"{len(typed)} artifact upload(s)")
    except Exception as exc:
        return [], CheckResult("Upload API", "fail", str(exc))


def check_upload_metadata(uploads: list[dict[str, Any]], max_expired_artifacts: int) -> list[CheckResult]:
    missing_hash = [upload for upload in uploads if not upload.get("sha256")]
    missing_download = [upload for upload in uploads if not upload.get("downloadUrl")]
    missing_policy = [upload for upload in uploads if not upload.get("downloadPolicy")]
    expired = [upload for upload in uploads if upload.get("retentionStatus") == "expired"]

    metadata_failures = len(missing_hash) + len(missing_download) + len(missing_policy)
    metadata_detail = (
        f"missingSha256={len(missing_hash)}, missingDownloadUrl={len(missing_download)}, "
        f"missingDownloadPolicy={len(missing_policy)}"
    )

    retention_status = "warn" if len(expired) > max_expired_artifacts else "ok"
    return [
        CheckResult("Artifact metadata contract", "fail" if metadata_failures else "ok", metadata_detail),
        CheckResult(
            "Artifact retention",
            retention_status,
            f"expired={len(expired)}, threshold={max_expired_artifacts}",
        ),
    ]


def load_runners(api_url: str) -> tuple[list[dict[str, Any]], CheckResult]:
    try:
        runners = request_json(api_url, "GET", "/api/runners")
        if not isinstance(runners, list):
            return [], CheckResult("Runner API", "fail", f"unexpected response: {runners!r}")
        typed = [item for item in runners if isinstance(item, dict)]
        return typed, CheckResult("Runner API", "ok", f"{len(typed)} runner(s), status: {format_counts(count_by(typed, 'status'))}")
    except Exception as exc:
        return [], CheckResult("Runner API", "fail", str(exc))


def check_runner_health(
    runners: list[dict[str, Any]],
    max_unhealthy_runners: int,
    runner_stale_minutes: int,
    now: datetime,
) -> list[CheckResult]:
    unhealthy = [runner for runner in runners if runner.get("status") in UNHEALTHY_RUNNER_STATUSES]
    old_active_heartbeats = []
    for runner in runners:
        if runner.get("currentBuildId") or int(runner.get("runningBuilds") or 0) > 0:
            age = age_minutes(runner.get("lastHeartbeatAt") or runner.get("lastLogAt"), now)
            if age is None or age > runner_stale_minutes:
                old_active_heartbeats.append(runner)

    unhealthy_status = "warn" if len(unhealthy) > max_unhealthy_runners else "ok"
    heartbeat_status = "warn" if old_active_heartbeats else "ok"
    return [
        CheckResult(
            "Runner availability",
            unhealthy_status,
            f"unhealthy={len(unhealthy)}, threshold={max_unhealthy_runners}",
        ),
        CheckResult(
            "Active runner heartbeat",
            heartbeat_status,
            f"staleActive={len(old_active_heartbeats)}, staleMinutes={runner_stale_minutes}",
        ),
    ]


def run_checks(args: argparse.Namespace) -> list[CheckResult]:
    now = datetime.now(timezone.utc)
    results: list[CheckResult] = []

    results.append(check_health(args.api_url))
    results.append(check_products(args.api_url))

    builds, build_check = load_builds(args.api_url)
    results.append(build_check)
    if build_check.status != "fail":
        results.extend(check_build_backlog(builds, args.max_queued_builds, args.max_failed_builds))
        results.append(check_release_gates(builds))
        results.append(check_log_stream(args.api_url, builds))

    uploads, upload_check = load_uploads(args.api_url)
    results.append(upload_check)
    if upload_check.status != "fail":
        results.extend(check_upload_metadata(uploads, args.max_expired_artifacts))

    runners, runner_check = load_runners(args.api_url)
    results.append(runner_check)
    if runner_check.status != "fail":
        results.extend(check_runner_health(runners, args.max_unhealthy_runners, args.runner_stale_minutes, now))

    return results


def result_exit_code(results: list[CheckResult], fail_on_warning: bool) -> int:
    if any(result.status == "fail" for result in results):
        return 1
    if fail_on_warning and any(result.status == "warn" for result in results):
        return 1
    return 0


def print_checks(results: list[CheckResult]) -> None:
    print("\nConfirmation lights:")
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


def check(args: argparse.Namespace) -> int:
    if not args.json:
        progress = Progress(total=1)
        progress.step("Run monitoring checks")
    results = run_checks(args)
    if args.json:
        print_json(results)
    else:
        print_checks(results)
    return result_exit_code(results, args.fail_on_warning)


def watch(args: argparse.Namespace) -> int:
    iteration = 0
    last_code = 0
    while args.iterations == 0 or iteration < args.iterations:
        iteration += 1
        if not args.json:
            print(f"\nWatch iteration {iteration}")
        results = run_checks(args)
        if args.json:
            print_json(results)
        else:
            print_checks(results)
        last_code = result_exit_code(results, args.fail_on_warning)
        if args.exit_on_failure and last_code != 0:
            return last_code
        if args.iterations != 0 and iteration >= args.iterations:
            break
        time.sleep(args.interval)
    return last_code


def add_common_args(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--api-url", default=DEFAULT_API_URL)
    parser.add_argument("--fail-on-warning", action="store_true", help="return non-zero when any warning is found")
    parser.add_argument("--json", action="store_true", help="print machine-readable check results")
    parser.add_argument("--max-queued-builds", type=int, default=10)
    parser.add_argument("--max-failed-builds", type=int, default=20)
    parser.add_argument("--max-expired-artifacts", type=int, default=0)
    parser.add_argument("--max-unhealthy-runners", type=int, default=0)
    parser.add_argument("--runner-stale-minutes", type=int, default=5)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Vela monitoring CLI.")
    subparsers = parser.add_subparsers(dest="command")

    check_parser = subparsers.add_parser("check", help="run one monitoring pass")
    add_common_args(check_parser)

    watch_parser = subparsers.add_parser("watch", help="run monitoring checks repeatedly")
    add_common_args(watch_parser)
    watch_parser.add_argument("--interval", type=int, default=30, help="seconds between checks")
    watch_parser.add_argument("--iterations", type=int, default=0, help="0 means run until interrupted")
    watch_parser.add_argument("--exit-on-failure", action="store_true")

    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args(["check"] if len(sys.argv) == 1 else None)
    if args.command == "check":
        return check(args)
    if args.command == "watch":
        return watch(args)
    parser.error(f"unknown command: {args.command}")
    return 2


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        print("\n[WARN] monitor stopped by user", file=sys.stderr)
        sys.exit(130)
    except Exception as exc:
        print(f"{light('fail')} {exc}", file=sys.stderr)
        sys.exit(1)

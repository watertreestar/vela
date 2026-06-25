#!/usr/bin/env python3
"""Validate artifact retention metadata and expired download enforcement."""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from dataclasses import asdict, dataclass
from typing import Any


DEFAULT_API_URL = "http://127.0.0.1:8080"
VALID_RETENTION_STATUSES = {"active", "expired", "permanent"}
VALID_DOWNLOAD_POLICIES = {"public", "platform", "restricted"}


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


def request_status(url: str, token: str, timeout: int) -> int:
    request = urllib.request.Request(url, headers=auth_headers(token), method="GET")
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return response.status
    except urllib.error.HTTPError as exc:
        exc.read()
        return exc.code
    except urllib.error.URLError as exc:
        raise RuntimeError(str(exc.reason)) from exc


def count_by(uploads: list[dict[str, Any]], key: str) -> dict[str, int]:
    counts: dict[str, int] = {}
    for upload in uploads:
        value = str(upload.get(key) or "missing")
        counts[value] = counts.get(value, 0) + 1
    return counts


def format_counts(counts: dict[str, int]) -> str:
    if not counts:
        return "none"
    return ", ".join(f"{key}={value}" for key, value in sorted(counts.items()))


def check_metadata_contract(uploads: list[dict[str, Any]]) -> CheckResult:
    missing_sha = [upload for upload in uploads if not upload.get("sha256")]
    missing_download = [upload for upload in uploads if not upload.get("downloadUrl")]
    invalid_policy = [upload for upload in uploads if upload.get("downloadPolicy") not in VALID_DOWNLOAD_POLICIES]
    invalid_status = [upload for upload in uploads if upload.get("retentionStatus") not in VALID_RETENTION_STATUSES]
    failures = len(missing_sha) + len(missing_download) + len(invalid_policy) + len(invalid_status)
    return CheckResult(
        "Artifact metadata contract",
        "fail" if failures else "ok",
        (
            f"missingSha256={len(missing_sha)}, missingDownloadUrl={len(missing_download)}, "
            f"invalidDownloadPolicy={len(invalid_policy)}, invalidRetentionStatus={len(invalid_status)}"
        ),
    )


def check_expired_threshold(uploads: list[dict[str, Any]], max_expired_artifacts: int) -> CheckResult:
    expired = [upload for upload in uploads if upload.get("retentionStatus") == "expired"]
    status = "warn" if len(expired) > max_expired_artifacts else "ok"
    return CheckResult("Expired artifact threshold", status, f"expired={len(expired)}, threshold={max_expired_artifacts}")


def check_retention_distribution(uploads: list[dict[str, Any]]) -> CheckResult:
    return CheckResult("Artifact retention distribution", "ok", format_counts(count_by(uploads, "retentionStatus")))


def check_expired_downloads(args: argparse.Namespace, uploads: list[dict[str, Any]], token: str) -> CheckResult:
    expired = [upload for upload in uploads if upload.get("retentionStatus") == "expired" and upload.get("downloadUrl")]
    if not expired:
        return CheckResult("Expired artifact download rejection", "ok", "no expired artifact download to sample")
    sampled = expired[: max(1, args.sample_expired_downloads)]
    wrong: list[str] = []
    checked = 0
    for upload in sampled:
        upload_id = str(upload.get("id") or "<unknown>")
        try:
            status = request_status(str(upload["downloadUrl"]), token, args.timeout)
        except Exception as exc:
            wrong.append(f"{upload_id}=error:{exc}")
            continue
        checked += 1
        if status != 410:
            wrong.append(f"{upload_id}=HTTP {status}")
    if wrong:
        return CheckResult("Expired artifact download rejection", "fail", "; ".join(wrong))
    return CheckResult("Expired artifact download rejection", "ok", f"checked={checked}, expectedHttp=410")


def run_checks(args: argparse.Namespace) -> list[CheckResult]:
    token = bearer_token(args)
    results: list[CheckResult] = []
    try:
        payload = request_json(args.api_url, "GET", "/api/uploads", token, args.timeout)
    except Exception as exc:
        return [CheckResult("Upload API", "fail", str(exc))]
    if not isinstance(payload, list):
        return [CheckResult("Upload API", "fail", f"unexpected response: {payload!r}")]
    uploads = [upload for upload in payload if isinstance(upload, dict)]
    results.append(CheckResult("Upload API", "ok", f"{len(uploads)} artifact upload(s)"))
    results.append(check_metadata_contract(uploads))
    results.append(check_retention_distribution(uploads))
    results.append(check_expired_threshold(uploads, args.max_expired_artifacts))
    if not args.skip_expired_download_check:
        results.append(check_expired_downloads(args, uploads, token))
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
    parser = argparse.ArgumentParser(description="Validate artifact retention metadata and expired download enforcement.")
    subparsers = parser.add_subparsers(dest="command")
    check = subparsers.add_parser("check", help="check upload metadata and expired download behavior")
    check.add_argument("--api-url", default=DEFAULT_API_URL)
    check.add_argument("--token", help="defaults to PACKAGING_API_TOKEN or AUTH_ADMIN_TOKEN")
    check.add_argument("--max-expired-artifacts", type=int, default=0)
    check.add_argument("--sample-expired-downloads", type=int, default=3)
    check.add_argument("--skip-expired-download-check", action="store_true")
    check.add_argument("--fail-on-warning", action="store_true")
    check.add_argument("--timeout", type=int, default=20)
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

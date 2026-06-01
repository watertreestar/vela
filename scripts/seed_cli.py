#!/usr/bin/env python3
"""Seed flow-validation data for local Packaging Platform testing."""

from __future__ import annotations

import argparse
import json
import sys
import urllib.error
import urllib.request
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any


DEFAULT_API_URL = "http://127.0.0.1:8080"
DEFAULT_NAMESPACE = "flow-validation"


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


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def request_json(api_url: str, method: str, path: str, body: dict[str, Any] | None = None) -> Any:
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(f"{api_url}{path}", data=data, headers=headers, method=method)
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


def upload_seed_file(api_url: str, filename: str, content: str) -> dict[str, Any]:
    boundary = f"----packaging-seed-{uuid.uuid4().hex}"
    body = b"".join(
        [
            f"--{boundary}\r\n".encode(),
            f'Content-Disposition: form-data; name="file"; filename="{filename}"\r\n'.encode(),
            b"Content-Type: application/octet-stream\r\n\r\n",
            content.encode("utf-8"),
            b"\r\n",
            f"--{boundary}--\r\n".encode(),
        ]
    )
    request = urllib.request.Request(
        f"{api_url}/api/uploads",
        data=body,
        headers={"Content-Type": f"multipart/form-data; boundary={boundary}"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        payload = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"upload {filename} failed: HTTP {exc.code}: {payload}") from exc


def component_overrides(api_url: str, upload: dict[str, Any]) -> dict[str, Any]:
    return {
        "location-engine": {
            "kind": "fixed-url",
            "url": f"{api_url}/seed-artifacts/location-engine-1.0.0.tar.gz",
        },
        "location-guard": {
            "kind": "fixed-url",
            "url": f"{api_url}/seed-artifacts/location-guard-1.0.0.tar.gz",
        },
        "location-push": {
            "kind": "fixed-url",
            "url": f"{api_url}/seed-artifacts/location-push-1.0.0.tar.gz",
        },
        "ui": {
            "kind": "fixed-url",
            "url": f"{api_url}/seed-artifacts/ui-1.0.0.zip",
        },
        "license": {
            "kind": "manual-upload",
            "uploadId": upload["id"],
            "uri": upload["uri"],
        },
    }


def build_request(
    scenario: str,
    target: str,
    profile: str,
    version: str,
    namespace: str,
    overrides: dict[str, Any] | None,
) -> dict[str, Any]:
    return {
        "productId": "lsap",
        "target": target,
        "profile": profile,
        "customer": "example-customer",
        "version": version,
        "releaseChannel": "preview",
        "variables": {
            "seedNamespace": namespace,
            "seedScenario": scenario,
            "requestedBy": "seed_cli.py",
        },
        "configOverrides": {
            "installDir": "C:\\LSAP-Validation" if target == "windows" else "/opt/lsap-validation",
            "ports": {"web": 49100},
            "customer": {"name": "Validation Customer", "license": "enterprise"},
            "database": {"passwordPolicy": "strong"},
            "modules": {"camera": False, "push": True, "map": True},
        },
        "componentOverrides": overrides or {},
    }


def find_seed_build(builds: list[dict[str, Any]], namespace: str, scenario: str) -> dict[str, Any] | None:
    for build in builds:
        variables = build.get("variables") or {}
        if variables.get("seedNamespace") == namespace and variables.get("seedScenario") == scenario:
            return build
    return None


def ensure_build(
    api_url: str,
    existing: list[dict[str, Any]],
    namespace: str,
    scenario: str,
    body: dict[str, Any],
) -> dict[str, Any]:
    found = find_seed_build(existing, namespace, scenario)
    if found:
        print(f"    {light('ok')} exists {scenario}: {found['id']} [{found['status']}]")
        return found
    created = request_json(api_url, "POST", "/api/builds", body)
    if not isinstance(created, dict):
        raise RuntimeError(f"unexpected create build response for {scenario}: {created!r}")
    print(f"    {light('ok')} created {scenario}: {created['id']} [{created['status']}]")
    return created


def approve_build(api_url: str, build: dict[str, Any]) -> dict[str, Any]:
    if build["status"] in {"queued", "running", "succeeded", "preview_validated", "published"}:
        return build
    updated = request_json(api_url, "POST", f"/api/builds/{build['id']}/approve", {})
    if not isinstance(updated, dict):
        raise RuntimeError(f"approve returned unexpected response for {build['id']}: {updated!r}")
    return updated


def append_log(api_url: str, build_id: str, source: str, message: str) -> None:
    request_json(api_url, "POST", f"/api/builds/{build_id}/logs", {"source": source, "message": message})


def claim_build(api_url: str, build: dict[str, Any], runner_id: str) -> dict[str, Any]:
    if build["status"] in {"running", "succeeded", "preview_validated", "published", "failed"}:
        return build
    claimed = request_json(
        api_url,
        "POST",
        "/api/runner/tasks/claim",
        {"runnerId": runner_id, "target": build["target"]},
    )
    if not isinstance(claimed, dict):
        raise RuntimeError(f"runner {runner_id} did not claim a queued {build['target']} build")
    if claimed["id"] != build["id"]:
        raise RuntimeError(
            f"runner {runner_id} claimed {claimed['id']} instead of seed build {build['id']}; "
            "clear queued non-seed builds before running seed"
        )
    append_log(api_url, build["id"], runner_id, "flow-validation runner claimed build")
    return claimed


def complete_build(
    api_url: str,
    build: dict[str, Any],
    status: str,
    artifact: dict[str, Any] | None = None,
    error: str = "",
) -> dict[str, Any]:
    if build["status"] in {"succeeded", "preview_validated", "published", "failed"}:
        return build
    body: dict[str, Any] = {"status": status}
    if status == "succeeded" and artifact:
        body["artifactUri"] = artifact["uri"]
        body["downloadUrl"] = artifact["downloadUrl"]
    if status == "failed":
        body["error"] = error or "seed: component checksum mismatch"
    updated = request_json(api_url, "POST", f"/api/runner/tasks/{build['id']}/complete", body)
    if not isinstance(updated, dict):
        raise RuntimeError(f"complete returned unexpected response for {build['id']}: {updated!r}")
    return updated


def validate_preview(api_url: str, build: dict[str, Any]) -> dict[str, Any]:
    if build["status"] in {"preview_validated", "published"}:
        return build
    updated = request_json(api_url, "POST", f"/api/builds/{build['id']}/validate-preview", {})
    if not isinstance(updated, dict):
        raise RuntimeError(f"validate-preview returned unexpected response for {build['id']}: {updated!r}")
    return updated


def run_to_terminal_state(
    api_url: str,
    build: dict[str, Any],
    runner_id: str,
    terminal_status: str,
    artifact: dict[str, Any] | None = None,
    error: str = "",
    validate: bool = False,
) -> dict[str, Any]:
    if build["status"] == "pending_approval":
        build = approve_build(api_url, build)
    if build["status"] == "queued":
        build = claim_build(api_url, build, runner_id)
    if build["status"] == "running":
        build = complete_build(api_url, build, terminal_status, artifact, error)
    if validate and build["status"] == "succeeded":
        build = validate_preview(api_url, build)
    return build


def queued_non_seed_builds(builds: list[dict[str, Any]], namespace: str) -> list[dict[str, Any]]:
    result = []
    for build in builds:
        variables = build.get("variables") or {}
        if build.get("status") == "queued" and variables.get("seedNamespace") != namespace:
            result.append(build)
    return result


def run_checks(api_url: str, namespace: str) -> list[CheckResult]:
    results: list[CheckResult] = []
    try:
        health = request_json(api_url, "GET", "/api/health")
        status = "ok" if isinstance(health, dict) and health.get("status") == "ok" else "fail"
        results.append(CheckResult("Packaging API health", status, f"{api_url}/api/health"))
    except Exception as exc:
        results.append(CheckResult("Packaging API health", "fail", str(exc)))

    try:
        products = request_json(api_url, "GET", "/api/products")
        has_lsap = isinstance(products, list) and any(product.get("id") == "lsap" for product in products)
        results.append(CheckResult("Product manifest lsap", "ok" if has_lsap else "fail", "product lsap is loaded"))
    except Exception as exc:
        results.append(CheckResult("Product manifest lsap", "fail", str(exc)))

    try:
        builds = request_json(api_url, "GET", "/api/builds")
        ok = isinstance(builds, list)
        results.append(CheckResult("Build API", "ok" if ok else "fail", "list builds endpoint is reachable"))
        if ok:
            external_queued = queued_non_seed_builds(builds, namespace)
            status = "warn" if external_queued else "ok"
            detail = (
                "no non-seed queued builds"
                if not external_queued
                else f"{len(external_queued)} non-seed queued build(s) may be claimed by runner APIs"
            )
            results.append(CheckResult("Runner claim queue isolation", status, detail))
    except Exception as exc:
        results.append(CheckResult("Build API", "fail", str(exc)))

    return results


def print_checks(results: list[CheckResult]) -> None:
    print("\nConfirmation lights:")
    for result in results:
        print(f"  {light(result.status)} {result.name}: {result.detail}")


def ensure_preflight(api_url: str, namespace: str, strict_queue: bool = False) -> None:
    checks = run_checks(api_url, namespace)
    print_checks(checks)
    failed = [item for item in checks if item.status == "fail"]
    if failed:
        raise RuntimeError("preflight checks failed; fix the failed items before seeding")
    if strict_queue:
        queue_warnings = [
            item
            for item in checks
            if item.name == "Runner claim queue isolation" and item.status == "warn"
        ]
        if queue_warnings:
            raise RuntimeError(
                "queued non-seed builds may be claimed by runner APIs; "
                "clear or cancel them before running seed"
            )


def seed(args: argparse.Namespace) -> int:
    progress = Progress(total=6)

    progress.step("Run environment checks")
    ensure_preflight(args.api_url, args.namespace, strict_queue=True)

    progress.step("Upload validation input files")
    license_upload = upload_seed_file(args.api_url, "validation-license.lic", "license for flow validation")
    package_upload = upload_seed_file(args.api_url, "preview-package.tar.gz", "preview package payload")
    windows_upload = upload_seed_file(args.api_url, "windows-setup.exe", "windows setup payload")
    overrides = component_overrides(args.api_url, license_upload)

    progress.step("Load existing build records")
    existing = request_json(args.api_url, "GET", "/api/builds")
    if not isinstance(existing, list):
        raise RuntimeError(f"unexpected builds response: {existing!r}")

    progress.step("Create or reuse scenario builds")
    builds_by_scenario: dict[str, dict[str, Any]] = {}

    workflow_specs = [
        ("03-succeeded preview", "linux", "qa", "1.0.0", overrides, "succeeded", "seed-linux-runner-01", package_upload, "", False),
        (
            "04-validated release-ready",
            "linux",
            "prod",
            "1.0.0",
            overrides,
            "succeeded",
            "seed-linux-runner-02",
            package_upload,
            "",
            True,
        ),
        (
            "05-release blocked latest",
            "linux",
            "qa",
            "latest",
            None,
            "succeeded",
            "seed-linux-runner-03",
            package_upload,
            "",
            True,
        ),
        (
            "06-windows preview succeeded",
            "windows",
            "qa",
            "1.0.0",
            overrides,
            "succeeded",
            "seed-windows-runner-01",
            windows_upload,
            "",
            False,
        ),
        (
            "07-failed retryable",
            "linux",
            "qa",
            "1.0.0",
            overrides,
            "failed",
            "seed-linux-runner-04",
            None,
            "seed: component checksum mismatch",
            False,
        ),
    ]

    for scenario, target, profile, version, item_overrides, terminal_status, runner_id, artifact, error, should_validate in workflow_specs:
        body = build_request(scenario, target, profile, version, args.namespace, item_overrides)
        build = ensure_build(args.api_url, existing, args.namespace, scenario, body)
        build = run_to_terminal_state(
            args.api_url,
            build,
            runner_id,
            terminal_status,
            artifact,
            error,
            validate=should_validate,
        )
        builds_by_scenario[scenario] = build

    pending_scenario = "01-pending preview"
    pending_body = build_request(pending_scenario, "linux", "qa", "1.0.0", args.namespace, overrides)
    builds_by_scenario[pending_scenario] = ensure_build(args.api_url, existing, args.namespace, pending_scenario, pending_body)

    queued_scenario = "02-queued linux"
    queued_body = build_request(queued_scenario, "linux", "qa", "1.0.0", args.namespace, overrides)
    queued_build = ensure_build(args.api_url, existing, args.namespace, queued_scenario, queued_body)
    builds_by_scenario[queued_scenario] = approve_build(args.api_url, queued_build)

    scenario_order = [
        "01-pending preview",
        "02-queued linux",
        "03-succeeded preview",
        "04-validated release-ready",
        "05-release blocked latest",
        "06-windows preview succeeded",
        "07-failed retryable",
    ]
    builds = [builds_by_scenario[scenario] for scenario in scenario_order]

    progress.step("Verify release gates")
    gate_results: list[CheckResult] = []
    for build in builds:
        check = request_json(args.api_url, "GET", f"/api/builds/{build['id']}/release-check")
        scenario = build["variables"]["seedScenario"]
        expected_status = "warn" if scenario == "05-release blocked latest" else "ok"
        if scenario == "04-validated release-ready" and not check["releaseReady"]:
            expected_status = "fail"
        if scenario == "05-release blocked latest" and check["releaseReady"]:
            expected_status = "fail"
        gate_results.append(
            CheckResult(
                scenario,
                expected_status,
                f"status={build['status']} preview={check['previewReady']} "
                f"release={check['releaseReady']} blocking={check['blockingCount']}",
            )
        )

    progress.step("Print confirmation lights")
    print_checks(gate_results)
    print(f"\nSeed namespace: {args.namespace}")
    print(f"API: {args.api_url}")
    return 0 if all(item.status != "fail" for item in gate_results) else 1


def check(args: argparse.Namespace) -> int:
    results = run_checks(args.api_url, args.namespace)
    print_checks(results)
    return 0 if all(item.status != "fail" for item in results) else 1


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Packaging Platform seed CLI.")
    parser.add_argument("--api-url", default=DEFAULT_API_URL)
    parser.add_argument("--namespace", default=DEFAULT_NAMESPACE)

    subparsers = parser.add_subparsers(dest="command")
    subparsers.add_parser("seed", help="create flow-validation seed records")
    subparsers.add_parser("check", help="run preflight checks only")
    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    if args.command in {None, "seed"}:
        return seed(args)
    if args.command == "check":
        return check(args)
    parser.error(f"unknown command: {args.command}")
    return 2


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(f"{light('fail')} {exc}", file=sys.stderr)
        sys.exit(1)

#!/usr/bin/env python3
"""Auth-enabled Runner E2E gate without handling runner credentials."""
from __future__ import annotations

import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Protocol

SCHEMA = "packaging.runner_e2e.report/v1"
FORBIDDEN_ENV = {
    "AUTH_RUNNER_TOKEN",
    "RUNNER_EXECUTION_HMAC_KEY",
    "X_RUNNER_EXECUTION",
    "RUNNER_EXECUTION_CAPABILITY",
}
SECRET_PATTERN = re.compile(r"(?i)(authorization|token|capability|secret|password|api[_-]?key)\s*[:=]\s*[^\s,|]+")
HEX_SHA256 = re.compile(r"^[0-9a-f]{64}$", re.IGNORECASE)


@dataclass
class CheckResult:
    name: str
    status: str
    detail: str


class Adapter(Protocol):
    def health(self) -> dict[str, Any]: ...
    def evidence(self, build_id: str) -> dict[str, Any]: ...
    def nomad_job(self, job_id: str) -> dict[str, Any]: ...
    def job_summary(self, job_id: str) -> dict[str, Any]: ...
    def allocations(self, job_id: str) -> list[dict[str, Any]]: ...


class HttpAdapter:
    def __init__(self, api_url: str, operator_token: str, nomad_url: str, nomad_token: str):
        self.api_url = api_url.rstrip("/")
        self.operator_token = operator_token
        self.nomad_url = nomad_url.rstrip("/")
        self.nomad_token = nomad_token

    @staticmethod
    def get_json(url: str, headers: dict[str, str], label: str) -> Any:
        request = urllib.request.Request(url, headers=headers)
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                payload = response.read().decode("utf-8")
                return json.loads(payload) if payload else {}
        except urllib.error.HTTPError as exc:
            raise RuntimeError(f"{label} failed: HTTP {exc.code}") from exc
        except urllib.error.URLError as exc:
            raise RuntimeError(f"{label} failed: unreachable") from exc

    def api_get(self, path: str) -> dict[str, Any]:
        payload = self.get_json(
            self.api_url + path,
            {"Authorization": f"Bearer {self.operator_token}", "Accept": "application/json"},
            f"Conductor GET {path}",
        )
        if not isinstance(payload, dict):
            raise RuntimeError(f"Conductor GET {path} returned an unexpected response")
        return payload

    def health(self) -> dict[str, Any]:
        return self.api_get("/api/health")

    def evidence(self, build_id: str) -> dict[str, Any]:
        return self.api_get(f"/api/builds/{urllib.parse.quote(build_id, safe='')}/runner-e2e-evidence")

    def nomad_get(self, path: str) -> Any:
        headers = {"Accept": "application/json"}
        if self.nomad_token:
            headers["X-Nomad-Token"] = self.nomad_token
        return self.get_json(self.nomad_url + path, headers, f"Nomad GET {path}")

    def nomad_job(self, job_id: str) -> dict[str, Any]:
        payload = self.nomad_get(f"/v1/job/{urllib.parse.quote(job_id, safe='')}")
        if not isinstance(payload, dict):
            raise RuntimeError("Nomad job query returned an unexpected response")
        return payload

    def job_summary(self, job_id: str) -> dict[str, Any]:
        payload = self.nomad_get(f"/v1/job/{urllib.parse.quote(job_id, safe='')}/summary")
        if not isinstance(payload, dict):
            raise RuntimeError("Nomad job summary query returned an unexpected response")
        return payload

    def allocations(self, job_id: str) -> list[dict[str, Any]]:
        payload = self.nomad_get(f"/v1/job/{urllib.parse.quote(job_id, safe='')}/allocations")
        if not isinstance(payload, list) or not all(isinstance(item, dict) for item in payload):
            raise RuntimeError("Nomad allocations query returned an unexpected response")
        return payload


class FixtureAdapter:
    def health(self) -> dict[str, Any]:
        return {"status": "ok"}

    def evidence(self, build_id: str) -> dict[str, Any]:
        return {
            "buildId": build_id,
            "status": "succeeded",
            "authEnabled": True,
            "attempt": 1,
            "target": "linux",
            "runnerId": "nomad-fixture-a1",
            "nomadJobId": "pkg-fixture-a1",
            "dispatchStartedAt": "2026-01-01T00:00:00Z",
            "artifactUploadId": "final-fixture",
            "checksumUploadId": "checksum-fixture",
            "artifactSha256": "a" * 64,
            "artifactVerifiedAt": "2026-01-01T00:00:00Z",
            "verificationStatus": "verified",
            "finalBuildId": build_id,
            "finalAttempt": 1,
            "finalRunnerId": "nomad-fixture-a1",
            "finalKind": "final",
            "checksumBuildId": build_id,
            "checksumAttempt": 1,
            "checksumRunnerId": "nomad-fixture-a1",
            "checksumKind": "checksum",
            "checksumParentUploadId": "final-fixture",
        }

    def nomad_job(self, job_id: str) -> dict[str, Any]:
        return {"ID": job_id, "Type": "batch", "Status": "dead", "Meta": {"packaging.build_id": "build-1", "packaging.attempt": "1", "packaging.runner_id": "nomad-fixture-a1"}}

    def job_summary(self, job_id: str) -> dict[str, Any]:
        return {"JobID": job_id, "Summary": {"builder": {"Complete": 1, "Failed": 0, "Lost": 0}}}

    def allocations(self, job_id: str) -> list[dict[str, Any]]:
        return [{"JobID": job_id, "TaskGroup": "builder", "DesiredStatus": "run", "ClientStatus": "complete", "TaskStates": {"package": {"State": "dead", "Failed": False}}}]


def redact(value: str) -> str:
    return SECRET_PATTERN.sub(lambda match: f"{match.group(1)}=<redacted>", value)


def exactly(value: Any, expected: Any) -> bool:
    return value == expected and value not in {"", None}


def evidence_binding_ok(evidence: dict[str, Any], build_id: str) -> bool:
    attempt = evidence.get("attempt")
    runner_id = evidence.get("runnerId")
    final_id = evidence.get("artifactUploadId")
    return (
        exactly(evidence.get("buildId"), build_id)
        and isinstance(attempt, int)
        and attempt > 0
        and isinstance(evidence.get("target"), str)
        and bool(evidence.get("target"))
        and isinstance(runner_id, str)
        and bool(runner_id)
        and isinstance(final_id, str)
        and bool(final_id)
        and bool(evidence.get("dispatchStartedAt"))
        and exactly(evidence.get("finalBuildId"), build_id)
        and evidence.get("finalAttempt") == attempt
        and evidence.get("finalRunnerId") == runner_id
        and evidence.get("finalKind") == "final"
        and exactly(evidence.get("checksumBuildId"), build_id)
        and evidence.get("checksumAttempt") == attempt
        and evidence.get("checksumRunnerId") == runner_id
        and evidence.get("checksumKind") == "checksum"
        and evidence.get("checksumParentUploadId") == final_id
    )


def allocation_complete(allocation: dict[str, Any], job_id: str) -> bool:
    if allocation.get("JobID") != job_id or allocation.get("TaskGroup") != "builder":
        return False
    if str(allocation.get("DesiredStatus", "")).lower() != "run" or str(allocation.get("ClientStatus", "")).lower() != "complete":
        return False
    task_states = allocation.get("TaskStates")
    if not isinstance(task_states, dict):
        return False
    package = task_states.get("package")
    return isinstance(package, dict) and not bool(package.get("Failed")) and str(package.get("State", "")).lower() in {"dead", "complete"}


def checks(adapter: Adapter, build_id: str, nomad_job_id: str, live: bool) -> list[CheckResult]:
    results: list[CheckResult] = []
    health = adapter.health()
    results.append(CheckResult("Conductor reachability", "ok" if health.get("status") == "ok" else "fail", f"status={health.get('status', 'unknown')}"))
    evidence = adapter.evidence(build_id)

    authenticated = exactly(evidence.get("buildId"), build_id)
    results.append(
        CheckResult(
            "Operator-authenticated evidence read",
            "ok" if authenticated else "fail",
            f"buildId={evidence.get('buildId', '')}",
        )
    )
    completion = evidence.get("status") == "succeeded" and evidence.get("authEnabled") is True
    results.append(CheckResult("Auth-enabled build completion", "ok" if completion else "fail", f"status={evidence.get('status', '')} authEnabled={str(evidence.get('authEnabled') is True).lower()}"))
    nomad_bound = live is False or exactly(evidence.get("nomadJobId"), nomad_job_id)
    results.append(
        CheckResult(
            "Conductor-to-Nomad job binding",
            "ok" if nomad_bound else "fail",
            f"nomadJobId={evidence.get('nomadJobId', '')}" if nomad_bound else "Nomad job ID does not match protected Conductor evidence",
        )
    )
    binding = evidence_binding_ok(evidence, build_id)
    results.append(
        CheckResult(
            "Exact attempt, runner, and output binding",
            "ok" if binding else "fail",
            f"attempt={evidence.get('attempt', '')} target={evidence.get('target', '')} runnerId={evidence.get('runnerId', '')}",
        )
    )
    output_ok = (
        evidence.get("verificationStatus") == "verified"
        and isinstance(evidence.get("artifactSha256"), str)
        and bool(HEX_SHA256.fullmatch(evidence["artifactSha256"]))
        and bool(evidence.get("checksumUploadId"))
        and bool(evidence.get("artifactVerifiedAt"))
    )
    results.append(
        CheckResult(
            "Verified final artifact and checksum",
            "ok" if output_ok else "fail",
            "server final/checksum binding verified" if output_ok else "verified server-side final/checksum evidence is missing",
        )
    )
    results.append(CheckResult("Execution-secret boundary", "ok", "operator token used only for protected control-plane evidence; runner bearer/capability were neither accepted nor reported"))

    if live:
        job = adapter.nomad_job(nomad_job_id)
        metadata = job.get("Meta") if isinstance(job.get("Meta"), dict) else {}
        job_matches = (
            exactly(job.get("ID"), nomad_job_id)
            and str(job.get("Type", "")).lower() == "batch"
            and str(job.get("Status", "")).lower() in {"dead", "complete"}
            and metadata.get("packaging.build_id") == build_id
            and metadata.get("packaging.attempt") == str(evidence.get("attempt"))
            and metadata.get("packaging.runner_id") == evidence.get("runnerId")
        )
        results.append(CheckResult("Nomad batch job evidence", "ok" if job_matches else "fail", f"jobId={job.get('ID', '')} type={job.get('Type', '')} status={job.get('Status', '')}"))
        summary = adapter.job_summary(nomad_job_id)
        task_group = summary.get("Summary", {}).get("builder", {}) if isinstance(summary.get("Summary"), dict) else {}
        summary_complete = summary.get("JobID") == nomad_job_id and task_group.get("Complete", 0) >= 1 and task_group.get("Failed", 0) == 0 and task_group.get("Lost", 0) == 0
        results.append(CheckResult("Nomad batch summary", "ok" if summary_complete else "fail", "builder complete>=1 failed=0 lost=0" if summary_complete else "Nomad builder summary has failed or lost allocations"))
        allocations = adapter.allocations(nomad_job_id)
        complete = len(allocations) == 1 and allocation_complete(allocations[0], nomad_job_id)
        results.append(CheckResult("Nomad allocation completion", "ok" if complete else "fail", f"activeAllocations={len(allocations)} exactSuccessfulAllocation={str(complete).lower()}"))
    else:
        results.append(CheckResult("Deployment boundary", "warn", "fixture adapter validates protocol contract only; it is not a Nomad deployment"))
    return results


def report(results: list[CheckResult], mode: str) -> dict[str, Any]:
    sanitized = [{**asdict(item), "detail": redact(item.detail)} for item in results]
    status = "fail" if any(item.status == "fail" for item in results) else ("warn" if any(item.status == "warn" for item in results) else "ok")
    return {"schema": SCHEMA, "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"), "mode": mode, "status": status, "gates": sanitized}


def parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Run auth-enabled Runner E2E without runner credentials.")
    parser.add_argument("check", nargs="?")
    parser.add_argument("--fixture", action="store_true", help="use local fake protocol adapter (default unless --live)")
    parser.add_argument("--live", action="store_true", help="verify an actual Conductor build and Nomad allocation")
    parser.add_argument("--api-url")
    parser.add_argument("--build-id", default="runner-e2e-fixture")
    parser.add_argument("--nomad-job-id", help="required deployed Nomad job ID in live mode")
    parser.add_argument("--nomad-url", help="Nomad API URL; defaults to NOMAD_ADDR")
    parser.add_argument("--report-file")
    parser.add_argument("--json", action="store_true")
    return parser


def assert_runner_secret_boundary() -> None:
    if any(os.environ.get(key) for key in FORBIDDEN_ENV):
        raise ValueError("runner credential environment is present; run this gate outside the Conductor/Runner secret environment")


def main() -> int:
    args = parser().parse_args()
    if args.live and args.fixture:
        raise ValueError("--live and --fixture are mutually exclusive")
    assert_runner_secret_boundary()
    if args.live:
        operator_token = os.environ.get("PACKAGING_OPERATOR_TOKEN", "")
        nomad_url = args.nomad_url or os.environ.get("NOMAD_ADDR", "")
        nomad_token = os.environ.get("NOMAD_TOKEN", "")
        if not args.api_url or not operator_token or not args.build_id or not args.nomad_job_id or not nomad_url or not nomad_token:
            raise ValueError("--live requires --api-url, PACKAGING_OPERATOR_TOKEN, --build-id, --nomad-job-id, --nomad-url (or NOMAD_ADDR), and NOMAD_TOKEN")
        adapter: Adapter = HttpAdapter(args.api_url, operator_token, nomad_url, nomad_token)
    else:
        adapter = FixtureAdapter()
    results = checks(adapter, args.build_id, args.nomad_job_id or "pkg-fixture-a1", args.live)
    payload = report(results, "live" if args.live else "fixture")
    if args.report_file:
        Path(args.report_file).write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")
    if args.json or args.report_file:
        print(json.dumps(payload, indent=2))
    else:
        for item in payload["gates"]:
            print(f"[{item['status'].upper()}] {item['name']}: {item['detail']}")
    return 1 if payload["status"] == "fail" else 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(f"[FAIL] {redact(str(exc))}", file=sys.stderr)
        sys.exit(1)

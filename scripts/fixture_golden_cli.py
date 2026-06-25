#!/usr/bin/env python3
"""Validate deterministic runner fixture artifacts."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import re
import subprocess
import sys
import tarfile
import tempfile
from dataclasses import dataclass
from typing import Any


LINUX_MARKER = "PACKAGING_PLATFORM_LINUX_PACKAGE_FIXTURE_V1"
WINDOWS_MARKER = "PACKAGING_PLATFORM_WINDOWS_SETUP_FIXTURE_V1"
WINDOWS_SIGNING_MARKER = "PACKAGING_PLATFORM_WINDOWS_SIGNING_FIXTURE_V1"


@dataclass
class CheckResult:
    name: str
    status: str
    detail: str


def repo_root() -> pathlib.Path:
    return pathlib.Path(__file__).resolve().parents[1]


def light(status: str) -> str:
    if status == "ok":
        return "[OK]"
    if status == "warn":
        return "[WARN]"
    return "[FAIL]"


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def sha256_bytes(payload: bytes) -> str:
    return hashlib.sha256(payload).hexdigest()


def write_json(path: pathlib.Path, payload: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, sort_keys=True, indent=2) + "\n", encoding="utf-8")


def create_fixture_workspace(workspace: pathlib.Path, target: str) -> None:
    context = workspace / "context"
    rendered = context / "rendered"
    components_dir = context / "components"
    rendered.mkdir(parents=True, exist_ok=True)
    components_dir.mkdir(parents=True, exist_ok=True)
    build_id = f"fixture-build-{target}"
    build_payload = {
        "buildId": build_id,
        "productId": "lsap",
        "target": target,
        "version": "1.0.0",
        "profile": "qa",
        "customer": "fixture-customer",
        "releaseChannel": "preview",
    }
    write_json(context / "build-info.json", build_payload)
    write_json(context / "build-lock.json", {**build_payload, "config": {"installDir": "/opt/lsap-fixture"}})
    write_json(context / "final-config.json", {"installDir": "/opt/lsap-fixture", "httpPort": 8080})
    components = [
        {"id": "engine", "uri": "local-upload://engine.bin", "sha256": "1" * 64},
        {"id": "ui", "uri": "local-upload://ui.bin", "sha256": "2" * 64},
    ]
    workloads = [{"id": "api", "runtime": "docker", "enabled": True}]
    write_json(rendered / "components.json", components)
    write_json(rendered / "workloads.json", workloads)
    (components_dir / "engine.bin").write_bytes(b"engine fixture component\n")
    (components_dir / "ui.bin").write_bytes(b"ui fixture component\n")


def run_command(command: list[str], cwd: pathlib.Path, env: dict[str, str]) -> None:
    result = subprocess.run(
        command,
        cwd=cwd,
        env={**os.environ, **env},
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=60,
    )
    if result.returncode != 0:
        output = "\n".join(part for part in [result.stdout, result.stderr] if part).strip()
        raise RuntimeError(output or f"{command[0]} exited {result.returncode}")


def run_linux_fixture(workspace: pathlib.Path, output: pathlib.Path) -> None:
    run_command(
        [sys.executable, str(repo_root() / "runner" / "fixtures" / "linux_package_packager.py")],
        workspace,
        {"OUTPUT": str(output), "CONTEXT_DIR": str(workspace / "context")},
    )


def run_windows_fixture(workspace: pathlib.Path, output: pathlib.Path) -> None:
    run_command(
        [sys.executable, str(repo_root() / "runner" / "fixtures" / "windows_setup_packager.py")],
        workspace,
        {"OUTPUT": str(output)},
    )
    run_command(
        [sys.executable, str(repo_root() / "runner" / "fixtures" / "windows_signing_fixture.py")],
        workspace,
        {
            "OUTPUT": str(output),
            "BUILD_ID": "fixture-build-windows",
            "PRODUCT_ID": "lsap",
            "TARGET": "windows",
            "VERSION": "1.0.0",
        },
    )


def read_linux_metadata(artifact: pathlib.Path) -> dict[str, Any]:
    with tarfile.open(artifact, "r:gz") as archive:
        marker = archive.extractfile("FIXTURE_MARKER.txt").read().decode("ascii").strip()
        if marker != LINUX_MARKER:
            raise RuntimeError(f"unexpected linux fixture marker: {marker}")
        return json.loads(archive.extractfile("fixture-metadata.json").read().decode("utf-8"))


def payload_sha256_from_linux_metadata(artifact: pathlib.Path) -> str:
    with tarfile.open(artifact, "r:gz") as archive:
        payload = archive.extractfile("fixture-metadata.json").read()
    return sha256_bytes(payload)


def json_block_after_marker(payload: bytes, marker: str, stop_at_digest: bool) -> dict[str, Any]:
    lines = payload.splitlines()
    try:
        marker_index = lines.index(marker.encode("ascii"))
    except ValueError as exc:
        raise RuntimeError(f"marker not found: {marker}") from exc
    json_lines: list[bytes] = []
    digest_pattern = re.compile(rb"^[0-9a-f]{64}$")
    for line in lines[marker_index + 1 :]:
        if stop_at_digest and digest_pattern.match(line):
            break
        if line == WINDOWS_SIGNING_MARKER.encode("ascii"):
            break
        if line:
            json_lines.append(line)
    return json.loads(b"\n".join(json_lines).decode("utf-8"))


def read_windows_setup_metadata(artifact: pathlib.Path) -> dict[str, Any]:
    return json_block_after_marker(artifact.read_bytes(), WINDOWS_MARKER, stop_at_digest=True)


def read_windows_signing_metadata(artifact: pathlib.Path) -> dict[str, Any]:
    return json_block_after_marker(artifact.read_bytes(), WINDOWS_SIGNING_MARKER, stop_at_digest=False)


def linux_checks(root: pathlib.Path) -> list[CheckResult]:
    first_workspace = root / "linux-first"
    second_workspace = root / "linux-second"
    create_fixture_workspace(first_workspace, "linux")
    create_fixture_workspace(second_workspace, "linux")
    first = first_workspace / "package.tar.gz"
    second = second_workspace / "package.tar.gz"
    run_linux_fixture(first_workspace, first)
    run_linux_fixture(second_workspace, second)
    metadata = read_linux_metadata(first)
    results = [
        CheckResult(
            "Linux fixture determinism",
            "ok" if sha256(first) == sha256(second) else "fail",
            sha256(first),
        ),
        CheckResult(
            "Linux fixture metadata",
            "ok"
            if metadata.get("marker") == LINUX_MARKER
            and metadata.get("buildId") == "fixture-build-linux"
            and metadata.get("componentCount") == 2
            and metadata.get("workloadCount") == 1
            else "fail",
            f"buildId={metadata.get('buildId')} components={metadata.get('componentCount')} workloads={metadata.get('workloadCount')}",
        ),
    ]
    return results


def windows_checks(root: pathlib.Path) -> list[CheckResult]:
    first_workspace = root / "windows-first"
    second_workspace = root / "windows-second"
    create_fixture_workspace(first_workspace, "windows")
    create_fixture_workspace(second_workspace, "windows")
    first = first_workspace / "setup.exe"
    second = second_workspace / "setup.exe"
    run_windows_fixture(first_workspace, first)
    run_windows_fixture(second_workspace, second)
    setup_metadata = read_windows_setup_metadata(first)
    signing_metadata = read_windows_signing_metadata(first)
    return [
        CheckResult(
            "Windows fixture determinism",
            "ok" if sha256(first) == sha256(second) else "fail",
            sha256(first),
        ),
        CheckResult(
            "Windows fixture metadata",
            "ok"
            if setup_metadata.get("marker") == WINDOWS_MARKER
            and setup_metadata.get("buildId") == "fixture-build-windows"
            and setup_metadata.get("componentCount") == 2
            else "fail",
            f"buildId={setup_metadata.get('buildId')} components={setup_metadata.get('componentCount')}",
        ),
        CheckResult(
            "Windows fixture signing",
            "ok"
            if signing_metadata.get("marker") == WINDOWS_SIGNING_MARKER
            and signing_metadata.get("buildId") == "fixture-build-windows"
            and signing_metadata.get("signatureKind") == "deterministic-fixture"
            else "fail",
            f"buildId={signing_metadata.get('buildId')} kind={signing_metadata.get('signatureKind')}",
        ),
    ]


def check(root: pathlib.Path | None = None) -> list[CheckResult]:
    if root is not None:
        root.mkdir(parents=True, exist_ok=True)
        return [*linux_checks(root), *windows_checks(root)]
    with tempfile.TemporaryDirectory(prefix="packaging-fixture-golden-") as temp_dir:
        return [*linux_checks(pathlib.Path(temp_dir)), *windows_checks(pathlib.Path(temp_dir))]


def result_exit_code(results: list[CheckResult]) -> int:
    return 1 if any(result.status == "fail" for result in results) else 0


def print_results(results: list[CheckResult]) -> None:
    print("Confirmation lights:")
    for result in results:
        print(f"  {light(result.status)} {result.name}: {result.detail}")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Validate deterministic runner fixture artifacts.")
    parser.add_argument("command", choices=["check"], nargs="?", default="check")
    parser.add_argument("--work-dir", type=pathlib.Path, help="optional scratch directory for generated fixture artifacts")
    return parser


def main() -> int:
    args = build_parser().parse_args()
    results = check(args.work_dir)
    print_results(results)
    return result_exit_code(results)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(f"{light('fail')} {exc}", file=sys.stderr)
        sys.exit(1)

#!/usr/bin/env python3
"""Create a deterministic Linux package fixture inside the Dagger workspace."""

from __future__ import annotations

import hashlib
import gzip
import io
import json
import os
import pathlib
import tarfile
from typing import Any


MARKER = "PACKAGING_PLATFORM_LINUX_PACKAGE_FIXTURE_V1"
SKIPPED_CONTEXT_FILES = {"context/build-info.json"}
SKIPPED_CONTEXT_PREFIXES = {"context/rendered/runner-cache-"}


def read_json(path: pathlib.Path) -> Any:
    if not path.exists():
        return {} if path.suffix == ".json" else None
    return json.loads(path.read_text(encoding="utf-8"))


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def context_checksums(context: pathlib.Path) -> list[dict[str, str]]:
    checksums: list[dict[str, str]] = []
    for path in sorted(item for item in context.rglob("*") if item.is_file()):
        relative = path.relative_to(context.parent).as_posix()
        if relative in SKIPPED_CONTEXT_FILES:
            continue
        if any(relative.startswith(prefix) for prefix in SKIPPED_CONTEXT_PREFIXES):
            continue
        checksums.append({"path": relative, "sha256": sha256(path)})
    return checksums


def fixture_metadata(context: pathlib.Path) -> dict[str, Any]:
    build_info = read_json(context / "build-info.json")
    build_lock = read_json(context / "build-lock.json")
    final_config = read_json(context / "final-config.json")
    components = read_json(context / "rendered" / "components.json")
    workloads = read_json(context / "rendered" / "workloads.json")

    metadata = {
        "marker": MARKER,
        "buildId": build_info.get("buildId") or build_lock.get("buildId") or "",
        "productId": build_info.get("productId") or build_lock.get("productId") or "",
        "target": build_info.get("target") or build_lock.get("target") or "linux",
        "version": build_info.get("version") or build_lock.get("version") or "",
        "profile": build_info.get("profile") or build_lock.get("profile") or "",
        "customer": build_info.get("customer") or build_lock.get("customer") or "",
        "releaseChannel": build_info.get("releaseChannel") or build_lock.get("releaseChannel") or "",
        "componentCount": len(components) if isinstance(components, list) else 0,
        "workloadCount": len(workloads) if isinstance(workloads, list) else 0,
        "installDir": final_config.get("installDir", "") if isinstance(final_config, dict) else "",
        "contextChecksums": context_checksums(context),
    }
    metadata_bytes = json.dumps(metadata, sort_keys=True, separators=(",", ":")).encode("utf-8")
    metadata["payloadSha256"] = hashlib.sha256(metadata_bytes).hexdigest()
    return metadata


def add_bytes(archive: tarfile.TarFile, name: str, content: bytes) -> None:
    info = tarfile.TarInfo(name)
    info.size = len(content)
    info.mode = 0o644
    info.mtime = 0
    archive.addfile(info, fileobj=io.BytesIO(content))


def write_package(output: pathlib.Path, metadata: dict[str, Any]) -> None:
    output.parent.mkdir(parents=True, exist_ok=True)
    payload = json.dumps(metadata, sort_keys=True, indent=2).encode("utf-8") + b"\n"
    marker = (MARKER + "\n").encode("ascii")
    with output.open("wb") as handle:
        with gzip.GzipFile(filename="", mode="wb", fileobj=handle, mtime=0) as gzip_file:
            with tarfile.open(fileobj=gzip_file, mode="w", format=tarfile.PAX_FORMAT) as archive:
                add_bytes(archive, "FIXTURE_MARKER.txt", marker)
                add_bytes(archive, "fixture-metadata.json", payload)
                add_bytes(archive, "payload.sha256", hashlib.sha256(payload).hexdigest().encode("ascii") + b"\n")


def main() -> int:
    output_value = os.environ.get("OUTPUT", "")
    context_value = os.environ.get("CONTEXT_DIR", "/workspace/context")
    if not output_value:
        raise RuntimeError("OUTPUT is required")
    context = pathlib.Path(context_value)
    if not context.exists():
        raise RuntimeError(f"context directory does not exist: {context}")
    write_package(pathlib.Path(output_value), fixture_metadata(context))
    print(f"created linux package fixture: {output_value}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

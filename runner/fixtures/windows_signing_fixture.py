#!/usr/bin/env python3
"""Append deterministic signing evidence to a Windows setup fixture."""

from __future__ import annotations

import hashlib
import json
import os
import pathlib
import sys
from typing import Any


MARKER = "PACKAGING_PLATFORM_WINDOWS_SIGNING_FIXTURE_V1"


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def signing_metadata(output: pathlib.Path, payload: bytes) -> dict[str, Any]:
    return {
        "marker": MARKER,
        "buildId": os.environ.get("BUILD_ID", ""),
        "productId": os.environ.get("PRODUCT_ID", ""),
        "target": os.environ.get("TARGET", "windows"),
        "version": os.environ.get("VERSION", ""),
        "artifactName": output.name,
        "artifactSha256BeforeSigning": sha256_bytes(payload),
        "signatureKind": "deterministic-fixture",
    }


def append_signature(output: pathlib.Path) -> None:
    payload = output.read_bytes()
    metadata = signing_metadata(output, payload)
    metadata_bytes = json.dumps(metadata, sort_keys=True, separators=(",", ":")).encode("utf-8")
    metadata["signatureSha256"] = sha256_bytes(metadata_bytes)
    signature_block = json.dumps(metadata, sort_keys=True, indent=2).encode("utf-8")
    output.write_bytes(
        b"\r\n".join(
            [
                payload.rstrip(b"\r\n"),
                b"",
                MARKER.encode("ascii"),
                signature_block,
                b"",
            ]
        )
    )


def main() -> int:
    output_value = os.environ.get("OUTPUT") or os.environ.get("ARTIFACT", "")
    if not output_value:
        print("OUTPUT or ARTIFACT is required", file=sys.stderr)
        return 2
    output = pathlib.Path(output_value)
    if not output.exists():
        print(f"artifact does not exist: {output}", file=sys.stderr)
        return 2
    append_signature(output)
    print(f"appended windows signing fixture: {output}")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(str(exc), file=sys.stderr)
        sys.exit(1)

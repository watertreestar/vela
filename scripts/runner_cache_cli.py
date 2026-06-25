#!/usr/bin/env python3
"""Inspect and clean Packaging runner cache directories."""

from __future__ import annotations

import argparse
import json
import os
import shutil
import sys
from dataclasses import asdict, dataclass
from pathlib import Path


@dataclass
class CacheEntry:
    name: str
    path: str
    configured: bool
    exists: bool
    files: int = 0
    directories: int = 0
    bytes: int = 0
    truncated: bool = False
    error: str = ""


def default_paths() -> list[tuple[str, str]]:
    return [
        ("bun", os.environ.get("BUN_INSTALL_CACHE_DIR", "")),
        ("xdg", os.environ.get("XDG_CACHE_HOME", "")),
        ("dagger", os.environ.get("DAGGER_CACHE_DIR", "")),
        ("artifacts", os.environ.get("ARTIFACT_ROOT", "")),
    ]


def collect_entry(name: str, raw_path: str, limit: int) -> CacheEntry:
    entry = CacheEntry(
        name=name,
        path=raw_path,
        configured=bool(raw_path),
        exists=False,
    )
    if not raw_path:
        return entry

    root = Path(raw_path)
    if not root.exists():
        return entry
    entry.exists = True
    try:
        if root.is_file():
            entry.files = 1
            entry.bytes = root.stat().st_size
            return entry
        for current, dirs, files in os.walk(root):
            entry.directories += len(dirs)
            for file_name in files:
                file_path = Path(current) / file_name
                try:
                    entry.bytes += file_path.stat().st_size
                except OSError as exc:
                    entry.error = str(exc)
                entry.files += 1
                if entry.files >= limit:
                    entry.truncated = True
                    return entry
    except OSError as exc:
        entry.error = str(exc)
    return entry


def collect(paths: list[tuple[str, str]], limit: int) -> list[CacheEntry]:
    return [collect_entry(name, raw_path, limit) for name, raw_path in paths]


def format_bytes(value: int) -> str:
    if value < 1024:
        return f"{value} B"
    if value < 1024 * 1024:
        return f"{value / 1024:.1f} KB"
    return f"{value / 1024 / 1024:.1f} MB"


def print_progress(message: str) -> None:
    print(message)


def print_confirmation_lights(entries: list[CacheEntry]) -> None:
    print("\nConfirmation lights:")
    for entry in entries:
        if not entry.configured:
            print(f"  [WARN] {entry.name}: not configured")
        elif entry.exists:
            suffix = " (truncated)" if entry.truncated else ""
            print(
                f"  [OK] {entry.name}: {format_bytes(entry.bytes)}, "
                f"{entry.files} file(s), {entry.directories} dir(s){suffix}"
            )
        else:
            print(f"  [WARN] {entry.name}: path does not exist: {entry.path}")


def safe_cleanup_path(path_value: str) -> Path:
    path = Path(path_value).expanduser().resolve()
    lowered = str(path).lower()
    if "cache" not in lowered and "packaging" not in lowered:
        raise ValueError(f"refuse to clean non-cache-looking path: {path}")
    return path


def cleanup_entries(entries: list[CacheEntry], apply: bool) -> list[str]:
    cleaned: list[str] = []
    for entry in entries:
        if not entry.configured or not entry.exists:
            continue
        root = safe_cleanup_path(entry.path)
        if not root.is_dir():
            continue
        for child in root.iterdir():
            cleaned.append(str(child))
            if not apply:
                continue
            if child.is_dir():
                shutil.rmtree(child)
            else:
                child.unlink()
    return cleaned


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Inspect or clean runner cache directories.")
    subparsers = parser.add_subparsers(dest="command", required=True)
    add_common_args(subparsers.add_parser("report", help="print runner cache status"))
    cleanup = subparsers.add_parser("cleanup", help="clean cache directory contents")
    add_common_args(cleanup)
    cleanup.add_argument("--apply", action="store_true", help="actually delete files; default is dry-run")
    return parser


def add_common_args(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--limit", type=int, default=5000, help="maximum files to scan per cache path")
    parser.add_argument("--json", action="store_true", help="print machine-readable JSON")
    parser.add_argument(
        "--path",
        action="append",
        default=[],
        help="extra cache path in name=path format; can be repeated",
    )


def parse_extra_paths(values: list[str]) -> list[tuple[str, str]]:
    parsed: list[tuple[str, str]] = []
    for value in values:
        if "=" not in value:
            raise ValueError(f"--path must use name=path: {value}")
        name, raw_path = value.split("=", 1)
        parsed.append((name.strip(), raw_path.strip()))
    return parsed


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    if args.limit <= 0:
        parser.error("--limit must be greater than 0")

    try:
        paths = default_paths() + parse_extra_paths(args.path)
        print_progress("[01/02] Scan runner cache paths")
        entries = collect(paths, args.limit)

        cleaned: list[str] = []
        if args.command == "cleanup":
            print_progress("[02/02] Clean runner cache paths" + ("" if args.apply else " (dry-run)"))
            cleaned = cleanup_entries(entries, args.apply)
        else:
            print_progress("[02/02] Print runner cache report")

        payload = {"entries": [asdict(entry) for entry in entries], "cleaned": cleaned}
        if args.json:
            print(json.dumps(payload, indent=2))
        else:
            print_confirmation_lights(entries)
            if args.command == "cleanup":
                mode = "deleted" if args.apply else "would delete"
                print(f"\nCleanup {mode}: {len(cleaned)} item(s)")
        return 0
    except (OSError, ValueError) as exc:
        print(f"[FAIL] {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

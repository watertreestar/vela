#!/usr/bin/env python3
"""Validate GitLab Generic Package Registry artifact availability."""

from __future__ import annotations

import argparse
import hashlib
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass


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


def env_or(value: str | None, key: str, fallback: str = "") -> str:
    if value:
        return value
    return os.environ.get(key, fallback)


def has_wildcard(value: str) -> bool:
    return any(marker in value for marker in ("*", "?", "["))


def path_escape(value: str) -> str:
    return urllib.parse.quote(value.strip(), safe="")


def gitlab_project_path(project: str) -> str:
    project = project.strip()
    if "%2F" in project or "%2f" in project:
        return project
    return path_escape(project)


def escape_path_parts(parts: list[str]) -> str:
    return "/".join(path_escape(part) for part in parts)


def parse_gitlab_source(repository: str, path: str) -> tuple[str, str, str, list[str]]:
    repository = repository.strip()
    object_path = path.strip().strip("/").replace("\\", "/")
    if has_wildcard(object_path):
        raise ValueError(f"GitLab package resolver requires exact file path: {path}")
    parts = [part for part in object_path.split("/") if part]
    project = ""
    if repository.startswith("gitlab:"):
        project = repository.split(":", 1)[1].strip()
        if len(parts) < 3:
            raise ValueError(f"GitLab path requires package/version/file when repository contains project: {path}")
        package_name, package_version, file_parts = parts[0], parts[1], parts[2:]
    elif len(parts) >= 7 and parts[0] == "projects" and parts[2] == "packages" and parts[3] == "generic":
        project, package_name, package_version, file_parts = parts[1], parts[4], parts[5], parts[6:]
    elif len(parts) >= 6 and parts[1] == "packages" and parts[2] == "generic":
        project, package_name, package_version, file_parts = parts[0], parts[3], parts[4], parts[5:]
    elif len(parts) >= 4:
        project, package_name, package_version, file_parts = parts[0], parts[1], parts[2], parts[3:]
    else:
        raise ValueError(f"GitLab path requires project/package/version/file: {path}")
    if not project or not package_name or not package_version or not file_parts:
        raise ValueError(f"GitLab path requires project/package/version/file: {path}")
    return project, package_name, package_version, file_parts


def build_download_url(base_url: str, repository: str, path: str) -> str:
    project, package_name, package_version, file_parts = parse_gitlab_source(repository, path)
    return (
        base_url.rstrip("/")
        + "/api/v4/projects/"
        + gitlab_project_path(project)
        + "/packages/generic/"
        + path_escape(package_name)
        + "/"
        + path_escape(package_version)
        + "/"
        + escape_path_parts(file_parts)
    )


def validate_inputs(args: argparse.Namespace, base_url: str, repository: str, path: str, token: str) -> list[CheckResult]:
    results = [
        CheckResult("GitLab base URL configured", "ok" if base_url else "fail", base_url or "GITLAB_PACKAGE_BASE_URL missing"),
        CheckResult("GitLab repository configured", "ok" if repository else "fail", repository or "--repository missing"),
        CheckResult("GitLab package path configured", "ok" if path else "fail", path or "--path missing"),
    ]
    if args.production:
        results.append(CheckResult("GitLab base URL uses HTTPS", "ok" if base_url.startswith("https://") else "fail", base_url or "<missing>"))
    if args.require_token:
        results.append(CheckResult("GitLab token configured", "ok" if token else "fail", "PRIVATE-TOKEN configured" if token else "GITLAB_PACKAGE_TOKEN missing"))
    if path and has_wildcard(path):
        results.append(CheckResult("GitLab exact file path", "fail", "wildcards are not supported for GitLab package resolver"))
    elif path:
        results.append(CheckResult("GitLab exact file path", "ok", path))
    return results


def download_and_hash(url: str, token: str, timeout: int) -> tuple[int, int, str]:
    headers = {"Accept": "application/octet-stream"}
    if token:
        headers["PRIVATE-TOKEN"] = token
    request = urllib.request.Request(url, headers=headers)
    hash_value = hashlib.sha256()
    size = 0
    with urllib.request.urlopen(request, timeout=timeout) as response:
        while True:
            chunk = response.read(1024 * 1024)
            if not chunk:
                break
            size += len(chunk)
            hash_value.update(chunk)
        return response.status, size, hash_value.hexdigest()


def validate(args: argparse.Namespace) -> list[CheckResult]:
    base_url = env_or(args.base_url, "GITLAB_PACKAGE_BASE_URL")
    token = env_or(args.token, "GITLAB_PACKAGE_TOKEN")
    repository = args.repository or "gitlab"
    path = args.path or gitlab_path_from_parts(args)
    results = validate_inputs(args, base_url, repository, path, token)
    if any(result.status == "fail" for result in results):
        return results
    try:
        url = build_download_url(base_url, repository, path)
    except ValueError as exc:
        results.append(CheckResult("GitLab package URL", "fail", str(exc)))
        return results
    results.append(CheckResult("GitLab package URL", "ok", url))
    try:
        status, size, digest = download_and_hash(url, token, args.timeout)
    except urllib.error.HTTPError as exc:
        results.append(CheckResult("GitLab package download", "fail", f"HTTP {exc.code} from GitLab package registry"))
        return results
    except urllib.error.URLError as exc:
        results.append(CheckResult("GitLab package download", "fail", str(exc.reason)))
        return results
    results.append(CheckResult("GitLab package download", "ok" if 200 <= status < 300 else "fail", f"HTTP {status}, bytes={size}"))
    results.append(CheckResult("GitLab package sha256", "ok", digest))
    expected = (args.expected_sha256 or "").strip().lower()
    if expected:
        results.append(
            CheckResult(
                "GitLab expected sha256",
                "ok" if digest == expected else "fail",
                f"expected={expected} actual={digest}",
            )
        )
    elif args.production:
        results.append(CheckResult("GitLab expected sha256", "warn", "not supplied; live check computed sha256 but did not compare an expected value"))
    return results


def gitlab_path_from_parts(args: argparse.Namespace) -> str:
    if not (args.project and args.package and args.version and args.file):
        return ""
    return "/".join([args.project, args.package, args.version, args.file.strip("/")])


def result_exit_code(results: list[CheckResult]) -> int:
    return 1 if any(result.status == "fail" for result in results) else 0


def print_results(results: list[CheckResult]) -> None:
    print("Confirmation lights:")
    for result in results:
        print(f"  {light(result.status)} {result.name}: {result.detail}")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Validate GitLab Generic Package Registry package availability.")
    subparsers = parser.add_subparsers(dest="command")
    check = subparsers.add_parser("check", help="download a GitLab Generic Package Registry file and compute sha256")
    check.add_argument("--base-url", help="defaults to GITLAB_PACKAGE_BASE_URL")
    check.add_argument("--token", help="defaults to GITLAB_PACKAGE_TOKEN")
    check.add_argument("--repository", default="gitlab", help="gitlab or gitlab:<project>; defaults to gitlab")
    check.add_argument("--path", help="project/package/version/file, or package/version/file when repository=gitlab:<project>")
    check.add_argument("--project", help="project id or URL-encoded project path; used when --path is omitted")
    check.add_argument("--package", help="package name; used when --path is omitted")
    check.add_argument("--version", help="package version; used when --path is omitted")
    check.add_argument("--file", help="package file path; used when --path is omitted")
    check.add_argument("--expected-sha256", help="optional expected sha256 digest")
    check.add_argument("--require-token", action="store_true", help="fail if no GitLab package token is configured")
    check.add_argument("--production", action="store_true", help="strict production readiness: require HTTPS and exact file path")
    check.add_argument("--timeout", type=int, default=30)
    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args(["check"] if len(sys.argv) == 1 else None)
    if args.command != "check":
        parser.error(f"unknown command: {args.command}")
    results = validate(args)
    print_results(results)
    return result_exit_code(results)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(f"{light('fail')} {exc}", file=sys.stderr)
        sys.exit(1)

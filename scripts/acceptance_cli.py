#!/usr/bin/env python3
"""Run a local end-to-end acceptance flow for Packaging Platform."""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any


DEFAULT_API_URL = "http://127.0.0.1:8080"
DEFAULT_RUNNER_IMAGE = "packaging-runner:local"
DEFAULT_BASE_IMAGE = "alpine:latest"
DEFAULT_DAGGER_ENGINE_IMAGE = "registry.dagger.io/engine:v0.21.3"


@dataclass
class CheckResult:
    name: str
    status: str
    detail: str


@dataclass
class StartedApi:
    mode: str
    process: subprocess.Popen[str] | None = None
    container: str = ""
    binary: pathlib.Path | None = None


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


def utc_stamp() -> str:
    return datetime.now(timezone.utc).strftime("%Y%m%d%H%M%S")


def request_json(api_url: str, method: str, path: str, body: dict[str, Any] | None = None) -> Any:
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(f"{api_url}{path}", data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            payload = response.read()
            if not payload:
                return None
            return json.loads(payload.decode("utf-8"))
    except urllib.error.HTTPError as exc:
        payload = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{method} {path} failed: HTTP {exc.code}: {payload}") from exc
    except urllib.error.URLError as exc:
        raise RuntimeError(f"{method} {path} failed: {exc.reason}") from exc


def upload_file(api_url: str, filename: str, content: bytes) -> dict[str, Any]:
    boundary = f"----packaging-acceptance-{uuid.uuid4().hex}"
    body = b"".join(
        [
            f"--{boundary}\r\n".encode(),
            f'Content-Disposition: form-data; name="file"; filename="{filename}"\r\n'.encode(),
            b"Content-Type: application/octet-stream\r\n\r\n",
            content,
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
        with urllib.request.urlopen(request, timeout=60) as response:
            return json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        payload = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"upload {filename} failed: HTTP {exc.code}: {payload}") from exc


def run_command(
    command: list[str],
    cwd: pathlib.Path,
    env: dict[str, str] | None = None,
    timeout: int = 300,
    stream: bool = False,
) -> subprocess.CompletedProcess[str]:
    if stream:
        process = subprocess.Popen(
            command,
            cwd=cwd,
            env={**os.environ, **(env or {})},
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
        )
        output: list[str] = []
        assert process.stdout is not None
        started = time.monotonic()
        for line in process.stdout:
            print(line, end="")
            output.append(line)
            if time.monotonic() - started > timeout:
                process.kill()
                raise RuntimeError(f"command timed out: {' '.join(command)}")
        code = process.wait(timeout=5)
        return subprocess.CompletedProcess(command, code, "".join(output), "")

    return subprocess.run(
        command,
        cwd=cwd,
        env={**os.environ, **(env or {})},
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=timeout,
    )


def require_success(result: subprocess.CompletedProcess[str], description: str) -> None:
    if result.returncode == 0:
        return
    output = "\n".join(part for part in [result.stdout, result.stderr] if part)
    raise RuntimeError(f"{description} failed with exit code {result.returncode}\n{output}".strip())


def repo_root() -> pathlib.Path:
    return pathlib.Path(__file__).resolve().parents[1]


def print_checks(results: list[CheckResult]) -> None:
    print("\nConfirmation lights:")
    for result in results:
        print(f"  {light(result.status)} {result.name}: {result.detail}")


def api_is_healthy(api_url: str) -> bool:
    try:
        health = request_json(api_url, "GET", "/api/health")
        return isinstance(health, dict) and health.get("status") == "ok"
    except Exception:
        return False


def wait_for_api(api_url: str, timeout_seconds: int) -> None:
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        if api_is_healthy(api_url):
            return
        time.sleep(1)
    raise RuntimeError(f"API did not become healthy within {timeout_seconds}s: {api_url}/api/health")


def api_port(api_url: str) -> int:
    parsed = urllib.parse.urlparse(api_url)
    if parsed.port:
        return parsed.port
    return 443 if parsed.scheme == "https" else 80


def default_runner_api_url(api_url: str) -> str:
    parsed = urllib.parse.urlparse(api_url)
    port = parsed.port or (443 if parsed.scheme == "https" else 80)
    return urllib.parse.urlunparse((parsed.scheme, f"host.docker.internal:{port}", "", "", "", ""))


def docker_mount_path(path: pathlib.Path) -> str:
    resolved = path.resolve()
    if os.name != "nt":
        return str(resolved)
    drive = resolved.drive.rstrip(":").lower()
    rest = resolved.as_posix().split(":", 1)[1]
    return f"/mnt/{drive}{rest}"


def docker_network_exists(network: str, root: pathlib.Path) -> bool:
    return run_command(["docker", "network", "inspect", network], root, timeout=30).returncode == 0


def ensure_docker_network(args: argparse.Namespace, root: pathlib.Path) -> None:
    if not docker_network_exists(args.api_docker_network, root):
        require_success(
            run_command(["docker", "network", "create", args.api_docker_network], root, timeout=60),
            f"create docker network {args.api_docker_network}",
        )
    if args.postgres_container:
        inspect = run_command(["docker", "container", "inspect", args.postgres_container], root, timeout=30)
        require_success(inspect, f"inspect postgres container {args.postgres_container}")
        connect = run_command(
            ["docker", "network", "connect", args.api_docker_network, args.postgres_container],
            root,
            timeout=60,
        )
        if connect.returncode != 0 and "already exists" not in (connect.stderr + connect.stdout).lower():
            require_success(connect, f"connect {args.postgres_container} to {args.api_docker_network}")


def docker_database_url(args: argparse.Namespace) -> str:
    default_host_url = "postgres://packaging:packaging@127.0.0.1:5432/packaging?sslmode=disable"
    if args.database_url == default_host_url:
        return "postgres://packaging:packaging@packaging-postgres:5432/packaging?sslmode=disable"
    return args.database_url


def build_api_binary(root: pathlib.Path, target: str) -> pathlib.Path:
    backend_dir = root / "backend"
    dist_dir = root / "dist"
    dist_dir.mkdir(exist_ok=True)
    if target == "linux":
        api_binary = dist_dir / "packaging-api-acceptance-linux-amd64"
        env = {"GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "0"}
    else:
        api_binary = dist_dir / ("packaging-api-acceptance.exe" if os.name == "nt" else "packaging-api-acceptance")
        env = {}
    build = run_command(["go", "build", "-o", str(api_binary), "./cmd/server"], backend_dir, env=env, timeout=300)
    require_success(build, "build local API binary")
    return api_binary


def start_api_if_needed(args: argparse.Namespace, root: pathlib.Path) -> StartedApi | None:
    if api_is_healthy(args.api_url):
        return None
    if not args.start_api:
        raise RuntimeError(f"Packaging API is not reachable: {args.api_url}/api/health")
    if args.api_mode == "docker":
        return start_docker_api(args, root)
    return start_host_api(args, root)


def start_host_api(args: argparse.Namespace, root: pathlib.Path) -> StartedApi:
    backend_dir = root / "backend"
    api_binary = build_api_binary(root, "host")
    env = {
        "PORT": str(api_port(args.api_url)),
        "STORE_DRIVER": args.store_driver,
        "DATABASE_URL": args.database_url,
        "PRODUCTS_DIR": str(root / "products"),
        "ARTIFACT_STORE": "local",
        "DATA_DIR": str(backend_dir / "data"),
        "DRY_RUN": "true",
        "PACKAGING_API_URL": args.api_url,
    }
    process = subprocess.Popen(
        [str(api_binary)],
        cwd=backend_dir,
        env={**os.environ, **env},
        text=True,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.STDOUT,
    )
    try:
        wait_for_api(args.api_url, args.api_start_timeout)
    except Exception:
        process.terminate()
        raise
    return StartedApi(mode="host", process=process, binary=api_binary)


def start_docker_api(args: argparse.Namespace, root: pathlib.Path) -> StartedApi:
    ensure_docker_network(args, root)
    api_binary = build_api_binary(root, "linux")
    run_command(["docker", "rm", "-f", args.api_container], root, timeout=60)
    port = str(api_port(args.api_url))
    command = [
        "docker",
        "run",
        "-d",
        "--name",
        args.api_container,
        "--network",
        args.api_docker_network,
        "-p",
        f"{port}:8080",
        "-v",
        f"{docker_mount_path(root)}:/workspace",
        "-w",
        "/workspace",
        "-e",
        "STORE_DRIVER=postgres",
        "-e",
        f"DATABASE_URL={docker_database_url(args)}",
        "-e",
        "PRODUCTS_DIR=/workspace/products",
        "-e",
        "ARTIFACT_STORE=local",
        "-e",
        "DATA_DIR=/workspace/backend/data",
        "-e",
        "DRY_RUN=true",
        "-e",
        f"PACKAGING_API_URL={args.api_url}",
        args.api_container_image,
        "/workspace/dist/packaging-api-acceptance-linux-amd64",
    ]
    require_success(run_command(command, root, timeout=120), "start docker API")
    if not args.runner_api_url:
        args.runner_api_url = f"http://{args.api_container}:8080"
    if not args.docker_network:
        args.docker_network = args.api_docker_network
    args.add_host_gateway = False
    try:
        wait_for_api(args.api_url, args.api_start_timeout)
    except Exception:
        run_command(["docker", "rm", "-f", args.api_container], root, timeout=60)
        raise
    return StartedApi(mode="docker", container=args.api_container, binary=api_binary)


def stop_started_api(started: StartedApi | None, root: pathlib.Path) -> None:
    if started is None:
        return
    if started.mode == "docker":
        run_command(["docker", "rm", "-f", started.container], root, timeout=60)
        if started.binary:
            started.binary.unlink(missing_ok=True)
        return
    process = started.process
    if process is None:
        return
    if os.name == "nt":
        subprocess.run(
            ["taskkill", "/PID", str(process.pid), "/T", "/F"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            timeout=15,
        )
        if started.binary:
            started.binary.unlink(missing_ok=True)
        return
    process.terminate()
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        process.kill()
    if started.binary:
        started.binary.unlink(missing_ok=True)


def docker_available(root: pathlib.Path) -> CheckResult:
    result = run_command(["docker", "version", "--format", "{{.Server.Version}}"], root, timeout=30)
    if result.returncode == 0:
        return CheckResult("Docker daemon", "ok", result.stdout.strip())
    detail = (result.stderr or result.stdout or "docker version failed").strip()
    return CheckResult("Docker daemon", "fail", detail)


def docker_image_exists(image: str, root: pathlib.Path) -> bool:
    result = run_command(["docker", "image", "inspect", image], root, timeout=30)
    return result.returncode == 0


def ensure_docker_image(image: str, root: pathlib.Path, pull_missing: bool) -> CheckResult:
    if docker_image_exists(image, root):
        return CheckResult(f"Docker image {image}", "ok", "available locally")
    if not pull_missing:
        return CheckResult(f"Docker image {image}", "fail", "missing locally")
    result = run_command(["docker", "pull", image], root, timeout=600, stream=True)
    if result.returncode == 0:
        return CheckResult(f"Docker image {image}", "ok", "pulled")
    return CheckResult(f"Docker image {image}", "fail", (result.stdout or result.stderr).strip())


def ensure_runner_image(args: argparse.Namespace, root: pathlib.Path) -> CheckResult:
    if args.rebuild_runner or not docker_image_exists(args.runner_image, root):
        result = run_command(["docker", "build", "-t", args.runner_image, "runner"], root, timeout=900, stream=True)
        if result.returncode != 0:
            return CheckResult("Runner image", "fail", (result.stdout or result.stderr).strip())
        return CheckResult("Runner image", "ok", f"built {args.runner_image}")
    return CheckResult("Runner image", "ok", f"available {args.runner_image}")


def runner_tooling_check(args: argparse.Namespace, root: pathlib.Path) -> CheckResult:
    result = run_command(
        [
            "docker",
            "run",
            "--rm",
            "-v",
            "/var/run/docker.sock:/var/run/docker.sock",
            args.runner_image,
            "sh",
            "-lc",
            "docker version --format '{{.Server.Version}}' && dagger version",
        ],
        root,
        timeout=120,
    )
    if result.returncode == 0:
        lines = [line.strip() for line in result.stdout.splitlines() if line.strip()]
        return CheckResult("Runner Docker/Dagger tooling", "ok", " | ".join(lines[:2]))
    return CheckResult("Runner Docker/Dagger tooling", "fail", (result.stdout + result.stderr).strip())


def queued_conflicts(api_url: str, target: str) -> list[dict[str, Any]]:
    builds = request_json(api_url, "GET", "/api/builds")
    if not isinstance(builds, list):
        raise RuntimeError(f"unexpected builds response: {builds!r}")
    return [build for build in builds if build.get("status") == "queued" and build.get("target") == target]


def cleanup_stale_acceptance_builds(args: argparse.Namespace) -> list[str]:
    if not args.cancel_stale_acceptance:
        return []
    builds = request_json(args.api_url, "GET", "/api/builds")
    if not isinstance(builds, list):
        raise RuntimeError(f"unexpected builds response: {builds!r}")
    canceled: list[str] = []
    for build in builds:
        variables = build.get("variables") or {}
        if variables.get("requestedBy") != "acceptance_cli.py":
            continue
        if build.get("status") not in {"pending_approval", "queued", "running"}:
            continue
        try:
            request_json(args.api_url, "POST", f"/api/builds/{build['id']}/cancel", {})
        except Exception as exc:
            time.sleep(1)
            current = request_json(args.api_url, "GET", f"/api/builds/{build['id']}")
            if not (isinstance(current, dict) and current.get("status") == "canceled"):
                raise exc
        canceled.append(build["id"])
    return canceled


def preflight(args: argparse.Namespace, root: pathlib.Path) -> list[CheckResult]:
    results = [
        CheckResult("Packaging API", "ok" if api_is_healthy(args.api_url) else "fail", f"{args.api_url}/api/health"),
        docker_available(root),
    ]

    try:
        products = request_json(args.api_url, "GET", "/api/products")
        has_product = isinstance(products, list) and any(item.get("id") == args.product_id for item in products)
        results.append(CheckResult("Product manifest", "ok" if has_product else "fail", args.product_id))
    except Exception as exc:
        results.append(CheckResult("Product manifest", "fail", str(exc)))

    try:
        conflicts = queued_conflicts(args.api_url, args.target)
        status = "ok" if not conflicts or args.allow_existing_queued else "fail"
        detail = "no queued target build" if not conflicts else f"{len(conflicts)} queued {args.target} build(s) exist"
        results.append(CheckResult("Runner queue isolation", status, detail))
    except Exception as exc:
        results.append(CheckResult("Runner queue isolation", "fail", str(exc)))

    return results


def ensure_environment(args: argparse.Namespace, root: pathlib.Path) -> None:
    checks = preflight(args, root)
    checks.append(ensure_runner_image(args, root))
    checks.append(ensure_docker_image(args.dagger_engine_image, root, args.pull_missing_images))
    if args.dagger_base_image_source == "host":
        checks.append(ensure_docker_image(args.dagger_base_image, root, args.pull_missing_images))
    checks.append(runner_tooling_check(args, root))
    print_checks(checks)
    failed = [check for check in checks if check.status == "fail"]
    if failed:
        raise RuntimeError("preflight failed; fix failed confirmation lights before running acceptance")


def upload_components(args: argparse.Namespace) -> dict[str, dict[str, Any]]:
    product = request_json(args.api_url, "GET", f"/api/products/{args.product_id}")
    if not isinstance(product, dict):
        raise RuntimeError(f"unexpected product response: {product!r}")
    uploads: dict[str, dict[str, Any]] = {}
    for component in product.get("components", []):
        component_id = component["id"]
        filename = f"{component_id}-{args.namespace}.bin"
        payload = (
            f"acceptance component\n"
            f"component={component_id}\n"
            f"namespace={args.namespace}\n"
            f"createdAt={datetime.now(timezone.utc).isoformat()}\n"
        ).encode("utf-8")
        uploads[component_id] = upload_file(args.api_url, filename, payload)
    return uploads


def component_overrides(uploads: dict[str, dict[str, Any]]) -> dict[str, dict[str, Any]]:
    return {
        component_id: {
            "kind": "manual-upload",
            "uploadId": upload["id"],
            "uri": upload["uri"],
        }
        for component_id, upload in uploads.items()
    }


def create_build(args: argparse.Namespace, overrides: dict[str, Any]) -> dict[str, Any]:
    body = {
        "productId": args.product_id,
        "target": args.target,
        "profile": args.profile,
        "customer": args.customer,
        "version": args.version,
        "releaseChannel": "preview",
        "variables": {
            "acceptanceNamespace": args.namespace,
            "seedNamespace": args.namespace,
            "seedScenario": "local-acceptance-real-runner",
            "requestedBy": "acceptance_cli.py",
        },
        "configOverrides": {
            "installDir": "/opt/lsap-acceptance",
            "ports": {"web": args.web_port},
            "customer": {"name": "Local Acceptance Customer", "license": "enterprise"},
            "database": {"passwordPolicy": "strong"},
            "modules": {"camera": True, "push": True, "map": True},
        },
        "componentOverrides": overrides,
    }
    build = request_json(args.api_url, "POST", "/api/builds", body)
    if not isinstance(build, dict):
        raise RuntimeError(f"unexpected build response: {build!r}")
    return build


def approve_build(api_url: str, build_id: str) -> dict[str, Any]:
    try:
        build = request_json(api_url, "POST", f"/api/builds/{build_id}/approve", {})
    except Exception as exc:
        time.sleep(1)
        current = request_json(api_url, "GET", f"/api/builds/{build_id}")
        if isinstance(current, dict) and current.get("status") in {"queued", "running", "succeeded", "preview_validated"}:
            print(f"    {light('warn')} approve response interrupted; continuing with status={current['status']}")
            return current
        raise exc
    if not isinstance(build, dict):
        raise RuntimeError(f"unexpected approve response: {build!r}")
    return build


def docker_runner_command(args: argparse.Namespace) -> list[str]:
    command = ["docker", "run", "--rm"]
    if args.add_host_gateway:
        command.extend(["--add-host", "host.docker.internal:host-gateway"])
    if args.docker_network:
        command.extend(["--network", args.docker_network])
    command.extend(["-v", "/var/run/docker.sock:/var/run/docker.sock"])
    env = {
        "PACKAGING_API_URL": args.runner_api_url or default_runner_api_url(args.api_url),
        "TARGET": args.target,
        "RUNNER_ID": args.runner_id,
        "DAGGER_NO_NAG": "1",
        "DAGGER_LINUX_BASE_IMAGE_SOURCE": args.dagger_base_image_source,
        "DAGGER_LINUX_BASE_IMAGE": args.dagger_base_image,
        "DAGGER_LINUX_INSTALL_TOOLS": args.dagger_install_tools,
    }
    if args.dagger_base_image_tar:
        env["DAGGER_LINUX_BASE_IMAGE_TAR"] = args.dagger_base_image_tar
    if args.dagger_base_image_tar_tag:
        env["DAGGER_LINUX_BASE_IMAGE_TAR_TAG"] = args.dagger_base_image_tar_tag
    for key, value in env.items():
        command.extend(["-e", f"{key}={value}"])
    command.append(args.runner_image)
    return command


def run_runner(args: argparse.Namespace, root: pathlib.Path) -> None:
    result = run_command(docker_runner_command(args), root, timeout=args.runner_timeout, stream=True)
    require_success(result, "docker runner")


def assert_build_status(api_url: str, build_id: str, expected: str) -> dict[str, Any]:
    build = request_json(api_url, "GET", f"/api/builds/{build_id}")
    if not isinstance(build, dict):
        raise RuntimeError(f"unexpected build response: {build!r}")
    if build.get("status") != expected:
        tail = "\n".join((entry.get("message", "") for entry in (build.get("logs") or [])[-8:]))
        raise RuntimeError(f"build {build_id} expected {expected}, got {build.get('status')}\n{tail}")
    return build


def validate_preview(api_url: str, build_id: str) -> dict[str, Any]:
    try:
        build = request_json(api_url, "POST", f"/api/builds/{build_id}/validate-preview", {})
    except Exception as exc:
        time.sleep(1)
        current = request_json(api_url, "GET", f"/api/builds/{build_id}")
        if isinstance(current, dict) and current.get("status") == "preview_validated":
            print(f"    {light('warn')} validate response interrupted; continuing with status=preview_validated")
            return current
        raise exc
    if not isinstance(build, dict):
        raise RuntimeError(f"unexpected validate-preview response: {build!r}")
    return build


def verify_download(url: str) -> None:
    request = urllib.request.Request(url, method="GET")
    with urllib.request.urlopen(request, timeout=30) as response:
        if response.status != 200:
            raise RuntimeError(f"download returned HTTP {response.status}: {url}")
        if len(response.read(1)) == 0:
            raise RuntimeError(f"download returned an empty response: {url}")


def run_acceptance(args: argparse.Namespace) -> int:
    root = repo_root()
    started_api: StartedApi | None = None
    progress = Progress(total=9)
    try:
        progress.step("Start or verify local API")
        started_api = start_api_if_needed(args, root)

        stale = cleanup_stale_acceptance_builds(args)
        if stale:
            print(f"    {light('warn')} canceled stale acceptance build(s): {', '.join(stale)}")

        progress.step("Check Docker, runner image, Dagger, and queue isolation")
        ensure_environment(args, root)

        progress.step("Upload local component artifacts")
        uploads = upload_components(args)

        progress.step("Create preview build")
        build = create_build(args, component_overrides(uploads))
        print(f"    buildId={build['id']} status={build['status']}")

        progress.step("Approve preview build")
        build = approve_build(args.api_url, build["id"])
        print(f"    buildId={build['id']} status={build['status']} nomadJobId={build.get('nomadJobId', '')}")

        progress.step("Run Docker runner with Dagger")
        run_runner(args, root)
        build = assert_build_status(args.api_url, build["id"], "succeeded")
        print(f"    artifact={build.get('artifactUri', '')}")

        progress.step("Verify artifact download")
        verify_download(build.get("downloadUrl") or f"{args.api_url}/api/builds/{build['id']}/download")

        progress.step("Validate Preview")
        build = validate_preview(args.api_url, build["id"])
        if build.get("status") != "preview_validated":
            raise RuntimeError(f"expected preview_validated, got {build.get('status')}")

        progress.step("Check Release gate")
        gate = request_json(args.api_url, "GET", f"/api/builds/{build['id']}/release-check")
        if not gate.get("releaseReady"):
            raise RuntimeError(f"release gate blocked: {json.dumps(gate, ensure_ascii=False)}")
        print_checks(
            [
                CheckResult("Preview validation", "ok", f"status={build['status']}"),
                CheckResult("Release gate", "ok", f"blockingCount={gate.get('blockingCount')}"),
                CheckResult("Build artifact", "ok", build.get("downloadUrl") or build.get("artifactUri", "")),
            ]
        )
        print(f"\nAcceptance complete: buildId={build['id']} namespace={args.namespace}")
        return 0
    finally:
        if started_api is not None and not args.keep_api:
            stop_started_api(started_api, root)


def run_check(args: argparse.Namespace) -> int:
    root = repo_root()
    started_api = None
    try:
        started_api = start_api_if_needed(args, root)
        checks = preflight(args, root)
        print_checks(checks)
        return 0 if all(check.status != "fail" for check in checks) else 1
    finally:
        if started_api is not None and not args.keep_api:
            stop_started_api(started_api, root)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Run local Packaging Platform acceptance checks.")
    parser.add_argument("--api-url", default=DEFAULT_API_URL)
    parser.add_argument("--runner-api-url", default="")
    parser.add_argument("--namespace", default=f"acceptance-{utc_stamp()}")
    parser.add_argument("--product-id", default="lsap")
    parser.add_argument("--target", default="linux")
    parser.add_argument("--profile", default="qa")
    parser.add_argument("--customer", default="example-customer")
    parser.add_argument("--version", default="1.0.0")
    parser.add_argument("--web-port", type=int, default=49400)
    parser.add_argument("--runner-id", default=f"local-acceptance-runner-{uuid.uuid4().hex[:8]}")
    parser.add_argument("--runner-image", default=DEFAULT_RUNNER_IMAGE)
    parser.add_argument("--rebuild-runner", action="store_true")
    parser.add_argument("--pull-missing-images", action=argparse.BooleanOptionalAction, default=True)
    parser.add_argument("--dagger-engine-image", default=DEFAULT_DAGGER_ENGINE_IMAGE)
    parser.add_argument("--dagger-base-image-source", choices=["host", "registry"], default="host")
    parser.add_argument("--dagger-base-image", default=DEFAULT_BASE_IMAGE)
    parser.add_argument("--dagger-base-image-tar", default="")
    parser.add_argument("--dagger-base-image-tar-tag", default="")
    parser.add_argument("--dagger-install-tools", default="skip")
    parser.add_argument("--docker-network", default="")
    parser.add_argument("--add-host-gateway", action=argparse.BooleanOptionalAction, default=True)
    parser.add_argument("--allow-existing-queued", action="store_true")
    parser.add_argument("--cancel-stale-acceptance", action=argparse.BooleanOptionalAction, default=True)
    parser.add_argument("--api-mode", choices=["docker", "host"], default="docker")
    parser.add_argument("--api-container", default="packaging-api-acceptance")
    parser.add_argument("--api-container-image", default="debian:stable-slim")
    parser.add_argument("--api-docker-network", default="packaging-net")
    parser.add_argument("--postgres-container", default="packaging-postgres")
    parser.add_argument("--start-api", action=argparse.BooleanOptionalAction, default=True)
    parser.add_argument("--keep-api", action="store_true")
    parser.add_argument("--api-start-timeout", type=int, default=45)
    parser.add_argument("--runner-timeout", type=int, default=600)
    parser.add_argument("--store-driver", default=os.environ.get("STORE_DRIVER", "postgres"))
    parser.add_argument(
        "--database-url",
        default=os.environ.get(
            "DATABASE_URL",
            "postgres://packaging:packaging@127.0.0.1:5432/packaging?sslmode=disable",
        ),
    )

    subparsers = parser.add_subparsers(dest="command")
    subparsers.add_parser("run", help="run the full local acceptance flow")
    subparsers.add_parser("check", help="run preflight checks only")
    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    if args.command in {None, "run"}:
        return run_acceptance(args)
    if args.command == "check":
        return run_check(args)
    parser.error(f"unknown command: {args.command}")
    return 2


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        print(f"{light('fail')} interrupted", file=sys.stderr)
        sys.exit(130)
    except Exception as exc:
        print(f"{light('fail')} {exc}", file=sys.stderr)
        sys.exit(1)

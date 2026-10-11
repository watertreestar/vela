#!/usr/bin/env python3
"""Run a local end-to-end acceptance flow for Vela."""

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
DEFAULT_LINUX_FIXTURE_INSTALL_TOOLS = "apk add --no-cache python3 tar gzip coreutils bash"
DEFAULT_WINDOWS_FIXTURE_COMMAND = "python3 /app/fixtures/windows_setup_packager.py"
DEFAULT_WINDOWS_SIGNING_FIXTURE_COMMAND = "python3 /app/fixtures/windows_signing_fixture.py"
WINDOWS_FIXTURE_MARKER = b"PACKAGING_PLATFORM_WINDOWS_SETUP_FIXTURE_V1"
WINDOWS_SIGNING_FIXTURE_MARKER = b"PACKAGING_PLATFORM_WINDOWS_SIGNING_FIXTURE_V1"
DEFAULT_LINUX_FIXTURE_COMMAND = "python3 /workspace/context/rendered/linux_package_packager.py"
LINUX_FIXTURE_MARKER = b"PACKAGING_PLATFORM_LINUX_PACKAGE_FIXTURE_V1"


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
    if args.store_driver in {"postgres", "postgresql", ""}:
        connect_container_to_network(args.postgres_container, args.api_docker_network, root, "postgres")
    if args.artifact_store == "minio":
        connect_container_to_network(args.minio_container, args.api_docker_network, root, "MinIO-compatible storage")


def connect_container_to_network(container: str, network: str, root: pathlib.Path, label: str) -> None:
    if container:
        inspect = run_command(["docker", "container", "inspect", container], root, timeout=30)
        require_success(inspect, f"inspect {label} container {container}")
        connect = run_command(
            ["docker", "network", "connect", network, container],
            root,
            timeout=60,
        )
        if connect.returncode != 0 and "already exists" not in (connect.stderr + connect.stdout).lower():
            require_success(connect, f"connect {container} to {network}")


def docker_database_url(args: argparse.Namespace) -> str:
    default_host_url = "postgres://packaging:packaging@127.0.0.1:5432/packaging?sslmode=disable"
    if args.database_url == default_host_url:
        return "postgres://packaging:packaging@packaging-postgres:5432/packaging?sslmode=disable"
    return args.database_url


def api_minio_endpoint(args: argparse.Namespace) -> str:
    if args.api_minio_endpoint:
        return args.api_minio_endpoint
    if args.api_mode == "docker" and args.minio_container:
        return f"{args.minio_container}:9000"
    return args.minio_endpoint


def runner_minio_public_url(args: argparse.Namespace) -> str:
    if args.runner_minio_public_url:
        return args.runner_minio_public_url.rstrip("/")
    if args.minio_public_url:
        return args.minio_public_url.rstrip("/")
    if args.docker_network and args.minio_container:
        return f"http://{args.minio_container}:9000"
    parsed = urllib.parse.urlparse(args.api_url)
    scheme = parsed.scheme or "http"
    return f"{scheme}://{args.minio_endpoint}"


def artifact_store_env(args: argparse.Namespace, for_api_container: bool) -> dict[str, str]:
    env = {"ARTIFACT_STORE": args.artifact_store}
    if args.artifact_store != "minio":
        return env
    endpoint = api_minio_endpoint(args) if for_api_container else args.minio_endpoint
    env.update(
        {
            "MINIO_ENDPOINT": endpoint,
            "MINIO_ACCESS_KEY": args.minio_access_key,
            "MINIO_SECRET_KEY": args.minio_secret_key,
            "MINIO_BUCKET": args.minio_bucket,
            "MINIO_USE_SSL": "true" if args.minio_use_ssl else "false",
        }
    )
    if args.minio_public_url:
        env["MINIO_PUBLIC_URL"] = args.minio_public_url.rstrip("/")
    return env


def build_api_binary(root: pathlib.Path, target: str) -> pathlib.Path:
    conductor_dir = root / "conductor"
    dist_dir = root / "dist"
    dist_dir.mkdir(exist_ok=True)
    if target == "linux":
        api_binary = dist_dir / "packaging-api-acceptance-linux-amd64"
        env = {"GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "0"}
    else:
        api_binary = dist_dir / ("packaging-api-acceptance.exe" if os.name == "nt" else "packaging-api-acceptance")
        env = {}
    build = run_command(["go", "build", "-o", str(api_binary), "./cmd/server"], conductor_dir, env=env, timeout=300)
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
    conductor_dir = root / "conductor"
    api_binary = build_api_binary(root, "host")
    env = {
        "PORT": str(api_port(args.api_url)),
        "STORE_DRIVER": args.store_driver,
        "DATABASE_URL": args.database_url,
        "PRODUCTS_DIR": str(root / "products"),
        "DATA_DIR": str(conductor_dir / "data"),
        "DRY_RUN": "true",
        "PACKAGING_API_URL": args.api_url,
    }
    env.update(artifact_store_env(args, for_api_container=False))
    process = subprocess.Popen(
        [str(api_binary)],
        cwd=conductor_dir,
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
        f"STORE_DRIVER={args.store_driver}",
        "-e",
        f"DATABASE_URL={docker_database_url(args)}",
        "-e",
        "PRODUCTS_DIR=/workspace/products",
        "-e",
        "DATA_DIR=/workspace/conductor/data",
        "-e",
        "DRY_RUN=true",
        "-e",
        f"PACKAGING_API_URL={args.api_url}",
        args.api_container_image,
        "/workspace/dist/packaging-api-acceptance-linux-amd64",
    ]
    for key, value in artifact_store_env(args, for_api_container=True).items():
        command[-2:-2] = ["-e", f"{key}={value}"]
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


def runner_download_url(args: argparse.Namespace, upload: dict[str, Any]) -> str:
    download_url = upload.get("downloadUrl") or ""
    parsed = urllib.parse.urlparse(download_url)
    path = parsed.path or f"/api/uploads/{upload['id']}/download"
    runner_base = (args.runner_api_url or default_runner_api_url(args.api_url)).rstrip("/")
    return f"{runner_base}{path}"


def component_overrides(args: argparse.Namespace, uploads: dict[str, dict[str, Any]]) -> dict[str, dict[str, Any]]:
    overrides: dict[str, dict[str, Any]] = {}
    for component_id, upload in uploads.items():
        override = {
            "kind": "manual-upload",
            "uploadId": upload["id"],
        }
        if args.artifact_store == "minio":
            override["uri"] = runner_download_url(args, upload)
        overrides[component_id] = override
    return overrides


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


def ensure_auth_mode_secret_boundary(args: argparse.Namespace) -> None:
    if not getattr(args, "auth_enabled", False):
        return
    forbidden = ("AUTH_RUNNER_TOKEN", "RUNNER_EXECUTION_HMAC_KEY", "X_RUNNER_EXECUTION", "RUNNER_EXECUTION_CAPABILITY")
    leaked = [key for key in forbidden if os.environ.get(key)]
    if leaked:
        raise RuntimeError("auth-enabled acceptance refuses runner secrets from process environment; use the dedicated Nomad live E2E gate")
    raise RuntimeError("auth-enabled acceptance is not implemented by the local Docker/host runner flow; use scripts/runner_e2e_cli.py --live against a real Nomad allocation")


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
    if uses_linux_packager_fixture(args):
        env["DAGGER_LINUX_PACKAGE_CMD"] = args.linux_fixture_command
        if args.dagger_install_tools == "skip":
            env["DAGGER_LINUX_INSTALL_TOOLS"] = DEFAULT_LINUX_FIXTURE_INSTALL_TOOLS
    if args.artifact_store == "minio":
        env["MINIO_PUBLIC_URL"] = runner_minio_public_url(args)
    if args.target == "windows":
        windows_command = args.windows_packager_command
        if not windows_command and uses_windows_packager_fixture(args):
            windows_command = args.windows_fixture_command
        if windows_command:
            env["WINDOWS_PACKAGER_CMD"] = windows_command
        windows_signing_command = args.windows_signing_command
        if not windows_signing_command and uses_windows_signing_fixture(args):
            windows_signing_command = args.windows_signing_fixture_command
        if windows_signing_command:
            env["WINDOWS_SIGNING_CMD"] = windows_signing_command
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


def download_bytes(url: str) -> bytes:
    request = urllib.request.Request(url, method="GET")
    with urllib.request.urlopen(request, timeout=30) as response:
        if response.status != 200:
            raise RuntimeError(f"download returned HTTP {response.status}: {url}")
        data = response.read()
        if len(data) == 0:
            raise RuntimeError(f"download returned an empty response: {url}")
        return data


def verify_download(url: str) -> None:
    download_bytes(url)


def uses_windows_packager_fixture(args: argparse.Namespace) -> bool:
    return args.target == "windows" and args.windows_packager_fixture and not args.windows_packager_command


def uses_windows_signing_fixture(args: argparse.Namespace) -> bool:
    return args.target == "windows" and args.windows_signing_fixture and not args.windows_signing_command


def uses_linux_packager_fixture(args: argparse.Namespace) -> bool:
    return args.target == "linux" and args.linux_packager_fixture


def verify_artifact_download(args: argparse.Namespace, build: dict[str, Any]) -> None:
    url = build.get("downloadUrl") or f"{args.api_url}/api/builds/{build['id']}/download"
    data = download_bytes(url)
    if uses_linux_packager_fixture(args) and args.verify_linux_fixture:
        if LINUX_FIXTURE_MARKER not in data:
            raise RuntimeError("linux artifact was downloaded, but fixture marker was not found")
        if str(build["id"]).encode("utf-8") not in data:
            raise RuntimeError("linux artifact fixture does not contain the build id")
    if uses_windows_packager_fixture(args) and args.verify_windows_fixture:
        if WINDOWS_FIXTURE_MARKER not in data:
            raise RuntimeError("windows artifact was downloaded, but fixture marker was not found")
        if str(build["id"]).encode("utf-8") not in data:
            raise RuntimeError("windows artifact fixture does not contain the build id")
    if uses_windows_signing_fixture(args) and args.verify_windows_signing_fixture:
        if WINDOWS_SIGNING_FIXTURE_MARKER not in data:
            raise RuntimeError("windows artifact was downloaded, but signing fixture marker was not found")
        if str(build["id"]).encode("utf-8") not in data:
            raise RuntimeError("windows signing fixture does not contain the build id")


def run_acceptance(args: argparse.Namespace) -> int:
    ensure_auth_mode_secret_boundary(args)
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
        build = create_build(args, component_overrides(args, uploads))
        print(f"    buildId={build['id']} status={build['status']}")

        progress.step("Approve preview build")
        build = approve_build(args.api_url, build["id"])
        print(f"    buildId={build['id']} status={build['status']} nomadJobId={build.get('nomadJobId', '')}")

        progress.step("Run Docker runner")
        run_runner(args, root)
        build = assert_build_status(args.api_url, build["id"], "succeeded")
        print(f"    artifact={build.get('artifactUri', '')}")
        if args.artifact_store == "minio" and not str(build.get("artifactUri", "")).startswith("minio://"):
            raise RuntimeError(f"expected minio artifact URI, got {build.get('artifactUri', '')}")

        progress.step("Verify artifact download")
        verify_artifact_download(args, build)

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
    parser = argparse.ArgumentParser(description="Run local Vela acceptance checks.")
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
    parser.add_argument("--linux-packager-fixture", action=argparse.BooleanOptionalAction, default=False)
    parser.add_argument("--linux-fixture-command", default=DEFAULT_LINUX_FIXTURE_COMMAND)
    parser.add_argument("--verify-linux-fixture", action=argparse.BooleanOptionalAction, default=True)
    parser.add_argument("--windows-packager-fixture", action=argparse.BooleanOptionalAction, default=True)
    parser.add_argument("--windows-fixture-command", default=DEFAULT_WINDOWS_FIXTURE_COMMAND)
    parser.add_argument("--windows-packager-command", default=os.environ.get("WINDOWS_PACKAGER_CMD", ""))
    parser.add_argument("--verify-windows-fixture", action=argparse.BooleanOptionalAction, default=True)
    parser.add_argument("--windows-signing-fixture", action=argparse.BooleanOptionalAction, default=True)
    parser.add_argument("--windows-signing-fixture-command", default=DEFAULT_WINDOWS_SIGNING_FIXTURE_COMMAND)
    parser.add_argument("--windows-signing-command", default=os.environ.get("WINDOWS_SIGNING_CMD", ""))
    parser.add_argument("--verify-windows-signing-fixture", action=argparse.BooleanOptionalAction, default=True)
    parser.add_argument("--docker-network", default="")
    parser.add_argument("--add-host-gateway", action=argparse.BooleanOptionalAction, default=True)
    parser.add_argument("--allow-existing-queued", action="store_true")
    parser.add_argument("--cancel-stale-acceptance", action=argparse.BooleanOptionalAction, default=True)
    parser.add_argument("--api-mode", choices=["docker", "host"], default="docker")
    parser.add_argument("--auth-enabled", action="store_true", help="refuses local acceptance; use runner_e2e_cli.py --live for auth-enabled Nomad evidence")
    parser.add_argument("--api-container", default="packaging-api-acceptance")
    parser.add_argument("--api-container-image", default="debian:stable-slim")
    parser.add_argument("--api-docker-network", default="packaging-net")
    parser.add_argument("--postgres-container", default="packaging-postgres")
    parser.add_argument("--start-api", action=argparse.BooleanOptionalAction, default=True)
    parser.add_argument("--keep-api", action="store_true")
    parser.add_argument("--api-start-timeout", type=int, default=45)
    parser.add_argument("--runner-timeout", type=int, default=600)
    parser.add_argument("--artifact-store", choices=["local", "minio"], default=os.environ.get("ARTIFACT_STORE", "local"))
    parser.add_argument("--minio-container", default=os.environ.get("MINIO_CONTAINER", "packaging-rustfs"))
    parser.add_argument("--minio-endpoint", default=os.environ.get("MINIO_ENDPOINT") or os.environ.get("RUSTFS_ENDPOINT", "127.0.0.1:9000"))
    parser.add_argument("--api-minio-endpoint", default=os.environ.get("API_MINIO_ENDPOINT", ""))
    parser.add_argument("--minio-public-url", default=os.environ.get("MINIO_PUBLIC_URL", ""))
    parser.add_argument("--runner-minio-public-url", default=os.environ.get("RUNNER_MINIO_PUBLIC_URL", ""))
    parser.add_argument("--minio-access-key", default=os.environ.get("MINIO_ACCESS_KEY") or os.environ.get("RUSTFS_ACCESS_KEY", "rustfsadmin"))
    parser.add_argument("--minio-secret-key", default=os.environ.get("MINIO_SECRET_KEY") or os.environ.get("RUSTFS_SECRET_KEY", "rustfsadmin"))
    parser.add_argument("--minio-bucket", default=os.environ.get("MINIO_BUCKET") or os.environ.get("RUSTFS_BUCKET", "packaging-artifacts"))
    parser.add_argument("--minio-use-ssl", action=argparse.BooleanOptionalAction, default=os.environ.get("MINIO_USE_SSL", "false") == "true")
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
    argv = sys.argv[1:]
    command = None
    if argv and argv[0] in {"run", "check"}:
        command = argv[0]
        argv = argv[1:]
    args = parser.parse_args(argv)
    if command:
        args.command = command
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

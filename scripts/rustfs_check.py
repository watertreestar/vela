#!/usr/bin/env python3
"""Check local RustFS connectivity and run Vela RustFS integration tests."""

from __future__ import annotations

import argparse
import os
import pathlib
import socket
import subprocess
import sys
import time
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


class Progress:
    def __init__(self, total: int) -> None:
        self.total = total
        self.current = 0

    def step(self, message: str) -> None:
        self.current += 1
        print(f"[{self.current:02d}/{self.total:02d}] {message}")


def repo_root() -> pathlib.Path:
    return pathlib.Path(__file__).resolve().parents[1]


def run_command(command: list[str], cwd: pathlib.Path, timeout: int = 60, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        command,
        cwd=cwd,
        env={**os.environ, **(env or {})},
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=timeout,
    )


def docker_container_running(name: str, root: pathlib.Path) -> bool:
    result = run_command(["docker", "inspect", "-f", "{{.State.Running}}", name], root, timeout=30)
    return result.returncode == 0 and result.stdout.strip() == "true"


def docker_container_has_network(name: str, root: pathlib.Path) -> bool:
    result = run_command(["docker", "inspect", "-f", "{{json .NetworkSettings.Networks}}", name], root, timeout=30)
    return result.returncode == 0 and result.stdout.strip() not in {"{}", "null", ""}


def docker_connect_bridge(name: str, root: pathlib.Path) -> CheckResult:
    if docker_container_has_network(name, root):
        return CheckResult("Docker bridge network", "ok", f"{name} already has a network")
    result = run_command(["docker", "network", "connect", "bridge", name], root, timeout=30)
    if result.returncode == 0:
        return CheckResult("Docker bridge network", "ok", f"connected {name} to bridge")
    return CheckResult("Docker bridge network", "fail", (result.stderr or result.stdout).strip())


def tcp_open(host: str, port: int, timeout: float = 3.0) -> bool:
    try:
        with socket.create_connection((host, port), timeout=timeout):
            return True
    except OSError:
        return False


def docker_tcp_open(container: str, root: pathlib.Path) -> bool:
    """Probe the S3 API from the RustFS network for remote Docker contexts."""
    result = run_command(
        [
            "docker", "run", "--rm", "--network", f"container:{container}",
            "curlimages/curl:8.10.1", "-sS", "--max-time", "5", "-o", "/dev/null", "-w", "%{http_code}",
            "http://127.0.0.1:9000/",
        ],
        root,
        timeout=30,
    )
    # An unauthenticated S3 root request usually returns 403, which still proves reachability.
    return result.returncode == 0 and result.stdout.strip() in {"200", "403"}


def docker_context_host(root: pathlib.Path) -> str:
    context = run_command(["docker", "context", "show"], root, timeout=30)
    if context.returncode != 0 or context.stdout.strip() in {"", "default"}:
        return ""
    endpoint = run_command(["docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}"], root, timeout=30)
    host = endpoint.stdout.strip()
    return host.removeprefix("ssh://").strip() if host.startswith("ssh://") else ""


def integration_endpoint(args: argparse.Namespace) -> str:
    if args.integration_endpoint:
        return args.integration_endpoint
    return args.endpoint


def rustfs_env(args: argparse.Namespace, integration_flag: str) -> dict[str, str]:
    env = {
        integration_flag: "1",
        "RUSTFS_ENDPOINT": integration_endpoint(args),
        "RUSTFS_ACCESS_KEY": args.access_key,
        "RUSTFS_SECRET_KEY": args.secret_key,
        "RUSTFS_USE_SSL": str(args.use_ssl).lower(),
    }
    return env


def remote_go_test(command: list[str], env: dict[str, str], root: pathlib.Path, timeout: int) -> subprocess.CompletedProcess[str]:
    """Run the Go test on the remote Docker host without forwarding credentials in argv."""
    host = docker_context_host(root)
    if not host:
        return subprocess.CompletedProcess(command, 1, "", "remote Docker host is not configured")
    # Each integration test needs its own workspace. Go's module cache contains
    # read-only directories, so reusing the same path can make the next test's
    # initial rm fail before that test is executed.
    remote_root = f"/tmp/vela-rustfs-check-{os.getpid()}-{time.time_ns()}"
    cleanup_command = f"chmod -R u+w {remote_root} 2>/dev/null || true; rm -rf {remote_root}"
    setup = subprocess.run(
        ["ssh", host, f"mkdir -p {remote_root}"],
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=30,
    )
    if setup.returncode != 0:
        return subprocess.CompletedProcess(command, setup.returncode, setup.stdout, setup.stderr)
    copy = subprocess.run(
        ["scp", "-r", str(root / "conductor"), f"{host}:{remote_root}/"],
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=timeout,
    )
    if copy.returncode != 0:
        subprocess.run(["ssh", host, cleanup_command], text=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
        return subprocess.CompletedProcess(command, copy.returncode, copy.stdout, copy.stderr)
    exported = " ".join(f"{key}={subprocess.list2cmdline([value])}" for key, value in env.items())
    remote_command = f"cd {remote_root}/conductor && {exported} {' '.join(command)}"
    result = subprocess.run(
        ["ssh", host, remote_command],
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=timeout,
    )
    subprocess.run(["ssh", host, cleanup_command], text=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
    return result


def integration_command(args: argparse.Namespace, root: pathlib.Path, command: list[str], env: dict[str, str]) -> subprocess.CompletedProcess[str]:
    if args.remote_docker_host:
        return remote_go_test(command, env, root, args.test_timeout)
    return run_command(command, root / "conductor", timeout=args.test_timeout, env=env)


def run_rustfs_integration(args: argparse.Namespace, root: pathlib.Path) -> CheckResult:
    env = rustfs_env(args, "PACKAGING_RUSTFS_INTEGRATION")
    if args.bucket:
        env["RUSTFS_BUCKET"] = args.bucket
    result = integration_command(args, root, ["go", "test", "./internal/artifact", "-run", "TestResolveArtifactSourceWithRustFSIntegration", "-v"], env)
    if result.returncode == 0:
        return CheckResult("RustFS artifact resolver", "ok", "wildcard URI and sha256 sidecar resolved")
    output = "\n".join(part for part in [result.stdout, result.stderr] if part).strip()
    return CheckResult("RustFS artifact resolver", "fail", output[-1200:])


def run_object_lock_integration(args: argparse.Namespace, root: pathlib.Path) -> CheckResult:
    env = rustfs_env(args, "PACKAGING_RUSTFS_OBJECT_LOCK_INTEGRATION")
    env.update(
        {
            "RUSTFS_OBJECT_LOCK_RETENTION_MODE": args.retention_mode,
            "RUSTFS_OBJECT_LOCK_RETENTION_DAYS": str(args.retention_days),
            "RUSTFS_OBJECT_LOCK_LEGAL_HOLD": str(args.legal_hold).lower(),
        }
    )
    if args.object_lock_bucket or args.bucket:
        env["RUSTFS_BUCKET"] = args.object_lock_bucket or args.bucket
    result = integration_command(args, root, ["go", "test", "./internal/artifact", "-run", "TestAuditArchiveObjectLockWithRustFSIntegration", "-v"], env)
    if result.returncode == 0:
        return CheckResult("RustFS audit object lock", "ok", "audit archive retention and legal hold verified")
    output = "\n".join(part for part in [result.stdout, result.stderr] if part).strip()
    return CheckResult("RustFS audit object lock", "fail", output[-1200:])


def print_results(results: list[CheckResult]) -> None:
    print("\nConfirmation lights:")
    for result in results:
        print(f"  {light(result.status)} {result.name}: {result.detail}")


def validate_production_inputs(args: argparse.Namespace) -> list[CheckResult]:
    if not args.production:
        return []
    results = [
        CheckResult("Production object-lock mode", "ok" if args.object_lock else "fail", "--object-lock is required for production bucket acceptance"),
        CheckResult("Production object-lock bucket", "ok" if args.object_lock_bucket or args.bucket else "fail", "--object-lock-bucket or --bucket must name the live bucket under test"),
        CheckResult("Production object-lock retention mode", "ok" if args.retention_mode in {"GOVERNANCE", "COMPLIANCE"} else "fail", f"retention mode={args.retention_mode or '<missing>'}; expected GOVERNANCE or COMPLIANCE"),
        CheckResult("Production object-lock retention days", "ok" if args.retention_days > 0 else "fail", f"retention days={args.retention_days}; expected a positive value"),
    ]
    if args.access_key == "rustfsadmin" or args.secret_key == "rustfsadmin":
        results.append(CheckResult("Production credentials", "warn", "default local RustFS credentials are in use"))
    return results


def check(args: argparse.Namespace) -> int:
    root = repo_root()
    args.retention_mode = args.retention_mode.upper()
    if not args.remote_docker_host:
        args.remote_docker_host = docker_context_host(root)
    production_results = validate_production_inputs(args)
    if any(result.status == "fail" for result in production_results):
        print_results(production_results)
        return 1
    total = 2 + (2 if not args.skip_container_check else 0) + (1 if args.object_lock else 0) + (1 if production_results else 0)
    progress = Progress(total)
    results: list[CheckResult] = []
    if production_results:
        progress.step("checking production object-lock inputs")
        results.extend(production_results)
    if not args.skip_container_check:
        progress.step("checking RustFS container")
        if docker_container_running(args.container, root):
            results.append(CheckResult("RustFS container", "ok", f"{args.container} is running"))
        else:
            results.append(CheckResult("RustFS container", "fail", f"{args.container} is not running"))
            print_results(results)
            return 1
        progress.step("checking Docker network")
        results.append(docker_connect_bridge(args.container, root) if args.connect_bridge else CheckResult("Docker bridge network", "ok" if docker_container_has_network(args.container, root) else "warn", "container has a network" if docker_container_has_network(args.container, root) else "container has no network; use --connect-bridge"))
    progress.step("checking S3 endpoint TCP")
    host, port_text = args.endpoint.rsplit(":", 1)
    reachable = tcp_open(host, int(port_text))
    if not reachable and not args.skip_container_check:
        reachable = docker_tcp_open(args.container, root)
    if not reachable:
        results.append(CheckResult("RustFS endpoint", "fail", f"{args.endpoint} is not reachable"))
        print_results(results)
        return 1
    results.append(CheckResult("RustFS endpoint", "ok", args.endpoint))
    progress.step("running Go RustFS integration test")
    results.append(run_rustfs_integration(args, root))
    if args.object_lock:
        progress.step("running audit archive object-lock integration test")
        results.append(run_object_lock_integration(args, root))
    print_results(results)
    return 0 if all(result.status != "fail" for result in results) else 1


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Vela RustFS check CLI.")
    parser.add_argument("--container", default="packaging-rustfs")
    parser.add_argument("--endpoint", default=os.environ.get("RUSTFS_ENDPOINT", "127.0.0.1:9000"))
    parser.add_argument("--integration-endpoint", default=os.environ.get("RUSTFS_INTEGRATION_ENDPOINT", ""), help="RustFS endpoint reachable from the Go test; defaults to --endpoint")
    parser.add_argument("--remote-docker-host", default="", help="SSH host used to run the Go test alongside a remote Docker daemon")
    parser.add_argument("--access-key", default=os.environ.get("RUSTFS_ACCESS_KEY", "rustfsadmin"))
    parser.add_argument("--secret-key", default=os.environ.get("RUSTFS_SECRET_KEY", "rustfsadmin"))
    parser.add_argument("--bucket", default=os.environ.get("RUSTFS_BUCKET", ""))
    parser.add_argument("--object-lock", action="store_true", help="also write and verify an audit archive object with retention/legal hold")
    parser.add_argument("--object-lock-bucket", default=os.environ.get("RUSTFS_OBJECT_LOCK_BUCKET", ""), help="fresh object-lock bucket for --object-lock; defaults to --bucket or a generated test bucket")
    parser.add_argument("--retention-mode", default=os.environ.get("RUSTFS_OBJECT_LOCK_RETENTION_MODE", "GOVERNANCE").upper(), help="object-lock retention mode expected by live check: GOVERNANCE or COMPLIANCE")
    parser.add_argument("--retention-days", type=int, default=int(os.environ.get("RUSTFS_OBJECT_LOCK_RETENTION_DAYS", "1")), help="object-lock retention days expected by live check")
    parser.add_argument("--legal-hold", action=argparse.BooleanOptionalAction, default=os.environ.get("RUSTFS_OBJECT_LOCK_LEGAL_HOLD", "true").lower() != "false", help="whether the live check expects object legal hold to be enabled")
    parser.add_argument("--use-ssl", action=argparse.BooleanOptionalAction, default=os.environ.get("RUSTFS_USE_SSL", "false").lower() == "true", help="use TLS for RustFS S3 API integration tests")
    parser.add_argument("--connect-bridge", action=argparse.BooleanOptionalAction, default=True)
    parser.add_argument("--skip-container-check", action="store_true", help="skip Docker container/network checks for a managed RustFS service")
    parser.add_argument("--production", action="store_true", help="strict live object-lock bucket acceptance: requires --object-lock and an explicit bucket")
    parser.add_argument("--test-timeout", type=int, default=120)
    return parser


def main() -> int:
    return check(build_parser().parse_args())


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        print(f"{light('fail')} interrupted", file=sys.stderr)
        sys.exit(130)
    except Exception as exc:
        print(f"{light('fail')} {exc}", file=sys.stderr)
        sys.exit(1)

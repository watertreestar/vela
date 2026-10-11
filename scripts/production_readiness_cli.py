#!/usr/bin/env python3
"""Run Vela production readiness gates."""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


@dataclass
class CheckResult:
    name: str
    status: str
    detail: str


SECRET_DETAIL_PATTERNS = (
    (
        re.compile(r"(?i)(authorization\s*:\s*bearer\s+)[^,\s|]+"),
        lambda match: f"{match.group(1)}<redacted>",
    ),
    (
        re.compile(r"(?i)(x-runner-execution\s*[:=]\s*)[^,\s|]+"),
        lambda match: f"{match.group(1)}<redacted>",
    ),
    (
        re.compile(r"(?i)(--[a-z0-9-]*(?:token|secret|password|key|code)[a-z0-9-]*)\s+(\"[^\"]+\"|'[^']+'|[^,\s|]+)"),
        lambda match: f"{match.group(1)} <redacted>",
    ),
    (
        re.compile(r"(?i)\b(token|private[_-]?token|access[_-]?key|secret[_-]?key|client[_-]?secret|password|api[_-]?key|authorization)\s*[:=]\s*[^,\s|]+"),
        lambda match: f"{match.group(1)}=<redacted>",
    ),
)


def repo_root() -> Path:
    return Path(__file__).resolve().parents[1]


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


def append_arg(command: list[str], flag: str, value: str) -> None:
    if value:
        command.extend([flag, value])


def run_command(
    command: list[str],
    cwd: Path,
    timeout: int = 240,
    env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        command,
        cwd=cwd,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=timeout,
        env=env,
    )


def command_summary(result: subprocess.CompletedProcess[str]) -> str:
    output = "\n".join(part for part in [result.stdout, result.stderr] if part).strip()
    if result.returncode == 0:
        return "passed"
    return (output[-1200:] if output else f"exited {result.returncode}").replace("\n", " | ")


def run_gate(
    name: str,
    command: list[str],
    root: Path,
    timeout: int,
    env: dict[str, str] | None = None,
) -> CheckResult:
    if env is None:
        result = run_command(command, root, timeout=timeout)
    else:
        result = run_command(command, root, timeout=timeout, env=env)
    return CheckResult(name, "ok" if result.returncode == 0 else "fail", command_summary(result))


def oidc_command(args: argparse.Namespace, root: Path) -> list[str]:
    command = [sys.executable, str(root / "scripts" / "oidc_check_cli.py"), "check", "--production"]
    append_arg(command, "--issuer", env_or(args.oidc_issuer, "OIDC_ISSUER"))
    append_arg(command, "--audience", env_or(args.oidc_audience, "OIDC_AUDIENCE"))
    append_arg(command, "--jwks-url", env_or(args.oidc_jwks_url, "OIDC_JWKS_URL"))
    append_arg(command, "--discovery-url", env_or(args.oidc_discovery_url, "OIDC_DISCOVERY_URL"))
    append_arg(command, "--authorization-url", env_or(args.oidc_authorization_url, "OIDC_AUTHORIZATION_URL"))
    append_arg(command, "--token-url", env_or(args.oidc_token_url, "OIDC_TOKEN_URL"))
    append_arg(command, "--role-claim", env_or(args.oidc_role_claim, "OIDC_ROLE_CLAIM", "roles"))
    append_arg(command, "--subject-claim", env_or(args.oidc_subject_claim, "OIDC_SUBJECT_CLAIM", "sub"))
    append_arg(command, "--expected-role", env_or(args.oidc_expected_role, "OIDC_EXPECTED_ROLE"))
    append_arg(command, "--token", env_or(args.oidc_token, "OIDC_CHECK_TOKEN"))
    append_arg(command, "--api-url", env_or(args.api_url, "PACKAGING_API_URL"))
    append_arg(command, "--protected-path", args.protected_path)
    if args.broker_live:
        command.append("--broker-live")
        append_arg(command, "--broker-url", env_or(args.broker_url, "OIDC_BROKER_URL"))
        append_arg(command, "--broker-code", env_or(args.broker_code, "OIDC_BROKER_CODE"))
        append_arg(command, "--broker-code-verifier", env_or(args.broker_code_verifier, "OIDC_BROKER_CODE_VERIFIER"))
        append_arg(command, "--broker-redirect-uri", env_or(args.broker_redirect_uri, "OIDC_REDIRECT_URI"))
        if args.broker_logout:
            command.append("--broker-logout")
        if args.require_session_store:
            command.append("--require-session-store")
    if args.require_broker and not args.broker_live:
        command.append("--require-broker")
    return command


def rustfs_command(args: argparse.Namespace, root: Path) -> list[str]:
    command = [
        sys.executable,
        str(root / "scripts" / "rustfs_check.py"),
        "--production",
        "--skip-container-check",
    ]
    append_arg(command, "--endpoint", env_or(args.rustfs_endpoint, "RUSTFS_ENDPOINT"))
    append_arg(command, "--integration-endpoint", env_or(args.rustfs_integration_endpoint, "RUSTFS_INTEGRATION_ENDPOINT"))
    append_arg(command, "--remote-docker-host", args.rustfs_remote_docker_host)
    append_arg(command, "--access-key", env_or(args.rustfs_access_key, "RUSTFS_ACCESS_KEY"))
    append_arg(command, "--secret-key", env_or(args.rustfs_secret_key, "RUSTFS_SECRET_KEY"))
    append_arg(command, "--bucket", env_or(args.rustfs_bucket, "RUSTFS_BUCKET"))
    append_arg(command, "--object-lock-bucket", env_or(args.rustfs_object_lock_bucket, "RUSTFS_OBJECT_LOCK_BUCKET"))
    append_arg(command, "--retention-mode", env_or(args.rustfs_retention_mode, "RUSTFS_OBJECT_LOCK_RETENTION_MODE", "COMPLIANCE"))
    append_arg(command, "--retention-days", str(args.rustfs_retention_days or int(os.environ.get("RUSTFS_OBJECT_LOCK_RETENTION_DAYS", "3650"))))
    if args.rustfs_use_ssl:
        command.append("--use-ssl")
    command.append("--object-lock")
    if args.rustfs_legal_hold:
        command.append("--legal-hold")
    else:
        command.append("--no-legal-hold")
    return command


def runner_e2e_command(args: argparse.Namespace, root: Path) -> list[str]:
    command = [sys.executable, str(root / "scripts" / "runner_e2e_cli.py"), "check", "--live"]
    append_arg(command, "--api-url", env_or(args.api_url, "PACKAGING_API_URL"))
    append_arg(command, "--build-id", args.runner_e2e_build_id)
    append_arg(command, "--nomad-job-id", args.runner_e2e_nomad_job_id)
    append_arg(command, "--nomad-url", env_or(args.nomad_address, "NOMAD_ADDR", "http://127.0.0.1:4646"))
    return command


def runner_e2e_env(args: argparse.Namespace) -> dict[str, str]:
    env = dict(os.environ)
    for key in ("AUTH_RUNNER_TOKEN", "RUNNER_EXECUTION_HMAC_KEY", "X_RUNNER_EXECUTION", "RUNNER_EXECUTION_CAPABILITY"):
        env.pop(key, None)
    operator_token = env_or(args.runner_e2e_operator_token, "PACKAGING_OPERATOR_TOKEN")
    if operator_token:
        env["PACKAGING_OPERATOR_TOKEN"] = operator_token
    nomad_token = env_or(args.nomad_token, "NOMAD_TOKEN")
    if nomad_token:
        env["NOMAD_TOKEN"] = nomad_token
    return env


def windows_command(args: argparse.Namespace, root: Path) -> list[str]:
    command = [sys.executable, str(root / "scripts" / "windows_runner_check_cli.py"), "check", "--live"]
    append_arg(command, "--api-url", env_or(args.api_url, "PACKAGING_API_URL"))
    append_arg(command, "--packager-command", env_or(args.windows_packager_command, "WINDOWS_PACKAGER_CMD"))
    append_arg(command, "--signing-command", env_or(args.windows_signing_command, "WINDOWS_SIGNING_CMD"))
    append_arg(command, "--runner-command", env_or(args.windows_runner_command, "WINDOWS_RUNNER_COMMAND"))
    append_arg(command, "--node-class", env_or(args.windows_node_class, "NOMAD_WINDOWS_NODE_CLASS", "windows-packager"))
    append_arg(command, "--bun-cache-dir", env_or(args.windows_bun_cache_dir, "BUN_INSTALL_CACHE_DIR"))
    append_arg(command, "--artifact-root", env_or(args.windows_artifact_root, "ARTIFACT_ROOT"))
    append_arg(command, "--secrets-dir", env_or(args.windows_secrets_dir, "NOMAD_SECRETS_DIR"))
    append_arg(command, "--token-filename", env_or(args.windows_token_filename, "PACKAGING_API_TOKEN_FILE", "vela-runner-auth"))
    append_arg(command, "--execution-filename", env_or(args.windows_execution_filename, "RUNNER_EXECUTION_FILE", "vela-runner-execution"))
    append_arg(command, "--runner-env-file", args.windows_runner_env_file)
    append_arg(command, "--allocation-id", args.windows_allocation_id or os.environ.get("NOMAD_ALLOC_ID", ""))
    append_arg(command, "--build-id", args.windows_build_id)
    append_arg(command, "--nomad-job-id", args.windows_nomad_job_id)
    append_arg(command, "--task-sid", args.windows_task_sid)
    append_arg(command, "--packager-kind", args.windows_packager_kind)
    if args.require_windows_signing:
        command.append("--require-signing")
    else:
        command.append("--no-require-signing")
    return command


def windows_env(args: argparse.Namespace) -> dict[str, str]:
    env = dict(os.environ)
    for key in ("AUTH_RUNNER_TOKEN", "RUNNER_EXECUTION_HMAC_KEY", "X_RUNNER_EXECUTION", "RUNNER_EXECUTION_CAPABILITY"):
        env.pop(key, None)
    operator_token = env_or(args.windows_operator_token, "PACKAGING_OPERATOR_TOKEN")
    if operator_token:
        env["PACKAGING_OPERATOR_TOKEN"] = operator_token
    nomad_token = env_or(args.nomad_token, "NOMAD_TOKEN")
    if nomad_token:
        env["NOMAD_TOKEN"] = nomad_token
    if args.nomad_address:
        env["NOMAD_ADDR"] = args.nomad_address
    return env


def nomad_command(args: argparse.Namespace, root: Path) -> list[str]:
    command = [sys.executable, str(root / "scripts" / "nomad_check_cli.py"), "check", "--production"]
    append_arg(command, "--address", env_or(args.nomad_address, "NOMAD_ADDR", "http://127.0.0.1:4646"))
    append_arg(command, "--ca-cert", env_or(args.nomad_ca_cert, "NOMAD_CACERT"))
    append_arg(command, "--client-cert", env_or(args.nomad_client_cert, "NOMAD_CLIENT_CERT"))
    append_arg(command, "--client-key", env_or(args.nomad_client_key, "NOMAD_CLIENT_KEY"))
    append_arg(command, "--linux-node-class", env_or(args.nomad_linux_node_class, "NOMAD_LINUX_NODE_CLASS", "linux-packager"))
    append_arg(command, "--windows-node-class", env_or(args.nomad_windows_node_class, "NOMAD_WINDOWS_NODE_CLASS", "windows-packager"))
    if args.require_nomad_linux_node:
        command.append("--require-linux-node")
    if args.require_nomad_windows_node:
        command.append("--require-windows-node")
    if args.nomad_require_token:
        command.append("--require-token")
    if args.nomad_require_acl:
        command.append("--require-acl")
    if args.nomad_require_mtls:
        command.append("--require-mtls")
    if args.nomad_allow_http:
        command.append("--allow-http")
    return command


def nomad_env(args: argparse.Namespace) -> dict[str, str]:
    env = dict(os.environ)
    token = env_or(args.nomad_token, "NOMAD_TOKEN")
    if token:
        env["NOMAD_TOKEN"] = token
    return env


def artifact_retention_command(args: argparse.Namespace, root: Path) -> list[str]:
    command = [sys.executable, str(root / "scripts" / "artifact_retention_cli.py"), "check"]
    append_arg(command, "--api-url", env_or(args.api_url, "PACKAGING_API_URL", "http://127.0.0.1:8080"))
    append_arg(command, "--token", env_or(args.artifact_retention_token, "PACKAGING_API_TOKEN"))
    append_arg(command, "--max-expired-artifacts", str(args.artifact_retention_max_expired))
    append_arg(command, "--sample-expired-downloads", str(args.artifact_retention_sample_expired_downloads))
    if args.artifact_retention_skip_download_check:
        command.append("--skip-expired-download-check")
    return command


def notification_routing_command(args: argparse.Namespace, root: Path) -> list[str]:
    command = [sys.executable, str(root / "scripts" / "notification_routing_cli.py"), "check"]
    append_arg(command, "--api-url", env_or(args.api_url, "PACKAGING_API_URL", "http://127.0.0.1:8080"))
    append_arg(command, "--token", env_or(args.notification_routing_token, "PACKAGING_API_TOKEN"))
    for route in args.notification_route or []:
        append_arg(command, "--route", route)
    for severity in args.notification_require_severity or []:
        append_arg(command, "--require-severity", severity)
    append_arg(command, "--limit", str(args.notification_routing_limit))
    if args.notification_send:
        command.append("--send")
    return command


def gitlab_command(args: argparse.Namespace, root: Path) -> list[str]:
    command = [sys.executable, str(root / "scripts" / "gitlab_package_check_cli.py"), "check", "--production"]
    append_arg(command, "--base-url", env_or(args.gitlab_base_url, "GITLAB_PACKAGE_BASE_URL"))
    append_arg(command, "--token", env_or(args.gitlab_token, "GITLAB_PACKAGE_TOKEN"))
    append_arg(command, "--repository", args.gitlab_repository)
    append_arg(command, "--path", args.gitlab_path)
    append_arg(command, "--project", args.gitlab_project)
    append_arg(command, "--package", args.gitlab_package)
    append_arg(command, "--version", args.gitlab_version)
    append_arg(command, "--file", args.gitlab_file)
    append_arg(command, "--expected-sha256", env_or(args.gitlab_expected_sha256, "GITLAB_PACKAGE_EXPECTED_SHA256"))
    if args.gitlab_require_token:
        command.append("--require-token")
    return command


def skipped_gate(name: str, required: bool, flag: str) -> CheckResult:
    return CheckResult(
        name,
        "fail" if required else "warn",
        f"skipped; pass {flag} to run this live gate",
    )


def skipped_broker_gate(args: argparse.Namespace) -> CheckResult | None:
    if args.broker_live:
        return CheckResult(
            "OIDC broker live acceptance",
            "fail",
            "--broker-live requires --idp-live because broker validation runs through the production IdP live gate",
        )
    if args.require_broker:
        return CheckResult(
            "OIDC broker live acceptance",
            "fail",
            "--require-broker requires --idp-live --broker-live",
        )
    return None


def run_checks(args: argparse.Namespace) -> list[CheckResult]:
    root = repo_root()
    results: list[CheckResult] = []

    if not args.skip_local_governance:
        results.append(
            run_gate(
                "Prometheus alert rules",
                [sys.executable, str(root / "scripts" / "observability_rules_cli.py"), "check"],
                root,
                args.timeout,
            )
        )
        results.append(
            run_gate(
                "Alertmanager routing example",
                [sys.executable, str(root / "scripts" / "alertmanager_config_cli.py"), "check"],
                root,
                args.timeout,
            )
        )
        lifecycle = [sys.executable, str(root / "scripts" / "lifecycle_cli.py"), "check"]
        if args.full_conductor:
            lifecycle.append("--full-conductor")
        results.append(run_gate("Lifecycle reconciliation", lifecycle, root, args.timeout))
        results.append(
            run_gate(
                "Runner fixture golden artifacts",
                [sys.executable, str(root / "scripts" / "fixture_golden_cli.py"), "check"],
                root,
                args.timeout,
            )
        )

    if args.idp_live:
        results.append(run_gate("Production IdP live acceptance", oidc_command(args, root), root, args.timeout))
    else:
        results.append(skipped_gate("Production IdP live acceptance", args.require_idp_live, "--idp-live"))
        broker_result = skipped_broker_gate(args)
        if broker_result:
            results.append(broker_result)

    if args.object_lock_live:
        results.append(run_gate("Object-lock bucket live acceptance", rustfs_command(args, root), root, args.timeout))
    else:
        results.append(skipped_gate("Object-lock bucket live acceptance", args.require_object_lock_live, "--object-lock-live"))

    if args.gitlab_live:
        results.append(run_gate("GitLab package live acceptance", gitlab_command(args, root), root, args.timeout))
    else:
        results.append(skipped_gate("GitLab package live acceptance", args.require_gitlab_live, "--gitlab-live"))

    if args.runner_e2e_live:
        results.append(run_gate("Auth-enabled runner E2E live acceptance", runner_e2e_command(args, root), root, args.timeout, runner_e2e_env(args)))
    else:
        results.append(skipped_gate("Auth-enabled runner E2E live acceptance", args.require_runner_e2e_live, "--runner-e2e-live"))

    if args.windows_live:
        results.append(run_gate("Windows runner production readiness", windows_command(args, root), root, args.timeout, windows_env(args)))
    else:
        results.append(skipped_gate("Windows runner production readiness", args.require_windows_live, "--windows-live"))

    if args.nomad_live:
        results.append(run_gate("Nomad cluster live acceptance", nomad_command(args, root), root, args.timeout, nomad_env(args)))
    else:
        results.append(skipped_gate("Nomad cluster live acceptance", args.require_nomad_live, "--nomad-live"))

    if args.artifact_retention_live:
        results.append(run_gate("Artifact retention live acceptance", artifact_retention_command(args, root), root, args.timeout))
    else:
        results.append(
            skipped_gate(
                "Artifact retention live acceptance",
                args.require_artifact_retention_live,
                "--artifact-retention-live",
            )
        )

    if args.notification_routing_live:
        results.append(run_gate("Notification routing live acceptance", notification_routing_command(args, root), root, args.timeout))
    else:
        results.append(
            skipped_gate(
                "Notification routing live acceptance",
                args.require_notification_routing_live,
                "--notification-routing-live",
            )
        )

    return results


def result_exit_code(results: list[CheckResult]) -> int:
    return 1 if any(result.status == "fail" for result in results) else 0


def report_status(results: list[CheckResult]) -> str:
    if any(result.status == "fail" for result in results):
        return "fail"
    if any(result.status == "warn" for result in results):
        return "warn"
    return "ok"


def sanitize_detail(detail: str) -> str:
    sanitized = detail
    for pattern, replacement in SECRET_DETAIL_PATTERNS:
        sanitized = pattern.sub(replacement, sanitized)
    return sanitized


def build_report(results: list[CheckResult], exit_code: int) -> dict[str, Any]:
    summary = {
        "total": len(results),
        "ok": sum(1 for result in results if result.status == "ok"),
        "warn": sum(1 for result in results if result.status == "warn"),
        "fail": sum(1 for result in results if result.status == "fail"),
    }
    gates = []
    for result in results:
        gate = asdict(result)
        gate["detail"] = sanitize_detail(gate["detail"])
        gates.append(gate)
    return {
        "schema": "packaging.production_readiness.report/v1",
        "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "status": report_status(results),
        "exitCode": exit_code,
        "summary": summary,
        "gates": gates,
    }


def write_report_file(path: str, report: dict[str, Any]) -> None:
    report_path = Path(path)
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def print_results(results: list[CheckResult]) -> None:
    print("Confirmation lights:")
    for result in results:
        print(f"  {light(result.status)} {result.name}: {result.detail}")


def print_json(report: dict[str, Any]) -> None:
    print(json.dumps(report, ensure_ascii=False, indent=2))


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Vela production readiness gate runner.")
    subparsers = parser.add_subparsers(dest="command")
    check = subparsers.add_parser("check", help="run production readiness gates")
    check.add_argument("--timeout", type=int, default=300)
    check.add_argument("--skip-local-governance", action="store_true", help="skip local observability and lifecycle checks")
    check.add_argument("--full-conductor", action="store_true", help="make lifecycle check also run conductor go test ./...")
    check.add_argument("--json", action="store_true", help="print machine-readable JSON report")
    check.add_argument("--report-file", help="write the machine-readable JSON report to this path")

    check.add_argument("--api-url", help="conductor API URL; defaults to PACKAGING_API_URL")
    check.add_argument("--protected-path", default="/api/audit-events?limit=1")

    check.add_argument("--idp-live", action="store_true", help="run live production IdP/JWKS/conductor token acceptance")
    check.add_argument("--require-idp-live", action="store_true", help="fail if --idp-live is not supplied")
    check.add_argument("--oidc-issuer")
    check.add_argument("--oidc-audience")
    check.add_argument("--oidc-jwks-url")
    check.add_argument("--oidc-discovery-url")
    check.add_argument("--oidc-authorization-url")
    check.add_argument("--oidc-token-url")
    check.add_argument("--oidc-role-claim")
    check.add_argument("--oidc-subject-claim")
    check.add_argument("--oidc-expected-role")
    check.add_argument("--oidc-token")
    check.add_argument("--broker-live", action="store_true")
    check.add_argument("--require-broker", action="store_true")
    check.add_argument("--broker-url")
    check.add_argument("--broker-code")
    check.add_argument("--broker-code-verifier")
    check.add_argument("--broker-redirect-uri")
    check.add_argument("--broker-logout", action="store_true")
    check.add_argument("--require-session-store", action="store_true")

    check.add_argument("--object-lock-live", action="store_true", help="run live production RustFS/MinIO object-lock acceptance")
    check.add_argument("--require-object-lock-live", action="store_true", help="fail if --object-lock-live is not supplied")
    check.add_argument("--rustfs-endpoint")
    check.add_argument("--rustfs-integration-endpoint", help="RustFS endpoint reachable from the Go integration host; defaults to RUSTFS_INTEGRATION_ENDPOINT")
    check.add_argument("--rustfs-remote-docker-host", help="SSH host for a remote Docker daemon's Go integration test")
    check.add_argument("--rustfs-access-key")
    check.add_argument("--rustfs-secret-key")
    check.add_argument("--rustfs-bucket")
    check.add_argument("--rustfs-object-lock-bucket")
    check.add_argument("--rustfs-retention-mode")
    check.add_argument("--rustfs-retention-days", type=int)
    check.add_argument("--rustfs-use-ssl", action=argparse.BooleanOptionalAction, default=os.environ.get("RUSTFS_USE_SSL", "true").lower() != "false")
    check.add_argument("--rustfs-legal-hold", action=argparse.BooleanOptionalAction, default=os.environ.get("RUSTFS_OBJECT_LOCK_LEGAL_HOLD", "true").lower() != "false")

    check.add_argument("--gitlab-live", action="store_true", help="run live GitLab Generic Package Registry exact-file acceptance")
    check.add_argument("--require-gitlab-live", action="store_true", help="fail if --gitlab-live is not supplied")
    check.add_argument("--gitlab-base-url")
    check.add_argument("--gitlab-token")
    check.add_argument("--gitlab-repository", default="gitlab")
    check.add_argument("--gitlab-path", default="")
    check.add_argument("--gitlab-project", default="")
    check.add_argument("--gitlab-package", default="")
    check.add_argument("--gitlab-version", default="")
    check.add_argument("--gitlab-file", default="")
    check.add_argument("--gitlab-expected-sha256")
    check.add_argument("--gitlab-require-token", action="store_true")

    check.add_argument("--runner-e2e-live", action="store_true", help="run auth-enabled runner E2E against an actual Nomad deployment")
    check.add_argument("--require-runner-e2e-live", action="store_true", help="fail if --runner-e2e-live is not supplied")
    check.add_argument("--runner-e2e-operator-token", help="human operator/admin bearer; passed to the child only through PACKAGING_OPERATOR_TOKEN")
    check.add_argument("--runner-e2e-build-id", default="")
    check.add_argument("--runner-e2e-nomad-job-id", default="")

    check.add_argument("--windows-live", action="store_true", help="run Windows runner production host readiness")
    check.add_argument("--require-windows-live", action="store_true", help="fail if --windows-live is not supplied")
    check.add_argument("--windows-packager-command")
    check.add_argument("--windows-signing-command")
    check.add_argument("--windows-runner-command")
    check.add_argument("--windows-node-class")
    check.add_argument("--windows-bun-cache-dir")
    check.add_argument("--windows-artifact-root")
    check.add_argument("--windows-secrets-dir")
    check.add_argument("--windows-token-filename")
    check.add_argument("--windows-execution-filename")
    check.add_argument("--windows-runner-env-file")
    check.add_argument("--windows-allocation-id", help="defaults to NOMAD_ALLOC_ID when invoked inside the Windows allocation")
    check.add_argument("--windows-build-id")
    check.add_argument("--windows-nomad-job-id")
    check.add_argument("--windows-task-sid", help="Windows allocation task SID; normally sourced from protected deployment metadata")
    check.add_argument("--windows-operator-token", help="human operator/admin bearer; passed to the child only through PACKAGING_OPERATOR_TOKEN")
    check.add_argument("--windows-packager-kind", choices=["auto", "inno", "wix", "custom"], default="auto")
    check.add_argument("--require-windows-signing", action=argparse.BooleanOptionalAction, default=True)

    check.add_argument("--nomad-live", action="store_true", help="run live Nomad cluster leader/node readiness")
    check.add_argument("--require-nomad-live", action="store_true", help="fail if --nomad-live is not supplied")
    check.add_argument("--nomad-address")
    check.add_argument("--nomad-token", help="passed to the child only through NOMAD_TOKEN")
    check.add_argument("--nomad-ca-cert", help="defaults to NOMAD_CACERT")
    check.add_argument("--nomad-client-cert", help="defaults to NOMAD_CLIENT_CERT")
    check.add_argument("--nomad-client-key", help="defaults to NOMAD_CLIENT_KEY")
    check.add_argument("--nomad-linux-node-class")
    check.add_argument("--nomad-windows-node-class")
    check.add_argument("--require-nomad-linux-node", action="store_true")
    check.add_argument("--require-nomad-windows-node", action="store_true")
    check.add_argument("--nomad-require-token", action="store_true")
    check.add_argument("--nomad-require-acl", action="store_true", help="require anonymous denial and a low-privilege client token")
    check.add_argument("--nomad-require-mtls", action="store_true", help="require trusted client credentials and server-side client certificate enforcement")
    check.add_argument("--nomad-allow-http", action="store_true")

    check.add_argument("--artifact-retention-live", action="store_true", help="run live API artifact retention metadata/download enforcement acceptance")
    check.add_argument("--require-artifact-retention-live", action="store_true", help="fail if --artifact-retention-live is not supplied")
    check.add_argument("--artifact-retention-token", help="conductor bearer token for retention live checks; defaults to PACKAGING_API_TOKEN")
    check.add_argument("--artifact-retention-max-expired", type=int, default=0)
    check.add_argument("--artifact-retention-sample-expired-downloads", type=int, default=3)
    check.add_argument("--artifact-retention-skip-download-check", action="store_true")

    check.add_argument("--notification-routing-live", action="store_true", help="run live notification API routing/receiver acceptance")
    check.add_argument("--require-notification-routing-live", action="store_true", help="fail if --notification-routing-live is not supplied")
    check.add_argument("--notification-routing-token", help="conductor bearer token for notification routing checks; defaults to PACKAGING_API_TOKEN")
    check.add_argument("--notification-route", action="append", default=[], help="severity=webhookUrl; repeated and forwarded to notification_routing_cli.py")
    check.add_argument("--notification-require-severity", action="append", default=[], help="required severity route; defaults inside notification_routing_cli.py when omitted")
    check.add_argument("--notification-routing-limit", type=int, default=50)
    check.add_argument("--notification-send", action="store_true")
    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args(["check"] if len(sys.argv) == 1 else None)
    if args.command != "check":
        parser.error(f"unknown command: {args.command}")
    results = run_checks(args)
    exit_code = result_exit_code(results)
    report = build_report(results, exit_code)
    if args.report_file:
        write_report_file(args.report_file, report)
    if args.json:
        print_json(report)
    else:
        print_results(results)
    return exit_code


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(f"{light('fail')} {exc}", file=sys.stderr)
        sys.exit(1)

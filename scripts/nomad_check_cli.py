#!/usr/bin/env python3
"""Validate live Nomad cluster readiness for Packaging Platform runners."""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import asdict, dataclass
from typing import Any


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


def build_url(address: str, path: str) -> str:
    return address.rstrip("/") + path


def request_json(url: str, token: str, timeout: int) -> Any:
    headers = {"Accept": "application/json"}
    if token:
        headers["X-Nomad-Token"] = token
    request = urllib.request.Request(url, headers=headers)
    with urllib.request.urlopen(request, timeout=timeout) as response:
        payload = response.read().decode("utf-8")
        return json.loads(payload) if payload else None


def node_kernel(node: dict[str, Any]) -> str:
    attributes = node.get("Attributes")
    if not isinstance(attributes, dict):
        return ""
    return str(attributes.get("kernel.name") or "").lower()


def node_class(node: dict[str, Any]) -> str:
    meta = node.get("Meta")
    if not isinstance(meta, dict):
        return ""
    return str(meta.get("node_class") or "")


def ready_nodes(nodes: Any) -> list[dict[str, Any]]:
    if not isinstance(nodes, list):
        return []
    return [node for node in nodes if isinstance(node, dict) and str(node.get("Status") or "").lower() == "ready"]


def count_matching_nodes(nodes: list[dict[str, Any]], expected_class: str, kernel: str) -> int:
    return sum(1 for node in nodes if node_class(node) == expected_class and node_kernel(node) == kernel)


def node_class_check(name: str, nodes: list[dict[str, Any]], expected_class: str, kernel: str, required: bool) -> CheckResult:
    count = count_matching_nodes(nodes, expected_class, kernel)
    status = "ok" if count > 0 else ("fail" if required else "warn")
    return CheckResult(name, status, f"{expected_class}={count}, kernel={kernel}, required={str(required).lower()}")


def validate_inputs(args: argparse.Namespace, address: str, token: str) -> list[CheckResult]:
    results = [CheckResult("Nomad address configured", "ok" if address else "fail", address or "NOMAD_ADDR missing")]
    if args.production:
        tls_ok = address.startswith("https://")
        if tls_ok:
            results.append(CheckResult("Nomad address uses TLS", "ok", address))
        elif args.allow_http:
            results.append(CheckResult("Nomad address uses TLS", "warn", "HTTP explicitly allowed for this live check"))
        else:
            results.append(CheckResult("Nomad address uses TLS", "fail", "production Nomad live check requires HTTPS or --allow-http"))
    if args.require_token:
        results.append(CheckResult("Nomad token configured", "ok" if token else "fail", "X-Nomad-Token configured" if token else "NOMAD_TOKEN missing"))
    return results


def validate(args: argparse.Namespace) -> list[CheckResult]:
    address = env_or(args.address, "NOMAD_ADDR", "http://127.0.0.1:4646")
    token = env_or(args.token, "NOMAD_TOKEN")
    linux_node_class = env_or(args.linux_node_class, "NOMAD_LINUX_NODE_CLASS", "linux-packager")
    windows_node_class = env_or(args.windows_node_class, "NOMAD_WINDOWS_NODE_CLASS", "windows-packager")
    results = validate_inputs(args, address, token)
    if any(result.status == "fail" for result in results):
        return results

    try:
        leader = request_json(build_url(address, "/v1/status/leader"), token, args.timeout)
    except urllib.error.HTTPError as exc:
        results.append(CheckResult("Nomad leader", "fail", f"HTTP {exc.code} from Nomad"))
        return results
    except urllib.error.URLError as exc:
        results.append(CheckResult("Nomad leader", "fail", str(exc.reason)))
        return results

    leader_value = str(leader or "").strip().strip('"')
    results.append(CheckResult("Nomad leader", "ok" if leader_value else "fail", leader_value or "no leader reported"))
    if not leader_value:
        return results

    try:
        nodes_payload = request_json(build_url(address, "/v1/nodes"), token, args.timeout)
    except urllib.error.HTTPError as exc:
        results.append(CheckResult("Nomad nodes", "fail", f"HTTP {exc.code} from Nomad"))
        return results
    except urllib.error.URLError as exc:
        results.append(CheckResult("Nomad nodes", "fail", str(exc.reason)))
        return results

    nodes = ready_nodes(nodes_payload)
    total_nodes = len(nodes_payload) if isinstance(nodes_payload, list) else 0
    results.append(CheckResult("Nomad ready nodes", "ok" if nodes else "fail", f"ready={len(nodes)}, total={total_nodes}"))
    results.append(node_class_check("Nomad Linux node class", nodes, linux_node_class, "linux", args.require_linux_node))
    results.append(node_class_check("Nomad Windows node class", nodes, windows_node_class, "windows", args.require_windows_node))
    return results


def result_exit_code(results: list[CheckResult]) -> int:
    return 1 if any(result.status == "fail" for result in results) else 0


def print_results(results: list[CheckResult]) -> None:
    print("Confirmation lights:")
    for result in results:
        print(f"  {light(result.status)} {result.name}: {result.detail}")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Validate live Nomad cluster readiness for Packaging Platform.")
    subparsers = parser.add_subparsers(dest="command")
    check = subparsers.add_parser("check", help="check Nomad leader and runner node classes")
    check.add_argument("--address", help="defaults to NOMAD_ADDR or http://127.0.0.1:4646")
    check.add_argument("--token", help="defaults to NOMAD_TOKEN")
    check.add_argument("--linux-node-class", help="defaults to NOMAD_LINUX_NODE_CLASS or linux-packager")
    check.add_argument("--windows-node-class", help="defaults to NOMAD_WINDOWS_NODE_CLASS or windows-packager")
    check.add_argument("--require-linux-node", action="store_true", help="fail unless a ready Linux runner node with the expected class exists")
    check.add_argument("--require-windows-node", action="store_true", help="fail unless a ready Windows runner node with the expected class exists")
    check.add_argument("--require-token", action="store_true", help="fail if no Nomad token is configured")
    check.add_argument("--production", action="store_true", help="strict production readiness: require TLS unless --allow-http is supplied")
    check.add_argument("--allow-http", action="store_true", help="allow an HTTP Nomad address in production live checks")
    check.add_argument("--timeout", type=int, default=20)
    check.add_argument("--json", action="store_true", help="print machine-readable JSON")
    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args(["check"] if len(sys.argv) == 1 else None)
    if args.command != "check":
        parser.error(f"unknown command: {args.command}")
    results = validate(args)
    if args.json:
        print(json.dumps([asdict(result) for result in results], ensure_ascii=False, indent=2))
    else:
        print_results(results)
    return result_exit_code(results)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(f"{light('fail')} {exc}", file=sys.stderr)
        sys.exit(1)

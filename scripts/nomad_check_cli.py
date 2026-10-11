#!/usr/bin/env python3
"""Validate live Nomad cluster readiness for Vela runners."""

from __future__ import annotations

import argparse
import json
import os
import ssl
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


def request_json(url: str, token: str, timeout: int, context: ssl.SSLContext | None = None) -> Any:
    headers = {"Accept": "application/json"}
    if token:
        headers["X-Nomad-Token"] = token
    request = urllib.request.Request(url, headers=headers)
    with urllib.request.urlopen(request, timeout=timeout, context=context) as response:
        payload = response.read().decode("utf-8")
        return json.loads(payload) if payload else None


def request_status(url: str, token: str, timeout: int, context: ssl.SSLContext | None = None) -> tuple[int, str]:
    try:
        request_json(url, token, timeout, context)
        return 200, "request accepted"
    except urllib.error.HTTPError as exc:
        return exc.code, f"HTTP {exc.code}"
    except urllib.error.URLError as exc:
        return 0, str(exc.reason)
    except (ConnectionError, ssl.SSLError) as exc:
        return 0, str(exc)


def tls_context(ca_cert: str, client_cert: str = "", client_key: str = "") -> ssl.SSLContext:
    context = ssl.create_default_context(cafile=ca_cert or None)
    if client_cert or client_key:
        if not client_cert or not client_key:
            raise ValueError("both Nomad client certificate and key are required")
        context.load_cert_chain(client_cert, client_key)
    return context


def client_certificate_rejected(status: int, detail: str) -> bool:
    if status != 0:
        return False
    lowered = detail.lower()
    return any(
        marker in lowered
        for marker in (
            "certificate required",
            "bad certificate",
            "certificate unknown",
            "handshake failure",
        )
    )


def configured_file(value: str, label: str) -> CheckResult:
    if not value:
        return CheckResult(label, "fail", f"{label} path missing")
    return CheckResult(label, "ok" if os.path.isfile(value) else "fail", "configured" if os.path.isfile(value) else "file not found")


def node_kernel(node: dict[str, Any]) -> str:
    attributes = node.get("Attributes")
    if not isinstance(attributes, dict):
        return ""
    return str(attributes.get("kernel.name") or "").lower()


def node_class(node: dict[str, Any]) -> str:
    meta = node.get("Meta")
    if isinstance(meta, dict) and meta.get("node_class"):
        return str(meta["node_class"])
    return str(node.get("NodeClass") or "")


def ready_nodes(nodes: Any) -> list[dict[str, Any]]:
    if not isinstance(nodes, list):
        return []
    return [node for node in nodes if isinstance(node, dict) and str(node.get("Status") or "").lower() == "ready"]


def hydrate_nodes(
    address: str,
    token: str,
    timeout: int,
    context: ssl.SSLContext | None,
    nodes: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    hydrated: list[dict[str, Any]] = []
    for node in nodes:
        if isinstance(node.get("Attributes"), dict) and (isinstance(node.get("Meta"), dict) or node.get("NodeClass")):
            hydrated.append(node)
            continue
        node_id = str(node.get("ID") or "")
        if not node_id:
            hydrated.append(node)
            continue
        detail = request_json(build_url(address, f"/v1/node/{urllib.parse.quote(node_id, safe='')}"), token, timeout, context)
        hydrated.append(detail if isinstance(detail, dict) else node)
    return hydrated


def count_matching_nodes(nodes: list[dict[str, Any]], expected_class: str, kernel: str) -> int:
    return sum(1 for node in nodes if node_class(node) == expected_class and node_kernel(node) == kernel)


def node_class_check(name: str, nodes: list[dict[str, Any]], expected_class: str, kernel: str, required: bool) -> CheckResult:
    count = count_matching_nodes(nodes, expected_class, kernel)
    status = "ok" if count > 0 else ("fail" if required else "warn")
    return CheckResult(name, status, f"{expected_class}={count}, kernel={kernel}, required={str(required).lower()}")


def validate_inputs(
    args: argparse.Namespace,
    address: str,
    token: str,
    ca_cert: str,
    client_cert: str,
    client_key: str,
) -> list[CheckResult]:
    results = [CheckResult("Nomad address configured", "ok" if address else "fail", address or "NOMAD_ADDR missing")]
    if args.production:
        tls_ok = address.startswith("https://")
        if tls_ok:
            results.append(CheckResult("Nomad address uses TLS", "ok", address))
        elif args.allow_http:
            results.append(CheckResult("Nomad address uses TLS", "warn", "HTTP explicitly allowed for this live check"))
        else:
            results.append(CheckResult("Nomad address uses TLS", "fail", "production Nomad live check requires HTTPS or --allow-http"))
    if args.require_token or args.require_acl:
        results.append(CheckResult("Nomad token configured", "ok" if token else "fail", "X-Nomad-Token configured" if token else "NOMAD_TOKEN missing"))
    if args.require_acl:
        results.append(CheckResult("Nomad ACL enforcement requested", "ok", "anonymous denial and low-privilege token checks enabled"))
    if args.require_mtls:
        if not address.startswith("https://"):
            results.append(CheckResult("Nomad mTLS endpoint", "fail", "--require-mtls requires an HTTPS Nomad address"))
        results.extend(
            [
                configured_file(ca_cert, "Nomad CA certificate"),
                configured_file(client_cert, "Nomad client certificate"),
                configured_file(client_key, "Nomad client key"),
            ]
        )
    return results


def validate(args: argparse.Namespace) -> list[CheckResult]:
    address = env_or(args.address, "NOMAD_ADDR", "http://127.0.0.1:4646")
    token = env_or(args.token, "NOMAD_TOKEN")
    ca_cert = env_or(args.ca_cert, "NOMAD_CACERT")
    client_cert = env_or(args.client_cert, "NOMAD_CLIENT_CERT")
    client_key = env_or(args.client_key, "NOMAD_CLIENT_KEY")
    linux_node_class = env_or(args.linux_node_class, "NOMAD_LINUX_NODE_CLASS", "linux-packager")
    windows_node_class = env_or(args.windows_node_class, "NOMAD_WINDOWS_NODE_CLASS", "windows-packager")
    results = validate_inputs(args, address, token, ca_cert, client_cert, client_key)
    if any(result.status == "fail" for result in results):
        return results

    context = tls_context(ca_cert, client_cert, client_key) if address.startswith("https://") else None

    try:
        leader = request_json(build_url(address, "/v1/status/leader"), token, args.timeout, context)
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
        nodes_payload = request_json(build_url(address, "/v1/nodes"), token, args.timeout, context)
    except urllib.error.HTTPError as exc:
        results.append(CheckResult("Nomad nodes", "fail", f"HTTP {exc.code} from Nomad"))
        return results
    except urllib.error.URLError as exc:
        results.append(CheckResult("Nomad nodes", "fail", str(exc.reason)))
        return results

    nodes = ready_nodes(nodes_payload)
    total_nodes = len(nodes_payload) if isinstance(nodes_payload, list) else 0
    try:
        nodes = hydrate_nodes(address, token, args.timeout, context, nodes)
    except urllib.error.HTTPError as exc:
        results.append(CheckResult("Nomad node details", "fail", f"HTTP {exc.code} from Nomad"))
        return results
    except urllib.error.URLError as exc:
        results.append(CheckResult("Nomad node details", "fail", str(exc.reason)))
        return results
    results.append(CheckResult("Nomad ready nodes", "ok" if nodes else "fail", f"ready={len(nodes)}, total={total_nodes}"))
    results.append(node_class_check("Nomad Linux node class", nodes, linux_node_class, "linux", args.require_linux_node))
    results.append(node_class_check("Nomad Windows node class", nodes, windows_node_class, "windows", args.require_windows_node))

    if args.require_acl:
        try:
            token_self = request_json(build_url(address, "/v1/acl/token/self"), token, args.timeout, context)
            token_type = str(token_self.get("Type") or "") if isinstance(token_self, dict) else ""
            low_privilege = token_type == "client"
            results.append(
                CheckResult(
                    "Nomad low-privilege ACL token",
                    "ok" if low_privilege else "fail",
                    "client token accepted" if low_privilege else "acceptance token must be a non-management client token",
                )
            )
        except urllib.error.HTTPError as exc:
            results.append(CheckResult("Nomad low-privilege ACL token", "fail", f"HTTP {exc.code} from token self endpoint"))
        except urllib.error.URLError as exc:
            results.append(CheckResult("Nomad low-privilege ACL token", "fail", str(exc.reason)))

        anonymous_status, anonymous_detail = request_status(build_url(address, "/v1/nodes"), "", args.timeout, context)
        anonymous_denied = anonymous_status in (401, 403)
        results.append(
            CheckResult(
                "Nomad anonymous ACL denial",
                "ok" if anonymous_denied else "fail",
                "anonymous node read denied" if anonymous_denied else f"anonymous node read was not denied ({anonymous_detail})",
            )
        )
        policy_status, policy_detail = request_status(build_url(address, "/v1/acl/tokens"), token, args.timeout, context)
        policy_denied = policy_status in (401, 403)
        results.append(
            CheckResult(
                "Nomad ACL administration denial",
                "ok" if policy_denied else "fail",
                "client token cannot list ACL tokens" if policy_denied else f"client token ACL administration was not denied ({policy_detail})",
            )
        )

    if args.require_mtls:
        anonymous_tls_context = tls_context(ca_cert)
        no_cert_status, no_cert_detail = request_status(
            build_url(address, "/v1/status/leader"),
            token,
            args.timeout,
            anonymous_tls_context,
        )
        mtls_enforced = client_certificate_rejected(no_cert_status, no_cert_detail)
        results.append(
            CheckResult(
                "Nomad client certificate enforcement",
                "ok" if mtls_enforced else "fail",
                "TLS handshake rejected without a client certificate"
                if mtls_enforced
                else f"request without a client certificate was not rejected specifically by TLS ({no_cert_detail})",
            )
        )
    return results


def result_exit_code(results: list[CheckResult]) -> int:
    return 1 if any(result.status == "fail" for result in results) else 0


def print_results(results: list[CheckResult]) -> None:
    print("Confirmation lights:")
    for result in results:
        print(f"  {light(result.status)} {result.name}: {result.detail}")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Validate live Nomad cluster readiness for Vela.")
    subparsers = parser.add_subparsers(dest="command")
    check = subparsers.add_parser("check", help="check Nomad leader and runner node classes")
    check.add_argument("--address", help="defaults to NOMAD_ADDR or http://127.0.0.1:4646")
    check.add_argument("--token", help="defaults to NOMAD_TOKEN")
    check.add_argument("--ca-cert", help="CA certificate path; defaults to NOMAD_CACERT")
    check.add_argument("--client-cert", help="mTLS client certificate path; defaults to NOMAD_CLIENT_CERT")
    check.add_argument("--client-key", help="mTLS client key path; defaults to NOMAD_CLIENT_KEY")
    check.add_argument("--linux-node-class", help="defaults to NOMAD_LINUX_NODE_CLASS or linux-packager")
    check.add_argument("--windows-node-class", help="defaults to NOMAD_WINDOWS_NODE_CLASS or windows-packager")
    check.add_argument("--require-linux-node", action="store_true", help="fail unless a ready Linux runner node with the expected class exists")
    check.add_argument("--require-windows-node", action="store_true", help="fail unless a ready Windows runner node with the expected class exists")
    check.add_argument("--require-token", action="store_true", help="fail if no Nomad token is configured")
    check.add_argument("--require-acl", action="store_true", help="require enforced ACLs, a low-privilege client token, and denied ACL administration")
    check.add_argument("--require-mtls", action="store_true", help="require trusted client credentials and reject requests without a client certificate")
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

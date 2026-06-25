#!/usr/bin/env python3
"""Validate Packaging Platform OIDC/JWKS configuration."""

from __future__ import annotations

import argparse
import base64
import json
import os
import sys
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Any


DEFAULT_PROTECTED_PATH = "/api/audit-events?limit=1"


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


def fetch_json(url: str, timeout: int = 10) -> Any:
    request = urllib.request.Request(url, headers={"Accept": "application/json"})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        payload = response.read().decode("utf-8")
    return json.loads(payload)


def post_json(url: str, payload: dict[str, Any], token: str = "", timeout: int = 10) -> tuple[int, Any]:
    headers = {"Accept": "application/json", "Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    request = urllib.request.Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers=headers,
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=timeout) as response:
        raw = response.read().decode("utf-8")
        return response.status, json.loads(raw or "{}")


def discovery_url_for_issuer(issuer: str) -> str:
    return issuer.rstrip("/") + "/.well-known/openid-configuration"


def validate_https_urls(require_https: bool, entries: dict[str, str]) -> list[CheckResult]:
    if not require_https:
        return []
    results: list[CheckResult] = []
    for name, value in entries.items():
        if not value:
            continue
        results.append(
            CheckResult(
                f"{name} uses HTTPS",
                "ok" if value.startswith("https://") else "fail",
                value,
            )
        )
    return results


def validate_discovery(discovery: Any, issuer: str, jwks_url: str, authorization_url: str, token_url: str) -> list[CheckResult]:
    if not isinstance(discovery, dict):
        return [CheckResult("OIDC discovery shape", "fail", "discovery document is not a JSON object")]
    results = [
        CheckResult(
            "OIDC discovery issuer",
            "ok" if discovery.get("issuer") == issuer else "fail",
            f"issuer={discovery.get('issuer') or '<missing>'}",
        ),
        CheckResult(
            "OIDC discovery JWKS URL",
            "ok" if not jwks_url or discovery.get("jwks_uri") == jwks_url else "fail",
            f"jwks_uri={discovery.get('jwks_uri') or '<missing>'}",
        ),
    ]
    if authorization_url:
        results.append(
            CheckResult(
                "OIDC discovery authorization endpoint",
                "ok" if discovery.get("authorization_endpoint") == authorization_url else "fail",
                f"authorization_endpoint={discovery.get('authorization_endpoint') or '<missing>'}",
            )
        )
    if token_url:
        results.append(
            CheckResult(
                "OIDC discovery token endpoint",
                "ok" if discovery.get("token_endpoint") == token_url else "fail",
                f"token_endpoint={discovery.get('token_endpoint') or '<missing>'}",
            )
        )
    methods = discovery.get("code_challenge_methods_supported")
    if isinstance(methods, list):
        results.append(
            CheckResult(
                "OIDC PKCE S256 support",
                "ok" if "S256" in methods else "warn",
                ",".join(str(method) for method in methods) or "<empty>",
            )
        )
    else:
        results.append(CheckResult("OIDC PKCE S256 support", "warn", "discovery document omits code_challenge_methods_supported"))
    return results


def decode_jwt_segment(segment: str) -> dict[str, Any]:
    padded = segment + "=" * (-len(segment) % 4)
    payload = base64.urlsafe_b64decode(padded.encode("ascii"))
    decoded = json.loads(payload.decode("utf-8"))
    if not isinstance(decoded, dict):
        raise ValueError("JWT segment is not a JSON object")
    return decoded


def decode_jwt(token: str) -> tuple[dict[str, Any], dict[str, Any]]:
    parts = token.split(".")
    if len(parts) != 3:
        raise ValueError("JWT must have header, payload, and signature segments")
    return decode_jwt_segment(parts[0]), decode_jwt_segment(parts[1])


def claim_values(value: Any) -> list[str]:
    if isinstance(value, str):
        return [part for part in value.replace(",", " ").split() if part]
    if isinstance(value, list):
        return [item.strip() for item in value if isinstance(item, str) and item.strip()]
    return []


def audience_values(value: Any) -> list[str]:
    if isinstance(value, str):
        return [value]
    if isinstance(value, list):
        return [item for item in value if isinstance(item, str)]
    return []


def validate_jwks(jwks: Any) -> list[CheckResult]:
    results: list[CheckResult] = []
    keys = jwks.get("keys") if isinstance(jwks, dict) else None
    if not isinstance(keys, list):
        return [CheckResult("JWKS shape", "fail", "missing keys array")]
    rsa_keys = [
        key
        for key in keys
        if isinstance(key, dict)
        and key.get("kty") == "RSA"
        and key.get("n")
        and key.get("e")
        and (not key.get("use") or key.get("use") == "sig")
        and (not key.get("alg") or key.get("alg") == "RS256")
    ]
    results.append(
        CheckResult(
            "JWKS RS256 signing keys",
            "ok" if rsa_keys else "fail",
            f"{len(rsa_keys)} usable RSA signing key(s)",
        )
    )
    kids = [str(key.get("kid")) for key in rsa_keys if key.get("kid")]
    results.append(
        CheckResult(
            "JWKS key ids",
            "ok" if kids else "warn",
            ",".join(kids) if kids else "no kid values; backend can only use unambiguous key sets",
        )
    )
    return results


def validate_token_claims(
    token: str,
    issuer: str,
    audience: str,
    role_claim: str,
    expected_role: str,
    subject_claim: str,
) -> list[CheckResult]:
    results: list[CheckResult] = []
    now = int(time.time())
    header, claims = decode_jwt(token)
    results.append(
        CheckResult(
            "JWT algorithm",
            "ok" if header.get("alg") == "RS256" else "fail",
            f"alg={header.get('alg') or '<missing>'}",
        )
    )
    results.append(
        CheckResult(
            "JWT issuer",
            "ok" if claims.get("iss") == issuer else "fail",
            f"iss={claims.get('iss') or '<missing>'}",
        )
    )
    audiences = audience_values(claims.get("aud"))
    results.append(
        CheckResult(
            "JWT audience",
            "ok" if audience in audiences else "fail",
            f"aud={','.join(audiences) or '<missing>'}",
        )
    )
    roles = claim_values(claims.get(role_claim))
    results.append(
        CheckResult(
            "JWT role claim",
            "ok" if not expected_role or expected_role in roles else "fail",
            f"{role_claim}={','.join(roles) or '<missing>'}",
        )
    )
    subject = claims.get(subject_claim)
    results.append(
        CheckResult(
            "JWT subject claim",
            "ok" if isinstance(subject, str) and subject.strip() else "fail",
            f"{subject_claim}={subject or '<missing>'}",
        )
    )
    if "exp" not in claims:
        results.append(CheckResult("JWT expiry", "fail", "exp claim is required by backend"))
    else:
        exp = claims.get("exp")
        results.append(
            CheckResult(
                "JWT expiry active",
                "ok" if isinstance(exp, int) and exp > now else "fail",
                f"exp={exp if exp is not None else '<missing>'}",
            )
        )
    if "nbf" in claims:
        nbf = claims.get("nbf")
        results.append(
            CheckResult(
                "JWT not-before active",
                "ok" if isinstance(nbf, int) and nbf <= now else "fail",
                f"nbf={nbf}",
            )
        )
    return results


def protected_api_check(api_url: str, token: str, path: str = DEFAULT_PROTECTED_PATH) -> CheckResult:
    url = api_url.rstrip("/") + path
    request = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}", "Accept": "application/json"})
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            return CheckResult("Backend OIDC acceptance", "ok", f"HTTP {response.status} from {path}")
    except urllib.error.HTTPError as exc:
        status = "fail" if exc.code in {401, 403} else "warn"
        return CheckResult("Backend OIDC acceptance", status, f"HTTP {exc.code} from {path}")


def protected_api_rejection_check(api_url: str, token: str, path: str = DEFAULT_PROTECTED_PATH) -> CheckResult:
    url = api_url.rstrip("/") + path
    request = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}", "Accept": "application/json"})
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            return CheckResult("Broker session revocation", "fail", f"token still accepted with HTTP {response.status} from {path}")
    except urllib.error.HTTPError as exc:
        if exc.code in {401, 403}:
            return CheckResult("Broker session revocation", "ok", f"revoked token rejected with HTTP {exc.code}")
        return CheckResult("Broker session revocation", "warn", f"unexpected HTTP {exc.code} from {path}")


def broker_endpoint(api_url: str, broker_url: str) -> str:
    if broker_url:
        return broker_url
    return api_url.rstrip("/") + "/api/auth/oidc/token"


def logout_check(api_url: str, token: str, require_session_store: bool) -> CheckResult:
    url = api_url.rstrip("/") + "/api/auth/logout"
    try:
        status, payload = post_json(url, {}, token=token)
    except urllib.error.HTTPError as exc:
        return CheckResult("Broker logout", "fail", f"HTTP {exc.code} from /api/auth/logout")
    revoked = bool(payload.get("revoked")) if isinstance(payload, dict) else False
    if status < 200 or status >= 300:
        return CheckResult("Broker logout", "fail", f"HTTP {status} from /api/auth/logout")
    if revoked:
        return CheckResult("Broker logout", "ok", "persisted platform session revoked")
    status_name = "fail" if require_session_store else "warn"
    return CheckResult("Broker logout", status_name, "logout succeeded but session was not revoked")


def validate_broker_live(args: argparse.Namespace) -> list[CheckResult]:
    missing = []
    api_url = getattr(args, "api_url", "")
    broker_url = getattr(args, "broker_url", "")
    broker_code = getattr(args, "broker_code", "")
    broker_code_verifier = getattr(args, "broker_code_verifier", "")
    broker_redirect_uri = getattr(args, "broker_redirect_uri", "")
    protected_path = getattr(args, "protected_path", DEFAULT_PROTECTED_PATH)
    broker_logout = bool(getattr(args, "broker_logout", False))
    require_session_store = bool(getattr(args, "require_session_store", False))
    if not api_url:
        missing.append("--api-url")
    if not broker_code:
        missing.append("--broker-code")
    if not broker_code_verifier:
        missing.append("--broker-code-verifier")
    if not broker_redirect_uri:
        missing.append("--broker-redirect-uri")
    if missing:
        return [CheckResult("OIDC broker live inputs", "fail", "missing " + ", ".join(missing))]

    endpoint = broker_endpoint(api_url, broker_url)
    try:
        status, payload = post_json(
            endpoint,
            {
                "code": broker_code,
                "codeVerifier": broker_code_verifier,
                "redirectUri": broker_redirect_uri,
            },
        )
    except urllib.error.HTTPError as exc:
        return [CheckResult("OIDC broker token exchange", "fail", f"HTTP {exc.code} from {endpoint}")]
    except urllib.error.URLError as exc:
        return [CheckResult("OIDC broker token exchange", "fail", str(exc.reason))]

    if status < 200 or status >= 300 or not isinstance(payload, dict):
        return [CheckResult("OIDC broker token exchange", "fail", f"HTTP {status} from {endpoint}")]
    token = str(payload.get("token") or "")
    role = str(payload.get("role") or "")
    subject = str(payload.get("subject") or "")
    session_id = str(payload.get("sessionId") or "")
    results = [
        CheckResult(
            "OIDC broker token exchange",
            "ok" if token and role and subject else "fail",
            f"role={role or '<missing>'} subject={subject or '<missing>'}",
        )
    ]
    if require_session_store:
        results.append(
            CheckResult(
                "OIDC broker persisted session",
                "ok" if session_id else "fail",
                f"sessionId={session_id or '<missing>'}",
            )
        )
    if token:
        results.append(protected_api_check(api_url, token, protected_path))
        if broker_logout:
            results.append(logout_check(api_url, token, require_session_store))
            if require_session_store:
                results.append(protected_api_rejection_check(api_url, token, protected_path))
    return results


def validate_production_inputs(
    args: argparse.Namespace,
    authorization_url: str,
    token_url: str,
    discovery_url: str,
) -> list[CheckResult]:
    if not args.production:
        return []
    return [
        CheckResult(
            "Production discovery URL",
            "ok" if discovery_url else "fail",
            discovery_url or "--discovery-url or OIDC_DISCOVERY_URL is required in --production mode",
        ),
        CheckResult(
            "Production authorization endpoint",
            "ok" if authorization_url else "fail",
            authorization_url or "--authorization-url or OIDC_AUTHORIZATION_URL is required in --production mode",
        ),
        CheckResult(
            "Production token endpoint",
            "ok" if token_url else "fail",
            token_url or "--token-url or OIDC_TOKEN_URL is required in --production mode",
        ),
        CheckResult(
            "Production expected role",
            "ok" if args.expected_role else "fail",
            args.expected_role or "--expected-role or the expected external role value is required in --production mode",
        ),
        CheckResult(
            "Production backend live API",
            "ok" if args.api_url else "fail",
            args.api_url or "--api-url is required in --production mode",
        ),
    ]


def validate(args: argparse.Namespace) -> list[CheckResult]:
    issuer = env_or(args.issuer, "OIDC_ISSUER")
    audience = env_or(args.audience, "OIDC_AUDIENCE")
    jwks_url = env_or(args.jwks_url, "OIDC_JWKS_URL")
    authorization_url = env_or(args.authorization_url, "OIDC_AUTHORIZATION_URL")
    token_url = env_or(args.token_url, "OIDC_TOKEN_URL")
    discovery_url = env_or(args.discovery_url, "OIDC_DISCOVERY_URL")
    role_claim = env_or(args.role_claim, "OIDC_ROLE_CLAIM", "roles")
    subject_claim = env_or(args.subject_claim, "OIDC_SUBJECT_CLAIM", "sub")
    token = env_or(args.token, "OIDC_CHECK_TOKEN")
    require_https = bool(args.require_https or args.production)

    results = [
        CheckResult("OIDC issuer configured", "ok" if issuer else "fail", issuer or "OIDC_ISSUER missing"),
        CheckResult("OIDC audience configured", "ok" if audience else "fail", audience or "OIDC_AUDIENCE missing"),
        CheckResult("OIDC JWKS URL configured", "ok" if jwks_url else "fail", jwks_url or "OIDC_JWKS_URL missing"),
    ]
    if issuer and not discovery_url and args.production:
        discovery_url = discovery_url_for_issuer(issuer)
    results.extend(validate_production_inputs(args, authorization_url, token_url, discovery_url))
    results.extend(
        validate_https_urls(
            require_https,
            {
                "OIDC issuer": issuer,
                "OIDC JWKS URL": jwks_url,
                "OIDC authorization URL": authorization_url,
                "OIDC token URL": token_url,
                "OIDC discovery URL": discovery_url,
                "Backend API URL": args.api_url or "",
            },
        )
    )
    if discovery_url:
        discovery = fetch_json(discovery_url)
        results.extend(validate_discovery(discovery, issuer, jwks_url, authorization_url, token_url))
    if jwks_url:
        results.extend(validate_jwks(fetch_json(jwks_url)))
    if token:
        results.extend(validate_token_claims(token, issuer, audience, role_claim, args.expected_role, subject_claim))
        if args.api_url:
            results.append(protected_api_check(args.api_url, token, args.protected_path))
        elif args.production:
            results.append(CheckResult("Backend live acceptance configured", "fail", "--api-url is required in --production mode"))
    else:
        status = "fail" if args.production else "warn"
        results.append(CheckResult("JWT token provided", status, "set OIDC_CHECK_TOKEN or --token to validate token claims"))
    if getattr(args, "broker_live", False):
        results.extend(validate_broker_live(args))
    elif getattr(args, "require_broker", False):
        results.append(CheckResult("OIDC broker live acceptance", "fail", "--broker-live is required when --require-broker is set"))
    return results


def result_exit_code(results: list[CheckResult]) -> int:
    return 1 if any(result.status == "fail" for result in results) else 0


def print_results(results: list[CheckResult]) -> None:
    print("Confirmation lights:")
    for result in results:
        print(f"  {light(result.status)} {result.name}: {result.detail}")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Validate Packaging Platform OIDC/JWKS configuration.")
    subparsers = parser.add_subparsers(dest="command")
    check = subparsers.add_parser("check", help="check OIDC configuration and optional backend token acceptance")
    check.add_argument("--issuer", help="OIDC issuer; defaults to OIDC_ISSUER")
    check.add_argument("--audience", help="OIDC audience; defaults to OIDC_AUDIENCE")
    check.add_argument("--jwks-url", help="OIDC JWKS URL; defaults to OIDC_JWKS_URL")
    check.add_argument("--discovery-url", help="OIDC discovery URL; defaults to OIDC_DISCOVERY_URL or issuer/.well-known/openid-configuration in --production mode")
    check.add_argument("--authorization-url", help="OIDC authorization endpoint expected in discovery; defaults to OIDC_AUTHORIZATION_URL")
    check.add_argument("--token-url", help="OIDC token endpoint expected in discovery; defaults to OIDC_TOKEN_URL")
    check.add_argument("--role-claim", help="role claim name; defaults to OIDC_ROLE_CLAIM or roles")
    check.add_argument("--subject-claim", help="subject claim name; defaults to OIDC_SUBJECT_CLAIM or sub")
    check.add_argument("--expected-role", default="", help="external role value expected in the token")
    check.add_argument("--token", help="JWT access token; defaults to OIDC_CHECK_TOKEN")
    check.add_argument("--api-url", help="backend API base URL for live acceptance, for example http://127.0.0.1:8080")
    check.add_argument("--protected-path", default=DEFAULT_PROTECTED_PATH, help="protected backend path used for live acceptance")
    check.add_argument("--require-https", action="store_true", help="fail non-HTTPS OIDC/API URLs")
    check.add_argument("--production", action="store_true", help="strict production readiness: HTTPS, discovery, token, and backend live acceptance are required")
    check.add_argument("--broker-live", action="store_true", help="also live-test the optional server-side OIDC code+PKCE broker")
    check.add_argument("--broker-url", help="broker endpoint; defaults to API_URL/api/auth/oidc/token")
    check.add_argument("--broker-code", help="one-time authorization code for broker live acceptance")
    check.add_argument("--broker-code-verifier", help="PKCE code verifier matching the authorization code")
    check.add_argument("--broker-redirect-uri", help="redirect URI used when the authorization code was issued")
    check.add_argument("--broker-logout", action="store_true", help="call POST /api/auth/logout with the issued platform session token")
    check.add_argument("--require-broker", action="store_true", help="fail unless --broker-live is supplied")
    check.add_argument("--require-session-store", action="store_true", help="treat broker logout without persisted session revocation as failure")
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

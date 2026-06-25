import base64
import json
import pathlib
import sys
import time
import unittest
from argparse import Namespace
from http.server import BaseHTTPRequestHandler, HTTPServer
from threading import Thread
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import oidc_check_cli


def jwt_segment(payload):
    raw = json.dumps(payload, separators=(",", ":")).encode("utf-8")
    return base64.urlsafe_b64encode(raw).decode("ascii").rstrip("=")


def make_token(claims, header=None):
    header = header or {"alg": "RS256", "kid": "key-1"}
    return f"{jwt_segment(header)}.{jwt_segment(claims)}.signature"


class JSONHandler(BaseHTTPRequestHandler):
    jwks_body = {"keys": [{"kty": "RSA", "kid": "key-1", "use": "sig", "alg": "RS256", "n": "AQ", "e": "AQAB"}]}
    discovery_body = {}
    api_status = 200
    api_status_after_logout = None
    broker_status = 200
    broker_body = {"token": "pps_session_token", "role": "operator", "subject": "oidc:alice", "sessionId": "ses_1"}
    logout_body = {"status": "ok", "sessionId": "ses_1", "revoked": True}

    def do_GET(self):
        if self.path == "/jwks":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps(self.jwks_body).encode("utf-8"))
            return
        if self.path == "/.well-known/openid-configuration":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps(self.discovery_body).encode("utf-8"))
            return
        self.send_response(self.api_status)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b"{}")

    def do_POST(self):
        if self.path == "/api/auth/oidc/token":
            _ = self.rfile.read(int(self.headers.get("Content-Length", "0") or "0"))
            self.send_response(self.broker_status)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps(self.broker_body).encode("utf-8"))
            return
        if self.path == "/api/auth/logout":
            _ = self.rfile.read(int(self.headers.get("Content-Length", "0") or "0"))
            if self.api_status_after_logout is not None:
                type(self).api_status = self.api_status_after_logout
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps(self.logout_body).encode("utf-8"))
            return
        self.send_response(404)
        self.end_headers()

    def log_message(self, format, *args):
        return


class OIDCCheckCliTest(unittest.TestCase):
    def setUp(self):
        JSONHandler.discovery_body = {}
        JSONHandler.api_status = 200
        JSONHandler.api_status_after_logout = None
        JSONHandler.broker_status = 200
        JSONHandler.broker_body = {"token": "pps_session_token", "role": "operator", "subject": "oidc:alice", "sessionId": "ses_1"}
        JSONHandler.logout_body = {"status": "ok", "sessionId": "ses_1", "revoked": True}
        self.server = HTTPServer(("127.0.0.1", 0), JSONHandler)
        self.thread = Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.base_url = f"http://127.0.0.1:{self.server.server_port}"

    def tearDown(self):
        self.server.shutdown()
        self.thread.join(timeout=5)
        self.server.server_close()

    def test_jwks_validation_accepts_rs256_signing_keys(self):
        results = oidc_check_cli.validate_jwks(JSONHandler.jwks_body)

        self.assertEqual(oidc_check_cli.result_exit_code(results), 0, results)
        self.assertTrue(any(result.name == "JWKS RS256 signing keys" and result.status == "ok" for result in results))

    def test_token_claim_validation_checks_issuer_audience_role_and_subject(self):
        token = make_token(
            {
                "iss": "https://issuer.example.test",
                "aud": ["packaging-platform", "other"],
                "sub": "alice",
                "groups": ["packaging-operator"],
                "exp": 1999999999,
            }
        )

        results = oidc_check_cli.validate_token_claims(
            token,
            issuer="https://issuer.example.test",
            audience="packaging-platform",
            role_claim="groups",
            expected_role="packaging-operator",
            subject_claim="sub",
        )

        self.assertEqual(oidc_check_cli.result_exit_code(results), 0, results)

    def test_token_claim_validation_rejects_expired_or_future_nbf_tokens(self):
        now = int(time.time())
        expired_token = make_token(
            {
                "iss": "https://issuer.example.test",
                "aud": "packaging-platform",
                "sub": "alice",
                "roles": "operator",
                "exp": now - 60,
            }
        )
        future_token = make_token(
            {
                "iss": "https://issuer.example.test",
                "aud": "packaging-platform",
                "sub": "alice",
                "roles": "operator",
                "exp": now + 3600,
                "nbf": now + 600,
            }
        )

        expired_results = oidc_check_cli.validate_token_claims(
            expired_token,
            issuer="https://issuer.example.test",
            audience="packaging-platform",
            role_claim="roles",
            expected_role="operator",
            subject_claim="sub",
        )
        future_results = oidc_check_cli.validate_token_claims(
            future_token,
            issuer="https://issuer.example.test",
            audience="packaging-platform",
            role_claim="roles",
            expected_role="operator",
            subject_claim="sub",
        )

        self.assertTrue(any(result.name == "JWT expiry active" and result.status == "fail" for result in expired_results))
        self.assertTrue(any(result.name == "JWT not-before active" and result.status == "fail" for result in future_results))

    def test_missing_config_fails(self):
        args = Namespace(
            issuer="",
            audience="",
            jwks_url="",
            discovery_url="",
            authorization_url="",
            token_url="",
            role_claim="roles",
            subject_claim="sub",
            expected_role="",
            token="",
            api_url="",
            protected_path=oidc_check_cli.DEFAULT_PROTECTED_PATH,
            require_https=False,
            production=False,
        )

        with patch.dict("os.environ", {}, clear=True):
            results = oidc_check_cli.validate(args)

        self.assertEqual(oidc_check_cli.result_exit_code(results), 1)
        self.assertTrue(any(result.name == "OIDC issuer configured" and result.status == "fail" for result in results))

    def test_live_backend_acceptance_marks_401_as_failure(self):
        JSONHandler.api_status = 401
        try:
            result = oidc_check_cli.protected_api_check(self.base_url, "token")
        finally:
            JSONHandler.api_status = 200

        self.assertEqual(result.status, "fail")
        self.assertIn("HTTP 401", result.detail)

    def test_validate_combines_jwks_and_token_checks(self):
        token = make_token(
            {
                "iss": "https://issuer.example.test",
                "aud": "packaging-platform",
                "sub": "reader",
                "roles": "viewer",
                "exp": 1999999999,
            }
        )
        args = Namespace(
            issuer="https://issuer.example.test",
            audience="packaging-platform",
            jwks_url=f"{self.base_url}/jwks",
            discovery_url="",
            authorization_url="",
            token_url="",
            role_claim="roles",
            subject_claim="sub",
            expected_role="viewer",
            token=token,
            api_url=self.base_url,
            protected_path=oidc_check_cli.DEFAULT_PROTECTED_PATH,
            require_https=False,
            production=False,
        )

        results = oidc_check_cli.validate(args)

        self.assertEqual(oidc_check_cli.result_exit_code(results), 0, results)
        self.assertTrue(any(result.name == "Backend OIDC acceptance" and result.status == "ok" for result in results))

    def test_production_mode_checks_discovery_and_https(self):
        issuer = self.base_url
        JSONHandler.discovery_body = {
            "issuer": issuer,
            "jwks_uri": f"{self.base_url}/jwks",
            "authorization_endpoint": f"{self.base_url}/auth",
            "token_endpoint": f"{self.base_url}/token",
            "code_challenge_methods_supported": ["S256"],
        }
        token = make_token(
            {
                "iss": issuer,
                "aud": "packaging-platform",
                "sub": "reader",
                "roles": "viewer",
                "exp": 1999999999,
            }
        )
        args = Namespace(
            issuer=issuer,
            audience="packaging-platform",
            jwks_url=f"{self.base_url}/jwks",
            discovery_url=f"{self.base_url}/.well-known/openid-configuration",
            authorization_url=f"{self.base_url}/auth",
            token_url=f"{self.base_url}/token",
            role_claim="roles",
            subject_claim="sub",
            expected_role="viewer",
            token=token,
            api_url=self.base_url,
            protected_path=oidc_check_cli.DEFAULT_PROTECTED_PATH,
            require_https=False,
            production=False,
        )

        results = oidc_check_cli.validate(args)

        self.assertEqual(oidc_check_cli.result_exit_code(results), 0, results)
        self.assertTrue(any(result.name == "OIDC discovery issuer" and result.status == "ok" for result in results))
        self.assertTrue(any(result.name == "OIDC PKCE S256 support" and result.status == "ok" for result in results))

        args.require_https = True
        strict_results = oidc_check_cli.validate(args)
        self.assertEqual(oidc_check_cli.result_exit_code(strict_results), 1)
        self.assertTrue(any(result.name == "OIDC issuer uses HTTPS" and result.status == "fail" for result in strict_results))

    def test_production_mode_requires_endpoints_expected_role_and_backend_live_api(self):
        args = Namespace(
            issuer="https://issuer.example.test",
            audience="packaging-platform",
            jwks_url="https://issuer.example.test/jwks",
            discovery_url="",
            authorization_url="",
            token_url="",
            role_claim="roles",
            subject_claim="sub",
            expected_role="",
            token="",
            api_url="",
            protected_path=oidc_check_cli.DEFAULT_PROTECTED_PATH,
            require_https=False,
            production=True,
            broker_live=False,
            require_broker=False,
        )

        with patch.object(oidc_check_cli, "fetch_json", return_value={"keys": []}):
            results = oidc_check_cli.validate(args)

        self.assertTrue(any(result.name == "Production authorization endpoint" and result.status == "fail" for result in results))
        self.assertTrue(any(result.name == "Production token endpoint" and result.status == "fail" for result in results))
        self.assertTrue(any(result.name == "Production expected role" and result.status == "fail" for result in results))
        self.assertTrue(any(result.name == "Production backend live API" and result.status == "fail" for result in results))

    def test_broker_live_acceptance_exchanges_session_and_logout(self):
        JSONHandler.api_status_after_logout = 401
        args = Namespace(
            api_url=self.base_url,
            broker_url="",
            broker_code="auth-code",
            broker_code_verifier="pkce-verifier",
            broker_redirect_uri="http://localhost:5173/",
            protected_path=oidc_check_cli.DEFAULT_PROTECTED_PATH,
            broker_logout=True,
            require_session_store=True,
        )

        results = oidc_check_cli.validate_broker_live(args)

        self.assertEqual(oidc_check_cli.result_exit_code(results), 0, results)
        self.assertTrue(any(result.name == "OIDC broker token exchange" and result.status == "ok" for result in results))
        self.assertTrue(any(result.name == "Backend OIDC acceptance" and result.status == "ok" for result in results))
        self.assertTrue(any(result.name == "Broker logout" and result.status == "ok" for result in results))
        self.assertTrue(any(result.name == "Broker session revocation" and result.status == "ok" for result in results))

    def test_broker_live_requires_session_id_when_session_store_is_required(self):
        JSONHandler.broker_body = {"token": "pps_session_token", "role": "operator", "subject": "oidc:alice"}
        args = Namespace(
            api_url=self.base_url,
            broker_url="",
            broker_code="auth-code",
            broker_code_verifier="pkce-verifier",
            broker_redirect_uri="http://localhost:5173/",
            protected_path=oidc_check_cli.DEFAULT_PROTECTED_PATH,
            broker_logout=False,
            require_session_store=True,
        )

        results = oidc_check_cli.validate_broker_live(args)

        self.assertTrue(any(result.name == "OIDC broker persisted session" and result.status == "fail" for result in results))

    def test_broker_live_requires_one_time_code_inputs(self):
        args = Namespace(
            api_url=self.base_url,
            broker_url="",
            broker_code="",
            broker_code_verifier="",
            broker_redirect_uri="",
            protected_path=oidc_check_cli.DEFAULT_PROTECTED_PATH,
            broker_logout=False,
            require_session_store=False,
        )

        results = oidc_check_cli.validate_broker_live(args)

        self.assertEqual(oidc_check_cli.result_exit_code(results), 1)
        self.assertIn("--broker-code", results[0].detail)

    def test_require_broker_fails_without_broker_live(self):
        args = Namespace(
            issuer="https://issuer.example.test",
            audience="packaging-platform",
            jwks_url="",
            discovery_url="",
            authorization_url="",
            token_url="",
            role_claim="roles",
            subject_claim="sub",
            expected_role="",
            token="",
            api_url="",
            protected_path=oidc_check_cli.DEFAULT_PROTECTED_PATH,
            require_https=False,
            production=False,
            broker_live=False,
            require_broker=True,
        )

        with patch.dict("os.environ", {}, clear=True):
            results = oidc_check_cli.validate(args)

        self.assertTrue(any(result.name == "OIDC broker live acceptance" and result.status == "fail" for result in results))


if __name__ == "__main__":
    unittest.main()

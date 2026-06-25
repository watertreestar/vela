import hashlib
import json
import pathlib
import sys
import unittest
from argparse import Namespace
from http.server import BaseHTTPRequestHandler, HTTPServer
from threading import Thread

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import gitlab_package_check_cli


class GitLabPackageHandler(BaseHTTPRequestHandler):
    payload = b"gitlab package artifact"
    last_private_token = ""
    status_code = 200

    def do_GET(self):
        self.__class__.last_private_token = self.headers.get("PRIVATE-TOKEN", "")
        if self.path != "/api/v4/projects/42/packages/generic/engine/1.2.3/engine.tar.gz":
            self.send_response(404)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"error": self.path}).encode("utf-8"))
            return
        self.send_response(self.status_code)
        self.send_header("Content-Type", "application/octet-stream")
        self.end_headers()
        self.wfile.write(self.payload)

    def log_message(self, format, *args):
        return


def args(**overrides):
    defaults = {
        "base_url": "",
        "token": "",
        "repository": "gitlab",
        "path": "42/engine/1.2.3/engine.tar.gz",
        "project": "",
        "package": "",
        "version": "",
        "file": "",
        "expected_sha256": "",
        "require_token": False,
        "production": False,
        "timeout": 10,
    }
    defaults.update(overrides)
    return Namespace(**defaults)


class GitLabPackageCheckCliTest(unittest.TestCase):
    def setUp(self):
        GitLabPackageHandler.payload = b"gitlab package artifact"
        GitLabPackageHandler.last_private_token = ""
        GitLabPackageHandler.status_code = 200
        self.server = HTTPServer(("127.0.0.1", 0), GitLabPackageHandler)
        self.thread = Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.base_url = f"http://127.0.0.1:{self.server.server_port}"

    def tearDown(self):
        self.server.shutdown()
        self.thread.join(timeout=5)
        self.server.server_close()

    def test_build_download_url_matches_backend_resolver_convention(self):
        url = gitlab_package_check_cli.build_download_url(
            "https://gitlab.example.test",
            "gitlab:42",
            "engine/1.2.3/engine.tar.gz",
        )

        self.assertEqual(
            url,
            "https://gitlab.example.test/api/v4/projects/42/packages/generic/engine/1.2.3/engine.tar.gz",
        )

    def test_validate_downloads_package_sends_token_and_computes_sha(self):
        expected = hashlib.sha256(GitLabPackageHandler.payload).hexdigest()

        results = gitlab_package_check_cli.validate(
            args(base_url=self.base_url, token="gitlab-token", expected_sha256=expected)
        )

        self.assertEqual(gitlab_package_check_cli.result_exit_code(results), 0, results)
        self.assertEqual(GitLabPackageHandler.last_private_token, "gitlab-token")
        self.assertTrue(any(result.name == "GitLab expected sha256" and result.status == "ok" for result in results))

    def test_expected_sha_mismatch_fails(self):
        results = gitlab_package_check_cli.validate(
            args(base_url=self.base_url, expected_sha256="0" * 64)
        )

        self.assertEqual(gitlab_package_check_cli.result_exit_code(results), 1)
        self.assertTrue(any(result.name == "GitLab expected sha256" and result.status == "fail" for result in results))

    def test_production_requires_https_and_rejects_wildcard(self):
        results = gitlab_package_check_cli.validate(
            args(base_url=self.base_url, path="42/engine/1.2.3/engine-*.tar.gz", production=True)
        )

        self.assertEqual(gitlab_package_check_cli.result_exit_code(results), 1)
        self.assertTrue(any(result.name == "GitLab base URL uses HTTPS" and result.status == "fail" for result in results))
        self.assertTrue(any(result.name == "GitLab exact file path" and result.status == "fail" for result in results))

    def test_require_token_fails_when_token_is_missing(self):
        results = gitlab_package_check_cli.validate(args(base_url=self.base_url, require_token=True))

        self.assertEqual(gitlab_package_check_cli.result_exit_code(results), 1)
        self.assertTrue(any(result.name == "GitLab token configured" and result.status == "fail" for result in results))

    def test_path_can_be_built_from_parts(self):
        path = gitlab_package_check_cli.gitlab_path_from_parts(
            args(project="42", package="engine", version="1.2.3", file="engine.tar.gz")
        )

        self.assertEqual(path, "42/engine/1.2.3/engine.tar.gz")


if __name__ == "__main__":
    unittest.main()

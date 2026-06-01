import argparse
import pathlib
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import seed_cli


class FakePackagingApi:
    def __init__(self):
        self.calls = []
        self.builds = []
        self.next_id = 1

    def request_json(self, api_url, method, path, body=None):
        self.calls.append((method, path, body))
        if (method, path) == ("GET", "/api/health"):
            return {"status": "ok"}
        if (method, path) == ("GET", "/api/products"):
            return [{"id": "lsap"}]
        if (method, path) == ("GET", "/api/builds"):
            return list(self.builds)
        if (method, path) == ("POST", "/api/builds"):
            build = {
                "id": f"build-{self.next_id}",
                "productId": body["productId"],
                "target": body["target"],
                "profile": body["profile"],
                "customer": body["customer"],
                "version": body["version"],
                "releaseChannel": body["releaseChannel"],
                "status": "pending_approval",
                "variables": body["variables"],
                "artifactUri": "",
                "downloadUrl": "",
                "error": "",
                "logs": [],
            }
            self.next_id += 1
            self.builds.append(build)
            return build
        if method == "POST" and path.endswith("/approve"):
            build = self._build(path.split("/")[3])
            build["status"] = "queued"
            return build
        if (method, path) == ("POST", "/api/runner/tasks/claim"):
            for build in self.builds:
                if build["status"] == "queued" and build["target"] == body["target"]:
                    build["status"] = "running"
                    build["runnerId"] = body["runnerId"]
                    return build
            return None
        if method == "POST" and path.endswith("/complete"):
            build = self._build(path.split("/")[4])
            build["status"] = body["status"]
            build["artifactUri"] = body.get("artifactUri", "")
            build["downloadUrl"] = body.get("downloadUrl", "")
            build["error"] = body.get("error", "")
            return build
        if method == "POST" and path.endswith("/validate-preview"):
            build = self._build(path.split("/")[3])
            build["status"] = "preview_validated"
            return build
        if method == "POST" and path.endswith("/logs"):
            return []
        if method == "GET" and path.endswith("/release-check"):
            build = self._build(path.split("/")[3])
            release_ready = build["status"] == "preview_validated" and build["version"] != "latest"
            return {
                "previewReady": build["status"] in {"succeeded", "preview_validated"},
                "releaseReady": release_ready,
                "blockingCount": 0 if release_ready else 1,
            }
        raise AssertionError(f"unexpected API call: {method} {path}")

    def _build(self, build_id):
        for build in self.builds:
            if build["id"] == build_id:
                return build
        raise AssertionError(f"unknown build {build_id}")


class SeedCliTest(unittest.TestCase):
    def test_seed_uses_api_workflow_without_direct_database_writes(self):
        fake = FakePackagingApi()
        args = argparse.Namespace(
            api_url="http://api.test",
            database_url="postgres://unused",
            namespace="test-seed",
        )

        with (
            patch("seed_cli.request_json", side_effect=fake.request_json),
            patch("seed_cli.upload_seed_file", return_value={"id": "upload-1", "uri": "local-upload://upload-1/file", "downloadUrl": "http://api.test/file"}),
        ):
            result = seed_cli.seed(args)

        self.assertEqual(result, 0)
        self.assertFalse(hasattr(seed_cli, "run_sql"))
        paths = [path for method, path, _ in fake.calls if method == "POST"]
        self.assertTrue(any(path.endswith("/approve") for path in paths))
        self.assertIn("/api/runner/tasks/claim", paths)
        self.assertTrue(any(path.endswith("/complete") for path in paths))
        self.assertTrue(any(path.endswith("/validate-preview") for path in paths))

    def test_seed_stops_when_non_seed_queued_build_could_be_claimed(self):
        fake = FakePackagingApi()
        fake.builds.append(
            {
                "id": "external-queued",
                "target": "linux",
                "status": "queued",
                "variables": {"owner": "external"},
            }
        )
        args = argparse.Namespace(
            api_url="http://api.test",
            namespace="test-seed",
        )

        with patch("seed_cli.request_json", side_effect=fake.request_json):
            with self.assertRaisesRegex(RuntimeError, "queued non-seed"):
                seed_cli.seed(args)


if __name__ == "__main__":
    unittest.main()

import argparse
import pathlib
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import monitor_cli


def args(**overrides):
    values = {
        "api_url": "http://api.test",
        "fail_on_warning": False,
        "json": False,
        "max_queued_builds": 10,
        "max_failed_builds": 20,
        "max_expired_artifacts": 0,
        "max_unhealthy_runners": 0,
        "runner_stale_minutes": 5,
    }
    values.update(overrides)
    return argparse.Namespace(**values)


class FakeMonitorApi:
    def __init__(self, *, warning_state=False, missing_artifact_metadata=False):
        self.warning_state = warning_state
        self.missing_artifact_metadata = missing_artifact_metadata

    def request_json(self, api_url, method, path, body=None):
        if (method, path) == ("GET", "/api/health"):
            return {"status": "ok"}
        if (method, path) == ("GET", "/api/products"):
            return [{"id": "lsap"}]
        if (method, path) == ("GET", "/api/builds"):
            return self.builds()
        if (method, path) == ("GET", "/api/uploads"):
            return self.uploads()
        if (method, path) == ("GET", "/api/runners"):
            return self.runners()
        raise AssertionError(f"unexpected API call: {method} {path}")

    def request_text(self, api_url, method, path):
        if method == "GET" and path == "/api/builds/build-1/logs/stream":
            return 'event: log\ndata: {"source":"runner-1","message":"ok","sequence":1}\n\n'
        raise AssertionError(f"unexpected text call: {method} {path}")

    def builds(self):
        if self.warning_state:
            return [
                {
                    "id": "build-1",
                    "status": "queued",
                    "logs": [{"source": "runner-1", "message": "queued"}],
                    "releaseCheckSummary": {"releaseReady": False, "blockingCount": 1},
                },
                {"id": "build-2", "status": "queued", "logs": []},
            ]
        return [
            {
                "id": "build-1",
                "status": "running",
                "runnerId": "runner-1",
                "logs": [{"source": "runner-1", "message": "running", "sequence": 1}],
                "releaseCheckSummary": {"releaseReady": True, "blockingCount": 0},
            }
        ]

    def uploads(self):
        upload = {
            "id": "upload-1",
            "fileName": "package.tar.gz",
            "downloadUrl": "http://api.test/api/uploads/upload-1/download",
            "sha256": "abc123",
            "downloadPolicy": "platform",
            "retentionStatus": "active",
        }
        if self.warning_state:
            upload["retentionStatus"] = "expired"
        if self.missing_artifact_metadata:
            upload["sha256"] = ""
        return [upload]

    def runners(self):
        if self.warning_state:
            return [
                {
                    "runnerId": "runner-1",
                    "status": "offline",
                    "runningBuilds": 0,
                    "lastHeartbeatAt": "2020-01-01T00:00:00Z",
                }
            ]
        return [
            {
                "runnerId": "runner-1",
                "status": "busy",
                "currentBuildId": "build-1",
                "runningBuilds": 1,
                "lastHeartbeatAt": "2100-01-01T00:00:00Z",
            }
        ]


class MonitorCliTest(unittest.TestCase):
    def run_with_fake(self, fake, monitor_args):
        with (
            patch("monitor_cli.request_json", side_effect=fake.request_json),
            patch("monitor_cli.request_text", side_effect=fake.request_text),
        ):
            return monitor_cli.run_checks(monitor_args)

    def test_run_checks_reports_green_confirmation_lights(self):
        results = self.run_with_fake(FakeMonitorApi(), args())

        self.assertTrue(results)
        self.assertTrue(all(result.status == "ok" for result in results))
        self.assertEqual(monitor_cli.result_exit_code(results, fail_on_warning=False), 0)

    def test_warnings_can_be_promoted_to_nonzero_exit_code(self):
        results = self.run_with_fake(FakeMonitorApi(warning_state=True), args(max_queued_builds=1))

        warnings = [result.name for result in results if result.status == "warn"]
        self.assertIn("Build queue backlog", warnings)
        self.assertIn("Release gate summary", warnings)
        self.assertIn("Artifact retention", warnings)
        self.assertIn("Runner availability", warnings)
        self.assertEqual(monitor_cli.result_exit_code(results, fail_on_warning=False), 0)
        self.assertEqual(monitor_cli.result_exit_code(results, fail_on_warning=True), 1)

    def test_missing_artifact_contract_fields_fail(self):
        results = self.run_with_fake(FakeMonitorApi(missing_artifact_metadata=True), args())

        artifact_contract = next(result for result in results if result.name == "Artifact metadata contract")
        self.assertEqual(artifact_contract.status, "fail")
        self.assertEqual(monitor_cli.result_exit_code(results, fail_on_warning=False), 1)


if __name__ == "__main__":
    unittest.main()

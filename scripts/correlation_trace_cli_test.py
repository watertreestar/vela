import pathlib
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import correlation_trace_cli


def parse_args(*values):
    return correlation_trace_cli.build_parser().parse_args(["trace", *values])


class CorrelationTraceCliTest(unittest.TestCase):
    def test_collects_trace_records_across_resources(self):
        def fake_request(api_url, method, path, token, timeout):
            self.assertEqual(api_url, "http://api.test")
            self.assertEqual(method, "GET")
            if path == "/api/audit-events?limit=100":
                return [
                    {
                        "id": "audit-1",
                        "action": "build.approve",
                        "resourceType": "build",
                        "resourceId": "build-1",
                        "buildId": "build-1",
                        "requestId": "req-approve",
                        "correlationId": "corr-release",
                    }
                ]
            if path == "/api/builds":
                return [
                    {
                        "id": "build-1",
                        "status": "succeeded",
                        "productId": "lsap",
                        "target": "linux",
                        "logs": [
                            {
                                "sequence": 1,
                                "source": "runner-1",
                                "message": "claimed",
                                "requestId": "req-runner",
                                "correlationId": "corr-release",
                            }
                        ],
                    }
                ]
            if path == "/api/uploads":
                return [
                    {
                        "id": "upload-1",
                        "fileName": "lsap.tar.gz",
                        "sha256": "abc123",
                        "requestId": "req-upload",
                        "correlationId": "corr-release",
                    }
                ]
            if path == "/api/notifications?limit=100":
                return [
                    {
                        "id": "notification-1",
                        "type": "preview.ready",
                        "severity": "success",
                        "buildId": "build-1",
                        "metadata": {"correlationId": "corr-release"},
                    }
                ]
            raise AssertionError(path)

        with patch.object(correlation_trace_cli, "request_json", side_effect=fake_request):
            report = correlation_trace_cli.build_trace_report(
                parse_args("--api-url", "http://api.test", "--correlation-id", "corr-release")
            )

        self.assertEqual(report["status"], "ok")
        self.assertEqual(report["summary"]["auditEvents"], 1)
        self.assertEqual(report["summary"]["builds"], 1)
        self.assertEqual(report["summary"]["buildLogs"], 1)
        self.assertEqual(report["summary"]["uploads"], 1)
        self.assertEqual(report["summary"]["notifications"], 1)
        self.assertEqual(report["matches"]["buildLogs"][0]["buildId"], "build-1")
        self.assertEqual(report["matches"]["notifications"][0]["id"], "notification-1")

    def test_scans_build_log_endpoint_when_requested(self):
        calls = []

        def fake_request(api_url, method, path, token, timeout):
            calls.append(path)
            if path == "/api/audit-events?limit=100":
                return []
            if path == "/api/builds":
                return [{"id": "build-1", "status": "running", "logs": []}]
            if path == "/api/builds/build-1/logs":
                return [
                    {
                        "sequence": 7,
                        "source": "runner-1",
                        "message": "heartbeat",
                        "requestId": "req-heartbeat",
                        "correlationId": "corr-runner",
                    }
                ]
            if path == "/api/uploads":
                return []
            if path == "/api/notifications?limit=100":
                return []
            raise AssertionError(path)

        with patch.object(correlation_trace_cli, "request_json", side_effect=fake_request):
            report = correlation_trace_cli.build_trace_report(
                parse_args("--correlation-id", "corr-runner", "--scan-build-log-endpoints")
            )

        self.assertIn("/api/builds/build-1/logs", calls)
        self.assertEqual(report["summary"]["buildLogs"], 1)
        self.assertEqual(report["matches"]["buildLogs"][0]["sequence"], 7)

    def test_no_matches_is_warning(self):
        with patch.object(correlation_trace_cli, "request_json", return_value=[]):
            report = correlation_trace_cli.build_trace_report(parse_args("--request-id", "req-missing"))

        self.assertEqual(report["status"], "warn")
        self.assertEqual(correlation_trace_cli.report_exit_code(report, fail_on_warning=False), 0)
        self.assertEqual(correlation_trace_cli.report_exit_code(report, fail_on_warning=True), 1)

    def test_requires_request_or_correlation_id(self):
        args = parse_args()

        with self.assertRaises(ValueError):
            correlation_trace_cli.build_trace_report(args)


if __name__ == "__main__":
    unittest.main()

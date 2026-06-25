import pathlib
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import artifact_retention_cli


def parse_args(*values):
    return artifact_retention_cli.build_parser().parse_args(["check", *values])


class ArtifactRetentionCliTest(unittest.TestCase):
    def test_metadata_contract_passes_without_expired_uploads(self):
        uploads = [
            {
                "id": "upload-active",
                "sha256": "abc123",
                "downloadUrl": "http://api.test/api/uploads/upload-active/download",
                "downloadPolicy": "platform",
                "retentionStatus": "active",
                "retentionDays": 7,
            },
            {
                "id": "upload-permanent",
                "sha256": "def456",
                "downloadUrl": "http://api.test/api/uploads/upload-permanent/download",
                "downloadPolicy": "restricted",
                "retentionStatus": "permanent",
                "retentionDays": 0,
            },
        ]

        with patch.object(artifact_retention_cli, "request_json", return_value=uploads):
            results = artifact_retention_cli.run_checks(parse_args("--api-url", "http://api.test"))

        self.assertEqual(artifact_retention_cli.result_exit_code(results, fail_on_warning=False), 0, results)
        self.assertTrue(any(result.name == "Artifact metadata contract" and result.status == "ok" for result in results))
        self.assertTrue(any(result.name == "Expired artifact download rejection" and result.status == "ok" for result in results))

    def test_missing_metadata_fields_fail_contract(self):
        uploads = [
            {
                "id": "upload-bad",
                "sha256": "",
                "downloadUrl": "",
                "downloadPolicy": "",
                "retentionStatus": "active",
            }
        ]

        with patch.object(artifact_retention_cli, "request_json", return_value=uploads):
            results = artifact_retention_cli.run_checks(parse_args("--api-url", "http://api.test"))

        self.assertEqual(artifact_retention_cli.result_exit_code(results, fail_on_warning=False), 1)
        self.assertTrue(any(result.name == "Artifact metadata contract" and result.status == "fail" for result in results))

    def test_expired_upload_download_must_return_gone(self):
        uploads = [
            {
                "id": "upload-expired",
                "sha256": "abc123",
                "downloadUrl": "http://api.test/api/uploads/upload-expired/download",
                "downloadPolicy": "platform",
                "retentionStatus": "expired",
                "retentionDays": 1,
            }
        ]

        with (
            patch.object(artifact_retention_cli, "request_json", return_value=uploads),
            patch.object(artifact_retention_cli, "request_status", return_value=410) as request_status,
        ):
            results = artifact_retention_cli.run_checks(parse_args("--api-url", "http://api.test", "--max-expired-artifacts", "1"))

        self.assertEqual(artifact_retention_cli.result_exit_code(results, fail_on_warning=False), 0, results)
        request_status.assert_called_once()
        self.assertTrue(any(result.name == "Expired artifact download rejection" and result.status == "ok" for result in results))

    def test_expired_upload_download_returning_success_fails(self):
        uploads = [
            {
                "id": "upload-expired",
                "sha256": "abc123",
                "downloadUrl": "http://api.test/api/uploads/upload-expired/download",
                "downloadPolicy": "platform",
                "retentionStatus": "expired",
            }
        ]

        with (
            patch.object(artifact_retention_cli, "request_json", return_value=uploads),
            patch.object(artifact_retention_cli, "request_status", return_value=200),
        ):
            results = artifact_retention_cli.run_checks(parse_args("--api-url", "http://api.test", "--max-expired-artifacts", "1"))

        self.assertEqual(artifact_retention_cli.result_exit_code(results, fail_on_warning=False), 1)
        self.assertTrue(any(result.name == "Expired artifact download rejection" and result.status == "fail" for result in results))

    def test_expired_threshold_warning_can_fail(self):
        uploads = [
            {
                "id": "upload-expired",
                "sha256": "abc123",
                "downloadUrl": "http://api.test/api/uploads/upload-expired/download",
                "downloadPolicy": "platform",
                "retentionStatus": "expired",
            }
        ]

        with (
            patch.object(artifact_retention_cli, "request_json", return_value=uploads),
            patch.object(artifact_retention_cli, "request_status", return_value=410),
        ):
            results = artifact_retention_cli.run_checks(parse_args("--api-url", "http://api.test"))

        self.assertEqual(artifact_retention_cli.result_exit_code(results, fail_on_warning=False), 0)
        self.assertEqual(artifact_retention_cli.result_exit_code(results, fail_on_warning=True), 1)
        self.assertTrue(any(result.name == "Expired artifact threshold" and result.status == "warn" for result in results))


if __name__ == "__main__":
    unittest.main()

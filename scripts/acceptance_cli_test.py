import argparse
import pathlib
import sys
import unittest
from unittest.mock import Mock, patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import acceptance_cli


class FakeAcceptanceApi:
    def __init__(self):
        self.calls = []
        self.build = None
        self.upload_count = 0

    def request_json(self, api_url, method, path, body=None):
        self.calls.append((method, path, body))
        if (method, path) == ("GET", "/api/health"):
            return {"status": "ok"}
        if (method, path) == ("GET", "/api/products"):
            return [{"id": "lsap"}]
        if (method, path) == ("GET", "/api/products/lsap"):
            return {
                "id": "lsap",
                "components": [
                    {"id": "location-engine"},
                    {"id": "location-guard"},
                    {"id": "ui"},
                ],
            }
        if (method, path) == ("GET", "/api/builds"):
            return [] if self.build is None else [self.build]
        if (method, path) == ("POST", "/api/builds"):
            self.build = {
                "id": "build-acceptance",
                "target": body["target"],
                "status": "pending_approval",
                "variables": body["variables"],
                "artifactUri": "",
                "downloadUrl": "",
                "logs": [],
            }
            return self.build
        if method == "POST" and path.endswith("/approve"):
            self.build["status"] = "queued"
            self.build["nomadJobId"] = "pkg-build-acceptance"
            return self.build
        if (method, path) == ("GET", "/api/builds/build-acceptance"):
            return self.build
        if method == "POST" and path.endswith("/validate-preview"):
            self.build["status"] = "preview_validated"
            return self.build
        if method == "GET" and path.endswith("/release-check"):
            return {"releaseReady": True, "blockingCount": 0}
        raise AssertionError(f"unexpected API call: {method} {path}")

    def upload_file(self, api_url, filename, content):
        self.upload_count += 1
        return {
            "id": f"upload-{self.upload_count}",
            "uri": f"local-upload://upload-{self.upload_count}/{filename}",
            "downloadUrl": f"{api_url}/api/uploads/upload-{self.upload_count}/download",
        }


def args(**overrides):
    values = {
        "api_url": "http://api.test",
        "runner_api_url": "http://runner-api.test",
        "namespace": "acceptance-test",
        "product_id": "lsap",
        "target": "linux",
        "profile": "qa",
        "customer": "example-customer",
        "version": "1.0.0",
        "web_port": 49400,
        "runner_id": "runner-test",
        "runner_image": "packaging-runner:local",
        "rebuild_runner": False,
        "pull_missing_images": False,
        "dagger_engine_image": "registry.dagger.io/engine:v0.21.3",
        "dagger_base_image_source": "host",
        "dagger_base_image": "alpine:latest",
        "dagger_base_image_tar": "",
        "dagger_base_image_tar_tag": "",
        "dagger_install_tools": "skip",
        "linux_packager_fixture": False,
        "linux_fixture_command": acceptance_cli.DEFAULT_LINUX_FIXTURE_COMMAND,
        "verify_linux_fixture": True,
        "windows_packager_fixture": True,
        "windows_fixture_command": acceptance_cli.DEFAULT_WINDOWS_FIXTURE_COMMAND,
        "windows_packager_command": "",
        "verify_windows_fixture": True,
        "windows_signing_fixture": True,
        "windows_signing_fixture_command": acceptance_cli.DEFAULT_WINDOWS_SIGNING_FIXTURE_COMMAND,
        "windows_signing_command": "",
        "verify_windows_signing_fixture": True,
        "docker_network": "",
        "add_host_gateway": True,
        "allow_existing_queued": False,
        "cancel_stale_acceptance": True,
        "api_mode": "docker",
        "api_container": "packaging-api-acceptance",
        "api_container_image": "debian:stable-slim",
        "api_docker_network": "packaging-net",
        "postgres_container": "packaging-postgres",
        "start_api": False,
        "keep_api": False,
        "api_start_timeout": 1,
        "runner_timeout": 60,
        "artifact_store": "local",
        "minio_container": "packaging-rustfs",
        "minio_endpoint": "127.0.0.1:9000",
        "api_minio_endpoint": "",
        "minio_public_url": "",
        "runner_minio_public_url": "",
        "minio_access_key": "rustfsadmin",
        "minio_secret_key": "rustfsadmin",
        "minio_bucket": "packaging-artifacts",
        "minio_use_ssl": False,
        "store_driver": "postgres",
        "database_url": "postgres://test",
    }
    values.update(overrides)
    return argparse.Namespace(**values)


class AcceptanceCliTest(unittest.TestCase):
    def test_docker_runner_command_sets_dagger_host_image_mode(self):
        command = acceptance_cli.docker_runner_command(args())

        self.assertIn("PACKAGING_API_URL=http://runner-api.test", command)
        self.assertIn("DAGGER_LINUX_BASE_IMAGE_SOURCE=host", command)
        self.assertIn("DAGGER_LINUX_BASE_IMAGE=alpine:latest", command)
        self.assertIn("DAGGER_LINUX_INSTALL_TOOLS=skip", command)
        self.assertIn("packaging-runner:local", command)

    def test_docker_runner_command_sets_minio_public_url(self):
        command = acceptance_cli.docker_runner_command(
            args(
                artifact_store="minio",
                docker_network="packaging-net",
                runner_minio_public_url="http://packaging-rustfs:9000",
            )
        )

        self.assertIn("MINIO_PUBLIC_URL=http://packaging-rustfs:9000", command)

    def test_docker_runner_command_sets_linux_fixture_packager(self):
        command = acceptance_cli.docker_runner_command(args(linux_packager_fixture=True))

        self.assertIn(
            f"DAGGER_LINUX_PACKAGE_CMD={acceptance_cli.DEFAULT_LINUX_FIXTURE_COMMAND}",
            command,
        )
        self.assertIn(
            f"DAGGER_LINUX_INSTALL_TOOLS={acceptance_cli.DEFAULT_LINUX_FIXTURE_INSTALL_TOOLS}",
            command,
        )

    def test_docker_runner_command_sets_windows_fixture_packager(self):
        command = acceptance_cli.docker_runner_command(args(target="windows"))

        self.assertIn(
            f"WINDOWS_PACKAGER_CMD={acceptance_cli.DEFAULT_WINDOWS_FIXTURE_COMMAND}",
            command,
        )
        self.assertIn(
            f"WINDOWS_SIGNING_CMD={acceptance_cli.DEFAULT_WINDOWS_SIGNING_FIXTURE_COMMAND}",
            command,
        )

    def test_docker_runner_command_prefers_custom_windows_packager(self):
        command = acceptance_cli.docker_runner_command(
            args(target="windows", windows_packager_command="iscc C:\\packaging\\setup.iss")
        )

        self.assertIn("WINDOWS_PACKAGER_CMD=iscc C:\\packaging\\setup.iss", command)
        self.assertNotIn(
            f"WINDOWS_PACKAGER_CMD={acceptance_cli.DEFAULT_WINDOWS_FIXTURE_COMMAND}",
            command,
        )

    def test_docker_runner_command_prefers_custom_windows_signing_command(self):
        command = acceptance_cli.docker_runner_command(
            args(target="windows", windows_signing_command="signtool sign /fd SHA256 setup.exe")
        )

        self.assertIn("WINDOWS_SIGNING_CMD=signtool sign /fd SHA256 setup.exe", command)
        self.assertNotIn(
            f"WINDOWS_SIGNING_CMD={acceptance_cli.DEFAULT_WINDOWS_SIGNING_FIXTURE_COMMAND}",
            command,
        )

    def test_minio_component_overrides_use_runner_api_download_url(self):
        overrides = acceptance_cli.component_overrides(
            args(artifact_store="minio", runner_api_url="http://packaging-api:8080"),
            {
                "ui": {
                    "id": "upload-1",
                    "uri": "minio://packaging-artifacts/uploads/upload-1/ui.bin",
                    "downloadUrl": "http://127.0.0.1:8080/api/uploads/upload-1/download",
                }
            },
        )

        self.assertEqual(overrides["ui"]["kind"], "manual-upload")
        self.assertEqual(overrides["ui"]["uploadId"], "upload-1")
        self.assertEqual(overrides["ui"]["uri"], "http://packaging-api:8080/api/uploads/upload-1/download")

    def test_run_acceptance_uses_real_runner_path_and_validates_release_gate(self):
        fake = FakeAcceptanceApi()

        def mark_succeeded(_args, _root):
            fake.build["status"] = "succeeded"
            fake.build["artifactUri"] = "local-upload://artifact/package.tar.gz"
            fake.build["downloadUrl"] = "http://api.test/api/uploads/artifact/download"

        with (
            patch("acceptance_cli.api_is_healthy", return_value=True),
            patch("acceptance_cli.request_json", side_effect=fake.request_json),
            patch("acceptance_cli.upload_file", side_effect=fake.upload_file),
            patch("acceptance_cli.ensure_environment"),
            patch("acceptance_cli.run_runner", side_effect=mark_succeeded),
            patch("acceptance_cli.verify_artifact_download"),
        ):
            result = acceptance_cli.run_acceptance(args())

        self.assertEqual(result, 0)
        self.assertEqual(fake.upload_count, 3)
        post_paths = [path for method, path, _ in fake.calls if method == "POST"]
        self.assertIn("/api/builds", post_paths)
        self.assertIn("/api/builds/build-acceptance/approve", post_paths)
        self.assertIn("/api/builds/build-acceptance/validate-preview", post_paths)
        self.assertIn(("GET", "/api/builds/build-acceptance/release-check", None), fake.calls)

    def test_run_acceptance_requires_minio_artifact_uri_in_minio_mode(self):
        fake = FakeAcceptanceApi()

        def mark_succeeded(_args, _root):
            fake.build["status"] = "succeeded"
            fake.build["artifactUri"] = "local-upload://artifact/package.tar.gz"
            fake.build["downloadUrl"] = "http://api.test/api/uploads/artifact/download"

        with (
            patch("acceptance_cli.api_is_healthy", return_value=True),
            patch("acceptance_cli.request_json", side_effect=fake.request_json),
            patch("acceptance_cli.upload_file", side_effect=fake.upload_file),
            patch("acceptance_cli.ensure_environment"),
            patch("acceptance_cli.run_runner", side_effect=mark_succeeded),
        ):
            with self.assertRaisesRegex(RuntimeError, "expected minio artifact URI"):
                acceptance_cli.run_acceptance(args(artifact_store="minio"))

    def test_verify_artifact_download_checks_linux_fixture_marker(self):
        build = {
            "id": "build-linux-fixture",
            "downloadUrl": "http://api.test/api/uploads/artifact/download",
        }
        with patch(
            "acceptance_cli.download_bytes",
            return_value=acceptance_cli.LINUX_FIXTURE_MARKER + b"\nbuild-linux-fixture\n",
        ):
            acceptance_cli.verify_artifact_download(args(linux_packager_fixture=True), build)

        with patch("acceptance_cli.download_bytes", return_value=b"plain artifact"):
            with self.assertRaisesRegex(RuntimeError, "linux artifact.*fixture marker"):
                acceptance_cli.verify_artifact_download(args(linux_packager_fixture=True), build)

    def test_verify_artifact_download_checks_windows_signing_fixture_marker(self):
        build = {
            "id": "build-windows-fixture",
            "downloadUrl": "http://api.test/api/uploads/artifact/download",
        }
        signed_fixture = b"\n".join(
            [
                acceptance_cli.WINDOWS_FIXTURE_MARKER,
                acceptance_cli.WINDOWS_SIGNING_FIXTURE_MARKER,
                b"build-windows-fixture",
            ]
        )

        with patch("acceptance_cli.download_bytes", return_value=signed_fixture):
            acceptance_cli.verify_artifact_download(args(target="windows"), build)

        with patch(
            "acceptance_cli.download_bytes",
            return_value=acceptance_cli.WINDOWS_FIXTURE_MARKER + b"\nbuild-windows-fixture\n",
        ):
            with self.assertRaisesRegex(RuntimeError, "signing fixture marker"):
                acceptance_cli.verify_artifact_download(args(target="windows"), build)

    def test_preflight_fails_when_existing_queued_build_could_be_claimed(self):
        def request_json(api_url, method, path, body=None):
            if (method, path) == ("GET", "/api/products"):
                return [{"id": "lsap"}]
            if (method, path) == ("GET", "/api/builds"):
                return [{"id": "queued-1", "target": "linux", "status": "queued"}]
            raise AssertionError(f"unexpected API call: {method} {path}")

        with (
            patch("acceptance_cli.api_is_healthy", return_value=True),
            patch("acceptance_cli.request_json", side_effect=request_json),
            patch("acceptance_cli.docker_available", return_value=acceptance_cli.CheckResult("Docker daemon", "ok", "ok")),
        ):
            checks = acceptance_cli.preflight(args(), pathlib.Path("."))

        queue_check = next(check for check in checks if check.name == "Runner queue isolation")
        self.assertEqual(queue_check.status, "fail")


if __name__ == "__main__":
    unittest.main()

import pathlib
import sys
import tarfile
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import fixture_golden_cli


class FixtureGoldenCliTest(unittest.TestCase):
    def test_linux_fixture_is_deterministic_and_contains_expected_metadata(self):
        with tempfile.TemporaryDirectory(prefix="packaging-fixture-linux-") as temp_dir:
            workspace = pathlib.Path(temp_dir)
            fixture_golden_cli.create_fixture_workspace(workspace, target="linux")

            first = workspace / "first.tar.gz"
            second = workspace / "second.tar.gz"
            fixture_golden_cli.run_linux_fixture(workspace, first)
            fixture_golden_cli.run_linux_fixture(workspace, second)

            self.assertEqual(fixture_golden_cli.sha256(first), fixture_golden_cli.sha256(second))
            metadata = fixture_golden_cli.read_linux_metadata(first)
            self.assertEqual(metadata["marker"], fixture_golden_cli.LINUX_MARKER)
            self.assertEqual(metadata["buildId"], "fixture-build-linux")
            self.assertEqual(metadata["target"], "linux")
            self.assertEqual(metadata["componentCount"], 2)
            self.assertEqual(metadata["workloadCount"], 1)
            self.assertTrue(any(item["path"] == "context/build-lock.json" for item in metadata["contextChecksums"]))

            with tarfile.open(first, "r:gz") as archive:
                payload_digest = archive.extractfile("payload.sha256").read().decode("ascii").strip()
            self.assertEqual(payload_digest, fixture_golden_cli.payload_sha256_from_linux_metadata(first))

    def test_windows_fixture_signing_is_deterministic_and_contains_expected_metadata(self):
        with tempfile.TemporaryDirectory(prefix="packaging-fixture-windows-") as temp_dir:
            workspace = pathlib.Path(temp_dir) / "first"
            second_workspace = pathlib.Path(temp_dir) / "second"
            fixture_golden_cli.create_fixture_workspace(workspace, target="windows")
            fixture_golden_cli.create_fixture_workspace(second_workspace, target="windows")

            first = workspace / "setup.exe"
            second = second_workspace / "setup.exe"
            fixture_golden_cli.run_windows_fixture(workspace, first)
            fixture_golden_cli.run_windows_fixture(second_workspace, second)

            self.assertEqual(fixture_golden_cli.sha256(first), fixture_golden_cli.sha256(second))
            setup_metadata = fixture_golden_cli.read_windows_setup_metadata(first)
            signing_metadata = fixture_golden_cli.read_windows_signing_metadata(first)
            self.assertEqual(setup_metadata["marker"], fixture_golden_cli.WINDOWS_MARKER)
            self.assertEqual(setup_metadata["buildId"], "fixture-build-windows")
            self.assertEqual(setup_metadata["target"], "windows")
            self.assertEqual(signing_metadata["marker"], fixture_golden_cli.WINDOWS_SIGNING_MARKER)
            self.assertEqual(signing_metadata["buildId"], "fixture-build-windows")
            self.assertEqual(signing_metadata["artifactName"], "setup.exe")
            self.assertEqual(signing_metadata["signatureKind"], "deterministic-fixture")

    def test_check_reports_confirmation_lights(self):
        with tempfile.TemporaryDirectory(prefix="packaging-fixture-check-") as temp_dir:
            results = fixture_golden_cli.check(pathlib.Path(temp_dir))

        self.assertEqual(fixture_golden_cli.result_exit_code(results), 0, results)
        self.assertTrue(any(result.name == "Linux fixture determinism" and result.status == "ok" for result in results))
        self.assertTrue(any(result.name == "Windows fixture signing" and result.status == "ok" for result in results))


if __name__ == "__main__":
    unittest.main()

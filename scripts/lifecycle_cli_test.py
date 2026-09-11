import argparse
import pathlib
import subprocess
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import lifecycle_cli


def args(**overrides):
    values = {"full_conductor": False}
    values.update(overrides)
    return argparse.Namespace(**values)


class LifecycleCliTest(unittest.TestCase):
    def test_check_passes_when_lifecycle_go_tests_pass(self):
        completed = subprocess.CompletedProcess(["go"], 0, "ok", "")
        with patch("lifecycle_cli.run_command", return_value=completed) as run_command:
            results = lifecycle_cli.run_checks(args())

        self.assertEqual(lifecycle_cli.result_exit_code(results), 0)
        self.assertEqual(results[0].status, "ok")
        run_command.assert_called_once()

    def test_check_fails_when_lifecycle_go_tests_fail(self):
        completed = subprocess.CompletedProcess(["go"], 1, "", "boom")
        with patch("lifecycle_cli.run_command", return_value=completed):
            results = lifecycle_cli.run_checks(args())

        self.assertEqual(lifecycle_cli.result_exit_code(results), 1)
        self.assertEqual(results[0].status, "fail")
        self.assertIn("boom", results[0].detail)

    def test_full_conductor_runs_second_regression_check(self):
        completed = subprocess.CompletedProcess(["go"], 0, "ok", "")
        with patch("lifecycle_cli.run_command", return_value=completed) as run_command:
            results = lifecycle_cli.run_checks(args(full_conductor=True))

        self.assertEqual(len(results), 2)
        self.assertEqual(lifecycle_cli.result_exit_code(results), 0)
        self.assertEqual(run_command.call_count, 2)


if __name__ == "__main__":
    unittest.main()

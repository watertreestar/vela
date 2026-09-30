import json
import pathlib
import subprocess
import sys
import unittest

ROOT = pathlib.Path(__file__).resolve().parent
CLI = ROOT / "runner_e2e_cli.py"
sys.path.insert(0, str(ROOT))

import runner_e2e_cli


class RunnerE2ECliTest(unittest.TestCase):
    def run_cli(self, *args, env=None):
        return subprocess.run([sys.executable, str(CLI), *args], text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30, env=env)

    def test_fixture_report_is_contract_shaped_and_not_live_success(self):
        result = self.run_cli("check", "--fixture", "--json")
        self.assertEqual(result.returncode, 0, result.stderr)
        payload = json.loads(result.stdout)
        self.assertEqual(payload["schema"], "packaging.runner_e2e.report/v1")
        self.assertEqual(payload["mode"], "fixture")
        self.assertEqual(payload["status"], "warn")
        self.assertTrue(any(item["name"] == "Deployment boundary" and item["status"] == "warn" for item in payload["gates"]))

    def test_live_requires_nomad_url_and_read_token_without_echoing_operator_token(self):
        result = self.run_cli("check", "--live", "--api-url", "https://api.example.test", "--build-id", "build-1", "--nomad-job-id", "job-1")
        self.assertEqual(result.returncode, 1)
        self.assertIn("PACKAGING_OPERATOR_TOKEN", result.stderr)
        self.assertNotIn("human-token", result.stderr)

    def test_refuses_runner_secret_environment_without_echoing_value(self):
        env = dict(__import__("os").environ, AUTH_RUNNER_TOKEN="runner-secret-sentinel")
        result = self.run_cli("check", "--fixture", "--json", env=env)
        self.assertEqual(result.returncode, 1)
        self.assertIn("runner credential environment", result.stderr)
        self.assertNotIn("runner-secret-sentinel", result.stderr)

    def test_report_redacts_secret_like_adapter_detail(self):
        payload = runner_e2e_cli.report([runner_e2e_cli.CheckResult("x", "fail", "Authorization=Bearer-secret capability=opaque")], "fixture")
        rendered = json.dumps(payload)
        self.assertNotIn("Bearer-secret", rendered)
        self.assertNotIn("opaque", rendered)

    def test_evidence_binding_rejects_mismatched_runner_output(self):
        evidence = runner_e2e_cli.FixtureAdapter().evidence("build-1")
        evidence["checksumRunnerId"] = "other-runner"
        self.assertFalse(runner_e2e_cli.evidence_binding_ok(evidence, "build-1"))

    def test_allocation_completion_requires_bound_terminal_package_task(self):
        complete = {"JobID": "job-1", "TaskGroup": "builder", "DesiredStatus": "run", "ClientStatus": "complete", "TaskStates": {"package": {"State": "dead", "Failed": False}}}
        self.assertTrue(runner_e2e_cli.allocation_complete(complete, "job-1"))
        self.assertFalse(runner_e2e_cli.allocation_complete({**complete, "JobID": "other-job"}, "job-1"))
        self.assertFalse(runner_e2e_cli.allocation_complete({**complete, "DesiredStatus": "stop"}, "job-1"))
        self.assertFalse(runner_e2e_cli.allocation_complete({**complete, "TaskStates": {}}, "job-1"))

    def test_live_check_compares_conductor_and_nomad_evidence(self):
        class Adapter(runner_e2e_cli.FixtureAdapter):
            def evidence(self, build_id):
                result = super().evidence(build_id)
                result["nomadJobId"] = "different-job"
                return result

        results = runner_e2e_cli.checks(Adapter(), "build-1", "job-1", live=True)
        mismatch = next(item for item in results if item.name == "Conductor-to-Nomad job binding")
        self.assertEqual(mismatch.status, "fail")

    def test_live_check_rejects_multiple_active_allocations(self):
        class Adapter(runner_e2e_cli.FixtureAdapter):
            def allocations(self, job_id):
                good = super().allocations(job_id)[0]
                return [good, {**good, "ClientStatus": "running"}]

        results = runner_e2e_cli.checks(Adapter(), "build-1", "job-1", live=True)
        allocation_gate = next(item for item in results if item.name == "Nomad allocation completion")
        self.assertEqual(allocation_gate.status, "fail")

    def test_live_check_rejects_job_with_wrong_dispatch_metadata(self):
        class Adapter(runner_e2e_cli.FixtureAdapter):
            def nomad_job(self, job_id):
                result = super().nomad_job(job_id)
                result["Meta"]["packaging.build_id"] = "other-build"
                return result

        results = runner_e2e_cli.checks(Adapter(), "build-1", "job-1", live=True)
        job_gate = next(item for item in results if item.name == "Nomad batch job evidence")
        self.assertEqual(job_gate.status, "fail")


if __name__ == "__main__":
    unittest.main()

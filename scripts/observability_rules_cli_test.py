import pathlib
import sys
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import observability_rules_cli


class ObservabilityRulesCliTest(unittest.TestCase):
    def test_packaging_prometheus_rules_cover_required_alerts_and_metrics(self):
        content = observability_rules_cli.read_rules(observability_rules_cli.DEFAULT_RULES)
        results = observability_rules_cli.validate_rules(content)

        self.assertEqual(observability_rules_cli.result_exit_code(results), 0, results)

    def test_missing_required_alert_fails_validation(self):
        content = """
groups:
  - name: vela.test
    rules:
      - alert: VelaAPIDown
        expr: up{job="vela"} == 0
"""

        results = observability_rules_cli.validate_rules(content)

        self.assertEqual(observability_rules_cli.result_exit_code(results), 1)
        self.assertTrue(any(result.name == "Required alerts" and result.status == "fail" for result in results))


if __name__ == "__main__":
    unittest.main()

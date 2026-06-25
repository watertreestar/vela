import pathlib
import sys
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import alertmanager_config_cli


class AlertmanagerConfigCliTest(unittest.TestCase):
    def test_packaging_alertmanager_config_covers_required_routes(self):
        content = alertmanager_config_cli.read_config(alertmanager_config_cli.DEFAULT_CONFIG)
        results = alertmanager_config_cli.validate_config(content)

        self.assertEqual(alertmanager_config_cli.result_exit_code(results), 0, results)

    def test_missing_receiver_fails_validation(self):
        content = """
route:
  receiver: packaging-default
receivers:
  - name: packaging-default
    webhook_configs:
      - url: http://alert-router.internal/default
        send_resolved: true
"""

        results = alertmanager_config_cli.validate_config(content)

        self.assertEqual(alertmanager_config_cli.result_exit_code(results), 1)
        self.assertTrue(any(result.name == "Required receivers" and result.status == "fail" for result in results))

    def test_undefined_receiver_reference_fails_validation(self):
        content = """
route:
  receiver: packaging-missing
receivers:
  - name: packaging-default
    webhook_configs:
      - url: http://alert-router.internal/default
        send_resolved: true
"""

        results = alertmanager_config_cli.validate_config(content)

        self.assertEqual(alertmanager_config_cli.result_exit_code(results), 1)
        self.assertTrue(any(result.name == "Route receiver references" and result.status == "fail" for result in results))


if __name__ == "__main__":
    unittest.main()

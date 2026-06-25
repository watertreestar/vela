import pathlib
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import notification_routing_cli


def parse_args(*values):
    return notification_routing_cli.build_parser().parse_args(["check", *values])


NOTIFICATIONS = [
    {
        "id": "n-critical",
        "type": "runner.offline",
        "severity": "critical",
        "title": "Runner offline",
        "message": "runner heartbeat timed out",
        "resourceType": "runner",
        "resourceId": "runner-1",
    },
    {
        "id": "n-warning",
        "type": "release.blocked",
        "severity": "warning",
        "title": "Release blocked",
        "message": "gate blocked",
    },
]


class NotificationRoutingCliTest(unittest.TestCase):
    def test_valid_routes_match_notifications_without_sending_in_dry_run(self):
        with (
            patch.object(notification_routing_cli, "request_json", return_value=NOTIFICATIONS),
            patch.object(notification_routing_cli, "post_json") as post_json,
        ):
            results = notification_routing_cli.run_checks(
                parse_args(
                    "--api-url",
                    "http://api.test",
                    "--route",
                    "critical=https://hooks.test/critical",
                    "--route",
                    "warning=https://hooks.test/warning",
                    "--require-severity",
                    "critical",
                )
            )

        self.assertEqual(notification_routing_cli.result_exit_code(results, fail_on_warning=False), 0, results)
        post_json.assert_not_called()
        self.assertTrue(any(result.name == "Notification route coverage" and result.status == "ok" for result in results))
        self.assertTrue(any(result.name == "Notification dispatch" and result.status == "ok" and "dryRun=2" in result.detail for result in results))

    def test_required_severity_route_is_missing(self):
        with patch.object(notification_routing_cli, "request_json", return_value=NOTIFICATIONS):
            results = notification_routing_cli.run_checks(
                parse_args("--api-url", "http://api.test", "--route", "warning=https://hooks.test/warning", "--require-severity", "critical")
            )

        self.assertEqual(notification_routing_cli.result_exit_code(results, fail_on_warning=False), 1)
        self.assertTrue(any(result.name == "Notification route coverage" and result.status == "fail" for result in results))

    def test_send_posts_matching_notifications_to_webhooks(self):
        sent = []

        def fake_post_json(url, payload, timeout):
            sent.append((url, payload))
            return 202

        with (
            patch.object(notification_routing_cli, "request_json", return_value=NOTIFICATIONS),
            patch.object(notification_routing_cli, "post_json", side_effect=fake_post_json),
        ):
            results = notification_routing_cli.run_checks(
                parse_args(
                    "--api-url",
                    "http://api.test",
                    "--route",
                    "critical=https://hooks.test/critical",
                    "--route",
                    "warning=https://hooks.test/warning",
                    "--require-severity",
                    "critical",
                    "--send",
                )
            )

        self.assertEqual(notification_routing_cli.result_exit_code(results, fail_on_warning=False), 0, results)
        self.assertEqual([item[0] for item in sent], ["https://hooks.test/critical", "https://hooks.test/warning"])
        self.assertEqual(sent[0][1]["schema"], "packaging.notification.dispatch/v1")
        self.assertEqual(sent[0][1]["notification"]["id"], "n-critical")

    def test_invalid_notification_severity_fails(self):
        bad_notifications = [{"id": "n-bad", "severity": "urgent", "title": "bad"}]

        with patch.object(notification_routing_cli, "request_json", return_value=bad_notifications):
            results = notification_routing_cli.run_checks(parse_args("--api-url", "http://api.test"))

        self.assertEqual(notification_routing_cli.result_exit_code(results, fail_on_warning=False), 1)
        self.assertTrue(any(result.name == "Notification severity contract" and result.status == "fail" for result in results))


if __name__ == "__main__":
    unittest.main()

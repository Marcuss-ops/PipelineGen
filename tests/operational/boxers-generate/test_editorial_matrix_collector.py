import importlib.util
import pathlib
import unittest
from unittest import mock


SCRIPT = pathlib.Path(__file__).with_name("editorial_matrix_e2e.py")
SPEC = importlib.util.spec_from_file_location("editorial_matrix_e2e", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


class CollectorRateLimitTest(unittest.TestCase):
    def test_retries_get_only_after_two_rate_limits(self):
        calls = []

        def fake_request(method, path, payload=None):
            calls.append((method, path, payload))
            if len(calls) < 3:
                return 429, {"_retry_after": "1"}
            return 200, {"status": "SUCCEEDED"}

        with mock.patch.object(MODULE.time, "sleep") as sleep:
            result = MODULE.wait("job-1", request_fn=fake_request)

        self.assertEqual(result["status"], "SUCCEEDED")
        self.assertEqual(result["_collection"], {"poll_attempts": 3, "http_429": 2})
        self.assertEqual([call[0] for call in calls], ["GET", "GET", "GET"])
        self.assertEqual(sleep.call_count, 2)
        self.assertEqual(calls[0][2], None)


if __name__ == "__main__":
    unittest.main()

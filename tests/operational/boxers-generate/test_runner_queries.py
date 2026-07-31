#!/usr/bin/env python3
"""Regression tests for the boxer runner's lifecycle-state SSOT query."""

import unittest
from pathlib import Path


RUNNER = Path(__file__).with_name("run.sh")


class RunnerLifecycleStateTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.source = RUNNER.read_text(encoding="utf-8")

    def test_active_queries_use_canonical_lifecycle_state(self):
        self.assertNotIn("lifecycle_status='ACTIVE'", self.source)
        self.assertNotIn('lifecycle_status = \'ACTIVE\'', self.source)
        self.assertGreaterEqual(self.source.count("lifecycle_state='ACTIVE'"), 2)


if __name__ == "__main__":
    unittest.main()

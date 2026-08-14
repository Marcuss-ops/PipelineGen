#!/usr/bin/env python3
"""Black-box tests for the VERIFY CACHE LIVE CERTIFICATION harness."""

from __future__ import annotations

import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).parents[2]
SCRIPT = ROOT / "scripts/ci/verify-cache-certification.py"


class VerifyCacheCertificationTests(unittest.TestCase):
    def test_certification_script_reports_certified(self):
        result = subprocess.run(
            [sys.executable, str(SCRIPT)],
            cwd=ROOT,
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("CERTIFIED", result.stdout)
        self.assertNotIn("FAIL ", result.stdout)

    def test_certification_script_fails_closed_on_missing_runner(self):
        result = subprocess.run(
            [sys.executable, str(SCRIPT), "--component-runner", str(ROOT / "does-not-exist.py")],
            cwd=ROOT,
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
        self.assertIn("CONFIG_ERROR", result.stderr)


if __name__ == "__main__":
    unittest.main()

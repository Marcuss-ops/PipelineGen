#!/usr/bin/env python3
import tempfile
import unittest
import sys
import importlib.util
from pathlib import Path

module_path = Path(__file__).with_name("verify-no-policy-hardcoding.py")
spec = importlib.util.spec_from_file_location("verify_no_policy_hardcoding", module_path)
gate = importlib.util.module_from_spec(spec)
assert spec.loader is not None
spec.loader.exec_module(gate)


class VerifyNoPolicyHardcodingContract(unittest.TestCase):
    def test_detects_multiline_policy_marker(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            target = root / "internal" / "application" / "bad.go"
            target.parent.mkdir(parents=True)
            target.write_text(
                "var policy = map[string]struct{}{\n"
                "\t\"genericStopWords\": {},\n"
                "}\n",
                encoding="utf-8",
            )
            self.assertTrue(gate.scan(root))

    def test_clean_registry_map_is_allowed(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            target = root / "internal" / "application" / "registry.go"
            target.parent.mkdir(parents=True)
            target.write_text(
                "var routes = map[string]Handler{\"/health\": health}\n",
                encoding="utf-8",
            )
            self.assertEqual(gate.scan(root), [])


if __name__ == "__main__":
    unittest.main()

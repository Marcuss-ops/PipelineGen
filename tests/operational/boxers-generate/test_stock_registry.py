#!/usr/bin/env python3
"""Regression tests for the shared boxer stock registry contract."""

import copy
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).parent
REGISTRY_PATH = ROOT / "fixtures" / "boxers_stock_registry.json"
MODULE_SPEC = importlib.util.spec_from_file_location("stock_registry", ROOT / "stock_registry.py")
stock_registry = importlib.util.module_from_spec(MODULE_SPEC)
assert MODULE_SPEC.loader is not None
MODULE_SPEC.loader.exec_module(stock_registry)


class StockRegistryTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.registry = stock_registry.load_json(REGISTRY_PATH)
        cls.resolved = stock_registry.resolve_registry(cls.registry)

    def test_duplicate_asset_between_subjects_is_rejected(self):
        duplicate = copy.deepcopy(self.registry)
        duplicate["boxers"]["muhammad_ali"]["assets"]["fight"]["asset_id"] = (
            duplicate["boxers"]["mike_tyson"]["assets"]["fight"]["asset_id"]
        )
        with self.assertRaisesRegex(ValueError, "assigned to both"):
            stock_registry.validate_registry(duplicate)

    def test_duplicate_asset_between_roles_is_rejected(self):
        duplicate = copy.deepcopy(self.registry)
        duplicate["boxers"]["mike_tyson"]["assets"]["interview"]["asset_id"] = (
            duplicate["boxers"]["mike_tyson"]["assets"]["fight"]["asset_id"]
        )
        with self.assertRaisesRegex(ValueError, "assigned to both"):
            stock_registry.validate_registry(duplicate)

    def test_materialization_rejects_literal_asset_outside_registry(self):
        payload = {"output": {"stock_bindings": [{"asset_id": "asset-from-another-subject"}]}}
        with self.assertRaisesRegex(ValueError, "outside resolved registry"):
            stock_registry.materialize(payload, self.resolved)

    def test_positive_scenario_materializes_by_scene_order(self):
        source = ROOT / "scenarios" / "top5_financial_stories_multilang.json"
        payload = stock_registry.load_json(source)
        materialized = stock_registry.materialize(payload, self.resolved)
        expected = [entry["asset_id"] for entry in stock_registry.scene_expectations(self.resolved)]
        first_item = materialized["items"][0]
        actual = [
            binding["asset_id"]
            for binding in first_item["output"]["stock_bindings"]
        ]
        self.assertEqual(actual, expected)

    def test_negative_scenario_preserves_cross_subject_swap(self):
        source = ROOT / "scenarios" / "top5_financial_stories_multilang_neg.json"
        payload = stock_registry.materialize(stock_registry.load_json(source), self.resolved)
        expected = stock_registry.scene_expectations(self.resolved)[0]["asset_id"]
        swapped = payload["items"][0]["output"]["stock_bindings"][0]["asset_id"]
        self.assertNotEqual(swapped, expected)
        self.assertEqual(
            swapped,
            self.resolved["boxers"]["muhammad_ali"]["assets"]["fight"]["asset_id"],
        )

    def test_materialize_cli_writes_resolved_payload(self):
        source = ROOT / "scenarios" / "08_600w_it.json"
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "materialized.json"
            self.assertEqual(
                stock_registry.main([
                    "materialize",
                    "--resolved", str(REGISTRY_PATH),
                    "--input", str(source),
                    "--output", str(output),
                ]),
                0,
            )
            self.assertTrue(json.loads(output.read_text(encoding="utf-8"))["items"])


if __name__ == "__main__":
    unittest.main()

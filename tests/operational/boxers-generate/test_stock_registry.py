#!/usr/bin/env python3
"""Regression tests for the shared boxer stock registry contract."""

import copy
import importlib.util
import json
import sqlite3
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
        with self.assertRaisesRegex(ValueError, "unavailable or unregistered"):
            stock_registry.materialize(payload, self.resolved)

    def test_pacquiao_requires_all_three_roles(self):
        incomplete = copy.deepcopy(self.registry)
        del incomplete["boxers"]["manny_pacquiao"]["assets"]["training"]
        with self.assertRaisesRegex(ValueError, "requires three validated stock assets"):
            stock_registry.validate_registry(incomplete)

    def test_pacquiao_fixture_uses_canonical_asset_ids(self):
        assets = self.registry["boxers"]["manny_pacquiao"]["assets"]
        self.assertEqual(assets["fight"]["asset_id"], "yt_6VtSrG1hs9U_119_164_v1")
        self.assertEqual(assets["interview"]["asset_id"], "yt_6VtSrG1hs9U_299_350_v1")
        self.assertEqual(assets["training"]["asset_id"], "yt_6VtSrG1hs9U_196_239_v1")

    def test_direct_pacquiao_scenario_materializes_without_tyson_ids(self):
        source = ROOT / "scenarios" / "04_direct_stock_bindings.json"
        payload = stock_registry.materialize(stock_registry.load_json(source), self.resolved)
        bindings = payload["items"][0]["output"]["stock_bindings"]
        self.assertEqual(
            [binding["asset_id"] for binding in bindings],
            [
                "yt_6VtSrG1hs9U_119_164_v1",
                "yt_6VtSrG1hs9U_299_350_v1",
                "yt_6VtSrG1hs9U_196_239_v1",
            ],
        )
        self.assertTrue(all(not binding.get("fallback", False) for binding in bindings))

    def _pacquiao_only_registry(self):
        registry = copy.deepcopy(self.registry)
        registry["scene_order"] = ["manny_pacquiao"]
        registry["boxers"] = {"manny_pacquiao": registry["boxers"]["manny_pacquiao"]}
        return registry

    def _write_pacquiao_db(self, directory, *, bad_role=None, bad_value=None, bad_field=None, metadata_role=None, source_provider=None):
        registry = self._pacquiao_only_registry()
        db_path = Path(directory) / "assets.sqlite"
        with sqlite3.connect(db_path) as connection:
            connection.execute(
                """CREATE TABLE media_assets (
                    id TEXT PRIMARY KEY,
                    lifecycle_state TEXT NOT NULL,
                    source TEXT NOT NULL,
                    source_provider TEXT,
                    index_state TEXT NOT NULL,
                    drive_file_id TEXT NOT NULL,
                    drive_link TEXT NOT NULL,
                    name TEXT NOT NULL,
                    metadata_json TEXT NOT NULL DEFAULT '{}'
                )"""
            )
            for role, asset in registry["boxers"]["manny_pacquiao"]["assets"].items():
                is_bad_role = bad_role == role
                source = bad_value if is_bad_role and bad_field == "source" else "youtube"
                drive_link = bad_value if is_bad_role and bad_field == "drive_link" else asset["drive_link"]
                lifecycle_state = bad_value if is_bad_role and bad_field == "lifecycle_state" else "ACTIVE"
                index_state = bad_value if is_bad_role and bad_field == "index_state" else "INDEXED"
                drive_file_id = bad_value if is_bad_role and bad_field == "drive_file_id" else "drive-file-" + role
                metadata = "{}"
                if metadata_role is not None and is_bad_role:
                    metadata = json.dumps({"role": metadata_role})
                connection.execute(
                    "INSERT INTO media_assets "
                    "(id, lifecycle_state, index_state, source, source_provider, drive_file_id, drive_link, name, metadata_json) "
                    "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
                    (
                        asset["asset_id"], lifecycle_state, index_state, source,
                        source_provider if is_bad_role and source_provider is not None else "youtube",
                        drive_file_id,
                        drive_link, f"Manny Pacquiao {role}", metadata,
                    ),
                )
            connection.commit()
        return registry, db_path

    def test_pacquiao_requires_active_state_not_shadow_status(self):
        with tempfile.TemporaryDirectory() as directory:
            registry, db_path = self._write_pacquiao_db(
                directory,
                bad_role="training",
                bad_value="DELETED",
                bad_field="lifecycle_state",
            )
            with self.assertRaisesRegex(ValueError, "lifecycle_state.*expected ACTIVE"):
                stock_registry.resolve_registry(registry, str(db_path))

    def test_pacquiao_requires_indexed_state(self):
        with tempfile.TemporaryDirectory() as directory:
            registry, db_path = self._write_pacquiao_db(
                directory,
                bad_role="training",
                bad_value="DISCOVERED",
                bad_field="index_state",
            )
            with self.assertRaisesRegex(ValueError, "index_state.*expected INDEXED"):
                stock_registry.resolve_registry(registry, str(db_path))

    def test_pacquiao_requires_drive_file_id(self):
        with tempfile.TemporaryDirectory() as directory:
            registry, db_path = self._write_pacquiao_db(
                directory,
                bad_role="fight",
                bad_value="",
                bad_field="drive_file_id",
            )
            with self.assertRaisesRegex(ValueError, "empty drive_file_id"):
                stock_registry.resolve_registry(registry, str(db_path))

    def test_pacquiao_rejects_wrong_source_and_empty_drive_link(self):
        for bad_role, bad_value, expected in (
            ("fight", "artlist", "source"),
            ("interview", "", "empty drive_link"),
        ):
            with self.subTest(expected=expected), tempfile.TemporaryDirectory() as directory:
                registry, db_path = self._write_pacquiao_db(
                    directory,
                    bad_role=bad_role,
                    bad_value=bad_value,
                    bad_field="source" if expected == "source" else "drive_link",
                )
                with self.assertRaisesRegex(ValueError, expected):
                    stock_registry.resolve_registry(registry, str(db_path))

    def test_pacquiao_rejects_non_youtube_source_provider(self):
        with tempfile.TemporaryDirectory() as directory:
            registry, db_path = self._write_pacquiao_db(
                directory,
                bad_role="fight",
                bad_value="artlist",
                bad_field="source_provider",
                source_provider="artlist",
            )
            with self.assertRaisesRegex(ValueError, "provider.*artlist"):
                stock_registry.resolve_registry(registry, str(db_path))

    def test_pacquiao_validates_declared_role_metadata(self):
        with tempfile.TemporaryDirectory() as directory:
            registry, db_path = self._write_pacquiao_db(
                directory,
                bad_role="training",
                metadata_role="interview",
            )
            with self.assertRaisesRegex(ValueError, "metadata role.*expected 'training'"):
                stock_registry.resolve_registry(registry, str(db_path))

    def test_top_five_preflight_resolves_registered_subjects(self):
        source = ROOT / "scenarios" / "top5_financial_stories_multilang.json"
        result = stock_registry.preflight(stock_registry.load_json(source), self.resolved)
        self.assertEqual(result, {"status": "PASS", "missing": []})

    def test_direct_pacquiao_preflight_passes(self):
        source = ROOT / "scenarios" / "04_direct_stock_bindings.json"
        result = stock_registry.preflight(stock_registry.load_json(source), self.resolved)
        self.assertEqual(result, {"status": "PASS", "missing": []})

    def test_preflight_cli_returns_blocked_exit_code_without_job_or_voiceover_fields(self):
        source = ROOT / "scenarios" / "top5_financial_stories_multilang.json"
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "preflight.json"
            self.assertEqual(
                stock_registry.main([
                    "preflight",
                    "--resolved", str(REGISTRY_PATH),
                    "--input", str(source),
                    "--output", str(output),
                ]),
                0,
            )
            result = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(result["status"], "PASS")
            self.assertNotIn("job_id", result)
            self.assertNotIn("voiceover_requests", result)

    def test_preflight_blocks_deleted_db_asset_using_lifecycle_state(self):
        registry = copy.deepcopy(self.registry)
        registry["boxers"] = {"manny_pacquiao": registry["boxers"]["manny_pacquiao"]}
        registry["scene_order"] = ["manny_pacquiao"]
        payload = {
            "items": [{"output": {"stock_bindings": [{
                "asset_id": "{{stock.manny_pacquiao.fight.asset_id}}"
            }]}}]
        }
        with tempfile.TemporaryDirectory() as directory:
            db_path = Path(directory) / "assets.sqlite"
            with sqlite3.connect(db_path) as connection:
                connection.execute(
                    "CREATE TABLE media_assets (id TEXT PRIMARY KEY, lifecycle_state TEXT NOT NULL, "
                    "source TEXT NOT NULL, drive_link TEXT NOT NULL, name TEXT NOT NULL)"
                )
                asset = registry["boxers"]["manny_pacquiao"]["assets"]["fight"]
                connection.execute(
                    "INSERT INTO media_assets VALUES (?, 'DELETED', 'youtube', ?, 'Manny Pacquiao fight')",
                    (asset["asset_id"], asset["drive_link"]),
                )
                connection.commit()
            # The payload must be scanned, not the registry itself.
            result = stock_registry.preflight(payload, stock_registry.resolve_registry(self.registry), str(db_path))
        self.assertEqual(result, {
            "status": "BLOCKED",
            "missing": [{
                "subject": "Manny Pacquiao",
                "role": "fight",
                "reason": "NO_ACTIVE_ASSET",
            }],
        })

    def test_top_five_materialization_uses_registered_stock_tokens(self):
        source = ROOT / "scenarios" / "top5_financial_stories_multilang.json"
        materialized = stock_registry.materialize(stock_registry.load_json(source), self.resolved)
        self.assertEqual(len(materialized["items"]), 10)

    def test_floyd_and_sugar_ray_use_verified_stock(self):
        self.assertEqual(
            self.registry["boxers"]["floyd_mayweather"]["assets"]["fight"]["asset_id"],
            "planner:33a65feeb627695e:3",
        )
        self.assertEqual(
            self.registry["boxers"]["sugar_ray_robinson"]["assets"]["fight"]["asset_id"],
            "planner:c7074c575f1975fb:3",
        )
        self.assertEqual(
            [entry["boxer_key"] for entry in stock_registry.scene_expectations(self.resolved)],
            ["mike_tyson", "muhammad_ali", "manny_pacquiao", "floyd_mayweather", "sugar_ray_robinson"],
        )

    def test_cross_subject_swap_resolves_only_declared_subject(self):
        payload = {
            "output": {
                "stock_bindings": [{
                    "asset_id": "{{stock.muhammad_ali.fight.asset_id}}",
                    "drive_link": "{{stock.muhammad_ali.fight.drive_link}}",
                }]
            }
        }
        materialized = stock_registry.materialize(payload, self.resolved)
        self.assertEqual(
            materialized["output"]["stock_bindings"][0]["asset_id"],
            "planner:96d07f0da43de615:0",
        )

    def test_materialize_cli_writes_resolved_payload(self):
        source = ROOT / "scenarios" / "04_direct_stock_bindings.json"
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

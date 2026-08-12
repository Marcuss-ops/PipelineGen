from __future__ import annotations

import contextlib
import io
import json
import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from artlist_scale_config import Settings, chunks, env_bool, env_float, env_int
from artlist_scale_reporting import emit_summary
from artlist_scale_e2e import ScaleRunner
from artlist_scale_e2e_entry import FailClosedScaleRunner


class ArtlistScaleModuleContractTest(unittest.TestCase):
    def test_fail_closed_entrypoint_keeps_runner_hooks(self) -> None:
        self.assertTrue(issubclass(FailClosedScaleRunner, ScaleRunner))
        for name in ("run_phase", "phase_items", "replay", "execute"):
            self.assertTrue(callable(getattr(FailClosedScaleRunner, name)))

    def test_diagnostics_and_identity_shapes(self) -> None:
        ok, failed = ScaleRunner.diagnostics_ok({"scraper": {"ok": True}, "browser": {"ok": False}})
        self.assertFalse(ok)
        self.assertIn("browser", failed)
        snapshot = ScaleRunner.identity_snapshot([{
            "id": "asset-1", "drive_file_id": "drive-1", "drive_link": "https://drive/1",
            "file_hash": "hash-1", "source_url": "https://source/1", "download_link": "https://download/1",
        }])
        self.assertEqual(snapshot["asset-1"]["file_hash"], "hash-1")

    def test_fail_closed_run_phase_aborts_after_new_failure(self) -> None:
        with tempfile.TemporaryDirectory() as report_dir:
            with patch.dict(os.environ, {"ARTLIST_SCALE_REPORT_DIR": report_dir}, clear=True):
                runner = FailClosedScaleRunner(Settings.load())
            with patch("artlist_scale_e2e.run_phase") as run_phase_mock:
                run_phase_mock.side_effect = lambda *_args, **_kwargs: (runner.fail("phase failed"), [])[1]
                with self.assertRaisesRegex(RuntimeError, "downstream quota work aborted"):
                    runner.run_phase("first")

    def test_summary_reporting_preserves_success_output_and_exit_code(self) -> None:
        with tempfile.TemporaryDirectory() as report_dir:
            with patch.dict(os.environ, {"ARTLIST_SCALE_REPORT_DIR": report_dir}, clear=True):
                runner = ScaleRunner(Settings.load())
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                result = emit_summary(runner, {"ok": True, "failures": []})
            self.assertEqual(result, 0)
            self.assertIn("PASS: Artlist scale, Drive, VLM/Qdrant and replay dedup checks succeeded", output.getvalue())
            self.assertEqual(json.loads((Path(report_dir) / "summary.json").read_text()), {"ok": True, "failures": []})

    def test_summary_reporting_preserves_failure_output_and_exit_code(self) -> None:
        with tempfile.TemporaryDirectory() as report_dir:
            with patch.dict(os.environ, {"ARTLIST_SCALE_REPORT_DIR": report_dir}, clear=True):
                runner = ScaleRunner(Settings.load())
            runner.failures.append("synthetic failure")
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                result = emit_summary(runner, {"ok": False, "failures": runner.failures})
            self.assertEqual(result, 1)
            self.assertIn("FAILURES:", output.getvalue())
            self.assertIn("  - synthetic failure", output.getvalue())

    def test_configuration_and_pure_helpers(self) -> None:
        with tempfile.TemporaryDirectory() as report_dir:
            values = {"ARTLIST_SCALE_KEYWORDS": "alpha, beta", "ARTLIST_SCALE_CLIPS_PER_KEYWORD": "3", "ARTLIST_SCALE_CLIP_CONCURRENCY": "2", "ARTLIST_SCALE_REPORT_DIR": report_dir, "ARTLIST_SCALE_ADMIN_BIN": "python3 -m cmd.admin"}
            with patch.dict(os.environ, values, clear=True):
                settings = Settings.load()
            self.assertEqual(settings.keywords, ["alpha", "beta"])
            self.assertEqual(settings.clips_per_keyword, 3)
            self.assertEqual(settings.clip_concurrency, 2)
            self.assertEqual(settings.report_dir, Path(report_dir))
            self.assertEqual(settings.admin_command, ["python3", "-m", "cmd.admin"])
        with patch.dict(os.environ, {"TEST_INT": "4", "TEST_FLOAT": "1.5", "TEST_BOOL": "off"}, clear=False):
            self.assertEqual(env_int("TEST_INT", 1, minimum=1), 4)
            self.assertEqual(env_float("TEST_FLOAT", 1.0), 1.5)
            self.assertFalse(env_bool("TEST_BOOL", True))
        self.assertEqual(list(chunks(["a", "b", "c"], 2)), [["a", "b"], ["c"]])


if __name__ == "__main__":
    unittest.main()

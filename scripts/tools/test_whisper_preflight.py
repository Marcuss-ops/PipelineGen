#!/usr/bin/env python3
"""Unit tests for scripts/tools/whisper_preflight.py."""

from __future__ import annotations

import contextlib
import importlib.util
import io
import json
import os
import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts/tools/whisper_preflight.py"
SPEC = importlib.util.spec_from_file_location("whisper_preflight", SCRIPT)
assert SPEC and SPEC.loader
preflight = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(preflight)


class WhisperPreflightTests(unittest.TestCase):
    def test_model_name_is_valid_without_download(self):
        with mock.patch.dict(os.environ, {"VELOX_WHISPER_MODEL": "base"}, clear=False):
            model, details, error = preflight._model_check()
        self.assertEqual(model, "base")
        self.assertEqual(details["kind"], "name")
        self.assertIsNone(error)

    def test_unknown_model_name_is_rejected_without_download(self):
        with mock.patch.dict(
            os.environ, {"VELOX_WHISPER_MODEL": "not-a-whisper-model"}, clear=False
        ):
            model, details, error = preflight._model_check()
        self.assertIsNone(model)
        self.assertFalse(details["configured"])
        self.assertIn("unsupported Whisper model name", error or "")

    def test_model_path_must_exist(self):
        with mock.patch.dict(
            os.environ, {"VELOX_WHISPER_MODEL": "/missing/whisper-model"}, clear=False
        ):
            model, details, error = preflight._model_check()
        self.assertIsNone(model)
        self.assertFalse(details["path_exists"])
        self.assertIn("does not exist", error or "")

    def test_model_path_accepts_existing_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            with mock.patch.dict(
                os.environ, {"VELOX_WHISPER_MODEL": directory}, clear=False
            ):
                model, details, error = preflight._model_check()
        self.assertEqual(model, directory)
        self.assertTrue(details["path_exists"])
        self.assertIsNone(error)

    def test_cuda_library_check_reports_all_required_sonames(self):
        with mock.patch.object(preflight, "_library_dirs", return_value=[]), mock.patch(
            "ctypes.CDLL", side_effect=OSError("not loaded")
        ):
            result = preflight._check_cuda_libraries()
        self.assertFalse(result["ok"])
        self.assertEqual(result["cublas"]["soname"], "libcublas.so.12")
        self.assertEqual(result["cuda_nvrtc"]["soname"], "libnvrtc.so.12")
        self.assertEqual(result["cudnn"]["soname"], "libcudnn.so.9")

    def test_cuda_library_check_can_pass_with_present_and_loaded_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for soname in preflight.CUDA_LIBRARIES.values():
                (root / soname).touch()
            with mock.patch.object(preflight, "_library_dirs", return_value=[root]), mock.patch(
                "ctypes.CDLL", return_value=object()
            ):
                result = preflight._check_cuda_libraries()
        self.assertTrue(result["ok"])
        self.assertTrue(all(result[name]["present"] for name in preflight.CUDA_LIBRARIES))
        self.assertTrue(all(result[name]["loaded"] for name in preflight.CUDA_LIBRARIES))

    def test_main_cpu_emits_success_json_without_cuda(self):
        versions = {package: "test" for package in preflight.LOCK_PACKAGES}
        fake_ct2 = types.SimpleNamespace(get_cuda_device_count=lambda: 0)
        output = io.StringIO()
        with (
            mock.patch.dict(os.environ, {"VELOX_WHISPER_DEVICE": "cpu"}, clear=False),
            mock.patch.object(preflight, "prepare_cuda_runtime"),
            mock.patch.object(preflight, "_package_versions", return_value=(versions, {})),
            mock.patch.object(preflight, "_check_cuda_libraries", return_value={"ok": False}),
            mock.patch.dict(sys.modules, {"ctranslate2": fake_ct2}),
            contextlib.redirect_stdout(output),
        ):
            exit_code = preflight.main()
        report = json.loads(output.getvalue())
        self.assertEqual(exit_code, 0)
        self.assertTrue(report["ok"])
        self.assertEqual(report["device"], "cpu")
        self.assertEqual(report["compute_type"], "int8")

    def test_main_auto_falls_back_to_cpu_without_cuda(self):
        versions = {package: "test" for package in preflight.LOCK_PACKAGES}
        fake_ct2 = types.SimpleNamespace(get_cuda_device_count=lambda: 0)
        output = io.StringIO()
        with (
            mock.patch.dict(os.environ, {"VELOX_WHISPER_DEVICE": "auto"}, clear=False),
            mock.patch.object(preflight, "prepare_cuda_runtime"),
            mock.patch.object(preflight, "_package_versions", return_value=(versions, {})),
            mock.patch.object(preflight, "_check_cuda_libraries", return_value={"ok": False}),
            mock.patch.dict(sys.modules, {"ctranslate2": fake_ct2}),
            contextlib.redirect_stdout(output),
        ):
            exit_code = preflight.main()
        report = json.loads(output.getvalue())
        self.assertEqual(exit_code, 0)
        self.assertTrue(report["ok"])
        self.assertEqual(report["device"], "cpu")
        self.assertTrue(report["warnings"])

    def test_main_cuda_fails_closed_without_usable_cuda(self):
        versions = {package: "test" for package in preflight.LOCK_PACKAGES}
        fake_ct2 = types.SimpleNamespace(get_cuda_device_count=lambda: 0)
        output = io.StringIO()
        with (
            mock.patch.dict(os.environ, {"VELOX_WHISPER_DEVICE": "cuda"}, clear=False),
            mock.patch.object(preflight, "prepare_cuda_runtime"),
            mock.patch.object(preflight, "_package_versions", return_value=(versions, {})),
            mock.patch.object(preflight, "_check_cuda_libraries", return_value={"ok": False}),
            mock.patch.dict(sys.modules, {"ctranslate2": fake_ct2}),
            contextlib.redirect_stdout(output),
        ):
            exit_code = preflight.main()
        report = json.loads(output.getvalue())
        self.assertEqual(exit_code, 2)
        self.assertFalse(report["ok"])
        self.assertEqual(report["device"], "cuda")

    def test_lockfile_reader_requires_canonical_runtime_pins(self):
        with tempfile.TemporaryDirectory() as directory:
            lockfile = Path(directory) / "whisper.lock.txt"
            lockfile.write_text("faster-whisper==1.2.1\n", encoding="utf-8")
            with mock.patch.dict(
                os.environ, {"VELOX_WHISPER_LOCKFILE": str(lockfile)}, clear=False
            ):
                pins, path, error = preflight._read_lock_pins()
        self.assertEqual(pins, {"faster-whisper": "1.2.1"})
        self.assertEqual(path, str(lockfile))
        self.assertIn("misses required pins", error or "")


if __name__ == "__main__":
    unittest.main()

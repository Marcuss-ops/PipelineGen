#!/usr/bin/env python3
"""Unit tests for the portable remote client (stdlib only)."""

from __future__ import annotations

import importlib.util
import os
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).with_name("chronon.py")
SPEC = importlib.util.spec_from_file_location("chronon_remote", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
chronon = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(chronon)


class ResolveDownloadPathTest(unittest.TestCase):
    def test_absolute_path_is_preserved(self) -> None:
        self.assertEqual(
            chronon.resolve_download_path("/tmp/chronon-download-test", "asset-1"),
            "/tmp/chronon-download-test",
        )

    def test_relative_name_is_preserved(self) -> None:
        self.assertEqual(
            chronon.resolve_download_path("clip.mp4", "asset-1"), "clip.mp4"
        )

    def test_existing_directory_gets_asset_name(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            self.assertEqual(
                chronon.resolve_download_path(directory, "asset-1"),
                os.path.join(directory, "asset-1.mp4"),
            )

    def test_trailing_separator_gets_asset_name(self) -> None:
        self.assertEqual(
            chronon.resolve_download_path("/tmp/", "asset-1"), "/tmp/asset-1.mp4"
        )

    def test_empty_output_uses_asset_id(self) -> None:
        self.assertEqual(chronon.resolve_download_path("", "asset-1"), "asset-1.mp4")

    def test_home_is_expanded(self) -> None:
        resolved = chronon.resolve_download_path("~/clip.mp4", "asset-1")
        self.assertTrue(resolved.startswith(os.path.expanduser("~") + os.sep))
        self.assertTrue(resolved.endswith("clip.mp4"))


class ClipLikeTest(unittest.TestCase):
    def test_audio_is_clip_like(self) -> None:
        self.assertTrue(
            chronon.is_clip_like(
                {"id": "1lEqAxjNWFXe3UpKNOpJrA2EU9izLPML2", "media_type": "audio"}
            )
        )

    def test_image_is_not_clip_like(self) -> None:
        self.assertFalse(
            chronon.is_clip_like({"id": "img-1", "media_type": "image"})
        )
        self.assertFalse(
            chronon.is_clip_like({"id": "img-1", "source": "internet_images"})
        )

    def test_path_id_is_not_clip_like(self) -> None:
        self.assertFalse(
            chronon.is_clip_like(
                {"id": "/tmp/pipelinegen-final-audio-1.m4a", "media_type": "audio"}
            )
        )


class InferSourceTest(unittest.TestCase):
    def test_youtube_prefix(self) -> None:
        self.assertEqual(chronon.infer_source("yt_abc"), "youtube")

    def test_voiceover_prefix(self) -> None:
        self.assertEqual(chronon.infer_source("vo_abc"), "voiceover")

    def test_other_defaults_to_local(self) -> None:
        self.assertEqual(chronon.infer_source("stock_abc"), "local")


if __name__ == "__main__":
    unittest.main()

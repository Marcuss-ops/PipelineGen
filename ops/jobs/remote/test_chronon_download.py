#!/usr/bin/env python3
"""Download integrity/resume tests for the portable remote client."""

from __future__ import annotations

import hashlib
import importlib.util
import os
import tempfile
import unittest
from pathlib import Path
from unittest import mock

MODULE_PATH = Path(__file__).with_name("chronon.py")
SPEC = importlib.util.spec_from_file_location("chronon_remote_download", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
chronon = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(chronon)


class FakeResponse:
    def __init__(self, body: bytes, status: int = 200, headers: dict[str, str] | None = None):
        self._body = body
        self._status = status
        self.headers = headers or {}

    def getcode(self) -> int:
        return self._status

    def read(self, size: int = -1) -> bytes:
        if size < 0:
            chunk, self._body = self._body, b""
            return chunk
        chunk, self._body = self._body[:size], self._body[size:]
        return chunk

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return False


class DownloadAssetTest(unittest.TestCase):
    def setUp(self) -> None:
        self.payload = b"video-payload-" * 2000
        self.asset = {
            "id": "yt_test",
            "source": "youtube",
            "content_hash": hashlib.sha256(self.payload).hexdigest(),
        }
        self.api = chronon.Api("http://master.test", "secret")

    def test_download_verifies_sha256_and_returns_it(self) -> None:
        captured: dict[str, object] = {}

        def fake_post(_self, path: str, extra_headers=None):
            captured["path"] = path
            captured["range"] = extra_headers
            return FakeResponse(
                self.payload,
                headers={
                    "Content-Type": "video/mp4",
                    "Content-Length": str(len(self.payload)),
                },
            )

        with tempfile.TemporaryDirectory() as directory:
            output = os.path.join(directory, "clip.mp4")
            with mock.patch.object(chronon, "fetch_asset", return_value=self.asset), mock.patch.object(
                chronon.Api, "post_stream_raw", fake_post
            ):
                size, content_type, digest = chronon.download_asset(
                    self.api, "yt_test", output
                )

            self.assertEqual(size, len(self.payload))
            self.assertEqual(content_type, "video/mp4")
            self.assertEqual(digest, self.asset["content_hash"])
            self.assertEqual(Path(output).read_bytes(), self.payload)
            self.assertIsNone(captured["range"])
            self.assertIn("/api/v1/media/clips/youtube/clips/yt_test/download", captured["path"])

    def test_resume_appends_from_part_file(self) -> None:
        prefix = self.payload[:7]
        captured: dict[str, object] = {}

        def fake_post(_self, _path: str, extra_headers=None):
            captured["range"] = extra_headers
            return FakeResponse(
                self.payload[len(prefix):],
                status=206,
                headers={
                    "Content-Type": "video/mp4",
                    "Content-Length": str(len(self.payload) - len(prefix)),
                },
            )

        with tempfile.TemporaryDirectory() as directory:
            output = os.path.join(directory, "clip.mp4")
            with open(output + ".part", "wb") as handle:
                handle.write(prefix)
            with mock.patch.object(chronon, "fetch_asset", return_value=self.asset), mock.patch.object(
                chronon.Api, "post_stream_raw", fake_post
            ):
                size, _content_type, digest = chronon.download_asset(
                    self.api, "yt_test", output, resume=True
                )

            self.assertEqual(size, len(self.payload))
            self.assertEqual(digest, self.asset["content_hash"])
            self.assertEqual(Path(output).read_bytes(), self.payload)
            self.assertEqual(captured["range"], {"Range": f"bytes={len(prefix)}-"})

    def test_hash_mismatch_fails_closed(self) -> None:
        bad_asset = dict(self.asset, content_hash="0" * 64)

        def fake_post(_self, _path: str, _extra_headers=None):
            return FakeResponse(
                self.payload,
                headers={
                    "Content-Type": "video/mp4",
                    "Content-Length": str(len(self.payload)),
                },
            )

        with tempfile.TemporaryDirectory() as directory:
            output = os.path.join(directory, "clip.mp4")
            with mock.patch.object(chronon, "fetch_asset", return_value=bad_asset), mock.patch.object(
                chronon.Api, "post_stream_raw", fake_post
            ):
                with self.assertRaises(SystemExit):
                    chronon.download_asset(self.api, "yt_test", output)

            self.assertFalse(os.path.exists(output))
            self.assertFalse(os.path.exists(output + ".part"))


if __name__ == "__main__":
    unittest.main()

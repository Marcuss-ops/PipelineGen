#!/usr/bin/env python3
"""Local coverage for the three extended `chronon.py verify` paths (CLOSE-06)."""

from __future__ import annotations

import hashlib
import importlib.util
import json
import unittest
from pathlib import Path
from unittest import mock

MODULE_PATH = Path(__file__).with_name("chronon.py")
SPEC = importlib.util.spec_from_file_location("chronon_remote_verify_extended", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
chronon = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(chronon)


class FakeResponse:
    def __init__(self, body: bytes, headers: dict[str, str] | None = None, status: int = 200):
        self._body = body
        self.headers = headers or {}
        self._status = status

    def getcode(self) -> int:
        return self._status

    def read(self, size: int = -1) -> bytes:
        if size < 0:
            chunk, self._body = self._body, b""
            return chunk
        chunk, self._body = self._body[:size], self._body[size:]
        return chunk

    def __enter__(self) -> "FakeResponse":
        return self

    def __exit__(self, *_args) -> bool:
        return False


class FakeApi:
    base_url = "http://master.test"
    secret = "secret"
    timeout = 5

    def __init__(self, probe_result=None, stream=None):
        self.probe_result = probe_result or (200, {})
        self.stream = stream
        self.probe_calls: list[tuple[str, str, bytes]] = []
        self.stream_paths: list[str] = []

    def probe(self, method: str, path: str, raw_body: bytes = b""):
        self.probe_calls.append((method, path, raw_body))
        result = (
            self.probe_result(method, path, raw_body)
            if callable(self.probe_result)
            else self.probe_result
        )
        status, payload = result
        if isinstance(payload, (dict, list)):
            return status, json.dumps(payload).encode("utf-8")
        return status, payload

    def get_stream_raw(self, path: str, extra_headers=None):
        self.stream_paths.append(path)
        return self.stream


class SemanticSearchTest(unittest.TestCase):
    def test_posts_to_the_m2m_search_route(self) -> None:
        api = FakeApi(probe_result=(200, {"items": [{"id": "asset-1"}]}))
        checks: list[dict[str, object]] = []
        chronon._verify_semantic_search(api, checks, "corrado augias")
        self.assertTrue(checks[0]["ok"])
        self.assertEqual(api.probe_calls[0][:2], ("POST", "/api/v1/media/search"))
        self.assertIn(b"corrado augias", api.probe_calls[0][2])

    def test_404_is_an_explicit_failure(self) -> None:
        api = FakeApi(probe_result=(404, {}))
        checks: list[dict[str, object]] = []
        chronon._verify_semantic_search(api, checks, "x")
        self.assertFalse(checks[0]["ok"])
        self.assertIn("non esposto", str(checks[0]["detail"]))


class ImageDownloadTest(unittest.TestCase):
    def test_verifies_hash_and_image_content_type(self) -> None:
        payload = b"image-bytes"
        digest = hashlib.sha256(payload).hexdigest()
        api = FakeApi(probe_result=(200, {"id": "img-1", "content_hash": digest}))
        checks: list[dict[str, object]] = []
        with mock.patch.object(
            chronon, "download_asset", return_value=(len(payload), "image/png", digest)
        ):
            chronon._verify_image_download(api, checks, "img-1", 5, 1000)
        self.assertTrue(checks[0]["ok"], checks[0])
        self.assertIn("img-1", str(checks[0]["detail"]))

    def test_non_image_content_type_fails(self) -> None:
        payload = b"{}"
        digest = hashlib.sha256(payload).hexdigest()
        api = FakeApi(probe_result=(200, {"id": "img-1", "content_hash": digest}))
        checks: list[dict[str, object]] = []
        with mock.patch.object(
            chronon,
            "download_asset",
            return_value=(len(payload), "application/json", digest),
        ):
            chronon._verify_image_download(api, checks, "img-1", 5, 1000)
        self.assertFalse(checks[0]["ok"])
        self.assertIn("content-type non image", str(checks[0]["detail"]))

    def test_missing_expected_hash_fails_before_download(self) -> None:
        api = FakeApi(probe_result=(200, {"id": "img-1"}))
        checks: list[dict[str, object]] = []
        with mock.patch.object(chronon, "download_asset") as download:
            chronon._verify_image_download(api, checks, "img-1", 5, 1000)
        download.assert_not_called()
        self.assertFalse(checks[0]["ok"])
        self.assertIn("SHA-256 non verificabile", str(checks[0]["detail"]))


class JobArtifactTest(unittest.TestCase):
    def test_verifies_header_hash_and_video_content_type(self) -> None:
        payload = b"final-render"
        digest = hashlib.sha256(payload).hexdigest()
        api = FakeApi(
            stream=FakeResponse(
                payload,
                headers={
                    "Content-Type": "video/mp4",
                    "Content-Length": str(len(payload)),
                    "X-Content-SHA256": digest,
                },
            )
        )
        checks: list[dict[str, object]] = []
        chronon._verify_job_artifact(api, checks, "job_1", 5, 10_000)
        self.assertTrue(checks[0]["ok"], checks[0])
        self.assertEqual(api.stream_paths, ["/api/v1/jobs/job_1/artifact"])

    def test_missing_hash_header_fails_closed(self) -> None:
        payload = b"final-render"
        api = FakeApi(
            probe_result=(200, {"result": {}}),
            stream=FakeResponse(
                payload,
                headers={"Content-Type": "video/mp4"},
            ),
        )
        checks: list[dict[str, object]] = []
        chronon._verify_job_artifact(api, checks, "job_1", 5, 10_000)
        self.assertFalse(checks[0]["ok"])
        self.assertIn("hash=assente", str(checks[0]["detail"]))


if __name__ == "__main__":
    unittest.main()

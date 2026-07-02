"""Tests for /index_bulk response format (Azione 6 — PR-AGENTE2-BULK).

Verifies the three status-derivation rules:
  - all embedded  → status="success"
  - mix            → status="partial"
  - all failed     → status="failed"

Uses monkey-patching to replace model.encode so tests run without
a real SentenceTransformer model loaded.
"""

import pytest
from fastapi.testclient import TestClient

from scripts.services.embedding_server.__init__ import app

# __init__.py already does app.include_router(text.router), so
# importing app above is sufficient.

client = TestClient(app)


# ── helpers ──────────────────────────────────────────────────────────

def _fake_encode(text: str, normalize_embeddings: bool = True):
    """Return a deterministic 3-dimensional embedding so tests are fast."""
    return [0.1 * len(text), 0.2 * len(text), 0.3 * len(text)]


def _patch_model(monkeypatch):
    """Replace model.encode with the fast deterministic stub."""
    monkeypatch.setattr(
        "scripts.services.embedding_server.text.model.encode",
        _fake_encode,
    )


# ── tests ────────────────────────────────────────────────────────────


class TestIndexBulkSuccessCounts:
    """All clips carry search_text → status=success, failed=0, skipped=0."""

    def test_all_success(self, monkeypatch):
        _patch_model(monkeypatch)
        payload = {
            "clips": [
                {"clip_id": "a", "search_text": "hello world"},
                {"clip_id": "b", "search_text": "sunset beach"},
                {"clip_id": "c", "search_text": "mountain trail"},
            ]
        }
        resp = client.post("/index_bulk", json=payload)
        assert resp.status_code == 200, resp.text
        body = resp.json()

        assert body["status"] == "success"
        assert body["total"] == 3
        assert body["successful"] == 3
        assert body["skipped"] == 0
        assert body["failed"] == 0
        assert len(body["results"]) == 3

        # Each result must carry embedding + dimensions (success item).
        for r in body["results"]:
            assert r["clip_id"] in {"a", "b", "c"}
            assert "embedding" in r
            assert r["dimensions"] == 3  # stub returns 3-d
            assert "status" not in r      # success items have no status field


class TestIndexBulkPartialCounts:
    """Mix of embedded, skipped (no text), and optionally failed clips."""

    def test_partial_with_skipped(self, monkeypatch):
        _patch_model(monkeypatch)
        payload = {
            "clips": [
                {"clip_id": "ok1", "search_text": "valid"},
                {"clip_id": "skip1", "search_text": ""},
                {"clip_id": "ok2", "search_text": "also valid"},
            ]
        }
        resp = client.post("/index_bulk", json=payload)
        assert resp.status_code == 200, resp.text
        body = resp.json()

        assert body["status"] == "partial"
        assert body["total"] == 3
        assert body["successful"] == 2
        assert body["skipped"] == 1
        assert body["failed"] == 0
        assert len(body["results"]) == 3

        # Verify per-item statuses.
        statuses = {r["clip_id"]: r.get("status", "success") for r in body["results"]}
        assert statuses["ok1"] == "success"
        assert statuses["ok2"] == "success"
        assert statuses["skip1"] == "skipped"

    def test_partial_one_failed_others_ok(self, monkeypatch):
        """Simulate one clip failing during encode while others succeed."""
        _patch_model(monkeypatch)

        failing_clip = "bad-clip"

        def _fail_for_bad(text, normalize_embeddings=True):
            if "bad" in text.lower():
                raise RuntimeError("simulated encode failure")
            return _fake_encode(text, normalize_embeddings)

        monkeypatch.setattr(
            "scripts.services.embedding_server.text.model.encode",
            _fail_for_bad,
        )

        payload = {
            "clips": [
                {"clip_id": "good", "search_text": "sunshine"},
                {"clip_id": failing_clip, "search_text": "bad data"},
                {"clip_id": "also-good", "search_text": "rainbow"},
            ]
        }
        resp = client.post("/index_bulk", json=payload)
        assert resp.status_code == 200, resp.text
        body = resp.json()

        assert body["status"] == "partial"
        assert body["total"] == 3
        assert body["successful"] == 2
        assert body["skipped"] == 0
        assert body["failed"] == 1
        assert len(body["results"]) == 3

        statuses = {r["clip_id"]: r.get("status", "success") for r in body["results"]}
        assert statuses["good"] == "success"
        assert statuses["also-good"] == "success"
        assert statuses[failing_clip] == "failed"


class TestIndexBulkFailedCounts:
    """Every clip fails or is skipped → status=failed, successful=0."""

    def test_all_skipped_no_text(self, monkeypatch):
        _patch_model(monkeypatch)
        payload = {
            "clips": [
                {"clip_id": "x", "search_text": ""},
                {"clip_id": "y", "search_text": "   "},
            ]
        }
        resp = client.post("/index_bulk", json=payload)
        assert resp.status_code == 200, resp.text
        body = resp.json()

        assert body["status"] == "failed"
        assert body["total"] == 2
        assert body["successful"] == 0
        assert body["skipped"] == 2
        assert body["failed"] == 0
        assert len(body["results"]) == 2

        for r in body["results"]:
            assert r["status"] == "skipped"

    def test_all_encode_failures(self, monkeypatch):
        """Every clip triggers an encode error → status=failed, failed==total."""

        def _always_fail(_text, normalize_embeddings=True):
            raise RuntimeError("global outage")

        monkeypatch.setattr(
            "scripts.services.embedding_server.text.model.encode",
            _always_fail,
        )

        payload = {
            "clips": [
                {"clip_id": "f1", "search_text": "crash"},
                {"clip_id": "f2", "search_text": "boom"},
            ]
        }
        resp = client.post("/index_bulk", json=payload)
        assert resp.status_code == 200, resp.text
        body = resp.json()

        assert body["status"] == "failed"
        assert body["total"] == 2
        assert body["successful"] == 0
        assert body["skipped"] == 0
        assert body["failed"] == 2
        assert len(body["results"]) == 2

        for r in body["results"]:
            assert r["status"] == "failed"
            assert "reason" in r


# ── edge cases ───────────────────────────────────────────────────────

class TestIndexBulkEdgeCases:
    def test_empty_clips_rejected(self):
        """Pydantic validator rejects empty clips list → 422."""
        resp = client.post("/index_bulk", json={"clips": []})
        assert resp.status_code == 422, resp.text

    def test_single_clip_success_still_counts(self, monkeypatch):
        _patch_model(monkeypatch)
        payload = {"clips": [{"clip_id": "only", "search_text": "one"}]}
        resp = client.post("/index_bulk", json=payload)
        assert resp.status_code == 200, resp.text
        body = resp.json()
        assert body["status"] == "success"
        assert body["total"] == 1
        assert body["successful"] == 1
        assert body["failed"] == 0

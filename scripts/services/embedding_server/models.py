"""Pydantic request schemas for the embedding endpoints.

QDRANT-001 (June 2026) closure: this sidecar no longer reads from or
writes to media.db.sqlite. Go is the canonical reader + writer. The
old `db_path` fields are KEPT (optional, ignored) only to surface a
clear schema error to legacy callers instead of a silent 500.

QDRANT-001 review-fix (June 2026): IndexTextRequest now exposes `name`
and `search_text` directly so the Go caller (clipindexer/indexing_api.go)
can pass them in the JSON body — no sidecar JSON pointer file required.
"""

from pydantic import BaseModel, field_validator


class EmbedRequest(BaseModel):
    text: str
    type: str = "query"  # "query" per retrieval, "passage" per document indexing


class PhashRequest(BaseModel):
    image_path: str


# ── DEPRECATED: serve 410 Gone. Kept for schema discovery only. ─────────
class IndexVisualRequest(BaseModel):
    db_path: str = ""
    clip_id: str = ""
    frame_path: str = ""


class IndexAudioRequest(BaseModel):
    db_path: str = ""
    clip_id: str = ""
    audio_path: str = ""


# ── QDRANT-001: caller-supplied text, no DB access. ──────────────────────
class IndexTextRequest(BaseModel):
    # Legacy fields retained as optional so a stale client surfaces a
    # clear request-validation error rather than a 500 from the underlying
    # code path.
    db_path: str = ""
    clip_id: str = ""

    # New canonical inputs — caller (Go) supplies text inline.
    name: str = ""
    search_text: str = ""
    transcript_path: str = ""


class BulkClipSpec(BaseModel):
    clip_id: str
    name: str = ""
    search_text: str = ""


class IndexBulkRequest(BaseModel):
    # Legacy fields kept optional. The canonical path requires `clips`.
    db_path: str = ""
    clip_ids: list[str] = []
    clips: list[BulkClipSpec] = []

    @field_validator("clip_ids", "clips")
    @classmethod
    def _validate_size(cls, v):
        if isinstance(v, list) and len(v) > 200:
            raise ValueError("batch cannot exceed 200 items")
        return v


class VisualEmbedRequest(BaseModel):
    text: str  # For CLIP text-to-visual embedding


class VisualAnalyzeRequest(BaseModel):
    image_path: str


class IndexVisualMultiRequest(BaseModel):
    # Legacy fields retained optional — caller must supply video_path.
    db_path: str = ""
    clip_id: str = ""
    video_path: str
    frame_positions: list[float] = [0.2, 0.5, 0.8]  # percentage of duration

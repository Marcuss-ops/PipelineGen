"""Pydantic request schemas for the embedding endpoints.

QDRANT-001 (June 2026) closure: this sidecar no longer reads from or
writes to media.db.sqlite. Go is the canonical reader + writer, so the
indexing endpoints accept only compute inputs and return only embedding
metadata to the Go caller.
"""

from pydantic import BaseModel, field_validator


class EmbedRequest(BaseModel):
    text: str
    type: str = "query"  # "query" per retrieval, "passage" per document indexing


class PhashRequest(BaseModel):
    image_path: str


# ── QDRANT-003: Go is the canonical writer of SQLite + Qdrant. ──────────
# Python sidecars are compute-only — embedding endpoints accept data
# and return vectors without touching any database.
class IndexTextRequest(BaseModel):
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
    clips: list[BulkClipSpec] = []

    @field_validator("clips")
    @classmethod
    def _validate_size(cls, v):
        if not v:
            raise ValueError("clips cannot be empty")
        if isinstance(v, list) and len(v) > 200:
            raise ValueError("batch cannot exceed 200 items")
        return v


class VisualEmbedRequest(BaseModel):
    text: str  # For text-to-visual embedding (SigLIP text encoder)


class ImageEmbedRequest(BaseModel):
    image_path: str  # For image-to-visual embedding (SigLIP image encoder)


class AudioFileEmbedRequest(BaseModel):
    audio_path: str  # For audio-file-to-audio embedding (CLAP audio encoder)


class VisualAnalyzeRequest(BaseModel):
    image_path: str

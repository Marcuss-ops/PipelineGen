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


# ── Batch image-embedding capability (godlike/07 fail-closed batch contract).
# Caps requests at 512 items so a misconfigured bulk caller cannot OOM the
# siglip-so400m-patch14-384 inference runtime at request time. Empty batches
# are rejected at the validator (Pydantic renders them as 422 by FastAPI).
# The hard upper bound matches the architecture/ownership capacity budget
# for the visual_ingest job family (≤ 512 frames per call).
class BatchImageEmbedRequest(BaseModel):
    image_paths: list[str]  # For batched image-to-visual embedding (1 HTTP round-trip)

    @field_validator("image_paths")
    @classmethod
    def _validate_size(cls, v):
        if not v:
            raise ValueError("image_paths cannot be empty")
        if isinstance(v, list) and len(v) > 512:
            raise ValueError("image_paths batch cannot exceed 512 items")
        return v


class AudioFileEmbedRequest(BaseModel):
    audio_path: str  # For audio-file-to-audio embedding (CLAP audio encoder)


class VisualAnalyzeRequest(BaseModel):
    image_path: str


# ── Face detection (POSTGRES-MEDIA-CUTOVER features leg) ─────────────────
# Canonical batch face endpoint contract consumed by Go's
# SidecarFaceDetector (internal/platform/postgres/media/enrichment_adapters.go):
# one {face_count, largest_face_ratio} observation per input path,
# order-preserved, fail-closed (no partial results).
class BatchFaceDetectRequest(BaseModel):
    image_paths: list[str]

    @field_validator("image_paths")
    @classmethod
    def _validate_size(cls, v):
        if not v:
            raise ValueError("image_paths cannot be empty")
        if isinstance(v, list) and len(v) > 512:
            raise ValueError("image_paths batch cannot exceed 512 items")
        return v

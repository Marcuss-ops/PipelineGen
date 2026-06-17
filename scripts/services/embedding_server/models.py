"""Pydantic request schemas for the embedding endpoints."""

from pydantic import BaseModel, field_validator


class EmbedRequest(BaseModel):
    text: str
    type: str = "query"  # "query" per retrieval, "passage" per document indexing


class PhashRequest(BaseModel):
    image_path: str


class IndexVisualRequest(BaseModel):
    db_path: str
    clip_id: str
    frame_path: str


class IndexAudioRequest(BaseModel):
    db_path: str
    clip_id: str
    audio_path: str


class IndexTextRequest(BaseModel):
    db_path: str
    clip_id: str


class IndexBulkRequest(BaseModel):
    db_path: str
    clip_ids: list[str]

    @field_validator("clip_ids")
    @classmethod
    def validate_clip_ids(cls, v: list[str]) -> list[str]:
        if not v:
            raise ValueError("clip_ids cannot be empty")
        if len(v) > 200:
            raise ValueError("clip_ids cannot exceed 200 items per batch")
        return v


class VisualEmbedRequest(BaseModel):
    text: str  # For CLIP text-to-visual embedding


class VisualAnalyzeRequest(BaseModel):
    image_path: str


class IndexVisualMultiRequest(BaseModel):
    db_path: str
    clip_id: str
    video_path: str
    frame_positions: list[float] = [0.2, 0.5, 0.8]  # percentage of duration

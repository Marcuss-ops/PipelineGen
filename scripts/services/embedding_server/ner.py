"""Source-grounded multilingual NER route for the existing embedding sidecar.

The model is loaded only when this route is first used. Entity offsets are
Unicode codepoint offsets, matching Python and spaCy; the Go adapter converts
them to canonical UTF-8 byte offsets before the shared SceneIR validator.
"""

import logging
import os
import resource
import sys
import threading
import time

import spacy
from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, Field

from . import _inference_sem

log = logging.getLogger("embedding_server.ner")
router = APIRouter()
_SUPPORTED_LANGUAGES = {"en", "it", "es", "pt", "fr", "de"}
_model = None
_model_name = ""
_model_load_ms = None
_model_lock = threading.Lock()


class NERRequest(BaseModel):
    version: str = "ner.v1"
    operation: str = "extract"
    source_text: str = Field(min_length=1, max_length=100_000)
    language: str = Field(min_length=1, max_length=32)
    entity_count: int = Field(default=0, ge=0, le=1000)


def _process_max_rss_mb():
    peak = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    # Linux reports KiB; macOS reports bytes.
    return peak / (1024 * 1024 if sys.platform == "darwin" else 1024)


def _load_ner_model():
    global _model, _model_name, _model_load_ms
    if _model is not None:
        return _model
    with _model_lock:
        if _model is not None:
            return _model
        model_name = os.environ.get("PIPELINEGEN_NER_MODEL", "xx_ent_wiki_sm").strip()
        if not model_name:
            raise RuntimeError("PIPELINEGEN_NER_MODEL must name an installed spaCy model")
        started = time.perf_counter()
        try:
            loaded = spacy.load(model_name)
        except Exception as exc:
            raise RuntimeError(f"spaCy NER model {model_name!r} is unavailable") from exc
        _model_load_ms = (time.perf_counter() - started) * 1000
        _model_name = model_name
        _model = loaded
        log.info("loaded spaCy NER model %s in %.1fms", _model_name, _model_load_ms)
        return _model


@router.post("/ner/extract")
async def extract_entities(req: NERRequest):
    if req.version != "ner.v1" or req.operation != "extract":
        raise HTTPException(status_code=400, detail="unsupported NER protocol version or operation")
    language = req.language.strip().lower().split("-", 1)[0]
    if language not in _SUPPORTED_LANGUAGES:
        raise HTTPException(status_code=400, detail="language must be one of en, it, es, pt, fr, de")
    if not req.source_text.strip():
        raise HTTPException(status_code=400, detail="source_text must contain non-whitespace text")
    async with _inference_sem:
        try:
            nlp = _load_ner_model()
        except RuntimeError as exc:
            raise HTTPException(status_code=503, detail=str(exc)) from exc
        except Exception as exc:
            log.exception("spaCy NER model load failed")
            raise HTTPException(status_code=503, detail="spaCy NER model load failed") from exc
        try:
            started = time.perf_counter()
            doc = nlp(req.source_text)
            elapsed_ms = (time.perf_counter() - started) * 1000
        except Exception as exc:
            log.exception("spaCy NER inference failed")
            raise HTTPException(status_code=503, detail="spaCy NER inference failed") from exc
        entity_count = req.entity_count or 3  # shared with Rust VisualNER's safe default
        spans = doc.ents[:entity_count]
        return {
            "version": "ner.v1",
            "model": _model_name,
            "model_load_ms": _model_load_ms if _model_load_ms is not None else 0.0,
            "inference_ms": elapsed_ms,
            "sidecar_process_max_rss_mb": _process_max_rss_mb(),
            "entities": [
                {
                    "text": ent.text,
                    "label": ent.label_,
                    "start_char": ent.start_char,
                    "end_char": ent.end_char,
                }
                for ent in spans
            ],
        }

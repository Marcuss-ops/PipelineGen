#!/usr/bin/env python3
"""Generate embeddings via SentenceTransformer and emit the canonical envelope.

QDRANT-001 / QDRANT-001b (July 2026): the output is now a JSON dict
envelope instead of a raw array, so downstream Go consumers can record
model identity + version provenance at write time.

Envelope shape:
  {"embedding": [...], "dimensions": 768,
   "model": "intfloat/multilingual-e5-base",
   "model_version": "<hf_revision>|<project_semver>",
   "contract_hash": "<sha256>", "error": ""}
"""
import argparse
import json
import sys
import hashlib
from pathlib import Path

try:
    from scripts.services.model_registry_generated import (
        TEXT_MODEL_DIMENSIONS,
        TEXT_MODEL_NAME,
        TEXT_MODEL_VERSION,
    )
except ModuleNotFoundError:  # direct execution from scripts/bridges
    sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
    from scripts.services.model_registry_generated import (  # type: ignore[no-redef]
        TEXT_MODEL_DIMENSIONS,
        TEXT_MODEL_NAME,
        TEXT_MODEL_VERSION,
    )

try:
    from sentence_transformers import SentenceTransformer
except ImportError:
    print(json.dumps({"embedding": [], "dimensions": 0, "model": "",
                        "model_version": "", "error": "sentence_transformers not installed"}))
    sys.exit(1)

parser = argparse.ArgumentParser()
parser.add_argument("--text", required=True)
args = parser.parse_args()

# Keep the wire producer honest: this is the canonical serialization used by
# internal/kernel/embedding.Contract.Hash(). The model/revision values are
# runtime provenance, while the hash covers the complete vector-space
# contract.
CONTRACT_FIELDS = (
    "v1", TEXT_MODEL_NAME, TEXT_MODEL_VERSION, str(TEXT_MODEL_DIMENSIONS),
    "l2", "Cosine", "query: ", "passage: ", "v3",
)
CONTRACT_HASH = hashlib.sha256("|".join(CONTRACT_FIELDS).encode()).hexdigest()

try:
    model = SentenceTransformer(TEXT_MODEL_NAME)
    # E5 requires 'query:' prefix for retrieval queries or 'passage:' for documents
    # Default to 'query:' for one-shot usage
    prefix = "query: "
    if args.text.startswith("passage:"):
        prefix = ""
    embedding = model.encode(prefix + args.text, normalize_embeddings=True).tolist()
    print(json.dumps({
        "embedding": embedding,
        "dimensions": len(embedding),
        "model": TEXT_MODEL_NAME,
        "model_version": TEXT_MODEL_VERSION,
        "contract_hash": CONTRACT_HASH,
        "error": "",
    }))
except Exception as e:
    print(json.dumps({
        "embedding": [],
        "dimensions": 0,
        "model": "",
        "model_version": "",
        "contract_hash": "",
        "error": str(e),
    }))
    sys.exit(1)

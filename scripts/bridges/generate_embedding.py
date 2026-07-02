#!/usr/bin/env python3
"""Generate embeddings via SentenceTransformer and emit the canonical envelope.

QDRANT-001 / QDRANT-001b (July 2026): the output is now a JSON dict
envelope instead of a raw array, so downstream Go consumers can record
model identity + version provenance at write time.

Envelope shape:
  {"embedding": [...], "dimensions": 768,
   "model": "intfloat/multilingual-e5-base",
   "model_version": "<hf_revision>|<project_semver>", "error": ""}
"""
import argparse
import json
import sys

try:
    from sentence_transformers import SentenceTransformer
except ImportError:
    print(json.dumps({"embedding": [], "dimensions": 0, "model": "",
                        "model_version": "", "error": "sentence_transformers not installed"}))
    sys.exit(1)

parser = argparse.ArgumentParser()
parser.add_argument("--text", required=True)
args = parser.parse_args()

try:
    model = SentenceTransformer("intfloat/multilingual-e5-base")
    # E5 requires 'query:' prefix for retrieval queries or 'passage:' for documents
    # Default to 'query:' for one-shot usage
    prefix = "query: "
    if args.text.startswith("passage:"):
        prefix = ""
    embedding = model.encode(prefix + args.text, normalize_embeddings=True).tolist()
    print(json.dumps({
        "embedding": embedding,
        "dimensions": len(embedding),
        "model": "intfloat/multilingual-e5-base",
        "model_version": "2026-06-16-v1",
        "error": "",
    }))
except Exception as e:
    print(json.dumps({
        "embedding": [],
        "dimensions": 0,
        "model": "",
        "model_version": "",
        "error": str(e),
    }))
    sys.exit(1)

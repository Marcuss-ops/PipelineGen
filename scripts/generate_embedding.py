#!/usr/bin/env python3
import argparse
import json
import sys

try:
    from sentence_transformers import SentenceTransformer
except ImportError:
    # Print an empty list or exit
    print("[]")
    sys.exit(0)

parser = argparse.ArgumentParser()
parser.add_argument("--text", required=True)
args = parser.parse_args()

try:
    model = SentenceTransformer("intfloat/multilingual-e5-base")
    embedding = model.encode(args.text).tolist()
    print(json.dumps(embedding))
except Exception as e:
    # Print empty array on error to prevent total crash
    print("[]")

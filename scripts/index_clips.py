#!/usr/bin/env python3
import sqlite3
import json
import argparse
from pathlib import Path

try:
    from sentence_transformers import SentenceTransformer
    import spacy
    import yake
except ImportError as e:
    print(f"Missing dependency: {e}")
    print("Install: pip install sentence-transformers spacy yake[full]")
    exit(1)

nlp = spacy.load("en_core_web_sm")
model = SentenceTransformer("all-MiniLM-L6-v2")

def normalize_text(text):
    doc = nlp(text.lower())
    return " ".join([token.lemma_ for token in doc if not token.is_stop and not token.is_punct])

def generate_search_text(tags):
    return " ".join(tags)

def compute_embedding(text):
    return json.dumps(model.encode(text).tolist())

def process_db(db_path):
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    cursor = conn.cursor()
    cursor.execute("SELECT id, clip_id, tags FROM clips WHERE search_text IS NULL OR embedding IS NULL")
    clips = cursor.fetchall()
    for clip in clips:
        tags_str = clip["tags"] or "[]"
        try:
            tags = json.loads(tags_str)
            if not isinstance(tags, list):
                tags = []
        except (json.JSONDecodeError, TypeError):
            tags = []
        search_text = generate_search_text(tags)
        embedding = compute_embedding(normalize_text(search_text))
        cursor.execute("UPDATE clips SET search_text = ?, embedding = ? WHERE id = ?", (search_text, embedding, clip["id"]))
        print(f"Updated {clip['clip_id']} in {db_path}")
    conn.commit()
    conn.close()

def process_clip(db_path, clip_id, clip_name="", clip_path=""):
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    cursor = conn.cursor()

    # Get clip info if clip_id provided
    if clip_id:
        cursor.execute("SELECT id, name, local_path, tags FROM clips WHERE id = ?", (clip_id,))
    else:
        cursor.execute("SELECT id, name, local_path, tags FROM clips WHERE search_text IS NULL OR embedding IS NULL")

    clips = cursor.fetchall()
    for clip in clips:
        clip_id = clip["id"]
        name = clip["name"] or ""
        local_path = clip["local_path"] or ""
        tags_str = clip["tags"] or "[]"
        try:
            tags = json.loads(tags_str)
            if not isinstance(tags, list):
                tags = []
        except (json.JSONDecodeError, TypeError):
            tags = []

        # Generate search_text from name and tags
        search_text = generate_search_text([name] + tags)

        # Compute embedding
        embedding = compute_embedding(search_text)

        cursor.execute(
            "UPDATE clips SET search_text = ?, embedding_json = ? WHERE id = ?",
            (search_text, embedding, clip_id)
        )
        print(f"Updated clip {clip_id}: search_text='{search_text[:50]}...'")

    conn.commit()
    conn.close()

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--db", nargs="+", required=True)
    parser.add_argument("--clip-id", default="")
    parser.add_argument("--clip-name", default="")
    parser.add_argument("--clip-path", default="")
    args = parser.parse_args()

    for db_path in args.db:
        if Path(db_path).exists():
            process_clip(db_path, args.clip_id, args.clip_name, args.clip_path)
        else:
            print(f"DB not found: {db_path}")
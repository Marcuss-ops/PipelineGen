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
        tags = json.loads(clip["tags"]) if clip["tags"] else []
        search_text = generate_search_text(tags)
        embedding = compute_embedding(normalize_text(search_text))
        cursor.execute("UPDATE clips SET search_text = ?, embedding = ? WHERE id = ?", (search_text, embedding, clip["id"]))
        print(f"Updated {clip['clip_id']} in {db_path}")
    conn.commit()
    conn.close()

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--db", nargs="+", required=True)
    args = parser.parse_args()
    for db_path in args.db:
        if Path(db_path).exists():
            process_db(db_path)
        else:
            print(f"DB not found: {db_path}")
#!/usr/bin/env python3
import sqlite3
import json
import os
import argparse
from pathlib import Path

try:
    from sentence_transformers import SentenceTransformer
    import spacy
    import yake
    import requests
    import subprocess
except ImportError as e:
    print(f"Missing dependency: {e}")
    print("Install: pip install sentence-transformers spacy yake[full] requests")
    exit(1)

_models = None  # populated lazily by get_models() on first call


def get_models():
    """Lazy-load ML models on first call.

    Returns ``(nlp, nlp_it_or_none, model)``. Memoised so subsequent calls
    are O(1).

    Why lazy (July 2026):
      * `spacy.load("en_core_web_sm")` reads the model package from disk
        (hundreds of MB) on every import.
      * `SentenceTransformer("intfloat/multilingual-e5-base")` downloads
        + caches the model on first run.
    Both are non-trivial and DOMINATE the index_clips import cost when
    the file is imported by tooling that does NOT actually need ML
    inference (e.g. CI lint jobs, parser smoke tests, lightweight
    import-graph inspection). The lazy pattern defers both to the
    first `compute_embedding` / `normalize_text` invocation; only an
    actual `--db ...` indexing run pays.
    """
    global _models
    if _models is not None:
        return _models
    nlp = spacy.load("en_core_web_sm")
    nlp_it = None
    try:
        nlp_it = spacy.load("it_core_news_sm")
    except Exception:
        # Italian model not installed — fall back to the multilingual
        # en pipeline. Bare-except → except Exception because we still
        # want KeyboardInterrupt / SystemExit to propagate normally.
        pass
    model = SentenceTransformer("intfloat/multilingual-e5-base")
    _models = (nlp, nlp_it, model)
    return _models


def __getattr__(name):
    """Module-level lazy attribute for back-compat with external callers.

    Lets code like ``from index_clips import nlp, model`` keep working
    without the caller having to know about `get_models()`. Replaces
    the pre-fix eager top-level `nlp = spacy.load(...)` /
    `model = SentenceTransformer(...)` global initializers.
    """
    if name in ("nlp", "nlp_it", "model"):
        nlp, nlp_it, model = get_models()
        return {"nlp": nlp, "nlp_it": nlp_it, "model": model}[name]
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")

def get_txt_content(local_path, name=None):
    if not local_path and not name:
        return ""
    # 1. Try with_suffix(".txt") if local_path is specified
    if local_path:
        try:
            p = Path(local_path)
            txt_file = p.with_suffix(".txt")
            if txt_file.exists():
                with open(txt_file, "r", encoding="utf-8", errors="ignore") as f:
                    return f.read().strip()
        except Exception as e:
            print(f"Error reading with_suffix txt: {e}")
            
    # 2. Try searching in same folder as local_path or download folders for {name}.txt
    if name:
        base_name = Path(name).stem
        search_dirs = [Path("data/media"), Path("data/downloads"), Path("data/youtube-clips")]
        if local_path:
            search_dirs.insert(0, Path(local_path).parent)
            
        for s_dir in search_dirs:
            if s_dir.exists():
                txt_file = s_dir / f"{base_name}.txt"
                if txt_file.exists():
                    try:
                        with open(txt_file, "r", encoding="utf-8", errors="ignore") as f:
                            return f.read().strip()
                    except Exception as e:
                        print(f"Error reading s_dir txt file {txt_file}: {e}")
                try:
                    for found_p in s_dir.rglob(f"{base_name}.txt"):
                        if found_p.exists():
                            with open(found_p, "r", encoding="utf-8", errors="ignore") as f:
                                return f.read().strip()
                except Exception as e:
                    print(f"Error reading rglob txt files under {s_dir}: {e}")
    return ""

def normalize_text(text):
    """Lemmatise + strip stopwords/punct. Italian-aware via a tiny
    stopword sniff; falls back to the en pipeline.
    """
    # Quick heuristic to detect Italian words
    italian_stopwords = {"il", "la", "i", "gli", "le", "un", "una", "di", "a", "da", "in", "con", "su", "per", "tra", "fra", "che"}
    words = text.lower().split()
    is_italian = any(w in italian_stopwords for w in words)
    nlp, nlp_it, _ = get_models()
    target_nlp = nlp_it if (is_italian and nlp_it) else nlp

    doc = target_nlp(text.lower())
    return " ".join([token.lemma_ for token in doc if not token.is_stop and not token.is_punct])

def generate_search_text(parts):
    """
    Generate a rich search_text from name, description, and tags.
    Aligned with semantic_tagger.py: produces a flat text blob with normalized tokens,
    deduplication, and rich contextual phrases for FTS + vector search.
    
    This is lighter than semantic_tagger.py (no taxonomy, no YAKE, no Ollama),
    but produces similarly rich normalized text for search indexing.
    """
    # Collect all parts
    text_parts = []
    for p in parts:
        if isinstance(p, str):
            text_parts.append(normalize_text(p))
        elif isinstance(p, (list, tuple)):
            for item in p:
                if item:
                    text_parts.append(item.lower().strip())
    
    # Tokenize and deduplicate with frequency awareness
    seen = set()
    tokens = []
    for text in text_parts:
        for token in text.split():
            token = token.strip(".,!?;:'\"()[]")
            if len(token) <= 2 or token in {"the", "and", "for", "are", "was", "had", "but", "not", "all", "can", "has", "its", "per", "via", "use", "get", "new"}:
                continue
            if token not in seen:
                seen.add(token)
                tokens.append(token)
    
    # Sort for deterministic output
    tokens.sort()
    
    # Also keep original full phrases for bigram matching
    phrases = []
    for p in parts:
        if isinstance(p, str) and len(p) > 3:
            clean = p.lower().strip()
            if clean not in seen:
                phrases.append(clean)
                seen.add(clean)
    
    return " ".join(tokens + phrases)

# In-memory deduplication cache for text -> embedding
embedding_cache_text = {}

def compute_embedding(text):
    """Encode documents in the same Ollama vector space used by queries."""
    if text in embedding_cache_text:
        return embedding_cache_text[text]
    ollama_addr = os.getenv("OLLAMA_ADDR", "http://127.0.0.1:11434").rstrip("/")
    model = os.getenv("OLLAMA_EMBED_MODEL", "nomic-embed-text")
    response = requests.post(
        f"{ollama_addr}/api/embeddings",
        json={"model": model, "prompt": text},
        timeout=120,
    )
    response.raise_for_status()
    vector = response.json().get("embedding")
    if not isinstance(vector, list) or len(vector) != 768:
        raise RuntimeError(f"unexpected Ollama embedding dimensions: {len(vector) if isinstance(vector, list) else 0}")
    emb = json.dumps(vector)
    embedding_cache_text[text] = emb
    return emb

def _iter_clips_needing_index(conn, clip_id: str = "") -> list:
    """Return rows from `media_assets` that need search-text / embedding indexing.

    Two modes:
      * `clip_id` provided: select that single clip (re-indexing a specific
        clip via the CLI's `--clip-id` flag).
      * `clip_id` empty: select every clip where `$.search_text` is NULL
        OR `embedding_json` is NULL — the canonical "needs indexing"
        predicate that drives full-database index sweeps.

    The caller owns `conn` and remains responsible for `commit()` /
    `close()`; this helper only stages the SELECT + fetchall.
    """
    cursor = conn.cursor()
    if clip_id:
        cursor.execute(
            "SELECT id, name, tags, "
            "json_extract(metadata_json, '$.local_path') as local_path, "
            "json_extract(metadata_json, '$.search_text') as existing_search_text "
            "FROM media_assets WHERE id = ?",
            (clip_id,),
        )
    else:
        cursor.execute(
            "SELECT id, name, tags, "
            "json_extract(metadata_json, '$.local_path') as local_path, "
            "json_extract(metadata_json, '$.search_text') as existing_search_text "
            "FROM media_assets "
            "WHERE json_extract(COALESCE(metadata_json,'{}'), '$.search_text') IS NULL "
            "OR embedding_json IS NULL"
        )
    return cursor.fetchall()


def process_clip(db_path, clip_id, clip_name="", clip_path=""):
    conn = sqlite3.connect(db_path, timeout=60)
    conn.row_factory = sqlite3.Row
    cursor = conn.cursor()
    # Match the Go-side SQLite configuration so this bridge waits for
    # transient writers instead of failing fast with "database is locked".
    cursor.execute("PRAGMA journal_mode = WAL")
    cursor.execute("PRAGMA busy_timeout = 60000")

    # SELECT boundary consolidated in _iter_clips_needing_index.
    clips = _iter_clips_needing_index(conn, clip_id)
    for clip in clips:
        clip_id = clip["id"]
        name = clip["name"] or ""
        local_path = clip["local_path"] or ""
        existing_search_text = clip["existing_search_text"] or ""
        tags_str = clip["tags"] or "[]"
        try:
            tags = json.loads(tags_str)
            if not isinstance(tags, list):
                tags = []
        except (json.JSONDecodeError, TypeError):
            tags = []

        # Get description from associated .txt file
        txt_desc = get_txt_content(local_path, name)
        
        # Use existing search_text if available (Go-side generated), otherwise generate new one
        if existing_search_text:
            search_text = existing_search_text
            print(f"Using existing search_text for clip {clip_id}: '{search_text[:50]}...'")
        else:
            search_parts = [name]
            if txt_desc:
                search_parts.append(txt_desc)
            search_parts.extend(tags)
            search_text = generate_search_text(search_parts)
            print(f"Generated new search_text for clip {clip_id}: '{search_text[:50]}...'")

        # Compute embedding
        embedding = compute_embedding(search_text)

        # Compute transcript embedding if text description is present
        transcript_embedding = "[]"
        if txt_desc:
            transcript_embedding = compute_embedding(txt_desc)

        cursor.execute(
            "UPDATE media_assets SET metadata_json = json_set(COALESCE(metadata_json,'{}'), '$.search_text', ?), "
            "embedding_json = ?, transcript_embedding = ? WHERE id = ?",
            (search_text, embedding, transcript_embedding, clip_id)
        )
        # Release the write lock before the expensive visual pass. The
        # old code held the transaction open until the end of the whole
        # clip, which made every other SQLite writer wait behind ffprobe
        # and ffmpeg work. Committing here keeps the critical section tiny.
        conn.commit()
        print(f"Updated clip {clip_id}: search_text='{search_text[:50]}...', transcript_embedding_len={len(transcript_embedding)}")

        # Visual Indexing - 1 frame every 2 seconds passed to CLIP
        if local_path and Path(local_path).exists():
            try:
                from PIL import Image
                import numpy as np
                # Load SigLIP model (768d) — aligned with QDRANT-003 schema
                siglip_model = SentenceTransformer("google/siglip-so400m-patch14-384")

                # Get video duration using ffprobe
                probe_cmd = [
                    "ffprobe", "-v", "error", "-show_entries", "format=duration",
                    "-of", "default=noprint_wrappers=1:nokey=1", local_path
                ]
                duration = float(subprocess.check_output(probe_cmd).decode().strip())
                
                # 1 frame every 2s
                timestamps = []
                t = 1.0
                while t < duration:
                    timestamps.append(t)
                    t += 2.0
                if not timestamps:
                    timestamps = [duration / 2.0]
                
                embeddings = []
                for i, ts in enumerate(timestamps):
                    frame_path = Path(local_path).parent / f"{clip_id}_thumb_{i}.png"
                    subprocess.run([
                        "ffmpeg", "-y", "-ss", str(ts), "-i", local_path, 
                        "-frames:v", "1", "-q:v", "2", str(frame_path)
                    ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                    
                    if frame_path.exists():
                        try:
                            img = Image.open(frame_path)
                            emb = siglip_model.encode(img).tolist()
                            embeddings.append(emb)
                        except Exception as e:
                            print(f"Error encoding frame at {ts}s: {e}")
                        finally:
                            frame_path.unlink()
                
                if embeddings:
                    avg_emb = np.mean(embeddings, axis=0).tolist()
                    cursor.execute(
                        "UPDATE media_assets SET visual_embedding = ? WHERE id = ?",
                        (json.dumps(avg_emb), clip_id)
                    )
                    conn.commit()
                    print(f"Visual indexing success: visual_embedding averaged {len(embeddings)} frames for clip {clip_id}")
            except Exception as e:
                print(f"Visual indexing error: {e}")

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

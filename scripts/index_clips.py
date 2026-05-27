#!/usr/bin/env python3
import sqlite3
import json
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

nlp = spacy.load("en_core_web_sm")
nlp_it = None
try:
    nlp_it = spacy.load("it_core_news_sm")
except:
    pass

model = SentenceTransformer("all-MiniLM-L6-v2")

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
                    except:
                        pass
                try:
                    for found_p in s_dir.rglob(f"{base_name}.txt"):
                        if found_p.exists():
                            with open(found_p, "r", encoding="utf-8", errors="ignore") as f:
                                return f.read().strip()
                except:
                    pass
    return ""

def normalize_text(text):
    # Quick heuristic to detect Italian words
    italian_stopwords = {"il", "la", "i", "gli", "le", "un", "una", "di", "a", "da", "in", "con", "su", "per", "tra", "fra", "che"}
    words = text.lower().split()
    is_italian = any(w in italian_stopwords for w in words)
    target_nlp = nlp_it if (is_italian and nlp_it) else nlp
    
    doc = target_nlp(text.lower())
    return " ".join([token.lemma_ for token in doc if not token.is_stop and not token.is_punct])

def generate_search_text(tags):
    return " ".join(tags)

# In-memory deduplication cache for text -> embedding
embedding_cache_text = {}

def compute_embedding(text):
    if text in embedding_cache_text:
        return embedding_cache_text[text]
    emb = json.dumps(model.encode(text).tolist())
    embedding_cache_text[text] = emb
    return emb

def process_db(db_path):
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    cursor = conn.cursor()
    cursor.execute("SELECT id, name, tags, json_extract(metadata_json, '$.local_path') as local_path FROM media_assets WHERE json_extract(COALESCE(metadata_json,'{}'), '$.search_text') IS NULL OR embedding_json IS NULL")
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
        
        # Get description from associated .txt file
        txt_desc = get_txt_content(local_path, name)
        search_parts = [name]
        if txt_desc:
            search_parts.append(txt_desc)
        search_parts.extend(tags)

        search_text = generate_search_text(search_parts)
        embedding = compute_embedding(normalize_text(search_text))
        cursor.execute("UPDATE media_assets SET metadata_json = json_set(COALESCE(metadata_json,'{}'), '$.search_text', ?), embedding_json = ? WHERE id = ?", (search_text, embedding, clip_id))
        print(f"Updated {clip_id} in {db_path}")
    conn.commit()
    conn.close()

def process_clip(db_path, clip_id, clip_name="", clip_path=""):
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    cursor = conn.cursor()

    # Get clip info if clip_id provided
    if clip_id:
        cursor.execute("SELECT id, name, tags, json_extract(metadata_json, '$.local_path') as local_path FROM media_assets WHERE id = ?", (clip_id,))
    else:
        cursor.execute("SELECT id, name, tags, json_extract(metadata_json, '$.local_path') as local_path FROM media_assets WHERE json_extract(COALESCE(metadata_json,'{}'), '$.search_text') IS NULL OR embedding_json IS NULL")

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

        # Get description from associated .txt file
        txt_desc = get_txt_content(local_path, name)
        search_parts = [name]
        if txt_desc:
            search_parts.append(txt_desc)
        search_parts.extend(tags)

        # Generate search_text from name, description and tags
        search_text = generate_search_text(search_parts)

        # Compute embedding
        embedding = compute_embedding(search_text)

        cursor.execute(
            "UPDATE media_assets SET metadata_json = json_set(COALESCE(metadata_json,'{}'), '$.search_text', ?), embedding_json = ? WHERE id = ?",
            (search_text, embedding, clip_id)
        )
        print(f"Updated clip {clip_id}: search_text='{search_text[:50]}...'")

        # Visual Indexing - Multi-frame extraction (25%, 50%, 75%)
        if local_path and Path(local_path).exists():
            try:
                # Get video duration using ffprobe
                probe_cmd = [
                    "ffprobe", "-v", "error", "-show_entries", "format=duration",
                    "-of", "default=noprint_wrappers=1:nokey=1", local_path
                ]
                duration = float(subprocess.check_output(probe_cmd).decode().strip())
                
                # Frames to extract at 25%, 50%, 75%
                timestamps = [duration * 0.25, duration * 0.50, duration * 0.75]
                
                for i, ts in enumerate(timestamps):
                    frame_path = Path(local_path).parent / f"{clip_id}_thumb_{i}.png"
                    subprocess.run([
                        "ffmpeg", "-y", "-ss", str(ts), "-i", local_path, 
                        "-frames:v", "1", "-q:v", "2", str(frame_path)
                    ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                    
                    # Call embedding server for visual indexing
                    resp = requests.post("http://127.0.0.1:8001/index_visual", json={
                        "db_path": str(Path(db_path).absolute()),
                        "clip_id": clip_id,
                        "frame_path": str(frame_path.absolute())
                    })
                    
                    if resp.status_code == 200:
                        data = resp.json()
                        print(f"Visual indexing success (frame {i}): phash={data.get('phash')}")
                    else:
                        print(f"Visual indexing failed (frame {i}): {resp.text}")
                    
                    # Cleanup frame
                    if frame_path.exists():
                        frame_path.unlink()
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
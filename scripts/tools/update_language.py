#!/usr/bin/env python3
"""
Universal language updater for ANY media type (youtube, artlist, stock, images, voiceovers).

Detects language via whisper transcription (if --auto-detect) or sets a fixed language.
Works on any asset in media_assets regardless of source.

Usage:
  # Batch: set language for ALL media with empty language field
  python3 scripts/update_language.py --batch --lang en [--source youtube] [--media-type video]

  # Batch: auto-detect language via whisper for all media without language
  python3 scripts/update_language.py --batch --auto-detect [--source artlist] [--media-type audio]

  # Single clip: set fixed language
  python3 scripts/update_language.py --clip <asset_id> --lang it

  # Single clip: auto-detect language from local file
  python3 scripts/update_language.py --clip <asset_id> --auto-detect
"""
import sqlite3
import json
import sys
import os
import subprocess
import tempfile

def update_asset_language(db_path: str, asset_id: str, language: str) -> bool:
    """Update language field in metadata_json for a single asset (any source/media type)."""
    conn = sqlite3.connect(db_path)
    
    row = conn.execute(
        "SELECT metadata_json FROM media_assets WHERE id = ?", (asset_id,)
    ).fetchone()
    
    if not row:
        print(f"Asset {asset_id} not found")
        conn.close()
        return False
    
    meta = json.loads(row[0])
    old_lang = meta.get("language", "")
    old_source = meta.get("source", meta.get("media_type", "?"))
    meta["language"] = language
    
    conn.execute(
        "UPDATE media_assets SET metadata_json = ? WHERE id = ?",
        (json.dumps(meta), asset_id)
    )
    conn.commit()
    conn.close()
    
    print(f"  [{old_source}] {asset_id}: language '{old_lang}' → '{language}'")
    return True


# ---------------------------------------------------------------------------
# Module-level whisper model cache — loaded ONCE, shared across all calls
# ---------------------------------------------------------------------------
_WHISPER_MODEL_CACHE: dict = {}

def _get_whisper_model(model_size: str = "tiny"):
    """Get or create a cached whisper model."""
    global _WHISPER_MODEL_CACHE
    if model_size not in _WHISPER_MODEL_CACHE:
        from faster_whisper import WhisperModel
        _WHISPER_MODEL_CACHE[model_size] = WhisperModel(
            model_size, device="cpu", compute_type="int8"
        )
    return _WHISPER_MODEL_CACHE[model_size]


def detect_language_from_file(file_path: str) -> dict:
    """Detect language from a local audio/video file using cached whisper model.
    
    Returns dict with 'language', 'probability', 'transcript_preview'.
    No DB writes — returns JSON for Go to consume.
    """
    if not os.path.exists(file_path):
        return {"error": f"File not found: {file_path}"}
    
    try:
        import faster_whisper
    except ImportError:
        return {"error": "faster-whisper not installed"}
    
    # Auto-extract audio if video file, using tempfile for safety
    ext = os.path.splitext(file_path)[1].lower()
    audio_path = file_path
    cleanup_path = None
    if ext in (".mp4", ".avi", ".mov", ".mkv", ".webm"):
        fd, audio_path = tempfile.mkstemp(suffix=".wav", prefix="langdetect_")
        os.close(fd)
        cleanup_path = audio_path
        subprocess.run([
            "ffmpeg", "-y", "-hide_banner", "-loglevel", "warning",
            "-i", file_path,
            "-vn", "-c:a", "pcm_s16le", "-ar", "16000", "-ac", "1",
            audio_path
        ], check=True)
    
    try:
        # Use cached model — loaded only ONCE per process
        model = _get_whisper_model("tiny")
        segments, info = model.transcribe(audio_path, beam_size=1)
        segments = list(segments)
        transcript = " ".join(seg.text.strip() for seg in segments)
        
        return {
            "language": info.language,
            "probability": round(info.language_probability, 4),
            "duration_seconds": round(info.duration, 1),
            "transcript_length": len(transcript),
            "transcript_preview": transcript[:300],
        }
    finally:
        if cleanup_path and os.path.exists(cleanup_path):
            os.unlink(cleanup_path)


def get_local_path(conn, asset_id: str) -> str:
    """Get the local file path for an asset from metadata."""
    row = conn.execute(
        "SELECT json_extract(metadata_json, '$.local_path') FROM media_assets WHERE id = ?",
        (asset_id,)
    ).fetchone()
    if row and row[0]:
        return row[0]
    return ""


def batch_update_missing_language(
    db_path: str,
    language: str = "",
    source: str = "",
    media_type: str = "",
    auto_detect: bool = False
) -> int:
    """Update language for ALL assets with empty language field.
    
    Args:
        db_path: Path to SQLite database
        language: Fixed language to set (ignored if auto_detect=True)
        source: Optional source filter ("youtube", "artlist", "stock", "image", "voiceover", etc.)
        media_type: Optional media type filter ("video", "audio", "image")
        auto_detect: If True, detect language from local file via whisper
    
    Returns:
        Number of assets updated
    """
    conn = sqlite3.connect(db_path)
    
    # Build dynamic WHERE clause — universal, not hardcoded for YouTube
    conditions = ["(json_extract(metadata_json, '$.language') IS NULL OR json_extract(metadata_json, '$.language') = '')"]
    params = []
    
    if source:
        conditions.append("source = ?")
        params.append(source)
    if media_type:
        conditions.append("json_extract(metadata_json, '$.media_type') = ?")
        params.append(media_type)
    
    where = " AND ".join(conditions)
    
    rows = conn.execute(
        f"SELECT id, name, source, metadata_json FROM media_assets WHERE {where}",
        params
    ).fetchall()
    
    if not rows:
        print("No assets found with empty language field")
        conn.close()
        return 0
    
    updated = 0
    skipped = 0
    
    for row in rows:
        asset_id, name, asset_source, meta_json = row
        meta = json.loads(meta_json)
        
        if auto_detect:
            # Try to get local file path and detect language
            local_path = get_local_path(conn, asset_id)
            if not local_path or not os.path.exists(local_path):
                print(f"  [!] {asset_id} ({name}): no local file, skipping auto-detect")
                skipped += 1
                continue
            
            print(f"  Detecting language for {name}...", end=" ", flush=True)
            result = detect_language_from_file(local_path)
            if "error" in result:
                print(f"FAILED: {result['error']}")
                skipped += 1
                continue
            detected_lang = result["language"]
            prob = result["probability"]
            print(f"→ {detected_lang} ({prob:.1%})")
        else:
            detected_lang = language
        
        old_lang = meta.get("language", "")
        meta["language"] = detected_lang
        conn.execute(
            "UPDATE media_assets SET metadata_json = ? WHERE id = ?",
            (json.dumps(meta), asset_id)
        )
        updated += 1
        print(f"  [{asset_source}] {asset_id}: language '{old_lang}' → '{detected_lang}'")
    
    conn.commit()
    conn.close()
    
    mode = "auto-detected" if auto_detect else f"forced to '{language}'"
    filters = []
    if source:
        filters.append(f"source={source}")
    if media_type:
        filters.append(f"media_type={media_type}")
    filter_str = f" ({', '.join(filters)})" if filters else " (ALL sources)"
    print(f"\nBatch complete{filter_str}: {updated} updated, {skipped} skipped — language {mode}")
    return updated


if __name__ == "__main__":
    import argparse
    
    parser = argparse.ArgumentParser(
        description="Universal language updater for ALL media types. "
                    "Set or auto-detect language for any asset in media_assets."
    )
    parser.add_argument("--db", default="data/media/media.db.sqlite",
                        help="Path to SQLite database (default: data/media/media.db.sqlite)")
    parser.add_argument("--batch", action="store_true",
                        help="Batch mode: update all assets with empty language")
    parser.add_argument("--clip", "--asset", dest="clip_id",
                        help="Single asset ID to update")
    parser.add_argument("--lang", default="en",
                        help="Language code to set (default: en). Ignored if --auto-detect")
    parser.add_argument("--source", default="",
                        help="Filter by source (youtube, artlist, stock, image, voiceover). "
                             "Omit for ALL sources.")
    parser.add_argument("--media-type", "--media_type", dest="media_type", default="",
                        help="Filter by media type (video, audio, image). Omit for ALL types.")
    parser.add_argument("--auto-detect", "--auto_detect", dest="auto_detect", action="store_true",
                        help="Auto-detect language from local file via whisper (instead of --lang)")
    
    args = parser.parse_args()
    
    if args.batch:
        batch_update_missing_language(
            db_path=args.db,
            language=args.lang,
            source=args.source,
            media_type=args.media_type,
            auto_detect=args.auto_detect
        )
    elif args.clip_id:
        if args.auto_detect:
            # Get local path and detect
            conn = sqlite3.connect(args.db)
            local_path = get_local_path(conn, args.clip_id)
            conn.close()
            if not local_path or not os.path.exists(local_path):
                print(f"Error: no local file found for {args.clip_id}")
                sys.exit(1)
            print(f"Detecting language from: {local_path}")
            result = detect_language_from_file(local_path)
            if "error" in result:
                print(f"Detection failed: {result['error']}")
                sys.exit(1)
            detected = result["language"]
            print(f"Detected: {detected} ({result['probability']:.1%})")
            update_asset_language(args.db, args.clip_id, detected)
        else:
            update_asset_language(args.db, args.clip_id, args.lang)
    else:
        parser.print_help()

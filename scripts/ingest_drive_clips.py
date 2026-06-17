#!/usr/bin/env python3
"""
Ingest clips from Google Drive celebrity folders into media.db.sqlite.

Source: https://drive.google.com/drive/u/1/folders/1CnHDc3NIzQvw0ARAPJ9WgTKzf1xDobYM
Each celebrity subfolder contains .mp4 + .txt (transcript) pairs.
Naming: {YouTubeID} - {Title}.{ext}
"""

import hashlib
import json
import os
import sqlite3
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'google-accounting'))
import drive_client

# ── Config ──────────────────────────────────────────────────────────────
ROOT_FOLDER_ID = '1CnHDc3NIzQvw0ARAPJ9WgTKzf1xDobYM'
OUTPUT_BASE = os.path.join(os.path.dirname(__file__), '..', 'data', 'youtube-clips')
DB_PATH = os.path.join(os.path.dirname(__file__), '..', 'data', 'media', 'media.db.sqlite')

# ── Helpers ─────────────────────────────────────────────────────────────

def generate_clip_id(youtube_id, title, index=0):
    """Generate a deterministic clip ID similar to existing clips."""
    raw = f"{youtube_id}:{title}:{index}"
    return hashlib.sha256(raw.encode()).hexdigest()[:32]


def download_file(service, file_id, dest_path):
    """Download a Drive file to local path."""
    import io
    from googleapiclient.http import MediaIoBaseDownload

    os.makedirs(os.path.dirname(dest_path), exist_ok=True)

    if os.path.exists(dest_path):
        print(f"  ⏭  Already exists: {dest_path}")
        return True

    request = service.files().get_media(fileId=file_id)
    fh = io.BytesIO()
    downloader = MediaIoBaseDownload(fh, request)
    done = False
    while not done:
        status, done = downloader.next_chunk()
        if status:
            pass  # print(f"  Download {int(status.progress() * 100)}%")

    fh.seek(0)
    with open(dest_path, 'wb') as f:
        f.write(fh.read())
    print(f"  ✅ Downloaded: {os.path.basename(dest_path)} ({os.path.getsize(dest_path)} bytes)")
    return True


def make_drive_link(file_id):
    """Generate a Drive sharing link."""
    return f"https://drive.google.com/file/d/{file_id}/view"


def insert_clip(db, clip_data):
    """Insert or update a clip in media_assets."""
    now = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())

    metadata = {
        'clean_transcript': clip_data['clean_transcript'],
        'youtube_video_id': clip_data['youtube_id'],
        'youtube_title': clip_data['youtube_title'],
        'drive_link': clip_data['drive_link'],
        'drive_file_id': clip_data['drive_file_id'],
        'local_path': clip_data['local_path'],
        'folder_name': clip_data['folder_name'],
    }
    metadata_json = json.dumps(metadata)

    tags_json = json.dumps(clip_data.get('tags', []))

    db.execute("""
        INSERT INTO media_assets (
            id, source, name, tags, tags_norm,
            media_type, status, local_path, relative_path,
            drive_file_id, drive_link,
            metadata_json,
            created_at, updated_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
            name=excluded.name,
            local_path=excluded.local_path,
            drive_file_id=excluded.drive_file_id,
            drive_link=excluded.drive_link,
            metadata_json=excluded.metadata_json,
            updated_at=excluded.updated_at
    """, (
        clip_data['id'],
        'youtube',
        clip_data['name'],
        tags_json,
        ' '.join(clip_data.get('tags', [])).lower(),
        'clip',
        'active',
        clip_data['local_path'],
        clip_data['local_path'],
        clip_data['drive_file_id'],
        clip_data['drive_link'],
        metadata_json,
        now,
        now,
    ))
    print(f"  💾 Saved to DB: {clip_data['id'][:16]}...")


# ── Main ────────────────────────────────────────────────────────────────

def main():
    print("=" * 60)
    print("🎬 Drive Clip Ingestor")
    print("=" * 60)

    service = drive_client._build_service()
    db = sqlite3.connect(DB_PATH)
    db.execute("PRAGMA journal_mode=WAL")
    db.execute("PRAGMA busy_timeout=5000")

    # Get all celebrity subfolders
    print("\n📂 Fetching subfolders...")
    folders = service.files().list(
        q=f"'{ROOT_FOLDER_ID}' in parents and trashed=false and mimeType='application/vnd.google-apps.folder'",
        fields='files(id, name)',
        pageSize=100
    ).execute().get('files', [])
    print(f"Found {len(folders)} celebrity folders\n")

    total_clips = 0
    total_transcripts = 0

    for folder in folders:
        folder_name = folder['name']
        folder_id = folder['id']
        clean_folder = folder_name.replace(' ', '_').replace("'", "").lower()

        print(f"📁 {folder_name}")

        # List files in this folder
        files = service.files().list(
            q=f"'{folder_id}' in parents and trashed=false",
            fields='files(id, name, mimeType, size)',
            pageSize=200
        ).execute().get('files', [])

        # Group by base name (without extension)
        mp4_files = {}
        txt_files = {}

        for f in files:
            name = f['name']
            if 'video' in f['mimeType']:
                base = os.path.splitext(name)[0]
                mp4_files[base] = f
            elif 'text' in f['mimeType']:
                base = os.path.splitext(name)[0]
                txt_files[base] = f

        # Process matched pairs
        for base_name, mp4_file in mp4_files.items():
            txt_file = txt_files.get(base_name)

            # Parse YouTube ID from filename: "{YouTubeID} - {Title}"
            try:
                youtube_id, title = base_name.split(' - ', 1)
            except ValueError:
                youtube_id = base_name[:11]
                title = base_name

            youtube_id = youtube_id.strip()
            title = title.strip()

            # Generate output paths
            safe_name = title[:60].replace('/', '_').replace('\\', '_').replace(':', ' -')
            out_dir = os.path.join(OUTPUT_BASE, clean_folder)
            mp4_path = os.path.join(out_dir, f"{safe_name}.mp4")

            print(f"  🎥 {title[:70]}")

            # Download mp4
            if not download_file(service, mp4_file['id'], mp4_path):
                continue

            # Download and read transcript
            transcript = ""
            if txt_file:
                txt_path = os.path.join(out_dir, f"{safe_name}.txt")
                if download_file(service, txt_file['id'], txt_path):
                    with open(txt_path, 'r', encoding='utf-8', errors='replace') as tf:
                        transcript = tf.read().strip()
                    print(f"  📝 Transcript: {len(transcript)} chars")
                    total_transcripts += 1

            # Generate clip ID and Drive link
            clip_id = generate_clip_id(youtube_id, title)

            clip_data = {
                'id': clip_id,
                'name': f"{folder_name} - {title}",
                'youtube_id': youtube_id,
                'youtube_title': title,
                'folder_name': folder_name,
                'local_path': mp4_path,
                'drive_file_id': mp4_file['id'],
                'drive_link': make_drive_link(mp4_file['id']),
                'clean_transcript': transcript,
                'tags': [folder_name, title[:30]],
            }

            insert_clip(db, clip_data)
            total_clips += 1

        # Check for orphaned txt files (no matching mp4)
        for base_name, txt_file in txt_files.items():
            if base_name not in mp4_files:
                print(f"  ⚠️  Orphaned txt (no mp4): {txt_file['name']}")

    db.commit()
    db.close()

    print(f"\n{'=' * 60}")
    print(f"✅ DONE: {total_clips} clips, {total_transcripts} transcripts ingested")
    print(f"   DB: {DB_PATH}")
    print(f"   Files: {OUTPUT_BASE}")
    print(f"{'=' * 60}")


if __name__ == '__main__':
    main()

#!/usr/bin/env python3
import os
import json
import sqlite3
import uuid
import urllib.request
from pathlib import Path
from google.oauth2.credentials import Credentials
from googleapiclient.discovery import build
from sentence_transformers import SentenceTransformer

# ── Configuration ─────────────────────────────────────────────────────────────
ROOT = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored"
TOKEN_FILE = os.path.join(ROOT, "token.json")
DB_PATH = os.path.join(ROOT, "data", "media", "media.db.sqlite")
QDRANT_URL = "http://127.0.0.1:6333"
QDRANT_COLLECTION = "media_assets"

# Initialize E5 model (intfloat/multilingual-e5-base -> 768 dims)
print("Loading multilingual-e5-base embedding model...")
model = SentenceTransformer("intfloat/multilingual-e5-base")

# Load Google credentials
with open(TOKEN_FILE, 'r') as f:
    token_data = json.load(f)

creds = Credentials(
    token=token_data["access_token"],
    refresh_token=token_data.get("refresh_token"),
    token_uri="https://oauth2.googleapis.com/token",
    client_id="964460747662-8oielvpbphij44agin684r57ojfio9h1.apps.googleusercontent.com",
    client_secret=None,
)

drive_service = build('drive', 'v3', credentials=creds)

def get_file_content(file_id):
    """Download Google Drive text file content directly in memory."""
    try:
        content = drive_service.files().get_media(fileId=file_id).execute()
        return content.decode('utf-8', errors='ignore').strip()
    except Exception as e:
        print(f"Error reading file content for {file_id}: {e}")
        return ""

def generate_deterministic_uuid(drive_id):
    """Generate a deterministic UUID v5 from the Google Drive file ID."""
    namespace = uuid.UUID('6ba7b810-9dad-11d1-80b4-00c04fd430c8') # DNS namespace
    return str(uuid.uuid5(namespace, drive_id))

# QDRANT-003: generate_normalized_vector removed — synthetic pseudo-random
# vectors are banned. Visual embeddings must come from a real model (CLIP/SigLIP)
# and audio embeddings from CLAP. Placeholder zero-filled vectors are also banned.
# See docs/qdrant/QDRANT-003_SCHEMA_VERSIONING_ALIASES_AND_REAL_EMBEDDINGS.md


def upsert_to_qdrant(point_id, vectors, payload):
    """Upsert a point to Qdrant using the REST API."""
    url = f"{QDRANT_URL}/collections/{QDRANT_COLLECTION}/points?wait=true"
    data = {
        "points": [
            {
                "id": point_id,
                "vectors": vectors,
                "payload": payload
            }
        ]
    }
    req = urllib.request.Request(
        url,
        data=json.dumps(data).encode('utf-8'),
        headers={'Content-Type': 'application/json'},
        method='PUT'
    )
    try:
        with urllib.request.urlopen(req) as res:
            res_body = res.read().decode('utf-8')
            return True, res_body
    except Exception as e:
        return False, str(e)

def scan_folder_recursive(folder_id, current_path=""):
    """Recursively fetch files in Google Drive folder."""
    print(f"Scanning Drive folder: {current_path or 'Root'} (ID: {folder_id})")
    files = []
    page_token = None
    
    while True:
        query = f"'{folder_id}' in parents and trashed = false"
        response = drive_service.files().list(
            q=query,
            spaces="drive",
            fields="nextPageToken, files(id, name, mimeType, createdTime, webViewLink)",
            pageToken=page_token,
            pageSize=100
        ).execute()
        
        files.extend(response.get("files", []))
        page_token = response.get("nextPageToken")
        if not page_token:
            break

    # Separate folders, videos and metadata
    folders = []
    video_files = []
    metadata_files = {} # name_stem -> file_object
    
    video_extensions = ('.mp4', '.mov', '.avi', '.mkv', '.webm')
    meta_extensions = ('.json', '.txt')
    
    for f in files:
        name = f['name']
        mime = f['mimeType']
        
        if mime == 'application/vnd.google-apps.folder':
            folders.append(f)
        elif name.lower().endswith(video_extensions):
            video_files.append(f)
        elif name.lower().endswith(meta_extensions):
            stem = Path(name).stem.lower()
            metadata_files[stem] = f

    # Process videos in the current folder
    db_conn = sqlite3.connect(DB_PATH)
    cursor = db_conn.cursor()
    
    for video in video_files:
        video_name = video['name']
        video_id = video['id']
        video_link = video['webViewLink']
        created_time = video['createdTime']
        stem = Path(video_name).stem
        stem_lower = stem.lower()
        
        print(f"\nProcessing video: {video_name} (ID: {video_id})")
        
        # Pairing logic
        title = stem
        description = ""
        tags = []
        
        if stem_lower in metadata_files:
            meta_file = metadata_files[stem_lower]
            meta_name = meta_file['name']
            meta_id = meta_file['id']
            print(f"  -> Found paired metadata: {meta_name}")
            
            content = get_file_content(meta_id)
            if meta_name.lower().endswith('.json'):
                try:
                    meta_json = json.loads(content)
                    title = meta_json.get('title', title)
                    description = meta_json.get('description', '')
                    # handle tags as string or list
                    raw_tags = meta_json.get('tags', [])
                    if isinstance(raw_tags, str):
                        tags = [t.strip() for t in raw_tags.split(',') if t.strip()]
                    elif isinstance(raw_tags, list):
                        tags = [str(t).strip() for t in raw_tags if str(t).strip()]
                except Exception as e:
                    print(f"  Warning: failed to parse JSON metadata: {e}. Falling back to plain text.")
                    description = content
            else:
                description = content
        else:
            print("  -> No paired metadata found. Using folder fallback tags.")
        
        # Fallback tags based on folder hierarchy
        fallback_tags = [part.strip().lower() for part in current_path.split('/') if part.strip()]
        for ft in fallback_tags:
            if ft not in tags:
                tags.append(ft)
                
        # Generate Text embeddings (prefixed with "passage: " for storage as recommended by E5)
        text_payload = f"passage: {title} {description} " + " ".join(tags)
        print(f"  Generating text embeddings for: '{title}'...")
        text_vector = model.encode(text_payload, normalize_embeddings=True).tolist()
        
        # Transcript vector
        transcript_vector = text_vector
        if description:
            transcript_vector = model.encode(f"passage: {description}", normalize_embeddings=True).tolist()
            
        # QDRANT-003: visual and audio vectors are no longer synthesized.
        # Visual embeddings require a real CLIP/SigLIP model.
        # Audio embeddings require a real CLAP model (optional until service available).
        # Clips without real model output omit these channels.
        visual_vector = None
        audio_vector = None
        
        # Deterministic Point ID
        point_id = generate_deterministic_uuid(video_id)
        
        # Qdrant Upsert
        print(f"  Upserting to Qdrant (ID: {point_id})...")
        vectors = {
            "text": text_vector,
            "transcript": transcript_vector,
        }
        # QDRANT-003: visual and audio vectors only included when produced by a real model.
        if visual_vector is not None:
            vectors["visual"] = visual_vector
        if audio_vector is not None:
            vectors["audio"] = audio_vector
        payload = {
            "drive_file_id": video_id,
            "drive_link": video_link,
            "name": title,
            "folder_path": current_path,
            "tags": tags,
            "status": "active",
            "created_at": created_time
        }
        
        success, q_res = upsert_to_qdrant(point_id, vectors, payload)
        if success:
            print("  Qdrant upsert success!")
        else:
            print(f"  Qdrant upsert failed: {q_res}")
            
        # SQLite Upsert (Dual Store Synchronization)
        print("  Saving metadata to SQLite...")
        meta_json_str = json.dumps(payload)
        now = created_time
        
        cursor.execute("""
            INSERT OR REPLACE INTO media_assets (
                id, source, name, tags, media_type, status, lifecycle_state,
                drive_file_id, drive_link, folder_path, folder_id, metadata_json,
                embedding_json, transcript_embedding, visual_embedding, created_at, updated_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        """, (
            point_id, "drive", title, json.dumps(tags), "video", "ready", "ready",
            video_id, video_link, current_path, folder_id, meta_json_str,
            json.dumps(text_vector), json.dumps(transcript_vector), json.dumps(visual_vector),
            now, now
        ))
        db_conn.commit()
        print("  SQLite database record updated!")
        
    db_conn.close()

    # Recurse into subfolders
    for subfolder in folders:
        sub_path = f"{current_path}/{subfolder['name']}" if current_path else subfolder['name']
        scan_folder_recursive(subfolder['id'], sub_path)

if __name__ == "__main__":
    # Scan the specified root folder
    ROOT_FOLDER_ID = "1ll2RlTaAbhnaLkAjEDBg41lAXUyo-zJ2"
    print(f"Starting synchronization of Drive folder ID: {ROOT_FOLDER_ID}")
    scan_folder_recursive(ROOT_FOLDER_ID)
    print("\nAll synchronization and indexing completed successfully!")

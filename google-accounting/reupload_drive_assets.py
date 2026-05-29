import os
import sys
import json
import sqlite3
import hashlib
from pathlib import Path
from googleapiclient.http import MediaFileUpload

# Append path to import drive_client
sys.path.append(str(Path(__file__).parent))
from drive_client import _build_service
from style_presets import STYLE_FOLDER_NAMES

DB_PATH = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/data/velox/velox.db.sqlite"
IMAGES_DIR = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/data/images"
PARENT_ROOT_ID = "1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh"

service = None

def get_or_create_drive_folder(parent_id, name):
    global service
    if service is None:
        service = _build_service()
    # Search for folder
    query = f"'{parent_id}' in parents and name = '{name}' and mimeType = 'application/vnd.google-apps.folder' and trashed = false"
    response = service.files().list(q=query, fields="files(id)").execute()
    files = response.get("files", [])
    if files:
        return files[0]["id"]
    
    # Create folder
    metadata = {
        "name": name,
        "mimeType": "application/vnd.google-apps.folder",
        "parents": [parent_id]
    }
    folder = service.files().create(body=metadata, fields="id").execute()
    print(f"Created folder '{name}' (ID: {folder['id']}) under parent '{parent_id}'")
    return folder["id"]

def upload_file_to_drive(parent_id, local_path, filename, mimetype):
    global service
    if service is None:
        service = _build_service()
    # Check if file already exists in folder
    query = f"'{parent_id}' in parents and name = '{filename}' and trashed = false"
    response = service.files().list(q=query, fields="files(id, webViewLink)").execute()
    files = response.get("files", [])
    if files:
        print(f"File '{filename}' already exists on Drive (ID: {files[0]['id']})")
        return files[0]["id"], files[0]["webViewLink"]

    metadata = {
        "name": filename,
        "parents": [parent_id]
    }
    media = MediaFileUpload(str(local_path), mimetype=mimetype, resumable=True)
    file_obj = service.files().create(body=metadata, media_body=media, fields="id, webViewLink").execute()
    print(f"Uploaded file '{filename}' (ID: {file_obj['id']}) under folder '{parent_id}'")
    return file_obj["id"], file_obj["webViewLink"]

def ensure_style_folders(parent_id, style_names):
    folders = {}
    for style in style_names:
        folders[style] = get_or_create_drive_folder(parent_id, style)
    return folders

def slugify(text):
    if not text:
        return ""
    import re
    text = text.lower()
    text = re.sub(r'[^a-z0-9_\-\s]', '', text)
    text = re.sub(r'[\s_\-]+', '-', text)
    return text.strip("-")

def main():
    conn = sqlite3.connect(DB_PATH)
    cursor = conn.cursor()

    # 1. Recreate main images root folder
    # In Go code, images are placed inside "images" subfolder under ImagesRootFolder.
    # So we get or create 'images' folder under PARENT_ROOT_ID.
    images_root_id = get_or_create_drive_folder(PARENT_ROOT_ID, "images")
    
    # 2. Get or create Video AI root folder
    video_ai_root_id = get_or_create_drive_folder(PARENT_ROOT_ID, "Video Ai ")
    print(f"Video AI Root ID: {video_ai_root_id}")

    # 2b. Seed all notebook-style folders upfront so the tree exists even before first upload.
    ensure_style_folders(images_root_id, STYLE_FOLDER_NAMES)
    ensure_style_folders(video_ai_root_id, STYLE_FOLDER_NAMES)

    # 3. Retrieve all records from media_assets
    cursor.execute("SELECT id, media_type, source, local_path, metadata_json, drive_file_id FROM media_assets")
    rows = cursor.fetchall()
    
    print(f"Total rows retrieved from DB: {len(rows)}")

    for row in rows:
        asset_id, media_type, source, local_path, metadata_json, old_drive_file_id = row
        
        # We only restore assets that had a drive file ID or are generated images/videos
        is_drive_asset = old_drive_file_id and old_drive_file_id.strip() != ""
        if not is_drive_asset:
            continue

        meta = {}
        if metadata_json:
            try:
                meta = json.loads(metadata_json)
            except Exception as e:
                print(f"Error decoding metadata_json for {asset_id}: {e}")

        if media_type == "image":
            # Image recovery
            if not local_path:
                print(f"Skipping image {asset_id} because local_path is empty")
                continue
            
            full_local_path = Path(IMAGES_DIR) / local_path.split("?")[0]
            if not full_local_path.exists():
                print(f"Local file does not exist: {full_local_path}")
                continue

            style = meta.get("style", "general")
            if isinstance(style, list) and len(style) > 0:
                style = style[0]
            style = slugify(style) or "general"
            
            # The hash folder name is the asset_id or file_hash
            file_hash = meta.get("hash", asset_id)
            
            print(f"Restoring image {asset_id} (style: {style}, hash: {file_hash})")

            # Get or create style folder: images/{style}
            style_folder_id = get_or_create_drive_folder(images_root_id, style)
            
            # Get or create hash folder: images/{style}/{hash}
            hash_folder_id = get_or_create_drive_folder(style_folder_id, file_hash)

            # Upload image file
            mimetype = "image/jpeg"
            if full_local_path.suffix.lower() == ".png":
                mimetype = "image/png"
            elif full_local_path.suffix.lower() == ".gif":
                mimetype = "image/gif"
                
            drive_file_id, drive_link = upload_file_to_drive(
                hash_folder_id,
                full_local_path,
                f"{file_hash}{full_local_path.suffix}",
                mimetype
            )

            # Upload metadata.json
            metadata_file_path = full_local_path.parent / f"img_metadata_{file_hash}.json"
            # If temp metadata file doesn't exist, create it from DB meta
            if not metadata_file_path.exists():
                # Reconstruct semantic metadata payload
                semantic_meta = {
                    "asset_id": file_hash,
                    "asset_type": "image",
                    "prompt_original": meta.get("prompt", meta.get("description", "")),
                    "semantic_description": meta.get("description", ""),
                    "subjects": [meta.get("subject_id", "")],
                    "style": [style],
                    "generator": meta.get("generator", source),
                    "created_at": meta.get("created_at", "")
                }
                metadata_file_path.write_text(json.dumps(semantic_meta, indent=2))
            
            # Upload metadata.json next to the image
            upload_file_to_drive(hash_folder_id, metadata_file_path, "metadata.json", "application/json")
            
            # Cleanup temp metadata file if we created it
            if metadata_file_path.exists() and "img_metadata_" in metadata_file_path.name:
                os.remove(metadata_file_path)

            # Update DB with new Drive info
            cursor.execute(
                "UPDATE media_assets SET drive_file_id = ?, drive_link = ?, drive_folder_id = ? WHERE id = ?",
                (drive_file_id, drive_link, hash_folder_id, asset_id)
            )
            conn.commit()

        elif media_type == "video" and source == "google-vids":
            # Video recovery
            if not local_path:
                print(f"Skipping video {asset_id} because local_path is empty")
                continue
            
            full_local_path = Path(local_path)
            if not full_local_path.exists():
                print(f"Local file does not exist: {full_local_path}")
                continue

            style = meta.get("style", "general")
            style = slugify(style) or "general"
            
            prompt = meta.get("prompt", asset_id)
            subject = slugify(prompt)[:50] or asset_id

            print(f"Restoring video {asset_id} (style: {style}, subject: {subject})")

            # Get or create style folder inside Video AI root: Video Ai/{style}
            style_folder_id = get_or_create_drive_folder(video_ai_root_id, style)
            
            # Get or create subject folder: Video Ai/{style}/{subject}
            subject_folder_id = get_or_create_drive_folder(style_folder_id, subject)

            # Upload video file
            drive_file_id, drive_link = upload_file_to_drive(
                subject_folder_id,
                full_local_path,
                full_local_path.name,
                "video/mp4"
            )

            # Update DB with new Drive info
            cursor.execute(
                "UPDATE media_assets SET drive_file_id = ?, drive_link = ?, drive_folder_id = ? WHERE id = ?",
                (drive_file_id, drive_link, subject_folder_id, asset_id)
            )
            conn.commit()

    conn.close()
    print("Drive recovery and DB sync completed successfully!")

if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""
Scansiona TUTTE le cartelle del Drive Stock e salva la struttura in JSON.
Da eseguire UNA SOLA VOLTA per popolare il DB locale.

Usage:
    python3 scripts/scan_drive_stock.py
"""

import json
import os
import sys
from google.oauth2.credentials import Credentials
from googleapiclient.discovery import build
from googleapiclient.errors import HttpError

# CONFIG
TOKEN_FILE = "token.json"
CREDENTIALS_FILE = "credentials.json"
STOCK_ROOT_FOLDER_NAME = "Stock"  # Change if different
OUTPUT_FILE = "data/stock_drive_structure.json"

def get_drive_service():
    """Autentica con Google Drive"""
    if not os.path.exists(TOKEN_FILE):
        print(f"❌ Token file not found: {TOKEN_FILE}")
        print("Run OAuth flow first.")
        sys.exit(1)
    
    creds = Credentials.from_authorized_user_file(TOKEN_FILE)
    service = build('drive', 'v3', credentials=creds)
    return service

def get_stock_root_id(service):
    """Trova l'ID della cartella Stock root"""
    results = service.files().list(
        q=f"mimeType='application/vnd.google-apps.folder' and name='{STOCK_ROOT_FOLDER_NAME}' and trashed=false",
        fields="files(id, name)"
    ).execute()
    
    folders = results.get('files', [])
    if not folders:
        print(f"❌ Folder '{STOCK_ROOT_FOLDER_NAME}' not found!")
        sys.exit(1)
    
    return folders[0]['id']

def list_all_folders(service, parent_id, parent_path=""):
    """Lista ricorsivamente tutte le sottocartelle"""
    all_folders = []
    
    # Get immediate subfolders
    results = service.files().list(
        q=f"'{parent_id}' in parents and mimeType='application/vnd.google-apps.folder' and trashed=false",
        fields="files(id, name, webViewLink, modifiedTime)",
        orderBy="name",
        pageSize=100
    ).execute()
    
    folders = results.get('files', [])
    
    for folder in folders:
        folder_path = f"{parent_path}/{folder['name']}" if parent_path else folder['name']
        
        folder_info = {
            "id": folder['id'],
            "name": folder['name'],
            "path": folder_path,
            "url": folder.get('webViewLink', ''),
            "parent_id": parent_id,
            "subfolders": []
        }
        
        # Recursive call
        folder_info['subfolders'] = list_all_folders(service, folder['id'], folder_path)
        
        all_folders.append(folder_info)
    
    return all_folders

def list_clips_in_folder(service, folder_id):
    """Lista tutti i video in una cartella"""
    clips = []
    
    results = service.files().list(
        q=f"'{folder_id}' in parents and mimeType contains 'video/' and trashed=false",
        fields="files(id, name, webViewLink, videoMediaMetadata, modifiedTime, size)",
        orderBy="name",
        pageSize=100
    ).execute()
    
    files = results.get('files', [])
    
    for f in files:
        clip = {
            "id": f['id'],
            "name": f['name'],
            "url": f.get('webViewLink', ''),
            "size": int(f.get('size', 0)),
        }
        
        # Video metadata
        if 'videoMediaMetadata' in f:
            vmeta = f['videoMediaMetadata']
            clip['duration_ms'] = int(vmeta.get('durationMillis', 0))
            clip['width'] = int(vmeta.get('width', 0))
            clip['height'] = int(vmeta.get('height', 0))
        
        clips.append(clip)
    
    return clips

def flatten_folders(folder_list, parent_path=""):
    """Converte struttura annidata in lista piatta"""
    flat = []
    
    for folder in folder_list:
        flat_folder = {
            "id": folder["id"],
            "name": folder["name"],
            "path": folder["path"],
            "url": folder["url"],
            "parent_id": folder["parent_id"]
        }
        flat.append(flat_folder)
        
        # Add clips
        if "clips" in folder:
            for clip in folder["clips"]:
                flat.append({
                    "type": "clip",
                    "id": clip["id"],
                    "name": clip["name"],
                    "url": clip["url"],
                    "folder_id": folder["id"],
                    "folder_path": folder["path"],
                    **{k: v for k, v in clip.items() if k not in ["id", "name", "url"]}
                })
        
        # Recurse
        flat.extend(flatten_folders(folder.get("subfolders", []), folder["path"]))
    
    return flat

def main():
    print("=" * 60)
    print("📂 Drive Stock Scanner")
    print("=" * 60)
    
    # 1. Authenticate
    print("\n🔑 Authenticating...")
    service = get_drive_service()
    print("✅ Authenticated")
    
    # 2. Find Stock root
    print(f"\n📁 Finding '{STOCK_ROOT_FOLDER_NAME}' folder...")
    stock_root_id = get_stock_root_id(service)
    print(f"✅ Found: {STOCK_ROOT_FOLDER_NAME} (ID: {stock_root_id})")
    
    # 3. Scan all folders
    print("\n📋 Scanning all folders recursively...")
    folders = list_all_folders(service, stock_root_id, "Stock")
    print(f"✅ Found {count_folders(folders)} folders")
    
    # 4. Scan clips in each folder
    print("\n🎬 Scanning clips in each folder...")
    total_clips = 0
    scan_clips(folders, service, lambda n: print(f"  📹 {n}"))
    total_clips = count_clips(folders)
    print(f"\n✅ Found {total_clips} clips total")
    
    # 5. Build output structure
    output = {
        "stock_root_id": stock_root_id,
        "stock_root_name": STOCK_ROOT_FOLDER_NAME,
        "total_folders": count_folders(folders),
        "total_clips": total_clips,
        "folders": folders,
        "flat": flatten_folders(folders)
    }
    
    # 6. Save to file
    os.makedirs(os.path.dirname(OUTPUT_FILE), exist_ok=True)
    with open(OUTPUT_FILE, 'w') as f:
        json.dump(output, f, indent=2)
    
    print(f"\n💾 Saved to: {OUTPUT_FILE}")
    print(f"📊 Summary:")
    print(f"   - Folders: {output['total_folders']}")
    print(f"   - Clips: {output['total_clips']}")
    print(f"\n✅ Done! Now you can use this file to populate the DB.")

def count_folders(folders):
    count = 0
    for f in folders:
        count += 1
        count += count_folders(f.get("subfolders", []))
    return count

def count_clips(folders):
    count = 0
    for f in folders:
        count += len(f.get("clips", []))
        count += count_clips(f.get("subfolders", []))
    return count

def scan_clips(folders, service, progress_callback=None):
    for folder in folders:
        if progress_callback:
            progress_callback(folder["path"])
        
        clips = list_clips_in_folder(service, folder["id"])
        folder["clips"] = clips
        
        scan_clips(folder.get("subfolders", []), service, progress_callback)

if __name__ == "__main__":
    main()

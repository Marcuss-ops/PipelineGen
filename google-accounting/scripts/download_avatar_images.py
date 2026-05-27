"""Scarica tutte le immagini avatar dal Drive Folder e le salva con nome = avatar_id"""
import asyncio, sys, json, io, time
from pathlib import Path
sys.path.insert(0, str(Path(__file__).parent.parent))
from drive_client import _build_service
from googleapiclient.http import MediaIoBaseDownload

AVATAR_FOLDER_ID = "1bZXzNX_90A7-PVV1IELrKVQkhv0MziKp"
OUTPUT_DIR = Path(__file__).parent.parent / "data" / "avatars"

def download_file(service, file_id, mime_type, dest_path):
    if dest_path.exists():
        print(f"    already exists, skipping")
        return
    if mime_type and mime_type.startswith("application/vnd.google-apps"):
        export_mime = "image/png"
        request = service.files().export_media(fileId=file_id, mimeType=export_mime)
    else:
        request = service.files().get_media(fileId=file_id)
    fh = io.FileIO(str(dest_path), "wb")
    downloader = MediaIoBaseDownload(fh, request, chunksize=10*1024*1024)
    done = False
    while not done:
        _, done = downloader.next_chunk()
    fh.close()
    print(f"    OK {dest_path.name} ({dest_path.stat().st_size} bytes)")

def list_folder(service, folder_id):
    files = []
    page_token = None
    while True:
        results = service.files().list(
            q=f"'{folder_id}' in parents and trashed=false",
            fields="nextPageToken, files(id, name, mimeType, size, modifiedTime)",
            pageToken=page_token,
        ).execute()
        files.extend(results.get("files", []))
        page_token = results.get("nextPageToken")
        if not page_token:
            break
    return files

def run():
    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
    service = _build_service()

    # List avatar folders
    avatar_folders = list_folder(service, AVATAR_FOLDER_ID)
    print(f"Found {len(avatar_folders)} avatar folders\n")

    manifest = []
    for folder in avatar_folders:
        avatar_id = folder["name"]
        folder_id = folder["id"]
        print(f"\n[{avatar_id}] scanning folder...")

        # List contents of each avatar folder
        contents = list_folder(service, folder_id)
        print(f"  {len(contents)} files found")

        for item in contents:
            name = item["name"]
            mime = item["mimeType"]
            fid = item["id"]
            print(f"  file: {name:40s}  {mime:35s}  {fid}")

            # Skip folders, only download actual files
            if mime == "application/vnd.google-apps.folder":
                print("    (folder, skipping)")
                continue

            ext = Path(name).suffix.lower()
            if not ext:
                ext = ".png"
            dest = OUTPUT_DIR / f"{avatar_id}{ext}"

            download_file(service, fid, mime, dest)

            if dest.exists():
                manifest.append({
                    "avatar_id": avatar_id,
                    "file": str(dest),
                    "drive_file_id": fid,
                    "original_name": name,
                    "mimeType": mime,
                })

    manifest_path = OUTPUT_DIR / "manifest.json"
    manifest_path.write_text(json.dumps(manifest, indent=2))
    print(f"\nDone. {len(manifest)} avatar images in {OUTPUT_DIR}")
    print(f"Manifest: {manifest_path}")

if __name__ == "__main__":
    run()

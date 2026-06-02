import os
import asyncio
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional
from google.oauth2.credentials import Credentials
from google.auth.transport.requests import Request
from google_auth_oauthlib.flow import InstalledAppFlow
from googleapiclient.discovery import build
from googleapiclient.http import MediaIoBaseDownload, MediaFileUpload
import aiofiles
from config import GOOGLE_CREDENTIALS_PATH, TOKEN_PATH, DOWNLOAD_DIR, DRIVE_SCOPES, VIDS_MIME_TYPE


def _get_credentials() -> Credentials:
    creds = None
    if TOKEN_PATH.exists():
        try:
            creds = Credentials.from_authorized_user_file(str(TOKEN_PATH), DRIVE_SCOPES)
        except ValueError:
            import json

            token_data = json.loads(TOKEN_PATH.read_text())
            access_token = token_data.get("access_token")
            refresh_token = token_data.get("refresh_token")
            expiry = token_data.get("expiry")
            if access_token and refresh_token:
                client_data = json.loads(Path(GOOGLE_CREDENTIALS_PATH).read_text())
                installed = client_data.get("installed") or client_data.get("web") or {}
                expiry_dt = None
                if expiry:
                    try:
                        expiry_dt = datetime.fromisoformat(expiry.replace("Z", "+00:00"))
                        if expiry_dt.tzinfo is None:
                            expiry_dt = expiry_dt.replace(tzinfo=timezone.utc)
                    except ValueError:
                        expiry_dt = None
                creds = Credentials(
                    token=access_token,
                    refresh_token=refresh_token,
                    token_uri=installed.get("token_uri", "https://oauth2.googleapis.com/token"),
                    client_id=installed.get("client_id"),
                    client_secret=installed.get("client_secret"),
                    scopes=DRIVE_SCOPES,
                    expiry=expiry_dt,
                )
            else:
                raise

    if not creds or not creds.valid:
        if creds and creds.expired and creds.refresh_token:
            creds.refresh(Request())
        else:
            flow = InstalledAppFlow.from_client_secrets_file(
                str(GOOGLE_CREDENTIALS_PATH), DRIVE_SCOPES
            )
            creds = flow.run_local_server(port=0)
        with open(TOKEN_PATH, "w") as f:
            f.write(creds.to_json())

    return creds


def _build_service():
    return build("drive", "v3", credentials=_get_credentials())


def list_vids_projects() -> list[dict]:
    service = _build_service()
    results = (
        service.files()
        .list(
            q=f"mimeType='{VIDS_MIME_TYPE}' and trashed=false",
            fields="files(id, name, modifiedTime, size)",
            orderBy="modifiedTime desc",
        )
        .execute()
    )
    return results.get("files", [])


def list_exported_files(video_id: str) -> list[dict]:
    """List MP4/image files in the same Drive folder as the Vids project."""
    service = _build_service()

    file_meta = service.files().get(fileId=video_id, fields="parents").execute()
    parents = file_meta.get("parents", [])
    if not parents:
        return []

    parent_id = parents[0]
    results = (
        service.files()
        .list(
            q=(
                f"'{parent_id}' in parents and trashed=false and "
                "(mimeType='video/mp4' or mimeType contains 'image/')"
            ),
            fields="files(id, name, mimeType, size, modifiedTime)",
            orderBy="modifiedTime desc",
        )
        .execute()
    )
    return results.get("files", [])


async def download_file(file_id: str, filename: str, file_type: str) -> Path:
    service = _build_service()

    subdir = "videos" if file_type == "video" else "images"
    dest_dir = DOWNLOAD_DIR / subdir
    dest_dir.mkdir(parents=True, exist_ok=True)
    dest_path = dest_dir / filename

    request = service.files().get_media(fileId=file_id)

    def _do_download():
        import io
        fh = io.FileIO(str(dest_path), "wb")
        downloader = MediaIoBaseDownload(fh, request, chunksize=10 * 1024 * 1024)
        done = False
        while not done:
            _, done = downloader.next_chunk()
        fh.close()

    await asyncio.get_event_loop().run_in_executor(None, _do_download)
    return dest_path


def upload_file_to_drive(
    folder_id: str | None,
    local_path: Path,
    filename: str,
    mime_type: str = "application/octet-stream",
    drive_mime_type: str | None = None,
) -> str:
    """Upload a local file to a specific Google Drive folder.

    Args:
        folder_id: The Drive folder ID to upload into.
        local_path: Local Path to the file to upload.
        filename: Name to give the file in Drive.
        mime_type: MIME type of the file (e.g. 'video/mp4', 'image/png').

    Returns:
        The Drive file ID of the uploaded file.
    """
    import logging
    log = logging.getLogger("DriveClient")
    service = _build_service()
    file_metadata = {
        "name": filename,
    }
    if folder_id:
        file_metadata["parents"] = [folder_id]
    if drive_mime_type:
        file_metadata["mimeType"] = drive_mime_type
    media = MediaFileUpload(str(local_path), mimetype=mime_type, resumable=True)
    file = (
        service.files()
        .create(body=file_metadata, media_body=media, fields="id")
        .execute()
    )
    file_id = file.get("id", "")
    log.info("Uploaded %s to Drive folder %s → file_id=%s", filename, folder_id, file_id)
    return file_id

def get_google_doc_title(doc_id: str) -> str:
    """Get the title of a Google Docs document.
    
    Args:
        doc_id: The Google Docs document ID (from the URL)
        
    Returns:
        The document title, or empty string on failure.
    """
    import logging
    log = logging.getLogger("DriveClient")
    
    try:
        service = _build_service()
        file_info = service.files().get(fileId=doc_id, fields="name").execute()
        title = file_info.get("name", "")
        log.info("Retrieved title for Google Doc %s: %s", doc_id, title)
        return title
    except Exception as e:
        log.error("Failed to get title for Google Doc %s: %s", doc_id, e)
        return ""


def download_google_doc_text(doc_id: str) -> str:
    """Download the content of a Google Docs document as plain text.
    
    Args:
        doc_id: The Google Docs document ID (from the URL)
        
    Returns:
        The document content as plain text, or empty string on failure.
    """
    import logging
    log = logging.getLogger("DriveClient")
    
    try:
        service = _build_service()
        
        # Export as plain text
        request = service.files().export_media(
            fileId=doc_id,
            mimeType="text/plain"
        )
        
        import io
        fh = io.BytesIO()
        downloader = MediaIoBaseDownload(fh, request, chunksize=10 * 1024 * 1024)
        done = False
        while not done:
            _, done = downloader.next_chunk()
        
        fh.seek(0)
        content = fh.read().decode("utf-8")
        log.info("Downloaded Google Doc %s (%d chars)", doc_id, len(content))
        return content
        
    except Exception as e:
        log.error("Failed to download Google Doc %s: %s", doc_id, e)
        return ""


async def auto_upload_to_drive(file_path: str, drive_folder_id: str, media_type: str = "video"):
    """Upload a generated file to Google Drive folder."""
    import logging
    log = logging.getLogger("DriveClient")
    try:
        p = Path(file_path)
        if not p.exists():
            log.warning("Auto-upload skipped: file not found %s", file_path)
            return None
        
        mime_map = {
            "video": "video/mp4",
            "image": "image/jpeg",
        }
        ext = p.suffix.lower()
        mimetype = mime_map.get(media_type, "application/octet-stream")
        if ext == ".png":
            mimetype = "image/png"
        elif ext == ".gif":
            mimetype = "image/gif"
        elif ext == ".mp4":
            mimetype = "video/mp4"

        file_id = upload_file_to_drive(drive_folder_id, p, p.name, mimetype)
        log.info("Auto-uploaded %s to Drive folder %s → file_id=%s", p.name, drive_folder_id, file_id)
        return {"drive_file_id": file_id}
    except Exception as e:
        log.error("Auto-upload failed for %s: %s", file_path, e)
        return None

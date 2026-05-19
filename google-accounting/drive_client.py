import os
import asyncio
from pathlib import Path
from typing import Optional
from google.oauth2.credentials import Credentials
from google.auth.transport.requests import Request
from google_auth_oauthlib.flow import InstalledAppFlow
from googleapiclient.discovery import build
from googleapiclient.http import MediaIoBaseDownload
import aiofiles
from config import GOOGLE_CREDENTIALS_PATH, DOWNLOAD_DIR, DRIVE_SCOPES, VIDS_MIME_TYPE

TOKEN_PATH = "token.json"


def _get_credentials() -> Credentials:
    creds = None
    if Path(TOKEN_PATH).exists():
        creds = Credentials.from_authorized_user_file(TOKEN_PATH, DRIVE_SCOPES)

    if not creds or not creds.valid:
        if creds and creds.expired and creds.refresh_token:
            creds.refresh(Request())
        else:
            flow = InstalledAppFlow.from_client_secrets_file(
                GOOGLE_CREDENTIALS_PATH, DRIVE_SCOPES
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

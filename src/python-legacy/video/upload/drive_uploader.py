"""
Module for uploading generated videos to Google Drive and importing from it.
Handles folder creation/retrieval based on group name and file upload/download.
"""
import os
import logging
import time
import io
import re
from typing import Optional, Dict, Any, List
from googleapiclient.http import MediaFileUpload, MediaIoBaseDownload

# Import the existing drive service helper
try:
    from utils.scripts.standalone_multi_video_modules.drive.drive import _get_drive_service
except ImportError:
    # Fallback import attempt
    try:
        from modules.utils.drive_utils import _get_drive_service
    except ImportError:
        _get_drive_service = None

logger = logging.getLogger(__name__)


def _safe_folder_name(name: Optional[str]) -> str:
    """Nome cartella sicuro per Drive (strip, no vuoto)."""
    if not name or not str(name).strip():
        return "Ungrouped"
    return str(name).strip()[:200]


class DriveUploader:
    def __init__(self):
        self.service = None
        self.account_email = None
        if _get_drive_service:
            try:
                self.service = _get_drive_service()
                if self.service:
                    try:
                        info = self.service.about().get(fields="user(emailAddress,displayName)").execute()
                        user = (info or {}).get("user") or {}
                        self.account_email = user.get("emailAddress")
                        logger.info(
                            "Drive service initialized for account: %s",
                            self.account_email or user.get("displayName") or "unknown",
                        )
                    except Exception:
                        pass
            except Exception as e:
                logger.error(f"Failed to initialize Drive service: {e}")

    def get_or_create_folder(self, folder_name: str, parent_id: Optional[str] = None) -> Optional[str]:
        """
        Finds a folder by name (optionally within a parent) or creates it if not found.
        Returns the folder ID.
        """
        if not self.service:
            logger.warning("Drive service not available.")
            return None

        try:
            # Escape single quotes in folder name for the query
            safe_name = folder_name.replace("'", "\\'")
            query = f"mimeType='application/vnd.google-apps.folder' and name='{safe_name}' and trashed=false"
            if parent_id:
                query += f" and '{parent_id}' in parents"
            
            # List folders
            results = self.service.files().list(
                q=query,
                fields="files(id, name)",
                pageSize=1
            ).execute()
            
            files = results.get('files', [])
            
            if files:
                logger.info(f"Found existing folder '{folder_name}' (ID: {files[0]['id']})")
                return files[0]['id']
            
            # Create if not exists
            file_metadata = {
                'name': folder_name,
                'mimeType': 'application/vnd.google-apps.folder'
            }
            if parent_id:
                file_metadata['parents'] = [parent_id]
            
            file = self.service.files().create(body=file_metadata, fields='id').execute()
            folder_id = file.get('id')
            logger.info(f"Created new folder '{folder_name}' (ID: {folder_id})")
            return folder_id

        except Exception as e:
            logger.error(f"Error getting/creating folder '{folder_name}': {e}")
            return None

    def upload_video(self, file_path: str, group_name: str, parent_folder_id: Optional[str] = None) -> bool:
        """
        Uploads a video file to a Google Drive folder.
        If parent_folder_id is provided, it uploads there.
        If ONLY group_name is provided (and no parent_id), it finds/creates a folder with that name in root.
        """
        if not self.service:
            logger.error("Drive upload failed: Service not initialized.")
            return False

        if not os.path.exists(file_path):
            logger.error(f"File not found: {file_path}")
            return False

        try:
            folder_id = parent_folder_id
            
            # If no specific parent folder ID is given, find/create based on group_name in root
            if not folder_id:
                folder_id = self.get_or_create_folder(group_name)
            
            if not folder_id:
                logger.error(f"Could not get destination folder ID for '{group_name}'")
                return False

            # 2. Prepare upload
            file_metadata = {
                'name': os.path.basename(file_path),
                'parents': [folder_id]
            }
            
            media = MediaFileUpload(
                file_path, 
                mimetype='video/mp4',
                resumable=True
            )

            # 3. Upload
            logger.info(f"Starting upload of {os.path.basename(file_path)} to folder ID: {folder_id}...")
            request = self.service.files().create(
                body=file_metadata,
                media_body=media,
                fields='id, webViewLink'
            )
            
            response = None
            while response is None:
                status, response = request.next_chunk()
                # progress could be handled here if needed

            file_id = response.get("id")
            folder_link = f"https://drive.google.com/drive/folders/{folder_id}"
            file_link = response.get("webViewLink") or (f"https://drive.google.com/file/d/{file_id}/view" if file_id else None)
            logger.info(f"Upload complete! File ID: {file_id}")
            logger.info(f"Drive folder link: {folder_link}")
            if file_link:
                logger.info(f"Drive file link: {file_link}")
            return {
                "success": True,
                "link": file_link,
                "folder_link": folder_link,
                "file_id": file_id,
                "folder_id": folder_id,
            }

        except Exception as e:
            logger.error(f"Error uploading video to Drive: {e}")
            return {"success": False, "error": str(e)}

    def upload_video_to_project_folder(self, file_path: str, group_name: str, project_name: str) -> bool:
        """
        Uploads a video to Drive under:
        ROOT -> project_name
        Uses DRIVE_ROOT_FOLDER_ID from config as the base if available.
        The `group_name` parameter is kept for backward compatibility but not used for folder nesting.
        """
        if not self.service:
            return False
            
        try:
            # Import config here to avoid circular imports.
            # Priority:
            # 1) modules.core.config.DRIVE_ROOT_FOLDER_ID
            # 2) config.config.DRIVE_ROOT_FOLDER_ID (legacy location)
            root_id = None
            try:
                from modules.core.config import DRIVE_ROOT_FOLDER_ID  # type: ignore
                root_id = DRIVE_ROOT_FOLDER_ID
            except Exception:
                root_id = None
            if not root_id:
                try:
                    from config.config import DRIVE_ROOT_FOLDER_ID  # type: ignore
                    root_id = DRIVE_ROOT_FOLDER_ID
                except Exception:
                    root_id = None

            safe_project = _safe_folder_name(project_name or group_name or "Ungrouped")
            logger.info(f"Drive upload target root_id={root_id or 'root'} project_folder='{safe_project}'")

            # Ensure project folder directly under root
            project_id = self.get_or_create_folder(safe_project, parent_id=root_id)
            if not project_id:
                return False

            return self.upload_video(file_path, safe_project, parent_folder_id=project_id)

        except Exception as e:
            logger.error(f"Error uploading to project folder: {e}")
            return {"success": False, "error": str(e)}

    def download_files_from_folder(self, folder_link_or_id: str, destination_dir: str) -> List[str]:
        """
        Downloads all files from a Drive folder to a local directory.
        Returns list of downloaded file paths.
        """
        if not self.service:
            return []
            
        # Extract ID from link if needed
        folder_id = folder_link_or_id
        if "drive.google.com" in folder_link_or_id:
            # Try to extract ID from URL
            match = re.search(r"folders\/([a-zA-Z0-9-_]+)", folder_link_or_id)
            if match:
                folder_id = match.group(1)
            else:
                match = re.search(r"[?&]id=([a-zA-Z0-9-_]+)", folder_link_or_id)
                if match:
                    folder_id = match.group(1)
                    
        os.makedirs(destination_dir, exist_ok=True)
        downloaded_files = []
        
        try:
            # List files
            query = f"'{folder_id}' in parents and trashed=false and mimeType != 'application/vnd.google-apps.folder'"
            results = self.service.files().list(
                q=query,
                fields="files(id, name, mimeType)",
                pageSize=100
            ).execute()
            
            files = results.get('files', [])
            logger.info(f"Found {len(files)} files in Drive folder {folder_id}")
            
            for file in files:
                file_id = file['id']
                file_name = file['name']
                dest_path = os.path.join(destination_dir, file_name)
                
                logger.info(f"Downloading {file_name}...")
                request = self.service.files().get_media(fileId=file_id)
                fh = io.FileIO(dest_path, 'wb')
                downloader = MediaIoBaseDownload(fh, request)
                done = False
                while done is False:
                    status, done = downloader.next_chunk()
                
                downloaded_files.append(dest_path)
                logger.info(f"Downloaded {dest_path}")
                
            return downloaded_files
            
        except Exception as e:
            logger.error(f"Error downloading files from folder {folder_id}: {e}")
            return downloaded_files

    def list_folder_content(self, folder_id: Optional[str] = None) -> List[Dict[str, Any]]:
        """
        Lists files and folders within a specific folder.
        Returns a list of dicts with id, name, mimeType, iconLink.
        """
        if not self.service:
            return []
            
        target_id = folder_id or 'root'
        try:
            query = f"'{target_id}' in parents and trashed=false"
            
            results = self.service.files().list(
                q=query,
                fields="files(id, name, mimeType, iconLink, webViewLink, thumbnailLink, webContentLink)",
                pageSize=1000,
                orderBy="folder,name" 
            ).execute()
            
            files = results.get('files', [])
            return files
            
        except Exception as e:
            logger.error(f"Error listing folder content for {target_id}: {e}")
            return []

    def download_file(self, file_id: str, destination_path: str) -> bool:
        """
        Downloads a single file from Drive by ID to a local destination path.
        """
        if not self.service:
            logger.error("Drive service not available.")
            return False
            
        try:
            request = self.service.files().get_media(fileId=file_id)
            with io.FileIO(destination_path, 'wb') as fh:
                downloader = MediaIoBaseDownload(fh, request)
                done = False
                while done is False:
                    status, done = downloader.next_chunk()
            logger.info(f"Downloaded file {file_id} to {destination_path}")
            return True
        except Exception as e:
            logger.error(f"Error downloading file {file_id}: {e}")
            return False

def upload_video_to_group_folder(video_path: str, group_name: str, project_name: Optional[str] = None) -> Dict[str, Any]:
    """Upload su Drive: la cartella ha il nome del progetto (titolo), non del gruppo. Se manca project_name usa group_name."""
    uploader = DriveUploader()
    return uploader.upload_video_to_project_folder(video_path, group_name, project_name or group_name or "Ungrouped")

from pathlib import Path
import json
from google.oauth2.credentials import Credentials
from google_auth_oauthlib.flow import InstalledAppFlow
from googleapiclient.discovery import build
from googleapiclient.http import MediaFileUpload

SCOPES = ['https://www.googleapis.com/auth/drive.file']

class DriveUploader:
    def __init__(self, credentials_path: Path = None, token_path: Path = None):
        self.credentials_path = credentials_path or Path("credentials.json")
        self.token_path = token_path or Path("token.json")
        self.service = None
    
    def authenticate(self):
        creds = None
        if self.token_path.exists():
            try:
                creds = Credentials.from_authorized_user_info(
                    json.loads(self.token_path.read_text()),
                    SCOPES
                )
            except Exception:
                creds = None
        
        if not creds or not creds.valid:
            if creds and creds.expired and creds.refresh_token:
                creds.refresh()
            else:
                flow = InstalledAppFlow.from_client_secrets_file(
                    str(self.credentials_path), SCOPES
                )
                creds = flow.run_local_server(port=0)
            
            self.token_path.write_text(creds.to_json())
        
        self.service = build('drive', 'v3', credentials=creds)
    
    def upload_file(self, file_path: Path, folder_id: str = None) -> str:
        if not self.service:
            self.authenticate()
        
        file_metadata: dict = {'name': file_path.name}
        if folder_id:
            file_metadata['parents'] = [folder_id]
        
        media = MediaFileUpload(str(file_path), resumable=True)
        file = self.service.files().create(
            body=file_metadata,
            media_body=media,
            fields='id, webViewLink'
        ).execute()
        
        return file.get('webViewLink')
    
    def upload_multiple(self, file_paths: list[Path], folder_id: str = None) -> list[str]:
        links = []
        for file_path in file_paths:
            print(f"Uploading: {file_path.name}")
            link = self.upload_file(file_path, folder_id)
            links.append(link)
            print(f"  Uploaded: {link}")
        return links
    
    def create_folder(self, name: str, parent_id: str = None) -> str:
        if not self.service:
            self.authenticate()
        
        file_metadata: dict = {
            'name': name,
            'mimeType': 'application/vnd.google-apps.folder',
        }
        if parent_id:
            file_metadata['parents'] = [parent_id]
        
        folder = self.service.files().create(
            body=file_metadata,
            fields='id'
        ).execute()
        
        return folder.get('id')

    def upload_with_folder_split(
        self, 
        file_paths: list[Path], 
        base_folder_name: str,
        parent_folder_id: str = None,
        max_per_folder: int = 49
    ) -> dict:
        if not self.service:
            self.authenticate()
        
        results = {}
        total_files = len(file_paths)
        
        num_folders = (total_files + max_per_folder - 1) // max_per_folder
        
        for i in range(num_folders):
            start_idx = i * max_per_folder
            end_idx = min(start_idx + max_per_folder, total_files)
            folder_files = file_paths[start_idx:end_idx]
            
            if i == 0:
                folder_name = base_folder_name
            else:
                folder_name = f"{base_folder_name} (V{i+1})"
            
            print(f"\nCreating folder: {folder_name} ({len(folder_files)} files)")
            folder_id = self.create_folder(folder_name, parent_folder_id)
            
            links = []
            for file_path in folder_files:
                print(f"  Uploading: {file_path.name}")
                link = self.upload_file(file_path, folder_id)
                links.append(link)
            
            results[folder_name] = {
                'folder_id': folder_id,
                'links': links
            }
            print(f"  Uploaded {len(links)} files to {folder_name}")
        
        return results

import sys
from pathlib import Path
from typing import Tuple
from datetime import datetime
from .config import LANGUAGES, _PROJECT_ROOT

try:
    sys.path.insert(0, str(_PROJECT_ROOT / "google-accounting"))
    from drive_client import upload_file_to_drive as _drive_upload, _build_service
    HAS_DRIVE = True
except ImportError:
    _drive_upload = None
    _build_service = None
    HAS_DRIVE = False

def create_book_drive_folder(book_name: str, drive_folder_id: str, language: str = "en") -> Tuple[str, str]:
    if not HAS_DRIVE or not _build_service:
        return "", ""
    
    try:
        service = _build_service()
        book_folder_name = f"{book_name[:50]}"
        
        book_folder = {
            "name": book_folder_name,
            "mimeType": "application/vnd.google-apps.folder",
            "parents": [drive_folder_id] if drive_folder_id else []
        }
        
        created_book_folder = service.files().create(body=book_folder, fields="id, name").execute()
        book_folder_id = created_book_folder.get("id")
        print(f"  Created Drive folder: {book_folder_name} ({book_folder_id})")
        
        lang_name = LANGUAGES.get(language.lower(), language)
        lang_folder_name = f"{lang_name}"
        
        lang_folder = {
            "name": lang_folder_name,
            "mimeType": "application/vnd.google-apps.folder",
            "parents": [book_folder_id]
        }
        
        created_lang_folder = service.files().create(body=lang_folder, fields="id, name").execute()
        lang_folder_id = created_lang_folder.get("id")
        print(f"  Created language subfolder: {lang_folder_name} ({lang_folder_id})")
        
        pdf_folder = {
            "name": "PDF",
            "mimeType": "application/vnd.google-apps.folder",
            "parents": [lang_folder_id]
        }
        created_pdf_folder = service.files().create(body=pdf_folder, fields="id, name").execute()
        pdf_folder_id = created_pdf_folder.get("id")
        print(f"  Created PDF subfolder ({pdf_folder_id})")
        
        return book_folder_id, pdf_folder_id
        
    except Exception as e:
        print(f"  Drive folder creation error: {e}")
        return "", ""

def upload_book_to_drive(summary_path: Path, pdf_path: Path, book_name: str, 
                         drive_folder_id: str, language: str = "en") -> dict:
    if not HAS_DRIVE:
        return {"success": False, "error": "Drive client not available"}
    
    try:
        book_folder_id, pdf_folder_id = create_book_drive_folder(book_name, drive_folder_id, language)
        
        if not book_folder_id:
            book_folder_id = drive_folder_id
            pdf_folder_id = drive_folder_id
        
        results = {"success": True, "folders": {}, "files": {}}
        
        if summary_path.exists():
            doc_name = f"{book_name}_rewritten.txt"
            
            file_id = _drive_upload(
                folder_id=book_folder_id,
                local_path=summary_path,
                filename=doc_name,
                mime_type="text/plain",
                drive_mime_type="application/vnd.google-apps.document",
            )
            if file_id:
                doc_link = f"https://docs.google.com/document/d/{file_id}/edit"
                print(f"  [OK] Uploaded text as Google Doc: {doc_link}")
                results["files"]["document"] = {"id": file_id, "link": doc_link}
        
        if pdf_path and Path(pdf_path).exists():
            pdf_name = f"{book_name}.pdf"
            pdf_folder = pdf_folder_id if pdf_folder_id else book_folder_id
            
            pdf_id = _drive_upload(
                folder_id=pdf_folder,
                local_path=pdf_path,
                filename=pdf_name,
                mime_type="application/pdf",
            )
            if pdf_id:
                pdf_link = f"https://drive.google.com/file/d/{pdf_id}/view"
                print(f"  [OK] Uploaded PDF: {pdf_link}")
                results["files"]["pdf"] = {"id": pdf_id, "link": pdf_link}
        
        if book_folder_id:
            results["folders"]["book"] = {
                "id": book_folder_id,
                "link": f"https://drive.google.com/drive/folders/{book_folder_id}"
            }
        
        return results
        
    except Exception as e:
        print(f"  Drive upload error: {e}")
        return {"success": False, "error": str(e)}
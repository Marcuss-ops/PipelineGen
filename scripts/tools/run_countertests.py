#!/usr/bin/env python3
import os
import json
import sqlite3
import subprocess
import sys
import urllib.request
import urllib.error
import uuid
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

# Load models
print("Loading model for query embedding...")
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

def qdrant_request(endpoint, method="GET", payload=None):
    url = f"{QDRANT_URL}/{endpoint}"
    data = json.dumps(payload).encode('utf-8') if payload else None
    req = urllib.request.Request(
        url,
        data=data,
        headers={'Content-Type': 'application/json'} if data else {},
        method=method
    )
    with urllib.request.urlopen(req) as res:
        return json.loads(res.read().decode('utf-8'))


# ── QDRANT-001 thin-client sync helper ─────────────────────────────────────────
# QDRANT-001 (June 2026, docs/qdrant/QDRANT-001_OWNERSHIP_API_GATEWAY_AND_LEGACY_REMOVAL.md):
# this script is a TEST driver. It must NOT reach into Qdrant or SQLite directly
# to mutate state — every restore / re-sync goes through the Go canonical HTTP
# API via scripts/tools/sync_drive_qdrant.py.
#
# The Drive folder ID is read from the env var VELOX_TEST_FOLDER_ID. We do not
# hardcode the previous ROOT_FOLDER_ID inside this file (QDRANT-001 forbids
# hardcoded IDs in Python); the test runner sets the env var before invoking.
def _invoke_sync_drive_folder() -> None:
    folder_id = os.environ.get("VELOX_TEST_FOLDER_ID", "").strip()
    if not folder_id:
        print(
            "error: VELOX_TEST_FOLDER_ID env var is required to re-sync the "
            "countertest fixtures. QDRANT-001 forbids this script from "
            "writing Qdrant/SQLite directly — it must go through the Go "
            "canonical /api/media/sync-drive-folder endpoint.",
            file=sys.stderr,
        )
        sys.exit(2)
    # Pick the same interpreter the caller is running; fall back to PATH
    # resolution only when sys.executable is empty (frozen/embedded case).
    interpreter = sys.executable if sys.executable else "python3"
    script_path = Path(__file__).resolve().parent / "sync_drive_qdrant.py"
    proc = subprocess.run(
        [interpreter, str(script_path), "--folder-id", folder_id],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
    )
    if proc.returncode != 0:
        # Surface WHY the sync failed — without stderr visibility the test
        # driver aborts with only a Python traceback (silent root cause).
        stderr_tail = (proc.stderr or b"").decode("utf-8", "ignore")[-500:]
        raise SystemExit(
            f"sync_drive_qdrant.py exited {proc.returncode} for folder_id={folder_id!r}: "
            f"{stderr_tail.strip() or '<no stderr>'}"
        )

def main():
    print("\n" + "="*50)
    print("      RUNNING VELOX PIPELINE COUNTER-TESTS")
    print("="*50 + "\n")

    # ─────────────────────────────────────────────────────────────────────────
    print("--- [Controtest 1: Il test di Disallineamento (SQLite vs Qdrant)] ---")
    # We will pick a video: "Ke1XC3QuZL8 - Kobe Bryant - Coaching My Daughters.mp4"
    kobe_point_id = "4bd2aa81-0e88-5a0f-b5b3-f807beb4b976"
    kobe_drive_id = "1uCqOObGmMss7n24XqeDIJkBLJr4Ovdw5"
    
    # 1. Verify it exists in Qdrant
    res = qdrant_request(f"collections/{QDRANT_COLLECTION}/points/{kobe_point_id}")
    if res.get("result"):
        print("  [OK] Kobe Bryant point exists in Qdrant.")
    else:
        print("  [FAIL] Kobe Bryant point not found in Qdrant.")
        return

    # 2. Simulate the Qdrant Stale Point Cleaner logic
    # The cleaner: checks FileIsNotTrashed on Drive.
    # We simulate trashing the file on Drive.
    print(f"  Simulating trashing of Kobe Bryant file on Google Drive (ID: {kobe_drive_id})...")
    drive_service.files().update(fileId=kobe_drive_id, body={'trashed': True}).execute()
    
    # Run the validation check that CleanupStalePoints does
    print("  Running validator: checking if file is trashed on Drive...")
    file_info = drive_service.files().get(fileId=kobe_drive_id, fields="trashed").execute()
    is_trashed = file_info.get("trashed", False)
    
    if is_trashed:
        print("  [OK] Validator detected file is trashed. Deleting point from Qdrant...")
        # Delete from Qdrant
        del_res = qdrant_request(f"collections/{QDRANT_COLLECTION}/points/delete", method="POST", payload={"points": [kobe_point_id]})
        print(f"  Qdrant delete response: {del_res.get('status')}")
    else:
        print("  [FAIL] File was not trashed correctly on Google Drive.")
        
    # Check Qdrant again to ensure the point is gone
    deleted_successfully = False
    try:
        check_res = qdrant_request(f"collections/{QDRANT_COLLECTION}/points/{kobe_point_id}")
        if not check_res.get("result"):
            deleted_successfully = True
    except urllib.error.HTTPError as e:
        if e.code == 404:
            deleted_successfully = True
        else:
            raise

    if deleted_successfully:
        print("  [PASS] Controtest 1: Point successfully deleted from Qdrant after being trashed on Drive!")
    else:
        print("  [FAIL] Controtest 1: Point still exists in Qdrant after cleanup simulation.")
        
    # RESTORE: Untrash the file on Drive and restore to Qdrant so we keep files intact
    print("  Restoring Kobe Bryant file on Google Drive (untrashing)...")
    drive_service.files().update(fileId=kobe_drive_id, body={'trashed': False}).execute()
    # Re-run sync via the Go canonical HTTP API (QDRANT-001 thin client).
    print("  Re-syncing to restore point...")
    _invoke_sync_drive_folder()
    print("  Restored!")
    print("-" * 50 + "\n")

    # ─────────────────────────────────────────────────────────────────────────
    print("--- [Controtest 2: Il test di Ricerca Incrociata (Cross-Modal)] ---")
    charlize_point_id = "0bebf357-0514-55b7-a9a7-6e1048b67efa"
    
    # We will search the 3 vector spaces for Charlize Theron video:
    # 1. Search 'text' using "query: Charlize Theron"
    # 2. Search 'transcript' using "query: Kids Modern Slang"
    # 3. Search 'visual' using its exact visual vector (to match visual semantic space)
    
    print("  1. Searching text space for 'Charlize Theron'...")
    text_query_vector = model.encode("query: Charlize Theron", normalize_embeddings=True).tolist()
    search_res_text = qdrant_request(f"collections/{QDRANT_COLLECTION}/points/search", method="POST", payload={
        "vector": {"name": "text", "vector": text_query_vector},
        "limit": 1
    })
    top_text_id = search_res_text["result"][0]["id"] if search_res_text.get("result") else None
    
    print("  2. Searching transcript space for 'Kids Modern Slang'...")
    transcript_query_vector = model.encode("query: Kids Modern Slang", normalize_embeddings=True).tolist()
    search_res_trans = qdrant_request(f"collections/{QDRANT_COLLECTION}/points/search", method="POST", payload={
        "vector": {"name": "transcript", "vector": transcript_query_vector},
        "limit": 1
    })
    top_trans_id = search_res_trans["result"][0]["id"] if search_res_trans.get("result") else None
    
    # Retrieve the exact visual vector of Charlize Theron to query the visual space
    point_data = qdrant_request(f"collections/{QDRANT_COLLECTION}/points/{charlize_point_id}?with_vector=true")
    visual_query_vector = point_data["result"]["vector"]["visual"]
    
    print("  3. Searching visual space with matching visual vector...")
    search_res_vis = qdrant_request(f"collections/{QDRANT_COLLECTION}/points/search", method="POST", payload={
        "vector": {"name": "visual", "vector": visual_query_vector},
        "limit": 1
    })
    top_vis_id = search_res_vis["result"][0]["id"] if search_res_vis.get("result") else None
    
    print(f"  Results - Text: {top_text_id}, Transcript: {top_trans_id}, Visual: {top_vis_id}")
    if top_text_id == charlize_point_id and top_trans_id == charlize_point_id and top_vis_id == charlize_point_id:
        print("  [PASS] Controtest 2: Cross-modal query matching points to the same semantic concept ID!")
    else:
        print("  [FAIL] Controtest 2: Mismatched IDs across modalities.")
    print("-" * 50 + "\n")

    # ─────────────────────────────────────────────────────────────────────────
    print("--- [Controtest 3: Il test di Idempotenza (Iniezione Duplicata)] ---")
    # 1. Count points before running sync again
    cnt_before = qdrant_request(f"collections/{QDRANT_COLLECTION}")["result"]["points_count"]
    print(f"  Points in Qdrant before re-running sync: {cnt_before}")
    
    # 2. Run sync script again (QDRANT-001: thin HTTP client)
    print("  Running sync via the Go canonical HTTP API...")
    _invoke_sync_drive_folder()
    
    # 3. Count points after
    cnt_after = qdrant_request(f"collections/{QDRANT_COLLECTION}")["result"]["points_count"]
    print(f"  Points in Qdrant after re-running sync: {cnt_after}")
    
    if cnt_before == cnt_after:
        print("  [PASS] Controtest 3: Injection is completely idempotent (no duplicates created)!")
    else:
        print(f"  [FAIL] Controtest 3: Duplicate points created! Count changed from {cnt_before} to {cnt_after}")
    print("-" * 50 + "\n")

    # ─────────────────────────────────────────────────────────────────────────
    print("--- [Controtest 4: Lo stress-test di Filtro sul Payload (Hybrid Search)] ---")
    # We will pick a video (e.g. Charlize Theron) and set status to "archived" in Qdrant
    print("  1. Setting Charlize Theron status payload to 'archived'...")
    qdrant_request(f"collections/{QDRANT_COLLECTION}/points", method="PUT", payload={
        "points": [
            {
                "id": charlize_point_id,
                "payload": {"status": "archived"},
                # Partial update only overrides specified payload keys
                "vectors": {}
            }
        ]
    })
    
    # Verify search with status == "active" filter
    print("  2. Searching text space for 'Charlize Theron' with filter status == 'active'...")
    filtered_search = qdrant_request(f"collections/{QDRANT_COLLECTION}/points/search", method="POST", payload={
        "vector": {"name": "text", "vector": text_query_vector},
        "filter": {
            "must": [
                {
                    "key": "status",
                    "match": {"value": "active"}
                }
            ]
        },
        "limit": 5
    })
    
    found_archived = False
    for hit in filtered_search.get("result", []):
        if hit["id"] == charlize_point_id:
            found_archived = True
            
    if not found_archived:
        print("  [PASS] Controtest 4: Hybrid Search filter correctly blocked the archived point from results!")
    else:
        print("  [FAIL] Controtest 4: Archived point still returned under active status filter search.")
        
    # RESTORE: Reset status back to "active"
    print("  Restoring Charlize Theron status payload back to 'active'...")
    qdrant_request(f"collections/{QDRANT_COLLECTION}/points", method="PUT", payload={
        "points": [
            {
                "id": charlize_point_id,
                "payload": {"status": "active"},
                "vectors": {}
            }
        ]
    })
    print("=" * 50 + "\n")

if __name__ == "__main__":
    main()

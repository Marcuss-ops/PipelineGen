import requests
import argparse
import json
import os
import sqlite3
from datetime import datetime, timedelta

class SketchfabClient:
    BASE_URL = "https://api.sketchfab.com/v3"

    def __init__(self, api_token):
        self.api_token = api_token
        self.headers = {
            "Authorization": f"Token {api_token}"
        }

    def search_models(self, query, limit=24):
        """Search for downloadable models by tag/query."""
        url = f"{self.BASE_URL}/models"
        params = {
            "q": query,
            "downloadable": "true",
            "count": limit,
            "sort_by": "-likeCount"
        }
        
        response = requests.get(url, params=params)
        response.raise_for_status()
        return response.json()

    def get_download_url(self, model_uid):
        """Request a download URL for a model."""
        url = f"{self.BASE_URL}/models/{model_uid}/download"
        response = requests.get(url, headers=self.headers)
        response.raise_for_status()
        return response.json()

def update_db(db_path, models_data):
    """Update sketchfab_models table with search results."""
    conn = sqlite3.connect(db_path)
    cursor = conn.cursor()
    
    for model in models_data.get('results', []):
        uid = model['uid']
        name = model['name']
        user_name = model.get('user', {}).get('username', 'unknown')
        license_type = model.get('license', {}).get('label', 'unknown')
        
        # Get thumbnail
        thumb_url = ""
        thumbnails = model.get('thumbnails', {}).get('images', [])
        if thumbnails:
            # Pick a medium sized one
            thumb_url = thumbnails[0].get('url', "")
            for img in thumbnails:
                if img.get('width', 0) >= 400:
                    thumb_url = img['url']
                    break
        
        view_url = model['viewerUrl']
        
        cursor.execute('''
            INSERT INTO sketchfab_models (uid, name, user_name, license_type, thumb_url, view_url)
            VALUES (?, ?, ?, ?, ?, ?)
            ON CONFLICT(uid) DO UPDATE SET
                name=excluded.name,
                user_name=excluded.user_name,
                license_type=excluded.license_type,
                thumb_url=excluded.thumb_url,
                view_url=excluded.view_url
        ''', (uid, name, user_name, license_type, thumb_url, view_url))
        
    conn.commit()
    conn.close()

def update_download_link(db_path, uid, download_data):
    """Update a model with its temporary download link."""
    conn = sqlite3.connect(db_path)
    cursor = conn.cursor()
    
    # download_data usually contains links for gltf, usdz, etc.
    # We'll pick gltf (source) or glb
    gltf = download_data.get('gltf', {})
    download_url = gltf.get('url', "")
    expires_in = gltf.get('expiresIn', 300) # seconds
    
    expires_at = (datetime.now() + timedelta(seconds=expires_in)).isoformat()
    
    cursor.execute('''
        UPDATE sketchfab_models
        SET download_url = ?, download_expires_at = ?, metadata_json = ?
        WHERE uid = ?
    ''', (download_url, expires_at, json.dumps(download_data), uid))
    
    conn.commit()
    conn.close()

def main():
    parser = argparse.ArgumentParser(description="Sketchfab API Client")
    parser.add_argument("--db", required=True, help="Path to media.db.sqlite")
    parser.add_argument("--token", help="Sketchfab API Token")
    parser.add_argument("--search", help="Search query")
    parser.add_argument("--download", help="Model UID to get download link for")
    parser.add_argument("--limit", type=int, default=24, help="Search limit")
    
    args = parser.parse_args()
    
    token = args.token or os.environ.get("SKETCHFAB_API_TOKEN")
    if not token:
        print("Error: Sketchfab API Token is required (--token or SKETCHFAB_API_TOKEN env)")
        return

    client = SketchfabClient(token)
    
    if args.search:
        print(f"Searching for: {args.search}...")
        results = client.search_models(args.search, limit=args.limit)
        update_db(args.db, results)
        print(f"Found and saved {len(results.get('results', []))} models.")
        
    if args.download:
        print(f"Requesting download link for {args.download}...")
        download_info = client.get_download_url(args.download)
        update_download_link(args.db, args.download, download_info)
        print(f"Download link updated (expires in 5 minutes).")
        print(json.dumps(download_info, indent=2))

if __name__ == "__main__":
    main()

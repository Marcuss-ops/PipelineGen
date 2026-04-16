#!/usr/bin/env python3
"""
Scarica clip da Artlist (URL m3u8), converte a 1920x1080 MP4,
e le carica su Google Drive in Stock/Artlist/
"""

import os
import re
import json
import sqlite3
import subprocess
import tempfile
import hashlib
from datetime import datetime

# File per tracciare le clip già caricate
TRACKING_FILE = "/home/pierone/Pyt/VeloxEditing/refactored/data/artlist_uploaded.json"

def load_uploaded_clips():
    """Carica l'elenco delle clip già caricate su Drive"""
    if os.path.exists(TRACKING_FILE):
        with open(TRACKING_FILE) as f:
            return json.load(f)
    return {"uploaded": {}, "drive_files": {}}

def save_uploaded_clips(data):
    """Salva l'elenco delle clip caricate"""
    with open(TRACKING_FILE, 'w') as f:
        json.dump(data, f, indent=2)

def get_clip_hash(url):
    """Genera un hash univoco dall'URL m3u8"""
    return hashlib.md5(url.encode()).hexdigest()[:16]

from google.oauth2.credentials import Credentials
from googleapiclient.discovery import build
from googleapiclient.http import MediaFileUpload
from google.auth.transport.requests import Request

ARTLIST_DB = "/home/pierone/Pyt/VeloxEditing/refactored/src/node-scraper/artlist_videos.db"
TOKEN_FILE = "/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/token.json"
STOCK_FOLDER_ID = "1aMjQlK9J1mEyT2TOYDNjeynO1GzZS4_S"
TEMP_DIR = "/tmp/artlist_downloads"

# Quante clip per termine scaricare
CLIPS_PER_TERM = 10

def get_google_creds():
    with open(TOKEN_FILE) as f:
        td = json.load(f)
    creds = Credentials(
        token=td['access_token'],
        refresh_token=td['refresh_token'],
        token_uri='https://oauth2.googleapis.com/token',
        client_id='964460747662-8oielvpbphij44agin684r57ojfio9h1.apps.googleusercontent.com',
        client_secret='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',
    )
    if creds.expired:
        creds.refresh(Request())
        td['access_token'] = creds.token
        with open(TOKEN_FILE, 'w') as f:
            json.dump(td, f, indent=2)
    return creds

def get_or_create_folder(drive, parent_id, folder_name):
    """Trova o crea una sottocartella"""
    results = drive.files().list(
        q=f"'{parent_id}' in parents and mimeType='application/vnd.google-apps.folder' and name='{folder_name}'",
        fields="files(id, name)"
    ).execute()
    
    folders = results.get('files', [])
    if folders:
        print(f"  📁 Cartella esistente: {folder_name}")
        return folders[0]['id']
    
    folder_metadata = {
        'name': folder_name,
        'mimeType': 'application/vnd.google-apps.folder',
        'parents': [parent_id]
    }
    folder = drive.files().create(body=folder_metadata, fields='id').execute()
    print(f"  📁 Cartella creata: {folder_name} ({folder['id']})")
    return folder['id']

def download_and_convert(m3u8_url, output_path, max_duration=15):
    """
    Scarica un URL m3u8 e converte a 1920x1080 MP4.
    Prende i primi max_duration secondi.
    """
    cmd = [
        'ffmpeg', '-y',
        '-i', m3u8_url,
        '-t', str(max_duration),  # Max duration
        '-vf', 'scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2:black',
        '-c:v', 'libx264',
        '-preset', 'fast',
        '-crf', '23',
        '-c:a', 'aac',
        '-b:a', '128k',
        '-movflags', '+faststart',
        output_path
    ]
    
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=120)
        if result.returncode == 0 and os.path.exists(output_path):
            size_mb = os.path.getsize(output_path) / (1024 * 1024)
            return True, f"{size_mb:.1f}MB"
        else:
            return False, result.stderr[-200:] if result.stderr else "Unknown error"
    except subprocess.TimeoutExpired:
        return False, "Timeout (120s)"
    except Exception as e:
        return False, str(e)

def upload_to_drive(drive, file_path, folder_id, file_name):
    """Carica un file su Google Drive"""
    media = MediaFileUpload(file_path, mimetype='video/mp4', resumable=True)
    file_metadata = {
        'name': file_name,
        'parents': [folder_id]
    }
    
    file = drive.files().create(
        body=file_metadata,
        media_body=media,
        fields='id, webViewLink'
    ).execute()
    
    return file.get('id'), file.get('webViewLink')

def main():
    print("=" * 60)
    print("🎬 Artlist → Stock Pipeline")
    print("=" * 60)
    
    # Google auth
    creds = get_google_creds()
    drive = build('drive', 'v3', credentials=creds)
    
    # Crea cartella Stock/Artlist
    print("\n📁 Creazione cartella Artlist...")
    artlist_folder_id = get_or_create_folder(drive, STOCK_FOLDER_ID, "Artlist")
    
    # Leggi clip dal DB
    conn = sqlite3.connect(ARTLIST_DB)
    cur = conn.cursor()
    
    # Prendi 5 clip per ogni termine (quelle con URL più corto = più pulito)
    cur.execute("""
        SELECT st.term, vl.url, vl.duration, vl.width, vl.height
        FROM video_links vl
        JOIN search_terms st ON vl.search_term_id = st.id
        WHERE vl.source='artlist' AND vl.url IS NOT NULL AND vl.url LIKE '%_playlist%'
        ORDER BY st.term, LENGTH(vl.url)
    """)
    
    # Raggruppa per termine
    clips_by_term = {}
    for row in cur.fetchall():
        term, url, duration, width, height = row
        if term not in clips_by_term:
            clips_by_term[term] = []
        clips_by_term[term].append({
            'url': url,
            'duration': duration / 1000 if duration else 10,  # ms → s
            'width': width,
            'height': height
        })
    
    conn.close()
    
    print(f"\n📊 Termini disponibili: {len(clips_by_term)}")
    for term, clips in clips_by_term.items():
        print(f"   {term:20s} → {len(clips)} clips")
    
    # Download e upload
    os.makedirs(TEMP_DIR, exist_ok=True)
    
    uploaded = []
    total_attempts = 0
    total_success = 0
    
    for term, clips in clips_by_term.items():
        print(f"\n{'=' * 60}")
        print(f"📁 Termine: {term} ({len(clips)} clips disponibili, scarico {CLIPS_PER_TERM})")
        print(f"{'=' * 60}")
        
        # Crea sottocartella per il termine
        term_folder_id = get_or_create_folder(drive, artlist_folder_id, term.title())
        
        for i, clip in enumerate(clips[:CLIPS_PER_TERM]):
            total_attempts += 1
            
            # Nome file
            clip_name = f"artlist_{term}_{i+1:02d}.mp4"
            temp_path = os.path.join(TEMP_DIR, clip_name)
            
            print(f"\n  [{i+1}/{CLIPS_PER_TERM}] {clip_name}")
            print(f"     URL: {clip['url'][:60]}...")
            print(f"     Duration: {clip['duration']:.0f}s | Resolution: {clip['width']}x{clip['height']}")
            
            # Download + convert
            dur_to_download = min(clip['duration'], 15)  # Max 15s
            print(f"     ⏳ Download ({dur_to_download:.0f}s) → 1920x1080...")
            
            success, msg = download_and_convert(clip['url'], temp_path, dur_to_download)
            
            if success:
                print(f"     ✅ Scaricato ({msg})")
                
                # Upload
                print(f"     ⏳ Upload su Drive...")
                file_id, file_url = upload_to_drive(drive, temp_path, term_folder_id, clip_name)
                print(f"     ✅ Caricato! 🔗 {file_url}")
                
                uploaded.append({
                    'term': term,
                    'name': clip_name,
                    'file_id': file_id,
                    'url': file_url,
                    'folder': f"Stock/Artlist/{term.title()}",
                    'folder_id': term_folder_id
                })
                
                total_success += 1
                
                # Pulisci temp
                os.remove(temp_path)
            else:
                print(f"     ❌ Fallito: {msg}")
            
            # Piccola pausa
            import time
            time.sleep(1)
    
    # Riepilogo
    print(f"\n{'=' * 60}")
    print(f"✅ PIPELINE COMPLETATA!")
    print(f"{'=' * 60}")
    print(f"📊 Tentativi: {total_attempts}")
    print(f"✅ Successi: {total_success}")
    print(f"❌ Falliti: {total_attempts - total_success}")
    print(f"\n📁 Cartella Artlist: https://drive.google.com/drive/folders/{artlist_folder_id}")
    print(f"\n📋 Clip caricate:")
    for u in uploaded:
        print(f"   🎬 {u['name']:30s} → {u['folder']:30s} | {u['url'][:60]}...")
    
    # Salva index
    index_file = "/home/pierone/Pyt/VeloxEditing/refactored/data/artlist_stock_index.json"
    with open(index_file, 'w') as f:
        json.dump({
            'folder_id': artlist_folder_id,
            'clips': uploaded,
            'created_at': datetime.now().isoformat()
        }, f, indent=2)
    print(f"\n💾 Index salvato: {index_file}")

if __name__ == '__main__':
    main()

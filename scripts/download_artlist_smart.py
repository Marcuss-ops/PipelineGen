#!/usr/bin/env python3
"""
Scarica clip da Artlist con tracking intelligente:
- Hash univoco per ogni URL m3u8
- Verifica su Drive se esiste già
- Upload solo clip nuove
- Tracciamento persistente
"""

import os
import re
import json
import sqlite3
import subprocess
import tempfile
import hashlib
from datetime import datetime
from pathlib import Path

from google.oauth2.credentials import Credentials
from googleapiclient.discovery import build
from googleapiclient.http import MediaFileUpload
from google.auth.transport.requests import Request

# Config
ARTLIST_DB = "/home/pierone/Pyt/VeloxEditing/refactored/src/node-scraper/artlist_videos.db"
ARTLIST_LOCAL_DB = "/home/pierone/Pyt/VeloxEditing/refactored/data/artlist_local.db.json"
TOKEN_FILE = "/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/token.json"
STOCK_FOLDER_ID = "1aMjQlK9J1mEyT2TOYDNjeynO1GzZS4_S"
TEMP_DIR = "/tmp/artlist_downloads"
TRACKING_FILE = "/home/pierone/Pyt/VeloxEditing/refactored/data/artlist_tracked.json"

# CLIPS per termine scaricare (nuove)
MAX_CLIPS_PER_TERM = 10


def load_tracking():
    """Carica tracking delle clip già caricate"""
    if os.path.exists(TRACKING_FILE):
        with open(TRACKING_FILE) as f:
            return json.load(f)
    return {
        "uploaded": {},  # hash -> {url, file_id, drive_url, uploaded_at}
        "by_term": {},   # term -> [hash1, hash2, ...]
        "drive_folders": {},  # term -> folder_id
        "stats": {
            "total_uploaded": 0,
            "last_run": None
        }
    }


def save_tracking(data):
    """Salva tracking"""
    os.makedirs(os.path.dirname(TRACKING_FILE), exist_ok=True)
    with open(TRACKING_FILE, 'w') as f:
        json.dump(data, f, indent=2)


def get_clip_hash(url):
    """Hash univoco dell'URL m3u8"""
    return hashlib.sha256(url.encode()).hexdigest()[:12]


def get_google_creds():
    """Credenziali Google"""
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
    """Trova o crea cartella su Drive"""
    # Cerca esistente
    results = drive.files().list(
        q=f"'{parent_id}' in parents and mimeType='application/vnd.google-apps.folder' and name='{folder_name}' and trashed=false",
        fields="files(id, name)"
    ).execute()

    folders = results.get('files', [])
    if folders:
        return folders[0]['id']

    # Crea nuova
    folder_metadata = {
        'name': folder_name,
        'mimeType': 'application/vnd.google-apps.folder',
        'parents': [parent_id]
    }
    folder = drive.files().create(body=folder_metadata, fields='id').execute()
    print(f"  📁 Creata cartella: {folder_name}")
    return folder['id']


def check_file_exists_on_drive(drive, folder_id, filename):
    """Controlla se file esiste già nella cartella"""
    results = drive.files().list(
        q=f"'{folder_id}' in parents and name='{filename}' and trashed=false",
        fields="files(id, name, createdTime)"
    ).execute()
    files = results.get('files', [])
    return files[0] if files else None


def download_and_convert(m3u8_url, output_path, max_duration=15):
    """Scarica m3u8 e converte a 1920x1080 MP4"""
    cmd = [
        'ffmpeg', '-y', '-hide_banner', '-loglevel', 'error',
        '-i', m3u8_url,
        '-t', str(max_duration),
        '-vf', 'scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2:black',
        '-c:v', 'libx264', '-preset', 'fast', '-crf', '23',
        '-c:a', 'aac', '-b:a', '128k',
        '-movflags', '+faststart',
        output_path
    ]

    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=120)
        if result.returncode == 0 and os.path.exists(output_path):
            size_mb = os.path.getsize(output_path) / (1024 * 1024)
            return True, f"{size_mb:.1f}MB"
        return False, result.stderr[-200:] if result.stderr else "Unknown error"
    except subprocess.TimeoutExpired:
        return False, "Timeout"
    except Exception as e:
        return False, str(e)


def upload_to_drive(drive, file_path, folder_id, file_name):
    """Carica file su Drive"""
    media = MediaFileUpload(file_path, mimetype='video/mp4', resumable=True)
    file_metadata = {'name': file_name, 'parents': [folder_id]}

    file = drive.files().create(
        body=file_metadata,
        media_body=media,
        fields='id, webViewLink, md5Checksum, size'
    ).execute()

    return {
        'id': file.get('id'),
        'url': file.get('webViewLink'),
        'md5': file.get('md5Checksum'),
        'size': file.get('size')
    }


def main():
    print("=" * 70)
    print("🎬 Artlist → Stock Pipeline (Smart Tracking)")
    print("=" * 70)

    # Carica tracking
    tracking = load_tracking()
    print(f"\n📦 Clip già caricate: {len(tracking['uploaded'])}")

    # Auth Google
    creds = get_google_creds()
    drive = build('drive', 'v3', credentials=creds)

    # Crea/ottieni cartella Artlist
    artlist_folder_id = get_or_create_folder(drive, STOCK_FOLDER_ID, "Artlist")

    # Leggi clip dal DB SQLite
    conn = sqlite3.connect(ARTLIST_DB)
    cur = conn.cursor()

    cur.execute("""
        SELECT st.term, vl.url, vl.duration, vl.width, vl.height
        FROM video_links vl
        JOIN search_terms st ON vl.search_term_id = st.id
        WHERE vl.source='artlist' AND vl.url IS NOT NULL AND vl.url LIKE '%_playlist%'
        ORDER BY st.term, vl.id
    """)

    # Raggruppa per termine
    clips_by_term = {}
    for row in cur.fetchall():
        term, url, duration, width, height = row
        clip_hash = get_clip_hash(url)
        if term not in clips_by_term:
            clips_by_term[term] = []
        clips_by_term[term].append({
            'hash': clip_hash,
            'url': url,
            'duration': duration / 1000 if duration else 10,
            'width': width,
            'height': height
        })

    conn.close()

    print(f"\n📊 Termini nel DB: {len(clips_by_term)}")
    for term, clips in clips_by_term.items():
        already = tracking['by_term'].get(term, [])
        print(f"   {term:15s}: {len(clips):3d} totali | {len(already):3d} già su Drive")

    # Setup temp dir
    os.makedirs(TEMP_DIR, exist_ok=True)

    # Risultati
    new_uploads = []
    skipped = []
    errors = []

    # Processa ogni termine
    for term, clips in clips_by_term.items():
        print(f"\n{'=' * 70}")
        print(f"📁 Termine: {term}")
        print(f"{'=' * 70}")

        # Crea/ottieni cartella per il termine
        term_folder_id = get_or_create_folder(drive, artlist_folder_id, term.title())
        tracking['drive_folders'][term] = term_folder_id

        # Filtra clip già caricate (per hash)
        already_uploaded = tracking['by_term'].get(term, [])
        new_clips = [c for c in clips if c['hash'] not in tracking['uploaded']]

        print(f"   {len(new_clips)} clip nuove da processare (max {MAX_CLIPS_PER_TERM})")

        # Processa max MAX_CLIPS_PER_TERM clip nuove
        for i, clip in enumerate(new_clips[:MAX_CLIPS_PER_TERM]):
            clip_hash = clip['hash']
            filename = f"artlist_{term}_{clip_hash}.mp4"

            print(f"\n  [{i+1}/{min(len(new_clips), MAX_CLIPS_PER_TERM)}] {filename}")
            print(f"     Hash: {clip_hash}")
            print(f"     URL: {clip['url'][:50]}...")

            # Verifica su Drive (per sicurezza)
            existing = check_file_exists_on_drive(drive, term_folder_id, filename)
            if existing:
                print(f"     ⚠️  Già esiste su Drive: {existing['id']}")
                tracking['uploaded'][clip_hash] = {
                    'url': clip['url'],
                    'file_id': existing['id'],
                    'drive_url': f"https://drive.google.com/file/d/{existing['id']}/view",
                    'uploaded_at': existing.get('createdTime', datetime.now().isoformat())
                }
                if term not in tracking['by_term']:
                    tracking['by_term'][term] = []
                tracking['by_term'][term].append(clip_hash)
                skipped.append({'term': term, 'hash': clip_hash, 'file_id': existing['id']})
                save_tracking(tracking)
                continue

            # Download
            temp_path = os.path.join(TEMP_DIR, filename)
            dur = min(clip['duration'], 15)
            print(f"     ⏳ Download {dur:.0f}s...", end='', flush=True)

            success, msg = download_and_convert(clip['url'], temp_path, dur)

            if not success:
                print(f" ❌ {msg}")
                errors.append({'term': term, 'hash': clip_hash, 'error': msg})
                continue

            print(f" ✅ {msg}")

            # Upload
            print(f"     ⏳ Upload Drive...", end='', flush=True)
            try:
                result = upload_to_drive(drive, temp_path, term_folder_id, filename)
                print(f" ✅ {result['id'][:20]}...")

                # Salva nel tracking
                tracking['uploaded'][clip_hash] = {
                    'url': clip['url'],
                    'file_id': result['id'],
                    'drive_url': result['url'],
                    'md5': result.get('md5'),
                    'size': result.get('size'),
                    'uploaded_at': datetime.now().isoformat()
                }
                if term not in tracking['by_term']:
                    tracking['by_term'][term] = []
                tracking['by_term'][term].append(clip_hash)

                new_uploads.append({
                    'term': term,
                    'hash': clip_hash,
                    'file_id': result['id'],
                    'url': result['url']
                })

                # Salva subito (se va in crash non perdiamo i progressi)
                save_tracking(tracking)

                # Aggiorna anche artlist_local.db.json per il server Go
                update_local_db(term, clip, result)

                # Pulisci temp
                os.remove(temp_path)

            except Exception as e:
                print(f" ❌ {e}")
                errors.append({'term': term, 'hash': clip_hash, 'error': str(e)})

    # Riepilogo finale
    print(f"\n{'=' * 70}")
    print("✅ PIPELINE COMPLETATA!")
    print(f"{'=' * 70}")
    print(f"\n📈 Risultati:")
    print(f"   ✅ Nuove clip caricate: {len(new_uploads)}")
    print(f"   ⏭️  Saltate (già su Drive): {len(skipped)}")
    print(f"   ❌ Errori: {len(errors)}")
    print(f"   📦 Totale clip tracciate: {len(tracking['uploaded'])}")

    if new_uploads:
        print(f"\n📋 Nuove clip:")
        for u in new_uploads:
            print(f"   🎬 {u['hash'][:8]}... | {u['term']:12s} | {u['url'][:50]}...")

    if errors:
        print(f"\n❌ Errori:")
        for e in errors:
            print(f"   {e['term']}: {e['error'][:50]}")

    # Aggiorna stats
    tracking['stats']['total_uploaded'] = len(tracking['uploaded'])
    tracking['stats']['last_run'] = datetime.now().isoformat()
    save_tracking(tracking)

    print(f"\n💾 Tracking salvato: {TRACKING_FILE}")

    # Genera index per il Go backend
    generate_backend_index(tracking)


def generate_backend_index(tracking):
    """Genera index compatibile con il backend Go"""
    index = {
        'folder_id': tracking['drive_folders'].get('spider', ''),  # Root Artlist
        'clips': [],
        'generated_at': datetime.now().isoformat(),
        'total_clips': len(tracking['uploaded']),
        'by_term': {}
    }

    for term, hashes in tracking['by_term'].items():
        index['by_term'][term] = []
        for h in hashes:
            clip = tracking['uploaded'].get(h)
            if clip:
                clip_entry = {
                    'term': term,
                    'hash': h,
                    'file_id': clip['file_id'],
                    'url': clip['drive_url'],
                    'folder': f"Stock/Artlist/{term.title()}",
                    'folder_id': tracking['drive_folders'].get(term, '')
                }
                index['clips'].append(clip_entry)
                index['by_term'][term].append(clip_entry)

    index_file = "/home/pierone/Pyt/VeloxEditing/refactored/data/artlist_stock_index.json"
    with open(index_file, 'w') as f:
        json.dump(index, f, indent=2)

    print(f"📄 Index backend salvato: {index_file}")


def update_local_db(term, clip_info, drive_result):
    """Aggiorna artlist_local.db.json con le clip scaricate"""
    if not os.path.exists(ARTLIST_LOCAL_DB):
        data = {"searches": {}}
    else:
        with open(ARTLIST_LOCAL_DB, 'r') as f:
            data = json.load(f)

    if "searches" not in data:
        data["searches"] = {}

    if term not in data["searches"]:
        data["searches"][term] = {"term": term, "clips": []}

    clip_entry = {
        "id": f"artlist_{clip_info['hash']}",
        "video_id": f"artlist_{clip_info['hash']}.mp4",
        "title": term,
        "original_url": clip_info.get('url', ''),
        "url": clip_info.get('url', ''),
        "drive_file_id": drive_result['id'],
        "drive_url": drive_result['url'],
        "local_path_drive": f"Stock/Artlist/{term}/artlist_{clip_info['hash']}.mp4",
        "download_path": "",
        "duration": clip_info.get('duration', 10000),
        "width": clip_info.get('width', 1920),
        "height": clip_info.get('height', 1080),
        "category": "Artlist/Artlist",
        "tags": [term, "artlist", "downloaded"],
        "downloaded": True,
        "added_at": datetime.now().isoformat() + "Z",
        "downloaded_at": datetime.now().isoformat() + "Z"
    }

    data["searches"][term]["clips"].append(clip_entry)

    with open(ARTLIST_LOCAL_DB, 'w') as f:
        json.dump(data, f, indent=2)


if __name__ == '__main__':
    main()

#!/usr/bin/env python3
import sqlite3
import json
import os
from pathlib import Path

# Configurazione percorsi
DATA_DIR = Path("data")
MEDIA_DB_PATH = DATA_DIR / "media" / "media.db.sqlite"

OLD_DBS = {
    "youtube": DATA_DIR / "clips" / "clips.db.sqlite",
    "artlist": DATA_DIR / "artlist" / "artlist.db.sqlite",
    "stock": DATA_DIR / "stock" / "stock.db.sqlite"
}

IMAGES_DB = DATA_DIR / "images" / "images.db.sqlite"
VOICEOVERS_DB = DATA_DIR / "voiceover" / "voiceover.db.sqlite"

def init_media_db():
    os.makedirs(MEDIA_DB_PATH.parent, exist_ok=True)
    conn = sqlite3.connect(MEDIA_DB_PATH)
    
    # Crea lo schema leggendo il file .sql
    schema_path = Path("migrations") / "media" / "001_initial_media.sql"
    with open(schema_path, "r") as f:
        conn.executescript(f.read())
        
    return conn

def migrate_clips(media_conn):
    for source, db_path in OLD_DBS.items():
        if not db_path.exists():
            print(f"Skipping {source}, file not found: {db_path}")
            continue
            
        print(f"Migrating {source} from {db_path}...")
        old_conn = sqlite3.connect(db_path)
        old_conn.row_factory = sqlite3.Row
        
        try:
            cursor = old_conn.cursor()
            # Seleziona le colonne se esistono
            cursor.execute("PRAGMA table_info(clips)")
            cols = [c["name"] for c in cursor.fetchall()]
            
            if not cols:
                print(f"No 'clips' table in {source}, skipping.")
                continue
                
            has_embedding = "embedding_json" in cols
            
            cursor.execute("SELECT * FROM clips")
            rows = cursor.fetchall()
            
            inserted = 0
            for row in rows:
                row_dict = dict(row)
                
                # Prepara i campi base
                asset_id = row_dict.get("id")
                name = row_dict.get("name") or row_dict.get("filename") or "Unknown"
                tags = row_dict.get("tags") or "[]"
                duration = row_dict.get("duration", 0)
                url = row_dict.get("drive_link") or row_dict.get("external_url") or ""
                created_at = row_dict.get("created_at")
                
                embedding_json = row_dict.get("embedding_json") if has_embedding else None
                
                # Raccogli tutti gli altri campi nel metadata
                metadata = {}
                for k, v in row_dict.items():
                    if k not in ["id", "name", "tags", "duration", "embedding_json", "created_at"] and v is not None:
                        metadata[k] = v
                        
                # Scrivi nel nuovo DB
                media_conn.execute("""
                    INSERT OR IGNORE INTO media_assets 
                    (id, source, name, tags, tags_norm, embedding_json, duration_ms, url, created_at, metadata_json)
                    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """, (
                    asset_id,
                    source,
                    name,
                    tags,
                    tags.lower() if tags else None,
                    embedding_json,
                    int(duration * 1000) if duration else 0, # Converti in ms
                    url,
                    created_at,
                    json.dumps(metadata)
                ))
                inserted += 1
                
            media_conn.commit()
            print(f"Migrated {inserted} records from {source}")
            
        except Exception as e:
            print(f"Error migrating {source}: {e}")
        finally:
            old_conn.close()

def migrate_images(media_conn):
    if not IMAGES_DB.exists():
        print(f"Skipping images, file not found: {IMAGES_DB}")
        return
        
    print(f"Migrating images from {IMAGES_DB}...")
    old_conn = sqlite3.connect(IMAGES_DB)
    old_conn.row_factory = sqlite3.Row
    
    try:
        cursor = old_conn.cursor()
        cursor.execute("SELECT * FROM images")
        rows = cursor.fetchall()
        
        inserted = 0
        for row in rows:
            row_dict = dict(row)
            
            asset_id = row_dict.get("hash")
            name = row_dict.get("subject_id")
            tags = row_dict.get("tags_json") or "[]"
            url = row_dict.get("source_url") or ""
            created_at = row_dict.get("created_at")
            
            metadata = {}
            for k, v in row_dict.items():
                if k not in ["hash", "subject_id", "tags_json", "source_url", "created_at"] and v is not None:
                    metadata[k] = v
                    
            media_conn.execute("""
                INSERT OR IGNORE INTO media_assets 
                (id, source, name, tags, tags_norm, embedding_json, duration_ms, url, created_at, metadata_json)
                VALUES (?, 'image', ?, ?, ?, NULL, 0, ?, ?, ?)
            """, (
                asset_id,
                name,
                tags,
                tags.lower() if tags else None,
                url,
                created_at,
                json.dumps(metadata)
            ))
            inserted += 1
            
        media_conn.commit()
        print(f"Migrated {inserted} images")
        
    except Exception as e:
        print(f"Error migrating images: {e}")
    finally:
        old_conn.close()

def migrate_subjects(media_conn):
    if not IMAGES_DB.exists():
        return
        
    print(f"Migrating subjects from {IMAGES_DB}...")
    old_conn = sqlite3.connect(IMAGES_DB)
    old_conn.row_factory = sqlite3.Row
    
    try:
        cursor = old_conn.cursor()
        cursor.execute("SELECT * FROM subjects")
        rows = cursor.fetchall()
        
        inserted = 0
        for row in rows:
            row_dict = dict(row)
            media_conn.execute("""
                INSERT OR IGNORE INTO subjects (id, name, description, metadata_json, created_at, updated_at)
                VALUES (?, ?, ?, ?, ?, ?)
            """, (
                row_dict.get("id"),
                row_dict.get("name"),
                row_dict.get("description"),
                row_dict.get("metadata_json") or "{}",
                row_dict.get("created_at"),
                row_dict.get("updated_at")
            ))
            inserted += 1
        media_conn.commit()
        print(f"Migrated {inserted} subjects")
    finally:
        old_conn.close()

def migrate_voiceovers(media_conn):
    if not VOICEOVERS_DB.exists():
        print(f"Skipping voiceovers, file not found: {VOICEOVERS_DB}")
        return
        
    print(f"Migrating voiceovers from {VOICEOVERS_DB}...")
    old_conn = sqlite3.connect(VOICEOVERS_DB)
    old_conn.row_factory = sqlite3.Row
    
    try:
        cursor = old_conn.cursor()
        cursor.execute("SELECT * FROM voiceovers")
        rows = cursor.fetchall()
        
        inserted = 0
        for row in rows:
            row_dict = dict(row)
            
            asset_id = row_dict.get("id")
            name = row_dict.get("text_preview") or "Voiceover"
            tags = "[]"
            url = row_dict.get("drive_link") or ""
            created_at = row_dict.get("created_at")
            duration_s = row_dict.get("duration_seconds", 0)
            
            metadata = {}
            for k, v in row_dict.items():
                if k not in ["id", "text_preview", "drive_link", "created_at", "duration_seconds"] and v is not None:
                    metadata[k] = v
                    
            media_conn.execute("""
                INSERT OR IGNORE INTO media_assets 
                (id, source, name, tags, tags_norm, embedding_json, duration_ms, url, created_at, metadata_json)
                VALUES (?, 'voiceover', ?, ?, ?, NULL, ?, ?, ?, ?)
            """, (
                asset_id,
                name,
                tags,
                tags.lower() if tags else None,
                int(duration_s * 1000) if duration_s else 0,
                url,
                created_at,
                json.dumps(metadata)
            ))
            inserted += 1
            
        media_conn.commit()
        print(f"Migrated {inserted} voiceovers")
        
    except Exception as e:
        print(f"Error migrating voiceovers: {e}")
    finally:
        old_conn.close()

if __name__ == "__main__":
    print("Starting database migration to media.db.sqlite...")
    media_conn = init_media_db()
    
    migrate_clips(media_conn)
    migrate_images(media_conn)
    migrate_subjects(media_conn)
    migrate_voiceovers(media_conn)
    
    media_conn.close()
    print("Migration complete! You can now use data/media/media.db.sqlite")

import sqlite3
import json
from pathlib import Path

db_path = Path("data/media/media.db.sqlite")
print(f"Checking DB: {db_path.absolute()}")
if not db_path.exists():
    print("DB does not exist!")
    exit(1)

conn = sqlite3.connect(db_path)
conn.row_factory = sqlite3.Row
cursor = conn.cursor()

# Get counts by source
cursor.execute("""
    SELECT 
        source,
        COUNT(*) as total,
        SUM(CASE WHEN embedding_json IS NULL OR embedding_json = '[]' OR embedding_json = '' THEN 1 ELSE 0 END) as senza_embedding
    FROM media_assets
    GROUP BY source
""")
rows = cursor.fetchall()
print("\nSources in media_assets:")
for row in rows:
    print(f"  Source: {row['source']}, Total: {row['total']}, Sans Embedding: {row['senza_embedding']}")

# Get sample image metadata
cursor.execute("SELECT id, source, name, tags, embedding_json, metadata_json FROM media_assets WHERE source='image' LIMIT 3")
images = cursor.fetchall()
print("\nSample Images:")
for img in images:
    print(f"  ID: {img['id']}")
    print(f"  Name: {img['name']}")
    print(f"  Tags: {img['tags']}")
    print(f"  Has Embedding: {img['embedding_json'] is not None}")
    print(f"  Metadata: {img['metadata_json']}")
    print("-" * 40)

conn.close()

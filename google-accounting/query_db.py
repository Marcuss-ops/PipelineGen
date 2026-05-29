import sqlite3
import json

db_path = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/data/velox/velox.db.sqlite"
conn = sqlite3.connect(db_path)
cursor = conn.cursor()

try:
    cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = cursor.fetchall()
    print("TABLES:")
    for t in tables:
        print(f" - {t[0]}")
        # If it looks like images, query columns
        if "image" in t[0] or "asset" in t[0]:
            cursor.execute(f"PRAGMA table_info({t[0]})")
            cols = [c[1] for c in cursor.fetchall()]
            print(f"   Columns: {cols}")
            try:
                cursor.execute(f"SELECT * FROM {t[0]} ORDER BY ROWID DESC LIMIT 2")
                print(f"   Sample rows: {cursor.fetchall()}")
            except Exception as row_err:
                print(f"   Failed to get sample rows: {row_err}")
except Exception as e:
    print("Error listing tables:", e)

conn.close()

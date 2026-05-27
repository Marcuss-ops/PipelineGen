import sqlite3
from pathlib import Path

for db_name in ["data/velox/velox.db.sqlite", "data/media/media.db.sqlite"]:
    db_path = Path(db_name)
    if not db_path.exists():
        print(f"{db_name} does not exist")
        continue
    
    print(f"\nTables in {db_name}:")
    conn = sqlite3.connect(db_path)
    cursor = conn.cursor()
    cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = [row[0] for row in cursor.fetchall()]
    for t in tables:
        try:
            cursor.execute(f"SELECT COUNT(*) FROM {t}")
            cnt = cursor.fetchone()[0]
            print(f"  - {t}: {cnt} rows")
        except Exception as e:
            print(f"  - {t}: error counting rows ({e})")
    conn.close()

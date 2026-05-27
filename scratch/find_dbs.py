import os
from pathlib import Path

print("Searching for database files in workspace:")
for root, dirs, files in os.walk("."):
    # skip some heavy folders
    if ".git" in root or "node_modules" in root or "web-admin" in root:
        continue
    for file in files:
        if file.endswith((".db", ".sqlite", ".sqlite3")):
            p = Path(root) / file
            print(f"  - {p} (Size: {p.stat().st_size} bytes)")

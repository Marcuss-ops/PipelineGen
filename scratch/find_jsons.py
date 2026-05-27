import os
from pathlib import Path

print("Searching for json files in data folder:")
for root, dirs, files in os.walk("data"):
    for file in files:
        if file.endswith(".json"):
            p = Path(root) / file
            print(f"  - {p} (Size: {p.stat().st_size} bytes)")

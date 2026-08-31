#!/usr/bin/env python3
"""
Top-level entrypoint for Google OAuth token generation.
Delegates directly to scripts/tools/generate_drive_token.py.
"""
import sys
from pathlib import Path

TOOLS_DIR = Path(__file__).resolve().parent / "tools"
sys.path.insert(0, str(TOOLS_DIR))

if __name__ == "__main__":
    import generate_drive_token
    generate_drive_token.main()

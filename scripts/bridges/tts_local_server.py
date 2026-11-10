#!/usr/bin/env python3
"""tts_local_server.py — thin entry point for the local TTS sidecar.

The implementation lives in the `local_tts_bridge` package so the
persistent server and the bench harness share the exact same engine
adapters. Mirrors `tts_edge_server.py`.
"""

import sys

from local_tts_bridge.server import main

if __name__ == "__main__":
    sys.exit(main())

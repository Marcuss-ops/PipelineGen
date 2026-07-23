#!/usr/bin/env python3
"""tts_edge_server.py — thin entry-point for the persistent Edge TTS sidecar.

The actual implementation lives in the `edge_tts` package so that the
server, request schema, and voice resolver can be shared with the
legacy CLI (tts_edge.py) without duplicating code.
"""

import sys

from edge_tts.server import main

if __name__ == "__main__":
    sys.exit(main())

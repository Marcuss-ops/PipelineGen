#!/usr/bin/env python3
"""argos_server.py — thin entry-point for the persistent Argos Translate sidecar.

The actual implementation lives in the `argos_bridge` package so the
server and the one-shot CLI (argos_translator.py) share the same
translation core without duplicating code.
"""

import sys

from argos_bridge.server import main

if __name__ == "__main__":
    sys.exit(main())

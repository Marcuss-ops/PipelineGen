#!/usr/bin/env bash
# Quota-expensive Artlist/VidRush scale battery.
#
# Default: 20 keywords x 10 clips, followed by Drive, VLM, Qdrant and replay
# dedup validation. Kept outside verify-live because it may consume up to 200
# authorized Artlist downloads on the first pass.

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$SCRIPT_DIR/artlist_scale_e2e.py" "$@"

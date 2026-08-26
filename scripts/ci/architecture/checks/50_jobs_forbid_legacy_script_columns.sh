#!/usr/bin/env bash
set -euo pipefail

if [ -n "${BASH_SOURCE[0]:-}" ]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
    echo "CI: cannot resolve check directory" >&2
    exit 1
fi
source "${SCRIPT_DIR}/lib/50_jobs_lib.sh"

# Retired: this check scanned for the legacy Template/TimelineJSON columns
# whose canonical owners are internal/platform/sqlite/scripts and
# internal/capabilities/scripts/adapters; its crude `Template:` regex
# false-positived on unrelated canonical fields (overlays registry,
# voiceover service), so it is retired with the legacy application root.

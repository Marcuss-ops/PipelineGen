#!/usr/bin/env bash
# certify-data-layer.sh — public four-plane data-layer certification.
#
# The storage certificate remains the single owner of the detailed rules.
# This wrapper gives CI and operators one stable data-layer contract and
# refuses to translate a partial/failed storage certificate into success.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
CERT_TIMEOUT="${DATA_LAYER_CERT_TIMEOUT:-120}"

JSON=false
for arg in "$@"; do
  case "$arg" in
    --json) JSON=true ;;
    --qdrant-url|--qdrant-url=*)
      # Forwarded unchanged to the canonical storage certificate.
      ;;
  esac
done

if $JSON; then
  storage_json="$(timeout "${CERT_TIMEOUT}s" bash scripts/ci/certify-storage.sh --json "$@" 2>/dev/null || true)"
  python3 - "$storage_json" <<'PY'
import json
import sys

raw = sys.argv[1]
try:
    storage = json.loads(raw)
except json.JSONDecodeError:
    storage = {"FINAL_CERTIFIED": False, "error": "storage certificate did not return JSON"}

certified = bool(storage.get("FINAL_CERTIFIED", False))
print(json.dumps({
    "FINAL_DATA_LAYER_CERTIFIED": certified,
    "storage_certificate": storage,
}, indent=2))
raise SystemExit(0 if certified else 1)
PY
fi

set +e
timeout "${CERT_TIMEOUT}s" bash scripts/ci/certify-storage.sh "$@"
rc=$?
set -e
echo
if [[ "$rc" -eq 0 ]]; then
  echo "FINAL_DATA_LAYER_CERTIFIED=true"
else
  echo "FINAL_DATA_LAYER_CERTIFIED=false"
fi
exit "$rc"

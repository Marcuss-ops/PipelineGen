#!/usr/bin/env bash
# resolve-pre-payload.sh — resolve a PREPARE selection manifest against the media SSOT.
#
# A SELECTION manifest (ops/jobs/remote/dolly5-pre.creator-77.json) names a
# scene's clip by `asset_id` plus the `duration_seconds` window to use. The
# remote master's PREPARE decoder is strict: it accepts
# `scenes[].clip{asset_id,drive_file_id,url,sha256,size_bytes,duration_ms}` and
# rejects a bare top-level `asset_id` (and any documentation key, e.g.
# `_comment`). This script turns the selection form into that resolved form.
#
# The drive_file_id / content_sha256 / duration_ms come from the media SSOT
# (`media_assets`) — never from the selection file, which records a CHOICE, not
# a fact (README §5). Fail-closed: a scene with no asset, or an asset with no
# SSOT row, no Drive id, or no content hash, stops the resolution instead of
# producing a payload the worker would have to trust blindly.
#
# Usage:
#   ops/jobs/remote/resolve-pre-payload.sh SELECTION.json            # → stdout
#   ops/jobs/remote/resolve-pre-payload.sh SELECTION.json OUT.json   # → file
#
# The envelope (job_type, copy_only, video_name, script_text, output,
# delivery_plan) is passed through untouched; an `idempotency_key` is left to
# the caller (run-flow.sh stamps it per run).
#
# Exit codes: 0 ok · 1 usage/tooling/DB · 2 unresolved asset
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
ENV_FILE="${VELOX_ENV_FILE:-$REPO_ROOT/.env}"
PG_CONTAINER="${VELOX_PG_CONTAINER:-pipelinegen-postgres-test}"

usage() { sed -n '2,22p' "${BASH_SOURCE[0]}"; }

[[ $# -ge 1 && $# -le 2 ]] || { usage >&2; exit 1; }
SELECTION="$1"
OUT="${2:-}"

[[ -r "$SELECTION" ]] || { echo "resolve-pre-payload: unreadable selection manifest: $SELECTION" >&2; exit 1; }
for t in jq docker; do command -v "$t" >/dev/null 2>&1 || { echo "resolve-pre-payload: required tool missing: $t" >&2; exit 1; }; done
[[ -f "$ENV_FILE" ]] || { echo "resolve-pre-payload: FAIL — $ENV_FILE not found" >&2; exit 1; }

# Every asset a scene references, from the selection form (top-level asset_id)
# or an already-resolved block (clip/stock). Reading both makes the script
# idempotent: re-running it over its own output only refreshes the SSOT facts.
IDS="$(jq -r '[.scenes[] | (.asset_id // .clip.asset_id // .stock.asset_id // "") | select(. != "")] | unique | .[]' "$SELECTION")"
NO_ID="$(jq -r '[.scenes[] | select((.asset_id // .clip.asset_id // .stock.asset_id // "") == "")] | length' "$SELECTION")"
if [[ "$NO_ID" != "0" ]]; then
  echo "resolve-pre-payload: FAIL — $NO_ID scene(s) name no asset_id (nothing to resolve)" >&2
  exit 2
fi
if [[ -z "$IDS" ]]; then
  echo "resolve-pre-payload: FAIL — the manifest references no asset" >&2
  exit 2
fi

# Ids go straight into a SQL IN list: reject anything that is not the canonical
# asset-id alphabet instead of quoting arbitrary input.
while IFS= read -r id; do
  [[ "$id" =~ ^[A-Za-z0-9:._-]{1,200}$ ]] || { echo "resolve-pre-payload: invalid asset id '$id'" >&2; exit 2; }
done <<<"$IDS"

DSN_RAW="$(rg -N '^PIPELINEGEN_MEDIA_POSTGRES_DSN=' "$ENV_FILE" | cut -d= -f2- || true)"
if [[ -z "$DSN_RAW" ]]; then
  echo "resolve-pre-payload: FAIL — PIPELINEGEN_MEDIA_POSTGRES_DSN not set in $ENV_FILE" >&2
  exit 1
fi
DSN_IN="$(printf '%s' "$DSN_RAW" | sed -E 's#@[^/]+/#@127.0.0.1:5432/#')"

VALUES="$(printf "'%s'," $IDS | sed 's/,$//')"
QUERY="SELECT id || E'\\t' || COALESCE(drive_file_id,'') || E'\\t' || COALESCE(content_sha256,'') || E'\\t' || COALESCE(duration_ms,0)::text FROM media_assets WHERE id IN ($VALUES);"
ROWS="$(docker exec "$PG_CONTAINER" psql -qtA "$DSN_IN" -c "$QUERY")"

MAP="$(printf '%s\n' "$ROWS" | jq -Rn '
  [inputs | split("\t") | select(length >= 4)
   | {key: .[0], value: {drive_file_id: .[1], content_sha256: .[2], duration_ms: (.[3] | tonumber? // 0)}}]
  | from_entries')"

# Fail closed on any asset the SSOT cannot identify completely.
MISSING="$(jq -r --argjson m "$MAP" '
  [.scenes[]
   | (.asset_id // .clip.asset_id // .stock.asset_id // "") as $id
   | select($id != "")
   | select(($m[$id] // null) == null or ($m[$id].drive_file_id // "") == "" or ($m[$id].content_sha256 // "") == "")
   | $id] | unique | .[]' "$SELECTION")"
if [[ -n "$MISSING" ]]; then
  echo "resolve-pre-payload: FAIL — no media SSOT row with a Drive id + content hash for:" >&2
  printf '  %s\n' $MISSING >&2
  exit 2
fi

RESOLVED="$(jq --argjson m "$MAP" '
  del(._comment)
  | .scenes |= [to_entries[] | .key as $i | .value as $s
      | ($s.asset_id // $s.clip.asset_id // $s.stock.asset_id) as $id
      | ($s | del(.asset_id))
        + {index: ($s.index // $i),
           kind: ($s.kind // "clip"),
           duration_seconds: ($s.duration_seconds // 0),
           clip: {asset_id: $id,
                  drive_file_id: $m[$id].drive_file_id,
                  url: ("velox-drive://" + $m[$id].drive_file_id),
                  sha256: $m[$id].content_sha256,
                  size_bytes: 0,
                  duration_ms: $m[$id].duration_ms}}]' "$SELECTION")"

if [[ -n "$OUT" ]]; then
  printf '%s\n' "$RESOLVED" >"$OUT"
  echo "resolve-pre-payload: wrote $OUT ($(jq -r '.scenes | length' "$OUT") scene(s) resolved from the media SSOT)" >&2
else
  printf '%s\n' "$RESOLVED"
fi

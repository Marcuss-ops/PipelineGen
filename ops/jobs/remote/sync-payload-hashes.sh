#!/usr/bin/env bash
# sync-payload-hashes.sh — put the real content hashes into the job payloads.
#
# The kit payloads used to ship `sha256: ""` for every asset, which removes any
# integrity check on the worker side. The hash is not something to compute by
# downloading the files: the media SSOT already stores it in `content_sha256`
# (populated for 100% of rows, matching the Drive `sha256Checksum` of the same
# file). This script maps every Drive id referenced by a payload to that value.
#
# It only ever READS the database; it writes the payload JSON files, and only
# with --write.
#
# Usage:
#   ops/jobs/remote/sync-payload-hashes.sh            # report (exit 1 if stale)
#   ops/jobs/remote/sync-payload-hashes.sh --write    # patch the payloads
#
# The DSN is read from the repository .env (PIPELINEGEN_MEDIA_POSTGRES_DSN) and
# rewritten to the in-container port; it is never echoed.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
ENV_FILE="${VELOX_ENV_FILE:-$REPO_ROOT/.env}"
PG_CONTAINER="${VELOX_PG_CONTAINER:-pipelinegen-postgres-test}"
WRITE="0"
PAYLOADS=("$SCRIPT_DIR/pre-job.creator-77.json" "$SCRIPT_DIR/finalize-job.creator-77.json")

while [[ $# -gt 0 ]]; do
  case "$1" in
    --write) WRITE="1"; shift ;;
    -h|--help) sed -n '2,18p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "sync-payload-hashes: unknown argument $1" >&2; exit 1 ;;
  esac
done

if [[ ! -f "$ENV_FILE" ]]; then
  echo "sync-payload-hashes: FAIL — $ENV_FILE not found" >&2
  exit 1
fi
DSN_RAW="$(rg -N '^PIPELINEGEN_MEDIA_POSTGRES_DSN=' "$ENV_FILE" | cut -d= -f2- || true)"
if [[ -z "$DSN_RAW" ]]; then
  echo "sync-payload-hashes: FAIL — PIPELINEGEN_MEDIA_POSTGRES_DSN not set in $ENV_FILE" >&2
  exit 1
fi
DSN_IN="$(printf '%s' "$DSN_RAW" | sed -E 's#@[^/]+/#@127.0.0.1:5432/#')"

# Every Drive id the payloads reference, either explicitly (drive_file_id) or
# through the canonical locator (url: velox-drive://<id>).
DRIVE_IDS="$(rg -oN --no-filename -e 'velox-drive://[A-Za-z0-9_-]+' -e '"drive_file_id": *"[A-Za-z0-9_-]+"' "${PAYLOADS[@]}" \
  | sed -E 's#^velox-drive://##; s#.*"drive_file_id": *"##; s#"$##' | sort -u)"
if [[ -z "$DRIVE_IDS" ]]; then
  echo "sync-payload-hashes: no Drive ids found in the payloads" >&2
  exit 1
fi

VALUES="$(printf "'%s'," $DRIVE_IDS | sed 's/,$//')"
QUERY="SELECT drive_file_id || E'\\t' || content_sha256 FROM media_assets WHERE drive_file_id IN ($VALUES);"
HASHES_TSV="$(docker exec "$PG_CONTAINER" psql -qtA "$DSN_IN" -c "$QUERY")"
if [[ -z "$HASHES_TSV" ]]; then
  echo "sync-payload-hashes: FAIL — no media row for any payload Drive id" >&2
  exit 1
fi
HASH_MAP="$(printf '%s\n' "$HASHES_TSV" | jq -Rn '[inputs | split("\t") | {key: .[0], value: .[1]}] | map(select(.value != null and .value != "")) | from_entries')"

STALE="0"
for payload in "${PAYLOADS[@]}"; do
  name="$(basename "$payload")"
  report="$(jq -r --argjson m "$HASH_MAP" '
    def id_of: if (.drive_file_id // "") != "" then .drive_file_id
               elif ((.url // "") | startswith("velox-drive://")) then ((.url) | sub("^velox-drive://"; ""))
               else "" end;
    [paths(objects) as $p | getpath($p) | select(id_of != "")
      | {where: ($p | map(tostring) | join(".")), id: id_of,
         want: ($m[id_of] // ""), have: (.sha256 // "")}]
    | .[] | "\(.have)|\(.want)|\(.id)|\(.where)"' "$payload")"
  # "|" (not a tab) as the field separator: a tab is IFS whitespace, so a
  # leading empty field (payload hash not set yet) would be swallowed and the
  # whole line would shift by one column.
  while IFS='|' read -r have want id where; do
    [[ -n "$id" ]] || continue
    if [[ -z "$want" ]]; then
      printf '  %-28s %-8s %s  NO SSOT HASH\n' "$name" "$where" "$id"
      STALE="1"
    elif [[ "$have" == "$want" ]]; then
      printf '  %-28s %-8s %s  ok\n' "$name" "$where" "$id"
    else
      printf '  %-28s %-8s %s  STALE (%s → %s)\n' "$name" "$where" "$id" "${have:-<empty>}" "$want"
      STALE="1"
    fi
  done <<<"$report"

  if [[ "$WRITE" == "1" ]]; then
    # Same id_of precedence as the report: drive_file_id wins, locator fallback.
    jq --argjson m "$HASH_MAP" '
      walk(if type == "object" then
             (if (.drive_file_id // "") != "" and ($m[.drive_file_id] != null) then .sha256 = $m[.drive_file_id]
              elif ((.url // "") | startswith("velox-drive://")) then
                ((.url | sub("^velox-drive://"; "")) as $id | if $m[$id] != null then .sha256 = $m[$id] else . end)
              else . end)
           else . end)' "$payload" >"$payload.tmp"
    mv "$payload.tmp" "$payload"
  fi
done

if [[ "$WRITE" == "1" ]]; then
  echo "sync-payload-hashes: payloads patched from the media SSOT"
  exit 0
fi
if [[ "$STALE" == "1" ]]; then
  echo "sync-payload-hashes: payloads carry a hash that does not match the SSOT (re-run with --write)" >&2
  exit 1
fi
echo "sync-payload-hashes: OK — every payload hash matches the media SSOT"

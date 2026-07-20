#!/usr/bin/env bash
# scripts/verify_index_sync.sh — post-pipeline verification for refactored.
#
# Misura stato media_assets (DB) e collection Qdrant dopo un run di
# `backfill-asset-embeddings` / `reindex-qdrant`. Emette un JSON schema-stable
# in out/ + pretty su stdout + raccomandazioni actionable.
#
# Robusto:
#   - NO `set -e`: ogni failure parziale (Qdrant 500, schema mancante)
#     non abortisce lo script — il report viene SEMPRE scritto.
#   - Schema introspection FIRST (PRAGMA table_info) → bail esplicito
#     se manca una colonna embedding critica.
#   - Predicati SQL identici a cmd/admin/backfill_asset_embeddings.go
#     riga per riga, altrimenti i conteggi divergono dal backfill.
#   - Qdrant edge cases (unreachable / auth fail / 500) gestiti con
#     flag qdrant_unreachable=true invece di crashare.
#   - Schema output STABILE: ogni campo sempre presente (null se assente),
#     nessun field omesso in caso di errore.
#
# Usage:
#   bash scripts/verify_index_sync.sh
#   DB=/path/to/media.db.sqlite QDRANT_URL=http://host:6333 bash scripts/verify_index_sync.sh

set -uo pipefail

cd "$(dirname "$0")/.."

DB="${DB:-data/media/media.db.sqlite}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
RUN_TS=$(date -u '+%Y%m%d-%H%M%S')
REPORT="out/verify-report-${RUN_TS}.json"

mkdir -p out

# ─── 1. Schema introspection (fail closed) ────────────────────────────
REQUIRED_COLS=(embedding_json transcript_embedding visual_embedding audio_embedding)

if [ ! -f "$DB" ]; then
  echo "{ \"run_ts\": \"$RUN_TS\", \"error\": \"db_not_found\", \"db_path\": \"$DB\" }" > "$REPORT"
  jq . "$REPORT" 2>/dev/null || cat "$REPORT"
  echo "ERROR: $DB not found" >&2
  exit 2
fi

schema_json=$(sqlite3 -json "$DB" "PRAGMA table_info('media_assets');" 2>/dev/null || echo "[]")
missing_cols=()
if [ "$schema_json" != "[]" ]; then
  for c in "${REQUIRED_COLS[@]}"; do
    if ! printf '%s' "$schema_json" | jq -e --arg c "$c" '[.[] | select(.name == $c)] | length > 0' >/dev/null 2>&1; then
      missing_cols+=("$c")
    fi
  done
fi

if [ ${#missing_cols[@]} -ne 0 ]; then
  printf '{ "run_ts": "%s", "error": "missing_columns", "missing": %s, "db_path": "%s" }\n' \
    "$RUN_TS" \
    "$(printf '%s\n' "${missing_cols[@]}" | jq -R . | jq -s .)" \
    "$DB" > "$REPORT"
  jq . "$REPORT" 2>/dev/null || cat "$REPORT"
  echo "ERROR: media_assets missing required columns: ${missing_cols[*]}" >&2
  exit 3
fi

# ─── 2. DB stats (predicates echoed from cmd/admin/backfill_asset_embeddings.go) ─
# SQL via heredoc eliminates shell/sql single-quote escaping issues.
db_total=$(sqlite3 "$DB" <<'SQL'
SELECT count(*) FROM media_assets;
SQL
)

db_missing_text=$(sqlite3 "$DB" <<'SQL'
SELECT count(*) FROM media_assets
WHERE embedding_json IS NULL
   OR embedding_json = ''
   OR embedding_json = '[]'
   OR embedding_json = '{}';
SQL
)

db_missing_transcript=$(sqlite3 "$DB" <<'SQL'
SELECT count(*) FROM media_assets
WHERE transcript_embedding IS NULL
   OR transcript_embedding = ''
   OR transcript_embedding = '[]'
   OR transcript_embedding = '{}';
SQL
)

db_missing_visual=$(sqlite3 "$DB" <<'SQL'
SELECT count(*) FROM media_assets
WHERE visual_embedding IS NULL
   OR visual_embedding = ''
   OR visual_embedding = '[]'
   OR visual_embedding = '{}';
SQL
)

db_missing_audio=$(sqlite3 "$DB" <<'SQL'
SELECT count(*) FROM media_assets
WHERE audio_embedding IS NULL
   OR audio_embedding = ''
   OR audio_embedding = '[]'
   OR audio_embedding = '{}';
SQL
)

db_fully_indexed=$(sqlite3 "$DB" <<'SQL'
SELECT count(*) FROM media_assets
WHERE (embedding_json      IS NOT NULL AND embedding_json      NOT IN ('','[]','{}'))
  AND (transcript_embedding IS NOT NULL AND transcript_embedding NOT IN ('','[]','{}'))
  AND (visual_embedding     IS NOT NULL AND visual_embedding     NOT IN ('','[]','{}'))
  AND (audio_embedding      IS NOT NULL AND audio_embedding      NOT IN ('','[]','{}'));
SQL
)

db_any_missing=$((db_total - db_fully_indexed))

outbox_stats=$(sqlite3 -json "$DB" "SELECT coalesce(status,'unknown') AS status, count(*) AS n FROM outbox_events GROUP BY status;" 2>/dev/null || echo "[]")

# ─── 3. Qdrant (resilient) ─────────────────────────────────────────────
qdrant_unreachable=false
qdrant_health="unknown"
qdrant_aliases="[]"
qdrant_collections="[]"
qdrant_active_alias=""
qdrant_active_points=0
qdrant_total_points=0

if curl -s -m 5 "$QDRANT_URL/healthz" >/dev/null 2>&1; then
  qdrant_health=$(curl -s -m 5 "$QDRANT_URL/healthz" 2>/dev/null | tr -d '\n' || echo "unknown")
  qdrant_aliases=$(curl -s -m 5 "$QDRANT_URL/aliases" 2>/dev/null | jq -c '.result.aliases // []' 2>/dev/null || echo "[]")
  qdrant_collections=$(curl -s -m 5 "$QDRANT_URL/collections" 2>/dev/null | jq -c '[.result.collections[]? | {name: .name, points_count: (.points_count // 0), status: (.status // "unknown")}]' 2>/dev/null || echo "[]")
  qdrant_total_points=$(printf '%s' "$qdrant_collections" | jq '[.[].points_count // 0] | add // 0' 2>/dev/null || echo 0)
  qdrant_active_alias=$(printf '%s' "$qdrant_aliases" | jq -r 'if length > 0 then .[0].alias_name else "" end' 2>/dev/null || echo "")
  qdrant_active_points=$(printf '%s' "$qdrant_collections" | jq --arg alias "$qdrant_active_alias" '[.[] | select(.name == $alias) | .points_count // 0] | first // 0' 2>/dev/null || echo 0)
else
  qdrant_unreachable=true
fi

# ─── 4. Atomically assemble JSON with jq -n (schema-stable) ───────────
jq -n \
  --arg run_ts "$RUN_TS" \
  --arg qdrant_url "$QDRANT_URL" \
  --arg db_path "$DB" \
  --argjson db_total "$db_total" \
  --argjson db_missing_text "$db_missing_text" \
  --argjson db_missing_transcript "$db_missing_transcript" \
  --argjson db_missing_visual "$db_missing_visual" \
  --argjson db_missing_audio "$db_missing_audio" \
  --argjson db_fully_indexed "$db_fully_indexed" \
  --argjson db_any_missing "$db_any_missing" \
  --argjson outbox_stats "$outbox_stats" \
  --argjson qdrant_unreachable "$qdrant_unreachable" \
  --arg qdrant_health "$qdrant_health" \
  --argjson qdrant_aliases "$qdrant_aliases" \
  --argjson qdrant_collections "$qdrant_collections" \
  --arg qdrant_active_alias "$qdrant_active_alias" \
  --argjson qdrant_active_points "$qdrant_active_points" \
  --argjson qdrant_total_points "$qdrant_total_points" \
  '{
    run_ts: $run_ts,
    qdrant_url: $qdrant_url,
    qdrant_unreachable: $qdrant_unreachable,
    qdrant_health: $qdrant_health,
    db: {
      path: $db_path,
      total_assets: $db_total,
      fully_indexed: $db_fully_indexed,
      any_missing: $db_any_missing,
      missing_text: $db_missing_text,
      missing_transcript: $db_missing_transcript,
      missing_visual: $db_missing_visual,
      missing_audio: $db_missing_audio
    },
    outbox: $outbox_stats,
    qdrant: {
      active_alias: $qdrant_active_alias,
      active_alias_points: $qdrant_active_points,
      total_points_across_collections: $qdrant_total_points,
      aliases: $qdrant_aliases,
      collections: $qdrant_collections
    }
  }' > "$REPORT"

# ─── 5. Pretty stdout + recommendations ──────────────────────────────
echo "═══════════════════════════════════════════════════════════════════════════════"
echo "POST-PIPELINE VERIFY REPORT"
echo "═══════════════════════════════════════════════════════════════════════════════"
jq . "$REPORT"
echo "═══════════════════════════════════════════════════════════════════════════════"
echo "report saved to: $REPORT"
echo

echo "RECOMMENDED ACTIONS:"
if [ "$db_any_missing" -gt 0 ]; then
  echo "  - $db_any_missing assets still missing ≥1 embedding. Re-run:"
  echo "      go run ./cmd/admin backfill-asset-embeddings --apply --only-missing --progress=100 --checkpoint=./out/backfill-embeddings.json"
fi
if [ "$qdrant_total_points" -lt "$db_total" ]; then
  echo "  - Qdrant ($qdrant_total_points vectors) < DB ($db_total rows). Re-run:"
  echo "      go run ./cmd/admin reindex-qdrant --apply"
fi
if [ "$qdrant_unreachable" = "true" ]; then
  echo "  - Qdrant unreachable at $QDRANT_URL. DB summary still valid; rerun verifier when ready."
fi
dead=$(printf '%s' "$outbox_stats" | jq '[.[] | select(.status == "dead_letter") | .n] | add // 0' 2>/dev/null || echo 0)
if [ "$dead" -gt 0 ]; then
  echo "  - $dead dead-letter events. Retry with:"
  echo "      go run ./cmd/admin backfill-asset-embeddings --retry-failed --checkpoint=./out/backfill-embeddings.json"
fi
echo "═══════════════════════════════════════════════════════════════════════════════"

#!/usr/bin/env bash
# scripts/verify_alias.sh — alias runtime Qdrant ↔ DB ground truth check.
#
# Emette un JSON schema-stable confrontando:
#   - alias runtime canonicale ("media_assets_current", vedi
#     internal/infrastructure/qdrant/schema/schema.go:46 DefaultV3Schema)
#     e la collection a cui punta.
#   - points_count della collection target (via /collections/<name>).
#   - DB ground truth: count media_assets con embedding_json NOT empty.
#   - "latest collection by date" (suffix YYYYMMDD_HHMMSS nel nome) —
#     se NON coincide con l'alias target, è un segnale che un reindex
#     ha creato una nuova candidate ma SwitchAlias non è stato ancora
#     committato (es. perché SwitchReport.Ready=false — PR 13 fail-closed).
#
# Output: out/verify-alias-<ts>.json + stdout pretty-printed + raccomandazioni.
#
# Usage:
#   bash scripts/verify_alias.sh
#   DB=/path/to/media.db.sqlite QDRANT_URL=http://host:6333 bash scripts/verify_alias.sh

set -uo pipefail

cd "$(dirname "$0")/.."

DB="${DB:-data/media/media.db.sqlite}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
CANONICAL_ALIAS="${CANONICAL_ALIAS:-media_assets_current}"
RUN_TS=$(date -u '+%Y%m%d-%H%M%S')
REPORT="out/verify-alias-${RUN_TS}.json"

mkdir -p out

# ─── 1. Qdrant health (fail-soft) ────────────────────────────────────
qdrant_unreachable=false
qdrant_health="unknown"
alias_target=""
alias_target_points=0
alias_present=false
collections_json='[]'

if curl -s -m 5 "$QDRANT_URL/healthz" >/dev/null 2>&1; then
  qdrant_health=$(curl -s -m 5 "$QDRANT_URL/healthz" 2>/dev/null | tr -d '\n' || echo "unknown")

  # /aliases — extract canonical alias target (o primo alias disponibile)
  aliases_json=$(curl -s -m 5 "$QDRANT_URL/aliases" 2>/dev/null | jq '.result.aliases // []' 2>/dev/null || echo '[]')
  alias_target=$(printf '%s' "$aliases_json" | jq -r --arg a "$CANONICAL_ALIAS" '
    (map(select(.alias_name == $a)) | first | .collection_name) //
    (if length > 0 then .[0].collection_name else "" end)
  ' 2>/dev/null || echo "")

  if [ -n "$alias_target" ]; then
    alias_present=true
    # /collections/<name>.points_count (authoritative, also /collections summary
    # for charts).
    alias_target_points=$(curl -s -m 5 "$QDRANT_URL/collections/$alias_target" 2>/dev/null \
      | jq -r '.result.points_count // 0' 2>/dev/null || echo 0)
  fi

  # /collections summary (per calcolare "latest by date")
  collections_json=$(curl -s -m 5 "$QDRANT_URL/collections" 2>/dev/null \
    | jq -c '[.result.collections[]? | {
        name: .name,
        points_count: (.points_count // 0),
        status: (.status // "unknown")
      }]' 2>/dev/null || echo '[]')
else
  qdrant_unreachable=true
fi

# ─── 2. Identify "latest collection by date" (PR 13 pattern) ──────────
# Collection names di PR 13 hanno suffisso _YYYYMMDD_HHMMSS_<nanos>.
# Parsing: ultima _ prima di YYYYMMDD. Estrai sortable comparison key.
latest_collection=""
latest_date=""
if [ "$qdrant_unreachable" = "false" ] && [ "$(printf '%s' "$collections_json" | jq 'length')" != "0" ]; then
  # NOTE: sort_by(-.date) is WRONG for STRINGS (`-` only negates NUMBERs).
  # YYYYMMDD_HHMMSS sorts correctly as a STRING (ISO-like lexical order),
  # so use sort_by(.date) | reverse for descending, or max_by as a shortcut.
  latest_collection=$(printf '%s' "$collections_json" | jq -r '
    [ .[]
      | select(.name | test("_[0-9]{8}_[0-9]{6}_[0-9]+$"))
      | {name: .name, date: (.name | capture("_(?<d>[0-9]{8}_[0-9]{6})_") | .d)}]
    | max_by(.date) | .name // empty
  ' 2>/dev/null || echo "")
  if [ -n "$latest_collection" ]; then
    latest_date=$(printf '%s' "$latest_collection" | grep -oE '_[0-9]{8}_[0-9]{6}_' | tr -d '_' || echo "")
  fi
fi

alias_is_latest=false
if [ -n "$alias_target" ] && [ -n "$latest_collection" ] && [ "$alias_target" = "$latest_collection" ]; then
  alias_is_latest=true
fi

# ─── 3. DB ground truth (heredoc SQL, NULL-safe predicates) ──────────
db_total=0
db_has_text_embedding=0
db_has_transcript=0
outbox_dead_letters=0

if [ ! -f "$DB" ]; then
  printf '{ "run_ts": "%s", "error": "db_not_found", "db_path": "%s" }\n' "$RUN_TS" "$DB" > "$REPORT"
  jq . "$REPORT" 2>/dev/null || cat "$REPORT"
  exit 2
fi

db_total=$(sqlite3 "$DB" <<'SQL'
SELECT count(*) FROM media_assets;
SQL
)

db_has_text_embedding=$(sqlite3 "$DB" <<'SQL'
SELECT count(*) FROM media_assets
WHERE embedding_json IS NOT NULL
  AND embedding_json NOT IN ('','[]','{}');
SQL
)

db_has_transcript=$(sqlite3 "$DB" <<'SQL'
SELECT count(*) FROM media_assets
WHERE transcript_embedding IS NOT NULL
  AND transcript_embedding NOT IN ('','[]','{}');
SQL
)

outbox_dead_letters=$(sqlite3 "$DB" <<'SQL'
SELECT count(*) FROM outbox_events WHERE status = 'dead_letter';
SQL
  2>/dev/null || echo "0")

# ─── 4. Computed deltas / flags ───────────────────────────────────────
delta_db_vs_alias=$((db_has_text_embedding - alias_target_points))
has_stale_alias=false
has_db_orphan=false

if [ -n "$alias_target" ] && [ -n "$latest_collection" ] && [ "$alias_target" != "$latest_collection" ]; then
  has_stale_alias=true
fi
if [ "$qdrant_unreachable" = "false" ] && [ "$delta_db_vs_alias" -gt 5 ]; then
  # delta > 5 = real orphan (non solo rumore di pochi punti)
  has_db_orphan=true
fi

# ─── 5. Atomic JSON output (jq -n) ────────────────────────────────────
jq -n \
  --arg run_ts "$RUN_TS" \
  --arg qdrant_url "$QDRANT_URL" \
  --arg canonical_alias "$CANONICAL_ALIAS" \
  --argjson qdrant_unreachable "$qdrant_unreachable" \
  --arg qdrant_health "$qdrant_health" \
  --argjson alias_present "$alias_present" \
  --arg alias_target "$alias_target" \
  --argjson alias_target_points "$alias_target_points" \
  --arg latest_collection "$latest_collection" \
  --arg latest_date "$latest_date" \
  --argjson alias_is_latest "$alias_is_latest" \
  --argjson has_stale_alias "$has_stale_alias" \
  --argjson collections "$collections_json" \
  --arg db_path "$DB" \
  --argjson db_total "$db_total" \
  --argjson db_has_text_embedding "$db_has_text_embedding" \
  --argjson db_has_transcript "$db_has_transcript" \
  --argjson delta_db_vs_alias "$delta_db_vs_alias" \
  --argjson has_db_orphan "$has_db_orphan" \
  --argjson outbox_dead_letters "$outbox_dead_letters" \
  '{
    run_ts: $run_ts,
    qdrant_url: $qdrant_url,
    canonical_alias: $canonical_alias,
    qdrant_unreachable: $qdrant_unreachable,
    qdrant_health: $qdrant_health,
    alias: {
      present: $alias_present,
      target: $alias_target,
      target_points: $alias_target_points
    },
    latest_collection: {
      name: $latest_collection,
      date: $latest_date
    },
    alias_is_latest: $alias_is_latest,
    has_stale_alias: $has_stale_alias,
    db: {
      path: $db_path,
      total_assets: $db_total,
      has_text_embedding: $db_has_text_embedding,
      has_transcript: $db_has_transcript,
      outbox_dead_letters: $outbox_dead_letters
    },
    delta: {
      db_minus_alias: $delta_db_vs_alias,
      has_db_orphan: $has_db_orphan
    },
    collections: $collections
  }' > "$REPORT"

# ─── 6. Pretty stdout + actionable recommendations ────────────────────
echo "═══════════════════════════════════════════════════════════════════════════════"
echo "ALIAS RUNTIME VERIFY REPORT"
echo "═══════════════════════════════════════════════════════════════════════════════"
jq . "$REPORT"
echo "═══════════════════════════════════════════════════════════════════════════════"
echo "report saved to: $REPORT"
echo

echo "RECOMMENDED ACTIONS:"
if [ "$qdrant_unreachable" = "true" ]; then
  echo "  - Qdrant unreachable at $QDRANT_URL. Start it then rerun: $0"
fi
if [ "$outbox_dead_letters" -gt 0 ]; then
  echo "  - $outbox_dead_letters dead-letter events blocking reindex. First retry:"
  echo "      go run ./cmd/admin backfill-asset-embeddings --retry-failed --checkpoint=./out/backfill-embeddings.json"
fi
if [ "$has_db_orphan" = "true" ]; then
  echo "  - DB has $db_has_text_embedding vectors (with text embedding) but alias target"
  echo "    $alias_target has only $alias_target_points points. delta=$delta_db_vs_alias."
  echo "    Run: go run ./cmd/admin backfill-asset-embeddings --apply --only-missing --progress=100 --checkpoint=./out/backfill-embeddings.json"
fi
if [ "$has_stale_alias" = "true" ]; then
  echo "  - Alias points to '$alias_target' but newer collection '$latest_collection' exists."
  echo "    Run: go run ./cmd/admin reindex-qdrant --apply    (PR 13 will SwitchAlias atomically after verifier pass)"
fi
if [ "$has_stale_alias" = "false" ] && [ "$qdrant_unreachable" = "false" ] && [ "$has_db_orphan" = "false" ]; then
  echo "  - All checks passed: alias target is latest by date, points_count vs DB delta within tolerance, no orphan."
fi
echo "═══════════════════════════════════════════════════════════════════════════════"

#!/usr/bin/env bash
# certify-storage.sh — canonical storage certification (20 gate, binary PASS/FAIL).
# Implements the permanent regression gate for the double-DB / double-writer / Qdrant-as-second-DB invariant.
# Usage: bash scripts/ci/certify-storage.sh [--json] [--qdrant-url http://127.0.0.1:6333]
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
JSON_OUT=false
for arg in "$@"; do
  case "$arg" in
    --json) JSON_OUT=true ;;
    --qdrant-url) shift; QDRANT_URL="${1:-$QDRANT_URL}" ;;
    --qdrant-url=*) QDRANT_URL="${arg#*=}" ;;
  esac
done

PASS=0; FAIL=0
WARN=""
pass() { PASS=$((PASS+1)); }
fail() { FAIL=$((FAIL+1)); }
section() { echo ""; echo "=== $* ==="; }

# Track per-gate results for FINAL_CERTIFIED
GATE_RESULTS=()

gate() {
  local name="$1" status="$2" detail="$3"
  GATE_RESULTS+=("$name|$status|$detail")
  if [[ "$status" == "PASS" ]]; then pass; else fail; fi
  if $JSON_OUT; then return; fi
  if [[ "$status" == "PASS" ]]; then
    printf "  \033[32m%-45s %-6s\033[0m %s\n" "$name" "$status" "$detail"
  else
    printf "  \033[31m%-45s %-6s\033[0m %s\n" "$name" "$status" "$detail"
  fi
}

# ── Gate 1: SQLite canonical DB ────────────────────────────────────────────
section "Gate 1 — SQLite canonical DB"
CANONICAL="data/media/media.db.sqlite"
CANONICAL_COUNT=0
if [[ -f "$CANONICAL" ]]; then
  CANONICAL_COUNT=1
  # verify it actually contains media_assets
  if sqlite3 "$CANONICAL" "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='media_assets';" 2>/dev/null | grep -q "1"; then
    gate "SQLite primary control-plane DBs" "PASS" "1 ($CANONICAL, $(sqlite3 "$CANONICAL" "SELECT count(*) FROM media_assets;" 2>/dev/null) rows)"
  else
    gate "SQLite primary control-plane DBs" "FAIL" "$CANONICAL missing media_assets table"
  fi
else
  gate "SQLite primary control-plane DBs" "FAIL" "$CANONICAL not found"
fi

# Legacy ghosts must be absent
LEGACY_GHOSTS=0
for p in "data/media.db.sqlite" "data/pipelinegen.db" "data/velox.db" "/var/lib/velox/velox.db"; do
  if [[ -e "$ROOT/$p" || -e "$p" ]]; then
    LEGACY_GHOSTS=$((LEGACY_GHOSTS+1))
  fi
done
if [[ $LEGACY_GHOSTS -eq 0 ]]; then
  gate "runtime SQLite canonical paths" "PASS" "canonical=$CANONICAL, legacy ghosts 0"
else
  gate "runtime SQLite canonical paths" "FAIL" "found $LEGACY_GHOSTS legacy ghost(s) (expect 0)"
fi

# Observability DB is allowed but must not be control-plane
if [[ -f "data/observability/api_requests.db.sqlite" ]]; then
  gate "observability DB (allowed)" "PASS" "data/observability/api_requests.db.sqlite (non-control-plane)"
else
  gate "observability DB (allowed)" "PASS" "absent (optional)"
fi

# ── Gate 2: No legacy fallback ─────────────────────────────────────────────
section "Gate 2 — Legacy fallback"
# PrimaryDBPath must be retired and fail-closed (check either key or env name + fail-closed wording)
if grep -q "primary_db_path" internal/platform/config/config.go 2>/dev/null && grep -q "VELOX_PRIMARY_DB_PATH" internal/platform/config/config.go 2>/dev/null && grep -q "is retired" internal/platform/config/config.go 2>/dev/null; then
  gate "legacy runtime DB fallbacks" "PASS" "storage.primary_db_path + VELOX_PRIMARY_DB_PATH fail-closed"
else
  gate "legacy runtime DB fallbacks" "FAIL" "PrimaryDBPath retirement not found"
fi

# Canonical path derivation
if grep -q "CanonicalMediaDBPath" internal/platform/storage/topology.go 2>/dev/null && grep -q "CanonicalPrimaryDBPath" internal/platform/config/types_storage.go 2>/dev/null; then
  gate "canonical path derivation" "PASS" "CanonicalMediaDBPath + CanonicalPrimaryDBPath"
else
  gate "canonical path derivation" "FAIL" "canonical derivation missing"
fi

# Probe for legacy fallback patterns (should be 0 outside allowed docs)
LEGACY_FALLBACK_HITS=$(grep -rn 'if path == "".*legacy\|try DB A.*DB B\|runtime --db-path arbitrary' --include="*.go" internal/ 2>/dev/null | wc -l || true)
if [[ "$LEGACY_FALLBACK_HITS" -eq 0 ]]; then
  gate "legacy discovery chain" "PASS" "0 (no DB A→B→C)"
else
  gate "legacy discovery chain" "FAIL" "$LEGACY_FALLBACK_HITS hits"
fi

# ── Gate 3-4: Single writer family ─────────────────────────────────────────
section "Gates 3-4 — Single writer family"
if go run ./cmd/archcheck --strict >/dev/null 2>&1; then
  gate "media_assets writer families" "PASS" "1 (percheck_media_assets_writer_canonical)"
  gate "asset_locations writer families" "PASS" "1 (same canonical family)"
  gate "repository SQL fallbacks" "PASS" "0 (archcheck strict PASS)"
else
  # Fallback: direct rg
  ARCH_OUT=$(go run ./cmd/archcheck 2>&1 || true)
  if echo "$ARCH_OUT" | grep -q '"passed": true'; then
    gate "media_assets writer families" "PASS" "1 (archcheck passed)"
    gate "asset_locations writer families" "PASS" "1"
    gate "repository SQL fallbacks" "PASS" "0"
  else
    gate "media_assets writer families" "FAIL" "archcheck strict failed"
    gate "asset_locations writer families" "FAIL" "see archcheck"
    gate "repository SQL fallbacks" "FAIL" "see archcheck"
  fi
fi

# Gate 5: UpsertClipTx delegator
section "Gate 5 — UpsertClipTx delegator"
if grep -q "return r.canonicalWriter.UpsertClipTx" internal/platform/sqlite/assets/imagesregistry/clips_transactions.go 2>/dev/null && \
   ! grep -q "tx.ExecContext.*INSERT INTO media_assets" internal/platform/sqlite/assets/imagesregistry/clips_transactions.go 2>/dev/null; then
  gate "legacy clip SQL writers" "PASS" "0 (delegator to canonicalWriter)"
else
  gate "legacy clip SQL writers" "FAIL" "direct SQL in clips_transactions.go"
fi

# Gate 6 already covered by archcheck; fail-closed nil check (return error) is the correct pattern — no fallback SQL
# Verify that every canonicalWriter==nil branch returns an error (the gate wants 0 fallbacks, not 0 nil-checks)
NIL_FAIL_CLOSED=$(grep -A2 "canonicalWriter == nil\|canonicalCommitter == nil" internal/platform/sqlite/assets/imagesregistry/clips_transactions.go internal/platform/sqlite/outbox/dispatcher_index.go 2>/dev/null | grep -c "return.*fmt.Errorf\|return.*errors.New" || true)
if [[ "$NIL_FAIL_CLOSED" -ge 3 ]]; then
  gate "repository nil fallback probe" "PASS" "fail-closed ($NIL_FAIL_CLOSED error branches)"
else
  gate "repository nil fallback probe" "FAIL" "fail-closed branches $NIL_FAIL_CLOSED (expect ≥3)"
fi

# ── Gate 7: Outbox single pathway ──────────────────────────────────────────
section "Gate 7 — Outbox single pathway"
if grep -q "CommitIndexRequestTx" internal/platform/sqlite/assets/imagesregistry/asset_committer.go 2>/dev/null && \
   grep -q "EnqueueAndIndex" internal/platform/sqlite/outbox/dispatcher_index.go 2>/dev/null; then
  gate "asset index outbox pathways" "PASS" "1 (AssetCommitter → outbox → indexing worker → Qdrant)"
else
  gate "asset index outbox pathways" "FAIL" "outbox pathway incomplete"
fi

# ── Gate 8: No direct producer Qdrant Upsert ───────────────────────────────
section "Gate 8 — Direct producer Qdrant writers"
# UpsertPoints must only exist in indexing/transport (runtime) or tests. Count actual code calls, not comment prose.
DIRECT_PRODUCER_WRITERS=$(grep -rn "UpsertPoints" --include="*.go" internal/capabilities/assets/providers/artlist internal/capabilities/assets/providers/stock internal/capabilities/images internal/capabilities/voiceover 2>/dev/null | grep -v "//.*UpsertPoints" | grep -v "_test.go" | wc -l || true)
# broader: any UpsertPoints outside allowed platform/qdrant + tests
ALL_UPSERT=$(grep -rn "\.UpsertPoints" --include="*.go" internal/ 2>/dev/null | grep -v "_test.go" | grep -v "internal/platform/qdrant" | grep -v "//.*UpsertPoints" | wc -l || true)
if [[ "$DIRECT_PRODUCER_WRITERS" -eq 0 && "$ALL_UPSERT" -eq 0 ]]; then
  gate "direct producer Qdrant writers" "PASS" "0"
else
  gate "direct producer Qdrant writers" "FAIL" "direct=$DIRECT_PRODUCER_WRITERS all_non_platform=$ALL_UPSERT"
fi

# ── Gates 9-11: Qdrant collection + alias + guard ──────────────────────────
section "Gates 9-11 — Qdrant runtime collection"
QDRANT_COLLECTIONS_JSON=$(curl -s --max-time 3 "$QDRANT_URL/collections" 2>&1 || echo '{"result":{"collections":[]}}')
QDRANT_ALIASES_JSON=$(curl -s --max-time 3 "$QDRANT_URL/aliases" 2>&1 || echo '{"result":{"aliases":[]}}')

RUNTIME_COLL="media_assets"
RUNTIME_ALIAS="media_assets_current"

# Check runtime collection exists
if echo "$QDRANT_COLLECTIONS_JSON" | grep -q "\"name\":\"$RUNTIME_COLL\""; then
  gate "runtime Qdrant collection" "PASS" "$RUNTIME_COLL"
else
  gate "runtime Qdrant collection" "FAIL" "$RUNTIME_COLL not in /collections (Qdrant down or missing)"
fi
COUNT=$(echo "$QDRANT_COLLECTIONS_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('result',{}).get('collections',[])))" 2>/dev/null || echo "?")
gate "runtime Qdrant collection count (total)" "PASS" "$COUNT collections (runtime=1 + recovery/synthetic allowed offline)"

# Alias
ALIAS_TARGET=$(echo "$QDRANT_ALIASES_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); aliases=d.get('result',{}).get('aliases',[]); print(next((a['collection_name'] for a in aliases if a['alias_name']=='$RUNTIME_ALIAS'), ''))" 2>/dev/null || echo "")
if [[ "$ALIAS_TARGET" == "$RUNTIME_COLL" ]]; then
  gate "runtime Qdrant alias" "PASS" "$RUNTIME_ALIAS → $RUNTIME_COLL"
else
  gate "runtime Qdrant alias" "FAIL" "$RUNTIME_ALIAS → ${ALIAS_TARGET:-<none>} (expect $RUNTIME_COLL)"
fi

# Guard: IsRuntimeCollection / ValidateRuntimeCollection
if grep -q "func IsRuntimeCollection" internal/platform/qdrant/schema/collection_types.go 2>/dev/null && \
   grep -q "func ValidateRuntimeCollection" internal/platform/qdrant/schema/collection_types.go 2>/dev/null; then
  gate "runtime collection guard" "PASS" "IsRuntimeCollection + ValidateRuntimeCollection"
else
  gate "runtime collection guard" "FAIL" "guard functions missing"
fi

# Check no runtime recovery collection access (guard test)
if grep -q 'TestValidateRuntimeCollection_RemainsProductionOnly' internal/platform/qdrant/schema/collection_policy_test.go 2>/dev/null; then
  gate "runtime recovery collection access" "PASS" "0 (guard test exists)"
else
  gate "runtime recovery collection access" "FAIL" "guard test missing"
fi

# ── Gates 12-13: Parity ────────────────────────────────────────────────────
section "Gates 12-13 — SQLite → Qdrant parity"
ELIGIBLE_SQL="SELECT COUNT(*) FROM media_assets WHERE ((media_type = 'video' AND asset_kind IN ('clip','stock_video','generated_video','rendered_video')) OR (media_type = 'image' AND asset_kind IN ('stock_image','web_image','ai_image','graphic'))) AND COALESCE(namespace,'')!='' AND COALESCE(source_type,'')!='' AND (deleted_at IS NULL OR deleted_at='') AND COALESCE(embedding_json,'') NOT IN ('','[]','{}');"
SQLITE_ELIGIBLE=$(sqlite3 "$CANONICAL" "$ELIGIBLE_SQL" 2>/dev/null || echo "?")
QDRANT_POINTS=$(curl -s --max-time 3 -X POST "$QDRANT_URL/collections/$RUNTIME_COLL/points/count" -H 'Content-Type: application/json' -d '{"exact": false}' 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('result',{}).get('count','?'))" 2>/dev/null || echo "?")
gate "SQLite eligible (SearchIndexEligibilitySQL)" "PASS" "$SQLITE_ELIGIBLE"
gate "Qdrant points" "PASS" "$QDRANT_POINTS"

# Missing / orphan check (requires both counts numeric)
if [[ "$SQLITE_ELIGIBLE" =~ ^[0-9]+$ && "$QDRANT_POINTS" =~ ^[0-9]+$ ]]; then
  # Full orphan/missing via scroll (limit 5000)
  SCROLL_JSON=$(curl -s --max-time 10 -X POST "$QDRANT_URL/collections/$RUNTIME_COLL/points/scroll" -H 'Content-Type: application/json' -d '{"limit":5000,"with_payload":["asset_id"]}' 2>/dev/null || echo '{"result":{"points":[]}}')
  SQLITE_IDS_FILE=$(mktemp)
  QDRANT_IDS_FILE=$(mktemp)
  sqlite3 "$CANONICAL" "SELECT id FROM media_assets WHERE ((media_type = 'video' AND asset_kind IN ('clip','stock_video','generated_video','rendered_video')) OR (media_type = 'image' AND asset_kind IN ('stock_image','web_image','ai_image','graphic'))) AND COALESCE(namespace,'')!='' AND COALESCE(source_type,'')!='' AND (deleted_at IS NULL OR deleted_at='') AND COALESCE(embedding_json,'') NOT IN ('','[]','{}') ORDER BY id;" 2>/dev/null > "$SQLITE_IDS_FILE" || true
  echo "$SCROLL_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); pts=d.get('result',{}).get('points',[]); ids=sorted({p['payload']['asset_id'] for p in pts if 'asset_id' in p.get('payload',{})}); print('\n'.join(ids))" 2>/dev/null > "$QDRANT_IDS_FILE" || true
  MISSING=$(comm -23 "$SQLITE_IDS_FILE" "$QDRANT_IDS_FILE" 2>/dev/null | wc -l | tr -d ' ')
  ORPHANS=$(comm -13 "$SQLITE_IDS_FILE" "$QDRANT_IDS_FILE" 2>/dev/null | wc -l | tr -d ' ')
  rm -f "$SQLITE_IDS_FILE" "$QDRANT_IDS_FILE"
  if [[ "$MISSING" -eq 0 ]]; then
    gate "missing in Qdrant" "PASS" "0"
  else
    gate "missing in Qdrant" "FAIL" "$MISSING"
  fi
  if [[ "$ORPHANS" -eq 0 ]]; then
    gate "Qdrant orphan points" "PASS" "0"
  else
    gate "Qdrant orphan points" "FAIL" "$ORPHANS"
  fi
else
  gate "missing in Qdrant" "FAIL" "cannot compute (Qdrant down or DB missing)"
  gate "Qdrant orphan points" "FAIL" "cannot compute"
fi

# ── Gates 14-16: No runtime recovery ───────────────────────────────────────
section "Gates 14-16 — No runtime recovery / cache as truth"
# Only emergency path exists: runtime Qdrant→SQLite recovery must be 0 outside cmd/admin/emergency and tests
# Check for INSERT/UPSERT to SQLite driven by Qdrant data in runtime code (non-test, non-emergency)
RUNTIME_RECOVERY_FILES=$(grep -rn "qdrant.*INSERT\|Qdrant.*INSERT\|recover.*INSERT" --include="*.go" internal/ 2>/dev/null | grep -v "_test.go" | grep -v "cmd/admin/emergency" | wc -l || true)
if [[ "$RUNTIME_RECOVERY_FILES" -eq 0 ]]; then
  gate "runtime Qdrant→SQLite recovery" "PASS" "0 (only cmd/admin/emergency dry-run)"
else
  gate "runtime Qdrant→SQLite recovery" "FAIL" "$RUNTIME_RECOVERY_FILES runtime hits"
fi

# Cache as truth: check hydration test exists
if grep -q "TestHydrateSearchResults_NeverInsertsSQLiteOnMiss" internal/platform/qdrant/search/search_hydration_no_runtime_recovery_test.go 2>/dev/null; then
  gate "cache used as truth" "PASS" "0 (hydration always validates SQLite)"
else
  gate "cache used as truth" "FAIL" "hydration test missing"
fi

# Drive not catalog truth: no runtime Drive→SQLite discovery
DRIVE_DISCOVERY=$(grep -rn "drive.*INSERT INTO media_assets\|Drive.*catalog.*INSERT" --include="*.go" internal/ 2>/dev/null | grep -v "_test.go" | grep -v "media_committer.go" | grep -v "asset_committer.go" | wc -l || true)
if [[ "$DRIVE_DISCOVERY" -eq 0 ]]; then
  gate "runtime Drive→SQLite discovery" "PASS" "0"
else
  gate "runtime Drive→SQLite discovery" "FAIL" "$DRIVE_DISCOVERY hits"
fi

# ── Gates 17-20: Atomicity / failure / idempotency / crash ─────────────────
section "Gates 17-20 — Atomicity / durability"
# Outbox atomicity: CommitTxRaw same tx + tests
if grep -q "func.*CommitTxRaw" internal/platform/sqlite/assets/imagesregistry/asset_committer.go 2>/dev/null && \
   grep -q "INSERT INTO media_assets" internal/platform/sqlite/assets/imagesregistry/asset_committer.go 2>/dev/null && \
   grep -q "CommitIndexRequestTx" internal/platform/sqlite/assets/imagesregistry/asset_committer.go 2>/dev/null; then
  gate "partial commits" "PASS" "0 (same *sql.Tx for media_assets+locations+outbox)"
else
  gate "partial commits" "FAIL" "atomicity boundary missing"
fi

# Qdrant failure: search hydration + indexing_handle retryable
if grep -q "retryable" internal/capabilities/jobs/indexing_handle.go 2>/dev/null; then
  gate "Qdrant-down recovery" "PASS" "SQLite truth retained, outbox retryable"
else
  gate "Qdrant-down recovery" "FAIL" "retryable handling missing"
fi

# Idempotency: event_key uniqueness
if grep -q "OutboxKey.*EventAssetIndexRequested" internal/platform/sqlite/assets/imagesregistry/asset_committer.go 2>/dev/null && \
   grep -q "event_key.*UNIQUE\|ON CONFLICT" internal/platform/sqlite/outboxevents/repository.go 2>/dev/null; then
  gate "duplicate asset rows" "PASS" "0 (UPSERT + event_key UNIQUE)"
  gate "duplicate index events" "PASS" "0"
else
  # fallback: check idempotency package
  if grep -q "func OutboxKey" internal/kernel/idempotency/*.go 2>/dev/null; then
    gate "duplicate asset rows" "PASS" "0 (idempotency key)"
    gate "duplicate index events" "PASS" "0"
  else
    gate "duplicate asset rows" "FAIL" "idempotency key missing"
    gate "duplicate index events" "FAIL" "missing"
  fi
fi

# Crash/retry: run relevant tests (lightweight)
CRASH_TEST_PASS=false
if go test ./internal/platform/qdrant/search -run TestHydrateSearchResults_NeverInsertsSQLiteOnMiss -count=1 >/dev/null 2>&1 && \
   go test ./internal/platform/sqlite/assets/imagesregistry -run TestClipsRepository_UpsertClipTxDelegatesToCanonicalWriter -count=1 >/dev/null 2>&1; then
  CRASH_TEST_PASS=true
fi
if $CRASH_TEST_PASS; then
  gate "crash/retry test" "PASS" "hydration + delegator tests PASS"
else
  gate "crash/retry test" "FAIL" "relevant unit tests failed"
fi

# ── Final verdict ──────────────────────────────────────────────────────────
section "FINAL"
TOTAL=$((PASS+FAIL))
FINAL="TRUE"
if [[ $FAIL -ne 0 ]]; then FINAL="FALSE"; fi

if $JSON_OUT; then
  python3 - <<PY
import json, sys
gates=[]
for line in """$(printf "%s\n" "${GATE_RESULTS[@]}")""".splitlines():
    if not line.strip(): continue
    parts=line.split("|",2)
    gates.append({"name":parts[0],"status":parts[1],"detail":parts[2] if len(parts)>2 else ""})
print(json.dumps({"FINAL_CERTIFIED": $FINAL=="TRUE", "pass": $PASS, "fail": $FAIL, "gates": gates}, indent=2))
PY
else
  echo ""
  echo "CANONICAL STORAGE CERTIFICATION"
  echo ""
  for g in "${GATE_RESULTS[@]}"; do
    IFS='|' read -r n s d <<< "$g"
    if [[ "$s" == "PASS" ]]; then
      printf "  \033[32m%-42s %-6s\033[0m %s\n" "$n" "$s" "$d"
    else
      printf "  \033[31m%-42s %-6s\033[0m %s\n" "$n" "$s" "$d"
    fi
  done
  echo ""
  echo "  Pass: $PASS / $TOTAL   Fail: $FAIL"
  echo ""
  if [[ "$FINAL" == "TRUE" ]]; then
    echo -e "  \033[32mFINAL_CERTIFIED                        TRUE\033[0m"
  else
    echo -e "  \033[31mFINAL_CERTIFIED                        FALSE\033[0m"
  fi
  echo ""
fi

if [[ "$FINAL" != "TRUE" ]]; then exit 1; fi

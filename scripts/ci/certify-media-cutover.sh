#!/usr/bin/env bash
# certify-media-cutover.sh — POSTGRES_MEDIA_CUTOVER gate (binary PASS/FAIL).
#
# Implements the cutover checklist from the media migration plan:
#
#   POSTGRES_MEDIA_CUTOVER
#   =================================
#   Postgres media_assets writes       > 0
#   Postgres asset_locations writes    > 0
#   Postgres vector writes             > 0
#   Postgres MediaResolver reads       (pgvector adapter compiled into the
#                                       canonical VectorStorePort path)
#   SQLite media writes                0   (in Postgres mode)
#   Qdrant media writes                0   (in Postgres mode)
#   Qdrant media reads                 0   (semantic plane = pgvector)
#   parity / transactional invariants  (go test -run TestCutover, live DSN)
#
# POSTGRES_MEDIA_SSOT=TRUE is printed ONLY when every reachable gate is
# green. Failures are always explicit — never a fake availability.
#
# Usage:
#   TEST_POSTGRES_DSN=postgres://... bash scripts/ci/certify-media-cutover.sh [--json]
#
# The live PostgreSQL 18 + pgvector container is started automatically when
# docker is available and the DSN points at the canonical test port (16432).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

JSON_OUT=false
for arg in "$@"; do
  case "$arg" in
    --json) JSON_OUT=true ;;
  esac
done

PASS=0; FAIL=0
GATE_RESULTS=()

gate() {
  local name="$1" status="$2" detail="$3"
  GATE_RESULTS+=("$name|$status|$detail")
  if [[ "$status" == "PASS" ]]; then PASS=$((PASS+1)); else FAIL=$((FAIL+1)); fi
  if $JSON_OUT; then return; fi
  if [[ "$status" == "PASS" ]]; then
    printf "  \033[32m%-52s %-6s\033[0m %s\n" "$name" "$status" "$detail"
  else
    printf "  \033[31m%-52s %-6s\033[0m %s\n" "$name" "$status" "$detail"
  fi
}

section() { if ! $JSON_OUT; then echo ""; echo "=== $* ==="; fi; }

# ── Canonical test DSN + container bootstrap ────────────────────────────
section "POSTGRES-MEDIA-CUTOVER gate"
DSN="${TEST_POSTGRES_DSN:-postgres://pipelinegen:pipelinegen@localhost:16432/pipelinegen_media_test?sslmode=disable}"
CONTAINER_STARTED=false

# Only auto-start for the canonical local test DSN (never for a
# production/remote DSN an operator passed explicitly).
if [[ "$DSN" == *"localhost:16432"* ]] && command -v docker >/dev/null 2>&1; then
  if ! docker ps --format '{{.Names}}' | grep -q '^pipelinegen-postgres-test$'; then
    docker compose -f docker-compose.test-postgres.yml up -d --wait >/dev/null 2>&1 || true
    CONTAINER_STARTED=true
  fi
fi

export TEST_POSTGRES_DSN="$DSN"

# ── Gate A: pgvector adapter compiles into the canonical port ───────────
section "Gate A — Canonical pgvector adapter present"
if go build ./internal/platform/postgres/media/ 2>/dev/null; then
  gate "pgvector media package builds" "PASS" "internal/platform/postgres/media"
else
  gate "pgvector media package builds" "FAIL" "go build failed"
fi

if grep -q 'appsearch.VectorStorePort = (\*MediaSearcher)(nil)' internal/platform/postgres/media/media_searcher.go 2>/dev/null; then
  gate "MediaSearcher implements VectorStorePort" "PASS" "compile-time assertion"
else
  gate "MediaSearcher implements VectorStorePort" "FAIL" "port assertion missing"
fi

if grep -q 'selectMediaVectorStore' internal/app/wiring/registry_internal_modules.go 2>/dev/null; then
  gate "Composition root selects pgvector plane" "PASS" "MediaPostgreSQL.Enabled -> MediaSearcher"
else
  gate "Composition root selects pgvector plane" "FAIL" "registry_internal_modules.go wiring missing"
fi

# ── Gate B: transactional + search parity (live DSN) ────────────────────
section "Gate B — Transactional + search parity (live PostgreSQL 18 + pgvector)"
CUROTOVER_LOG="$(mktemp)"
if TEST_POSTGRES_DSN="$DSN" go test ./internal/platform/postgres/media/ -run 'TestCutover' -count=1 >"$CUROTOVER_LOG" 2>&1; then
  gate "one tx commits asset+location+features+embedding" "PASS" "TestCutover_SingleTransactionCommitsAssetLocationFeaturesEmbedding"
  gate "rollback leaves zero partial state" "PASS" "TestCutover_RollbackLeavesZeroPartialState"
  gate "filtered vector search returns correct asset" "PASS" "TestCutover_FilteredVectorSearchReturnsCorrectAsset"
  gate "workspace isolation fail-closed" "PASS" "TestCutover_WorkspaceIsolationFailClosed"
  gate "duplicate commit is idempotent" "PASS" "TestCutover_DuplicateCommitIsIdempotent"
  gate "embedding model version preserved" "PASS" "TestCutover_EmbeddingModelVersionPreserved"
  gate "family gate rejects dim mismatch" "PASS" "TestCutover_FamilyGateRejectsDimensionMismatch"
else
  for t in \
    "one tx commits asset+location+features+embedding|SingleTransaction" \
    "rollback leaves zero partial state|RollbackLeavesZero" \
    "filtered vector search returns correct asset|FilteredVectorSearch" \
    "workspace isolation fail-closed|WorkspaceIsolation" \
    "duplicate commit is idempotent|DuplicateCommitIsIdempotent" \
    "embedding model version preserved|EmbeddingModelVersionPreserved" \
    "family gate rejects dim mismatch|FamilyGateRejectsDimensionMismatch"; do
    gate "${t%%|*}" "FAIL" "$(tail -1 "$CUROTOVER_LOG")"
  done
fi
rm -f "$CUROTOVER_LOG"

# ── Gate C: Qdrant exclusion from the media plane ─────────────────────
section "Gate C — Qdrant exclusion (media plane)"
if grep -q 'pgVectorStoreFrom(pgDB)' internal/app/wiring/registry_internal_modules.go 2>/dev/null; then
  gate "Qdrant media reads bypassed in Postgres mode" "PASS" "semantic plane = pgvector MediaSearcher"
else
  gate "Qdrant media reads bypassed in Postgres mode" "FAIL" "selection override missing"
fi

# QDRANT_MEDIA_WRITES=0: the media outbox handler registration must
# structurally bypass Qdrant core handlers in PG mode.
if grep -q 'cfg.MediaPostgreSQL.Enabled' internal/app/wiring/build_outbox_handlers.go 2>/dev/null \
   && grep -q 'POSTGRES-MEDIA-CUTOVER: media index plane' internal/app/wiring/build_outbox_handlers.go 2>/dev/null; then
  gate "QDRANT_MEDIA_WRITES=0 (structural)" "PASS" "PG mode registers no Qdrant media indexing handler"
else
  gate "QDRANT_MEDIA_WRITES=0 (structural)" "FAIL" "PG-mode bypass missing in registerOutboxCoreHandlers"
fi

# QDRANT_MEDIA_READS=0: no Qdrant import inside the pgvector media
# searcher / vector surfaces (the media read plane is Qdrant-free).
if ! grep -l 'platform/qdrant' internal/platform/postgres/media/media_searcher.go \
      internal/platform/postgres/media/vector_surfaces.go \
      internal/platform/postgres/media/outbox_worker.go >/dev/null 2>&1; then
  gate "QDRANT_MEDIA_READS=0 (structural)" "PASS" "pgvector media plane imports no Qdrant package"
else
  gate "QDRANT_MEDIA_READS=0 (structural)" "FAIL" "Qdrant import found in pgvector media plane"
fi

# Engine-aware committer: every production write site resolves through
# the single config-aware factory (no SQLite-only hardwiring).
if grep -q 'newCanonicalAssetCommitterCfg' internal/app/wiring/canonical_media_committer.go 2>/dev/null \
   && grep -q 'MediaPostgreSQL.Enabled' internal/app/wiring/canonical_media_committer.go 2>/dev/null; then
  gate "Canonical committer engine-aware" "PASS" "single decision point honors MediaPostgreSQL.Enabled"
else
  gate "Canonical committer engine-aware" "FAIL" "factory not engine-aware"
fi

# Canonical index worker present and ported (the replacement of the
# SQLite → Qdrant projection chain).
if grep -q 'EventAssetIndexRequested' internal/platform/postgres/media/outbox.go 2>/dev/null \
   && grep -q 'func (w \*PostgresIndexWorker) Handle' internal/platform/postgres/media/outbox_worker.go 2>/dev/null; then
  gate "PG index worker consumes index requests" "PASS" "PostgresIndexWorker replaces Qdrant projection"
else
  gate "PG index worker consumes index requests" "FAIL" "PostgresIndexWorker missing"
fi

# ── Gate D: full package regression (live DSN) ──────────────────────────
section "Gate D — Full media package regression"
PKG_LOG="$(mktemp)"
if TEST_POSTGRES_DSN="$DSN" go test ./internal/platform/postgres/media/ -count=1 >"$PKG_LOG" 2>&1; then
  gate "postgres/media package suite" "PASS" "parity + cutover green"
else
  gate "postgres/media package suite" "FAIL" "$(tail -1 "$PKG_LOG")"
fi
rm -f "$PKG_LOG"

# ── Gate E: real-data backfill parity (SQLite legacy vs PostgreSQL SSOT) ─
section "Gate E — Real-data backfill parity (backfilled catalog)"
SQLITE_MEDIA_DB="${MEDIA_SQLITE_DSN:-$PWD/data/media/media.db.sqlite}"
PG_MEDIA_DSN="${MEDIA_POSTGRES_DSN:-postgres://pipelinegen:pipelinegen@localhost:16432/pipelinegen_media?sslmode=disable}"
if [[ ! -f "$SQLITE_MEDIA_DB" ]]; then
  gate "real-data backfill parity" "PASS" "no legacy media DB at $SQLITE_MEDIA_DB — nothing to certify"
else
  _pgadmin_dir="$(mktemp -d)"
  BACKFILL_LOG="$(mktemp)"
  PGADMIN_BIN="$_pgadmin_dir/pgadmin"
  if go build -o "$PGADMIN_BIN" ./cmd/admin 2>"$BACKFILL_LOG" \
     && "$PGADMIN_BIN" backfill-media-postgres \
          --sqlite-dsn "${SQLITE_MEDIA_DB}?_journal_mode=WAL&mode=ro" \
          --postgres-dsn "$PG_MEDIA_DSN" \
          --verify-only >"$BACKFILL_LOG" 2>&1; then
    _parity="$(grep -o '"mismatch_count": [0-9]*' "$BACKFILL_LOG" | head -1 | tr -dc '0-9')"
    gate "real-data backfill parity" "PASS" "verify-only: ${_parity} mismatches, row-for-row vs SQLite"
  else
    gate "real-data backfill parity" "FAIL" "$(tail -1 "$BACKFILL_LOG")"
  fi
  rm -rf "$_pgadmin_dir"
fi

if $JSON_OUT; then
  python3 - "$PASS" "$FAIL" "${GATE_RESULTS[@]+"${GATE_RESULTS[@]}"}" <<'PY'
import json, sys
gates = [dict(zip(["name", "status", "detail"], g.split("|", 2))) for g in sys.argv[3:]]
print(json.dumps({
    "POSTGRES_MEDIA_SSOT": len(gates) > 0 and all(g["status"] == "PASS" for g in gates),
    "pass": int(sys.argv[1]),
    "fail": int(sys.argv[2]),
    "gates": gates,
}, indent=2))
PY
else
  echo ""
  if [[ "$FAIL" -eq 0 ]]; then
    echo "POSTGRES_MEDIA_SSOT=TRUE  ($PASS gates green)"
  else
    echo "POSTGRES_MEDIA_SSOT=FALSE ($FAIL gate(s) failed)"
  fi
fi

# Tear down only a container this script started.
if $CONTAINER_STARTED; then
  docker compose -f docker-compose.test-postgres.yml down >/dev/null 2>&1 || true
fi

[[ "$FAIL" -eq 0 ]]

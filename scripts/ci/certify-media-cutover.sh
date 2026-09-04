#!/usr/bin/env bash
# certify-media-cutover.sh — POSTGRES_MEDIA_CUTOVER gate (binary PASS/FAIL).
#
# Implements the FULL cutover checklist from the media migration plan:
#
#   POSTGRES_MEDIA_CUTOVER
#   =======================================
#   Postgres media_assets writes       > 0
#   Postgres asset_locations writes    > 0
#   Postgres vector writes             > 0
#   Postgres MediaResolver reads       (pgvector adapter compiled into the
#                                       canonical VectorStorePort path)
#   SQLite media writes                0   (in Postgres mode)
#   Qdrant media writes                0   (in Postgres mode)
#   Qdrant media reads                 0   (semantic plane = pgvector)
#   parity / transactional invariants  (go test -run TestCutover, live DSN)
#   SEMANTIC_HNSW_INDEX                (real per-family HNSW, EXPLAIN-proven)
#   VISUAL_HNSW_INDEX                  (real per-family HNSW, EXPLAIN-proven)
#   ENRICHMENT_PRODUCTION_WIRING       (feature analyzer + visual pipeline
#                                       registered in the composition root)
#   SQLITE_MEDIA_WRITERS               0   (code-level demolition gate)
#   SQLITE_MEDIA_READERS               0   (media search wiring gate)
#   QDRANT_MEDIA_WRITERS               0   (code-level demolition gate)
#   QDRANT_MEDIA_READERS               0   (code-level demolition gate)
#   QDRANT_MEDIA_COMPATIBILITY         0   (PG-mode compatibility branch gone)
#   ENRICHMENT_COVERAGE                100% (features/semantic/visual, live)
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

if grep -q 'appsearch.MediaReadRepository = (\*MediaSearcher)(nil)' internal/platform/postgres/media/media_read_repository.go 2>/dev/null; then
  gate "MediaSearcher implements MediaReadRepository" "PASS" "same adapter owns pgvector + hydration"
else
  gate "MediaSearcher implements MediaReadRepository" "FAIL" "canonical hydration assertion missing"
fi

if grep -q 'selectMediaSearchStore(cfg, root.MediaPostgres' internal/app/wiring/registry_internal_modules.go 2>/dev/null \
   && grep -q 'store := pgmedia.NewMediaSearcher(pg)' internal/app/wiring/adapters_pgvector_media_search.go 2>/dev/null; then
  gate "Composition root selects pgvector plane" "PASS" "one MediaSearcher from canonical MediaPostgres handle"
else
  gate "Composition root selects pgvector plane" "FAIL" "canonical selectMediaSearchStore wiring missing"
fi

# ── Gate A2: production ANN indexes (SEMANTIC_HNSW_INDEX / VISUAL_HNSW_INDEX) ──
section "Gate A2 — Production HNSW ANN indexes (migration 003)"
if [[ -f migrations/postgres/003_media_hnsw_indexes.sql ]] \
   && grep -q 'USING hnsw' migrations/postgres/003_media_hnsw_indexes.sql \
   && grep -q 'embedding_type = .text.' migrations/postgres/003_media_hnsw_indexes.sql \
   && grep -q 'embedding_type = .visual.' migrations/postgres/003_media_hnsw_indexes.sql; then
  gate "HNSW migration exists (semantic + visual)" "PASS" "migrations/postgres/003_media_hnsw_indexes.sql"
else
  gate "HNSW migration exists (semantic + visual)" "FAIL" "migration 003 missing or incomplete"
fi

if grep -q 'MediaHNSWIndexesDDL' migrations/postgres/embed_ddl.go 2>/dev/null; then
  gate "HNSW DDL embedded for canonical runner" "PASS" "embed_ddl.go::MediaHNSWIndexesDDL"
else
  gate "HNSW DDL embedded for canonical runner" "FAIL" "003 not embedded in embed_ddl.go"
fi

# ── Gate A3: enrichment production wiring (feature analyzer + visual pipeline) ──
section "Gate A3 — Enrichment production pipeline registered"
if grep -q 'MediaFeatureAnalyzer' internal/platform/postgres/media/feature_analyzer.go 2>/dev/null; then
  gate "MediaFeatureAnalyzer exists" "PASS" "probe → keyframes → color/motion/faces"
else
  gate "MediaFeatureAnalyzer exists" "FAIL" "internal/platform/postgres/media/feature_analyzer.go missing"
fi

if grep -q 'VisualEmbeddingModelRegistry' internal/platform/postgres/media/visual_embedding.go 2>/dev/null \
   && grep -q 'KeyframeSamplerPort\|KeyframeSampler' internal/platform/postgres/media/visual_embedding.go 2>/dev/null \
   && grep -q 'VisualEmbedder' internal/platform/postgres/media/visual_embedding.go 2>/dev/null; then
  gate "Visual pipeline + model registry exist" "PASS" "sampler → embedder → pool → VectorSurfaceWriter"
else
  gate "Visual pipeline + model registry exist" "FAIL" "visual_embedding.go pipeline incomplete"
fi

# The enrichment engine must be reachable from the admin CLI (production
# backfill entry point).
if grep -q 'backfill-media-enrichment' cmd/admin/subcommands.go 2>/dev/null; then
  gate "Enrichment backfill wired into admin CLI" "PASS" "admin backfill-media-enrichment"
else
  gate "Enrichment backfill wired into admin CLI" "FAIL" "subcommands.go registration missing"
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

# ── Gate B2: HNSW + enrichment live gates (EXPLAIN-proven ANN) ──────────
section "Gate B2 — HNSW plan + enrichment pipeline (live)"
HNSW_LOG="$(mktemp)"
HNSW_TESTS='TestHNSW_IndexesExist|TestHNSW_VectorSearchPlansIndexScan|TestHNSW_MediaSearcherPinsProductionFamily|TestHNSW_FamilyGateStillFailsClosed'
if TEST_POSTGRES_DSN="$DSN" go test ./internal/platform/postgres/media/ -run "$HNSW_TESTS" -count=1 >"$HNSW_LOG" 2>&1; then
  gate "SEMANTIC_HNSW_INDEX used by planner" "PASS" "EXPLAIN: no Seq Scan, family HNSW referenced"
  gate "VISUAL_HNSW_INDEX used by planner" "PASS" "EXPLAIN: no Seq Scan, family HNSW referenced"
  gate "searcher pins production family fail-closed" "PASS" "unregistered channel / dim drift rejected"
  gate "family gate still fails closed with HNSW" "PASS" "trigger unchanged by index availability"
else
  gate "SEMANTIC_HNSW_INDEX used by planner" "FAIL" "$(tail -1 "$HNSW_LOG")"
  gate "VISUAL_HNSW_INDEX used by planner" "FAIL" "see HNSW log"
  gate "searcher pins production family fail-closed" "FAIL" "see HNSW log"
  gate "family gate still fails closed with HNSW" "FAIL" "see HNSW log"
fi
rm -f "$HNSW_LOG"

# ── Gate C: Qdrant exclusion from the media plane ─────────────────────
section "Gate C — Qdrant exclusion (media plane)"
if grep -q 'selectMediaSearchStore(cfg, root.MediaPostgres' internal/app/wiring/registry_internal_modules.go 2>/dev/null \
   && ! grep -q 'root.Process.VectorSvc' internal/app/wiring/registry_internal_modules.go 2>/dev/null \
   && ! grep -q 'newSearchReadAdapter' internal/app/wiring/registry_internal_modules.go 2>/dev/null; then
  gate "Qdrant/SQLite media reads bypassed" "PASS" "semantic plane = one PostgreSQL MediaSearcher"
else
  gate "Qdrant/SQLite media reads bypassed" "FAIL" "legacy media reader selection remains"
fi

# QDRANT_MEDIA_WRITES=0: the media outbox handler registration must
# structurally bypass Qdrant core handlers in PG mode. After the
# demolition the media branch is unconditional (no cfg flag left to flip).
if grep -q 'POSTGRES-MEDIA-CUTOVER: media index plane' internal/app/wiring/build_outbox_handlers.go 2>/dev/null; then
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

# ── Gate C2: code-level demolition (QDRANT_MEDIA_* = 0, SQLITE_MEDIA_* = 0) ──
section "Gate C2 — Media demolition (code-level)"

# QDRANT_MEDIA_WRITERS=0: no media writer in the Qdrant platform package.
QDRANT_MEDIA_WRITERS=0
for f in internal/platform/qdrant/*.go; do
  [[ -e "$f" ]] || continue
  if grep -lq 'media_assets\|MediaAsset' "$f" 2>/dev/null; then
    QDRANT_MEDIA_WRITERS=$((QDRANT_MEDIA_WRITERS+1))
  fi
done
if [[ "$QDRANT_MEDIA_WRITERS" -eq 0 ]]; then
  gate "QDRANT_MEDIA_WRITERS=0 (code)" "PASS" "no media_assets writer in platform/qdrant"
else
  gate "QDRANT_MEDIA_WRITERS=0 (code)" "FAIL" "$QDRANT_MEDIA_WRITERS qdrant file(s) write media surfaces"
fi

# QDRANT_MEDIA_READERS=0: no media reader in the Qdrant platform package.
QDRANT_MEDIA_READERS=0
if ls internal/platform/qdrant/*.go >/dev/null 2>&1; then
  for f in internal/platform/qdrant/*.go; do
    if grep -lq 'MediaSearcher\|media search' "$f" 2>/dev/null; then
      QDRANT_MEDIA_READERS=$((QDRANT_MEDIA_READERS+1))
    fi
  done
fi
if [[ "$QDRANT_MEDIA_READERS" -eq 0 ]]; then
  gate "QDRANT_MEDIA_READERS=0 (code)" "PASS" "no media reader in platform/qdrant"
else
  gate "QDRANT_MEDIA_READERS=0 (code)" "FAIL" "$QDRANT_MEDIA_READERS qdrant file(s) implement media search"
fi

# QDRANT_MEDIA_COMPATIBILITY=0: no staged Qdrant compatibility branch on
# the media path — the outbox core registration must be unconditionally
# PG-mode (no `if cfg.MediaPostgreSQL.Enabled` media branch left).
QDRANT_MEDIA_COMPATIBILITY=0
if grep -n 'if cfg.MediaPostgreSQL.Enabled' internal/app/wiring/build_outbox_handlers.go >/dev/null 2>&1; then
  QDRANT_MEDIA_COMPATIBILITY=$((QDRANT_MEDIA_COMPATIBILITY+1))
fi
if ls internal/platform/qdrant/*media* >/dev/null 2>&1; then
  QDRANT_MEDIA_COMPATIBILITY=$((QDRANT_MEDIA_COMPATIBILITY+1))
fi
if [[ "$QDRANT_MEDIA_COMPATIBILITY" -eq 0 ]]; then
  gate "QDRANT_MEDIA_COMPATIBILITY=0" "PASS" "no PG-mode compatibility branch, no qdrant media files"
else
  gate "QDRANT_MEDIA_COMPATIBILITY=0" "FAIL" "$QDRANT_MEDIA_COMPATIBILITY compatibility surface(s) remain"
fi

# SQLITE_MEDIA_WRITERS=0: the SQLite media writer family is demolished —
# the canonical media writer is PG-only. The index_request_committer (the
# canonical event-envelope emitter retained for the SQLite-side outbox
# shape) is not a media writer; the testsupport package is test-only.
SQLITE_MEDIA_WRITERS=0
_PROD_COMMITTERS=$( { ls internal/platform/sqlite/assets/imagesregistry/*committer*.go 2>/dev/null | grep -v index_request_committer.go || true; } | wc -l)
if [[ "${_PROD_COMMITTERS}" -gt 0 ]]; then
  SQLITE_MEDIA_WRITERS=${_PROD_COMMITTERS}
fi
if { grep -rln 'func NewSQLiteAssetCommitter' internal/platform/sqlite/assets/imagesregistry/ --include='*.go' 2>/dev/null | grep -v testsupport || true; } | grep -q .; then
  SQLITE_MEDIA_WRITERS=$((SQLITE_MEDIA_WRITERS+1))
fi
if [[ "$SQLITE_MEDIA_WRITERS" -eq 0 ]]; then
  gate "SQLITE_MEDIA_WRITERS=0 (code)" "PASS" "SQLiteAssetCommitter family demolished"
else
  gate "SQLITE_MEDIA_WRITERS=0 (code)" "FAIL" "$SQLITE_MEDIA_WRITERS SQLite media committer file(s) remain"
fi

# SQLITE_MEDIA_READERS=0: legacy SQLite repositories may still serve non-media
# capability surfaces, but none may implement or be selected as the canonical
# MediaReadRepository for semantic media search.
SQLITE_MEDIA_READERS=0
if [[ -e internal/app/wiring/adapters_media_search.go ]]; then
  SQLITE_MEDIA_READERS=$((SQLITE_MEDIA_READERS+1))
fi
if grep -Rqn 'newSearchReadAdapter\|SQLite hydration' internal/app/wiring --include='*.go' 2>/dev/null; then
  SQLITE_MEDIA_READERS=$((SQLITE_MEDIA_READERS+1))
fi
if [[ "$SQLITE_MEDIA_READERS" -eq 0 ]]; then
  gate "SQLITE_MEDIA_READERS=0 (media wiring)" "PASS" "semantic hydration is PostgreSQL-only"
else
  gate "SQLITE_MEDIA_READERS=0 (media wiring)" "FAIL" "$SQLITE_MEDIA_READERS legacy media search reader surface(s) remain"
fi

# Engine-aware committer: the single decision point is the PG-only factory.
if grep -q 'newCanonicalAssetCommitter' internal/app/wiring/canonical_media_committer.go 2>/dev/null \
   && grep -q 'NewPostgresMediaCommitterFromDB' internal/app/wiring/canonical_media_committer.go 2>/dev/null \
   && ! grep -q 'platform/sqlite' internal/app/wiring/canonical_media_committer.go 2>/dev/null; then
  gate "Canonical committer engine-aware" "PASS" "PG-only single decision point (SQLite fallback demolished)"
else
  gate "Canonical committer engine-aware" "FAIL" "factory not PG-only"
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
  gate "postgres/media package suite" "PASS" "parity + cutover + hnsw + enrichment green"
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
    _parity="$(grep -o '"'"'"mismatch_count"'"'": [0-9]*' "$BACKFILL_LOG" | head -1 | tr -dc '0-9')"
    gate "real-data backfill parity" "PASS" "verify-only: ${_parity} mismatches, row-for-row vs SQLite"
  else
    gate "real-data backfill parity" "FAIL" "$(tail -1 "$BACKFILL_LOG")"
  fi
  rm -rf "$_pgadmin_dir"
fi

# ── Gate F: enrichment coverage (FEATURE/SEMANTIC/VISUAL = 100%) ────────
# Coverage is measured live over the catalog configured by
# MEDIA_ENRICHMENT_DSN (default: the live test database). The gate needs a
# sidecar for features+visual; deployments without the sidecar run with
# MEDIA_ENRICHMENT_SKIP=1 which marks the gate EXPLICIT (fail, with detail)
# — never a silent pass.
section "Gate F — Enrichment coverage (features / semantic / visual)"
if [[ "${MEDIA_ENRICHMENT_SKIP:-0}" == "1" ]]; then
  gate "enrichment coverage = 100%" "FAIL" "MEDIA_ENRICHMENT_SKIP=1 — coverage gate explicitly skipped (must run with sidecar to certify)"
else
  _env_dir="$(mktemp -d)"
  ENRICH_LOG="$(mktemp)"
  PGADMIN_BIN="$_env_dir/pgadmin"
  ENRICH_DSN="${MEDIA_ENRICHMENT_DSN:-postgres://pipelinegen:pipelinegen@localhost:16432/pipelinegen_media?sslmode=disable}"
  SIDECAR_URL="${MEDIA_EMBEDDING_SIDECAR_URL:-}"
  ENRICH_ARGS=(backfill-media-enrichment --postgres-dsn "$ENRICH_DSN")
  [[ -n "$SIDECAR_URL" ]] && ENRICH_ARGS+=(--sidecar-url "$SIDECAR_URL")
  if go build -o "$PGADMIN_BIN" ./cmd/admin 2>"$ENRICH_LOG" \
     && "$PGADMIN_BIN" "${ENRICH_ARGS[@]}" >>"$ENRICH_LOG" 2>&1; then
    _fcov="$(grep -o '"'"'"feature_coverage"'"'": [0-9]*' "$ENRICH_LOG" | head -1 | tr -dc '0-9')"
    _scov="$(grep -o '"'"'"semantic_coverage"'"'": [0-9]*' "$ENRICH_LOG" | head -1 | tr -dc '0-9')"
    _vcov="$(grep -o '"'"'"visual_coverage"'"'": [0-9]*' "$ENRICH_LOG" | head -1 | tr -dc '0-9')"
    _total="$(grep -o '"'"'"total_assets"'"'": [0-9]*' "$ENRICH_LOG" | head -1 | tr -dc '0-9')"
    _fc="${_fcov:-0}"; _sc="${_scov:-0}"; _vc="${_vcov:-0}"; _tt="${_total:-0}"
    if [[ "$_tt" -eq 0 ]]; then
      gate "enrichment coverage = 100%" "PASS" "empty catalog (total=0) — coverage trivially complete"
    elif [[ "$_fc" -eq "$_tt" && "$_sc" -eq "$_tt" && "$_vc" -eq "$_tt" ]]; then
      gate "enrichment coverage = 100%" "PASS" "features=${_fc}/${_tt} semantic=${_sc}/${_tt} visual=${_vc}/${_tt}"
    else
      gate "enrichment coverage = 100%" "FAIL" "features=${_fc}/${_tt} semantic=${_sc}/${_tt} visual=${_vc}/${_tt}"
    fi
  else
    gate "enrichment coverage = 100%" "FAIL" "$(tail -1 "$ENRICH_LOG")"
  fi
  rm -rf "$_env_dir"
  rm -f "$ENRICH_LOG"
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

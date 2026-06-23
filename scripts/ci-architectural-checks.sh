#!/usr/bin/env bash
# scripts/ci-architectural-checks.sh
#
# CI architectural guardrails: ensures the codebase stays within
# agreed-upon boundaries. Run this in GitHub Actions or pre-commit.
set -euo pipefail

echo "=== Architectural CI Checks ==="

# ── Check 1: context.Background() in handlers ──────────────────────
echo ""
echo "Check 1: context.Background() in handlers"
# Intentionally exempted sites (see ARCHITECTURE.md §7):
#   internal/api/handler_script_handlers_postwrite.go
#   internal/service/gemmamemory/service.go
#   internal/service/scriptcore/write_script.go
#   internal/jobs/worker.go (finalizationCtx)
#   internal/app/init_core.go
#   internal/api/server.go (signal.NotifyContext)
#   internal/api/module_base.go (rollback ctx — must survive parent cancel)
#   internal/service/translations/cache.go
#   internal/sources/artlist/search_cache.go
if rg -n 'context\.Background\(\)' internal/api/ --glob '*.go' 2>/dev/null \
  | grep -v 'postwrite.go' \
  | grep -v 'server.go' \
  | grep -v 'module_base.go' \
  | grep -v 'handler_sources_youtube_helpers.go' \
  | grep -v '_test.go'; then
    echo "FAIL: bare context.Background() found in handlers"
    echo "Use request context from gin.Context.Request.Context() instead."
    echo "See ARCHITECTURE.md §7 for exemptions."
    exit 1
fi
echo "  OK: no forbidden context.Background() in handlers"

# ── Check 2: business logic in handlers ────────────────────────────
echo ""
echo "Check 2: business logic in handlers"
# Handlers must not do direct SQL queries for business data (os/exec for external tools is allowed)
if rg -n 'sql\.Open|db\.QueryRow|db\.ExecContext|db\.QueryContext' internal/api/ --glob '*.go' 2>/dev/null \
  | grep -v '_test.go' \
  | grep -v 'index_health_handler.go'; then
    echo "FAIL: direct database operations found in handlers"
    echo "Handlers must delegate data access to internal/service/ or internal/infrastructure/database/sqlite/."
    exit 1
fi
echo "  OK: no business logic leaks in handlers"

# ── Check 3: pkg/ must not import internal/ ────────────────────────
echo ""
echo "Check 3: pkg/ import purity"
# Known violations (must be migrated out):
#   pkg/handlerutil/job.go → internal/jobs, internal/media/models
#   pkg/media/downloader/ → internal/config, internal/security
#   pkg/media/ffmpeg/ → internal/config
# New violations are forbidden; existing ones must be migrated.
KNOWN_PKG_VIOLATIONS="pkg/handlerutil|pkg/media/downloader|pkg/media/ffmpeg"
# In-line comments (doc and otherwise) frequently mention paths like
# `github.com/Marcuss-ops/PipelineGen/internal/...` for context. Those
# MUST NOT count as import violations. Filter them out by rejecting
# any rg hit whose content starts with whitespace + `//`.
if rg -n '"github.com/Marcuss-ops/PipelineGen/internal/' pkg/ --glob '*.go' 2>/dev/null \
  | grep -vE "$KNOWN_PKG_VIOLATIONS" \
  | grep -vE ':[0-9]+:[[:space:]]*//'; then
    echo "FAIL: new pkg/ imports internal/"
    echo "pkg/ must be leaf-only (zero import from internal/)."
    echo "Known violations to migrate: pkg/handlerutil, pkg/media/downloader, pkg/media/ffmpeg"
    exit 1
fi
echo "  OK: no new pkg/ imports of internal/ (known violations tracked)"

# ── Check 4: core/ must not import service/ or repository/ ─────────
echo ""
echo "Check 4: core/ layer purity"
# Known violations exempted by filename match (migration tracked):
#   internal/core/lifecycle/*
#   internal/core/maintenance/*
#   internal/core/jobs/types.go
# New violations are forbidden.

if rg -n '"github.com/Marcuss-ops/PipelineGen/internal/(service|repository|sources|media)/' internal/core/ --glob '*.go' 2>/dev/null \
  | grep -v '_test.go' \
  | grep -v '[\\/]lifecycle[\\/]' \
  | grep -v '[\\/]maintenance[\\/]' \
  | grep -v '[\\/]jobs[\\/]types.go'; then
    echo "FAIL: new core/ imports service/repository/sources/media"
    echo "core/ must only depend on standard library and pkg/."
    echo "Known violations to migrate: core/jobs, core/lifecycle, core/maintenance"
    exit 1
fi
echo "  OK: no new core/ layer violations (known violations tracked)"

# ── Check 5: directory size limits ─────────────────────────────────
echo ""
echo "Check 5: directory size limits"
# Enforce maximum production .go files per directory.
# See docs/api-package-boundaries.md for rationale and allowlist.
MAX_ROOT=15
MAX_FEATURE=30
MAX_HARD=40

EXIT=0
for dir in $(find internal/api -mindepth 1 -type d 2>/dev/null); do
    count=$(find "$dir" -maxdepth 1 -name '*.go' ! -name '*_test.go' 2>/dev/null | wc -l)
    if [ "$count" -gt "$MAX_HARD" ]; then
        echo "FAIL: $dir contains $count production .go files (hard limit: $MAX_HARD)"
        EXIT=1
    elif [ "$count" -gt "$MAX_FEATURE" ]; then
        echo "WARN: $dir contains $count production .go files (soft limit: $MAX_FEATURE)"
    fi
done

# Check root specifically against tighter limit
root_count=$(find internal/api -maxdepth 1 -name '*.go' ! -name '*_test.go' 2>/dev/null | wc -l)
if [ "$root_count" -gt "$MAX_ROOT" ]; then
    echo "WARN: internal/api/ root contains $root_count production .go files (target: $MAX_ROOT)"
    echo "  Verticalise into internal/api/<feature>/ per docs/api-package-boundaries.md"
    # Soft warning — becomes hard fail when migration completes
fi

if [ "$EXIT" -eq 1 ]; then
    echo ""
    echo "Directory size limits violated. See docs/api-package-boundaries.md."
    exit 1
fi
echo "  OK: all directories within size limits"

# ── Check 6: database/sql import in API layer ────────────────────
echo ""
echo "Check 6: database/sql import in API layer"
# Allowed: middleware_logger.go (logging pipeline), _test.go files.
# health.go was previously exempted; as of PR1 Health boundary (June 2026)
# it is clean — the handler delegates to application/system/health/Service.
echo "  Checking api/ for database/sql ..."
if rg -n '"database/sql"' internal/api/ --glob '*.go' 2>/dev/null \
  | grep -v '_test.go' \
  | grep -v '/middleware/middleware_logger\.go'; then
    echo "FAIL: database/sql imported in API layer"
    echo "Handlers must delegate DB access to internal/infrastructure/database/sqlite/ or internal/service/."
    echo "See docs/api-package-boundaries.md."
    exit 1
fi
echo "  OK: no database/sql in API handlers"

# ── Check 7: os.Getenv in API layer ───────────────────────────────
echo ""
echo "Check 7: os.Getenv in API layer"
# Allowed: routes.go (METRICS_AUTH_TOKEN is router bootstrap),
# server.go (signal.NotifyContext is Go canonical pattern),
# middleware/ (infrastructure wiring),
# handler_script_handlers_flow_scene_images.go (VELOX_SCENE_PARALLELISM —
#   tracked for Worker 2 migration to constructor-based injection),
# _test.go files.
KNOWN_API_GETENV="internal/api/routes.go|internal/api/server.go|internal/api/middleware/|internal/api/script/handler_script_handlers_flow_scene_images\.go"
if rg -n 'os\.Getenv' internal/api/ --glob '*.go' 2>/dev/null \
  | grep -v '_test.go' \
  | grep -vE "$KNOWN_API_GETENV"; then
    echo "FAIL: os.Getenv in API handler code"
    echo "Environment variables must be read in config/bootstrap and passed via constructor."
    exit 1
fi
echo "  OK: no os.Getenv in API handlers"

# ── Check 8: Drive SDK import in API layer ────────────────────────
echo ""
echo "Check 8: Drive SDK import in API layer"
if rg -n '"google\.golang\.org/api/drive/' internal/api/ --glob '*.go' 2>/dev/null \
  | grep -v '_test.go'; then
    echo "FAIL: google.golang.org/api/drive/v3 imported in API layer"
    echo "Handlers must not depend on the Google Drive SDK directly."
    exit 1
fi
echo "  OK: no Drive SDK in API handlers"

# ── Check 9: os/exec in API layer ─────────────────────────────────
echo ""
echo "Check 9: os/exec in API layer"
# Allowed: middleware/ (infrastructure), _test.go files.
if rg -n '"os/exec"' internal/api/ --glob '*.go' 2>/dev/null \
  | grep -v '_test.go' \
  | grep -v 'middleware/'; then
    echo "FAIL: os/exec imported in API layer"
    echo "Handlers must not shell out to external processes directly."
    exit 1
fi
echo "  OK: no os/exec in API handlers"

# ── Check 10: map[string]any in API layer ──────────────────────────
echo ""
echo "Check 10: map[string]any in API layer (tracked migration)"
# Known violations — all tracked for Worker 2 migration:
#   internal/api/script/*          → migration to typed payloads (Block 2B)
#   internal/api/sources/clips/*    → clip enrichment enqueue sites
#   internal/api/sources/youtube/*  → YouTube clip enqueue
#   internal/api/lessons/*          → lesson generation enqueue
#   internal/api/books/*            → book generation enqueue
#   internal/api/images/*           → image generation
#   internal/api/job.go             → job model types
#   internal/api/helpers.go         → json helpers
#   internal/api/realtime/*         → realtime search results
#
# This check TRACKS the count and reports it. It does NOT fail the build.
# When Worker 2 completes typed-payload migration, this becomes a hard fail.
MAP_COUNT=$(rg -c 'map\[string\]any' internal/api/ --glob '*.go' 2>/dev/null \
  | grep -v '_test.go' \
  | grep -v '/middleware/' \
  | grep -v '/common/health\.go' \
  | grep -v 'internal/api/helpers\.go:' \
  | grep -v 'internal/api/job\.go:' \
  | awk -F: '{sum+=$2} END {print sum+0}' || true)
# Visual upgrade (PR1): print current vs allowed vs remaining when allowlist found.
MAP_ALLOWED=0
if [ -f scripts/archcheck/grandfathered_allowlist.json ]; then
  MAP_ALLOWED=$(python3 -c 'import json; a=json.load(open("scripts/archcheck/grandfathered_allowlist.json")); print(a.get("map_string_any_in_api_legacy",0))' 2>/dev/null || echo 0)
fi
echo "  map[string]any occurrences in API (tracked): current=${MAP_COUNT:-0} allowed=${MAP_ALLOWED} remaining=$((MAP_ALLOWED - ${MAP_COUNT:-0}))"
echo "  Migration target: 0 (Worker 2 — typed payloads)."

# ── Check 11: new imports of internal/infrastructure root ─────────
echo ""
echo "Check 11: internal/infrastructure root imports (tracked)"
# The mega-package internal/infrastructure (package platform) is deprecated.
# Files should import the focused sub-packages or pkg/ utilities instead.
# This check tracks the count and reports it. It does NOT fail the build.
# When migration completes, this becomes a hard fail with an allowlist.
ROOT_INFRA_IMPORT=$(rg -l '"github\.com/Marcuss-ops/PipelineGen/internal/infrastructure"' \
  internal/api/ internal/application/ \
  --glob '*.go' 2>/dev/null \
  | grep -v '_test.go' \
  | wc -l || true)
ROOT_INFRA_ALLOWED=0
if [ -f scripts/archcheck/grandfathered_allowlist.json ]; then
  ROOT_INFRA_ALLOWED=$(python3 -c 'import json; a=json.load(open("scripts/archcheck/grandfathered_allowlist.json")); print(a.get("internal_infrastructure_root_imports_legacy",0))' 2>/dev/null || echo 0)
fi
echo "  Files importing internal/infrastructure root (tracked): current=${ROOT_INFRA_IMPORT:-0} allowed=${ROOT_INFRA_ALLOWED} remaining=$((ROOT_INFRA_ALLOWED - ${ROOT_INFRA_IMPORT:-0}))"
echo "  Migration target: 0 (use pkg/ or internal/infrastructure/<sub>/)."

# ── Check 13: transport-layer boundary (tracked) ─────────────────────
echo ""
echo "Check 13: transport-layer boundary (tracked)"
# After commits 2-7 of the API-surface consolidation (see
# docs/CHANGELOG_2026-06-03.md), every consolidated module under
# internal/api/{assets,channels,content,images,jobs,scripts,system}/
# MUST NOT import:
#   - database/sql or mattn/go-sqlite3   → go through repository/services
#   - os/exec                            → go through internal/infrastructure/process
#   - google.golang.org/api/drive/v3     → go through drive.Uploader / DocClient
#
# Handlers in those modules should be one-liners that call
# transport.JSON(c, useCase, errorMapper), with useCase + errorMapper
# injected by the composition root (internal/app/registry.go).
#
# This check TRACKS the count and reports it. It does NOT fail the build.
# When migration completes (commits 2-7 land), this gets promoted to a
# hard fail with a focused allowlist.
TARGET_DIRS="internal/api/assets internal/api/channels internal/api/content internal/api/images internal/api/jobs internal/api/scripts internal/api/system internal/api/transport"
EXISTING=""
for d in $TARGET_DIRS; do
    [ -d "$d" ] && EXISTING="$EXISTING $d" || true
done
if [ -z "$EXISTING" ]; then
    echo "  No consolidated modules yet — check is N/A (will activate after commit 2)."
else
    VIOLATIONS=$(rg -c '"database/sql"|"os/exec"|"mattn/go-sqlite3"|"google\.golang\.org/api/drive/v3"' \
        $EXISTING --glob '*.go' 2>/dev/null \
        | grep -v '_test.go' \
        | awk -F: '{sum+=$2} END {print sum+0}' || true)
    TL_ALLOWED=0
    if [ -f scripts/archcheck/grandfathered_allowlist.json ]; then
      TL_ALLOWED=$(python3 -c 'import json; a=json.load(open("scripts/archcheck/grandfathered_allowlist.json")); print(a.get("transport_layer_boundary_legacy",0))' 2>/dev/null || echo 0)
    fi
    echo "  Import statements violating transport-layer boundary (tracked): current=${VIOLATIONS:-0} allowed=${TL_ALLOWED} remaining=$((TL_ALLOWED - ${VIOLATIONS:-0}))"
    echo "  Migration target: 0."
fi

# ── Check 12: testing import in production files ──────────────────
echo ""
echo "Check 12: testing import in production files"
# The testing package must only appear in _test.go files.
# Known exception: testdb.go is a test helper (NewTestDB) used across packages.
if rg -n '"testing"' --glob '*.go' --glob '!*_test.go' 2>/dev/null \
  | grep -v 'pkg/testutil/' \
  | grep -v 'internal/infrastructure/database/testdb\.go'; then
    echo "FAIL: testing package imported in production file"
    echo "Testing helpers belong in pkg/testutil/ or _test.go files."
    exit 1
fi
echo "  OK: no testing import in production files"

# ── Check 13: no new files in legacy (migration-only) directories ─
echo ""
echo "Check 13: no new production code in legacy directories"
# Legacy directories are migration-only: no new files, no new public types,
# no new repositories, no new handlers. Existing files may only be removed
# or modified in-place during their migration. Tests are allowed (they cover
# the migration surface).
#
# See AGENTS.md Migration Status section for the completed wave history.
# These directories must NEVER reappear — CI fails hard if they exist with .go files.
LEGACY_DIRS=(
    internal/media
    internal/sources
    internal/api/sources
    internal/core
    internal/assets
    internal/artifacts
    internal/contracts
    internal/jobs
    internal/outboxhandlers
    internal/application/scriptflow
    internal/application/association
    internal/application/realtime
    internal/domain/media
    internal/domain/worker
    internal/domain/outbox
    internal/upload
)
# Compute the change set with status (A / R / M / D / C). --
# name-status with find-renames lets git pair renames internally, so the
# status column is authoritative: 'A' rows are TRUE adds (the new path was
# not paired with any deleted blob), 'R(num)' rows are renames (old->new).
#
# Critical: we ALWAYS union untracked files into the STATUS stream (as
# synthetic 'A<TAB>path' rows), independent of whether origin/main exists.
# Without this, a contributor adding a new file inside a legacy dir locally
# and pushing would NOT be flagged until the file is committed -- because
# `git diff origin/main...HEAD` only sees staged/committed changes.
if git rev-parse --verify origin/main >/dev/null 2>&1; then
  DIFF_STATUS=$(git diff --name-status --find-renames origin/main...HEAD 2>/dev/null || true)
else
  DIFF_STATUS=$(git diff --cached --name-status --find-renames 2>/dev/null || true)
fi
UNTRACKED_AS_ADDS=$(git ls-files --others --exclude-standard 2>/dev/null \
                     | awk '{printf "A\t%s\n", $0}' || true)
STATUS="${DIFF_STATUS}
${UNTRACKED_AS_ADDS}"
# Trim a leading blank line that may appear when DIFF_STATUS is empty and
# UNTRACKED_AS_ADDS begins with a newline-producing awk output.
STATUS=$(printf "%s" "${STATUS}" | sed '/^$/d')

VIOLATIONS=""
# Walk the (status, old?, new) rows. A path lands in VIOLATIONS only when:
#   - status = A and new path is in a legacy dir               (new file in legacy)
#   - status = R and new path is in a legacy dir AND old path
#              is NOT in the same legacy dir                   (cross-dir rename INTO legacy)
# _test.go is excluded everywhere.
# Intra-legacy renames (status = R, both old and new in the same legacy
# dir) are allowed migration steps and NOT flagged.
while IFS=$'\t' read -r status old new; do
  [ -z "${status:-}" ] && continue
  case "${status}" in
    A)
      new_path="${old:-}"; new_path="${new}"  # 'A' rows: $2 is new path; $1 is 'A'
      # Note: with --name-status and a single column 'A\t<path>', new_path
      # is actually $2 not $3. Adjust per git version below.
      # (handled implicitly - read the next cases)
      ;;
  esac
done <<< "${STATUS:-}"

# Re-parse using positional reads with care for variable column count.
# git --name-status output:
#   "A<tab>path"
#   "D<tab>path"
#   "M<tab>path"
#   "C<tab>path"
#   "R<num><tab>old<tab>new"
#   "T<tab>path" (type change)  - rare in this repo
#
# Pre-process: build two lists.
#   1) Added paths (status starts with 'A', single additional column)
#   2) Rename new->old pairs (status starts with 'R', two additional columns)
ADDED_LIST=$(printf "%s\n" "${STATUS:-}" | awk -F'\t' '$1 ~ /^A/ {print $2}' || true)
RENAME_PAIRS=$(printf "%s\n" "${STATUS:-}" | awk -F'\t' '$1 ~ /^R/ {print $2 "\t" $3}' || true)

for legacy in "${LEGACY_DIRS[@]}"; do
  hits=$(printf "%s\n" "${ADDED_LIST:-}" \
         | grep -E "^${legacy}/.*\.go$" \
         | grep -v "_test.go$" || true)
  if [ -n "${hits}" ]; then
    VIOLATIONS="${VIOLATIONS}${hits}
"
  fi

  # Cross-dir renames INTO this legacy dir (old path is NOT in the same dir).
  cross_renames=$(printf "%s\n" "${RENAME_PAIRS:-}" \
                  | awk -F'\t' -v leg="${legacy}" \
                      '$1 !~ "^" leg "/.*" && $2 ~ "^" leg "/.*\.go$" \
                          && $2 !~ "_test\.go$" {print $2 "  (renamed from " $1 ")"}' \
                  || true)
  if [ -n "${cross_renames}" ]; then
    VIOLATIONS="${VIOLATIONS}${cross_renames}
"
  fi
done

if [ -n "${VIOLATIONS}" ]; then
    echo "FAIL: new production code in legacy (migration-only) directories:"
    printf "%s" "${VIOLATIONS}" | sed 's/^/  /'
    echo ""
    echo "Legacy directories accept only removals or in-place migrations."
    echo "Place new files in their migration target instead:"
    echo "  internal/core/*                       -> internal/domain/asset/* or internal/infrastructure/<X>/"
    echo "  internal/media/<feature>/*            -> internal/domain/asset/<feature>/  or  internal/application/<feature>/"
    echo "  internal/assets/*                     -> internal/domain/asset/"
    echo "  internal/artifacts/*                  -> internal/domain/job/ (artifacts is interface-wrap; eliminate)"
    echo "  internal/sources/{youtube,artlist}/*  -> internal/application/assets/providers/<X>/"
    echo "  internal/upload/drive/*               -> internal/infrastructure/drive/"
    echo "  internal/application/scriptflow/*     -> internal/application/scripts/<X>/"
    echo "  internal/domain/media/*               -> internal/domain/asset/"
    echo "  internal/domain/worker/*              -> internal/domain/job/"
    echo "  internal/domain/outbox/*              -> internal/domain/lifecycle/"
    echo "See AGENTS.md §Legacy Directories Policy."
    exit 1
fi
echo "  OK: no new files in legacy directories"

# ── Check 14: handler + worker binary link verification ──────────────
echo ""

# ── Check 17: database/sql import gate (June 2026 codex/db-sql-ownership-gate) ───
# Enforces zero NEW violations of the canonical ownership rule:
# ONLY `internal/infrastructure/database/` may import `database/sql`.
# The hand-curated baseline below lists every file in
# internal/{api,application,domain} that grandfathered in a database/sql
# import BEFORE this gate was wired. Regressions above the baseline
# (i.e. any NEW file added to the import list) fail CI with exit 1;
# removals below the baseline are encouraged and the baseline can be
# shrunk by followup migration PRs that port a file off database/sql.
#
# IMPORTANT: per the user's mandate (June 2026), this gate replaces
# the historical `scripts/archcheck/baseline.json` (deleted in
# codex/dir-strict-gates as a "ratchet excuse"). The baseline now
# lives inside this script so it is revision-controlled and visible
# inline — no JSON sidecar.
echo ""
echo "Check 17: database/sql import in api/app/domain (zero NEW violations)"
LEGACY_DB_SQL_FILES=$(
echo "    internal/api/middleware/middleware_auth_test.go
    internal/api/middleware/middleware_logger.go
    internal/application/assets/artifacts/clips_adapter.go
    internal/application/assets/artifacts/finalizer_test.go
    internal/application/assets/artifacts/repository.go
    internal/application/assets/artifacts/resolvers/resolvers.go
    internal/application/assets/ingest/adapter_clip.go
    internal/application/assets/maintenance/deep_cleanup.go
    internal/application/assets/maintenance/run_cleanup.go
    internal/application/assets/maintenance/service.go
    internal/application/assets/monitor/channel_monitor.go
    internal/application/assets/providers/artlist/assetrepo_integration_test.go
    internal/application/assets/providers/artlist/search_cache.go
    internal/application/assets/providers/artlist/service.go
    internal/application/assets/providers/artlist/service_test.go
    internal/application/assets/realtime/index_health_test.go
    internal/application/books/service.go
    internal/application/images/google_generate.go
    internal/application/jobs/outbox/delivery.go
    internal/application/jobs/outbox/metadata_export.go
    internal/application/jobs/outbox/registry.go
    internal/application/jobs/service_test.go
    internal/application/scripts/batch_persistence_test.go
    internal/application/scripts/gemmamemory/stub.go
    internal/application/scripts/gemmamemory/stub_test.go
    internal/application/voiceover/groups_resolver_test.go
    internal/application/voiceover/service.go
    internal/application/youtube/assetrepo_integration_test.go
    internal/application/youtube/cache/service.go
    internal/application/youtube/jobs/rebuild.go
    internal/application/youtube/ports/ports.go
    internal/domain/asset/assets.go
    internal/domain/asset/dedup.go
    internal/domain/asset/list_clips.go
    internal/domain/asset/locations.go
    internal/domain/asset/processing.go
    internal/domain/asset/scan.go
    internal/domain/asset/store_core.go
    internal/domain/asset/tags.go
    internal/domain/asset/utility.go"
)
LEGACY_COUNT=$(echo "$LEGACY_DB_SQL_FILES" | grep -c '^' || true)
if [ "$LEGACY_COUNT" -ne 40 ]; then
    echo "FAIL: Check 17 baseline drift."
    echo "  The in-script baseline lists $LEGACY_COUNT files but the"
    echo "  expected count is 40. Re-run \`rg -ln \\\"database/sql\\\" internal/api internal/application internal/domain --type go | sort\`"
    echo "  and update this list. If the count legitimately changed, also update"
    echo "  the \`expected_count\` match above."
    exit 1
fi
ACTUAL_DB_SQL=$(rg -ln '"database/sql"' internal/api/ internal/application/ internal/domain/ --type go 2>/dev/null | sort)
# Strip leading spaces from baseline before comparison (echo "    file" → "file")
CLEAN_BASELINE=$(printf '%s\n' "$LEGACY_DB_SQL_FILES" | sed 's/^[[:space:]]*//')
ADDED=$(comm -13 <(echo "$CLEAN_BASELINE") <(echo "$ACTUAL_DB_SQL") || true)
if [ -n "$ADDED" ]; then
    echo "FAIL: NEW database/sql imports in api/app/domain (regression above Check 17 baseline):"
    echo "$ADDED" | sed 's/^/  /'
    echo ""
    echo "These files were added to the forbidden-import set since the"
    echo "Check 17 baseline was last refreshed. Either:"
    echo "  - port the file off database/sql (preferred — shrinks the baseline)"
    echo "  - if the import is genuinely necessary, add it to LEGACY_DB_SQL_FILES"
    echo "    AFTER team review (gate stays tight)."
    exit 1
fi
REMOVED=$(comm -23 <(echo "$CLEAN_BASELINE") <(echo "$ACTUAL_DB_SQL") || true)
if [ -n "$REMOVED" ]; then
    REM_COUNT=$(echo "$REMOVED" | grep -c '^' || true)
    echo "  reminder: $LEGACY_COUNT legacy files in baseline, $REM_COUNT candidate-removal(s) detected below baseline:"
    echo "$REMOVED" | sed 's/^/    - /'
    echo "  Consider scrubbing them from LEGACY_DB_SQL_FILES in a followup PR."
fi
echo "  OK: Check 17 baseline ($LEGACY_COUNT files) holds — no regressions"

# ── Check 16: registered-list enforcement for data/*.db*.sqlite (codex/db-set-and-paths, June 2026) ───
echo ""
echo "Check 16: data/ contains only the registered DB files"
# The DatabaseSet contract guarantees exactly 2 production DBs at runtime:
#   * data/media/media.db.sqlite           — primary
#   * data/observability/api_requests.db.sqlite — observability
# Any other *.db or *.sqlite file in data/ is an unregistered spurious file
# (a leftover from a prior schema, a half-rolled-out migration, a runaway
# test fixture, etc.). Catch it before it silently becomes the runtime
# source of truth.
#
# Note: DB files are runtime artifacts — they may not exist in a fresh
# checkout. This check only flags EXTRA (unregistered) files, not missing ones.
REGISTERED_DB_FILES=(
    "data/media/media.db.sqlite"
    "data/observability/api_requests.db.sqlite"
)
ACTUAL=$(find data -type f \( -name '*.db' -o -name '*.sqlite' \) 2>/dev/null | sort)
if [ -n "$ACTUAL" ]; then
    UNREGISTERED=$(comm -23 <(echo "$ACTUAL") <(printf "%s\n" "${REGISTERED_DB_FILES[@]}" | sort) || true)
    if [ -n "$UNREGISTERED" ]; then
        echo "FAIL: unregistered DB files found in data/:"
        echo "$UNREGISTERED" | sed 's/^/  /'
        echo "  Register new DBs in REGISTERED_DB_FILES above and"
        echo "  document the new owner in architecture/ownership.yaml."
        exit 1
    fi
fi
echo "  OK: data/ contains only registered DB files"

echo "Check 14: handler + worker binary link verification"
# PR1 (June 2026): the post-cascade verify-gate found that go vet on
# internal/api/ and go build on cmd/worker were not enforced by the
# local CI script. These commands are now mandatory so future refactors
# are never scoped to fewer than the full transport + worker layer.
#
# The GitHub Actions workflow already runs go vet ./... (which covers
# api/) and go build ./cmd/worker/ (in "Build all binaries"), so this
# check mirrors the CI contract for local pre-push / pre-commit use.

if ! go vet ./internal/api/... 2>&1 | tee /tmp/ci14_vet.out; then
    echo "FAIL: go vet ./internal/api/... failed"
    cat /tmp/ci14_vet.out
    exit 1
fi
# Ignore empty output (vet is silent on success).
if [ -s /tmp/ci14_vet.out ]; then
    echo "FAIL: go vet ./internal/api/... produced output"
    cat /tmp/ci14_vet.out
    exit 1
fi

if ! go build ./cmd/worker/ 2>&1 | tee /tmp/ci14_build.out; then
    echo "FAIL: go build ./cmd/worker/... failed"
    cat /tmp/ci14_build.out
    exit 1
fi
echo "  OK: internal/api/ vets clean and cmd/worker/ builds"

# ── Run legacy asset guard if it exists ────────────────────────────
echo ""
if [[ -x "scripts/ci-legacy-asset-guard.sh" ]]; then
    scripts/ci-legacy-asset-guard.sh
fi

# ── Check 18: absolute directory existence gates ───────────────────
echo ""
echo "Check 18: absolute directory existence gates"
# These directories have been fully eliminated. If any of them reappear
# with .go files, the migration has regressed and CI must fail hard.
# ABSOLUTE_GATE_DIRS — canonical successor mapping
# Each entry below pairs a fully-eliminated legacy directory with its
# canonical migration target. The gate is fail-CLOSED: if any legacy
# directory reappears with .go files, CI fails hard (exit 1).
# Use this map when porting a file back from a canonical successor
# (e.g., during a regression) so the successor path stays the only
# legitimate route forward.
#
#   legacy                              → canonical successor
#   internal/media                      → internal/application/<feature>/ + internal/infrastructure/<X>/
#   internal/sources                    → internal/application/assets/providers/<source>/
#   internal/core                       → internal/domain/<X>/ + internal/infrastructure/<X>/
#   internal/assets                     → internal/domain/asset/ + internal/application/assets/
#   internal/artifacts                  → internal/domain/job/ (artifacts is interface-wrap; eliminate)
#   internal/contracts                  → internal/domain/<X>/
#   internal/jobs (legacy)              → internal/application/jobs/
#   internal/outboxhandlers             → internal/application/jobs/outbox/
#   internal/application/scriptflow     → internal/application/scripts/<X>/
#   internal/application/association    → internal/application/assets/association/
#   internal/application/realtime       → internal/application/assets/realtime/
#   internal/domain/media               → internal/domain/asset/
#   internal/domain/worker              → internal/domain/job/
#   internal/domain/outbox              → internal/domain/lifecycle/
#   internal/upload                     → internal/infrastructure/drive/ + internal/infrastructure/media/processor/
ABSOLUTE_GATE_DIRS=(
    internal/media
    internal/sources
    internal/core
    internal/assets
    internal/artifacts
    internal/contracts
    internal/jobs
    internal/outboxhandlers
    internal/application/scriptflow
    internal/application/association
    internal/application/realtime
    internal/domain/media
    internal/domain/worker
    internal/domain/outbox
    internal/upload
)
for dir in "${ABSOLUTE_GATE_DIRS[@]}"; do
    if [ -d "$dir" ]; then
        go_count=$(find "$dir" -name '*.go' 2>/dev/null | wc -l)
        if [ "$go_count" -gt 0 ]; then
            echo "FAIL: eliminated directory $dir has reappeared with $go_count .go files"
            echo "This directory was fully removed during architecture cleanup."
            echo "New code must go to the canonical migration target."
            exit 1
        fi
    fi || true
done
echo "  OK: no eliminated directories have reappeared"

# ── Check 19: forbidden infrastructure imports in API layer (HARD-FAIL, evidence-based, ci/archcheck-hard-fail) ──
echo ""
echo "Check 19: forbidden infrastructure imports in API layer (HARD-FAIL)"
# POLICY (commit ci/archcheck-hard-fail, June 2026):
#   * Promoted from soft-log (PR1 baseline) to HARD-FAIL evidence-based.
#   * Detection: every `.go` file under internal/api/ (excluding _test.go)
#     whose contents match the canonical infrastructure-import path
#     `github.com/Marcuss-ops/PipelineGen/internal/infrastructure/`.
#     grep -rln returns the LIST OF FILES, not individual lines.
#   * Allowlist: explicit per-file exemptions in
#     `docs/migrations/api-infrastructure-imports-allowlist.txt` (paths
#     RELATIVE TO REPO ROOT). Submission here REQUIRES a documented
#     migration plan in docs/architecture/LEGACY_DIRECTORIES.md.
#   * Fail-closed: any file in `ACTUAL \ ALLOWLIST` causes `exit 1`.
#
# Migration target: ALLOWLIST remains empty (zero allowed exemptions).
# Removing an exemption is always safe; the gate will fail if the
# underlying import is still present.
ACTUAL=$(grep -rln 'github.com/Marcuss-ops/PipelineGen/internal/infrastructure/' internal/api/ --include='*.go' 2>/dev/null | grep -v '_test.go' | sort -u || true)
# Note on the grep pattern: we deliberately use the `github.com/` prefix
# to match ONLY real Go import lines. The user's spec-quoted pattern
# (without the prefix) catches 28 false positives — docstrings, path
# mentions, and comments narrating migrations — none of which are
# actual imports. The tightened pattern matches AGENTS.md convention
# and the legacy Check 19 regex (which used `github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database`
# etc. as full quoted paths). See scripts/archcheck/main.go package
# doc-comment for the cross-reference to this design choice.
ALLOWLIST=$(grep -v '^#' docs/migrations/api-infrastructure-imports-allowlist.txt 2>/dev/null | sort -u || true)
NEW_VIOLATIONS=$(comm -13 <(echo "$ALLOWLIST") <(echo "$ACTUAL") || true)
if [ -n "$NEW_VIOLATIONS" ]; then
    VIOL_COUNT=$(echo "$NEW_VIOLATIONS" | grep -c '^' || true)
    echo "FAIL: forbidden infrastructure imports detected in API layer (post-allowlist):"
    echo "$NEW_VIOLATIONS" | sed 's/^/  /'
    echo ""
    echo "Per commit ci/archcheck-hard-fail, this is now a HARD-FAIL gate."
    echo "Either remove the import (preferred — port the handler to use a"
    echo "service in internal/application/ or external pkg/ utility) or add"
    echo "the file path to docs/migrations/api-infrastructure-imports-allowlist.txt"
    echo "AFTER team review (each grand-parented import must ship with a"
    echo "migration plan in docs/architecture/LEGACY_DIRECTORIES.md)."
    exit 1
fi
echo "  OK: no forbidden infrastructure imports in API handlers (allowlist empty or satisfied)"

# ── Check 20: docs/api/ACTIVE_API_GENERATED.md tracking (PR1) ──
echo ""
echo "Check 20: docs/api/ACTIVE_API_GENERATED.md tracking"
# Per PR1 (Repository truth, June 2026): the canonical API doc is generated
# via `go run ./cmd/admin gen-api-docs <path>` (.github/workflows/ci.yml
# step "Generate API docs" — ordered BEFORE this bash check). The gate
# for "is the file current?" lives in ci.yml's "Verify generated docs"
# step which diffs the freshly-generated file against the committed one.
#
# This bash check performs a PARITY assertion for local CI / pre-commit
# runs (mirrors what ci.yml does, but works without running the generator
# in-process). It tolerates a missing file with a soft log so a checkout
# that generated the doc in a separate pre-step still passes here.
GEN_DOC="docs/api/ACTIVE_API_GENERATED.md"
if [ ! -f "$GEN_DOC" ]; then
    echo "  INFO: $GEN_DOC not present in working tree."
    echo "        This script runs BEFORE the generator step in CI; the actual"
    echo "        gate is .github/workflows/ci.yml::Verify generated docs."
    echo "        Local: run \`go run ./cmd/admin gen-api-docs $GEN_DOC\`."
else
    # Is git tracking the file?
    if ! git ls-files --error-unmatch "$GEN_DOC" >/dev/null 2>&1; then
        echo "FAIL: $GEN_DOC exists in the working tree but is NOT git-tracked."
        echo "  Add it to the index: git add $GEN_DOC && git commit"
        exit 1
    fi
    # Has the working tree drift? (i.e., someone regenerated without committing)
    if [ -n "$(git status --porcelain "$GEN_DOC" 2>/dev/null)" ]; then
        echo "FAIL: $GEN_DOC working tree is dirty (regenerated without committing)."
        echo "  Run: go run ./cmd/admin gen-api-docs $GEN_DOC && git add $GEN_DOC && git commit"
        exit 1
    fi
    echo "  OK: $GEN_DOC present, git-tracked, and in sync"
fi

# ── Check 21: migration.yaml verified_zero policy (HARD-FAIL, evidence-based, ci/archcheck-hard-fail) ────
echo ""
echo "Check 21: migration.yaml verified_zero policy (HARD-FAIL, evidence-based)"
# POLICY (commit ci/archcheck-hard-fail, June 2026):
#   * Promoted from yq/python-regex fallback to evidence-based.
#   * Single source of truth: `go run ./scripts/archcheck` produces a
#     JSON snapshot with `verified_zero: boolean`. We assert via `jq -e`
#     so the JSON shape (NOT the exit code) is the contract.
#   * Fail-closed: if the script cannot run, or jq fails to parse, or
#     `verified_zero != true`, this check exits 1.
#   * Legacy parser (yq OR python heredoc) REMOVED in this commit. The
#     JSON contract is the single point of truth; new policies added in
#     scripts/archcheck automatically extend the gate without CI edits.
ARCHCHECK_STDERR=$(mktemp)
ARCHCHECK_OUT=$(go run ./scripts/archcheck 2>"$ARCHCHECK_STDERR" || true)
if [ -z "$ARCHCHECK_OUT" ]; then
    echo "FAIL: scripts/archcheck produced no output"
    echo "--- archcheck stderr ---"
    cat "$ARCHCHECK_STDERR"
    rm -f "$ARCHCHECK_STDERR"
    echo ""
    echo "The archcheck binary failed to run. Diagnose locally with:"
    echo "  go build ./scripts/archcheck && ./archcheck"
    exit 1
fi
rm -f "$ARCHCHECK_STDERR"
if ! echo "$ARCHCHECK_OUT" | jq -e '.verified_zero == true' >/dev/null 2>&1; then
    echo "FAIL: archcheck evidence check failed"
    echo "$ARCHCHECK_OUT" | jq . 2>/dev/null || echo "$ARCHCHECK_OUT"
    echo ""
    echo "Per the verified-zero policy (migration.yaml header), any wave with"
    echo "\`status: done\` requires evidence from ./scripts/archcheck that"
    echo "the gate holds. Either fix the violation OR downgrade the affected wave"
    echo "to \`status: in_progress\` until the gate can pass."
    exit 1
fi
echo "  OK: archcheck evidence holds (verified_zero: true)"
echo "$ARCHCHECK_OUT" | jq -c '{mode, commit, checks, violations_count: (.violations | length)}'

echo ""
echo "=== All architectural checks passed ==="

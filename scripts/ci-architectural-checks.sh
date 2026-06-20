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
  | grep -v 'health.go' \
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
# Allowed: health.go (deep health probe), middleware_logger.go (logging pipeline),
# _test.go files. Everything else must route through repository/services.
if rg -n '"database/sql"' internal/api/ --glob '*.go' 2>/dev/null \
  | grep -v '_test.go' \
  | grep -v '/common/health\.go' \
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
KNOWN_API_GETENV="internal/api/routes.go|internal/api/server.go|internal/api/middleware/|internal/api/script/handler_script_handlers_flow_scene_images\\.go"
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
echo "  map[string]any occurrences in API (tracked): ${MAP_COUNT:-0}"
echo "  Migration target: 0 (Worker 2 — typed payloads)."

# ── Check 11: new imports of internal/infrastructure root ─────────
echo ""
echo "Check 11: internal/infrastructure root imports (tracked)"
# The mega-package internal/infrastructure (package platform) is deprecated.
# Files should import the focused sub-packages or pkg/ utilities instead.
# This check tracks the count and reports it. It does NOT fail the build.
# When migration completes, this becomes a hard fail with an allowlist.
ROOT_INFRA_IMPORT=$(rg -l '"github\.com/Marcuss-ops/PipelineGen/internal/infrastructure"' \
  internal/api/ internal/media/ internal/scripts/ \
  --glob '*.go' 2>/dev/null \
  | grep -v '_test.go' \
  | wc -l || true)
echo "  Files importing internal/infrastructure root (tracked): ${ROOT_INFRA_IMPORT:-0}"
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
    [ -d "$d" ] && EXISTING="$EXISTING $d"
done
if [ -z "$EXISTING" ]; then
    echo "  No consolidated modules yet — check is N/A (will activate after commit 2)."
else
    VIOLATIONS=$(rg -c '"database/sql"|"os/exec"|"mattn/go-sqlite3"|"google\.golang\.org/api/drive/v3"' \
        $EXISTING --glob '*.go' 2>/dev/null \
        | grep -v '_test.go' \
        | awk -F: '{sum+=$2} END {print sum+0}')
    echo "  Import statements violating transport-layer boundary (tracked): ${VIOLATIONS:-0}"
    echo "  Migration target: 0."
fi

# ── Check 12: testing import in production files ──────────────────
echo ""
echo "Check 12: testing import in production files"
# The testing package must only appear in _test.go files.
# All known violations have been migrated.
if rg -n '"testing"' --glob '*.go' --glob '!*_test.go' 2>/dev/null \
  | grep -v 'pkg/testutil/'; then
    echo "FAIL: testing package imported in production file"
    echo "Testing helpers belong in pkg/testutil/ or _test.go files."
    exit 1
fi
echo "  OK: no testing import in production files"

# ── Run legacy asset guard if it exists ────────────────────────────
echo ""
if [[ -x "scripts/ci-legacy-asset-guard.sh" ]]; then
    scripts/ci-legacy-asset-guard.sh
fi

echo ""
echo "=== All architectural checks passed ==="

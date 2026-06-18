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
#   internal/api/handlers/script/handlers/postwrite.go
#   internal/service/gemmamemory/service.go
#   internal/service/scriptcore/write_script.go
#   internal/jobs/worker.go (finalizationCtx)
#   internal/app/init_core.go
#   internal/api/server.go (signal.NotifyContext)
#   internal/service/translations/cache.go
#   internal/sources/artlist/search_cache.go
if rg -n 'context\.Background\(\)' internal/api/handlers/ --glob '*.go' 2>/dev/null \
  | grep -v 'postwrite.go' \
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
if rg -n 'sql\.Open|db\.QueryRow|db\.ExecContext|db\.QueryContext' internal/api/handlers/ --glob '*.go' 2>/dev/null \
  | grep -v '_test.go' \
  | grep -v 'health.go' \
  | grep -v 'index_health_handler.go'; then
    echo "FAIL: direct database operations found in handlers"
    echo "Handlers must delegate data access to internal/service/ or internal/repository/."
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
if rg -n '"github.com/Marcuss-ops/PipelineGen/internal/' pkg/ --glob '*.go' 2>/dev/null \
  | grep -vE "$KNOWN_PKG_VIOLATIONS"; then
    echo "FAIL: new pkg/ imports internal/"
    echo "pkg/ must be leaf-only (zero import from internal/)."
    echo "Known violations to migrate: pkg/handlerutil, pkg/media/downloader, pkg/media/ffmpeg"
    exit 1
fi
echo "  OK: no new pkg/ imports of internal/ (known violations tracked)"

# ── Check 4: core/ must not import service/ or repository/ ─────────
echo ""
echo "Check 4: core/ layer purity"
if rg -n '"github.com/Marcuss-ops/PipelineGen/internal/(service|repository|sources|media)/' internal/core/ --glob '*.go' 2>/dev/null \
  | grep -vE 'service_test\.go|_test\.go' \
  | grep -vE 'internal/core/jobs/types\.go|internal/core/lifecycle/|internal/core/maintenance/'; then
    echo "FAIL: new core/ imports service/repository/sources/media"
    echo "core/ must only depend on standard library and pkg/."
    echo "Known violations to migrate: core/jobs, core/lifecycle, core/maintenance"
    exit 1
fi
echo "  OK: no new core/ layer violations (known violations tracked)"

# ── Run legacy asset guard if it exists ────────────────────────────
echo ""
if [[ -x "scripts/ci-legacy-asset-guard.sh" ]]; then
    scripts/ci-legacy-asset-guard.sh
fi

echo ""
echo "=== All architectural checks passed ==="

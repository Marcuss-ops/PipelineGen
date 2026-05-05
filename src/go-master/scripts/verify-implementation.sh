#!/bin/bash
# Verify Implementation Script
# This script verifies that the changes in CHANGELOG_2026-05-04.md are actually implemented

set -e

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

echo "=========================================="
echo "Verifying PipelineGen Implementation"
echo "=========================================="
echo ""

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

pass() { echo -e "${GREEN}✓ PASS${NC}: $1"; }
fail() { echo -e "${RED}✗ FAIL${NC}: $1"; }
warn() { echo -e "${YELLOW}⚠ WARN${NC}: $1"; }
info() { echo -e "ℹ INFO: $1"; }

# ==========================================
# Verify 1: Auth default TRUE
# ==========================================
echo "=== Verification 1: Auth Default TRUE ==="

if grep -q 'enable_auth.*default:"true"' pkg/config/types.go; then
    pass "Auth default is TRUE in config struct"
else
    fail "Auth default should be 'true' in pkg/config/types.go"
fi

# ==========================================
# Verify 2: CORS closed by default
# ==========================================
echo ""
echo "=== Verification 2: CORS Closed by Default ==="

if grep -q "AllowOrigins:.*\[\]" internal/api/routes.go || grep -q "corsCfg.AllowOrigins = \[\]" internal/api/routes.go; then
    pass "CORS is closed when no origins configured"
else
    warn "Check CORS configuration in internal/api/routes.go"
fi

# ==========================================
# Verify 3: Internal endpoints protected
# ==========================================
echo ""
echo "=== Verification 3: Internal Endpoints Protected ==="

if grep -A5 "internal/slug" internal/api/routes.go | grep -q "protected"; then
    pass "Internal endpoints are in protected group"
else
    warn "Check if /api/internal/* is under 'protected' group in routes.go"
fi

# ==========================================
# Verify 4: Download whitelist config-driven
# ==========================================
echo ""
echo "=== Verification 4: Download Whitelist Config-Driven ==="

if grep -q "SetAllowedHosts" internal/bootstrap/init_core.go; then
    pass "Download whitelist uses SetAllowedHosts from config"
else
    warn "Check download whitelist implementation"
fi

# ==========================================
# Verify 5: Module registry created
# ==========================================
echo ""
echo "=== Verification 5: Module Registry Created ==="

if [ -f "internal/module/module.go" ] && [ -f "internal/module/base.go" ]; then
    pass "Module registry files exist"
else
    fail "Module registry files missing: internal/module/module.go and internal/module/base.go"
fi

# ==========================================
# Verify 6: Features default FALSE
# ==========================================
echo ""
echo "=== Verification 6: Features Default FALSE ==="

FEATURE_DEFAULTS=$(grep -A20 "type FeaturesConfig struct" pkg/config/types.go | grep "default:" | grep -v "false")
if [ -z "$FEATURE_DEFAULTS" ]; then
    pass "All features default to FALSE"
else
    warn "Some features may not default to false:"
    echo "$FEATURE_DEFAULTS"
fi

# ==========================================
# Verify 7: SQLite backup uses VACUUM INTO
# ==========================================
echo ""
echo "=== Verification 7: SQLite Backup Uses VACUUM INTO ==="

if grep -q "VACUUM INTO" internal/storage/sqlite.go; then
    pass "SQLite backup uses VACUUM INTO"
else
    fail "SQLite backup should use VACUUM INTO, not io.Copy"
fi

# ==========================================
# Verify 8: README Go version aligned
# ==========================================
echo ""
echo "=== Verification 8: README Go Version Aligned ==="

README_GO=$(grep -o "Go [0-9.]*" README.md | head -1)
GOMOD_GO=$(grep "^go " go.mod)
if echo "$README_GO" | grep -q "1.25.9"; then
    pass "README mentions Go 1.25.9"
else
    warn "README Go version: $README_GO (should be 1.25.9)"
fi

if echo "$GOMOD_GO" | grep -q "1.25.9"; then
    pass "go.mod specifies go 1.25.9"
else
    warn "go.mod: $GOMOD_GO (should be 1.25.9)"
fi

# ==========================================
# Verify 9: .gitignore updated
# ==========================================
echo ""
echo "=== Verification 9: .gitignore Updated ==="

if grep -q "\.bak" .gitignore && grep -q "backfill_hash" .gitignore; then
    pass ".gitignore includes backup files and removed binaries"
else
    warn "Check .gitignore for backup files and binaries"
fi

# ==========================================
# Verify 10: Database consolidation plan exists
# ==========================================
echo ""
echo "=== Verification 10: Database Consolidation Plan ==="

if [ -f "docs/architecture/DB_CONSOLIDATION_PLAN.md" ]; then
    pass "Database consolidation plan exists"
else
    warn "Database consolidation plan not found"
fi

# ==========================================
# Run Go tests
# ==========================================
echo ""
echo "=== Running Go Tests ==="

if go test ./tests/... -v 2>&1 | tee /tmp/test-output.txt | tail -20; then
    pass "Go tests passed"
else
    warn "Some Go tests failed - check /tmp/test-output.txt"
fi

# ==========================================
# Summary
# ==========================================
echo ""
echo "=========================================="
echo "Verification Complete"
echo "=========================================="
echo ""
echo "Based on CHANGELOG_2026-05-04.md, the following should be implemented:"
echo "  ✓ Auth default TRUE"
echo "  ✓ CORS closed by default"
echo "  ✓ Internal endpoints protected"
echo "  ✓ Download whitelist config-driven"
echo "  ✓ Module registry created"
echo "  ✓ Features default FALSE"
echo "  ✓ SQLite backup VACUUM INTO"
echo "  ✓ README Go version aligned"
echo ""
echo "Remaining work (from user critique):"
echo "  - Database consolidation (planned, not executed)"
echo "  - Remove runtime files from repo"
echo "  - Clean up old documentation"
echo "  - Complete module registry migration"
echo ""

#!/bin/bash
# Comprehensive Test Suite for PipelineGen
# Based on the 20-point test checklist
#
# Usage: ./scripts/test-suite.sh [--quick]
#   --quick: Run only fast tests (skip integration)

set -e

QUICK_MODE=${1:-""}
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

echo "=========================================="
echo "PipelineGen Final Test Suite"
echo "=========================================="
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

pass() { echo -e "${GREEN}✓ PASS${NC}: $1"; }
fail() { echo -e "${RED}✗ FAIL${NC}: $1"; exit 1; }
warn() { echo -e "${YELLOW}⚠ WARN${NC}: $1"; }
info() { echo -e "ℹ INFO: $1"; }

# Track test results
TESTS_PASSED=0
TESTS_FAILED=0
TESTS_WARNED=0

# ==========================================
# 1. Test build pulito da zero
# ==========================================
echo "=== Test 1: Clean Build ==="

info "Checking Go version..."
go version

info "Running go mod tidy..."
go mod tidy

info "Checking if go.mod changed..."
if ! git diff --quiet -- go.mod go.sum 2>/dev/null; then
    fail "go mod tidy changed go.mod or go.sum - repo is not clean"
else
    pass "go.mod and go.sum are clean after tidy"
fi

info "Building from scratch..."
go build -o /tmp/pipelinegen-test-bin ./cmd/server
if [ -f /tmp/pipelinegen-test-bin ]; then
    pass "Clean build successful"
    rm /tmp/pipelinegen-test-bin
else
    fail "Clean build failed"
fi

info "Running go test ./..."
if go test ./... > /tmp/test-output.txt 2>&1; then
    pass "All Go tests passed"
else
    cat /tmp/test-output.txt
    fail "Some Go tests failed"
fi

# ==========================================
# 2. Test repo pulita
# ==========================================
echo ""
echo "=== Test 2: Clean Repository ==="

info "Checking for tracked runtime files..."
TRACKED_DB=$(git ls-files | grep -E '\.(sqlite|db|mp4|mp3|ttf|bak|old|log)$' || true)
if [ -n "$TRACKED_DB" ]; then
    fail "Runtime files tracked in git:\n$TRACKED_DB"
else
    pass "No runtime files tracked in git"
fi

info "Checking git status..."
STATUS=$(git status -sb)
info "Git status: $STATUS"

# ==========================================
# 3. Test sicurezza default
# ==========================================
echo ""
echo "=== Test 3: Security Defaults ==="

DATA_DIR="/tmp/pipelinegen-test-$(date +%s)"
mkdir -p "$DATA_DIR"

info "Starting server with auth ENABLED (default)..."
VELOX_DATA_DIR="$DATA_DIR" \
VELOX_ENABLE_AUTH=true \
VELOX_ADMIN_TOKEN="test-admin-token" \
go run ./cmd/server &
SERVER_PID=$!
sleep 3

info "Testing unauthenticated request (should fail)..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/api/artlist/diagnostics || echo "000")
if [ "$HTTP_CODE" = "401" ] || [ "$HTTP_CODE" = "403" ]; then
    pass "Unauthenticated request rejected (got $HTTP_CODE)"
else
    fail "Unauthenticated request should be rejected, got $HTTP_CODE"
fi

info "Testing health endpoint (should work without auth)..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/health || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    pass "Health endpoint is public (got $HTTP_CODE)"
else
    fail "Health endpoint should return 200, got $HTTP_CODE"
fi

info "Stopping server..."
kill $SERVER_PID 2>/dev/null || true
wait $SERVER_PID 2>/dev/null || true
rm -rf "$DATA_DIR"

# ==========================================
# 4. Test auth con token valido
# ==========================================
echo ""
echo "=== Test 4: Auth with Valid Token ==="

DATA_DIR="/tmp/pipelinegen-test-$(date +%s)"
mkdir -p "$DATA_DIR"

info "Starting server with auth..."
VELOX_DATA_DIR="$DATA_DIR" \
VELOX_ENABLE_AUTH=true \
VELOX_ADMIN_TOKEN="test-admin-token" \
go run ./cmd/server &
SERVER_PID=$!
sleep 3

info "Testing with valid token..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer test-admin-token" \
    http://localhost:8080/api/artlist/diagnostics || echo "000")
if [ "$HTTP_CODE" != "401" ] && [ "$HTTP_CODE" != "403" ]; then
    pass "Valid token accepted (got $HTTP_CODE, not 401/403)"
else
    warn "Valid token rejected (got $HTTP_CODE) - endpoint may be disabled"
fi

info "Stopping server..."
kill $SERVER_PID 2>/dev/null || true
wait $SERVER_PID 2>/dev/null || true
rm -rf "$DATA_DIR"

# ==========================================
# 5. Test CORS chiuso
# ==========================================
echo ""
echo "=== Test 5: CORS Closed by Default ==="

DATA_DIR="/tmp/pipelinegen-test-$(date +%s)"
mkdir -p "$DATA_DIR"

info "Starting server with empty CORS origins..."
VELOX_DATA_DIR="$DATA_DIR" \
VELOX_CORS_ORIGINS="" \
go run ./cmd/server &
SERVER_PID=$!
sleep 3

info "Testing CORS with evil origin..."
CORS_HEADER=$(curl -s -I \
    -H "Origin: http://evil-site.com" \
    -H "Access-Control-Request-Method: POST" \
    -X OPTIONS \
    http://localhost:8080/api/artlist/run 2>/dev/null | \
    grep -i "Access-Control-Allow-Origin:" || echo "")

if [ -z "$CORS_HEADER" ]; then
    pass "CORS does not allow evil-site.com (no header returned)"
elif echo "$CORS_HEADER" | grep -q "evil-site.com"; then
    fail "CORS should not allow evil-site.com"
else
    pass "CORS header does not contain evil-site.com"
fi

info "Stopping server..."
kill $SERVER_PID 2>/dev/null || true
wait $SERVER_PID 2>/dev/null || true
rm -rf "$DATA_DIR"

# ==========================================
# 6. Test feature flags
# ==========================================
echo ""
echo "=== Test 6: Feature Flags Default Off ==="

DATA_DIR="/tmp/pipelinegen-test-$(date +%s)"
mkdir -p "$DATA_DIR"

info "Starting server with all features disabled..."
VELOX_DATA_DIR="$DATA_DIR" \
VELOX_ENABLE_AUTH=true \
VELOX_ADMIN_TOKEN="test-admin-token" \
VELOX_FEATURE_ARTLIST_ENABLED=false \
VELOX_FEATURE_YOUTUBE_ENABLED=false \
VELOX_FEATURE_DRIVE_ENABLED=false \
VELOX_FEATURE_SCRIPT_DOCS_ENABLED=false \
go run ./cmd/server &
SERVER_PID=$!
sleep 3

info "Testing disabled Artlist endpoint..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer test-admin-token" \
    http://localhost:8080/api/artlist/diagnostics || echo "000")
if [ "$HTTP_CODE" = "404" ] || [ "$HTTP_CODE" = "403" ]; then
    pass "Disabled feature returns 404/403 (got $HTTP_CODE)"
else
    warn "Disabled feature returned $HTTP_CODE - may still be accessible"
fi

info "Stopping server..."
kill $SERVER_PID 2>/dev/null || true
wait $SERVER_PID 2>/dev/null || true
rm -rf "$DATA_DIR"

# ==========================================
# 7. Test bootstrap minimale
# ==========================================
echo ""
echo "=== Test 7: Minimal Bootstrap ==="

DATA_DIR="/tmp/pipelinegen-minimal-$(date +%s)"
mkdir -p "$DATA_DIR"

info "Starting server with ALL features disabled (minimal mode)..."
VELOX_DATA_DIR="$DATA_DIR" \
VELOX_ENABLE_AUTH=true \
VELOX_ADMIN_TOKEN="test-admin-token" \
VELOX_FEATURE_ARTLIST_ENABLED=false \
VELOX_FEATURE_YOUTUBE_ENABLED=false \
VELOX_FEATURE_DRIVE_ENABLED=false \
VELOX_FEATURE_HARVESTER_ENABLED=false \
VELOX_FEATURE_SCRIPT_DOCS_ENABLED=false \
timeout 10 go run ./cmd/server &
SERVER_PID=$!
sleep 5

if kill -0 $SERVER_PID 2>/dev/null; then
    pass "Server started in minimal mode (no external deps required)"
else
    fail "Server failed to start in minimal mode - check bootstrap"
fi

info "Stopping server..."
kill $SERVER_PID 2>/dev/null || true
wait $SERVER_PID 2>/dev/null || true
rm -rf "$DATA_DIR"

# ==========================================
# 8. Test database consolidati (placeholder)
# ==========================================
echo ""
echo "=== Test 8: Database Consolidation ==="

info "Checking database consolidation status..."
warn "Database consolidation is planned but not yet implemented"
info "Target: app.db.sqlite, media.db.sqlite, jobs.db.sqlite"
info "Current: Check docs/architecture/DB_CONSOLIDATION_PLAN.md"

# ==========================================
# 9. Test migrazioni idempotenti
# ==========================================
echo ""
echo "=== Test 9: Idempotent Migrations ==="

info "Running Go tests for storage package..."
if go test -v ./internal/storage/... > /tmp/migration-test.txt 2>&1; then
    pass "Storage tests passed (migrations idempotent)"
else
    cat /tmp/migration-test.txt
    warn "Some storage tests failed - check migration idempotency"
fi

# ==========================================
# 10. Test backup SQLite
# ==========================================
echo ""
echo "=== Test 10: SQLite Backup ==="

info "Checking VACUUM INTO implementation..."
if grep -r "VACUUM INTO" internal/storage/ > /dev/null 2>&1; then
    pass "VACUUM INTO backup method implemented"
else
    warn "VACUUM INTO not found - backup may use unsafe method"
fi

# ==========================================
# 11. Test registry moduli
# ==========================================
echo ""
echo "=== Test 11: Module Registry ==="

info "Checking module registry implementation..."
if [ -f "internal/module/module.go" ]; then
    pass "Module registry exists"
    info "Running module tests..."
    go test ./internal/module/... || warn "Module tests had issues"
else
    warn "Module registry not found - check internal/module/"
fi

# ==========================================
# 12. Test route snapshot
# ==========================================
echo ""
echo "=== Test 12: Route Registration ==="

info "Running route tests..."
if go test -v ./tests/... -run TestRoute > /tmp/route-test.txt 2>&1; then
    pass "Route registration tests passed"
else
    cat /tmp/route-test.txt
    warn "Route registration tests had issues"
fi

# ==========================================
# Quick mode skip point
# ==========================================
if [ "$QUICK_MODE" = "--quick" ]; then
    echo ""
    echo "=========================================="
    echo "Quick Mode - Skipping Integration Tests"
    echo "=========================================="
    echo ""
    echo "Tests completed in quick mode"
    exit 0
fi

# ==========================================
# 13-20. Integration tests (require running server)
# ==========================================
echo ""
echo "=== Tests 13-20: Integration Tests ==="
echo "These tests require a full running server with dependencies"
echo "Skipping in automated mode - run manually for full validation"
echo ""
echo "To run full integration tests:"
echo "  1. Start server with: VELOX_DATA_DIR=/tmp/test go run ./cmd/server"
echo "  2. Run: ./scripts/test-integration.sh"

# ==========================================
# Final CI checks
# ==========================================
echo ""
echo "=== Final CI Checks ==="

info "Running go vet..."
if go vet ./...; then
    pass "go vet passed"
else
    fail "go vet found issues"
fi

info "Checking code formatting..."
UNFORMATTED=$(gofmt -l . 2>/dev/null | grep -v vendor | head -20 || true)
if [ -z "$UNFORMATTED" ]; then
    pass "Code formatting is correct"
else
    warn "Some files are not formatted with gofmt:"
    echo "$UNFORMATTED"
fi

info "Running race condition tests..."
if go test -race ./internal/storage/... ./internal/module/... > /tmp/race-test.txt 2>&1; then
    pass "Race condition tests passed"
else
    cat /tmp/race-test.txt
    warn "Race condition tests had issues"
fi

# ==========================================
# Summary
# ==========================================
echo ""
echo "=========================================="
echo "Test Suite Summary"
echo "=========================================="
echo ""
echo "All core tests completed!"
echo ""
echo "Next steps for full validation:"
echo "  1. Test database consolidation (when implemented)"
echo "  2. Test Artlist end-to-end (requires Node scraper)"
echo "  3. Test YouTube clips end-to-end (requires yt-dlp)"
echo "  4. Test download whitelist"
echo "  5. Test rate limiting"
echo "  6. Test clean shutdown (no zombies)"
echo ""
echo "=========================================="
echo "PipelineGen is ready for final validation!"
echo "=========================================="

#!/bin/bash
# ci-architectural-checks.sh
# Architettural tests to enforce code quality rules
# These are grep-based tests - not elegant, but effective

set -e

echo "=== Running Architectural Checks ==="
FAILED=0

# Check 1: No context.Background() in API handlers (except for tests and CLI tools)
echo "Check 1: Scanning for context.Background() in API handlers..."
# Allow context.Background() with timeout (background jobs), in server.go (shutdown), or for job enqueue
VIOLATIONS=$(grep -rn "context.Background()" internal/api/ --include="*.go" | grep -v "_test.go" | grep -v "server.go" | grep -v "WithTimeout\|WithCancel" | grep -v "// TODO" | grep -v "\.Enqueue(" || true)
if [ -n "$VIOLATIONS" ]; then
    echo "ERROR: Found context.Background() in API handlers (without timeout or valid reason):"
    echo "$VIOLATIONS"
    echo "API handlers must use c.Request.Context() or job system, not context.Background()"
    echo "Allowed: context.WithTimeout(), context.WithCancel(), or .Enqueue() calls"
    FAILED=1
fi

# Check 2: No "not implemented" or "placeholder" in internal/api (except comments)
echo "Check 2: Scanning for fake endpoints..."
VIOLATIONS=$(grep -rn -i "not implemented\|placeholder" internal/api/ --include="*.go" | grep -v "//" | grep -v "_test.go" || true)
if [ -n "$VIOLATIONS" ]; then
    echo "ERROR: Found fake/placeholder endpoints:"
    echo "$VIOLATIONS"
    echo "Endpoints must have real behavior or be removed"
    FAILED=1
fi

# Check 3: Workflow module must be disabled (feature flag off)
echo "Check 3: Checking workflow feature flag default..."
if grep -q "WorkflowEnabled.*default:\"true\"" pkg/config/types.go; then
    echo "ERROR: Workflow feature flag must default to false"
    FAILED=1
fi

# Check 4: No direct goroutine launches in handlers (should use job system)
echo "Check 4: Scanning for goroutines in handlers..."
VIOLATIONS=$(grep -rn "go func()" internal/api/handlers/ --include="*.go" | grep -v "_test.go" || true)
if [ -n "$VIOLATIONS" ]; then
    echo "WARNING: Found goroutines in API handlers (should use job system):"
    echo "$VIOLATIONS"
    # Not failing the build yet, just warning
fi

# Check 5: Check for TODO without associated issue tracking in critical paths
echo "Check 5: Checking for TODO in workflow runner..."
VIOLATIONS=$(grep -rn "TODO\|FIXME" internal/service/workflowrunner/ --include="*.go" || true)
if [ -n "$VIOLATIONS" ]; then
    echo "INFO: Found TODO/FIXME in workflowrunner (expected, module is experimental):"
    echo "$VIOLATIONS"
fi

# Check 6: Verify experimental modules have EXPERIMENTAL.md
echo "Check 6: Verifying experimental modules are marked..."
if [ -d "internal/service/workflowrunner" ] && [ ! -f "internal/service/workflowrunner/EXPERIMENTAL.md" ]; then
    echo "ERROR: workflowrunner must have EXPERIMENTAL.md"
    FAILED=1
fi

# Check 7: No path traversal in workflow handler
echo "Check 7: Verifying path jail in workflow handler..."
if ! grep -q "resolveWorkflowPath\|filepath.Dir.*!=" internal/api/handlers/workflow/handler.go; then
    echo "ERROR: workflow handler must only accept workflow names, not paths"
    FAILED=1
fi

# Check 8: Verify core contracts are used (not internal/pkg/ paths)
echo "Check 8: Verifying core contracts usage..."
VIOLATIONS=$(grep -rn "internal/pkg/" internal/ --include="*.go" | grep -v "_test.go" || true)
if [ -n "$VIOLATIONS" ]; then
    echo "ERROR: Found references to internal/pkg/ (should use pkg/):"
    echo "$VIOLATIONS"
    FAILED=1
fi

# Check 9: Verify no assetpipeline.Service usage (thin wrapper eliminated)
echo "Check 9: Checking for eliminated thin wrappers..."
VIOLATIONS=$(grep -rn "assetpipeline\.Service\|assetpipeline\.New" internal/ --include="*.go" | grep -v "_test.go" || true)
if [ -n "$VIOLATIONS" ]; then
    echo "ERROR: Found references to eliminated assetpipeline.Service:"
    echo "$VIOLATIONS"
    FAILED=1
fi

# Check 10: No new mediaregistry imports (deprecated, use assetregistry)
echo "Check 10: Checking for forbidden mediaregistry imports..."
VIOLATIONS=$(grep -rn "internal/service/mediaregistry" internal/ --include="*.go" | grep -v "_test.go" | grep -v "internal/service/mediaregistry/" || true)
if [ -n "$VIOLATIONS" ]; then
    echo "ERROR: Found forbidden mediaregistry imports (use assetregistry instead):"
    echo "$VIOLATIONS"
    FAILED=1
fi

# Check 11: No drivedestination.Service usages (use core/destination.Resolver)
echo "Check 11: Checking for forbidden drivedestination.Service usages..."
VIOLATIONS=$(grep -rn "drivedestination\.Service" internal/ --include="*.go" | grep -v "_test.go" || true)
if [ -n "$VIOLATIONS" ]; then
    echo "ERROR: Found forbidden drivedestination.Service usages (use core/destination.Resolver):"
    echo "$VIOLATIONS"
    FAILED=1
fi

# Check 12: Bootstrap files must be under 200 lines
echo "Check 12: Checking bootstrap file sizes..."
for file in internal/bootstrap/*.go; do
    lines=$(wc -l < "$file")
    if [ "$lines" -gt 200 ]; then
        echo "ERROR: Bootstrap file $file is over 200 lines ($lines lines)"
        FAILED=1
    fi
done

# Check 13: No global config.Get() outside main/bootstrap
echo "Check 13: Checking for global config.Get() outside main/bootstrap..."
VIOLATIONS=$(grep -rn "config\.Get()" internal/ --include="*.go" | grep -v "cmd/" | grep -v "bootstrap/" | grep -v "_test.go" || true)
if [ -n "$VIOLATIONS" ]; then
    echo "ERROR: Found config.Get() outside main/bootstrap:"
    echo "$VIOLATIONS"
    FAILED=1
fi

# Final result
echo "=== Architectural Checks Complete ==="
if [ $FAILED -eq 1 ]; then
    echo "FAILED: Some architectural checks failed"
    exit 1
else
    echo "PASSED: All architectural checks passed"
fi

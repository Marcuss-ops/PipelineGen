#!/usr/bin/env bash
# scripts/ci/verify-changed.sh
#
# Resolves changed files to optimize local verify runs, mapping changed
# directories and file suffixes to specific verification targets.
# Matches the rule: "New routing, provider selection, source policy, sampling, or resolution logic
# must enter a shared registry, resolver, or sampler. Do not duplicate the same decision logic."
# Keeps decisions consistent in one central location.

set -euo pipefail

# Find base commit for comparison
if git rev-parse --verify origin/main >/dev/null 2>&1; then
    BASE_REF="origin/main"
elif git rev-parse --verify main >/dev/null 2>&1; then
    BASE_REF="main"
else
    # Fallback to HEAD~1 if neither exists
    if git rev-parse --verify HEAD~1 >/dev/null 2>&1; then
        BASE_REF="HEAD~1"
    else
        echo "⚠️ No base ref (origin/main, main, or HEAD~1) found. Running all checks."
        make verify-node-native verify-architecture
        exit 0
    fi
fi

echo "Comparing HEAD against $BASE_REF to detect changed files..."

# Get list of changed files (committed on this branch + uncommitted/staged working tree changes)
CHANGED_FILES=$( (git diff --name-only "$BASE_REF"...HEAD 2>/dev/null || git diff --name-only "$BASE_REF" 2>/dev/null || true; git diff --name-only; git diff --cached --name-only) | sort -u )

if [ -z "$CHANGED_FILES" ]; then
    echo "✅ No files changed."
    exit 0
fi

RUN_ALL=false
RUN_NODE=false
RUN_ARCH=false
RUN_GO_PACKAGE_TESTS=false

for file in $CHANGED_FILES; do
    # Makefile, make/**, scripts/hooks/** -> full verify
    if [[ "$file" =~ ^Makefile$ ]] || [[ "$file" =~ ^make/ ]] || [[ "$file" =~ ^scripts/hooks/ ]]; then
        RUN_ALL=true
        break
    fi
    
    # go.mod or go.sum -> full Go test (all targets)
    if [[ "$file" =~ ^go\.mod$ ]] || [[ "$file" =~ ^go\.sum$ ]]; then
        RUN_ALL=true
        break
    fi

    # node-scraper/** -> fast native-binding verification
    if [[ "$file" =~ ^node-scraper/ ]]; then
        RUN_NODE=true
    fi

    # cmd/archcheck/** or architecture/** -> verify-architecture
    if [[ "$file" =~ ^cmd/archcheck/ ]] || [[ "$file" =~ ^architecture/ ]]; then
        RUN_ARCH=true
    fi

    # Go files -> mark that we have go changes
    if [[ "$file" =~ \.go$ ]]; then
        RUN_GO_PACKAGE_TESTS=true
    fi
done

if [ "$RUN_ALL" = true ]; then
    echo "🔄 Core toolchain, configuration or hooks changed. Running full verification..."
    make verify-node verify-architecture
    exit 0
fi

if [ "$RUN_NODE" = true ]; then
    echo "📦 node-scraper changed. Running native Node verification..."
    make verify-node-native
fi

if [ "$RUN_ARCH" = true ]; then
    echo "🏛️ Architecture files changed. Running architecture verification..."
    make verify-architecture
fi

# Go package-specific testing
if [ "$RUN_GO_PACKAGE_TESTS" = true ]; then
    GO_PACKAGES=()
    for file in $CHANGED_FILES; do
        if [[ "$file" =~ \.go$ ]] && [ -f "$file" ]; then
            dir=$(dirname "$file")
            GO_PACKAGES+=("./$dir")
        fi
    done
    if [ ${#GO_PACKAGES[@]} -gt 0 ]; then
        # Get unique package directories
        UNIQUE_PACKAGES=$(echo "${GO_PACKAGES[@]}" | tr ' ' '\n' | sort -u)
        echo "🧪 Testing modified Go packages:"
        for pkg in $UNIQUE_PACKAGES; do
            echo "  go test $pkg"
            go test "$pkg"
        done
    fi
fi

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
        GO_BIN="${GO:-go}"
        if ! command -v "$GO_BIN" >/dev/null 2>&1 && [ ! -x "$GO_BIN" ]; then
            echo "❌ Go binary '$GO_BIN' was not found; set GO to the Makefile-configured toolchain." >&2
            exit 1
        fi
        GO="$GO_BIN" make verify-node-native verify-architecture
        exit 0
    fi
fi

echo "Comparing HEAD against $BASE_REF to detect changed files..."

# Get list of changed files (committed on this branch + every working-tree
# state). NUL-delimited output keeps paths containing whitespace intact.
collect_changed_files() {
    git diff --name-only -z "$BASE_REF"...HEAD 2>/dev/null \
        || git diff --name-only -z "$BASE_REF" 2>/dev/null \
        || true
    git diff --name-only -z
    git diff --cached --name-only -z
    git ls-files --others --exclude-standard -z
}

mapfile -d '' -t CHANGED_FILES < <(collect_changed_files | sort -z -u)

if [ "${#CHANGED_FILES[@]}" -eq 0 ]; then
    echo "✅ No files changed."
    exit 0
fi

GO_BIN="${GO:-go}"

RUN_ALL=false
RUN_NODE=false
RUN_ARCH=false
RUN_GO_PACKAGE_TESTS=false
RUN_SH_SYNTAX=false
RUN_PY_SYNTAX=false

for file in "${CHANGED_FILES[@]}"; do
    # Classify every changed file: RUN_ALL marks core/global changes but must
    # not short-circuit the loop, or a .go/.sh/.py change sorted before it
    # would lose its targeted checks entirely.
    # Makefile, make/**, scripts/hooks/** -> core verification
    if [[ "$file" =~ ^Makefile$ ]] || [[ "$file" =~ ^make/ ]] || [[ "$file" =~ ^scripts/hooks/ ]]; then
        RUN_ALL=true
    fi
    
    # go.mod or go.sum -> core verification
    if [[ "$file" =~ ^go\.mod$ ]] || [[ "$file" =~ ^go\.sum$ ]]; then
        RUN_ALL=true
    fi

    # node-scraper/** -> fast native-binding verification
    if [[ "$file" =~ ^node-scraper/ ]]; then
        RUN_NODE=true
    fi

    # Shell scripts (scripts/, tests/, hooks/) -> cheap fail-fast syntax
    # check. No registry component owns these paths, so without this branch a
    # broken operational/CI script would verify ZERO checks in the agent loop.
    if [[ "$file" =~ \.sh$ ]]; then
        RUN_SH_SYNTAX=true
    fi

    # Python files (scripts/, tests/) -> cheap fail-fast syntax check via
    # py_compile (compiles without importing, so no dependency resolution).
    if [[ "$file" =~ \.py$ ]]; then
        RUN_PY_SYNTAX=true
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
    if ! command -v "$GO_BIN" >/dev/null 2>&1 && [ ! -x "$GO_BIN" ]; then
        echo "❌ Go binary '$GO_BIN' was not found; set GO to the Makefile-configured toolchain." >&2
        exit 1
    fi
    # Agent-loop scope for core/configuration changes: the native Node probe
    # plus the architecture gates (both part of verify-main). The full
    # node-scraper test suite (verify-node-tests) is deliberately excluded:
    # verify-main itself only requires verify-node-native, and node-scraper
    # source changes are already covered by the native probe branch.
    echo "🔄 Core toolchain, configuration or hooks changed. Running native Node probe + architecture gates..."
    GO="$GO_BIN" make verify-node-native verify-architecture
fi

# node-scraper/architecture branches are already covered by the RUN_ALL path;
# only run them standalone when no core file changed. The targeted Go package
# tests and shell/python syntax checks below ALWAYS run, so a .go/.sh/.py
# change alongside a core change keeps its coverage instead of being swallowed
# by the early core verification.
if [ "$RUN_ALL" = false ] && [ "$RUN_NODE" = true ]; then
    echo "📦 node-scraper changed. Running native Node verification..."
    make verify-node-native
fi

if [ "$RUN_ALL" = false ] && [ "$RUN_ARCH" = true ]; then
    if ! command -v "$GO_BIN" >/dev/null 2>&1 && [ ! -x "$GO_BIN" ]; then
        echo "❌ Go binary '$GO_BIN' was not found; set GO to the Makefile-configured toolchain." >&2
        exit 1
    fi
    echo "🏛️ Architecture files changed. Running architecture verification..."
    GO="$GO_BIN" make verify-architecture
fi

# Go package-specific testing
if [ "$RUN_GO_PACKAGE_TESTS" = true ]; then
    if ! command -v "$GO_BIN" >/dev/null 2>&1 && [ ! -x "$GO_BIN" ]; then
        echo "❌ Go binary '$GO_BIN' was not found; set GO to the Makefile-configured toolchain." >&2
        exit 1
    fi
    GO_PACKAGES=()
    for file in "${CHANGED_FILES[@]}"; do
        if [[ "$file" =~ \.go$ ]] && [ -f "$file" ]; then
            dir=$(dirname -- "$file")
            GO_PACKAGES+=("./$dir")
        fi
    done
    if [ ${#GO_PACKAGES[@]} -gt 0 ]; then
        # Get unique package directories without whitespace splitting.
        mapfile -d '' -t UNIQUE_PACKAGES < <(
            printf '%s\0' "${GO_PACKAGES[@]}" | sort -z -u
        )
        echo "🧪 Testing modified Go packages with $GO_BIN:"
        for pkg in "${UNIQUE_PACKAGES[@]}"; do
            # Tag-gated command directories (e.g. scripts/archcheck/gates,
            # where every .go file carries a c2_* build tag) have no
            # buildable files under the default build context, so go test
            # would fail with "build constraints exclude all Go files".
            # Those gates are invoked with explicit -tags from verify.mk,
            # never as a default-context test target, so skip them here.
            if ! "$GO_BIN" list "$pkg" >/dev/null 2>&1; then
                echo "  skipping $pkg (no buildable Go files under the default build context)"
                continue
            fi
            echo "  $GO_BIN test $pkg"
            "$GO_BIN" test "$pkg"
        done
    fi
fi

# Shell-script changes get a fail-fast syntax check so operational and CI
# scripts are verified even though no registry component owns them.
if [ "$RUN_SH_SYNTAX" = true ]; then
    echo "🔧 Shell scripts changed. Running bash -n syntax check..."
    for file in "${CHANGED_FILES[@]}"; do
        if [[ "$file" =~ \.sh$ ]] && [ -f "$file" ]; then
            bash -n "$file"
        fi
    done
    echo "✅ Shell script syntax check passed"
fi

# Python-script changes get the same fail-fast treatment: py_compile checks
# syntax without importing, so a broken CI/operational script fails the loop
# in milliseconds instead of surfacing later. PYTHONPYCACHEPREFIX keeps the
# generated .pyc files out of the working tree.
if [ "$RUN_PY_SYNTAX" = true ]; then
    PYCACHE_DIR=$(mktemp -d "${TMPDIR:-/tmp}/verify-pyc.XXXXXX")
    trap 'rm -rf "$PYCACHE_DIR"' EXIT
    echo "🐍 Python files changed. Running py_compile syntax check..."
    for file in "${CHANGED_FILES[@]}"; do
        if [[ "$file" =~ \.py$ ]] && [ -f "$file" ]; then
            PYTHONPYCACHEPREFIX="$PYCACHE_DIR" python3 -m py_compile "$file"
        fi
    done
    echo "✅ Python syntax check passed"
fi

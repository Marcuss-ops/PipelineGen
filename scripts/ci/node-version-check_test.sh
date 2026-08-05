#!/usr/bin/env bash
# scripts/ci/node-version-check_test.sh - focused tests for Node version policy

set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
CHECK="$ROOT/scripts/ci/node-version-check.sh"
JSON_NODE=$(command -v node)
FIXTURE=$(mktemp -d "${TMPDIR:-/tmp}/node-version-check.XXXXXX")
trap 'rm -rf "$FIXTURE"' EXIT

mkdir -p "$FIXTURE/node-scraper" "$FIXTURE/web" "$FIXTURE/bin"

write_manifest() {
    local path=$1 requirement=$2
    cat >"$path" <<EOF
{
  "engines": {
    "node": "$requirement"
  }
}
EOF
}

cat >"$FIXTURE/bin/node-22" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' 'v22.22.2'
EOF
cat >"$FIXTURE/bin/node-20" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' 'v20.19.0'
EOF
chmod +x "$FIXTURE/bin/node-22" "$FIXTURE/bin/node-20"

run_check() {
    NODE_VERSION_CHECK_ROOT="$FIXTURE" NODE_VERSION_CHECK_NODE="$1" NODE_VERSION_CHECK_JSON_NODE="$JSON_NODE" bash "$CHECK"
}

write_manifest "$FIXTURE/node-scraper/package.json" '22.x'
write_manifest "$FIXTURE/web/package.json" '22.x'
run_check "$FIXTURE/bin/node-22" >/dev/null

expect_failure() {
    local name=$1
    shift
    if "$@"; then
        echo "FAIL: $name unexpectedly passed" >&2
        exit 1
    fi
}

expect_failure missing-manifest bash -c "rm -f '$FIXTURE/web/package.json'; NODE_VERSION_CHECK_ROOT='$FIXTURE' NODE_VERSION_CHECK_NODE='$FIXTURE/bin/node-22' NODE_VERSION_CHECK_JSON_NODE='$JSON_NODE' bash '$CHECK'"
write_manifest "$FIXTURE/web/package.json" '20.x'
expect_failure major-mismatch bash -c "NODE_VERSION_CHECK_ROOT='$FIXTURE' NODE_VERSION_CHECK_NODE='$FIXTURE/bin/node-22' NODE_VERSION_CHECK_JSON_NODE='$JSON_NODE' bash '$CHECK'"
write_manifest "$FIXTURE/web/package.json" '22.x'
expect_failure host-mismatch bash -c "NODE_VERSION_CHECK_ROOT='$FIXTURE' NODE_VERSION_CHECK_NODE='$FIXTURE/bin/node-20' NODE_VERSION_CHECK_JSON_NODE='$JSON_NODE' bash '$CHECK'"

write_manifest "$FIXTURE/node-scraper/package.json" 'x'
expect_failure unsupported-format bash -c "NODE_VERSION_CHECK_ROOT='$FIXTURE' NODE_VERSION_CHECK_NODE='$FIXTURE/bin/node-22' NODE_VERSION_CHECK_JSON_NODE='$JSON_NODE' bash '$CHECK'"

printf '%s\n' '✅ node-version-check focused tests passed'

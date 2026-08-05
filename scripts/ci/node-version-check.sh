#!/usr/bin/env bash
# scripts/ci/node-version-check.sh - canonical Node toolchain contract
#
# Both JavaScript workspaces publish their required Node major through
# package.json. This check reads those declarations, requires the majors to
# agree, and fails closed when the host Node major differs.

set -euo pipefail

ROOT=${NODE_VERSION_CHECK_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}
NODE_BIN=${NODE_VERSION_CHECK_NODE:-node}
JSON_NODE=${NODE_VERSION_CHECK_JSON_NODE:-node}

fail() {
    echo "❌ Node version check: $*" >&2
    exit 1
}

[[ -d "$ROOT" ]] || fail "repository root does not exist: $ROOT"
[[ -f "$ROOT/node-scraper/package.json" ]] || fail "missing node-scraper/package.json"
[[ -f "$ROOT/web/package.json" ]] || fail "missing web/package.json"

command -v "$NODE_BIN" >/dev/null 2>&1 || fail "Node binary not found: $NODE_BIN"
command -v "$JSON_NODE" >/dev/null 2>&1 || fail "JSON parser Node binary not found: $JSON_NODE"

# Parse the manifests with Node's JSON parser so the check reads the actual
# engines.node field rather than relying on formatting-sensitive text scans.
read_engine() {
    local manifest=$1 value
    value=$("$JSON_NODE" -e '
const fs = require("node:fs");
const manifest = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
const value = manifest?.engines?.node;
if (typeof value !== "string" || value.trim() === "") process.exit(2);
process.stdout.write(value.trim());
' "$manifest" 2>/dev/null) || fail "$manifest must declare a valid engines.node string"
    printf '%s\n' "$value"
}

scraper_req=$(read_engine "$ROOT/node-scraper/package.json")
web_req=$(read_engine "$ROOT/web/package.json")
host=$("$NODE_BIN" --version 2>/dev/null | sed 's/^v//')
[[ -n "$host" ]] || fail "Node binary returned an empty version"

major_from_requirement() {
    local requirement=$1 major
    [[ "$requirement" =~ ^[0-9]+\.x$ ]] \
        || fail "unsupported engines.node format (expected <major>.x): $requirement"
    major=${requirement%%.*}
    printf '%s\n' "$major"
}

scraper_major=$(major_from_requirement "$scraper_req")
web_major=$(major_from_requirement "$web_req")
host_major=${host%%.*}
[[ "$host_major" =~ ^[0-9]+$ ]] || fail "unsupported host Node version: $host"

if [[ "$scraper_major" != "$web_major" ]]; then
    fail "Node major mismatch: node-scraper requires $scraper_req, web requires $web_req"
fi

if [[ "$host_major" != "$scraper_major" ]]; then
    fail "Node version mismatch: both packages require major $scraper_major, host has $host"
fi

echo "✅ Node version $host meets scraper=$scraper_req and web=$web_req"

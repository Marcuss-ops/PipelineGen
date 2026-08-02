#!/usr/bin/env bash
# Certify the verification-gate graph and its cost-tier contracts.
# This gate is structural: it uses make's database and dry-run plans, so it
# does not depend on application tests being green and never invokes live.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
PLAN_DIR=${VERIFY_SPLIT_PLAN_DIR:-/tmp/verify-split}
mkdir -p "$PLAN_DIR"
failures=0

fail() {
    echo "FAIL: $*" >&2
    failures=$((failures + 1))
}

# The sentinel target `:` is intentionally absent, so GNU Make may return 1
# after printing its complete database. The database itself is what matters.
graph=$(cd "$ROOT" && make -pRrq -f Makefile : 2>/dev/null) || true
if [[ -z "$graph" ]]; then
    echo "FAIL: unable to read Make graph" >&2
    exit 1
fi

graph_prerequisites() {
    local target=$1 line
    line=$(printf '%s\n' "$graph" | awk -v prefix="${target}:" \
        'index($0, prefix) == 1 { print substr($0, length(prefix) + 1); exit }')
    printf '%s\n' "$line" | xargs
}

assert_graph() {
    local target=$1 expected=$2 actual
    actual=$(graph_prerequisites "$target")
    if [[ "$actual" != "$expected" ]]; then
        fail "$target prerequisites: expected [$expected], got [$actual]"
    fi
}

assert_graph verify-fast "verify-foundation verify-static"
assert_graph verify-push "verify-foundation verify-static verify-unit-fast verify-changed-components"
assert_graph verify-main "verify-push verify-node-native verify-architecture"
assert_graph verify-race "verify-foundation verify-unit-race verify-race-components"
assert_graph verify-full "verify-main verify-race verify-node-tests"
assert_graph verify-release "verify-full verify-integration"
assert_graph verify-live "auth-check verify-images-live verify-artlist-live verify-script-live verify-vidrush-live"

for target in verify-fast verify-main verify-race verify-full verify-release verify-live; do
    (cd "$ROOT" && make -n --no-print-directory "$target") >"$PLAN_DIR/${target}.plan"
done

fast=$PLAN_DIR/verify-fast.plan
main=$PLAN_DIR/verify-main.plan
race=$PLAN_DIR/verify-race.plan
full=$PLAN_DIR/verify-full.plan
release=$PLAN_DIR/verify-release.plan
live=$PLAN_DIR/verify-live.plan

require() {
    local file=$1 pattern=$2 description=$3
    if ! grep -Eq -- "$pattern" "$file"; then
        fail "$description missing from $(basename "$file")"
    fi
}

forbid() {
    local file=$1 pattern=$2 description=$3
    if grep -Eq -- "$pattern" "$file"; then
        fail "$description found in $(basename "$file")"
    fi
}

# FAST: toolchain, hygiene, formatting, tidy, vet and build only.
require "$fast" 'go-version-check|go version' "Go version check"
require "$fast" 'node-version-check|node --version' "Node version check"
require "$fast" 'ci-no-secrets' "secret audit"
require "$fast" 'gofmt|format' "format check"
require "$fast" 'go mod tidy|tidy' "module tidy"
require "$fast" 'go vet' "Go vet"
require "$fast" 'go build' "Go build"
require "$fast" 'bash -n' "hook syntax check"
forbid "$fast" 'go test|npm test|tests/operational|with-velox-auth|(^|[[:space:]])-race([[:space:]]|$)' "heavy, race or live command"

# MAIN: push gate plus native binding probe and architecture, without race,
# full Node, or post-deploy batteries.
require "$main" 'verify-changed-components\.py' "changed-component runner"
require "$main" 'better-sqlite3' "native Node probe"
require "$main" 'cmd/architecture-aggregate|cmd/archcheck' "architecture checks"
forbid "$main" 'tests/operational|with-velox-auth|npm test|verify-live|scraper-up|(^|[[:space:]])-race([[:space:]]|$)' "race, full Node or live command"

# RACE: at least one explicit race command, but no live battery.
require "$race" '(^|[[:space:]])-race([[:space:]]|$)' "race detector"
forbid "$race" 'tests/operational|with-velox-auth|verify-live' "live command"

# FULL: main + race + complete Node tests, still headless.
require "$full" '(^|[[:space:]])-race([[:space:]]|$)' "race detector"
require "$full" 'npm test|node --test' "complete Node tests"
forbid "$full" 'tests/operational|with-velox-auth|verify-live' "live command"

# RELEASE: integration is included, live/post-deploy batteries are not.
require "$release" 'verify-go-tests|go test .*tests' "integration tests"
forbid "$release" 'tests/operational|with-velox-auth|verify-live|scraper-up' "post-deploy command"

# LIVE: all operational batteries must be present and authenticated through
# the canonical wrapper.
require "$live" 'tests/operational' "operational battery"
require "$live" 'with-velox-auth' "canonical auth wrapper"

# GNU Make must reuse shared prerequisites within one aggregate invocation.
trace=$PLAN_DIR/verify-full.trace
(cd "$ROOT" && make --trace -n --no-print-directory verify-full) >"$trace" 2>&1
for target in verify-foundation verify-node-tests verify-architecture; do
    count=$(grep -cF "target '$target'" "$trace" || true)
    if [[ "$count" -ne 1 ]]; then
        fail "$target is scheduled $count times in verify-full (expected 1)"
    fi
done

# Heavy recipe lines must not be copied into multiple branches of FULL.
duplicates=$(
    grep -E 'go test|go vet|go build|npm test|python3 scripts/ci/verify' "$full" |
        sed -E 's/^[[:space:]]+//' |
        sort | uniq -c | awk '$1 > 1 { print }'
)
if [[ -n "$duplicates" ]]; then
    fail "duplicate heavy commands in verify-full: $duplicates"
fi

# Every registry component has an explicit Make alias. Underscores in a
# registry key are normalized to the hyphenated Make spelling.
registry_components=$(cd "$ROOT" && python3 - <<'PY'
import json
from pathlib import Path

for name in sorted(json.loads(Path("config/verify-components.json").read_text())):
    print(name)
PY
)
while IFS= read -r component; do
    [[ -n "$component" ]] || continue
    target=${component//_/-}
    if ! grep -qE "^verify-${target}:" <<<"$graph"; then
        fail "registry component $component has no verify-${target} Make alias"
    fi
done <<< "$registry_components"

# Coverage is a separate fail-closed contract, not an implicit fallback in
# verify-changed-components. Keep its reports in the temporary plan directory.
coverage_report=$PLAN_DIR/component-coverage.json
if ! (cd "$ROOT" && python3 scripts/ci/verify-component-coverage.py --report "$coverage_report"); then
    fail "component registry coverage is not complete"
fi
changed_report=$PLAN_DIR/changed-strict.json
if ! (cd "$ROOT" && python3 scripts/ci/verify-changed-components.py --dry-run --report "$changed_report"); then
    fail "changed-component dry-run failed"
fi
if [[ -f "$changed_report" ]] && [[ "$(jq '.unmapped_files | length' "$changed_report")" -ne 0 ]]; then
    fail "changed-component report contains unmapped files"
fi

# The component runner must expose command reuse in its machine report.
latest_report=$ROOT/artifacts/verify/latest.json
if [[ -f "$latest_report" ]]; then
    reused=$(jq '[.. | objects | select(.reused == true)] | length' "$latest_report")
    if [[ "$reused" -lt 1 ]]; then
        fail "component report does not record reused commands"
    fi
else
    fail "missing artifacts/verify/latest.json; run a component verification before verify-split"
fi

if [[ "$failures" -ne 0 ]]; then
    echo "verify-split failures=$failures final=FAIL" >&2
    exit 1
fi

echo "verify-split plans=$PLAN_DIR final=PASS"

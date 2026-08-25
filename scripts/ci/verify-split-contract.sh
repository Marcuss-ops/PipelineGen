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
assert_graph verify-full "verify-main verify-race verify-node-tests verify-clean-checkout-build"
assert_graph verify-release "verify-full verify-integration"
assert_graph verify-live "auth-check verify-images-live verify-artlist-live verify-script-live verify-vidrush-live verify-stock-live"
assert_graph verify-stock-unit "test-stock-component test-youtube-stock-fast"
assert_graph verify-stock-integration "test-youtube-stock-local test-youtube-stock-resilience"
assert_graph verify-stock-live "auth-check"
assert_graph verify-stock-release "verify-stock-unit verify-stock-integration verify-stock-live"

for target in verify-fast verify-main verify-race verify-full verify-release verify-live verify-stock-unit verify-stock-integration verify-stock-live verify-stock-release; do
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
require "$fast" 'ci-submodule-integrity|verify-repository-integrity' "repository integrity"
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

# FULL: main + race + complete Node tests + clean-checkout reproducibility,
# still headless.
require "$full" '(^|[[:space:]])-race([[:space:]]|$)' "race detector"
require "$full" 'npm test|node --test' "complete Node tests"
require "$full" 'ci-clean-checkout-build' "clean-checkout build"
forbid "$full" 'tests/operational|with-velox-auth|verify-live' "live command"

# RELEASE: integration is included, live/post-deploy batteries are not.
require "$release" 'verify-go-tests|go test .*tests' "integration tests"
forbid "$release" 'tests/operational|with-velox-auth|verify-live|scraper-up' "post-deploy command"

# LIVE: all operational batteries must be present and authenticated through
# the canonical wrapper.
require "$live" 'tests/operational' "operational battery"
require "$live" 'with-velox-auth' "canonical auth wrapper"
stock_live=$PLAN_DIR/verify-stock-live.plan
stock_release=$PLAN_DIR/verify-stock-release.plan
require "$stock_live" 'stock_e2e_full_battery|with-velox-auth' "Stock live battery"
require "$stock_live" 'auth-check|with-velox-auth' "Stock live auth"
require "$stock_release" 'verify-stock-unit|test-youtube-stock-fast' "Stock release unit level"
require "$stock_release" 'verify-stock-integration|test-youtube-stock-local' "Stock release integration level"
require "$stock_release" 'verify-stock-live|stock_e2e_full_battery' "Stock release live level"
require "$stock_release" 'verify-stock-receipt\.sh' "Stock release receipt validator"
require "$stock_release" 'verify-stock-receipt\.sh' "Stock release invokes receipt validator"
require "$ROOT/scripts/ci/verify-stock-receipt.sh" 'expected_verdict=.*14/14 PASS' "Stock release canonical receipt verdict"
require "$ROOT/scripts/ci/verify-stock-receipt.sh" 'required_markers=\(route job outbox qdrant mp4 ffprobe\)' "Stock release route/job/outbox/Qdrant/MP4/ffprobe marker list"
require "$ROOT/tests/operational/stock_e2e_download_smoke.sh" 'SIZE.*MIN_BYTES|SIZE.*100000' "Stock MP4 size assertion"
require "$ROOT/tests/operational/stock_e2e_download_smoke.sh" 'ffprobe' "Stock ffprobe assertion"
forbid "$stock_release" 'verify-youtube-stock-live|verify-stock-acquisition|verify-stock-indexing' "retired Stock verification alias"

# Hermetic receipt/claim contract: a valid attested receipt authorizes the
# claim, while missing provenance, surface markers, a wrong run ID, or a
# tampered attestation fail closed with empty stdout.
receipt_test_dir="$PLAN_DIR/stock-receipt-contract"
rm -rf "$receipt_test_dir"
mkdir -p "$receipt_test_dir"
cat >"$receipt_test_dir/valid.log" <<'EOF'
RECEIPT: source=stock_e2e_full_battery.sh
RECEIPT: execution=live
RECEIPT: run_id=contract-test-run
RECEIPT: attestation=PLACEHOLDER
RECEIPT: route=PASS
RECEIPT: job=PASS
RECEIPT: outbox=PASS
RECEIPT: qdrant=PASS
RECEIPT: mp4=PASS
RECEIPT: ffprobe=PASS
VERDICT: 14/14 PASS (STOCK-E2E-BATTERY-2026-07-05 wave-flip eligible)
EOF
attestation_key='contract-test-key'
attestation_payload='contract-test-run|STOCK-E2E-BATTERY-2026-07-05|14/14|route=PASS|job=PASS|outbox=PASS|qdrant=PASS|mp4=PASS|ffprobe=PASS'
attestation=$(printf '%s' "$attestation_payload" | openssl dgst -sha256 -hmac "$attestation_key" -hex | sed 's/^.*= //')
sed -i "s/RECEIPT: attestation=.*/RECEIPT: attestation=${attestation}/" "$receipt_test_dir/valid.log"
if ! (cd "$ROOT" && STOCK_E2E_RECEIPT_KEY="$attestation_key" bash scripts/ci/verify-stock-receipt.sh "$receipt_test_dir/valid.log" "contract-test-run" >/dev/null); then
    fail "valid attested 14/14 receipt was rejected"
fi
claim_output=$(cd "$ROOT" && STOCK_E2E_RECEIPT_KEY="$attestation_key" bash scripts/ci/verify-stock-claim.sh "$receipt_test_dir/valid.log" "contract-test" "contract-test-run")
if [[ "$claim_output" != 'STOCK VERIFIED: contract-test '* ]]; then
    fail "valid attested receipt did not emit the authoritative claim"
fi
for missing in source execution run_id route job outbox qdrant mp4 ffprobe; do
    case "$missing" in
        source) marker='RECEIPT: source=stock_e2e_full_battery.sh' ;;
        execution) marker='RECEIPT: execution=live' ;;
        run_id) marker='RECEIPT: run_id=contract-test-run' ;;
        *) marker="RECEIPT: ${missing}=PASS" ;;
    esac
    sed "/${marker}/d" "$receipt_test_dir/valid.log" >"$receipt_test_dir/missing-${missing}.log"
    if claim_output=$(cd "$ROOT" && STOCK_E2E_RECEIPT_KEY="$attestation_key" bash scripts/ci/verify-stock-claim.sh "$receipt_test_dir/missing-${missing}.log" "contract-test" "contract-test-run" 2>/dev/null); then
        fail "receipt claim accepted missing ${missing} proof"
    elif [[ -n "$claim_output" ]]; then
        fail "invalid receipt emitted a claim for missing ${missing} proof"
    fi
done
sed 's/RECEIPT: attestation=.*/RECEIPT: attestation=invalid/' "$receipt_test_dir/valid.log" >"$receipt_test_dir/invalid-attestation.log"
if claim_output=$(cd "$ROOT" && STOCK_E2E_RECEIPT_KEY="$attestation_key" bash scripts/ci/verify-stock-claim.sh "$receipt_test_dir/invalid-attestation.log" "contract-test" "contract-test-run" 2>/dev/null); then
    fail "receipt claim accepted an invalid attestation"
elif [[ -n "$claim_output" ]]; then
    fail "invalid attestation emitted an authoritative claim"
fi
if claim_output=$(cd "$ROOT" && STOCK_E2E_RECEIPT_KEY="$attestation_key" bash scripts/ci/verify-stock-claim.sh "$receipt_test_dir/valid.log" "contract-test" "wrong-run-id" 2>/dev/null); then
    fail "receipt claim accepted a mismatched run ID"
elif [[ -n "$claim_output" ]]; then
    fail "mismatched run ID emitted an authoritative claim"
fi

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

# Every executable registry component has an explicit Make alias. Stock's
# component runner is intentionally diagnostic (`test-stock-component`), so
# it is not a second public `verify-*` component gate. (Web Admin removed
# 2026-08-25 — no web utility remains.)
registry_components=$(cd "$ROOT" && python3 - <<'PY'
import json
from pathlib import Path

for name in sorted(json.loads(Path("config/verify-components.json").read_text())):
    if name == "web":
        continue
    print(name)
PY
)
while IFS= read -r component; do
    [[ -n "$component" ]] || continue
    target=${component//_/-}
    case "$component" in
        stock)
            if ! grep -qE '^test-stock-component:' <<<"$graph"; then
                fail "registry component stock has no test-stock-component Make alias"
            fi
            ;;
        *)
            if ! grep -qE "^verify-${target}:" <<<"$graph"; then
                fail "registry component $component has no verify-${target} Make alias"
            fi
            ;;
    esac
done <<< "$registry_components"

clean_checkout_script=$ROOT/scripts/ci/ci-clean-checkout-build.sh
require "$clean_checkout_script" 'vet ./\.\.\.' "full Go vet"
require "$clean_checkout_script" 'test ./\.\.\.' "full Go tests"
require "$clean_checkout_script" 'build -o pipelinegen ./cmd/server' "server build"
require "$clean_checkout_script" 'build -o worker ./cmd/worker' "worker build"
require "$clean_checkout_script" 'build -o admin ./cmd/admin' "admin build"
require "$clean_checkout_script" 'GO_BIN=\$\{GO:-go\}' "configurable Go binary"
require "$clean_checkout_script" 'GO_BIN=\$\(command -v' "absolute Go binary resolution"
require "$ROOT/make/verify.mk" 'GO="\$\(GO\)" bash scripts/ci/ci-clean-checkout-build\.sh' "Make Go forwarding"
require "$ROOT/scripts/ci/ci-submodule-integrity.sh" 'git ls-files --stage -z' "index gitlink scan"
require "$ROOT/scripts/ci/ci-submodule-integrity.sh" 'no tracked gitlinks' "orphan gitlink rejection"
for workflow in "$ROOT"/.github/workflows/*.yml "$ROOT"/.github/workflows/*.yaml; do
    [[ -f "$workflow" ]] || continue
    require "$workflow" 'submodules: false' "explicit submodule policy"
done

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

#!/usr/bin/env bash
# Emit the only authoritative stock verification claim, or scan static
# documentation/report surfaces for unqualified aggregate claims.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
RECEIPT_VALIDATOR="$ROOT/scripts/ci/verify-stock-receipt.sh"

scan_claim_surfaces() {
    local scan_root=${STOCK_CLAIM_SCAN_ROOT:-$ROOT}
    local pattern='(^|[^[:alnum:]])stock[[:space:]]+(pipeline[[:space:]]+)?(is[[:space:]]+)?verified([^[:alnum:]]|$)|(^|[^[:alnum:]])stock[[:space:]]+works([^[:alnum:]]|$)|(^|[^[:alnum:]])verified[[:space:]]+stock([^[:alnum:]]|$)|(^|[^[:alnum:]])stock[[:space:]]+verification[[:space:]]+(passed|complete|successful)([^[:alnum:]]|$)'
    local matches=''
    local path file file_matches line relative

    for path in docs out artifacts logs reports tests/operational scripts make; do
        [[ -d "$scan_root/$path" ]] || continue
        while IFS= read -r file; do
            [[ -f "$file" ]] || continue
            relative="${file#"$scan_root"/}"
            case "$relative" in
                scripts/ci/verify-stock-claim.sh|scripts/ci/verify-stock-receipt.sh|scripts/ci/verify-split-contract.sh)
                    continue
                    ;;
            esac
            file_matches=$(grep -n -I -i -E "$pattern" "$file" 2>/dev/null || true)
            [[ -n "$file_matches" ]] || continue
            while IFS= read -r line; do
                matches+="${file#"$scan_root"}:$line"$'\n'
            done <<< "$file_matches"
        done < <(find "$scan_root/$path" -type f -print)
    done

    if [[ -n "$matches" ]]; then
        echo "FAIL: unqualified stock verification claim found in docs/reports:" >&2
        printf '%s' "$matches" >&2
        echo "Use the canonical live receipt claim gate." >&2
        return 1
    fi
    echo "PASS: no unqualified stock verification claims in docs/reports"
}

if [[ "${1:-}" == "--scan" ]]; then
    scan_claim_surfaces
    exit 0
fi

receipt=${1:?usage: verify-stock-claim.sh RECEIPT_FILE CLAIM_LABEL RUN_ID}
label=${2:?usage: verify-stock-claim.sh RECEIPT_FILE CLAIM_LABEL RUN_ID}
run_id=${3:?usage: verify-stock-claim.sh RECEIPT_FILE CLAIM_LABEL RUN_ID}

bash "$RECEIPT_VALIDATOR" "$receipt" "$run_id" >/dev/null
printf 'STOCK VERIFIED: %s (live receipt: %s; VERDICT: 14/14 PASS)\n' "$label" "$receipt"

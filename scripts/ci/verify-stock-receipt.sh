#!/usr/bin/env bash
# Validate the receipt emitted by stock_e2e_full_battery.sh.
set -euo pipefail

receipt=${1:?usage: verify-stock-receipt.sh RECEIPT_FILE RUN_ID}
run_id=${2:?usage: verify-stock-receipt.sh RECEIPT_FILE RUN_ID}
attestation_key=${STOCK_E2E_RECEIPT_KEY:-}
if [[ -n "${STOCK_E2E_RECEIPT_KEY_FILE:-}" ]]; then
    [[ -r "$STOCK_E2E_RECEIPT_KEY_FILE" ]] || { echo "FAIL: stock receipt key file is unreadable" >&2; exit 1; }
    attestation_key=$(<"$STOCK_E2E_RECEIPT_KEY_FILE")
fi
attestation_key=${attestation_key//$'\n'/}

if [[ -z "$attestation_key" ]]; then
    echo "FAIL: stock E2E receipt attestation key is missing" >&2
    exit 1
fi
if ! command -v openssl >/dev/null 2>&1; then
    echo "FAIL: openssl is required to verify the stock E2E receipt" >&2
    exit 1
fi
if [[ ! "$run_id" =~ ^[A-Za-z0-9._:-]+$ ]]; then
    echo "FAIL: invalid stock E2E run ID" >&2
    exit 1
fi
if [[ ! -s "$receipt" ]]; then
    echo "FAIL: stock E2E receipt is missing or empty: $receipt" >&2
    exit 1
fi

expected_verdict='VERDICT: 14/14 PASS (STOCK-E2E-BATTERY-2026-07-05 wave-flip eligible)'
grep -Fqx "$expected_verdict" "$receipt" || { echo "FAIL: stock E2E receipt does not contain the canonical 14/14 PASS verdict: $receipt" >&2; exit 1; }

required_markers=(route job outbox qdrant mp4 ffprobe)
for marker in "${required_markers[@]}"; do
    expected_marker="RECEIPT: ${marker}=PASS"
    grep -Fqx "$expected_marker" "$receipt" || { echo "FAIL: stock E2E receipt missing PASS marker for ${marker}: $receipt" >&2; exit 1; }
done

for expected_marker in \
    'RECEIPT: source=stock_e2e_full_battery.sh' \
    'RECEIPT: execution=live' \
    "RECEIPT: run_id=${run_id}"; do
    grep -Fqx "$expected_marker" "$receipt" || { echo "FAIL: stock E2E receipt missing provenance marker '${expected_marker}': $receipt" >&2; exit 1; }
done

attestation_payload="${run_id}|STOCK-E2E-BATTERY-2026-07-05|14/14|route=PASS|job=PASS|outbox=PASS|qdrant=PASS|mp4=PASS|ffprobe=PASS"
expected_attestation=$(printf '%s' "$attestation_payload" | openssl dgst -sha256 -hmac "$attestation_key" -hex | sed 's/^.*= //')
grep -Fqx "RECEIPT: attestation=${expected_attestation}" "$receipt" || { echo "FAIL: stock E2E receipt attestation is missing or invalid: $receipt" >&2; exit 1; }

echo "PASS: canonical stock E2E receipt verified (14/14; live run ${run_id}; route, job, outbox, qdrant, mp4, ffprobe, attested)"

#!/usr/bin/env bash
# Validate the receipt emitted by stock_e2e_full_battery.sh.
# This helper is intentionally live-free and only accepts the exact canonical
# verdict plus every required surface marker.
set -euo pipefail

receipt=${1:?usage: verify-stock-receipt.sh RECEIPT_FILE}

if [[ ! -s "$receipt" ]]; then
    echo "FAIL: stock E2E receipt is missing or empty: $receipt" >&2
    exit 1
fi

expected_verdict='VERDICT: 14/14 PASS (STOCK-E2E-BATTERY-2026-07-05 wave-flip eligible)'
grep -Fqx "$expected_verdict" "$receipt" || {
    echo "FAIL: stock E2E receipt does not contain the canonical 14/14 PASS verdict: $receipt" >&2
    exit 1
}

required_markers=(route job outbox qdrant mp4 ffprobe)
for marker in "${required_markers[@]}"; do
    expected_marker="RECEIPT: ${marker}=PASS"
    grep -Fqx "$expected_marker" "$receipt" || {
        echo "FAIL: stock E2E receipt missing PASS marker for ${marker}: $receipt" >&2
        exit 1
    }
done

echo "PASS: canonical stock E2E receipt verified (14/14; route, job, outbox, qdrant, mp4, ffprobe)"

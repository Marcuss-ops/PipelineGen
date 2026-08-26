#!/usr/bin/env bash
# Check 42: enforce current database/sql ownership.
set -euo pipefail

echo "=== Check 42: enforce database/sql ownership in current internal roots ==="
go run ./scripts/archcheck --ratchet

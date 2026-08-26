#!/usr/bin/env bash
# Check 42: enforce current database/sql ownership.
#
# The former gate scanned deleted internal/application and internal/api roots
# and consumed the retired app-sql allowlist. The current tree has four roots;
# the canonical AST-based checker permits database/sql only in internal/app
# and internal/platform and reports capability/kernel leaks directly.
set -euo pipefail

echo "=== Check 42: enforce database/sql ownership in current internal roots ==="
go run ./scripts/archcheck --ratchet

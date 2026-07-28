#!/usr/bin/env bash
set -euo pipefail
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/tests/operational/stock_pipeline_live_test.sh" "$@"

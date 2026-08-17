#!/usr/bin/env bash
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

# shellcheck source=scripts/lib/dotenv.sh
source "$SCRIPT_DIR/lib/dotenv.sh"

# Explicit environment wins: .env only fills variables that are unset or
# empty, so a caller-provided VELOX_ADMIN_TOKEN is never silently overridden.
load_dotenv_missing .env

exec ./bin/pipelinegen

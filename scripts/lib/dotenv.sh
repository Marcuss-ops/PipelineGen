# scripts/lib/dotenv.sh — load a KEY=VALUE dotenv file WITHOUT overriding
# already-set environment variables.
#
# Explicit environment always wins over the file: the file only supplies
# development defaults for variables that are currently unset (or empty).
# This is the opposite of `set -a; source .env` / `export $(cat .env)`, which
# silently clobber caller-provided values — the exact failure that can
# desynchronize a live VELOX_ADMIN_TOKEN and turn every request into a 401.
#
# The canonical token file (/etc/pipelinegen/pipelinegen.env) is loaded by
# scripts/with-velox-auth, never here; this helper exists for repository-local
# .env development defaults.
#
# Usage:
#   SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   # shellcheck source=scripts/lib/dotenv.sh
#   source "$SCRIPT_DIR/lib/dotenv.sh"
#   load_dotenv_missing .env

# load_dotenv_missing <file> sources a dotenv file so that only variables that
# are currently unset (or empty) are populated. Variables already exported by
# the caller are never touched.
load_dotenv_missing() {
  local file="$1" line key value
  [ -f "$file" ] || return 0
  while IFS= read -r line || [ -n "$line" ]; do
    # Trim leading whitespace.
    line="${line#"${line%%[![:space:]]*}"}"
    case "$line" in
      ''|'#'*) continue ;;
      export\ *) line="${line#export }" ;;
    esac
    key="${line%%=*}"
    [ -n "$key" ] || continue
    # Fill only missing (unset or empty) variables.
    if [ -z "${!key:-}" ]; then
      value="${line#*=}"
      # Strip one layer of surrounding quotes if present.
      case "$value" in
        \"*\"|\'*\') value="${value#?}"; value="${value%?}" ;;
      esac
      printf -v "$key" '%s' "$value"
      export "$key"
    fi
  done < "$file"
}

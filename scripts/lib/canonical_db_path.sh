#!/usr/bin/env bash
# canonical_db_path.sh — single shell resolver for the operational primary DB.
#
# The runtime primary database is always:
#   <VELOX_DATA_DIR>/media/media.db.sqlite
#
# Legacy paths such as <data>/media.db.sqlite, data/pipelinegen.db,
# data/velox.db, and /var/lib/velox/velox.db are never fallback candidates.

canonical_primary_db_path() {
    local root="${1:-${ROOT_DIR:-.}}"
    if [[ "$root" != /* ]]; then
        root="$(cd -- "$root" 2>/dev/null && pwd -P)" || {
            printf 'canonical DB path error: project root is not resolvable: %s\n' "$root" >&2
            return 2
        }
    fi
    local data_dir="${VELOX_DATA_DIR:-$root/data}"
    if [[ "$data_dir" != /* ]]; then
        data_dir="$root/${data_dir#./}"
    fi
    printf '%s/media/media.db.sqlite\n' "${data_dir%/}"
}

validate_canonical_primary_db_path() {
    local candidate="${1:-}"
    local root="${3:-${ROOT_DIR:-.}}"
    local expected="${2:-$(canonical_primary_db_path "$root")}"
    [[ -n "$candidate" ]] || candidate="$expected"
    if [[ "$root" != /* ]]; then
        root="$(cd -- "$root" 2>/dev/null && pwd -P)" || {
            printf 'canonical DB path error: project root is not resolvable: %s\\n' "$root" >&2
            return 2
        }
    fi
    local candidate_abs="$candidate"
    if [[ "$candidate_abs" != /* ]]; then
        candidate_abs="$root/${candidate_abs#./}"
    fi
    if [[ "$candidate_abs" != "$expected" ]]; then
        printf 'canonical DB path error: non-canonical primary DB: %s (expected %s)\n' "$candidate" "$expected" >&2
        return 2
    fi
    printf '%s\n' "$expected"
}

resolve_canonical_primary_db() {
    local candidate="${1:-}"
    local root="${2:-${ROOT_DIR:-.}}"
    local expected="$(canonical_primary_db_path "$root")" || return
    validate_canonical_primary_db_path "$candidate" "$expected" "$root"
}

require_canonical_primary_db() {
    local path
    path="$(resolve_canonical_primary_db "${1:-}")" || return
    [[ -f "$path" ]] || {
        printf 'canonical DB path error: primary DB not found: %s\n' "$path" >&2
        return 3
    }
    printf '%s\n' "$path"
}

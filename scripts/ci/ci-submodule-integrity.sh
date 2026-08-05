#!/usr/bin/env bash
# scripts/ci/ci-submodule-integrity.sh - fail-closed tracked gitlink check
#
# A gitlink (mode 160000) is valid only when the staged .gitmodules file
# declares the same path and provides a non-empty URL. With no gitlinks,
# .gitmodules is not required. This check inspects the index only and never
# mutates the working tree, so ignored local directories remain untouched.

set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"

fail() {
    echo "❌ submodule integrity: $*" >&2
    exit 1
}

# Read staged entries as NUL-delimited records so paths containing whitespace
# are handled without word splitting. The path is the portion after Git's
# tab separator; the mode is the first field.
gitlinks=()
while IFS= read -r -d '' entry; do
    mode=${entry%% *}
    [[ "$mode" == "160000" ]] || continue
    path=${entry#*$'\t'}
    [[ "$path" != "$entry" ]] || fail "malformed git index entry"
    gitlinks+=("$path")
done < <(git ls-files --stage -z)

if ((${#gitlinks[@]} == 0)); then
    echo "✅ submodule integrity: no tracked gitlinks"
    exit 0
fi

# Read .gitmodules from the index, not the working tree. An untracked or
# unstaged .gitmodules must never make an invalid commit appear valid.
git ls-files --error-unmatch -- .gitmodules >/dev/null 2>&1 \
    || fail "tracked gitlink(s) exist but .gitmodules is absent from the index: ${gitlinks[*]}"
modules_file=$(mktemp)
trap 'rm -f "$modules_file"' EXIT
git cat-file blob :.gitmodules >"$modules_file" \
    || fail "unable to read staged .gitmodules"
git config --file "$modules_file" --list >/dev/null 2>&1 \
    || fail "staged .gitmodules is not valid Git configuration"

# `git config --get-regexp` emits each key followed by its value. The value
# is retained after the first separator, so paths containing spaces remain
# intact.
declarations=$(git config --file "$modules_file" --get-regexp '^submodule\..*\.path$' 2>/dev/null || true)
[[ -n "$declarations" ]] || fail "staged .gitmodules declares no submodule paths"

# Every declared submodule path must have a non-empty URL, including entries
# that do not currently have a corresponding tracked gitlink.
while IFS= read -r declaration; do
    [[ -n "$declaration" ]] || continue
    key=${declaration%% *}
    declared_path=${declaration#* }
    url_key=${key%.path}.url
    url=$(git config --file "$modules_file" --get "$url_key" 2>/dev/null || true)
    [[ -n "$url" ]] || fail "declared submodule path '$declared_path' has no non-empty URL"
done <<< "$declarations"

for path in "${gitlinks[@]}"; do
    found_path=false

    while IFS= read -r declaration; do
        [[ -n "$declaration" ]] || continue
        declared_path=${declaration#* }
        [[ "$declared_path" == "$path" ]] || continue
        found_path=true
        break
    done <<< "$declarations"

    [[ "$found_path" == true ]] || fail "gitlink '$path' has no matching path in staged .gitmodules"
done

echo "✅ submodule integrity: ${#gitlinks[@]} tracked gitlink(s) validated"

#!/usr/bin/env bash
set -euo pipefail

# Validate that every tracked gitlink is declared by the staged .gitmodules.
gitlinks=()
while IFS= read -r -d '' entry; do
  [[ "${entry%% *}" == 160000 ]] || continue
  gitlinks+=("${entry#*$'\t'}")
done < <(git ls-files --stage -z)

if ((${#gitlinks[@]} == 0)); then
  echo "✅ submodule integrity: no tracked gitlinks"
  exit 0
fi

git ls-files --error-unmatch -- .gitmodules >/dev/null 2>&1 || {
  echo "❌ submodule integrity: .gitmodules missing" >&2
  exit 1
}
while IFS= read -r path; do
  [[ -n "$path" ]] || continue
  # Compare the declared path field exactly: `grep -F` cannot anchor `$`, so
  # read the value column and match it whole rather than the raw config line.
  git config --file .gitmodules --get-regexp '^submodule\..*\.path$' \
    | awk '{print $2}' | grep -Fxq -- "$path" || {
    echo "❌ submodule integrity: undeclared gitlink $path" >&2
    exit 1
  }
done < <(printf '%s\n' "${gitlinks[@]}")
echo "✅ submodule integrity: ${#gitlinks[@]} tracked gitlink(s) validated"

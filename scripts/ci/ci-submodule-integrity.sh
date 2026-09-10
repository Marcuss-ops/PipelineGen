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
  git config --file .gitmodules --get-regexp '^submodule\..*\.path$' | grep -Fq " $path$" || {
    echo "❌ submodule integrity: undeclared gitlink $path" >&2
    exit 1
  }
done < <(printf '%s\n' "${gitlinks[@]}")
echo "✅ submodule integrity: ${#gitlinks[@]} tracked gitlink(s) validated"

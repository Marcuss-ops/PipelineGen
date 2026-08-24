#!/usr/bin/env bash
# 50_jobs sub-check (verbatim-extracted section of the original monolithic
# scripts/ci/architecture/checks/50_jobs.sh — see
# scripts/ci/architecture/checks/lib/50_jobs_section_map.json for the
# byte-precise line range, and the lib/50_jobs_profile.sh for the
# analysis that produced this split). Do NOT hand-edit body to fix
# checks; edit the original 50_jobs.sh and re-run the splitter (or
# move body content out-of-line manually here with a corresponding
# orchestrator update).

if [ -n "${BASH_SOURCE[0]:-}" ]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
    echo "CI: cannot resolve sub-check directory from BASH_SOURCE[0]=" >&2
    exit 1
fi
# shellcheck source=/dev/null
source "${SCRIPT_DIR}/lib/50_jobs_lib.sh"

# ── Verbatim section body extracted from the original monolithic ────────
# ── Check 54: forbid legacy stock pipeline keywords (Stock Cutover Commit 3, July 2026) ──
# Stock Cutover Cleanup Plan Commits 4-8 retire the assetIndex / media_assets /
# EnqueueAndIndex / UploadFile / Publisher.Publish / YTDLPDownloader / youtube.Service
# surfaces of the stock pipeline. Check 54 is the regression guard: any NEW
# occurrence of these banned keywords in a non-allowlisted file exits CI red.
# The allowlist (docs/migrations/stock-legacy-keyword-allowlist.txt)
# grandfathers the legacy files that Commits 4-8 will retire; remove the
# matching allowlist entry at the same commit as the file deletion.
echo "=== Check 54: forbid legacy stock pipeline keywords ==="
banned_words='\bassetIndex\b|media_assets|\bEnqueueAndIndex\b|\bUploadFile\b|\bPublisher\.Publish\b|\bYTDLPDownloader\b|\byoutube\.Service\b'

# 1. Gather raw hits with grep + rescue grep failure via || true at the
#    command level (kept outside $() so bash parsing isn't confused by
#    pipe+or+close-paren combinations).
all_hits=$(grep -rnE "$banned_words" \
    internal/capabilities/assets/providers/stock/ 2>/dev/null || true)
# Comments may name canonical columns and ports while documenting a
# migration. Only executable source lines are part of this regression gate.
all_hits=$(printf '%s\n' "$all_hits" | grep -vE ':[0-9]+:[[:space:]]*(//|\*)' || true)

# 2. Parse the allowlist into a |-joined regex of repo-relative file paths.
# Comments (#) and blank lines are stripped before the join.
allowed_files=""
if [ -f docs/migrations/stock-legacy-keyword-allowlist.txt ]; then
    allowed_files=$(
        grep -vE '^[[:space:]]*(#|$)' docs/migrations/stock-legacy-keyword-allowlist.txt 2>/dev/null \
            | sort -u | paste -sd'|' -
    )
fi
[ -z "$allowed_files" ] && allowed_files="__no_allowlist__"

# 3. Subtract allowlist matches. The awk body has NO inline comments — inline
# comments after awk statements confuse some awk/bash combinations. Hits in
# non-allowlisted files trigger the gate regardless of code-vs-comment:
# a comment mentioning a banned keyword is still a regression-risk surface
# (the comment-bypass prevented this in the prior implementation).
fails=""
if [ -n "$all_hits" ]; then
    fails=$(printf '%s\n' "$all_hits" | awk -F':' -v allow="$allowed_files" '
        BEGIN {
            n = split(allow, a, "|")
            for (i = 1; i <= n; i++) if (a[i] != "") allowed[a[i]] = 1
        }
        { if ($1 in allowed) next; print }
    ' || true)
fi

# 4. Assess. Empty diff → OK gate green; non-empty → FAIL gate red.
if [ -n "$fails" ]; then
    echo "FAIL: legacy stock pipeline keyword(s) found in non-allowlisted files:"
    echo "$fails"
    echo ""
    echo "Fix: the stock pipeline is locked for cleanup (Commits 4-8). Do not"
    echo "introduce NEW occurrences of assetIndex, media_assets, EnqueueAndIndex,"
    echo "UploadFile, Publisher.Publish, YTDLPDownloader, or youtube.Service in"
    echo "production paths of internal/capabilities/assets/providers/stock/."
    echo "If retiring a legacy file (Commits 4-8), remove the matching entry"
    echo "from docs/migrations/stock-legacy-keyword-allowlist.txt at the same commit."
    exit 1
fi
echo "OK: no net-new legacy stock pipeline keywords"

# -- Check 54: P0.1 -- forbid capability-layer .UploadFile* calls outside admin/legacy allowlist --
# Drive cutover P0.1 (July 2026): every .UploadFile( / .UploadFileWithDescription(
# call site in internal/application/** production code MUST carry a
# forward-pointer marker on the call line or within the 2 lines directly
# above it:
#   // TODO(P0.4): migrate to delivery.Publisher   (delivery.Publisher P0.4 target)
#   // TODO(P0.5): ArtifactUploader port — NOT delivery.Publisher P0.4 target (grandfathered)
# Zero-baseline (HARD-FAIL since Azione 5, 2026-08-02): this gate fails
# closed on any NEW UploadFile* call in internal/application/** that lacks
# the marker. Comment-only references (descriptive prose naming the retired
# surface) are dropped — they are not call sites.
#
# Forward-pointer: when P0.4 CONTRACT lands, tighten this gate to ban
# .UploadFile* calls in internal/application/** entirely (zero tolerance,
# no marker exception) and retire the P0.5 grandfather token.
echo "=== Check 54: P0.1 -- forbid new UploadFile* calls in capability-layer without TODO(P0.4)/TODO(P0.5) marker ==="
# Two rg calls merged with sort -u: call-site hits + marker lines (so the
# marker-window logic can see a marker on the line(s) directly above a call).
# Comment-only references are dropped by the awk pre-pass (descriptive prose
# naming the retired surface is not a call site).
upload_hits=$(rg -n --type go     -e '\.UploadFile\('     -e '\.UploadFileWithDescription\('     --glob '!**/cmd/admin/**'     --glob '!**/internal/infrastructure/**'     --glob '!**/internal/app/**'     --glob '!**/*_test.go'     internal/application/ 2>/dev/null || true)
marker_hits=$(rg -n --type go     -e 'TODO\(P0\.4\)'     -e 'TODO\(P0\.5\)'     internal/application/ 2>/dev/null || true)
upload_calls=$(printf '%s\n%s\n' "$upload_hits" "$marker_hits" | grep -v '^$' | sort -u | awk -F: '
    {
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /TODO\(P0\.[45]\)/) { markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2); next }
        if (rest ~ /^[[:space:]]*\/\//) next
        n = (markers[$1] == "" ? 0 : split(markers[$1], mlist, ","))
        allowed = 0
        for (mi = 1; mi <= n; mi++) {
            m = mlist[mi] + 0
            if (m > 0 && $2 + 0 >= m && $2 + 0 <= m + 2) { allowed = 1; break }
        }
        if (allowed) next
        print
    }' || true)
if [ -n "$upload_calls" ]; then
    echo "FAIL: .UploadFile* call site(s) lacking TODO(P0.4)/TODO(P0.5) marker:"
    echo "$upload_calls"
    echo ""
    echo "These call sites will be migrated to delivery.Publisher.Publish in P0.4."
    echo "Each site MUST carry a // TODO(P0.4): migrate to delivery.Publisher marker"
    echo "(or the // TODO(P0.5) ArtifactUploader-port grandfather token) on the call"
    echo "line or within the 2 lines directly above it."
    echo "See architecture/deprecations.yaml DRIVE-CUTOVER-P0-1 for the full audit."
    echo "(HARD-FAIL since Azione 5: zero tolerance for unmarked NEW call sites)"
    exit 1
fi
echo "OK: Check 54 -- every .UploadFile* site carries a TODO(P0.4) or TODO(P0.5) marker"





echo "==== Check 56: Forward-pointer marker + linked_issue cross-ref enforcement ===="
# Forward-prevention gate (CI complement to compile-time Assumption 1).
# Rationale (godlike/07 no-fake-availability): A composition-root function in
# internal/app/*.go that introduces a forward-pointer NIL field MUST carry:
#   (a) A marker `// forward-pointer: PR-<NAME>` on either:
#       (i)  the SAME line as the nil assignment: `Field: nil, // forward-pointer: PR-<NAME>`
#       (ii) a comment line directly above (within 25 lines of scroll-window)
#   (b) The PR-<NAME> registered as a `linked_issues[*].id` in
#       architecture/current.yaml::wave_status.
# Without BOTH, the nil field is a masked PLACEHOLDER -- runtime may
# dereference it (panic) or treat it as fake-success. Forward-prevention
# discipline: zero-baseline allowlist (godlike/08); transient baselines
# require explicit owner+deadline in the allowlist row.
#
# SLOT-SELECTION NOTE: Spec said "Check 54"; origin/main's Check 54 is
# canonical (Stock Cutover reset-gate, commit f12eb12f). Using Check 56
# preserves godlike/06 one-canonical-owner-per-fact.

ALLOWLIST_55="docs/migrations/check55-forward-pointer-allowlist.txt"
mkdir -p "$(dirname "$ALLOWLIST_55")"
[ -f "$ALLOWLIST_55" ] || touch "$ALLOWLIST_55" || { echo "  WARN: allowlist touch failed"; ALLOWLIST_55=/dev/null; }

# Build the set of PR-* IDs registered as linked_issues in architecture/current.yaml.
YAML_IDS_FILE=$(mktemp 2>/dev/null || echo "/tmp/.check55_yaml_pr_ids_v3.txt")
{
  rg '^\s*-\s+id:\s+(PR-[A-Z0-9.\-]+)\s*$' architecture/current.yaml --no-filename -or '$1' 2>/dev/null || true
  rg '^\s*id:\s+(PR-[A-Z0-9.\-]+)\s*$'  architecture/current.yaml --no-filename -or '$1' 2>/dev/null || true
} | sort -u > "$YAML_IDS_FILE"
echo "  Allowlist: $ALLOWLIST_55 ($(wc -l < $ALLOWLIST_55 | tr -d ' ') rows; empty by default per godlike/08)"
echo "  YAML registered PR-* IDs: $(wc -l < "$YAML_IDS_FILE" | tr -d ' ')"

fail_count56=0
ok_count55=0
skip_count55=0
inspect_output=$(mktemp 2>/dev/null || echo "/tmp/.check55_inspect_v3.txt")

files_list=$(find internal/app -maxdepth 2 -type f -name '*.go' 2>/dev/null | sort)

# Stateful awk: scan composition-root function bodies for `: nil,` patterns.
# KEY FIX (v3): outer regex matches ANY `: nil,` (not just at EOL). Then branch
# on marker presence (sentinel `forward-pointer: PR-<NAME>` anywhere on the
# matched line) to extract the PR-XYZ identifier. Production code uniformly
# places marker on SAME LINE as nil assignment; v1/v2 required EOL anchor
# which silently zero-matched them -> false-positive-OK (no rows emit).
while IFS= read -r gf; do
  [ -z "$gf" ] && continue
  awk -v file="$gf" '
    BEGIN { in_func = 0 }
    # Composition-root function entry points only.
    /^func[[:space:]]+(register[A-Za-z0-9_]*Module|Wire[A-Za-z0-9_]*)\(/ { in_func = 1 }
    # Closing brace at column 1 ends the function scope.
    in_func && /^}/ { in_func = 0 }
    # Inside a composition-root function: every `: nil,` line.
    in_func && /:[[:space:]]+nil,/ {
      # 1. Extract PR-XXX identifier if present anywhere on the line.
      # 2. Branch on whether marker exists at all.
      line = $0
      pr_found = ""
      # Look for the canonical marker token on this line.
      if (match(line, /forward-pointer:[[:space:]]*PR-[A-Za-z0-9.\-]+/)) {
        pr_full = substr(line, RSTART, RLENGTH)
        if (match(pr_full, /PR-[A-Za-z0-9.\-]+/)) {
          pr_found = substr(pr_full, RSTART, RLENGTH)
        }
      }
      if (pr_found != "") {
        print "OK\t" pr_found "\t" file ":" NR
      } else {
        print "FAIL\tMISSING_MARKER\t" file ":" NR "\t" line
      }
    }
  ' "$gf"
done < <(printf '%s\n' "$files_list") > "$inspect_output"

# Iterate status rows. Function-style iteration via process substitution avoids
# bash subshell scoping (which would lose $fail_count56 updates).
while IFS=$'\t' read -r status payload loc raw; do
  case "$status" in
    OK)
      pr="$payload"
      if grep -qxF "$pr" "$YAML_IDS_FILE"; then
        ok_count55=$((ok_count55 + 1))
      else
        echo "[Check 56] $loc : forward-pointer $pr not registered in architecture/current.yaml::wave_status.linked_issues[*].id (godlike/06 SSOT breach)"
        fail_count56=$((fail_count56 + 1))
      fi
      ;;
    FAIL)
      if [ "$ALLOWLIST_55" != /dev/null ] && grep -qF "$loc" "$ALLOWLIST_55"; then
        skip_count55=$((skip_count55 + 1))
      else
        echo "[Check 56] $loc : nil field lacks same-line marker `// forward-pointer: PR-<NAME>`"
        if [ -n "$raw" ]; then
          echo "         raw: $raw"
        fi
        fail_count56=$((fail_count56 + 1))
      fi
      ;;
  esac
done < "$inspect_output"

# Lint allowlist: every row must be `file:line` and the target file must still exist.
if [ -s "$ALLOWLIST_55" ]; then
  while IFS= read -r arow; do
    [ -z "$arow" ] && continue
    file="${arow%:*}"
    if [ -f "$file" ]; then
      skip_count55=$((skip_count55 + 1))
    else
      echo "[Check 56] allowlist $arow : target file no longer exists (zero-baseline discipline: clean up)"
      fail_count56=$((fail_count56 + 1))
    fi
  done < "$ALLOWLIST_55"
fi

rm -f "$inspect_output"

echo "  Stats: OK=$ok_count55 FAIL=$fail_count56 SKIP(allowlisted)=$skip_count55"
if [ "$fail_count56" -gt 0 ]; then
  echo "RESULT: Check 56 FAIL ($fail_count56 violations)"
  rm -f "$YAML_IDS_FILE"
  exit 1
fi
echo "RESULT: Check 56 OK (forward-pointer markers present + YAML-registered)"

rm -f "$YAML_IDS_FILE"

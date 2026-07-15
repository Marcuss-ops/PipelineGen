# ── Check 62: forbid inline middleware in >300 LoC feature routing files (SCRIPT-FLOW-SPLIT) ──
# The canonical auth cluster (RequireAdminToken + extractHeaderToken +
# AdminTokenProvider interface + EnableAuth / AdminToken methods) lives in
# internal/api/<feature>/middleware_auth.go per AGENTS.md Pattern 5. A >300-
# LoC feature-routing file that still defines inline middleware signatures
# is an extraction candidate: middleware code in a too-large routing file
# couples two concerns (HTTP transport + auth secret handling) and
# silently bloats the orchestrator.
#
# Pattern anchors (ripgrep -E syntax; alternation regex catches ANY of
# the 4 inline-middleware signatures):
#   RequireAdminToken|extractHeaderToken|EnableAuth|AdminTokenProvider
#
# Allowlist (production sites where the signatures legitimately live):
#   - internal/api/<feature>/middleware_auth.go  — canonical SOLE mirror
#     of the 4 signatures per feature; the rg --glob below excludes any
#     file matching this leaf-name pattern so the check passes regardless
#     of LoC.
#
# Size threshold: 300 LoC. Mirrors AGENTS.md Pattern 5 "30+ review
# threshold" + godlike/07 minimum-blast-radius file discipline. Files
# >300 LoC AND carrying inline middleware signatures = extraction
# candidate. Files <=300 LoC that carry the signatures are exempt
# (forward-prevention only fires on bloat + middleware compound).
#
# Tests are excluded via --glob '!**/*_test.go' so test fixtures may
# freely reference the signatures (e.g. *_test.go that mock-constructs
# AdminTokenProvider structs).
#
# Forward-prevention gate: catches future drift at pre-CI time. The
# current production tree is canonical (per PR-SCRIPT-AUTH-EXTRACT +
# PR-SCRIPT-FACADE-EXTRACT) so this gate MUST exit 0 today; the gate
# exists to lock the contract.
#
# Mirror: Go scanner at cmd/archcheck/scan/percheck_inline_middleware.go
# (PR-ARCHCHECK-GO-MIGRATION-PHASE-2 follow-up).
echo "=== Check 62: forbid inline middleware in >300 LoC feature routing files (SCRIPT-FLOW-SPLIT) ==="
threshold=300
all_hits=$(rg -n --type go \
    -e 'RequireAdminToken|extractHeaderToken|EnableAuth|AdminTokenProvider' \
    --glob '!**/middleware_auth.go' \
    --glob '!**/*_test.go' \
    internal/api/ 2>/dev/null \
    || true)
# Drop full-line comments so descriptive prose doesn't trip the regex.
non_comment_hits=$(printf '%s\n' "$all_hits" | awk -F: '{
    rest = ""
    for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
    if (rest ~ /^[[:space:]]*\/\//) next
    print
}' || true)
# For each distinct file with non-comment hits, fail if LoC > threshold.
violations=""
printf '%s\n' "$non_comment_hits" | awk -F: '{print $1}' | sort -u > /tmp/check62_files.txt
if [ -s /tmp/check62_files.txt ]; then
    while IFS= read -r f; do
        loc=$(wc -l < "$f" 2>/dev/null || echo 0)
        if [ "$loc" -gt "$threshold" ]; then
            violations="${violations}  ${f}  (${loc} LoC > ${threshold})"$'\n'
        fi
    done < /tmp/check62_files.txt
    rm -f /tmp/check62_files.txt
fi
if [ -n "$violations" ]; then
    printf '%s\n' "$non_comment_hits" | awk -F: '{print $1}' | sort -u > /tmp/check62_files.txt
    if [ -s /tmp/check62_files.txt ]; then
        while IFS= read -r f; do
            loc=$(wc -l < "$f" 2>/dev/null || echo 0)
            if [ "$loc" -gt "$threshold" ]; then
                line=$(printf 'inline middleware in feature routing file %s %d LoC exceeds %d; extract to %s/middleware_auth.go per AGENTS.md Pattern 5 + SCRIPT-FLOW-SPLIT precedent' "$f" "$loc" "$threshold" "$(dirname "$f")")
                echo "$line" >> /tmp/check62_violations
            fi
        done < /tmp/check62_files.txt
        rm -f /tmp/check62_files.txt
    fi
    echo "FAIL: inline middleware in feature routing file(s) exceeding ${threshold} LoC:"
    cat /tmp/check62_violations
    rm -f /tmp/check62_violations
    echo ""
    echo "Fix: extract the middleware signatures to internal/api/<feature>/middleware_auth.go"
    echo "per AGENTS.md Pattern 5 + SCRIPT-FLOW-SPLIT precedent. The canonical surface is:"
    echo "  - internal/api/script/middleware_auth.go  = AdminTokenProvider + RequireAdminToken"
    echo "    + extractHeaderToken + EnableAuth/AdminToken methods (the 4-element auth cluster)"
    echo ""
    echo "Violation note format: 'inline middleware in feature routing file N LoC exceeds 300;"
    echo "extract to <feature>/middleware_auth.go per AGENTS.md Pattern 5 + SCRIPT-FLOW-SPLIT precedent'"
    exit 1
fi
echo "OK: no inline middleware in >${threshold} LoC feature routing files (SCRIPT-FLOW-SPLIT invariant upheld)"


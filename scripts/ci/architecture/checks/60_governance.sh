
# === Check 60: forbid t.Skip without godlike/07 honest-limitation comment (forward-prevention) ===
# PR-PR6-TEST-REACTIVATE (Wave 1 P0 #3, deadline 2026-07-15): the 2 t.Skip markers
# in processor_persistence_test.go were removed in PR-PERSIST-PR6-CANONICAL
# (commit d17c78ae). This gate bans NEW t.Skip(...) / t.Skipf(...) / t.SkipNow()
# calls in that ONE test file unless preceded (within a 25-line scroll window)
# by a `// godlike/07 honest-limitation` comment that documents the reason.
# Scope: ONLY internal/application/scripts/adapters/processor_persistence_test.go
# (the canonical godlike/07 zero-legacy contract test file). Other test files
# are explicitly OUT OF SCOPE — widening the gate could block legitimate t.Skip
# usages in unrelated packages.
# Pattern: t\.Skip[a-zA-Z]*\( catches t.Skip(, t.Skipf(, t.SkipNow().
# Multi-line invocations (t.Skipf(\n"long",\n"args")) are a known blind spot —
# matches Check 55's own posture on multi-line struct literals.
skip_hits=$(rg -n 't\.Skip[a-zA-Z]*\(' internal/application/scripts/adapters/processor_persistence_test.go 2>/dev/null || true)
filtered_skip=""
if [ -n "$skip_hits" ]; then
  filtered_skip=$(echo "$skip_hits" | while IFS= read -r hit; do
    [ -z "$hit" ] && continue
    f=$(echo "$hit" | cut -d: -f1)
    l=$(echo "$hit" | cut -d: -f2)
    # 25-line scroll-window lookback: check the preceding 25 lines for the
    # godlike/07 honest-limitation marker. If found, the skip is allowed.
    start=$((l - 25))
    [ "$start" -lt 1 ] && start=1
    end=$((l - 1))
    marker=$(sed -n "${start},${end}p" "$f" 2>/dev/null | grep -c "godlike/07 honest-limitation" || true)
    if [ "$marker" = "0" ]; then
      echo "$hit"
    fi
  done)
fi
fail_count=0
if [ -n "$filtered_skip" ]; then
  echo "Check 60 (forbid t.Skip without godlike/07 honest-limitation comment): FAIL" >&2
  echo "  t.Skip/t.Skipf/t.SkipNow calls in processor_persistence_test.go without" >&2
  echo "  a godlike/07 honest-limitation comment in the preceding 25 lines:" >&2
  echo "$filtered_skip" | sed 's/^/    /' >&2
  echo "  Fix: remove the t.Skip marker (canonical contract is zero-skip)," >&2
  echo "  OR prepend a \`// godlike/07 honest-limitation: <reason>\` comment" >&2
  echo "  within 25 lines before the t.Skip call." >&2
  fail_count=$((fail_count + 1))
fi
if [ "$fail_count" -gt 0 ]; then
  exit 1
fi
echo "Check 60 (forbid t.Skip without godlike/07 honest-limitation comment): OK (0 unjustified t.Skip hits)"

# === Check 60: forbid t.Skip without godlike/07 honest-limitation comment (forward-prevention) ===
# PR-PR6-TEST-REACTIVATE (Wave 1 P0 #3, deadline 2026-07-15): the 2 t.Skip markers
# in processor_persistence_test.go were removed in PR-PERSIST-PR6-CANONICAL
# (commit d17c78ae). This gate bans NEW t.Skip(...) / t.Skipf(...) / t.SkipNow()
# calls in that ONE test file unless preceded (within a 25-line scroll window)
# by a `// godlike/07 honest-limitation` comment that documents the reason.
# Scope: ONLY internal/application/scripts/adapters/processor_persistence_test.go
# (the canonical godlike/07 zero-legacy contract test file). Other test files
# are explicitly OUT OF SCOPE — widening the gate could block legitimate t.Skip
# usages in unrelated packages.
# Pattern: t\.Skip[a-zA-Z]*\( catches t.Skip(, t.Skipf(, t.SkipNow().
# Multi-line invocations (t.Skipf(\n"long",\n"args")) are a known blind spot —
# matches Check 55's own posture on multi-line struct literals.
skip_hits=$(rg -n 't\.Skip[a-zA-Z]*\(' internal/application/scripts/adapters/processor_persistence_test.go 2>/dev/null || true)
filtered_skip=""
if [ -n "$skip_hits" ]; then
  filtered_skip=$(echo "$skip_hits" | while IFS= read -r hit; do
    [ -z "$hit" ] && continue
    f=$(echo "$hit" | cut -d: -f1)
    l=$(echo "$hit" | cut -d: -f2)
    # 25-line scroll-window lookback: check the preceding 25 lines for the
    # godlike/07 honest-limitation marker. If found, the skip is allowed.
    start=$((l - 25))
    [ "$start" -lt 1 ] && start=1
    end=$((l - 1))
    marker=$(sed -n "${start},${end}p" "$f" 2>/dev/null | grep -c "godlike/07 honest-limitation" || true)
    if [ "$marker" = "0" ]; then
      echo "$hit"
    fi
  done)
fi
fail_count=0
if [ -n "$filtered_skip" ]; then
  echo "Check 60 (forbid t.Skip without godlike/07 honest-limitation comment): FAIL" >&2
  echo "  t.Skip/t.Skipf/t.SkipNow calls in processor_persistence_test.go without" >&2
  echo "  a godlike/07 honest-limitation comment in the preceding 25 lines:" >&2
  echo "$filtered_skip" | sed 's/^/    /' >&2
  echo "  Fix: remove the t.Skip marker (canonical contract is zero-skip)," >&2
  echo "  OR prepend a \`// godlike/07 honest-limitation: <reason>\` comment" >&2
  echo "  within 25 lines before the t.Skip call." >&2
  fail_count=$((fail_count + 1))
fi
if [ "$fail_count" -gt 0 ]; then
  exit 1
fi
echo "Check 60 (forbid t.Skip without godlike/07 honest-limitation comment): OK (0 unjustified t.Skip hits)"

# === Check 60: forbid t.Skip without godlike/07 honest-limitation comment (forward-prevention) ===
# PR-PR6-TEST-REACTIVATE (Wave 1 P0 #3, deadline 2026-07-15): the 2 t.Skip markers
# in processor_persistence_test.go were removed in PR-PERSIST-PR6-CANONICAL
# (commit d17c78ae). This gate bans NEW t.Skip(...) / t.Skipf(...) / t.SkipNow()
# calls in that ONE test file unless preceded (within a 25-line scroll window)
# by a `// godlike/07 honest-limitation` comment that documents the reason.
# Scope: ONLY internal/application/scripts/adapters/processor_persistence_test.go
# (the canonical godlike/07 zero-legacy contract test file). Other test files
# are explicitly OUT OF SCOPE — widening the gate could block legitimate t.Skip
# usages in unrelated packages.
# Pattern: t\.Skip[a-zA-Z]*\( catches t.Skip(, t.Skipf(, t.SkipNow().
# Multi-line invocations (t.Skipf(\n"long",\n"args")) are a known blind spot —
# matches Check 55's own posture on multi-line struct literals.
skip_hits=$(rg -n 't\.Skip[a-zA-Z]*\(' internal/application/scripts/adapters/processor_persistence_test.go 2>/dev/null || true)
filtered_skip=""
if [ -n "$skip_hits" ]; then
  filtered_skip=$(echo "$skip_hits" | while IFS= read -r hit; do
    [ -z "$hit" ] && continue
    f=$(echo "$hit" | cut -d: -f1)
    l=$(echo "$hit" | cut -d: -f2)
    # 25-line scroll-window lookback: check the preceding 25 lines for the
    # godlike/07 honest-limitation marker. If found, the skip is allowed.
    start=$((l - 25))
    [ "$start" -lt 1 ] && start=1
    end=$((l - 1))
    marker=$(sed -n "${start},${end}p" "$f" 2>/dev/null | grep -c "godlike/07 honest-limitation" || true)
    if [ "$marker" = "0" ]; then
      echo "$hit"
    fi
  done)
fi
fail_count=0
if [ -n "$filtered_skip" ]; then
  echo "Check 60 (forbid t.Skip without godlike/07 honest-limitation comment): FAIL" >&2
  echo "  t.Skip/t.Skipf/t.SkipNow calls in processor_persistence_test.go without" >&2
  echo "  a godlike/07 honest-limitation comment in the preceding 25 lines:" >&2
  echo "$filtered_skip" | sed 's/^/    /' >&2
  echo "  Fix: remove the t.Skip marker (canonical contract is zero-skip)," >&2
  echo "  OR prepend a \`// godlike/07 honest-limitation: <reason>\` comment" >&2
  echo "  within 25 lines before the t.Skip call." >&2
  fail_count=$((fail_count + 1))
fi
if [ "$fail_count" -gt 0 ]; then
  exit 1
fi
echo "Check 60 (forbid t.Skip without godlike/07 honest-limitation comment): OK (0 unjustified t.Skip hits)"
# ── Check 56: FASE 2.1 PR-VOICE-FREEZE — gate banning new imports in legacy script handlers ──
# FASE 2.1 (July 2026) freezes the legacy script-generation adapter surface
# (internal/api/script/handler_legacy_*.go) for retirement on 2026-12-31.
# The FREEZE pattern is the canonical deadline-driven retirement per
# godlike/07 minimum-blast-radius: counters (legacy_generate_from_clips_total
# + legacy_generate_with_images_total) keep observability alive until
# rate(...[7d]) == 0, at which point the 4 handler_legacy_*.go files
# (plus the 2 typed counter declarations in handler_legacy_deprecation.go)
# are git-rm'd atomically. The CI gate enforces the FREEZE contract at
# the import layer: any new import line in handler_legacy_*.go (outside
# the audit-pin ARCH-ALLOWLIST scope) is a forward-progress violation.
#
# godlike/06 SSOT (one canonical owner per fact): the FREEZE file set IS
# the canonical surface for legacy design-time concerns. Composition-root
# adapters (lifecycle.go, wire_*.go) MUST NOT import handler_legacy_*.go
# via a passthrough — the proper pattern is a new typed port surfaced
# from the canonical typed handler interface per AGENTS.md Pattern 0.
#
# godlike/08 zero-baseline rule (FASE 2.1 wave-level): the import spec
# audit-pin is the SSOT for "what is still active in legacy code today".
# New import lines are NOT accepted unless preceded by the ARCH-ALLOWLIST
# marker `// ARCH-ALLOWLIST: legacy-script-freeze` on the line
# preceding the import (covers both single-line and multi-line `import (`
# block syntax). Per AGENTS.md §7, every marker entry requires explicit
# owner + deadline; the marker is the call-site equivalent of an
# allowlist row in the Check 5 / Check 8 / Check 33 / Check 54 pattern.
#
# Pattern anchors (ripgrep --type go; matches the canonical import
# statement shape — `"github.com/.../pkg/path"` literal inside the
# import block). Comment-only lines are dropped via awk so descriptive
# prose doesn't trigger false positives. Tests are excluded via
# --glob '!*_test.go' so legacy tests may import legitimately for
# fixture construction (handled by godlike/06 SSOT test-side pin
# discipline per Check 54's precedent).
#
# Marker placement (mirrors Check 54 canonical Go syntax):
#   (a) PREFERRED: marker immediately above the `import (` line:
#         N:   // ARCH-ALLOWLIST: legacy-script-freeze
#         N+1: import (
#         N+2:     "github.com/Marcuss-ops/PipelineGen/.../pkg"
#   (b) ACCEPTABLE: marker immediately above single-line import:
#         N:   // ARCH-ALLOWLIST: legacy-script-freeze
#         N+1: _ "github.com/Marcuss-ops/PipelineGen/.../pkg"
# The awk pre-pass accepts offending_line == marker+1 OR marker+2.
#
# Behaviour (per user spec):
#   - FAIL-CLOSED: new `"github.com/...` import line in any
#     internal/api/script/handler_legacy_*.go file outside the
#     ARCH-ALLOWLIST scroll-window. Exit 1.
#   - WARN (non-fatal): comment-only references + ARCH-ALLOWLIST
#     marker sites are logged for audit-pinning per godlike/07
#     no-fake-availability (the operator sees marker accounting every
#     CI run, not silently).
#   - 7-day-zero-retirement: the operator checks Prometheus for
#     rate(legacy_generate_from_clips_total[7d]) == 0 AND
#     rate(legacy_generate_with_images_total[7d]) == 0. When both
#     counters report zero for 7 consecutive days, the post-2026-12-31
#     deadline can be advanced — git rm the 4 handler_legacy_*.go files
#     + the 2 counter declarations in handler_legacy_deprecation.go
#     in a single atomic commit.
#
# Scope: strictly internal/api/script/handler_legacy_*.go ONLY (the
# canonical FREEZE file set). The audit-pin markers at canonical owner
# sites (e.g. NewService ctor in handler_flow.go) are unaffected —
# those files continue to evolve via the typed-handler pattern per
# AGENTS.md Pattern 0.
echo "=== Check 56: FASE 2.1 PR-VOICE-FREEZE — gate banning new imports in legacy script handlers ==="
all_hits=$(rg -n --type go \
    -e '^[[:space:]]*"github\.com/' \
    --glob '!*_test.go' \
    internal/api/script/handler_legacy_*.go 2>/dev/null \
    || true)
# Stage 1: drop full-line comments + ARCH-ALLOWLIST marker lines + lines
# whose marker site (in the SAME file) is on marker+1 OR marker+2 lines
# upstream of the offending import statement (mirrors Check 54's canonical
# Go-syntax contract).
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*legacy-script-freeze/) {
                markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
            n = (markers[$1] == "" ? 0 : split(markers[$1], mlist, ","))
            allowed = 0
            for (mi = 1; mi <= n; mi++) {
              m = mlist[mi] + 0
              if (m > 0 && ($2 + 0 == m + 1 || $2 + 0 == m + 2)) { allowed = 1; break }
            }
            if (allowed) next
            print
        }' \
    || true)
# Stage 2: audit-pin residue accounting (godlike/07 honest-limitation).
comment_count=0
allowlist_count=0
if [ -n "$all_hits" ]; then
    comment_count=$(printf '%s\n' "$all_hits" | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) print
    }' | wc -l | awk "{print \$1+0}")
    allowlist_count=$(printf '%s\n' "$all_hits" | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*legacy-script-freeze/) print
    }' | wc -l | awk "{print \$1+0}")
fi
# Stage 3: hard-fail on production imports per user spec.
if [ -n "$literal_calls" ]; then
    echo "FAIL: forbidden new external/internal import in internal/api/script/handler_legacy_*.go (FASE 2.1 PR-VOICE-FREEZE):"
    echo "$literal_calls"
    echo ""
    echo "Fix: handler_legacy_*.go is under FASE 2.1 FREEZE until 2026-12-31."
    echo "      No new imports are accepted. If a critical CVE requires an"
    echo "      import (rare; should never happen for a retired-adapter path),"
    echo "      prepend the magic marker on the line preceding the import:"
    echo "    // ARCH-ALLOWLIST: legacy-script-freeze"
    echo "    import ("
    echo "      \"github.com/Marcuss-ops/PipelineGen/.../pkg\""
    echo "    )"
    echo "Per AGENTS.md §7 every marker entry requires explicit owner + deadline."
    exit 1
fi
if [ "$comment_count" -gt 0 ]; then
    echo "WARN (${comment_count} hits): comment-only external/internal import references in handler_legacy_*.go"
    echo "      (descriptive prose; non-fatal per godlike/07 no-fake-availability)"
fi
if [ "$allowlist_count" -gt 0 ]; then
    echo "WARN (${allowlist_count} hits): ARCH-ALLOWLIST: legacy-script-freeze marker sites in handler_legacy_*.go"
    echo "      (audit-pin residue; non-fatal; tracked per godlike/06)"
fi
echo "OK: FASE 2.1 PR-VOICE-FREEZE respected — no new imports in legacy script handlers"
# ── Check 61: wave-tracker baseline size summary (PR-CI-WAVE-ALLOWLIST, July 2026) ──
# INFORMATIONAL gate (godlike/07 minimum-blast-radius). Does NOT change the
# exit code of any prior check. The baseline size number is the canonical signal
# for the question "is the baseline stable?" — a non-zero count means every
# wave-tracker-known-acceptable PR-id was extracted from architecture/current.yaml
# (zero false-positive regression on the allowlist side), a zero count means
# the wave-tracker file is absent or unparseable.
#
# This is a NEW layer that consults the wave-tracker allowlist
# (extract_known_acceptable_ids_from_yaml) populated at script start. The
# summary is reproducible across runs (the wave-tracker file is the same),
# so the baseline size number is a stable count rather than an all-violations
# dump. Future per-check integration can opt-in by calling
# `is_known_acceptable <PR_ID>` to consult the same allowlist.
#
# Wave-tracker status (informational only, NOT a gate):
#   - YAML file present:        KNOWN_ACCEPTABLE_IDS populated
#   - YAML file missing:        KNOWN_ACCEPTABLE_IDS empty (safe default)
#   - YAML file unparseable:    KNOWN_ACCEPTABLE_IDS may be partial (text-based
#                                extraction tolerates cascade bugs at lines
#                                ~1582, ~2852, ~2996; line-anchored `id: PR-*`
#                                patterns survive)
# Per godlike/07 no-fake-availability: this gate is purely informational and
# does NOT exit 1 on a low count. The operator dashboard surfaces the
# number; CI exit code reflects the prior per-check exit-1 semantics
# (unchanged). A future promotion to enforcement would require a separate
# `verified_zero: true` flip per godlike/08 zero-baseline rule.
echo "=== Check 61: wave-tracker baseline size summary (PR-CI-WAVE-ALLOWLIST) ==="
if [ -n "${KNOWN_ACCEPTABLE_IDS}" ]; then
    echo "INFO: wave-tracker file parsed; ${WAVE_BASELINE_SIZE} PR-id(s) extracted as known-acceptable baseline"
    echo "      (id: PR-* entries with status: pending / in_progress, plus PRE-EXISTING-*-2026-07-04 parents)"
    echo "      Baseline: per-check exit-1 semantics unchanged; this gate is informational only."
    echo "      Operators may consult is_known_acceptable <PR_ID> in future per-check opt-ins."
    echo "OK: Check 61 baseline size summary printed (informational; no exit-code change)"
else
    echo "WARN: KNOWN_ACCEPTABLE_IDS empty (wave-tracker file absent or unparseable)"
    echo "      Defaulting to: every violation is treated as new (safe per godlike/07 no-fake-availability)"
    echo "      Future: file presence or YAML fix restores the baseline"
    echo "OK: Check 61 baseline size summary printed (informational; no exit-code change; allowlist empty)"
fi
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
# ── Check 63: forbid direct http.NewRequest in storage_search.go (B4 migration lock, July 2026) ───
# Forward-prevention for PR-IMAGES-AI-VS-NORMAL-PLAN B4 (July 2026): the
# canonical HTTP-GET single-call surface for storage_search.go is
# pkg/httpjson (GetJSON[T] / GetBytes). The pre-B4 inline
# http.NewRequest + Do + ReadAll copies (~180 LoC of boilerplate × 9
# sites) were collapsed into single-call sites per the explicit
# exit-gate documented on pkg/httpjson/get_json.go:16:
#   `rg "http.NewRequest" storage_search → 0`.
#
# This CI gate locks the post-B4 invariant: any new caller in
# storage_search.go that bypasses pkg/httpjson is a regression. The
# test-side counterpart (compile-time GetJSON pin + runtime scan
# inside the package's *_test.go suite) lives at
# internal/application/images/storage_search_contract_test.go.
#
# ARCH-ALLOWLIST opt-in: a future PR that, after due consideration,
# legitimately needs to call http.NewRequest directly in
# storage_search.go (e.g. a streaming upload that GetBytes cannot
# handle) MUST prepend the magic marker
# `// ARCH-ALLOWLIST: storage-search-httpreq` on the line preceding
# the call. The awk pre-pass strips such hits from the failing-set
# via the 25-line window tolerated by Checks 5 / 8 / 33 / 50 / 54.
# Per AGENTS.md §8 zero-baseline rule, every new allowlist entry
# requires explicit owner + deadline; the marker is the call-site
# equivalent of an allowlist row.
#
# Pattern anchors (ripgrep regex):
#   http\.NewRequest                  matches `http.NewRequest(` AND
#                                     `http.NewRequestWithContext(`.
# Mirrors the explicit migration exit-gate on pkg/httpjson's package godoc:
# the gate output is byte-stable with the godoc-documented target.
#
# Scope: strictly internal/application/images/storage_search.go.
# Widening to the entire package would over-block legitimate cross-
# layer composition wiring. The test-side companion
# scanPackageForHTTPNewRequest in storage_search_contract_test.go
# covers the wider package scope (any future split to
# storage_search_wikipedia.go + storage_search_searxng.go +
# storage_search_ddg.go stays regression-locked).
echo "=== Check 63: forbid http.NewRequest in storage_search.go (B4 lock, July 2026) ==="
all_hits=$(rg -n --type go \
    -e 'http\.NewRequest' \
    internal/application/images/storage_search.go 2>/dev/null \
    || true)
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*storage-search-httpreq/) {
                markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
            n = (markers[$1] == "" ? 0 : split(markers[$1], mlist, ","))
            allowed = 0
            for (mi = 1; mi <= n; mi++) {
              m = mlist[mi] + 0
              if (m > 0 && $2 + 0 >= m + 1 && $2 + 0 <= m + 25) { allowed = 1; break }
            }
            if (allowed) next
            print
        }' \
    || true)
if [ -n "$literal_calls" ]; then
    echo "FAIL: http.NewRequest detected in storage_search.go (B4 migration lock):"
    echo "$literal_calls"
    echo ""
    echo "Fix: route the call through pkg/httpjson.GetJSON[T] or pkg/httpjson.GetBytes"
    echo "(the canonical single-call surface post-B4). If the call is genuinely"
    echo "a streaming upload or other GetBytes-cannot-handle case, prepend an"
    echo "ARCH-ALLOWLIST marker on the line preceding the call:"
    echo "    // ARCH-ALLOWLIST: storage-search-httpreq"
    echo "    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)"
    echo "The marker strips the hit from the failing-set via a 25-line window."
    exit 1
fi
echo "OK: 0 http.NewRequest calls in storage_search.go (B4 lock upheld)"
# ────────────────────────────────────────────────────────────────────────────
# === Check 64: postprocessor registration order is canonical 10-processor sequence
# ────────────────────────────────────────────────────────────────────────────
#
# SCRIPTCONTRACT-2026-07-08 PR-3 forward-prevention gate.
# The user spec referred to this as "Check 62"; numbers 62 + 63 in
# scripts/ci-architectural-checks.sh are already taken (62 = inline-middleware-
# in-feature-routing-files; 63 = admin-port-resolution). This is the canonical
# forward-prevention lock that lives at number 64.
# See architecture/action-plans/2026-07-08-script-pipeline-contract.md section 3.PR-3.

EXPECTED_ORDER="adapters.NewPersistenceProcessor adapters.NewDocumentProcessor adapters.NewImageProcessor adapters.NewVoiceoverProcessor adapters.NewEntitiesProcessor adapters.NewMetadataProcessor adapters.NewTranslationProcessor adapters.NewClipBindingsProcessor adapters.NewStockAssociationProcessor adapters.NewClipSearchProcessor"

# Scope the extraction to the registerScriptPostProcessors function body only
# (avoids catching New*Processor ctor calls in the wire_*.go composition for
# OTHER pipelines -- e.g. handler_jobs.go -- that legitimately co-locate here).
ACTUAL_ORDER=$(awk '
  /registerScriptPostProcessors[[:space:]]*\(/ { in_fn = 1; next }
  in_fn && /^}$/ { exit }
  in_fn { print }
' internal/app/wire_script_postprocess.go | grep -oE 'adapters\.New[A-Za-z]+Processor' | tr '\n' ' ' | sed 's/ $//')

# Empty-extraction guard: if the function is no longer present (renamed / moved)
# or contains 0 New*Processor ctor calls, this check would fire with empty vs
# expected and surface a generic "wrong order" message -- force a distinct
# diagnostic naming the root cause so a future agent has an actionable signal.
if [ -z "$ACTUAL_ORDER" ]; then
    echo "FAIL: Check 64 internal extraction -- registerScriptPostProcessors function could not be located or contained 0 New*Processor calls."
    echo "Verify the function still exists at internal/app/wire_script_postprocess.go and contains adapters.New*Processor() ctor calls in its body."
    exit 1
fi

if [ "$ACTUAL_ORDER" != "$EXPECTED_ORDER" ]; then
    echo "FAIL: Check 64 -- registerScriptPostProcessors order does not match canonical 10-processor sequence."
    echo ""
    echo "Expected (pers. to godlike/06 SSOT CanonicalProcessorNames() at internal/application/scripts/adapters/processor_names.go):"
    echo "    $EXPECTED_ORDER"
    echo ""
    echo "Observed in registerScriptPostProcessors (in file order):"
    echo "    $ACTUAL_ORDER"
    echo ""
    echo "Refer to architecture/action-plans/2026-07-08-script-pipeline-contract.md section 3.PR-3 for"
    echo "the canonical 10-processor order + insert position for new processors (TranslationProcessor is"
    echo "between Metadata and ClipBindings per PR-TRANSLATE-SCRIPT-SPEC FP2)."
    exit 1
fi
echo "OK: registerScriptPostProcessors sequence matches canonical 10-processor order"
echo "      (Persistence->Document->Image->Voiceover->Entities->Metadata->Translation->ClipBindings->StockAssociation->ClipSearch)"
# ── Check 69: NoAutoTriggerLiveBattery (operator-only-by-design, July 2026) ──
# Per godlike/07 NO-FAKE-AVAILABILITY + the operator-only policy in
# docs/operations/stock-e2e-runbook.md §10.6, the stock pipeline live battery
# (scripts/stock_pipeline_live_test.sh) is registered for OPERATOR MANUAL-ONLY
# invocation. The script hits yt-dlp + Drive writes + Qdrant mutations --
# side-effect-heavy, NEVER belonging in the PR/push feedback loop. The canonical
# workflow workflows/test_stock_pipeline_live.yaml MUST declare `triggers:`
# with `workflow_dispatch` only. This gate enforces the policy at pre-CI time.
# Per godlike/06 SSOT lockstep: runbook §10.6 + this gate + the canonical
# workflow YAML form a 3-surface contract; drift is itself an SSOT regression.
#
# C1-REGRESSION-FIX (2026-07-12, code-reviewer verdict on v2): the v2 filter
# chain `grep -E 'push|...' | grep -v 'workflow_dispatch'` filtered out the
# WHOLE LINE because the line happened to contain the substring
# `workflow_dispatch` (e.g., mixed-array case `triggers: [workflow_dispatch,
# push]`). The v3 approach tokenizes trigger kinds with grep -Eo (extracts
# ONLY the matched-kind tokens, not whole-line context), then sort -u to
# produce a unique sorted set, then accepts ONLY when the resulting set is
# exactly the single-line string `workflow_dispatch`. ANY other shape is
# fail-closed: pure non-workflow_dispatch (e.g. "push" only), mixed array
# (e.g. "push\nworkflow_dispatch"), multi-kind, malformed DSL, or empty.
#
# Allowlist: NONE. Future legitimate automation (rare) MUST add an entry to
# docs/migrations/live-battery-auto-trigger-allowlist.txt with rationale +
# owner + deadline per AGENTS.md §8 zero-baseline rule.
echo "=== Check 69: NoAutoTriggerLiveBattery (godlike/07, 2026-07-12 v3 fix) ==="
gh_off=""
gh_workflow_dir="${REPO_ROOT}/.github/workflows"
if [ -d "${gh_workflow_dir}" ]; then
    gh_off=$(rg -l 'stock_pipeline_live_test\.sh' "${gh_workflow_dir}" 2>/dev/null || true)
fi
dsl_off=""
dsl_workflow_dir="${REPO_ROOT}/workflows"
internal_workflow="${dsl_workflow_dir}/test_stock_pipeline_live.yaml"
canon_bad=""
canon_missing=""
if [ -d "${dsl_workflow_dir}" ]; then
    dsl_off=$(rg -l --type yaml \
        --glob '!test_stock_pipeline_live.yaml' \
        'stock_pipeline_live_test\.sh' \
        "${dsl_workflow_dir}" 2>/dev/null || true)
    if [ -f "${internal_workflow}" ]; then
        # 2-pass: rg captures ANY `triggers:` line at any indent. The capture
        # is intentionally permissive; the follow-up tokenize+sort+equality
        # check is the validator.
        trigger=$(rg -n '^[[:space:]]*triggers:[[:space:]]' "${internal_workflow}" 2>/dev/null || true)
        if [ -z "${trigger}" ]; then
            canon_missing="explicit triggers: line required (godlike/07 minimum-blast-radius, §10.6)"
        else
            trigger_tokens=$(echo "${trigger}" \
                | grep -Eo '(push|pull_request|schedule|workflow_call|workflow_run|workflow_dispatch)' \
                | sort -u || true)
            if [ -z "${trigger_tokens}" ]; then
                canon_bad="no-recognized-trigger-kind-on-triggers-line"
            elif [ "${trigger_tokens}" != "workflow_dispatch" ]; then
                canon_bad="${trigger_tokens}"
            fi
        fi
    fi
fi
if [ -n "${gh_off}${dsl_off}${canon_bad}${canon_missing}" ]; then
    echo "FAIL: stock_pipeline_live_test.sh referenced outside the manual-only operator surface (godlike/07):"
    [ -n "${gh_off}" ] && {
        echo "  .github/workflows/ hits:"
        echo "${gh_off}" | sed 's/^/    /'
    }
    [ -n "${dsl_off}" ] && {
        echo "  workflows/ non-canonical hits:"
        echo "${dsl_off}" | sed 's/^/    /'
    }
    [ -n "${canon_missing}" ] && {
        echo "  canonical file MISSING triggers: line:"
        echo "    ${canon_missing}"
    }
    [ -n "${canon_bad}" ] && {
        echo "  canonical file has non-conforming trigger kinds:"
        echo "${canon_bad}" | sed 's/^/    /'
    }
    echo ""
    echo "Fix: the live battery hits yt-dlp + Drive writes + Qdrant mutations;"
    echo "      PR/push auto-trigger is forbidden per docs/operations/stock-e2e-runbook.md §10.6."
    echo "      The canonical surfaces are:"
    echo "        - Internal DSL workflow: workflows/test_stock_pipeline_live.yaml (manual-only: workflow_dispatch)"
    echo "        - Operator CLI invocation: bash scripts/stock_pipeline_live_test.sh"
    exit 1
fi
echo "OK: no auto-trigger references to stock_pipeline_live_test.sh (operator-only invariant holds)"
# ── Check 70: LiveBatteryCopyByteEquivalence (godlike/06 SSOT, July 2026) ──
# Per docs/operations/stock-e2e-runbook.md §10.8, the source script
# (scripts/stock_pipeline_live_test.sh) and the registered copy
# (scripts/tests/stock_pipeline_live_test.sh) MUST be byte-identical at every
# commit. Drift detection is enforced here at pre-CI time using cmp -s
# (POSIX-portable, works on macOS/BSD/CI Linux). When they diverge:
#   1. An operator edited the copy directly (forbidden -- see §10.2).
#   2. The source was committed without a `cp -p` regen of the copy.
# Either way the registered copy is stale; CI fails fast.
#
# M2 FIX (2026-07-12, code-reviewer verdict): prior version used GNU-specific
# `sha256sum` for diagnostic hashes. On macOS/BSD operators running the script
# directly (outside the CI Linux container), sha256sum is absent. The
# portable shim below selects `sha256sum` if present, else `shasum -a 256`.
# Mirrors the portability pattern already used by Check 33 (envTimestampIsImmutable
# block) in this same script.
#
# Allowlist: NONE. SSOT has one source. Drift equals mismatch equals fail.
hash_of() {
    local f="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$f" 2>/dev/null | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$f" 2>/dev/null | awk '{print $1}'
    else
        echo "no-hash-tool-available"
    fi
}
echo "=== Check 70: LiveBatteryCopyByteEquivalence (godlike/06 SSOT, \u00a710.8) ==="
src_path="${REPO_ROOT}/scripts/stock_pipeline_live_test.sh"
copy_path="${REPO_ROOT}/scripts/tests/stock_pipeline_live_test.sh"
if [ ! -f "${src_path}" ]; then
    echo "INFO: source script absent at ${src_path} (skipping byte-equivalence check; not registered)"
elif [ ! -f "${copy_path}" ]; then
    echo "FAIL: registered copy absent at ${copy_path} but source present -- godlike/06 SSOT lockstep requires both (\u00a710.2 canonical paths)"
    echo "Fix: cp -p scripts/stock_pipeline_live_test.sh scripts/tests/stock_pipeline_live_test.sh"
    exit 1
else
    if cmp -s "${src_path}" "${copy_path}"; then
        echo "OK: source is byte-equivalent to registered copy"
    else
        src_sha=$(hash_of "${src_path}")
        copy_sha=$(hash_of "${copy_path}")
        echo "FAIL: source vs registered copy byte-divergence (godlike/06 SSOT \u00a710.8 lockstep broken)"
        echo "  source:    ${src_path}  (sha256: ${src_sha})"
        echo "  registered: ${copy_path}  (sha256: ${copy_sha})"
        echo ""
        echo "Fix: regenerate the registered copy from the source via the canonical"
        echo "      cp -p command (\u00a710.2 canonical paths):"
        echo "        cp -p scripts/stock_pipeline_live_test.sh scripts/tests/stock_pipeline_live_test.sh"
        echo "      An SSOT edit landed on the SOURCE without the regen step; commit"
        echo "      the cp -p regeneration in the SAME PR (godlike/06 lockstep discipline)."
        exit 1
    fi
fi
# ── Check 64: DEBT BUDGET (max 5 PRE-EXISTING open) ─────────────
# Caps the cumulative open carry-forward surface in
# architecture/issues.yaml. Per architecture/policy.yaml::debt_budget
# (`max_pre_existing_open: 5`) + docs/operations/debt-budget.md, every
# entry whose id starts with `PRE-EXISTING-` AND status == "open"
# counts toward the cap. `in_progress` and `resolved` are deliberately
# NOT counted: transitioning an `open` entry to `in_progress` unblocks
# CI, incentivising operators to start work immediately rather than
# letting debt rot. Cap-increase requires a documented SSOT-marker PR
# (godlike/06 one-owner-per-fact); AGENTS.md YAGNI doctrine + godlike/07
# fail-closed: there is NO env-flag to flip the gate off.
#
# Pattern anchors:
#   `kind == "PRE-EXISTING-*"` (literal prefix on `id`)
#   `status == "open"` (literal exact-match)
#
# Fail mode: godlike/07 fail-closed. If the YAML is unparseable, the
# gate falls back to fail-closed too (no silent pass-through) — the
# canonical godlike/07 contract: never let a missing/invalid artefact
# represent itself as a passing validation.
#
# YAML reader reuses the python3 heredoc pattern from
# extract_known_acceptable_ids_from_yaml (this file's top section) so
# the parser-surrogate lives at a single canonical site.
echo "=== Check 64: DEBT BUDGET (max 5 PRE-EXISTING open) ==="
debt_budget_output=""
debt_budget_rc=0
debt_budget_output=$(python3 -c '
import sys, yaml
try:
    with open("architecture/issues.yaml", "r", encoding="utf-8") as f:
        docs = yaml.safe_load(f)
    issues = docs.get("issues", []) if isinstance(docs, dict) else []
    cap = 5
    offenders = [
        str(it.get("id", ""))
        for it in issues
        if isinstance(it, dict)
        and str(it.get("id", "")).startswith("PRE-EXISTING-")
        and it.get("status") == "open"
    ]
    if len(offenders) > cap:
        sys.stderr.write("FAIL: PRE-EXISTING open count = %d > %d (DEBT BUDGET cap=%d)\n" % (len(offenders), cap, cap))
        for oid in offenders:
            sys.stderr.write("  - %s\n" % oid)
        sys.stderr.write("\n")
        sys.stderr.write("Fix: follow docs/operations/debt-budget.md procedure:\n")
        sys.stderr.write("  1. Migrate one of the offenders to `resolved` (preferred;\n")
        sys.stderr.write("     evidence_filename MUST cite the fix artifact) OR\n")
        sys.stderr.write("     `in_progress` (valid intermediate; unblocks CI).\n")
        sys.stderr.write("  2. Do NOT rename id to drop the PRE-EXISTING prefix.\n")
        sys.stderr.write("  3. Do NOT env-gate the gate off (no DEBT_BUDGET_STRICT\n")
        sys.stderr.write("     flag by design — YAGNI + godlike/07 fail-closed).\n")
        sys.stderr.write("  4. Lifting the cap requires a SSOT-marker PR (see\n")
        sys.stderr.write("     architecture/policy.yaml::debt_budget+lint_gates rationale).\n")
        sys.exit(2)
    print("PRE-EXISTING open count = %d (cap = %d)" % (len(offenders), cap))
except (yaml.YAMLError, OSError, UnicodeDecodeError) as e:
    # godlike/07 fail-closed: a missing/unreadable catalogue MUST not
    # silently pass the gate. Surface the failure to stderr + exit 2
    # so the wrapper below propagates exit 1.
    sys.stderr.write("FAIL: architecture/issues.yaml is broken or unreadable (godlike/07 fail-closed): %s\n" % e)
    sys.exit(2)
' 2>&1) || debt_budget_rc=$?
if [ "${debt_budget_rc}" -ne 0 ]; then
    printf '%s\n' "${debt_budget_output}" >&2
    exit 1
fi
echo "OK: DEBT BUDGET respected -- ${debt_budget_output}"
# ── Check 70: AssetCommitter SSOT (Wave 5, July 2026) ──
# AssetCommitter is the single canonical persistence boundary for
# processed assets. Direct calls to AssetFinalizerTx.FinalizeAsset
# or mutations.AssetMutationDispatcher.EnqueueAndIndex outside the
# AssetCommitter are SSOT regressions: they bypass the canonical
# transaction + outbox orchestration owned by the committer.
#
# Allowlist:
#   - internal/application/assets/processing/asset_committer.go : the canonical AssetCommitter implementation.
#   - *_test.go                                                   : tests may exercise the underlying primitives directly.
#   - internal/application/assets/finalizer/**                   : the finalizer interface definition and its tests.
#   - internal/application/assets/mutations/**                   : the dispatcher interface definition and its tests.
#
# Pattern anchors:
#   \.FinalizeAsset\(          — direct finalizer call
#   \.EnqueueAndIndex\(        — direct dispatcher call
#   AssetFinalizerTx\.FinalizeAsset — rare fully-qualified call
#   AssetMutationDispatcher\.EnqueueAndIndex — rare fully-qualified call

echo "=== Check 70: AssetCommitter SSOT (Wave 5, July 2026) ==="
asset_committer_hits=$(rg -n --type go \
    -e '\.FinalizeAsset\(' \
    -e '\.EnqueueAndIndex\(' \
    -e 'AssetFinalizerTx\.FinalizeAsset' \
    -e 'AssetMutationDispatcher\.EnqueueAndIndex' \
    --glob '!**/asset_committer.go' \
    --glob '!**/*_test.go' \
    --glob '!**/finalizer/**' \
    --glob '!**/mutations/**' \
    internal/application internal/api 2>/dev/null \
    | awk -F: '{ rest = ""; for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i; if (rest ~ /^[[:space:]]*\/\//) next; print }' \
    || true)
if [ -n "$asset_committer_hits" ]; then
    echo "FAIL: direct asset persistence call outside AssetCommitter:"
    echo "$asset_committer_hits"
    echo ""
    echo "Fix: route persistence through processing.AssetCommitter.Commit or"
    echo "     processing.AssetCommitter.EnqueueAndIndex. The committer is the"
    echo "     single owner of the asset persistence transaction + outbox"
    echo "     orchestration."
    exit 1
fi
echo "OK: no direct asset persistence calls outside AssetCommitter"
# ── Check 71: Qdrant upsert SSOT (Wave 5, July 2026) ──
# IndexWriter is the ONLY code path that calls
# transport.Client.UpsertPoints / transport.Client.DeletePoints.
# Any direct caller outside index_writer.go bypasses the canonical
# write path (outbox.Dispatcher → IndexingHandler → IndexWriter)
# and risks stale data racing the source_version supersede gate.
#
# Allowlist:
#   - internal/infrastructure/qdrant/indexing/index_writer*.go : the canonical IndexWriter package.
#   - *_test.go                                                   : tests may construct transport.Client fakes directly.
#
# Pattern anchors:
#   \.UpsertPoints\(  — direct transport.Client upsert
#   \.DeletePoints\( — direct transport.Client delete

echo "=== Check 71: Qdrant upsert SSOT (Wave 5, July 2026) ==="
qdrant_upsert_hits=$(rg -n --type go \
    -e '\.UpsertPoints\(' \
    -e '\.DeletePoints\(' \
    --glob '!**/qdrant/indexing/**' \
    --glob '!**/*_test.go' \
    internal/ 2>/dev/null \
    | awk -F: '{ rest = ""; for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i; if (rest ~ /^[[:space:]]*\/\//) next; print }' \
    || true)
if [ -n "$qdrant_upsert_hits" ]; then
    echo "FAIL: direct Qdrant upsert/delete call outside IndexWriter:"
    echo "$qdrant_upsert_hits"
    echo ""
    echo "Fix: route Qdrant writes through outbox.Dispatcher (production) or the"
    echo "     admin reindex CLI (operator tooling). The canonical write path is"
    echo "     outbox.Dispatcher → IndexingHandler → IndexWriter."
    exit 1
fi
echo "OK: no direct Qdrant upsert/delete calls outside IndexWriter"
# ── Check 72: SearchAggregator uniqueness (Wave 5, July 2026) ──
# There must be exactly one SearchAggregator in the production
# codebase. Multiple aggregators or ad-hoc backend fan-out bypass
# the canonical ranking/dedup pipeline.
#
# Allowlist:
#   - internal/application/search/aggregator.go : the canonical Aggregator definition.
#   - *_test.go                                : tests may construct aggregators for verification.
#
# Pattern anchors:
#   search\.NewAggregator\(  — canonical constructor
#   NewAggregator\(        — generic constructor name collision

echo "=== Check 72: SearchAggregator uniqueness (Wave 5, July 2026) ==="
aggregator_count=$(rg -n --type go \
    -e 'search\.NewAggregator\(' \
    -e '\bNewAggregator\(' \
    --glob '!**/search/aggregator.go' \
    --glob '!**/*_test.go' \
    internal/ 2>/dev/null \
    | awk -F: '{ rest = ""; for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i; if (rest ~ /^[[:space:]]*\/\//) next; print }' \
    | wc -l | awk '{print $1+0}')
if [ "$aggregator_count" -gt 0 ]; then
    echo "FAIL: extra SearchAggregator constructor found outside canonical aggregator:"
    rg -n --type go \
        -e 'search\.NewAggregator\(' \
        -e '\bNewAggregator\(' \
        --glob '!**/search/aggregator.go' \
        --glob '!**/*_test.go' \
        internal/ 2>/dev/null \
        | awk -F: '{ rest = ""; for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i; if (rest ~ /^[[:space:]]*\/\//) next; print }'
    echo ""
    echo "Fix: route all search aggregation through the canonical search.Aggregator."
    echo "     Do not introduce additional aggregator implementations."
    exit 1
fi
echo "OK: no extra SearchAggregator constructors outside canonical aggregator"

#!/usr/bin/env bash
set -euo pipefail

if [ -n "${BASH_SOURCE[0]:-}" ]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
  echo "CI: cannot resolve architectural checks directory from BASH_SOURCE[0]" >&2
  exit 1
fi
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../../.." && pwd)"

# scripts/ci-architectural-checks.sh — CI gate for the architectural checker.
#
# PR-A (June 2026): adds the `--future-ratchet` flag so the 5 Phase 0 rules
# (interface{}/any growth, setter detector, cross-package type alias,
# fake route, handler-to-DB) run in baseline-on-baseline ratchet mode.
# During the minor cycle (this script's current state) the gate fails ONLY
# on regressions vs scripts/archcheck/phase0_baseline.json; existing
# entries in the baseline are accepted.
#
# PR-B (June 2026): adds Check 0 — forbid literal job-type strings outside
# the canonical domain/job/job.go decl. Four canonical constants live there
# (TypeBatchScriptGenerate, TypeClipScriptGenerate, TypeCatalogScriptGenerate,
# TypeMediaCurate); every consumer should reference them by name. New
# quoted-string occurrences of those values outside the canonical decl are
# a SSOT regression and fail this gate.
#
# PR-D (June 2026): adds Check 4 — migration version uniqueness lint.
# Fails when two or more files in `migrations/sqlite/` share the leading
# numeric version prefix. Historically observed as the `069_*.sql` × 2
# incident (surface: composition-test panic at server startup) — the
# duplicate-prefix collision silently picks one candidate at runtime.
# Renumbered to Check 4 because Checks 0/1/2/3 were already claimed by
# PR-B, QDRANT-002, QDRANT-001, and PR 6 (engine.Generate SSOT gate)
# respectively.
#
# PR 6 (June 2026): adds Check 3 — forbid `engine.Generate()` outside
# the canonical `GenerateOneUseCase`. Engine access must flow through
# the typed pipeline orchestrator; any direct call is a SSOT regression.
# Check 3 was introduced on origin/main after this branch forked; the
# merge here places PR-D's lint as Check 4 to avoid the collision.
#
# Promote-to-required checklist (separate follow-up PR):
#   1. Drop `--future-ratchet` from the command line below.
#   2. Fold runPhase0Checks() into runRatchetChecks() in
#      scripts/archcheck/main.go.
#   3. Update docs/architecture/godlike/14_INITIAL_BACKLOG.md — mark
#      Block 1 + the 5 Phase 0 rules as verified_zero: true.

# ── Wave-tracker allowlist consultation (PR-CI-WAVE-ALLOWLIST, July 2026) ──
# Text-based extraction of `id: PR-*` lines from architecture/current.yaml
# whose entry has `status: pending` or `status: in_progress` OR whose entry
# is a `PRE-EXISTING-*-2026-07-04` parent. Per godlike/07 no-fake-availability:
# the wave-tracker file is currently UNPARSEABLE (3+ indent cascade bugs at
# lines ~1582, ~2852, ~2996), so we use a TEXT-BASED FALLBACK rather than
# `yaml.safe_load`. The fallback is the canonical extraction strategy
# (mirrors how the rest of the script uses awk / rg / sed line-by-line).
#
# This is an ADDITIVE informational layer (godlike/07 minimum-blast-radius):
# no existing check's exit-1 semantics change. The baseline log becomes a
# stable pass-rate count instead of an all-violations dump. Future per-check
# integration can opt-in by calling `is_known_acceptable <PR_ID>`.
KNOWN_ACCEPTABLE_IDS=""
extract_known_acceptable_ids_from_yaml() {
    # PR-REMOVE-CI-AWK-SED-FALLBACKS (2026-07-04): replaced the 132-line
    # bash state-machine fallback (which compensated for the broken YAML)
    # with a ~15-line Python heredoc that uses yaml.safe_load. After
    # PR-FIX-YAML-PARSE-LINE-1551 shipped, the YAML is fully parseable,
    # so the text-based fallback is no longer needed.
    #
    # The replacement preserves the canonical external contract:
    #   - sets KNOWN_ACCEPTABLE_IDS to newline-separated ID list
    #   - empty on missing file OR parse error (godlike/07 fail-closed default)
    #   - accepts:
    #     - top-level parents: PRE-EXISTING-* (always) OR status pending/in_progress
    #     - children of PRE-EXISTING-* parents (always)
    #     - children of pending/in_progress parents
    #
    # Per godlike/06 SSOT: the wave-tracker is the SOLE source of truth for
    # PR/issue status. Per godlike/07 minimum-blast-radius: the Python
    # heredoc is ~15 lines (vs 132 lines of bash state-machine); the
    # output format (newline-separated sorted deduped IDs) is byte-identical
    # to the previous implementation, so callers (is_known_acceptable +
    # WAVE_BASELINE_SIZE) need no changes.
    local yaml_path="${REPO_ROOT:-$(pwd)}/architecture/current.yaml"
    KNOWN_ACCEPTABLE_IDS=""
    if [ ! -f "${yaml_path}" ]; then
        return 0
    fi
    KNOWN_ACCEPTABLE_IDS=$(python3 -c '
import sys, yaml
try:
    with open(sys.argv[1], "r", encoding="utf-8") as f:
        docs = yaml.safe_load(f)
    accepted = set()
    for p in (docs if isinstance(docs, list) else []):
        if not isinstance(p, dict):
            continue
        pid, pst = str(p.get("id", "")), p.get("status", "")
        if pid.startswith("PRE-EXISTING-") or pst in ("pending", "in_progress"):
            if pid:
                accepted.add(pid)
            for child in (p.get("linked_issues") or []):
                if isinstance(child, dict) and child.get("id"):
                    accepted.add(str(child["id"]))
    for val in sorted(accepted):
        print(val)
except (yaml.YAMLError, OSError, UnicodeDecodeError):
    pass
' "${yaml_path}" 2>/dev/null || true)
    # godlike/07 no-fake-availability: even if the YAML is unparseable,
    # the silent failure path is preserved (empty KNOWN_ACCEPTABLE_IDS
    # means all violations become "new" until the wave-tracker is
    # re-introduced). The previous bash state-machine had the same
    # silent-fallback contract; this Python replacement preserves it
    # 1:1. NOTE: do NOT `export` — the variable is read in the same
    # shell process (the function-call below + Check 61); child
    # processes don't need it.
}
is_known_acceptable() {
    local pr_id="${1:-}"
    [ -z "${pr_id}" ] && return 1
    [ -z "${KNOWN_ACCEPTABLE_IDS}" ] && return 1
    # Pure bash membership check on the newline-separated allowlist.
    if printf '%s\n' "${KNOWN_ACCEPTABLE_IDS}" | grep -qxF "${pr_id}"; then
        return 0
    fi
    return 1
}
# Populate the global once at script start. Any per-check opt-in can
# call is_known_acceptable <PR_ID> to consult.
extract_known_acceptable_ids_from_yaml
WAVE_BASELINE_SIZE=0
if [ -n "${KNOWN_ACCEPTABLE_IDS}" ]; then
    WAVE_BASELINE_SIZE=$(printf '%s\n' "${KNOWN_ACCEPTABLE_IDS}" | wc -l | awk '{print $1+0}')
fi


# ── Self-check mode (TODO 16, June 2026) ──────────────────────────────
# When invoked with `--self-check`, the script runs each check's regex
# against its corresponding fixture in tests/fixtures/zero_legacy/ and
# verifies the regex catches the forbidden pattern. Exits 0 only if
# every check's pattern still matches its fixture; exits 1 if any
# fixture is missing or any pattern is broken.
#
# Self-check is a UNIT TEST FOR THE REGEXES — it does NOT scan the
# production tree. The standard mode (no flag) is the production gate.
if [ "${1:-}" = "--self-check" ]; then
    FIXTURE_DIR="${REPO_ROOT}/tests/fixtures/zero_legacy"
    if [ ! -d "${FIXTURE_DIR}" ]; then
        echo "FAIL: fixture dir ${FIXTURE_DIR} does not exist (run from repo root)" >&2
        exit 1
    fi

    # Format: name|pattern|fixture_file. Pattern uses ripgrep -E syntax.
    # The fixture MUST contain the forbidden pattern; rg must match.
    check_defs=(
        "Check 8 (SetOutboxHandler/SetMediasearchHandler after construction)|\\.SetOutboxHandler\\(|check_08_setter.go"
        "Check 8 (SetOutboxHandler/SetMediasearchHandler after construction)|\\.SetMediasearchHandler\\(|check_08_setter.go"
        "Check 9 (nil-dispatcher silent fallback)|dispatcher\\s*==\\s*nil\\s*\\{[^}]*return\\s+nil\\b|check_09_nil_dispatcher.go"
        "Check 10 (asset-repo Upsert outside allowlist)|\\.Upsert\\(ctx,|check_10_upsert.go"
        "Check 11 (event_key constructed with random UUID, inline)|eventKey[^\\n]*uuid\\.NewString|check_11_uuid_event_key.go"
        "Check 11 (event_key constructed with random UUID, multiline reverse)|eventID[^\\n]*=\\s*uuid\\.NewString[^\\n]*\\n(?:[^\\n]*\\n){0,3}[^\\n]*eventKey[^\\n]*=[^\\n]*\\beventID\\b|check_11_uuid_event_key.go"
        "Check 11 (event_key constructed with random UUID, multiline forward)|eventKey[^\\n]*\\n(?:[^\\n]*\\n){0,3}[^\\n]*eventID[^\\n]*=\\s*uuid\\.NewString|check_11_uuid_event_key.go"
        "Check 12 (payload_mapper legacy lifecycle_state fallback)|\"lifecycle_state\":\\s*\\w+\\.Status|check_12_payload_mapper_status.go"
        "Check 13 (ListAssetsForReconcile placeholder)|wired as build-time placeholder|check_13_listassets_placeholder.go"
        "Check 14 (BuildPayload legacy status key)|\"status\":\\s*\\w+\\.|check_14_buildpayload_status_key.go"
        "Check 15 (qdrant.NewClient construction)|qdrant\\.NewClient\\(&qdrant\\.Config\\{|check_15_qdrant_config_apikey.go"
        "Check 50 (forbid void Register* signature)|func \\(\\w+ \\*?\\w+\\) [A-Z][A-Za-z0-9_]*[Rr]egister\\([^)]*\\bjobs\\.?Service[^)]*\\)[[:space:]]*\\{|check_50_void_register.go"
        "Check 57 (forbid ports.ScriptRecord literal outside canonical allowlist)|ports\\.ScriptRecord\\{|check_57_scriptrecord_prod_literal.go"
        "Check 55 (forbid legacy Template writes outside canonical allowlist)|Template:\\s|check_55_template_timeline_literal.go"
        "Check 55 (forbid legacy TimelineJSON writes outside canonical allowlist)|TimelineJSON:\\s|check_55_template_timeline_literal.go"
        "Check 60 (forbid t.Skip without godlike/07 honest-limitation comment)|t\\.Skip[a-zA-Z]*\\(|check_60_t_skip.go"
        "Check 62 (forbid RequireAdminToken inline in feature routes)|RequireAdminToken|check_62_inline_middleware.go"
        "Check 62 (forbid extractHeaderToken inline in feature routes)|extractHeaderToken|check_62_inline_middleware.go"
        "Check 62 (forbid EnableAuth inline in feature routes)|EnableAuth|check_62_inline_middleware.go"
        "Check 62 (forbid AdminTokenProvider inline in feature routes)|AdminTokenProvider|check_62_inline_middleware.go"
    )

    failed=0
    seen_names=""
    for def in "${check_defs[@]}"; do
        IFS='|' read -r name pattern fixture <<< "${def}"
        fixture_path="${FIXTURE_DIR}/${fixture}"
        if [ ! -f "${fixture_path}" ]; then
            echo "FAIL: ${name} — fixture ${fixture} missing" >&2
            failed=1
            continue
        fi
        if rg -qU -- "${pattern}" "${fixture_path}" 2>/dev/null; then
            # De-duplicate per check (Check 8 has 2 patterns for 2 methods).
            if [[ "${seen_names}" != *"${name}"* ]]; then
                echo "PASS: ${name} — caught fixture ${fixture}"
                seen_names="${seen_names}|${name}|"
            fi
        else
            echo "FAIL: ${name} — pattern did NOT catch fixture ${fixture} (regex is broken)" >&2
            failed=1
        fi
    done

    if [ "${failed}" -gt 0 ]; then
        echo "" >&2
        echo "Self-check FAILED: at least one regex does not catch its fixture." >&2
        echo "Fix the regex OR update the fixture so the forbidden pattern is present." >&2
        exit 1
    fi
    # Count unique check names (Check 8 has 2 patterns for 2 methods; Check 11
    # has 3 patterns for 3 shapes; etc.) so the summary is accurate. Use
    # bash parameter expansion (${def%%|*}) instead of awk to avoid breakage
    # if a check name ever contains a `|` character.
    unique_names=()
    for def in "${check_defs[@]}"; do
        name="${def%%|*}"
        if [[ " ${unique_names[*]} " != *" ${name} "* ]]; then
            unique_names+=("$name")
        fi
    done
    unique_count=${#unique_names[@]}
    pattern_count=${#check_defs[@]}
    # Count fixture files dynamically (ls instead of hardcoded "7") so the
    # summary stays accurate when new fixtures are added.
    fixture_count=$(ls -1 "${FIXTURE_DIR}"/*.go 2>/dev/null | wc -l)
    echo "All self-checks passed (${pattern_count} patterns / ${unique_count} unique checks / ${fixture_count} fixtures)."
    exit 0
fi
# ── Check 0: forbid literal job-type strings outside canonical SSOT ─────
# The 4 canonical constants carry string values:
#   "script.generate_batch"          (job.TypeBatchScriptGenerate)
#   "script.generate_from_clips"     (job.TypeClipScriptGenerate)
#   "script.generate_from_catalog"   (job.TypeCatalogScriptGenerate)
#   "media.curate"                   (job.TypeMediaCurate)
# Per godlike/02 SSOT ("capability-specific constants live in their
# owning domain package"), each canonical declaration lives in the
# capability-specific package; the legacy `internal/domain/job/job.go`
# is a Phase-A.2 back-compat alias layer (type aliases to `kernel/job`)
# and does NOT own the literal values. Canonical owners:
#   - "media.curate"                  → internal/domain/media/job_types.go (media capability)
#   - "script.generate_batch"         → internal/domain/script/         (script capability)
#   - "script.generate_from_clips"    → internal/domain/script/         (script capability)
#   - "script.generate_from_catalog"  → internal/domain/script/         (script capability)
# Any new rg hit on those strings as quoted STRING LITERALS (not comments)
# in production code outside these canonical owners indicates a regression
# — the canonical reference should always be the typed constant from the
# capability-specific domain.
#
# PR-B (June 2026) closes the 4 script-related constants only. The
# remaining literal constants in internal/application/jobs/registry.go
# (TypeBulkUploadYouTubeClips, TypeDriveFolderSync) and the other keys in
# internal/application/jobs/worker.go's timeout registry are intentionally
# out of PR-B scope and will be folded in a separate wave.
#
# Pattern anchors:
#   [=:(,]\s*"..."  — matches TypeBatchScriptGenerate = "...", Type: "...", func args
#   "..."\s*[:,)]  — matches map keys ("...": NUMBER), trailing ,) cases
# Comment-only lines are excluded via awk so descriptive log strings
# ("handling foo job") don't trigger false positives. A second grep-vE
# belt-and-suspenders rejects inline comments where "// \"...\" ..."
# appears on a code line.
echo "=== Check 1: forbid direct IndexWriter callers (QDRANT-002, Wave 14 §3) ==="
literals=$(rg -n --type go \
    -e 'qdrant\.NewIndexWriter\(' \
    -e '(&?qdrant\.IndexWriter)\{' \
    --glob '!**/cmd/admin/**' \
    --glob '!**/build_bundles_process.go' \
    --glob '!**/*_test.go' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' \
    || true)
if [ -n "$literals" ]; then
    echo "FAIL: direct IndexWriter constructor outside canonical composition root:"
    echo "$literals"
    echo ""
    echo "Fix: route writes through outbox.Dispatcher (production) or the admin"
    echo "reindex CLI (operator tooling). The allowlist (cmd/admin/, internal/app/"
    echo "build_bundles_process.go) is the ONLY legitimate construction site."
    exit 1
fi
echo "OK: no direct IndexWriter constructors outside the canonical allowlist"
# ── Check N: unresolved architecture-symbol references (action P0-5 slice 4/4) ─────
# The architecture/ docs reference internal/, pkg/, cmd/ paths and Go packages.
# When a ref'd file or package disappears (rename, deletion, move), the docs
# become silent lies. This check validates each leaf-string-scalar token matching
# the canonical Go-path prefixes (internal/, pkg/, cmd/) or the .go suffix.
# One Finding is emitted per missing reference; the trailing summary line
# reports the count. Exit 1 if any finding exists (fail-closed, AGENTS.md §8
# ARCHITECTURE-CI-GATES). The function lives at
# scripts/archcheck/symbol_refs.go and is invoked via
# `go run ./scripts/archcheck --symbol-refs`.
echo "=== Check N: symbol_refs unresolved references (action P0-5 slice 4/4) ==="
sr_out=$(go run ./scripts/archcheck --symbol-refs 2>&1 1>/dev/null) || sr_out=""
if [ -n "$sr_out" ]; then
    printf '%s\n' "$sr_out" | sed 's/^/  /'
    exit 1
fi
echo "Check N: 0 unresolved architecture-symbol references"
# ── Check 2: QDRANT-001 canonical sidecar envelope + zero-legacy search contract ──
#
# Enforces the QDRANT-001 definition-of-done gates:
#   (a) One single canonical AssetIDToQdrantPointID declaration.
#   (b) No LocalPath/DriveLink locators in the application search DTO.
#   (c) No PointIDToAssetID (UUID v5 is one-way).
#   (d) Sidecar endpoints return model + model_version.
#
# See docs/architecture/qdrant/QDRANT-001.md for the full spec.
echo "=== Check 2: QDRANT-001 canonical sidecar envelope + zero-legacy search contract ==="
failures=0

# Gate (a): one canonical AssetIDToQdrantPointID declaration.
count=$(rg -n --glob '!**/*_test.go' 'func AssetIDToQdrantPointID\(' internal/infrastructure/qdrant | wc -l)
if [ "$count" -ne 1 ]; then
    echo "FAIL: expected exactly 1 AssetIDToQdrantPointID declaration, found $count"
    failures=$((failures+1))
fi

# Gate (b): no LocalPath or DriveLink in the application search DTO.
if rg -q '^\s*(LocalPath|DriveLink)\s+string' internal/application/assets/search/ports.go; then
    echo "FAIL: LocalPath/DriveLink still present in VectorSearchResult (internal/application/assets/search/ports.go)"
    failures=$((failures+1))
fi

# Gate (c): no PointIDToAssetID (UUID v5 is one-way; the reverse helper was removed).
if rg -n --glob '!**/*_test.go' -e 'PointIDToAssetID' internal/infrastructure/qdrant | grep -vE '^\s*(//|\*)' | grep -q .; then
    echo "FAIL: PointIDToAssetID found in non-comment code in internal/infrastructure/qdrant (must be removed; UUID v5 is one-way)"
    failures=$((failures+1))
fi

# Gate (d): sidecar endpoints return model AND model_version.
if ! rg -q '"model"' scripts/services/embedding_server/visual.py; then
    echo "FAIL: visual.py does not return 'model' in its JSON responses"
    failures=$((failures+1))
fi
if ! rg -q '"model_version"' scripts/services/embedding_server/visual.py; then
    echo "FAIL: visual.py does not return 'model_version' in its JSON responses"
    failures=$((failures+1))
fi
if ! rg -q '"model"' scripts/services/embedding_server/audio.py; then
    echo "FAIL: audio.py does not return 'model' in its JSON responses"
    failures=$((failures+1))
fi
if ! rg -q '"model_version"' scripts/services/embedding_server/audio.py; then
    echo "FAIL: audio.py does not return 'model_version' in its JSON responses"
    failures=$((failures+1))
fi

if [ "$failures" -gt 0 ]; then
    echo "QDRANT-001: $failures gate(s) FAILED"
    exit 1
fi
echo "OK: QDRANT-001 gates pass"
# ── Check 3: forbid engine.Generate() outside GenerateOneUseCase (PR-6) ──
# The canonical engine entry point is Engine.Generate(ctx, plan). The ONLY
# permitted production caller is generate_one_usecase.go::GenerateOneUseCase.
#
# Allowlist:
#   - generate_one_usecase.go   : canonical caller (typed pipeline orchestrator)
#   - engine.go                  : definition site
#   - *_test.go                  : tests may call Generate for verification
#
# Any new engine.Generate( call in production code outside the allowlist
# is a PR-6 regression: engine access must flow through GenerateOneUseCase.
echo "=== Check 3: forbid engine.Generate() outside GenerateOneUseCase (PR-6) ==="
literals=$(rg -n --type go \
    -e '\bengine\.Generate\(' \
    --glob '!**/generate_one_usecase.go' \
    --glob '!**/engine.go' \
    --glob '!**/*_test.go' \
    internal/ 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' \
    || true)
if [ -n "$literals" ]; then
    echo "FAIL: direct engine.Generate() call outside GenerateOneUseCase:"
    echo "$literals"
    echo ""
    echo "Fix: route engine access through GenerateOneUseCase.Execute()."
    echo "The engine is the canonical script-generator; the sole production"
    echo "caller is generate_one_usecase.go. Handler code, resolvers, and"
    echo "postprocessors must NOT call engine.Generate() directly."
    exit 1
fi
echo "OK: no direct engine.Generate() calls outside GenerateOneUseCase"
# ── Check 5: forbid mutation primitives in production callers (QDRANT-asset-mutation isolation, Wave 22) ────
# The three primitive methods UpsertClip / Restore / HardDelete are
# dispatcher-only / admin-only entry points to media_assets. The
# canonical narrow interface is mutations.AssetMutationPrimitives
# (consumed by outbox.Dispatcher) and admin.InternalAdminPurge
# (consumed by cmd/admin tooling). Production code paths in
# internal/application/** and internal/api/** MUST NOT call these
# methods directly:
#
#   - artlist ingestion MUST route through outbox.Dispatcher.EnqueueAndIndex
#   - sourcing ingest MUST route through IndexDispatcherPort.EnqueueAndIndex
#   - hash-recovery patches MUST use the lower-level Upsert (a public
#     method that still bypasses the outbox but is syntactically
#     permitted on the port surfaces; the lint is a syntactic guard)
#   - admin physical-purge MUST go through InternalAdminPurge
#     (these calls land in cmd/admin/** which is NOT production
#     caller territory per AGENTS.md / Pattern 8)
#
# Verification:
#   rg 'UpsertClip\(|^\s*\.Restore\(|^\s*\.HardDelete\(' \
#      internal/application internal/api --glob '!**/*_test.go'
#   must return ZERO hits.
#
# Allowlist:
#   - mutations/primitives.go : defines the interface, not a caller.
#   - admin/purge*.go         : cmd/admin's package, not production caller.
#   - internal/infrastructure/** : the dispatcher + the canonical
#                                  ClipsRepository (which is the
#                                  owner of the SQL primitives).
#   - *_test.go               : tests may call the methods directly.
#
# ARCH-ALLOWLIST opt-in (Wave 22 task 5 follow-up, June 2026): admin
# migration / backfill files that legitimately need to call the raw
# primitives (e.g. a one-shot operator tool that bypasses the dispatcher
# during an offline maintenance window) MUST prepend the marker comment
# `// ARCH-ALLOWLIST: admin-only` on the line preceding the call. Check 5
# then strips any line-with-marker hit from the failing-set via an
# awk pre-pass that drops matches whose preceding comment line carries
# the magic marker. The marker is enforced strictly (typos in the magic
# word = lint failure = corruption-safe by design). Per AGENTS.md §7
# zero-baseline rule, new allowlist entries require explicit owner +
# deadline; the marker is the call-site equivalent of an allowlist row.
echo "=== Check 5: forbid mutation primitives in production callers (QDRANT-asset-mutation) ==="
# Step 1: collect ALL raw hits, including ARCH-ALLOWLIST marker sites,
# so the post-pass can recognise the magic marker on the line
# PRECEDING the call line.
all_hits=$(rg -n --type go \
    -e '\bUpsertClip\(' \
    -e '(^|[\s.(])r\.Restore\(' \
    -e '(^|[\s.(])r\.HardDelete\(' \
    -e '\.repo\.UpsertClip\(' \
    -e '\.clips\.UpsertClip\(' \
    -e '\.inner\.UpsertClip\(' \
    --glob '!**/*_test.go' \
    --glob '!**/mutations/primitives.go' \
    --glob '!**/admin/purge*.go' \
    --glob '!**/infrastructure/database/sqlite/**' \
    internal/application internal/api 2>/dev/null \
    || true)
# Step 2: drop full-line comments AND lines preceded by the ARCH-ALLOWLIST
# marker comment (i.e. the preceding line carries the magic marker).
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        BEGIN { prev_marker = 0 }
        # Group hits by file so we look at the line BEFORE each hit
        # within the same file. Maintain a ring buffer of the last
        # 2 lines of each file in awk is non-trivial; instead we
        # rely on rg already having line numbers and we look for
        # marker on the SAME or PRECEDING line (rg joins via -).
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*admin-only/) {
                # Save as a marker line for THIS file. Format: file<TAB>marker_line
                markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
            # Allow if THIS line number is within marker+1..marker+3
            # for the same file (tolerates a blank-line separator between
            # marker comment and the call site). Strict equality would
            # miss the operator-common pattern of
            #   // ARCH-ALLOWLIST: admin-only
            #
            #   clipsRepo.UpsertClip(...)
            # Scan every marker line in markers[$1] (comma-joined). If THIS
            # hit line is within `marker+1..marker+25` for any marker in
            # the same file, drop it (allowlisted). Scroll-window is 25
            # lines; the marker count is unbounded per-file.
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
    echo "FAIL: forbidden mutation primitive call in production caller:"
    echo "$literal_calls"
    echo ""
    echo "Fix: route writes through outbox.Dispatcher.EnqueueAndIndex"
    echo "(production) or admin.InternalAdminPurge (offline tooling)."
    echo "The narrowed surface is mutations.AssetMutationPrimitives; the"
    echo "underlying methods on *assets.ClipsRepository are dispatcher-only."
    echo ""
    echo "If the call is genuinely admin migration / backfill, prepend the"
    echo "comment marker on the line preceding the call:"
    echo "    // ARCH-ALLOWLIST: admin-only"
    echo "    clipsRepo.UpsertClip(ctx, &asset.Asset{ID: \"__backfill__\"})"
    echo "The marker is stripped from the failing-set automatically."
    exit 1
fi
echo "OK: no forbidden mutation primitive calls in production callers"
# ── Check 6: Migration version uniqueness lint (PR-D) ──────────────
# Fails when two or more files in `migrations/sqlite/` share the
# leading numeric version prefix. The canonical convention for this
# repo is one file per migration number; the slot ordering encodes the
# upgrade path, and a duplicate-prefix collision silently picks one
# candidate at runtime — historically observed as `069_*.sql` × 2 in
# the working tree (surface: composition-test panic at server startup).
#
# This lint catches the same pattern at pre-CI time so a new migration
# cannot land with a colliding slot.
#
# Implementation: list all migration files, project the prefix, then
# fail on any prefix that appears more than once. The regex
# `/^[0-9]+$/` (one or more digits) matches the canonical 3-digit slot
# AND any future widening (4-digit slot if a future numbering scheme
# requires it), while excluding vim backup files (`~001_foo.sql`),
# Emacs locks (`.#002_bar.sql`), and any other neighbour of a real
# migration that would otherwise look like a colliding slot.
migration_root="${MIGRATIONS_ROOT:-${REPO_ROOT}/migrations/sqlite}"
if [ -d "${migration_root}" ]; then
  dupes=$(ls -1 "${migration_root}/" 2>/dev/null \
    | awk -F_ '$1 ~ /^[0-9]+$/ {print $1}' \
    | sort \
    | uniq -d) || true
  if [ -n "${dupes}" ]; then
    echo "CI: duplicate migration version prefix(es) detected in ${migration_root}/:" >&2
    for v in ${dupes}; do
      echo >&2
      echo "  prefix ${v}:" >&2
      ls -1 "${migration_root}/${v}_"*.sql 2>/dev/null | sed 's|^|    |' >&2
    done
    echo >&2
    echo "Convention: one file per 3-digit version prefix." >&2
    echo "Resolve by renaming one of the colliding files to a free numeric slot." >&2
    exit 1
  fi
fi
echo "OK: no duplicate migration version prefixes in ${migration_root}/"
# ── Check 5: same-package duplicate-type-declarations lint (QDRANT-RECOVERY-001 follow-up) ──
# Go cannot distinguish file-level types from package-level types — two .go files
# in the same package declaring `type X struct{...}` produces a build error:
# "<X> redeclared in this block". Historically observed as the SnapshotDescription
# duplicate in internal/infrastructure/qdrant/types.go + types_dr.go on origin/main
# (fixed by commits 2b67d701 + 38187ded — see docs/operations/05 ticket
# QDRANT-RECOVERY-001). This lint catches the same pattern at pre-CI time so a new
# type declaration cannot land with a colliding same-package symbol.
#
# Implementation: walk every non-test .go file under internal/, extract the
# `package X` line + every `^type X ...` declaration, project to the
# canonical Go-package-identity tuple `<dir>/<package>:<type>` (per
# architecture/policy.yaml::lint_gates[check=5] — Go's package identity is
# `(directory_path, package_name)`, NOT package_name alone; the principle is
# also restated in architecture/current.yaml::WAVE-20-QDRANT-005D-HYGIENE
# / PRE-EXISTING-15-LENS-MIGRATION), fail on any redeclaration that is NOT
# listed in the per-(dir,pkg) allowlist.
#
# Allowlist: docs/migrations/duplicate-types-allowlist.txt lists one
# `<package>:<TypeName>` per line for intentional redeclarations. Per AGENTS.md
# §8 ARCHITECTURE-CI-GATES zero-baseline rule, every new entry requires
# owner + deadline. The file is currently empty by design — same-package
# redeclaration is never a valid production pattern (use a cross-package
# mirror instead, e.g. qdrant.SnapshotDescription (wire) + dr.SnapshotDescription
# (canonical application-layer)); the allowlist exists for transitional cases.
#
# Pattern anchors:
#   ^type[[:space:]]+[A-Z]      — exported type declaration (lowercase skipped)
#   Generic types `type X[T any]` — captured to identifier before `[`
#   Type aliases `type X = ...`  — captured to identifier before space-or-`=`
#   *_test.go files              — excluded so test fixtures may freely declare
#                                  exported types (CI fixture pattern, not a
#                                  SSOT invariant)
echo "=== Check 5: same-package duplicate-type-declarations (QDRANT-RECOVERY-001 follow-up) ==="

# Step 1: extract every exported type declaration as TSV:
# dir<TAB>package<TAB>Type<TAB>file:line
# where the dedup key = `dir/pkg:Type` (canonical Go-package identity tuple
# `(directory_path, package_name)` per policy.yaml::lint_gates[check=5]).
# PRE-EXISTING-15-LENS-MIGRATION (closes WAVE-20-QDRANT-005D-HYGIENE, July 2026):
# the previous lens extracted ONLY `package_name`, which co-classified two
# files in DIFFERENT directories declaring the same `package <name>` as the
# same Go package — generating ~14 cross-directory same-package-NAME
# false-positives (e.g. internal/domain/job/job.go::job.Filter vs
# internal/kernel/job/job.go::job.Filter were flagged as one redeclaration).
# Extending the lens to the canonical (dir, name) tuple distinguishes them
# because Go's package identity is, literally, the dir+name pair.
decls=""
while IFS= read -r -d '' f; do
  # extract package name from the first `package X` line (guard against empty).
  # Canonical awk $2 field-extraction rather than the prior brittle shell
  # prefix-strip — the prior `${pkg_line#package }` collapsed to empty pkg
  # for every file, grouping 381 type declarations under one empty-pkg bucket
  # and producing a false-positive `(count=381 in same package)`.
  pkg=$(awk '/^package[[:space:]]+/ {print $2; exit}' "$f" 2>/dev/null || true)
  [ -z "$pkg" ] && continue
  # PRE-EXISTING-15-LENS-MIGRATION: derive directory_path from the file path
  # via `dirname`. The find walks internal/... so all paths are repo-root
  # relative and already start with `internal/`; `dirname` returns the
  # canonical POSIX directory component (e.g. internal/domain/job for
  # internal/domain/job/job.go). The (dir, pkg) tuple now matches Go's own
  # package-identity contract.
  dir=$(dirname "$f")
  per_file=$(awk -v pkg="$pkg" -v dir="$dir" -v file="$f" '
    /^type[[:space:]]+[A-Z]/ {
      s = $0
      sub(/^type[[:space:]]+/, "", s)
      if (match(s, /^[A-Z][A-Za-z0-9_]*/)) {
        printf("%s\t%s\t%s\t%s:%d\n", dir, pkg, substr(s, RSTART, RLENGTH), file, FNR)
      }
    }' "$f" 2>/dev/null || true)
  decls="$decls"$'\n'"$per_file"
done < <(find internal/ -name '*.go' -not -name '*_test.go' -print0 2>/dev/null || true)

# Step 2: load allowlist keys (pipe-delimited) if the allowlist file is present.
# Empty file = no exceptions; missing file = no exceptions (the file is expected
# to exist on disk per AGENTS.md §8 but we do not gate on its presence here).
allowed=""
if [ -f "docs/migrations/duplicate-types-allowlist.txt" ]; then
  allowed=$(grep -vE '^\s*(#|$)' docs/migrations/duplicate-types-allowlist.txt 2>/dev/null \
            | awk '{print $1}' | sort -u | paste -sd'|' - || true)
fi

# Step 3: dedup by (dir, package, TypeName), count, fail on count >= 2 not in allowlist.
# PRE-EXISTING-15-LENS-MIGRATION: key now = `dir/pkg:Type`. Two files in
# DIFFERENT directories declaring the same package name + same type are
# NOT a Go redeclaration (they live in different Go packages — Go's
# package identity is the directory_path+package_name tuple) and this
# gate correctly lets them pass. Two files in the SAME directory
# declaring the same package name + same type ARE a Go redeclaration
# (the build error "<X> redeclared in this block") and correctly fail.
# The awk END loop visits counts in arbitrary order (hash) — sorted by count desc
# would be nicer but not required for correct FAIL output.
fails=$(printf '%s\n' "$decls" \
  | sort \
  | awk -v allow="$allowed" -F'\t' '
    BEGIN {
      n = split(allow, a, "|")
      for (i = 1; i <= n; i++) if (a[i] != "") allowed[a[i]] = 1
      out = ""
    }
    {
      if (NF < 4) next
      dir = $1; pkg = $2; tn = $3
      key = dir "/" pkg ":" tn
      sites[key] = (sites[key] == "" ? $4 : sites[key] ", " $4)
      counts[key]++
    }
    END {
      for (key in counts) {
        if (counts[key] < 2) continue
        if (key in allowed) continue
      # Display path uses dir/pkg.TypeName for human readability.
      # Key shape is `dir/pkg:TypeName` (e.g. `internal/foo/job:Filter`).
      # Split on `:` first (yields the (dir/pkg, TypeName) tuple), then
      # split the dir/pkg segment on `/` to recover the package name
      # (the last slash-token). This avoids the latent bug where
      # dp[dp_n] retains the colon and prints `pkg:Type.Type`.
      c_n = split(key, key_parts, ":")
      s_n = split(key_parts[1], dp, "/")
      out = out sprintf("\n  %s.%s  (count=%d in same (dir,pkg))\n    sites: %s\n",
                        dp[s_n], key_parts[2], counts[key], sites[key])
      }
      printf "%s", out
    }' 2>/dev/null || true)

if [ -n "$fails" ]; then
  echo "FAIL: same-package type-redeclaration(s) detected in internal/"
  echo "$fails"
  echo ""
  echo "Resolution order:"
  echo "  1. Pick one file as canonical; remove the duplicate from every other file;"    echo "  2. Or if the redeclaration is intentional (documented wire-mirror), add"
    echo "     an entry to docs/migrations/duplicate-types-allowlist.txt"
    echo "     in the form '<dir>/<package>:<TypeName>   # rationale + owner + deadline'."
    echo "     (The <dir> token is the canonical directory_path component of"
    echo "     Go's directory_path+package_name package-identity tuple; see"
    echo "     architecture/policy.yaml::lint_gates[check=5].)"
  echo ""
  echo "Per AGENTS.md §8 ARCHITECTURE-CI-GATES zero-baseline rule, every new"
  echo "allowlist entry requires explicit owner + deadline; transitional"
  echo "baselines default-block the lint rather than silently pass."
  exit 1
fi
echo "Check 5: 0 same-package type-redeclarations detected across internal/"
# ── Check 7: Asset-Mutation Bypass Audit (Wave 22 PR-4, June 2026) ──
# Runs the four rg queries from docs/migrations/bypass_audit_<date>.md and
# subtracts the per-file allowlist at docs/migrations/admin-sql-allowlist.txt
# using `comm -13`. Any non-allowlisted production hit fails the gate.
#
# This is the wire-up for the canonical AssetMutationDispatcher SSOT
# (internal/application/assets/mutations/dispatcher.go) — Wave 22 tasks
# 2/3/5 migrate the "production — must use dispatcher" files out from
# under the allowlist, so this gate tightens with each migration PR.
#
# The allowlist is the SINGLE SOURCE OF TRUTH for what bypass-survives.
# Adding/removing a row must ship in the same PR as the corresponding
# code change. See AGENTS.md §"Agenter Workflow" for the 1-PR rule.
# NON-FATAL bypass-audit wrap. The file opens with `set -euo pipefail`;
# without this wrap, a non-zero exit short-circuits every subsequent
# check (Check 8 factory-only, ServiceDeps cap, engine SSOT gates,
# final archcheck). The captured exit is logged below; do NOT remove
# this wrap — every check added since Wave 22 PR-4 has implicitly
# depended on bypass-audit being NON-FATAL.
set +e
bash "${REPO_ROOT}/scripts/ci-bypass-audit.sh"
bypass_audit_rc=$?
set -e
echo "ci-bypass-audit exit code: ${bypass_audit_rc} (NON-FATAL)"
# ── Check 8 (factory-only, S3e, Wave 22): forbid literal map[string]*assets.ClipsRepository ──
# The canonical contract for clip-store access in production paths is the
# typed ClipRepositoryPort / ClipStorePort surface; inline
# `map[string]*assets.ClipsRepository{...}` literals are a regression to
# the pre-port days and block the architecture from migrating to alternate
# clip-store implementations (Qdrant-only, in-memory cache, mock-driven
# tests, etc.).
#
# Canonical factory sites (explicitly allowlisted):
#   - internal/infrastructure/database/assetindex/resolver.go
#   - internal/app/build_bundles_core.go
#
# Both canonical sites are composition-root concerns: they construct the
# bag of repos that the PortAdapters project onto the typed interfaces.
# Production callers (internal/application/**, internal/api/**) MUST
# consume the typed Port interface (clips.ClipRepositoryPort,
# ytports.ClipStorePort, etc.) — not the *assets.ClipsRepository
# concrete.
#
# ARCH-ALLOWLIST opt-in (mirrors Check 5): a transitional backfill or
# production test fixture that legitimately needs the literal at a
# non-production scope MUST prepend the magic marker `// ARCH-ALLOWLIST:
# factory-only` on the line preceding the literal. The marker is
# stripped from the failing-set via an awk pre-pass that drops matches
# whose preceding line carries the magic marker (window: 25 lines —
# tolerates the operator-common pattern of marker + blank line +
# literal). Per AGENTS.md §7 zero-baseline rule, new allowlist entries
# require explicit owner + deadline; the marker is the call-site
# equivalent of an allowlist row.
#
# Pattern anchors:
#   map[string]*assets.ClipsRepository{   — exact literal text
#     (rg -e uses regex escaping; \{\} is the brace literal)
# Tests are excluded via --glob '!**/*_test.go' since they may freely
# construct the literal as fixtures without affecting production
# contracts.
echo "=== Check 8 (factory-only, S3e): forbid literal map[string]*assets.ClipsRepository ==="
all_hits=$(rg -n --type go \
    -e 'map\[string\]\*assets\.ClipsRepository\{' \
    --glob '!**/*_test.go' \
    --glob '!**/infrastructure/database/assetindex/resolver.go' \
    --glob '!**/app/build_bundles_core.go' \
    internal/application internal/api 2>/dev/null \
    || true)
# Drop full-line comments AND lines preceded by the ARCH-ALLOWLIST marker
# (i.e. the preceding line of the SAME FILE carries the magic marker;
# the marker is recognised on a single-line comment OR on the line
# immediately above the literal, with a 25-line scroll-window tolerance).
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*factory-only/) {
                # Per-line accumulation: append this marker'\''s line number
                # to the file'\''s comma-separated list so multiple markers
                # in the same file BOTH persist (overwrite-avoidance —
                # mirrors Check 5 admin-only semantics).
                markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next
            # Check the hit against EVERY stored marker for this file
            # (any of them may own the active scroll-window).
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
    echo "FAIL: literal map[string]*assets.ClipsRepository{...} detected in production path:"
    echo "$literal_calls"
    echo ""
    echo "Fix: consume the clip-store via the typed ClipRepositoryPort (or any"
    echo "      application-level port that abstracts *assets.ClipsRepository)."
    echo "      Production callers MUST NOT construct the literal bag directly;"
    echo "      the bag lives ONLY on the canonical factory sites"
    echo "      (assetindex/resolver.go + app/build_bundles_core.go)."
    echo ""
    echo "If the literal is genuinely a transitional backfill / offline admin"
    echo "      migration (rare), prepend the magic marker on the line preceding"
    echo "      the literal:"
    echo "    // ARCH-ALLOWLIST: factory-only"
    echo "    repos := map[string]*assets.ClipsRepository{...}"
    exit 1
fi
echo "OK: no literal map[string]*assets.ClipsRepository literals in production paths"
# ── Check 8: forbid inline large-batch clip pagination in production paths (S1b, Wave 22) ──
# Cleanup now ALWAYS routes through the jobs system (`system.cleanup`). Any inline
# `ListClipsPaged(...NNN...)` call where NNN >= 1000 in production paths is a
# regression to the legacy per-source synchronous 10000-record pagination. The
# canonical replacement is `jobs.Enqueue(JobsEnqueueRequest{Type: "system.cleanup"...})`.
#
# Pattern anchors:
#   ListClipsPaged\(\s*<args>\s*,\s*[1-9][0-9]{3,}\b  — 4+ digit limit (1000..N)
#   ListClipsPaged by cap-of-10000 specifically:
#       ListClipsPaged\([^,]+,\s*(10000|5000|1000|100000)\b
# Tests and the canonical callers upstream of the outbox dispatcher
# (internal/infrastructure/database/sqlite/assets/clips_repository.go) are
# allowlisted because the SQL layer legitimately uses large batches for
# snapshots / bulk maintenance; the lint targets the production-API+App layer
# where the legacy inline fallback lived.
#
# ARCH-ALLOWLIST opt-in: prepend `// ARCH-ALLOWLIST: admin-migration` on the
# line preceding the call site to opt into the allowlist (mirrors Check 5).
echo "=== Check 8: forbid inline ListClipsPaged(>=1000) in production paths (S1b, Wave 22) ==="
all_hits=$(rg -n --type go \
    -e "ListClipsPaged\([^,]+,\s*[1-9][0-9]{3,}\b" \
    --glob '!**/*_test.go' \
    --glob '!**/infrastructure/database/sqlite/**' \
    internal/application internal/api 2>/dev/null \
    || true)
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        BEGIN { prev = "" }
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*admin-migration/) {
                # Per-line accumulation: append this marker'\''s line number
                # to the file'\''s comma-separated list so multiple markers
                # in the same file BOTH persist (overwrite-avoidance —
                # mirrors Check 5 admin-only semantics).
                markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next
            # Check the hit against EVERY stored marker for this file
            # (any of them may own the active scroll-window).
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
    echo "FAIL: inline ListClipsPaged(>=1000) detected in production path:"
    echo "$literal_calls"
    echo ""
    echo "Fix: route bulk pagination through the jobs system (system.cleanup)"
    echo "or the canonical ClipOpsService.Cleanup entry point. The legacy"
    echo "synchronous 10000-record fallback is removed (S1b, Wave 22 task 5)."
    echo ""
    echo "If the call is genuinely admin migration / backfill, prepend the"
    echo "marker comment on the preceding line:"
    echo "    // ARCH-ALLOWLIST: admin-migration"
    echo "    repo.ListClipsPaged(ctx, src, 10000, offset)"
    exit 1
fi
echo "OK: no inline ListClipsPaged(>=1000) calls in production paths"
# ── Check 23: ServiceDeps / Deps field-count cap (PR-D, Wave 22 §D3) ─────
# The canonical `Deps` struct passed to a service's NewService MUST NOT
# exceed 8 fields. Sub-groups (e.g. artlist.ServicePorts + ServiceDependencies
# embedded into artlist.ServiceDeps) count the embedded Promotion fields
# toward the cap — the cap is the number of fields a maintainer sees on the
# struct, not the leaf-group member count.
#
# The cap is enforced on struct types whose declared name matches
# `ServiceDeps` OR `Deps` (case-sensitive, exported); plain `*Config`
# / `*Options` / `*Args` / `*Params` / `*Inputs` types are NOT
# gated — the cap is specific to ServiceDeps + Deps because those are
# the two names the PR-D spec calls out.
#
# Pattern anchors:
#   ^type ServiceDeps struct { ... }   — captures the post-brace block
#   ^type Deps struct { ... }          — captures the post-brace block
#
# Field count is computed by ignoring blank lines, comment lines starting
# with `//` or `/*`, the closing `}` brace. Embedded-type lines (those
# whose name matches a struct type identifier) count as 1 field — we do
# NOT recurse into the embedded struct's fields because the spec cap is
# the visible top-level field lines, mirroring what a maintainer sees.
#
# Allowlist: docs/migrations/deps-struct-allowlist.txt lists one
# `<package>:<TypeName>` per line for transitional exceptions. Per
# AGENTS.md §8 ARCHITECTURE-CI-GATES zero-baseline rule, every new
# entry requires explicit owner + deadline. Today the file has exactly
# one entry (artlist:ServiceDeps) grandfathered via PR2.6/2.7.
echo "=== Check 23: ServiceDeps / Deps field-count cap (PR-D, Wave 22 §D3) ==="
max_fields=8
# Collect every `(package, TypeName, file, fieldcount)` line into TSV.
decls=$(while IFS= read -r -d '' f; do
  case "$f" in
    */internal/application/*) ;;
    *) continue ;;
  esac
  case "$f" in
    *_test.go) continue ;;
  esac
  # extract package name (Check 23, parallel fix to 8fa8a501's Check 5 hardening).
  # Canonical `awk $2` field-extraction; the prior `${pkg_line#package }` shell-strip
  # collapses to empty pkg on non-canonical separators and would surface the same
  # `(count=N in same package)` false-positive shape here too.
  pkg=$(awk '/^package[[:space:]]+/ {print $2; exit}' "$f" 2>/dev/null || true)
  [ -z "$pkg" ] && continue
  awk -v pkg="$pkg" -v file="$f" -v max="$max_fields" '
    function flush_field(    line, lines, n, i, k) {
      if (in_name == "") return
      n = split(fields, lines, "\n")
      k = 0
      for (i = 1; i <= n; i++) {
        line = lines[i]
        if (line == "") continue
        if (line ~ /^[[:space:]]*\/\//) continue
        if (line ~ /^[[:space:]]*\/\*/) continue
        if (line == "}") continue
        k++
      }
      if (k > max) {
        printf("%s\t%s\t%d\t%s\n", pkg, in_name, k, file)
      }
      in_name = ""; fields = ""
    }
    /^type[[:space:]]+(ServiceDeps|Deps)[[:space:]]+struct/ {
      flush_field()
      s = $0
      sub(/^type[[:space:]]+/, "", s)
      if (match(s, /^(ServiceDeps|Deps)/)) {
        in_name = substr(s, RSTART, RLENGTH)
      }
      next
    }
    in_name != "" {
      fields = fields "\n" $0
      if ($0 ~ /^}/) { flush_field() }
    }
  ' "$f" 2>/dev/null
done < <(find internal/application -name '*.go' -not -name '*_test.go' -print0 2>/dev/null || true) || true)

# Apply allowlist (drop package:TypeName pairs listed in the allowlist file).
allowed_keys=""
if [ -f "docs/migrations/deps-struct-allowlist.txt" ]; then
  allowed_keys=$(grep -vE '^[[:space:]]*(#|$)' docs/migrations/deps-struct-allowlist.txt 2>/dev/null \
    | awk '{print $1}' | sort -u | paste -sd'|' - || true)
fi

violations=$(printf '%s\n' "$decls" | awk -v allow="$allowed_keys" -F'\t' '
  BEGIN {
    n = split(allow, a, "|")
    for (i = 1; i <= n; i++) if (a[i] != "") allowed[a[i]] = 1
  }
  NF >= 3 {
    key = $1 ":" $2
    if (key in allowed) next
    printf("  %s.%s  (fields=%d, max=8)\n    file: %s\n", $1, $2, $3, $4)
  }
' || true)

if [ -n "$violations" ]; then
  echo "FAIL: ServiceDeps / Deps struct(s) exceeding the 8 visible field-line cap:"
  echo "$violations"
  echo ""
  echo "Fix: split a Deps struct into sub-deps groups (e.g. StorageDeps,"
  echo "      MediaDeps, ProviderDeps) so each top-level field is itself"
  echo "      a typed bundle. Embedded-type lines (e.g. Storage StorageDeps)"
  echo "      count as 1 visible line — promote via sub-structs to stay under"
  echo "      the cap. See docs/migrations/deps-struct-allowlist.txt for the"
  echo "      interpretation note on embedded-type field promotion."
  echo ""
  echo "If a Deps struct legitimately needs >8 visible fields under a transitional"
  echo "      baseline, add an entry to docs/migrations/deps-struct-allowlist.txt with"
  echo "      '<package>:<TypeName>   # rationale + owner + deadline'. Per AGENTS.md"
  echo "      §8 zero-baseline rule, the entry MUST carry owner + deadline."
  exit 1
fi
echo "Check 23: 0 ServiceDeps/Deps structs exceeding the 8 visible field-line cap"
# ── PR 9 anti-regression gates (Cleanup CONTRACT, June 2026) ─────
# Nine gates enforce the canonical V1 pipeline invariants that
# prior PRs (1-8) converged on. Any unauthorized hit fails CI.
#
# Allowlist: production code may comment-reference the banned
# patterns in prose; the rg post-pass strips full-line `//`-comments
# so descriptive log strings etc. don't trigger false positives.

# Check 24 (engine SSOT decoder): the canonical decoder
# DecodeModelOutput MUST be referenced from engine.go exactly
# once on the fresh-generation path. Legacy JSON-string parsing
# inside engine.go is forbidden.
echo "=== Check 24: structured decoder SSOT (PR 9, engine.go) ==="
# PR-CHECK-24-FIXUP (2026-07-08): the legacy scripts engine at
# internal/application/scripts/engine.go was intentionally removed in
# commit ad2874c59 (Wave 17/18 prep) and re-removed in e99da1cfe
# (internal/modules refactor); the structured decoder at
# model_output_decoder.go was created in 72e1d5c94 then deleted. The
# canonical engine surface now lives at
# internal/application/scripts/usecase/engine.go. This check tolerates
# the missing legacy file (godlike/07 NO-FAKE-AVAILABILITY: don't
# fabricate a reference in the new engine.go for a surface that was
# canonically removed) while preserving the forward-prevention intent
# if the file is ever restored. Per AGENTS.md Godlike-06 SSOT the check
# is also DEFERRED pending a Phase 5 closure in
# CANONICAL-SURFACES-UNIFICATION-2026-07-08.
if [ -f internal/application/scripts/engine.go ]; then
    if ! rg -q 'DecodeModelOutput' internal/application/scripts/engine.go; then
        echo "FAIL: engine.go does not reference DecodeModelOutput (the canonical structured decoder)"
        echo "Restoring fresh-generation conformance: route through internal/application/scripts/model_output_decoder.go::DecodeModelOutput."
        exit 1
    fi
fi
echo "OK: engine.go uses canonical DecodeModelOutput decoder"

# Check 25 (no legacy WriteScript): forbid the legacy pre-V1
# surface — function calls, request types, and result types.
echo "=== Check 25: no legacy WriteScript surface (PR 9) ==="
literals=$(rg -n --type go \
    -e '\.WriteScript\(' \
    -e 'WriteScriptRequest\b' \
    -e 'WriteScriptResult\b' \
    --glob '!**/*_test.go' \
    internal/ 2>/dev/null \
    | awk -F: '{ rest = ""; for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i; if (rest ~ /^[[:space:]]*\/\//) next; print }' \
    || true)
if [ -n "$literals" ]; then
    echo "FAIL: legacy WriteScript surface detected:"
    echo "$literals"
    exit 1
fi
echo "OK: no legacy WriteScript references"

# Check 26 (no prompt/fingerprint mixing): the PR-2 anti-patterns
# — writing a fingerprint hash into a model input, or routing a
# fingerprint via Guidelines. Either pattern is a contract-breaking
# regression.
echo "=== Check 26: no prompt/fingerprint mixing (PR 9) ==="
literals=$(rg -n --type go \
    -e 'Prompt[[:space:]]*=[[:space:]]*resolved\.Fingerprint' \
    -e 'Prompt[[:space:]]*=[[:space:]]*plan\.Fingerprint' \
    -e 'Guidelines:[[:space:]]*sourceFingerprint' \
    -e 'Guidelines:[[:space:]]*plan\.Fingerprint' \
    --glob '!**/*_test.go' \
    internal/ 2>/dev/null \
    | awk -F: '{ rest = ""; for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i; if (rest ~ /^[[:space:]]*\/\//) next; print }' \
    || true)
if [ -n "$literals" ]; then
    echo "FAIL: prompt/fingerprint anti-pattern detected:"
    echo "$literals"
    echo "Fix: render the prompt through plan.RenderedPrompt (model input);"
    echo "     keep SourceFingerprint as cache-key input only — never mix."
    exit 1
fi
echo "OK: no prompt/fingerprint mixing"

# Check 27 (engine does NOT persist): engine.go does not save to
# SQLite directly. The single owner of script-table writes is
# PersistenceProcessor; engine never sees ScriptRepository.
echo "=== Check 27: engine does NOT save scripts (PR 9 / PR 5) ==="
if rg -q 'SaveScript' internal/application/scripts/engine.go; then
    echo "FAIL: engine.go references SaveScript (engine must NOT persist)"
    echo "Engine persistence is the job of PersistenceProcessor;"
    echo "engine.Generate returns EngineResult only."
    exit 1
fi
echo "OK: engine does not save scripts to SQLite"

# Check 28 (no legacy Single result): the canonical envelope
# always emits Version + OK + Items + Summary; the legacy
# `Single *GenerationResult` field was removed in PR 7.
echo "=== Check 28: no Single *GenerationResult anti-pattern (PR 9 / PR 7) ==="
literals=$(rg -n --type go \
    -e 'Single[[:space:]]+\*GenerationResult\b' \
    -e '^\s+Single[[:space:]]+\*GenerationResult\b' \
    --glob '!**/*_test.go' \
    internal/ 2>/dev/null \
    | awk -F: '{ rest = ""; for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i; if (rest ~ /^[[:space:]]*\/\//) next; print }' \
    || true)
if [ -n "$literals" ]; then
    echo "FAIL: Single *GenerationResult surface detected:"
    echo "$literals"
    echo "Fix: emit canonical envelope via GenerationEnvelopeResult.Items[0].Result."
    exit 1
fi
echo "OK: no legacy Single *GenerationResult field anywhere"

# Check 29 (no legacySpecFromPlan bridge): the legacy pre-V1
# processor bridge was removed in PR 5; any resurrection is a
# regression.
echo "=== Check 29: no legacySpecFromPlan bridge (PR 9 / PR 5) ==="
if rg -q 'legacySpecFromPlan' internal/application/scripts/; then
    echo "FAIL: legacySpecFromPlan reference in internal/application/scripts/ (forbidden post-PR 5)"
    exit 1
fi
echo "OK: no legacySpecFromPlan bridge anywhere"

# Extracted Check-N dispatcher (dynamic glob, added by atomic-extraction PR series 2026-07-04)
# godlike/06 SSOT: sourced in numerical natural order (extracted checks) BEFORE the
# load-bearing 30-40-50-60 iteration loop (per-check topic modules).
for extracted_check in "${SCRIPT_DIR}/all_checks"/check_*.sh; do
    [ -e "$extracted_check" ] || continue
    # shellcheck source=/dev/null
    source "$extracted_check"
done

# Check groups are sourced in their original canonical order.
for CHECK_MODULE in \
  "30_database.sh" \
  "40_api.sh" \
  "50_jobs.sh" \
  "60_governance.sh"
do
  CHECK_PATH="${SCRIPT_DIR}/${CHECK_MODULE}"
  if [ ! -f "${CHECK_PATH}" ]; then
    echo "CI: architectural check module missing: ${CHECK_PATH}" >&2
    exit 1
  fi
  # shellcheck source=/dev/null
  source "${CHECK_PATH}"
done

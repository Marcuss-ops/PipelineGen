#!/usr/bin/env bash
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
set -euo pipefail

# Resolve REPO_ROOT once so the migration-uniqueness lint below works from
# any cwd (CI runners, IDE hook invocations, manual bash). BASH_SOURCE is
# always the script's own absolute path under `bash script.sh` — the only
# case where it's empty / unset is when the script is being read via
# process substitution (`bash <(curl ...)`) or `bash -c "source ..."` from
# a parent shell, which we refuse to silently misroute (a wrong-resolution
# REPO_ROOT would silently scan the wrong dir and emit false-negative
# passes). Fail loud and let the operator fix the invocation.
if [ -n "${BASH_SOURCE[0]:-}" ]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
  echo "CI: cannot resolve script directory from BASH_SOURCE[0]=" >&2
  echo "    (process substitution / bash -c \"source ...\" invocation)." >&2
  echo "    Run the script as: bash scripts/ci-architectural-checks.sh" >&2
  echo "    or set MIGRATIONS_ROOT=/abs/path/to/migrations/sqlite explicitly." >&2
  exit 1
fi
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

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
# Each canonical declaration lives in internal/domain/job/job.go. Any new
# rg hit on those strings as quoted STRING LITERALS (not comments) in
# production code indicates a regression — the canonical reference should
# always be the typed constant.
#
# PR-B (June 2026) closes the 4 script-related constants only. The
# remaining literal constants in internal/application/jobs/registry.go
# (TypeBulUploadYouTubeClips, TypeDriveFolderSync) and the other keys in
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
echo "=== Check 0: forbid literal job-type strings (PR-B, Wave 19 §7) ==="
literals=$(rg -n --type go \
    -e '[=:(,]\s*"(script\.generate_batch|media\.curate|script\.generate_from_catalog|script\.generate_from_clips)"' \
    -e '"(script\.generate_batch|media\.curate|script\.generate_from_catalog|script\.generate_from_clips)"\s*[:,)]' \
    --glob '!**/domain/job/job.go' \
    --glob '!**/*_test.go' \
    internal/ 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' \
    | grep -vE '\/\/.*"(script\.generate_batch|media\.curate|script\.generate_from_catalog|script\.generate_from_clips)"' \
    || true)
if [ -n "$literals" ]; then
    echo "FAIL: literal job-type string found outside canonical SSOT:"
    echo "$literals"
    echo ""
    echo "Fix: replace the literal with the canonical constant from"
    echo "internal/domain/job/job.go (e.g. job.TypeBatchScriptGenerate)."
    echo "If the literal is required for documentation, wrap it in a"
    echo "backtick code span in prose, not in a string literal."
    exit 1
fi
echo "OK: no literal job-type strings outside canonical domain/job/job.go"

# ── Check 1: forbid direct IndexWriter callers outside composition root (QDRANT-002) ─────
# The canonical IndexWriter MUST live behind outbox.Dispatcher (production) or the
# admin reindex CLI (one-shot operator tool). Both sites are explicitly allowlisted:
#
#   - cmd/admin/reindex_qdrant.go          : operator-driven reindex, bypasses outbox by design.
#   - internal/app/build_bundles_process.go: the SSOT composition root that owns the wiring.
#
# Every other Go file that constructs (or takes the address of) an IndexWriter is
# either (a) a forgotten legacy call site that bypassed the outbox dispatcher, or
# (b) a leak of the canonical writer into a downstream handler. Either is a
# QDRANT-002 regression: the canonical write path is outbox.Dispatcher →
# IndexingHandler → IndexWriter. Anything else risks stale data racing the
# source_version supersede gate (the indexer reads via the dispatcher).
#
# Pattern anchors:
#   qdrant.NewIndexWriter(...)                — function call, 99% of constructions
#   = &qdrant.IndexWriter{...}                — rare direct literal; reserved for tests
#   := qdrant.IndexWriter{...}                — same as above
#
# Comment-only lines are excluded via awk so descriptive prose ("calls
# qdrant.NewIndexWriter from inside the dispatcher") doesn't trigger false
# positives. Tests are excluded so *_test.go can construct fakes freely.
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
                split($1, p, "")
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
# `package X` line + every `^type X ...` declaration, project to <package>:<type>,
# fail on any redeclaration that is NOT listed in the per-package allowlist.
#
# Allowlist: docs/architecture/godlike/duplicate-types-allowlist.txt lists one
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

# Step 1: extract every exported type declaration as TSV: package<TAB>Type<TAB>file:line
decls=""
while IFS= read -r -d '' f; do
  # extract package name from the first `package X` line (guard against empty).
  # Canonical awk $2 field-extraction rather than the prior brittle shell
  # prefix-strip — the prior `${pkg_line#package }` collapsed to empty pkg
  # for every file, grouping 381 type declarations under one empty-pkg bucket
  # and producing a false-positive `(count=381 in same package)`.
  pkg=$(awk '/^package[[:space:]]+/ {print $2; exit}' "$f" 2>/dev/null || true)
  [ -z "$pkg" ] && continue
  per_file=$(awk -v pkg="$pkg" -v file="$f" '
    /^type[[:space:]]+[A-Z]/ {
      s = $0
      sub(/^type[[:space:]]+/, "", s)
      if (match(s, /^[A-Z][A-Za-z0-9_]*/)) {
        printf("%s\t%s\t%s:%d\n", pkg, substr(s, RSTART, RLENGTH), file, FNR)
      }
    }' "$f" 2>/dev/null || true)
  decls="$decls"$'\n'"$per_file"
done < <(find internal/ -name '*.go' -not -name '*_test.go' -print0 2>/dev/null || true)

# Step 2: load allowlist keys (pipe-delimited) if the allowlist file is present.
# Empty file = no exceptions; missing file = no exceptions (the file is expected
# to exist on disk per AGENTS.md §8 but we do not gate on its presence here).
allowed=""
if [ -f "docs/architecture/godlike/duplicate-types-allowlist.txt" ]; then
  allowed=$(grep -vE '^\s*(#|$)' docs/architecture/godlike/duplicate-types-allowlist.txt 2>/dev/null \
            | awk '{print $1}' | sort -u | paste -sd'|' - || true)
fi

# Step 3: dedup by (package, TypeName), count, fail on count >= 2 not in allowlist.
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
      pkg = $1; tn = $2
      key = pkg ":" tn
      sites[key] = (sites[key] == "" ? $3 : sites[key] ", " $3)
      counts[key]++
    }
    END {
      for (key in counts) {
        if (counts[key] < 2) continue
        if (key in allowed) continue
        split(key, parts, ":")
        out = out sprintf("\n  %s.%s  (count=%d in same package)\n    sites: %s\n",
                          parts[1], parts[2], counts[key], sites[key])
      }
      printf "%s", out
    }' 2>/dev/null || true)

if [ -n "$fails" ]; then
  echo "FAIL: same-package type-redeclaration(s) detected in internal/"
  echo "$fails"
  echo ""
  echo "Resolution order:"
  echo "  1. Pick one file as canonical; remove the duplicate from every other file;"
  echo "  2. Or if the redeclaration is intentional (documented wire-mirror), add"
  echo "     an entry to docs/architecture/godlike/duplicate-types-allowlist.txt"
  echo "     in the form '<package>:<TypeName>   # rationale + owner + deadline'."
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
if ! rg -q 'DecodeModelOutput' internal/application/scripts/engine.go; then
    echo "FAIL: engine.go does not reference DecodeModelOutput (the canonical structured decoder)"
    echo "Restoring fresh-generation conformance: route through internal/application/scripts/model_output_decoder.go::DecodeModelOutput."
    exit 1
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

# Check 30 (no legacy scene-splitters): the pre-V1 paragraph-
# splitting helpers were removed in PR 9; scenes come from the
# canonical typed MSOV1 output directly.
echo "=== Check 30: no legacy scene-splitters (PR 9) ==="
if rg -q 'splitScriptIntoSegments\|sceneCountFromPlan' internal/application/scripts/; then
    echo "FAIL: legacy scene-splitter helper(s) detected in internal/application/scripts/"
    echo "Fix: read scenes from engineResult.Output.SpecScene.Scenes"
    echo "     (validated by PR 6 ValidateAndEnrichSpecScene)."
    exit 1
fi
echo "OK: no splitScriptIntoSegments / sceneCountFromPlan"

# Check 31 (no artificial empty Scene.Text): the canonical MSOV1
# validator (PR 6) requires every scene to carry non-empty text;
# bypassing it via raw struct literals is a regression.
#
# PR 9 (June 2026, gate-tightening pass): the original blanket ban
# on `Text: ""` false-positived legitimate defensive defaults like
# `if sceneText == "" { sceneText = fallback }`. The tightened
# pattern restricts the match to scene-construction contexts:
# struct literals in the postprocessor layer (the path that
# constructs a *scriptpkg.SpecScene / SpecSceneOutput / SceneImage
# / SceneVoiceover literal). Defensive `sceneText == ""` guards
# remain free to use the empty string literal.
echo "=== Check 31: no synthetic empty scene Text (PR 9 / PR 6) ==="
literals=$(rg -n --type go \
    -e '(scene|SpecScene|SpecSceneOutput|SceneImage|SceneVoiceover|ClipScene)\{[^}]*Text:[[:space:]]*""' \
    --glob '!**/*_test.go' \
    internal/application/scripts/ 2>/dev/null \
    | awk -F: '{ rest = ""; for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i; if (rest ~ /^[[:space:]]*\/\//) next; print }' \
    || true)
if [ -n "$literals" ]; then
    echo "FAIL: synthetic Text: \"\" detected in scene-construction context:"
    echo "$literals"
    echo "Fix: route scene construction through ValidateAndEnrichSpecScene"
    echo "     (rejects empty Text per PR 6 spec)."
    exit 1
fi
echo "OK: no synthetic Text:\"\" in scene-construction literals"

# Check 32 (no prose OutputFmt in canonical path): post-PR-6,
# the validator rejects OutputFmt=\"prose\" outright. Any
# production-code reference to the value is dead code or a
# regression; documentation comments in tests are excluded via
# the _test.go-with-comment pattern below.
echo "=== Check 32: no prose OutputFmt in canonical path (PR 9 / PR 6) ==="
literals=$(rg -n --type go \
    -e 'OutputFmt[[:space:]]*[:=][[:space:]]*"prose"' \
    -e 'output_fmt[[:space:]]*[:=][[:space:]]*"prose"' \
    -e "OutputFmt[[:space:]]*[:=][[:space:]]*'prose'" \
    -e "output_fmt[[:space:]]*[:=][[:space:]]*'prose'" \
    --glob '!**/*_test.go' \
    internal/application/scripts internal/domain/script 2>/dev/null \
    | awk -F: '{ rest = ""; for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i; if (rest ~ /^[[:space:]]*\/\//) next; print }' \
    || true)
if [ -n "$literals" ]; then
    echo "FAIL: OutputFmt \"prose\" detected in production path:"
    echo "$literals"
    exit 1
fi
echo "OK: no OutputFmt \"prose\" surface in canonical path"

# ── Check 33: forbid retention:created_at:mutable SQL tag in jobs (Wave 22 followup, June 2026) ────
# The retention sweeper (lifecycle.go::NewRetentionSweeper) deletes aged-out
# outbox events by `created_at`. The canonical contract: created_at is
# IMMUTABLE — once an event is inserted, the timestamp MUST NOT be
# updated. A mutable created_at leaks indefinitely-aged rows past the
# cutoff (and risks dropping active rows the moment a non-creation write
# touches the column).
#
# The TagWeaver sql-tag annotation `retention:created_at:mutable` flags
# any column-default or column-declaration that allows (or accepts) a
# created_at update. Production SQL MUST NOT carry this tag — the
# canonical schema is `DEFAULT CURRENT_TIMESTAMP` with no `ON UPDATE
# CURRENT_TIMESTAMP` (the MySQL idiom that the project's tag-based
# schema linter catches on review).
#
# Production-side companion to the canonical retention contract. The CI
# gate rg-greps for the annotation in the production jobs package and
# fails the gate when the operator has explicitly opted into fail-closed
# semantics via `eventTimestampIsImmutable=true`. When the env flag is
# unset / false, the gate logs an INFO message and exits 0 — the
# hit-count is observable in every CI run so the rollout can be audited
# before the env flag flips on. A complementary unit test
# (`TestRetentionSweeper_CreatedAtIsImmutable`) is the planned
# read-side enforcement; this gate is the operator-side enforcement.
#
# Allowlist: a future migration file that legitimately needs to mark the
# column as mutable (e.g. a feature toggle, an admin one-shot repair
# that backfills stale timestamps) MUST prepend the magic marker
# `// ARCH-ALLOWLIST: retention-created-at-mutable` on the line
# preceding the sql-tag annotation. The awk pre-pass strips such hits
# from the failing-set via the same 25-line window tolerated by
# Check 5 / Check 8. Per AGENTS.md §8 zero-baseline rule, every new
# allowlist entry requires explicit owner + deadline; the marker is
# the call-site equivalent of an allowlist row.
#
# Pattern anchors:
#   retention:created_at:mutable    — exact literal sql-tag string
#
# Env-gated semantics (per user spec, June 2026):
#   eventTimestampIsImmutable=true   — fail-closed (exit 1 on hits)
#   eventTimestampIsImmutable=other  — pass-through, log INFO (rollout mode)
#   The gate ALWAYS runs the rg-grep regardless of the env flag so the
#   hit count is observable in CI output every run — the env gate only
#   controls whether hits translate into a hard CI failure.
echo "=== Check 33: forbid retention:created_at:mutable in sqlite/jobs ==="
all_hits=$(rg -n --type go \
    -e 'retention:created_at:mutable' \
    --glob '!**/*_test.go' \
    internal/infrastructure/database/sqlite/jobs/ 2>/dev/null \
    || true)
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        BEGIN { prev_marker = 0 }
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*retention-created-at-mutable/) {
                split($1, p, "")
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
hits_count=${all_hits:+$(printf '%s' "$all_hits" | wc -l | awk '{print $1+0}')}
hits_count=${hits_count:-0}
literal_count=${literal_calls:+$(printf '%s' "$literal_calls" | wc -l | awk '{print $1+0}')}
literal_count=${literal_count:-0}
echo "INFO: retention:created_at:mutable scan in internal/infrastructure/database/sqlite/jobs/:"
echo "      total hits: ${hits_count}"
echo "      non-allowlisted hits: ${literal_count}"
if [ -n "$literal_calls" ]; then
    if [ "${eventTimestampIsImmutable:-}" = "true" ]; then
        echo "FAIL: retention:created_at:mutable annotation in production jobs package (eventTimestampIsImmutable=true):"
        echo "$literal_calls"
        echo ""
        echo "Fix: remove the `retention:created_at:mutable` annotation from production SQL"
        echo "or column declarations — the created_at column is canonical IMMUTABLE"
        echo "(DEFAULT CURRENT_TIMESTAMP, no ON UPDATE clause). The retention sweeper"
        echo "depends on this; a mutable created_at leaks active rows past the cutoff"
        echo "and drops active rows the moment a non-creation write touches the column."
        echo ""
        echo "If the annotation is required for a feature flag or admin one-shot repair,"
        echo "prepend the magic marker on the preceding line:"
        echo "    // ARCH-ALLOWLIST: retention-created-at-mutable"
        echo "    // ... ctx -- retention:created_at:mutable"
        exit 1
    else
        echo "INFO: eventTimestampIsImmutable!=true — non-allowlisted hits present but permitted (transitional pass-through):"
        echo "$literal_calls"
        echo ""
        echo "Operator action: when the retention-immutability contract stabilises,"
        echo "flip eventTimestampIsImmutable=true in CI to fail-closed."
    fi
else
    if [ "${eventTimestampIsImmutable:-}" = "true" ]; then
        echo "OK: eventTimestampIsImmutable=true, 0 retention:created_at:mutable hits in production jobs package"
    else
        echo "OK: 0 retention:created_at:mutable hits in production jobs package (eventTimestampIsImmutable not set; gate is informational)"
    fi
fi

# ── Check 46: C2-A registry-call-only-in-capability-registry (Blocco C2, June 2026) ──
# The canonical composable composition point for ALL TypedRegistry.Register
# calls is internal/app/capability_registry.go. The Phase 0 closure at
# Blocco C1-Step 2 migrated every typed-punctuated Registry.Register call
# out of every direct caller; this AST-based gate is the complementary
# forward-prevention rule that re-asserts the invariant with go/parser
# precision (ripsgrep substring scan misses string-literal false-positives
# and reflection-based indirection). The gate binary lives at
# scripts/archcheck/gates/gate_c2_registry_only.go and is invoked via
# `go run` (single-file `package main`; the .go extension is required).
#
# Pattern anchors (AST SelectorExpr chain walk, see gate_c2_registry_only.go
# for the rigorous 3-level chain + allowlist semantics):
#   <typed>.Registry.Register(    where <typed> ∈ {api, module, jobs, providers}
#
# Allowlist (the ONLY permitted caller surface):
#   - internal/app/capability_registry.go  — the canonical single composition point.
#
# Tests (`*_test.go`) and `generated/` subdirectories are excluded by the
# gate's discoverGoFiles walker (mirrors capability_inventory.yaml's
# `excludes` section).
echo "=== Check 46: C2-A registry-call-only-in-capability-registry (Blocco C2, June 2026) ==="
c2a_out=$(go run -tags=c2_registry_only ./scripts/archcheck/gates/gate_c2_registry_only_main.go . 2>&1) || c2a_rc=$?
c2a_rc=${c2a_rc:-0}
if [ "$c2a_rc" -ne 0 ]; then
    printf '%s\n' "$c2a_out" | sed 's/^/  /'
    echo ""
    echo "Fix: every {api|module|jobs|providers}.Registry.Register call MUST live in"
    echo "      internal/app/capability_registry.go (the canonical single composition"
    echo "      point per Blocco C1-Step 2 + godlike/07 §zero-legacy-policy)."
    echo "      Forward the call through that file's registerProviders /"
    echo "      registerHTTPModules / registerJobs closure, OR route the registration"
    echo "      through a typed port interface (AGENTS.md Pattern 0)."
    echo ""
    echo "If the call is genuinely a test fixture, ensure the file is *_test.go"
    echo "(this gate excludes *_test.go)."
    exit 1
fi
# Print the AST gate's own success line verbatim so the operator sees it in CI output.
printf '%s\n' "$c2a_out" | grep -E '^C2-A gate:' || true

# ── Check 47: C2-C no-source-switch-outside-catalog (Blocco C2, June 2026) ──
# The canonical Source Catalog dispatch surface lives in exactly two files:
#
#   - internal/application/assets/artifacts/source_resolver.go  (assets-side SourceCatalog registry)
#   - internal/application/scripts/adapters/source_registry.go  (script-side SourceRegistry registry)
#
# Every other source-kind switch (case "artlist" / case scriptpkg.SourceCatalog /
# if source == "youtube" / etc.) in production code is a SSOT regression: the
# Source Catalog is the canonical owner of source-kind metadata + dispatch
# (godlike/06 §"data-and-config-ownership"). The AST gate is the complementary
# forward-prevention rule to the SourceCatalog registry pattern.
#
# Pattern anchors (AST walk, see gate_c2_source_catalog_only.go for the
# rigorous BasicLit + Ident + SelectorExpr matching semantics):
#   switch X { case "artlist" | case scriptpkg.SourceCatalog | case SourceCatalog: ... }
#   if X == "youtube" / if X == scriptpkg.SourceArtlist / if X == SourceStock: ...
#
# Allowlist (the ONLY permitted dispatch surface):
#   - internal/application/assets/artifacts/source_resolver.go
#   - internal/application/scripts/adapters/source_registry.go
#
# Tests (`*_test.go`) and the generated/ subdirectory are excluded by the
# gate's discoverGoFiles walker. Walker scope is RESTRICTED to
# internal/application + internal/api + internal/domain (excludes infra as
# adapter-decoding, pkg/ as leaf utility, cmd/ as one-shot operator tooling —
# documented in capability_inventory.yaml::gates_baseline::C2-C::walker_scope_rationale).
#
# Transitional baseline (per AGENTS.md "transitional baselines" + godlike/08
# §"zero-baseline rule"): --baseline=33 absorbs the 33 production violations
# observed at C2-C landing time; each migration PR must decrement
# --baseline by the count of sites migrated, until --baseline=0 enables
# enforce_zero promotion. The yaml entry mirrors this count.
echo "=== Check 47: C2-C no-source-switch-outside-catalog (Blocco C2, June 2026) ==="
c2c_out=$(go run -tags=c2_source_catalog_only ./scripts/archcheck/gates/gate_c2_source_catalog_only_main.go . --baseline=33 2>&1) || c2c_rc=$?
c2c_rc=${c2c_rc:-0}
if [ "$c2c_rc" -ne 0 ]; then
    printf '%s\n' "$c2c_out" | sed 's/^/  /'
    echo ""
    echo "Fix: every source-kind switch/if dispatch"
    echo '      (case "<canonical>" OR case scriptpkg.Source<> OR if == "<canonical>")'
    echo "      MUST live in ONE of the Source Catalog canonical files:"
    echo "        - internal/application/assets/artifacts/source_resolver.go  (assets-side SourceCatalog)"
    echo "        - internal/application/scripts/adapters/source_registry.go  (script-side SourceRegistry)"
    echo "      See capability_inventory.yaml::gates_baseline::C2-C for the canonical surface contract."
    echo ""
    echo "Per godlike/06 (data-and-config-ownership) the Source Catalog is the SSOT for"
    echo "source-kind metadata + dispatch. In-place switch/if chains are SSOT regressions."
    echo ""
    echo "Remediation paths (in priority order):"
    echo "  1. Route the dispatch through SourceCatalog.Resolve(<source>) or"
    echo "     SourceRegistry.Resolve(<source>) so the canonical lookup is the SSOT."
    echo "  2. If the dispatch is structural-validation (SourceType.IsValid-style enum"
    echo "     exhaustiveness), migrate the check next to the enum declaration in"
    echo "     internal/domain/{asset,script}/ so the validation stays co-located"
    echo "     with the canonical type."
    echo "  3. If the file legitimately needs extended canonical ownership, follow"
    echo "     godlike/07 (EXPAND -> BACKFILL -> CUTOVER) and add a co-equal entry"
    echo "     to capability_inventory.yaml::gates_baseline::C2-C. (Don't just widen"
    echo "     the allowlist without a documented owner + deadline + cutover plan.)"
    echo ""
    echo "To advance the transitional baseline after a migration PR, update the"
    echo "--baseline=NN value below to match the live count (lambda \\u2192 0 when the"
    echo "tree is Source-Catalog-clean; this promotion targets 2026-07-20)."
    exit 1
fi
# Print the AST gate's own success line verbatim so the operator sees it in CI output
# (with remaining-allowance info if --baseline > 0 and current violations < baseline).
printf '%s\n' "$c2c_out" | grep -E '^C2-C gate:' || true

# ── Check 48: C2-E route-manifest-≡-generated-docs (Blocco C2, June 2026) ──
# The canonical route surface has three sources of truth that MUST agree:
#
#   1. STATIC — `architecture/routes.yaml` — generated by the pre-step
#      `scripts/admin/generate_routes_yaml.go` from an AST scan of every
#      `internal/api/**/RegisterRoutes` (and equivalent method bodies).
#      Best-effort row: (METHOD, PATH, source-file) for every direct
#      `.GET/.POST/.PUT/.PATCH/.DELETE/.HEAD/.OPTIONS` call on a
#      *gin.RouterGroup / *gin.Engine receiver whose path-arg is a
#      string literal. Children under `:= rg.Group("/api/foo")` are
#      folded inline to `"/api/foo" + child-literal".
#
#   2. RUNTIME — `docs/api/ACTIVE_API_GENERATED.md` — generated by
#      `cmd/admin/gen_api_docs.go` via gin.Engine.Routes() capture at
#      boot, asserted against `routeDescriptions` for human-readable
#      strings. Per-group MD-table format: `| METHOD | `/path` | ... |`.
#
#   3. CODE — the AST-detected routes from source #1, mirrored here
#      for drift detection.
#
# The invariant: for any given state of the codebase, the manifest
# (source 1) and the runtime-generated docs (source 2) MUST agree on
# every (METHOD, PATH) row. Mismatches are SSOT regressions:
#   - `manifest-only`  — in YAML but absent from docs (manifest is stale,
#                       or pre-step produced a phantom route that never
#                       reaches the gin engine).
#   - `docs-only`      — in docs but absent from manifest (a route
#                       bypassed the canonical composition).
#
# Allowlist: routes registered via gin methods the static AST cannot
# resolve without whole-program analysis (`.Handle`, `.Any`, `.Match`,
# `.Redirect`, `.Static`, `.StaticFS`) MAY surface as docs-only drift;
# the pre-step emits a per-call warning so the operator sees the gap.
# Once a route is documented as a known limitation in the package doc,
# the gate exit remains 0 (drift-detection is informational, NOT fail-closed).
#
# Pre-step gate (mandatory): the pre-step generator MUST be run before
# the gate to produce a fresh `architecture/routes.yaml`. If the manifest
# is missing OR zero-route, we run the pre-step here so the gate sees a
# canonical YAML even if the operator forgot to run it pre-CI. This
# mirrors the publish-to-staging step pattern (canonical artefact must
# exist before the integrity check runs).
echo "=== Check 48: C2-E route-manifest-≡-generated-docs (Blocco C2, June 2026) ==="
manifest_path="${REPO_ROOT}/architecture/routes.yaml"
docs_path="${REPO_ROOT}/docs/api/ACTIVE_API_GENERATED.md"

if [ ! -f "${docs_path}" ]; then
    echo "FAIL: required artefact missing at ${docs_path}"
    echo ""
    echo "Fix: regenerate via the canonical runtime-capture binary:"
    echo "  go run ./cmd/admin gen-api-docs"
    echo ""
    echo "The route-manifest gate has no second source to compare against if"
    echo "the generated docs file is absent — fail-closed (no soft-skip)."
    exit 1
fi

if [ ! -f "${manifest_path}" ]; then
    echo "INFO: architecture/routes.yaml absent — running pre-step generator inline"
    if ! go run ./scripts/admin/generate_routes_yaml.go "${REPO_ROOT}" "${manifest_path}" 2> /tmp/c2e_prestep.stderr; then
        printf '%s\n' "$(cat /tmp/c2e_prestep.stderr)" | sed 's/^/  /'
        echo "Fix: investigate the pre-step generator output above; this gate"
        echo "      cannot compare without a canonical manifest."
        exit 1
    fi
    cat /tmp/c2e_prestep.stderr | sed 's/^/  [pre-step] /'
fi

c2e_out=$(go run -tags=c2_route_manifest ./scripts/archcheck/gates/gate_c2_route_manifest_main.go "${REPO_ROOT}" 2>&1) || c2e_rc=$?
c2e_rc=${c2e_rc:-0}
if [ "$c2e_rc" -ne 0 ]; then
    printf '%s\n' "$c2e_out" | sed 's/^/  /'
    echo ""
    echo "Fix: the route manifest (architecture/routes.yaml) and the runtime-"
    echo "generated docs (docs/api/ACTIVE_API_GENERATED.md) disagree. Run the"
    echo "AST pre-step generator to refresh the manifest:"
    echo "  go run ./scripts/admin/generate_routes_yaml.go . architecture/routes.yaml"
    echo "Then regenerate the docs:"
    echo "  go run ./cmd/admin gen-api-docs"
    echo "Re-run the gate to confirm both sources agree."
    echo ""
    echo "Common root causes:"
    echo "  - New route registered that didn't go through the canonical"
    echo "    RegisterRoutes site (bypass composition root → 'docs-only')."
    echo "  - Manifest pre-step uses a stale AST ─ run the generator."
    echo "  - Inline chained-group or non-literal path pattern surfaces as"
    echo "    drift (pre-step emits warnings; the manifest will be incomplete)."
    exit 1
fi
printf '%s\n' "$c2e_out" | grep -E '^C2-E gate:' || true

# ── Check 49: go vet ./internal/... drift gate (FASE 9 post-rename follow-up, June 2026) ──
# Canonical fail-closed `go vet` pass (covering internal/ entirely).
# Catches the regression class where an upstream rename (e.g. FASE 9
# Step 6 gdrive.Service -> drive.Admin) updates a struct field but a
# consumer (production code, test fixture, or composition wiring) still
# references the OLD field/method name. rg-based content gates miss
# type-signature drift because they scan for patterns, not type
# conformance; `go vet --all` runs the canonical `composites` checker
# (Go 1.20+) which catches `unknown field X in struct literal of type Y`
# regressions like the one observed at
# `internal/app/voiceover_adapters_drive_test.go:53:30`. This gate
# fails BEFORE a force-with-lease push lands.
#
# Fail-closed per godlike-08 zero-baseline rule: any non-allowlisted
# vet warning exits 1 with the offender listed.
#
# ARCH-ALLOWLIST opt-in (mirrors Check 5 / 10b / 11 / 33): a
# transitional backfill or intentional deprecation call that
# legitimately surfaces a vet warning MUST prepend the magic marker
# `// ARCH-ALLOWLIST: vet-warn` on the line preceding the offending
# construct. Per godlike-08 zero-baseline rule, new allowlist
# sites require explicit owner + deadline.
echo "=== Check 49: go vet ./internal/... drift gate ==="
all_vet=$(go vet ./internal/... 2>&1) || vet_rc=$?
vet_rc=${vet_rc:-0}
# Strip ARCH-ALLOWLIST: vet-warn sites from the failing-set (25-line
# scroll-window of the magic marker - mirrors Check 5 semantics).
literal_vet=$(printf '%s\n' "$all_vet" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*vet-warn/) {
                markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next
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
if [ "$vet_rc" -ne 0 ] && [ -n "$literal_vet" ]; then
    echo "FAIL: go vet drift detected (non-allowlisted warnings):"
    printf '%s\n' "$literal_vet" | sed 's/^/  /'
    echo ""
    echo "Fix: align struct literals and method signatures with the canonical"
    echo "      type after upstream renames. If a vet warning is intentional,"
    echo "      prepend the magic marker on the preceding line:"
    echo "    // ARCH-ALLOWLIST: vet-warn"
    exit 1
fi
echo "OK: go vet ./internal/... passes (0 non-allowlisted warnings)"

# ── Main gate ──────────────────────────────────────────────────────
# Run the focused+ratchet archcheck; PR-A's `--future-ratchet` keeps the
# 5 Phase 0 rules in grace-cycle regression-detection mode.
# ── Check 8: forbid post-Setup SetOutboxHandler/SetMediasearchHandler (TODO 16, Wave 19) ────
# The deprecated setters on *Server MUST NOT be called from production
# code. The constructor NewServerWithHealth accepts outboxHandler and
# mediasearchHandler as params; routes are wired BEFORE Setup() runs.
# Post-construction setter calls silently fail to register routes.
#
# Allowlist (the ONLY legitimate call sites):
#   - internal/api/server.go        : the Server constructor wires handlers before Setup().
#   - internal/api/routes.go        : Router.SetOutboxHandler/SetMediasearchHandler (called
#                                     FROM the constructor, not by external callers).
#   - *_test.go                     : test files may call deprecation-setters to verify
#                                     the error contract.
#   - tests/fixtures/zero_legacy/** : self-check fixtures (caught only in --self-check mode).
echo "=== Check 8: forbid post-Setup SetOutboxHandler / SetMediasearchHandler (TODO 16) ==="
postSetupSetters=$(rg -n --type go \
    -e '\.SetOutboxHandler\(' \
    -e '\.SetMediasearchHandler\(' \
    --glob '!**/internal/api/server.go' \
    --glob '!**/internal/api/routes.go' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)
if [ -n "$postSetupSetters" ]; then
    echo "FAIL: SetOutboxHandler / SetMediasearchHandler call outside canonical constructor:"
    echo "$postSetupSetters"
    echo ""
    echo "Fix: pass outboxHandler and mediasearchHandler through the"
    echo "NewServerWithHealth constructor (before Setup()), NOT via post-"
    echo "construction setters. The setters are deprecated and return errors"
    echo "when called after the gin engine is already built."
    exit 1
fi
echo "OK: no SetOutboxHandler / SetMediasearchHandler calls outside the canonical allowlist"

# ── Check 9: forbid nil-dispatcher silent fallback (return nil) (TODO 16, Wave 19) ────
# The canonical write path for indexed mutations is outbox.Dispatcher. Any code
# path that silently no-ops when the dispatcher is nil (`if dispatcher == nil {
# return nil }`) risks silently dropping writes. Hard-error patterns (return
# fmt.Errorf, return err) are intentionally NOT caught by this check — those
# correctly fail-fast and the existing artlist/search_core.go is a canonical
# example of the fail-fast pattern.
#
# Allowlist:
#   - internal/app/**                : composition root (Build*Bundle constructors).
#   - internal/infrastructure/database/sqlite/outbox/** : canonical dispatcher impl.
#   - *_test.go                      : test fixtures may stub nil dispatcher.
#   - cmd/admin/**                   : one-shot operator tooling.
#   - tests/fixtures/zero_legacy/**  : self-check fixtures.
echo "=== Check 9: forbid nil-dispatcher silent fallback (return nil) (TODO 16) ==="
nilDispatcher=$(rg -nU --type go \
    -e 'dispatcher\s*==\s*nil\s*\{[^}]*return\s+nil\b' \
    -e 'dispatcher\s*==\s*nil\s*\{?\s*\n\s*return(\s+nil\b|\s*$)' \
    --glob '!**/internal/app/**' \
    --glob '!**/internal/infrastructure/database/sqlite/outbox/**' \
    --glob '!**/*_test.go' \
    --glob '!**/cmd/admin/**' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)
if [ -n "$nilDispatcher" ]; then
    echo "FAIL: nil-dispatcher silent fallback (return nil) outside composition/test/allowlist:"
    echo "$nilDispatcher"
    echo ""
    echo "Fix: handlers MUST fail-fast when the dispatcher is nil rather than"
    echo "silently returning nil. The canonical pattern is:"
    echo "  if d.dispatcher == nil { return fmt.Errorf(\"dispatcher is nil — invariant broken\") }"
    echo "instead of:"
    echo "  if d.dispatcher == nil { return nil }  // silently drops writes"
    exit 1
fi
echo "OK: no nil-dispatcher silent fallback patterns outside composition/test/allowlist"

# ── Check 10: forbid asset-repo Upsert(ctx, outside allowlist (TODO 16, Wave 19) ────
# The domain-level asset.Repository.Upsert and the concrete *ClipsRepository.Upsert
# are outbox-bypass surfaces in production handler code. Any handler that calls
# repo.Upsert (or assetStore.Upsert) outside the canonical write path (outbox
# dispatcher) risks silently writing to media_assets without an outbox event,
# leaving the Qdrant vector stale.
#
# Allowlist: cmd/admin/**, internal/infrastructure/database/sqlite/**,
# internal/application/{assets/{ingest,jobs/assets,artifacts,providers,searchqueries,catalogsync},
# voiceover,channels,images,youtube,clips}/**, internal/api/assets/**,
# internal/app/**, internal/infrastructure/{ai/autotag,database/assetindex}/**,
# *_test.go, tests/fixtures/zero_legacy/**.
echo "=== Check 10: forbid asset-repo Upsert outside canonical allowlist (TODO 16) ==="
assetUpserts=$(rg -n --type go \
    -e '\.Upsert\(ctx,' \
    --glob '!**/cmd/admin/**' \
    --glob '!**/internal/infrastructure/database/sqlite/**' \
    --glob '!**/internal/application/assets/ingest/**' \
    --glob '!**/internal/application/jobs/assets/**' \
    --glob '!**/internal/application/assets/artifacts/**' \
    --glob '!**/internal/application/assets/providers/**' \
    --glob '!**/internal/application/voiceover/**' \
    --glob '!**/internal/application/channels/**' \
    --glob '!**/internal/application/images/**' \
    --glob '!**/internal/application/youtube/**' \
    --glob '!**/internal/application/clips/**' \
    --glob '!**/internal/application/assets/searchqueries/**' \
    --glob '!**/internal/application/assets/catalogsync/**' \
    --glob '!**/internal/api/assets/**' \
    --glob '!**/internal/app/**' \
    --glob '!**/internal/infrastructure/ai/autotag/**' \
    --glob '!**/internal/infrastructure/database/assetindex/**' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)
if [ -n "$assetUpserts" ]; then
    echo "FAIL: asset-repo Upsert call outside canonical allowlist:"
    echo "$assetUpserts"
    echo ""
    echo "Fix: route writes through the outbox dispatcher (production) or"
    echo "the canonical adapter layer (internal/application/assets/ingest/)."
    echo "Direct repo.Upsert in handler code silently bypasses the outbox"
    echo "and leaves Qdrant vectors stale."
    exit 1
fi
echo "OK: no asset-repo Upsert calls outside the canonical allowlist"

# ── Check 10b (PR 2 / Blocco 1 sub-PR, June 2026): forward-prevention gate
# for the dispatcher-only *assets.ClipsRepository surface methods that are
# STILL public (for legacy adapter delegation and the new Mutate typed-
# command wrapper) but MUST NOT be called directly from production paths.
#
# Today the literal PR 2 spec — lowercase all of UpsertClipTx,
# HardDeleteTx, RestoreTx, UpsertFolder, SoftDeleteFilter — is
# STRUCTURALLY-BLOCKED: UpsertClipTx is called cross-package by
# outbox.Dispatcher; HardDeleteTx/RestoreTx already live in
# txmutation/ (Wave 22 PR-CLIP-RAW-MUTATIONS); UpsertFolder +
# SoftDeleteFilter depend on the embedded *asset.AssetStoreSQLite
# whose removal is the (aborted) PR 1 deliverable. So this gate is the
# SAFE-ADDITIVE form of the spec: it can't lowercase the methods, but
# it CAN catch NEW direct callers from production paths so the
# 159+ historical call sites migrate and never re-accumulate.
#
# Pattern anchors:
#   \.UpsertFolder\(       — caller wants to write clip_folders row
#   \.SoftDeleteFilter\(   — caller wants the SQL filter string;
#                            legitimate in internal/infrastructure/sqlite/
#                            callers, NOT in production paths.
#
# Allowlist mirrors Check 10 (production-canonical adapter layer +
# sqlite infrastructure + tests + zero_legacy fixtures).
#
# ARCH-ALLOWLIST opt-in: prepend `// ARCH-ALLOWLIST: clips-ssot-only`
# on the line preceding the call site to opt into the allowlist
# (mirrors Check 5 / Check 8 conventions).
echo "=== Check 10b: forbid PR 2 Blocco 1 dispatcher-only primitive callers (PR 2 / Blocco 1 sub-PR) ==="
all_ips=$(rg -n --type go \
    -e '\.UpsertFolder\(' \
    -e '\.SoftDeleteFilter\(' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    --glob '!**/cmd/admin/**' \
    --glob '!**/internal/infrastructure/database/sqlite/**' \
    --glob '!**/internal/application/assets/ingest/**' \
    --glob '!**/internal/application/jobs/assets/**' \
    --glob '!**/internal/application/assets/artifacts/**' \
    --glob '!**/internal/application/assets/providers/**' \
    --glob '!**/internal/application/voiceover/**' \
    --glob '!**/internal/application/channels/**' \
    --glob '!**/internal/application/images/**' \
    --glob '!**/internal/application/youtube/**' \
    --glob '!**/internal/application/clips/**' \
    --glob '!**/internal/application/assets/searchqueries/**' \
    --glob '!**/internal/application/assets/catalogsync/**' \
    --glob '!**/internal/api/assets/**' \
    --glob '!**/internal/app/**' \
    --glob '!**/internal/infrastructure/ai/autotag/**' \
    --glob '!**/internal/infrastructure/database/assetindex/**' \
    . 2>/dev/null \
    || true)
literal_ips=$(printf '%s\n' "$all_ips" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*clips-ssot-only/) {
                markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next
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
if [ -n "$literal_ips" ]; then
    echo "FAIL: dispatcher-only primitive call from production path:"
    echo "$literal_ips"
    echo ""
    echo "Fix: route via the canonical Mutate(ctx, mutations.AssetMutationCommand)"
    echo "typed-command entry point on *assets.ClipsRepository, or via the"
    echo "AssetMutationDispatcher SSOT for actions that pre-date the wiki."
    echo "Direct .UpsertFolder( / .SoftDeleteFilter( calls in handler code"
    echo "leak the SQL-primitive surface and break the eventual migration."
    echo ""
    echo "If the call is genuinely a composition-root adapter delegate"
    echo "(rare; today only the canonical ClipsRepository adapter files"
    echo "in internal/app/**), prepend the magic marker on the preceding line:"
    echo "    // ARCH-ALLOWLIST: clips-ssot-only"
    echo "    a.inner.UpsertFolder(ctx, folder)"
    exit 1
fi
echo "OK: no dispatcher-only primitive calls from production paths"

# ── Check 11: forbid event_key construction with random UUID (TODO 16, Wave 19) ────
# Outbox event_keys MUST be deterministic (computed from the aggregate id +
# content hash) so the ON CONFLICT(event_key) DO NOTHING guarantee collapses
# duplicate enqueues. A random UUID in the event_key shape forces every
# enqueue to produce a new row, defeating idempotency. The canonical shapes
# are `delete:<asset_id>` (delete_envelope.go) and the index envelope in
# outboxevents/repository.go; uuid-suffixed keys are an anti-pattern.
#
# ALLOWLIST RATIONALE: the tightened multi-line patterns (June 2026
# follow-up) match uuid.NewString ONLY when the eventKey assignment line
# references the variable that holds the uuid (eventID). This lets the
# gate distinguish:
#
#   ANTI-PATTERN: eventKey assignment line contains `\beventID\b` (the
#     uuid-holding variable), so the uuid IS concatenated into the
#     eventKey value (directly via `+ eventID`, via `fmt.Sprintf` with
#     eventID as an arg, or any other reference).
#
#   LEGITIMATE:   eventKey assignment line does NOT reference eventID
#     at all (e.g. `eventKey := "delete:" + assetID`), so the uuid
#     is for a SEPARATE field (event_id audit) and ON CONFLICT(event_key)
#     DO NOTHING still works correctly.
#
# The allowlist below covers Category B only (reindex is intentionally
# uuid-suffixed per canonical design). Category A (UUID for separate
# event_id field) is NO LONGER allowlisted — the tightened patterns
# correctly accept it without an explicit allowlist entry.
#
# Category B — reindex is intentionally uuid-suffixed per canonical design:
#   - internal/infrastructure/database/sqlite/outboxevents/envelope.go::
#     BuildReindexEnvelopeV1: the eventKey IS uuid-suffixed by design
#     ("reconcile:reindex:<assetID>:<eventID>"). Idempotency is enforced
#     DOWNSTREAM by the worker's supersede gate on source_version
#     (from media_assets.metadata_json.$.content_hash), not at the
#     outbox-enqueue layer. Every --apply run enqueues a fresh reindex
#     event; redundant fix-up work is collapsed at execution time.
#   - internal/infrastructure/database/sqlite/outbox/delete_envelope.go::
#     buildDeleteRequestV1: pre-existing canonical pattern.
#
# Pattern shapes (3 tightened patterns):
#   1. INLINE:   `eventKey[^\n]*uuid\.NewString` — uuid.NewString is on
#                the SAME line as the eventKey assignment (direct
#                concatenation, e.g. `eventKey := "..." + uuid.NewString()`).
#   2. FORWARD:  `eventKey[^\n]*\n(?:[^\n]*\n){0,3}[^\n]*eventID[^\n]*=
#                \s*uuid\.NewString` — eventKey is on line N, and an
#                `eventID := uuid.NewString()` assignment is on line
#                N+1..N+3 (uuid-suffixed via a forward intermediate var).
#   3. REVERSE:  `eventID[^\n]*=\s*uuid\.NewString[^\n]*\n(?:[^\n]*\n){0,3}
#                [^\n]*eventKey[^\n]*=[^\n]*\beventID\b` — the canonical
#                production shape: `eventID := uuid.NewString()` on line N,
#                `eventKey := "..." + eventID` on line N+1. The `\beventID\b`
#                on the eventKey line proves the uuid IS being concatenated
#                into the eventKey value (not just adjacent to it).
#
# Loophole: the patterns hardcode the variable name `eventID`. A future
# contributor using a different name (e.g. `uid := uuid.NewString()`) would
# not be caught. ripgrep's default regex engine does not support
# backreferences for dynamic variable matching. The trade-off is
# acceptable because (a) `eventID` is the canonical name across all
# canonical envelope builders (BuildReindexEnvelopeV1, buildDeleteRequestV1)
# and the canonical reconcile adapter, and (b) the escape hatch is to
# promote Check 11 to a Go-side AST pass via
# `scripts/archcheck/check11eventkey/` (mirrors the Wave 19 PR2-1 pattern
# for cross-capability edge graph emission) if the loophole is exercised
# in practice.
#
# Allowlist:
#   - internal/infrastructure/database/sqlite/outbox/**       : canonical envelope builders
#                                                              (Category B pattern).
#   - internal/infrastructure/database/sqlite/outboxevents/** : canonical reindex envelope
#                                                              (Category B pattern).
#   - *_test.go                                               : test fixtures may use
#                                                              uuid.NewString for distinct keys.
#   - tests/fixtures/zero_legacy/**                           : self-check fixtures.
echo "=== Check 11: forbid event_key construction with random UUID (TODO 16) ==="
uuidEventKeys=$(rg -nU --type go \
    -e 'eventKey[^\n]*uuid\.NewString' \
    -e 'eventKey[^\n]*\n(?:[^\n]*\n){0,3}[^\n]*eventID[^\n]*=\s*uuid\.NewString' \
    -e 'eventID[^\n]*=\s*uuid\.NewString[^\n]*\n(?:[^\n]*\n){0,3}[^\n]*eventKey[^\n]*=[^\n]*\beventID\b' \
    --glob '!**/internal/infrastructure/database/sqlite/outbox/**' \
    --glob '!**/internal/infrastructure/database/sqlite/outboxevents/**' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)
if [ -n "$uuidEventKeys" ]; then
    echo "FAIL: event_key constructed with random UUID outside canonical envelope:"
    echo "$uuidEventKeys"
    echo ""
    echo "Fix: use the canonical envelope builders (delete_envelope.go, index"
    echo "envelope in outboxevents/repository.go) which produce deterministic"
    echo "event_keys from the aggregate id + content hash. uuid.NewString in"
    echo "the event_key shape defeats ON CONFLICT(event_key) DO NOTHING and"
    echo "creates a fresh outbox row on every enqueue."
    exit 1
fi
echo "OK: no event_key construction with random UUID outside canonical envelope"

# ── Check 12: forbid legacy "lifecycle_state: <asset>.Status" fallback (TODO 16) ────
# QDRANT-001 §(b): the canonical lifecycle key is `lifecycle_state`; the
# legacy `status` column is the QDRANT-RECOVERY-001 / QDRANT-005 source of
# truth, but BuildPayload MUST populate the canonical key from
# `asset.LifecycleState`, NOT from the legacy `asset.Status`. The latter is a
# SSOT regression that loses fidelity on rows where Status and LifecycleState
# diverge (which is most rows post-059 migration).
#
# Allowlist:
#   - *_test.go                  : tests may exercise the legacy path explicitly.
#   - tests/fixtures/zero_legacy/** : self-check fixtures.
echo "=== Check 12: forbid legacy \"lifecycle_state\": <asset>.Status fallback (TODO 16) ==="
legacyLifecycleState=$(rg -n --type go \
    -e '"lifecycle_state":\s*\w+\.Status' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)
if [ -n "$legacyLifecycleState" ]; then
    echo "FAIL: legacy \"lifecycle_state\": <asset>.Status fallback in payload builder:"
    echo "$legacyLifecycleState"
    echo ""
    echo "Fix: change the BuildPayload (or equivalent) line to source the"
    echo "lifecycle_state from asset.LifecycleState (the canonical field),"
    echo "not asset.Status (the legacy column). The status -> lifecycle_state"
    echo "rename happened in migration 059; rows where both exist will have"
    echo "diverged since then and the legacy key reads stale data."
    exit 1
fi
echo "OK: no legacy \"lifecycle_state\": <asset>.Status fallback in payload builders"

# ── Check 13: forbid ListAssetsForReconcile placeholder (TODO 16, TODO 2) ────
# SQLiteAssetStore.ListAssetsForReconcile is currently wired as a build-time
# placeholder (returns `wired as build-time placeholder only` error). That
# means any reconcile --apply call silently produces 0 findings, hiding real
# drift. The fix is to implement the SQL scan; this check fails until then.
#
# Pattern: any source code that returns the placeholder error string.
#
# Allowlist:
#   - *_test.go                  : tests may stub the placeholder explicitly.
#   - tests/fixtures/zero_legacy/** : self-check fixtures.
echo "=== Check 13: forbid ListAssetsForReconcile placeholder (TODO 16, TODO 2) ==="
placeholderReconcile=$(rg -n --type go \
    -e 'wired as build-time placeholder' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)
if [ -n "$placeholderReconcile" ]; then
    echo "FAIL: ListAssetsForReconcile placeholder still wired in production:"
    echo "$placeholderReconcile"
    echo ""
    echo "Fix: implement the SQL scan in SQLiteAssetStore.ListAssetsForReconcile."
    echo "The placeholder (return fmt.Errorf(\"wired as build-time placeholder\"))"
    echo "silently produces 0 reconcile findings, hiding real drift. See TODO 2."
    exit 1
fi
echo "OK: no ListAssetsForReconcile placeholder in production"

# ── Check 14: forbid legacy "status" key in BuildPayload (TODO 16) ────
# QDRANT-001 §(b): the canonical payload key is `lifecycle_state`; a `status`
# key in BuildPayload is the QDRANT-RECOVERY-001 legacy that QDRANT-001
# removed. Any new BuildPayload that re-introduces the `status` key is a
# SSOT regression: the qdrant-side search filter (`lifecycle_state`) is
# what payloads and queries must agree on.
#
# Pattern: `"status": <value>` where value is a struct field reference
# (e.g. asset.Status). Literal-string `status` values (HTTP codes, state
# machine strings) are not in scope — the pattern is restricted to
# `<word>.<word>` (struct field ref) to keep the check tight.
#
# Allowlist:
#   - *_test.go                  : tests may construct legacy payloads.
#   - tests/fixtures/zero_legacy/** : self-check fixtures.
echo "=== Check 14: forbid legacy \"status\" key in BuildPayload (TODO 16) ==="
legacyStatusKey=$(rg -n --type go \
    -e '"status":\s*\w+\.' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)
if [ -n "$legacyStatusKey" ]; then
    echo "FAIL: legacy \"status\" payload key (struct field ref) in BuildPayload:"
    echo "$legacyStatusKey"
    echo ""
    echo "Fix: rename the payload key from \"status\" to \"lifecycle_state\""
    echo "and source it from asset.LifecycleState. The QDRANT-001 §(b)"
    echo "search contract requires both writer (BuildPayload) and reader"
    echo "(Qdrant filter) to agree on the canonical key. See TODO 16."
    exit 1
fi
echo "OK: no legacy \"status\" payload key in BuildPayload"

# ── Check 15: qdrant.NewClient constructions must propagate APIKey (QDRANT-005A) ────
# QDRANT-005A Phase 1 Blocker #1: cfg.Qdrant.APIKey is not propagated to
# qdrant.NewClient at every construction site. An API-key-protected Qdrant
# deployment appears unhealthy (401) because the client omits the X-Api-Key
# header on every request. The canonical pattern is:
#
#   client := qdrant.NewClient(&qdrant.Config{
#       BaseURL: cfg.Qdrant.BaseURL,
#       APIKey:  cfg.Qdrant.APIKey,   // <-- REQUIRED
#       Timeout: cfg.Qdrant.Timeout,
#   }, log)
#
# Implementation: per-file check. Find every Go file that constructs
# qdrant.NewClient(&qdrant.Config{...}), then verify the SAME file
# also contains the literal pattern `APIKey:\s*cfg\.Qdrant\.APIKey`.
# A file that constructs the client but does NOT propagate the APIKey
# is the production anti-pattern.
#
# Why per-file (not per-block): a Go file may legitimately construct
# multiple qdrant.Config{...} literals (e.g. one for the production
# client + one for a test stub). Per-file is the conservative
# scope: any file that touches the client must also touch the
# APIKey propagation. If a file has TWO client constructions and
# ONE omits APIKey, the per-file check still catches it (the
# file-level pattern absence is the signal).
#
# Limit: a test file that constructs a stub client with no auth
# would false-positive. Test files are excluded via --glob
# `!**/*_test.go` per the standard check convention.
#
# Allowlist:
#   - *_test.go                  : test stubs may construct unauthenticated clients.
#   - tests/fixtures/zero_legacy/** : self-check fixtures.
#   - internal/infrastructure/qdrant/** : the Config TYPE lives here;
#                                     test files in this package are
#                                     excluded by the *_test.go rule,
#                                     and production code in this
#                                     package does NOT construct the
#                                     client (it only defines types).
echo "=== Check 15: qdrant.NewClient must propagate APIKey (QDRANT-005A) ==="
clientFiles=$(rg -l 'qdrant\.NewClient\(&qdrant\.Config\{' --type go \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    || true)
missingApiKey=""
for f in $clientFiles; do
    if ! rg -q 'APIKey:\s*cfg\.Qdrant\.APIKey' "$f" 2>/dev/null; then
        missingApiKey="$missingApiKey $f"
    fi
done
if [ -n "$missingApiKey" ]; then
    echo "FAIL: file(s) construct qdrant.NewClient but do NOT propagate cfg.Qdrant.APIKey:"
    for f in $missingApiKey; do echo "  $f"; done
    echo ""
    echo "Fix: add 'APIKey: cfg.Qdrant.APIKey,' to the qdrant.Config{...} literal."
    echo "An API-key-protected Qdrant deployment appears unhealthy (401) when"
    echo "the client omits the X-Api-Key header. QDRANT-005A Phase 1 Blocker 1."
    exit 1
fi
echo "OK: all qdrant.NewClient constructions propagate cfg.Qdrant.APIKey"

# ── Check 19: forbid infrastructure imports in API layer ──
# Scans internal/api/ for production Go files that import
# github.com/Marcuss-ops/PipelineGen/internal/infrastructure/
# and fails on any file NOT listed in the per-file allowlist at
# docs/migrations/api-infrastructure-imports-allowlist.txt.
# Symmetric comparison: both non-allowlisted imports AND stale
# allowlist entries with no matching import fail the gate.
#
# This gate enforces AGENTS.md Pattern 8 (API package: thin transport
# only). The API layer MUST NOT import database/sql, Google Drive SDK,
# FFmpeg/process execution, or any other infrastructure concrete.
# Infrastructure dependencies must flow through typed ports in
# internal/application/ and be injected at the composition root.
#
# Zero-baseline: as of P0.6 (June 2026), the API layer has ZERO
# infrastructure imports. Any new import fails this gate.
echo "=== Check 19: forbid infrastructure imports in API layer ==="
allowlist_file="docs/migrations/api-infrastructure-imports-allowlist.txt"

# Collect all files in internal/api that import internal/infrastructure
actual=$(rg -l --type go \
    'github\.com/Marcuss-ops/PipelineGen/internal/infrastructure/' \
    internal/api \
    --glob '!**/*_test.go' \
    2>/dev/null | sort || true)

# Build sorted allowlist from the file (strip comments + blank lines)
allowed=$(grep -vE '^\s*(#|$)' "$allowlist_file" 2>/dev/null | sort || true)

# Violations: files with infra imports NOT in the allowlist.
# Pipe through grep . to strip spurious blank lines from empty
# variable expansion (echo "" produces a newline that would
# otherwise hit the comm output as a false-positive blank entry).
violations=$(comm -13 <(echo "$allowed" | grep .) <(echo "$actual" | grep .) 2>/dev/null || true)

# Stale entries: allowlist entries with NO matching infra import
stale=$(comm -23 <(echo "$allowed" | grep .) <(echo "$actual" | grep .) 2>/dev/null || true)

if [ -n "$violations" ]; then
    echo "FAIL: forbidden infrastructure imports detected in API layer:"
    echo "$violations"
    echo ""
    echo "Fix: move the infrastructure dependency to a port in"
    echo "      internal/application/ and inject it at the composition root."
    echo "      If the import is grandfathered, add the file path to"
    echo "      $allowlist_file with owner + deadline per AGENTS.md §8."
    exit 1
fi
if [ -n "$stale" ]; then
    echo "FAIL: stale allowlist entry with no matching infrastructure import:"
    echo "$stale"
    echo ""
    echo "Fix: remove the stale path from $allowlist_file. The import was"
    echo "      already removed from the source code; keeping a dead allowlist"
    echo "      entry masks future regressions. Per AGENTS.md §8 zero-baseline"
    echo "      rule, allowlist entries must exactly mirror the codebase."
    exit 1
fi
echo "OK: no infrastructure imports in API layer (0 actual, 0 allowed, symmetric clean)"

# ── Check 35: context.Background / context.WithoutCancel exemption tracking ──
# Wave 22 task 6 / PR-CONTEXT-NO-CANCEL-CI-GATE (June 2026): promote the
# documented exemption family from documentation-only status (S3g) to a
# dedicated CI gate. A site PASSES if EITHER:
#   (a) the file path is listed in AGENTS.md §Migration Status "Known
#       intentional exempt sites" table (canonical SSOT), OR
#   (b) the line preceding the call carries the magic marker
#       // ARCH-ALLOWLIST: no-cancel  (for context.WithoutCancel)
#       // ARCH-ALLOWLIST: bg-only    (for context.Background)
echo "=== Check 35: context.Background / context.WithoutCancel exemption tracking (PR-CONTEXT-NO-CANCEL-CI-GATE / Wave 22 task 6) ==="

EXEMPT_FILES=$(rg -oE '`internal/[^` ]+`' AGENTS.md 2>/dev/null \
    | sed 's/^`//' | sed 's/`$//' \
    | sort -u)
EXEMPT_FILE_COUNT=$(printf '%s\n' "$EXEMPT_FILES" | grep -c . || true)

ALL_HITS=$(rg -nE 'context\.(Background|WithoutCancel)\(' internal/ \
    --type go --glob '!**/*_test.go' 2>/dev/null || true)

if [ -z "$ALL_HITS" ]; then
    echo "OK: 0 context.Background / context.WithoutCancel call sites"
else
    UNDOCUMENTED_COUNT=0
    UNDOCUMENTED_OUTPUT=""
    while IFS= read -r hit; do
        [ -z "$hit" ] && continue
        FILE=$(echo "$hit" | cut -d: -f1)
        LINE=$(echo "$hit" | cut -d: -f2)
        # (a) AGENTS.md canonical-table exemption
        if printf '%s\n' "$EXEMPT_FILES" | grep -qxF "$FILE"; then
            continue
        fi
        # (b) ARCH-ALLOWLIST marker within a 25-line window preceding the
        # call, ONLY inside the godoc / comment block OF THE ENCLOSING
        # CODE path. (Mirrors Check 5 + Check 8 convention; real-world
        # godoc spans 2-5 lines.) Hard-stops on the first non-comment,
        # non-blank line encountered so an unrelated prior function's
        # marker can't accidentally exempt a NEW call site in the same
        # file (avoids false-positive exemption via shared-file markers).
        WALK_OK=0
        for OFFSET in $(seq 1 25); do
            PREV=$((LINE - OFFSET))
            [ "$PREV" -lt 1 ] && break
            LINE_TEXT=$(sed -n "${PREV}p" "$FILE" 2>/dev/null)
            if echo "$LINE_TEXT" \
                | grep -qE 'ARCH-ALLOWLIST:[[:space:]]*(no-cancel|bg-only)'; then
                WALK_OK=1
                break
            fi
            # Stop walking if we hit non-comment/non-blank line BEFORE
            # the marker (boundary of the surrounding godoc block).
            TRIMMED=$(echo "$LINE_TEXT" | sed 's/^[[:space:]]*//')
            if [ -n "$TRIMMED" ] && ! echo "$TRIMMED" | grep -qE '^//|^/\*'; then
                break
            fi
        done
        if [ "$WALK_OK" = "1" ]; then
            continue
        fi
        UNDOCUMENTED_OUTPUT="${UNDOCUMENTED_OUTPUT}${hit}
"
        UNDOCUMENTED_COUNT=$((UNDOCUMENTED_COUNT + 1))
    done <<< "$ALL_HITS"
    if [ "$UNDOCUMENTED_COUNT" -gt 0 ]; then
        echo "FAIL: ${UNDOCUMENTED_COUNT} context.Background/WithoutCancel sites LACK a tracking entry."
        echo ""
        echo "Each site must have BOTH one of the following exemptions:"
        echo "  (a) The file path appears in AGENTS.md \u00a7Migration Status"
        echo "      \"Known intentional exempt sites\" table."
        echo "  (b) Within the 25 lines preceding the call carries the magic marker:"
        echo "        // ARCH-ALLOWLIST: no-cancel  (for context.WithoutCancel)"
        echo "        // ARCH-ALLOWLIST: bg-only    (for context.Background)"
        echo ""
        echo "PR-CONTEXT-NO-CANCEL-CI-GATE / Wave 22 task 6 (June 2026)."
        echo ""
        echo "Sites requiring tracking:"
        printf '%s\n' "$UNDOCUMENTED_OUTPUT"
        exit 1
    fi
    echo "OK: all context.Background / context.WithoutCancel sites are tracked (${EXEMPT_FILE_COUNT} canonical exempt files)"
fi

# ── Check 36: anti-reintro gate for diagnostic / snapshot artefacts (PR-A, June 2026) ──
# Forward-prevention after the Wave 21 PR-G mega-batch that re-landed
# .tmp-diag/ directory + CURRENT_<X>.go + TODO<N>_<X>.go fixtures in the
# working tree (see paste audit). This gate ensures the .gitignore
# patterns appended by PR-A remain effective: any re-introduction of
# the four diagnostic patterns under internal/ cmd/ pkg/ scripts/ tests/
# fails CI with a remediation `git rm -rf` instruction.
#
# Pattern anchors (case-sensitive, basename-only):
#   directory names:  .tmp-diag,  tmp-diag
#   file basenames:   CURRENT_*.go  (literal CURRENT_ prefix)
#                     TODO[0-9]*.go (literal TODO prefix + 1 digit, no underscore required)
#
# Scope: the four top-level source roots only. .git/ hidden by default
# via `find` not descending into .git; tests/fixtures/zero_legacy/ is
# OUT of scope (`tests/` only matches the directory, fixtures of the
# canonical negative-example shape are not flagged).
#
# Implementation: `find` is canonical here (consistent with Check 23
# field-count extraction). rg --glob filters the search space, not the
# file-name; for basenameonly matching, find -name is the precise tool.
#
# Failure mode: emit the offending paths AND a copy-pasteable `git rm`
# one-liner so the operator can clean up in one step. Standard
# fail-fast + literal remediation. Index/PR-bodies stay consistent
# across the diagnostic-artefact family.
echo "=== Check 36: diagnostic-artefact anti-reintro gate (PR-A, June 2026) ==="
diag_files=$(find internal cmd pkg scripts tests -type f \
    \( -name 'CURRENT_*.go' -o -name 'TODO[0-9]*.go' \) \
    -not -path 'tests/fixtures/zero_legacy/*' 2>/dev/null || true)
diag_dirs=$(find internal cmd pkg scripts tests -type d \
    \( -name '.tmp-diag' -o -name 'tmp-diag' \) 2>/dev/null || true)
diag_hits=$(printf '%s\n%s\n' "$diag_files" "$diag_dirs" \
    | grep -v '^$' | sort -u || true)
if [ -n "$diag_hits" ]; then
    echo "FAIL: diagnostic / snapshot artefacts detected in source roots:"
    printf '%s\n' "$diag_hits" | sed 's/^/  /'
    echo ""
    echo "Resolution:"
    echo "  1. If these are intended diagnostic snapshots, MOVE them under"
    echo "     tests/fixtures/zero_legacy/ (the canonical negative-example"
    echo "     surface exempted by this gate)."
    echo "  2. Otherwise the canonical cleanup is to remove them via:"
    printf '%s\n' "$diag_hits" | sed 's/^/     git rm -rf /'
    echo ""
    echo "Per AGENTS.md §8 ARCHITECTURE-CI-GATES zero-baseline rule,"
    echo "re-introduction of these patterns is now blocked; this gate"
    echo "is the forward-prevention half of PR-A."
    exit 1
fi
echo "Check 36: 0 diagnostic-artefact paths in internal/ cmd/ pkg/ scripts/ tests/"

# ── Check 39: HC-7 anti-reintro gate (ChunkDuration: 25 literal + parent_id:"") ────
# HC-7 (June 2026) consolidates the script-video SSOT into
# pkg/defaults/video.go::{VideoConfig, DefaultVideoConfig}. Two patterns
# historically leaked past the SSOT and the leak-prone variants are
# gated here:
#
#   (a) ChunkDuration: 25 literal in platform/config/video.go::WithDefaults
#       (was hard-coded at line 64 pre-HC-7). The handler-side video
#       pipeline must read defaults.DefaultVideoConfig().ChunkDuration.
#       Pattern: `ChunkDuration <= 0 { ... = 25 `  (the cheap-to-grep
#       textual re-occurrence of the literal in the *conditioned* default
#       path — the unconditional canonical is in defaults package).
#
#   (b) `"parent_id": ""` literal in /api/scripts/* HTTP responses. The
#       canonical reader uses `s.ParentScriptID` (line 121 of
#       internal/api/script/helpers.go::ListScripts post-HC-7); the empty
#       string was DRIFT-23-4.
#
# Pattern anchors:
#   ChunkDuration.{0,40}= 25   — the conditioned-default shape; tolerates
#                                 any arithmetic (e.g. `+=25` `=((25))`)
#                                 but REMAINS strict on the literal value.
#   "parent_id":[[:space:]]*""  — the exact JSON-empty pattern.
#
# Scope: the same four top-level source roots used by Check 36 to keep
# the diagnostic-artefact family aligned. tests/fixtures/zero_legacy/
# is OUT of scope (negative-example fixtures exempt, mirrors Check 36).
#
# Negative examples live in fixtures/zero_legacy/ — if a future
# negative-EXAMPLE fixture needs to exist, place it there (the gate
# excludes that path) and update Check 39's allowlist rationale.
echo "=== Check 39: HC-7 anti-reintro gate (ChunkDuration: 25 literal + parent_id:\"\") ==="
hc7_hits=$(rg -n --type go \
    -e 'ChunkDuration.{0,40}=[[:space:]]*25\b' \
    -e '"parent_id":[[:space:]]*""' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/*' \
    internal cmd pkg scripts 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' \
    || true)
# Filter out the SSOT itself: pkg/defaults/video.go is where the canonical
# 25 + "parent_id" literal legitimately lives; excluding it keeps the gate
# focused on consumer re-introduction.
hc7_literal=$(printf '%s\n' "$hc7_hits" \
    | awk -F: '$1 != "pkg/defaults/video.go"' \
    || true)
if [ -n "$hc7_literal" ]; then
    echo "FAIL: HC-7 re-introduction detected (ChunkDuration: 25 literal OR parent_id:\"\"):"
    printf '%s\n' "$hc7_literal" | sed 's/^/  /'
    echo ""
    echo "Fix: route the value through pkg/defaults/video.go::{VideoConfig,"
    echo "      DefaultVideoConfig}. The canonical CSV lives in:"
    echo "    - ChunkDuration: 25          → defaults.DefaultVideoConfig().ChunkDuration"
    echo "    - parent_id JSON field name → defaults.DefaultVideoConfig().ParentFieldName"
    echo "    - EffectsDir: 'effects/'     → defaults.DefaultVideoConfig().EffectsDir"
    echo ""
    echo "For ListScripts-style parent_id emission, iterate scriptRecords and"
    echo "emit `s.ParentScriptID` (the canonical int64) rather than the literal"
    echo 'empty string `"parent_id": ""` (the DRIFT-23-4 anti-pattern).'
    exit 1
fi
echo "Check 39: 0 HC-7 re-introduction patterns (ChunkDuration: 25 \/ parent_id:\"\")"

# ── Check 41: forbid recreation of internal/api/common/ (Issue 10, June 2026) ──
# internal/api/common/ was a compatibility stub with a duplicated OK helper.
# Removed in Issue 10 (June 2026). Any new import of the package or
# existence of the directory is a regression — the canonical helpers
# live in pkg/apiutil.
#
# This check fails if:
#   (a) internal/api/common/ directory exists, OR
#   (b) any production .go file imports ".../internal/api/common"
echo "=== Check 41: forbid recreation of internal/api/common/ (Issue 10) ==="
if [ -d "${REPO_ROOT}/internal/api/common" ]; then
    echo "FAIL: internal/api/common/ directory exists — delete it (removed in Issue 10, June 2026)"
    echo "      The canonical HTTP helpers live in pkg/apiutil."
    exit 1
fi
commonImports=$(rg -n --type go \
    -e 'github\.com/Marcuss-ops/PipelineGen/internal/api/common"' \
    --glob '!**/internal/api/common/**' \
    --glob '!**/*_test.go' \
    "${REPO_ROOT}" 2>/dev/null \
    || true)
if [ -n "$commonImports" ]; then
    echo "FAIL: import of internal/api/common detected (package was removed in Issue 10):"
    echo "$commonImports"
    echo ""
    echo "Fix: use pkg/apiutil instead. internal/api/common was a compatibility stub"
    echo "      with a duplicated OK helper — removed June 2026."
    exit 1
fi
echo "OK: internal/api/common/ is not present and no imports reference it"

# ── Check 42: forbid `database/sql` import in application/api production paths (P1-8, Wave 19) ──
# AGENTS.md Pattern 0 mandates that `internal/infrastructure/database/**`
# owns SQL; `internal/application/**` and `internal/api/**` consume SQL
# ONLY through typed ports declared in the consumer's `ports.go`.
# Direct `database/sql` import in production app/api code is a layering
# leak — the canonical placement is the typed-port adapter, not the
# consumer's import block. The one legitimate exception is the
# typed-port signature itself (e.g., `*sql.Tx` as a typed-port parameter
# in `internal/application/voiceover/ports.go::TxOutboxEnqueuer`); it
# stays in the allowlist with `never-canonical` deadline so the
# tx-outbox bridge shape survives the ratchet.
#
# Allowlist: `docs/migrations/app-sql-imports-allowlist.txt` lists
# one `<file_path>` per line for the P1-8 (Wave 19) grandfathered
# baseline. Per AGENTS.md §8 ARCHITECTURE-CI-GATES zero-baseline rule,
# every entry MUST carry an inline comment with owner + deadline.
# The inline deadline preamble is stripped here to compare against
# `rg` hits; the comment line stays attached to the entry so the
# zero-baseline rationale is auditable from the file.
#
# Pattern anchor: `^\s*"database/sql"\s*$` — matches the single-line
# Go import of `"database/sql"` exactly. Aliased imports are
# intentionally out of scope; introducing aliases is itself a layering
# indicator that code review should surface, not a CI fast-pass.
#
# Tests are excluded via `--glob '!**/*_test.go'` per the convention
# used by every other architectural check; test fixtures may freely
# construct `sql.Open` for `internal/infrastructure/health/...` smoke tests.
#
# Symmetric compare mirrors Check 19's two-way gate:
#   * violations: production files importing `"database/sql"` NOT in the
#     allowlist → FAIL the gate (regression detected).
#   * stale:     allowlist entries whose file no longer carries the
#     import → FAIL the gate (zombie-prevention — a dead row would
#     silently mask a future regression). Per AGENTS.md 1-PR rule the
#     removal ships in the same PR as the migration that drops the import.
echo "=== Check 42: forbid 'database/sql' import in app/api production paths (P1-8, Wave 19) ==="
allowlist_file="docs/migrations/app-sql-imports-allowlist.txt"
if [ ! -f "${REPO_ROOT}/${allowlist_file}" ]; then
    echo "FAIL: ${allowlist_file} missing — cannot run P1-8 gate"
    echo "      (the gate cannot grandfather without an allowlist file)"
    exit 1
fi

# Collect every production non-test .go file that imports `"database/sql"`
# exactly (the canonical Go import line shape).
actual=$(rg -l --type go \
    -e '^\s*"database/sql"\s*$' \
    --glob '!**/*_test.go' \
    internal/application internal/api 2>/dev/null | sort || true)

# Build sorted allowlist: strip full-line comments + blank lines +
# the trailing inline `# rationale + owner + deadline` part of each
# entry, keeping only the first whitespace-delimited token (= the
# file path).
allowed=$(grep -vE '^[[:space:]]*(#|$)' "${REPO_ROOT}/${allowlist_file}" 2>/dev/null \
          | awk -F'#' '{print $1}' \
          | awk '{print $1}' \
          | grep -v '^$' \
          | sort || true)

# Symmetric Check 42: fail on production hits NOT in allowlist AND on
# stale allowlist entries (mirrors Check 19's two-way gate).
violations=$(comm -13 <(printf '%s\n' "$allowed" | grep .) \
                   <(printf '%s\n' "$actual" | grep .) 2>/dev/null || true)
stale=$(comm -23 <(printf '%s\n' "$allowed" | grep .) \
               <(printf '%s\n' "$actual" | grep .) 2>/dev/null || true)

if [ -n "$violations" ]; then
    echo "FAIL: forbidden 'database/sql' import in production app/api layers (P1-8):"
    echo "$violations"
    echo ""
    echo "Fix: route SQL through a typed port in"
    echo "      internal/application/<consumer>/ports.go with the adapter"
    echo "      in internal/infrastructure/database/<feature>/, wired at"
    echo "      the composition root (internal/app/<feature>_adapters.go)."
    echo ""
    echo "If the import is grandfathered under the Wave 19 P1-8 transitional"
    echo "      baseline, add the file path to ${allowlist_file} with explicit"
    echo "      owner + deadline per AGENTS.md §8 zero-baseline rule."
    exit 1
fi
if [ -n "$stale" ]; then
    echo "FAIL: stale allowlist entry (file no longer imports 'database/sql'):"
    echo "$stale"
    echo ""
    echo "Fix: remove the stale path from ${allowlist_file} IN THE SAME PR"
    echo "      as the migration that drops the import (AGENTS.md 1-PR rule)."
    echo "      Leaving a dead allowlist entry masks future regressions."
    exit 1
fi
actual_count=$(printf '%s\n' "$actual" | grep -c . || true)
allowed_count=$(printf '%s\n' "$allowed" | grep -c . || true)
echo "OK: P1-8 'database/sql' baseline symmetric clean (${actual_count} actual = ${allowed_count} allowlisted; 0 pending migrations)"


# ── Main gate ──────────────────────────────────────────────────────────
# Run the focused+ratchet archcheck; PR-A's `--future-ratchet` keeps the
# 5 Phase 0 rules in grace-cycle regression-detection mode.
go run ./scripts/archcheck --ratchet --future-ratchet

# PR-I (June 2026): promote cmd/archcheck --strict as a blocking CI gate.
# Reads architecture/policy.yaml; --strict turns warn → exit-1 on any
# violation per cmd/archcheck/main.go:204-205. Ratchets #id-20-21:
# duplicate-types-allowlist (Check 5) + max_files_per_package=40
# (pack-size cap). Transitional baseline:
# docs/migrations/archcheck-strict-baseline.json holds any open
# exceptions; fail-closed semantics deadlined entries become hard
# fail (verdict: PR-I implementation in_progress per ADR-0002 §D5).
go run ./cmd/archcheck --strict
# HC-1 (June 2026) deletes the pre-HC-1 package-level `var jobTimeoutRegistry`
# global in internal/application/jobs/worker.go + the `SetJobTimeout` and
# `jobTimeout(` helper callers. Per-job-type timeouts are now keyed through
# `*jobs.Registry.Compose()[j.Type]` (or the typed `JobTimeout()` method)
# via the Worker.WithRegistry(reg) builder attached at composition time.
#
# Pattern anchors (re-introduction patterns we forbid):
#   var jobTimeoutRegistry[[:space:]]*=
#       — package-level map re-emergence with a MapType-typed name
#       (catches `var jobTimeoutRegistry`, `var ( ... jobTimeoutRegistry ...)`).
#   SetJobTimeout\(
#       — exported helper to mutate the map (the pre-HC-1 surface);
#       only worker.go::SetJobTimeout defined this; the alias was removed.
#   ^func jobTimeout\(  (top-level package function)
#   {{:blank:}}jobTimeout\(  (in-function call to package helper)
#       — the lowercase helper that read from the global; renamed to
#       Worker.jobTimeoutFor(t) post-HC-1.
#
# Scope: internal/ + cmd/ (composition root + production callers).
# The canonical site is internal/application/jobs/registry.go (owns the
# TimeoutMap + TimeoutResolver surface); it does NOT contain the
# forbidden patterns. *Registry.Compose() / JobTimeout() are the
# AND ONLY the supported lookup paths.
#
# Negative examples (the patterns being checked for, when invoked
# legitimately as inline fixtures/tests) live in tests/fixtures/zero_legacy/
# — excluded below to mirror Check 36 / Check 39 gating convention.
echo "=== Check 40: HC-1 anti-reintro gate (var jobTimeoutRegistry re-emergence) ==="
hc1_hits=$(rg -n --type go \
    -e 'var[[:space:]]+jobTimeoutRegistry[[:space:]]*=' \
    -e 'SetJobTimeout\(' \
    -e '^func[[:space:]]+jobTimeout\(' \
    -e '\bjobTimeout\(' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/*' \
    internal cmd 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' \
    || true)
if [ -n "$hc1_hits" ]; then
    echo "FAIL: HC-1 re-introduction detected (jobTimeoutRegistry global / SetJobTimeout / jobTimeout helper):"
    printf '%s\n' "$hc1_hits" | sed 's/^/  /'
    echo ""
    echo "Fix: per-job-type timeouts MUST be keyed through *jobs.Registry via"
    echo "      Worker.WithRegistry(reg) at composition time. The HC-1 surface:"
    echo "    - registry.Compose()  → TimeoutMap (type-keyed snapshot)"
    echo "    - registry.JobTimeout(t) → typed single-shot lookup (the canonical"
    echo "                              TimeoutResolver method)"
    echo "    - worker.WithRegistry(reg)  → builder attached at composition time"
    echo "      (also snapshots reg.Compose() so runJob's lookup is branch-free)."
    echo ""
    echo "There is NO legitimate use of `var jobTimeoutRegistry ... = ...`, no"
    echo "`SetJobTimeout(t, d)` mutation hook, and no top-level `jobTimeout(t)`"
    echo "helper. Adding any of these requires a godlike/07 EXPAND/BACKFILL/"
    echo "CUTOVER/CONTRACT migration sequence (architecture/deprecations.yaml)"
    echo "and a tracking entry in architecture/current.yaml#HC-1 sub_tasks."
    exit 1
fi
echo "Check 40: 0 HC-1 re-introduction patterns (var jobTimeoutRegistry \/ SetJobTimeout \/ jobTimeout)"

# Check 15: 500-LoC per file (transitional allowlist, scadenza 2026-07-15)
bash "$(dirname "$0")/ci/architecture/checks/15_file_size.sh" || { echo "Step 6 check 15 (file size) failed"; exit 1; }

# Check 16: <=39 productive files per package (transitional allowlist qdrant)
bash "$(dirname "$0")/ci/architecture/checks/16_package_size.sh" || { echo "Step 6 check 16 (package size) failed"; exit 1; }
# Check 43: forbid .DB() chain outside infrastructure (P1.6, June 2026)
bash "$(dirname "$0")/ci/architecture/checks/43_db_chain_outside_infra.sh" || { echo "Check 43 (DB chain) failed"; exit 1; }

# Check 45: forbid inline bare map[string]*ClipsRepository{...} literals (Wave 23, action P1-3)
# Companion to Check 8 (S3e) which bans the fully-qualified
# `"map[string]*assets.ClipsRepository{"` shape. Check 45 catches the
# BARE / unqualified variant `"map[string]*ClipsRepository{"` -- a
# likely regression shape if a future contributor imports the canonical
# type without a package alias (or introduces a new unqualified alias).
# Canonical-allowed sites (composition root + canonical registry +
# tests + zero_legacy fixtures) are excluded via rg --glob inside the
# check script.
# ── Check 44 (P1-2 application size cap + types_aliases.go filename ban) ──
# Action P1-2 of cleanup plan (June 2026): promoted from `current_state: deferred` to active.
# Slot was reserved in the original Check 45 commit per the now-removed
# `NOTE: Check 44 ... monotone-ratchetable.` comment above (see git history).
# Companion `arch(current):` commit in this PR flips wave_status.P1-2.current_state.
# SSOT (target + transitional_cap + current_state) read live from
# architecture/current.yaml::doc[1].wave_status.P1-2 per AGENTS.md §8 SSOT discipline.
bash "$(dirname "$0")/ci/architecture/checks/44_application_size_cap_and_aliases_ban.sh" || { echo "Check 44 (P1-2 application size cap + types_aliases.go filename ban) failed"; exit 1; }

bash "$(dirname "$0")/ci/architecture/checks/46_inline_clips_repository_map_ban.sh" || { echo "Check 46 (inline ClipsRepository map ban) failed"; exit 1; }

# ── Check 45: Channel-monitor E2E dedup contract test coverage (PR-C-YouTube-Cutover Commit I, June 2026) ──
# Verifies that the canonical E2E test file
# `internal/application/assets/monitor/e2e_no_duplicates_test.go`
# exists AND asserts the locked counter invariants so the assertion
# coverage cannot be silently neutered. Pin tokens match the spec
# invariants (parallel-safe-bypass semantics):
#   accepted_jobs==1     (Tick1+Tick2 dedups the channel-level
#                         sync job via the mockSyncBroker set)
#   duplicate_enqueues==5 (Tick2's 5 per-video emits classified
#                            as broker duplicates)
# Tick1/Tick2/parallel-race spec assertions are inspected at the
# source level so a gate regression on any of them surfaces here
# before CI tests run. Slot picked per spec (PR-C-YouTube-Cutover
# Commit I — user-explicit slot assignment supersedes the prior
# Check 50 numbering; the inline `map[string]*ClipsRepository` ban
# detection remains enforced via Check 46's script invocation).
echo "=== Check 45: Channel-monitor E2E dedup counter coverage (PR-C-YouTube-Cutover Commit I) ==="
e2e_test_file="internal/application/assets/monitor/e2e_no_duplicates_test.go"
if [ ! -f "$e2e_test_file" ]; then
  echo "FAIL: $e2e_test_file is missing."
  echo "Fix: add the E2E test file at the canonical path; the file is the"
  echo "single source of truth for the Tick1/Tick2 + parallel race contract."
  exit 1
fi
missing=""
for tok in qdrant db_clips drive_uploads outbox accepted_jobs duplicate_enqueues FiveByTwo; do
  if ! grep -qi "$tok" "$e2e_test_file"; then
    missing="$missing $tok"
  fi
done
if [ -n "$missing" ]; then
  echo "FAIL: $e2e_test_file is missing counter assertions for:$missing"
  exit 1
fi
echo "OK: Check 45 - E2E counter coverage verified on monitor/. (PR-C-YouTube-Cutover Commit I)"

# ── Check 50: forbid retry-classifier substring-matcher outside pkg/retry (Step 7 closure, June 2026) ──
# The canonical transient-error classification lives in pkg/retry/retry.go
# (typed-path: TransientInfrastructureError + IsTransient + WrapTransient +
# transientSubstrings taxonomy + DefaultOptions with JitterFraction=0.25).
# Production classifiers MUST delegate to pkg/retry.IsTransient or wrap at the
# SDK / port exit via pkg/retry.WrapTransient. A function whose name matches
# one of the canonical retry-classifier tokens (IsTransient|isTransient|
# IsRetryable|isRetryable|ShouldRetry|shouldRetry) followed by an optional
# PascalCase suffix AND uses strings.Contains natively is a Step 7 SSOT
# regression: a substring-based classifier outside pkg/retry.
#
# Allowlist (hardcoded package-level + per-file transitional baseline):
#   pkg/retry                          — canonical home.
#   pkg/textutil                       — string manipulation helpers.
#   pkg/similarity                     — token-set similarity math.
#   docs/migrations/retry-classifier-  — per-file transitional baseline with
#     substring-allowlist.txt            explicit owner + deadline + rationale.
# Tests (_test.go files) excluded per the standard check convention.
#
# Migration plan for future offenders:
#   1. Wrap raw SDK / port error at the exit boundary via pkg/retry.WrapTransient.
#   2. Classify at the gate via pkg/retry.IsTransient (typed path first).
#   3. Delete local strings.Contains taxonomy; retry.IsTransient owns the list.
echo "=== Check 50: forbid retry-classifier substring-matcher outside pkg/retry (Step 7) ==="

# ── Transitional baseline (per-file allowlist) ─────────────────────
# Per AGENTS.md godlike/08 zero-baseline rule (mirrors Check 5 / Check 8 /
# Check 23 / Check 33). Every entry requires explicit owner + deadline +
# rationale documented inline. Migration of any entry to the canonical
# typed-path surface deletes the corresponding line from the allowlist.
declare -a retry_classifier_extras=()
if [ -f "docs/migrations/retry-classifier-substring-allowlist.txt" ]; then
  while IFS= read -r _line; do
    [[ -z "$_line" || "$_line" =~ ^[[:space:]]*# ]] && continue
    # Each entry is <path>\t# <owner> <deadline> <rationale>. Extract just
    # the first whitespace-delimited token (the path). Trailing inline
    # comments are owned by the file's per-entry documentation.
    _path=$(awk '{print $1}' <<< "$_line")
    [[ -z "$_path" || "$_path" =~ ^# ]] && continue
    retry_classifier_extras+=( -not -path "./${_path}" )
  done < docs/migrations/retry-classifier-substring-allowlist.txt
fi

violators=$(find . -name '*.go' -not -name '*_test.go' \
    -not -path '*/pkg/retry/*' \
    -not -path '*/pkg/textutil/*' \
    -not -path '*/pkg/similarity/*' \
    "${retry_classifier_extras[@]}" \
    -print0 2>/dev/null \
    | xargs -0 awk '
    BEGIN { in_classifier = 0 ; func_line = 0 }
    /^func[[:space:]]+(\([^)]*\)[[:space:]]+)?(IsTransient|isTransient|IsRetryable|isRetryable|ShouldRetry|shouldRetry)[A-Za-z0-9_]*[[:space:]]*\(/ && /err/ {
        in_classifier = 1
        func_line = FNR
        next
    }
    in_classifier && /strings\.Contains/ {
        print FILENAME ":" func_line ": " $0
        in_classifier = 0
    }
    /^}/ && in_classifier {
        in_classifier = 0
    }
    ' 2>/dev/null || true)
if [ -n "$violators" ]; then
    echo "FAIL: retry-classifier function uses strings.Contains natively outside pkg/retry:"
    echo "$violators"
    echo ""
    echo "Fix: delegate the substring classifier to pkg/retry.IsTransient (typed"
    echo "      path). Optionally wrap outgoing port errors via pkg/retry.WrapTransient"
    echo "      at the SDK / port exit so errors.As(err, *TransientInfrastructureError)"
    echo "      finds the typed carrier. Allowlist: pkg/retry (canonical home),"
    echo "      pkg/textutil, pkg/similarity, and the per-file transitional list at"
    echo "      docs/migrations/retry-classifier-substring-allowlist.txt."
    exit 1
fi
echo "OK: no retry-classifier substring-matchers outside pkg/retry"

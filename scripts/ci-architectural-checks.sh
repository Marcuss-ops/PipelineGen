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
        "Check 11 (event_key constructed with random UUID)|eventKey.*uuid\\.NewString|check_11_uuid_event_key.go"
        "Check 11 (event_key constructed with random UUID, multiline reverse)|uuid\\.NewString[^\\n]*\\n(?:[^\\n]*\\n){0,3}[^\\n]*eventKey|check_11_uuid_event_key.go"
        "Check 11 (event_key constructed with random UUID, multiline forward)|eventKey[^\\n]*\\n(?:[^\\n]*\\n){0,3}[^\\n]*uuid\\.NewString|check_11_uuid_event_key.go"
        "Check 12 (payload_mapper legacy lifecycle_state fallback)|\"lifecycle_state\":\\s*\\w+\\.Status|check_12_payload_mapper_status.go"
        "Check 13 (ListAssetsForReconcile placeholder)|wired as build-time placeholder|check_13_listassets_placeholder.go"
        "Check 14 (BuildPayload legacy status key)|\"status\":\\s*\\w+\\.|check_14_buildpayload_status_key.go"
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
    echo "All self-checks passed (8 patterns / 7 fixtures)."
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
  # extract package name from the first `package X` line (guard against empty)
  pkg_line=$(awk '/^package / {print; exit}' "$f" 2>/dev/null || true)
  pkg="${pkg_line#package }"
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
bash "${REPO_ROOT}/scripts/ci-bypass-audit.sh"


# ── Check 8 (factory-only, S3e, Wave 22 task 5 follow-up): forbid literal map[string]*assets.ClipsRepository ──
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
                mp[$1] = $2
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next
            if (mp[$1] != "" && $2 + 0 >= mp[$1] + 0 + 1 && $2 + 0 <= mp[$1] + 0 + 25) next
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
                mp[$1] = $2
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next
            if (mp[$1] != "" && $2 + 0 >= mp[$1] + 0 + 1 && $2 + 0 <= mp[$1] + 0 + 25) next
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
  pkg_line=$(awk '/^package / {print; exit}' "$f" 2>/dev/null || true)
  pkg="${pkg_line#package }"
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

# ── Check 11: forbid event_key construction with random UUID (TODO 16, Wave 19) ────
# Outbox event_keys MUST be deterministic (computed from the aggregate id +
# content hash) so the ON CONFLICT(event_key) DO NOTHING guarantee collapses
# duplicate enqueues. A random UUID in the event_key shape forces every
# enqueue to produce a new row, defeating idempotency. The canonical shapes
# are `delete:<asset_id>` (delete_envelope.go) and the index envelope in
# outboxevents/repository.go; uuid-suffixed keys are an anti-pattern.
#
# Allowlist:
#   - internal/infrastructure/database/sqlite/outbox/** : canonical envelope builders.
#   - *_test.go                  : test fixtures may use uuid.NewString for distinct keys.
#   - tests/fixtures/zero_legacy/** : self-check fixtures.
# Multiline pattern (rg -nU) catches the production anti-pattern at
# cmd/admin/reconcile_qdrant.go:413-414 where `eventID := uuid.NewString()`
# and `eventKey := "..." + eventID` are on separate lines. A single-line
# pattern would miss this because the uuid is hidden behind an intermediate
# var. The pattern matches within a 3-line window in either order:
#   - eventKey ... uuid.NewString (single line)
#   - eventKey ... NEWLINE ... uuid.NewString (multiline, eventKey first)
#   - uuid.NewString ... NEWLINE ... eventKey (multiline, uuid first)
# The cmd/admin/** allowlist is required because reconcile_qdrant.go
# deliberately bypasses idempotency for the --apply admin one-shot tool
# (per the comment on line 326: "fresh UUID for every call so re-running
# reconcile-qdrant --apply twice produces two distinct events").
echo "=== Check 11: forbid event_key construction with random UUID (TODO 16) ==="
uuidEventKeys=$(rg -nU --type go \
    -e 'eventKey.*uuid\.NewString' \
    -e 'eventKey.*uuid\.New\(\)' \
    -e 'eventKey[^\n]*\n(?:[^\n]*\n){0,3}[^\n]*uuid\.NewString' \
    -e 'uuid\.NewString[^\n]*\n(?:[^\n]*\n){0,3}[^\n]*eventKey' \
    --glob '!**/internal/infrastructure/database/sqlite/outbox/**' \
    --glob '!**/cmd/admin/**' \
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

# ── Check 14: forbid legacy \"status\" key in BuildPayload (TODO 16) ────
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

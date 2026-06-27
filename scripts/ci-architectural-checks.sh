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

# ── Check 4: Migration version uniqueness lint (PR-D) ──────────────
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

# ── Main gate ────────────────────────────────────────────────────
# Run the focused+ratchet archcheck; PR-A's `--future-ratchet` keeps the
# 5 Phase 0 rules in grace-cycle regression-detection mode.
go run ./scripts/archcheck --ratchet --future-ratchet

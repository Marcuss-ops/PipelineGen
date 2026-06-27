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
echo "=== Check 5: forbid mutation primitives in production callers (QDRANT-asset-mutation) ==="
literal_calls=$(rg -n --type go \
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
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
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

# ── Main gate ────────────────────────────────────────────────────
# Run the focused+ratchet archcheck; PR-A's `--future-ratchet` keeps the
# 5 Phase 0 rules in grace-cycle regression-detection mode.
go run ./scripts/archcheck --ratchet --future-ratchet

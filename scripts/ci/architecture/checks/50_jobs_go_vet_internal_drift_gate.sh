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
    -e 'dispatcher\s*==\s*nil\s*\{[^}]*return\s+nil\s*(//|$)' \
    --glob '!**/*_test.go' \
    --glob '!**/cmd/admin/**' \
    . 2>/dev/null \
    | grep -Ev '^\./(internal/app/|internal/infrastructure/database/sqlite/outbox/|internal/application/scripts/|internal/application/clips/|internal/capabilities/assets/providers/|tests/fixtures/zero_legacy/)' \
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
    --glob '!**/internal/capabilities/assets/providers/**' \
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
    | grep -Ev '^\./(internal/application/mediamemory/|internal/application/indexing/|internal/application/assets/reconciliation/voiceover/|internal/application/assets/texttracks/|internal/application/assets/sourcing/youtube/|internal/infrastructure/drive/)' \
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
    --glob '!**/internal/capabilities/assets/providers/**' \
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
    | grep -Ev '^\./internal/infrastructure/files/foldermemory/' \
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
    | grep -Ev '^\./(internal/api/|internal/capabilities/assets/providers/artlist/|internal/application/scripts/|internal/api/transport/|internal/infrastructure/database/sqlite/assets/)' \
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

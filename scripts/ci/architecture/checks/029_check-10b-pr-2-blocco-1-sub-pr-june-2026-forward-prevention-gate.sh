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


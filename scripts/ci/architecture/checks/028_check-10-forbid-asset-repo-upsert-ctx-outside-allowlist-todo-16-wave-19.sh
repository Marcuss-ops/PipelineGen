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


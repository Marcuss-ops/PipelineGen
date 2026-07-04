# architecture/qdrant/

Canonical wire-format definitions for Qdrant collections used by PipelineGen.

## Files

| File | Purpose | Source |
|------|---------|--------|
| `v3-schema.json` | Canonical v3 collection wire format (text:768, transcript:768, visual:768, audio:512 + sparse bm25_text with `model=qdrant/bm25`). Used by `curl -X PUT /collections/<name>`. | `internal/infrastructure/qdrant/schema/schema.go::DefaultV3Schema()` |
| `001-sidecar-and-pointid.md` | Historical narrative (June 2026) — sidecar architecture + point ID convention | — |
| `002-sidecar-envelope-ripple.md` | Historical narrative (June 2026) — sidecar envelope ripple | — |

## Usage

```bash
# Bring up the ephemeral test stack (port :16333, no persistent volume)
docker compose -f docker-compose.test-qdrant.yml up -d

# Wait for the liveness probe
curl -sf http://localhost:16333/healthz && echo READY

# PUT the v3 physical collection
curl -X PUT http://localhost:16333/collections/media_assets_v3_e5_768_siglip_768 \
  -H 'Content-Type: application/json' \
  -d @architecture/qdrant/v3-schema.json

# Create the runtime alias (production reads/writes via the alias)
curl -X POST http://localhost:16333/collections/aliases \
  -H 'Content-Type: application/json' \
  -d '{"actions":[{"create_alias":{"alias_name":"media_assets_current","collection_name":"media_assets_v3_e5_768_siglip_768"}}]}'

# Verify the alias resolves + 5 named vectors present
curl -s http://localhost:16333/collections/media_assets_current | jq '.result.config.params.vectors | keys'
# expected: ["audio","text","transcript","visual"]
```

## Design invariants (godlike/06 one-canonical-owner-per-fact)

- **Schema source**: `internal/infrastructure/qdrant/schema/schema.go::DefaultV3Schema()` is the **only** programmatic owner of the v3 collection structure. `v3-schema.json` is a *projection* of that source for Qdrant's PUT /collections wire format. Any future schema drift MUST be made in the Go source first, then re-emitted as JSON.
- **Physical + alias split**: the physical collection name `media_assets_v3_e5_768_siglip_768` MUST differ from the runtime alias `media_assets_current` (the canonical Go `IndexSchema.Validate()` enforces this). The JSON file documents both names in `_meta`.
- **Owner team**: Configservice / qdrant-configservice (per `architecture/current.yaml#QDRANT-PREFLIGHT-EXECUTION-2026-07-04.owner_capability`).

## Qdrant 1.18 caveats (godlike/07 no-fake-availability)

- `sparse_vectors.bm25_text.model` is **accepted** on PUT but **not echoed** on GET. The field is preserved on disk and used at server-side BM25 inference time. The `compatible: false` flag in `schema-diff.json` is therefore a false-negative for this single field — dense vector dimensions + sparse `modifier` are the load-bearing assertions.
- `PUT /collections/<name>` returns 200 even when the collection already exists (idempotent re-create). To check whether a collection was newly created, read the response body's `result` field.

## End-to-end verification (executed 2026-07-04 on :16333 Qdrant 1.18 test stack)

| Probe | Endpoint | Result |
|-------|----------|--------|
| Liveness | `GET /healthz` | HTTP 200, "healthz check passed" |
| Collection list | `GET /collections` | 1 collection present (media_assets_v3_e5_768_siglip_768) |
| Alias list | `GET /aliases` | media_assets_current → media_assets_v3_e5_768_siglip_768 |
| Alias resolves | `GET /collections/media_assets_current` | 4 dense + 1 sparse vector present |
| Dense dims | text/transcript/visual/audio | 768/768/768/512 Cosine — ALL MATCH expected |
| Sparse modifier | bm25_text | `idf` — matches expected |
| Sparse upsert (id:2) | `PUT /points` with `bm25_text.indices` + `values` | HTTP 200, point stored |
| Sparse search | `POST /points/search` with `using: bm25_text` | HTTP 200, server-side BM25 inference works |

## Forward pointer

- `v3-payload-indexes.json` — DEFERRED to a followup PR. The 16 payload indexes from `DefaultV3Schema.PayloadIndexes` (workspace_id, lifecycle_state, source, media_type, language, category, style, channel_id, license, index_version, embedding_version_text, embedding_version_visual, duration_ms, created_at, updated_at, deleted_at) cannot be applied via `PUT /collections/<name>` (the endpoint doesn't accept them inline). They must be created via separate `PUT /collections/<name>/index` calls per field, or via a `cmd/admin/qdrant-preflight/` Go binary.
- Wave-tracker anchor: `architecture/current.yaml#QDRANT-PREFLIGHT-EXECUTION-2026-07-04.linked_issues[PR-QDRANT-PREFLIGHT-SCHEMA-V3-SHIPPED]`.
- Parent verification chain: `architecture/action-plans/2026-07-04-qdrant-verification-chain.md` (11 SQL/curl sanity probes; this file ships Tests 1+2 of Phase 1A).

# Ownership inventory shards

`architecture/ownership.generated.yaml` is a compatibility artifact and must not
be edited or split manually. Its canonical sources are the ownership shards.

## Capability ownership matrix

| Macro-owner | Owns | Must not own |
|---|---|---|
| `capabilities/assets` | Asset identity, persistence coordination, ingest, catalog, stock, search, lifecycle and enrichment | Script semantics, generic audio execution, job orchestration |
| `capabilities/scripts` | Script IR, planning, semantic interpretation, retrieval intent and Script → `AudioIntent` compilation | Generic audio execution, provider-specific platform contracts |
| `capabilities/audio` | Generic audio assets, loops, mixing, ducking, timelines and audio execution | Script-specific interpretation |
| `capabilities/images` | Image generation, search, validation, ingest and delivery | Generic asset persistence and job lifecycle policy |
| `capabilities/jobs` | Queue, scheduling, worker, finalization and completion policy | Domain business decisions owned by assets/scripts/images |

The root of each capability is a facade/contracts/registration boundary. New
implementation belongs in an existing owner's subpackage; do not create a new
capability root without updating the ownership registry.

## Platform and contract invariants

- `platform/qdrant/indexing` contains the single `PayloadMapper` /
  `IndexDocument` airlock. All Qdrant writes use it.
- Qdrant contains retrieval projection data only; SQLite remains canonical
  business state and search results hydrate from SQLite before sampling.
- `internal/kernel/asset/detail.SourceCatalog` is the canonical source catalog;
  `capabilities/scripts/adapters.SourceRegistry` is the single resolver
  dispatch registry. No parallel source normalizers or dispatch switches.
- The canonical asset writer remains the only production mutation gateway for
  `media_assets`; direct SQL is forbidden outside the documented allowlist.
- The canonical sampler registry remains the only clip-selection surface.
- `platform/shared`, `**/common`, `**/utils` and `**/helpers` are forbidden
  dumping grounds.

## Regenerate and validate

Regenerate the aggregate with:

```sh
go run ./cmd/architecture-aggregate
```

Validate that the committed aggregate matches the shards with:

```sh
go run ./cmd/architecture-aggregate --dry-run
```

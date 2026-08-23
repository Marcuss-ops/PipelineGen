# CLI Utilities (`cmd/`)

The `cmd/` directory contains the canonical runtime entrypoints plus
architecture-governance tooling.

## Runtime entrypoints

| Binary | Source | Description |
|---|---|---|
| `server` | `cmd/server/` | Main HTTP server (API + composition root) |
| `worker` | `cmd/worker/` | Background worker runtime (job execution) |

Start with `make run` (or `go run ./cmd/server`) and `go run ./cmd/worker`.

## Admin commands

`cmd/admin/` contains one-shot operational and maintenance commands. It is not
a runtime process.

```bash
go run ./cmd/admin <command> [flags]
```

The command registry lives in `cmd/admin/subcommands.go`. Run `go run ./cmd/admin`
with no arguments to list all registered commands (~95 total). Key categories:

- **Asset backfills**: `backfill-asset-embeddings`, `backfill-clip-folder-path`,
  `backfill-media-durations`, `backfill-payload-hash`, `backfill-provider-timestamps`,
  `backfill-source-url-metadata`, `backfill-missing`, `backfill-visual-embeddings`
- **Drive operations**: `drive-reconcile`, `drive-doctor`, `drive-bootstrap`,
  `sync-all-drive`, `sync-drive-folder`, `drive-create-folder`,
  `remove-drive-folder-recursive`, `trash-drive-files`, `upload-drive-file`
- **Qdrant**: `qdrant-preflight`, `qdrant-readiness`, `qdrant-maintenance`,
  `qdrant-bucket-report`, `qdrant-enrichment-recover`, `reconcile-qdrant`,
  `reindex-qdrant`, `dr-qdrant`
- **Cleanup**: `cleanup-orphans`, `cleanup-all-orphans`,
  `cleanup-artlist-empty-folders`, `cleanup-stock-orphans`,
  `zombie-sweep`, `delete-specific-folders`, `delete-drive-images`,
  `delete-clip-by-drive-file`
- **Sound effects**: `classify-sound-effects`, `download-sound-effects`,
  `organize-sound-effects-drive`, `organize-foley-drive`, `trim-sound-effects`,
  `rename-indexed-sound-effects`, `rename-sound-effects`,
  `update-sound-effect-metadata`
- **Repair**: `repair-drive-links`, `repair-stock-metadata`,
  `broken-references`, `clip-drive-audit`, `clip-drive-orphan-cleanup`
- **Performance**: `performance-backfill`, `performance-report`,
  `benchmark`, `multilingual-benchmark`
- **Identity / stock / DB**: `identity-audit`, `stock-reset`,
  `stock-subfolders-reset`, `sqlite-audit`, `db`, `verify-projection`,
  `storage-snapshot`
- **Misc**: `gen-api-docs`, `render-short`, `multilingual-render`,
  `summarize-book`, `reset-video-ai`, `reachability-graph`,
  `control-plane`, `migrate-legacy-cache`, `unify-catalogs`

## Architecture governance

| Binary | Source | Description |
|---|---|---|
| `archcheck` | `cmd/archcheck/` | Target-tree policy scanner (`go run ./cmd/archcheck`) |
| `architecture-aggregate` | `cmd/architecture-aggregate/` | Generates `architecture/ownership.generated.yaml` |
| `capability-inventory-aggregate` | `cmd/capability-inventory-aggregate/` | Generates `architecture/capability_inventory/baseline.yaml` |

## Diagnostic tools (`tools/`)

Specialized fixture, benchmark, and live-diagnostic tools live in `tools/`,
not under `cmd/`:

| Source | Description |
|---|---|
| `tools/golden06/` | RenderingGen fixture generator |
| `tools/overlaytimings/` | Overlay rendering microbenchmark |
| `tools/researchlive/` | Live research pipeline diagnostic |

These are not deployable services. Rebuild on demand:

```bash
go build -o golden06 ./tools/golden06/
go build -o overlaytimings ./tools/overlaytimings/
go build -o researchlive ./tools/researchlive/
```

## Notes

- `cmd/server` and `cmd/worker` are the canonical runtime processes.
- `cmd/admin` is one-shot; it is not a long-running service.
- Root-level binaries (e.g. `/golden06`, `/pipelinegen`, `/archcheck`) are
  build artifacts and are gitignored — rebuild from source as needed.
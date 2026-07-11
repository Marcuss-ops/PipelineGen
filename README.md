# PipelineGen

PipelineGen is a headless Go backend for media discovery, extraction, processing, indexing, and delivery.

## Core capabilities

- YouTube clip extraction and metadata processing.
- Stock and Artlist asset ingestion.
- Script, image, and voiceover workflows.
- SQLite-backed jobs and transactional outbox processing.
- Qdrant semantic and hybrid search.
- Google Drive publishing and delivery.

## Requirements

- Go 1.25+
- Python 3.10+
- FFmpeg and ffprobe
- yt-dlp
- SQLite
- Optional: Qdrant, Ollama, Google Drive credentials

## Run

```bash
cp config.example.yaml config.yaml
go build -o pipelinegen ./cmd/server
go build -o pipelinegen-worker ./cmd/worker
go build -o admin ./cmd/admin
./pipelinegen --mode all
```

The default HTTP port is `8000` and can be changed with `VELOX_PORT`.

## Repository layout

```text
cmd/                     process entry points
internal/api/            HTTP transport
internal/app/            composition root and wiring
internal/application/    use cases, ports, jobs, policies
internal/domain/         shared domain contracts
internal/infrastructure/ SQLite, Drive, Qdrant, AI and media adapters
pkg/                     leaf utilities
migrations/sqlite/       database migrations
scripts/                 operational and CI utilities
tests/                   automated and operational tests
```

## Architecture rules

- SQLite is the source of truth.
- Qdrant is a rebuildable search projection.
- Long-running work uses the job system.
- Post-commit side effects use the transactional outbox.
- Application code depends on ports; concrete adapters live in infrastructure.
- Dependency construction belongs in `internal/app`.
- New capabilities must use shared registries, resolvers, or samplers instead of duplicating routing logic.

## Verification

```bash
make verify-main
```

## Canonical documentation

- [`ARCHITECTURE.md`](ARCHITECTURE.md) — current system design.
- [`AGENTS.md`](AGENTS.md) — engineering and Git rules.
- [`CANONICAL.md`](CANONICAL.md) — authoritative source map.
- [`docs/api/ACTIVE_API_GENERATED.md`](docs/api/ACTIVE_API_GENERATED.md) — generated HTTP routes.
- [`architecture/policy.yaml`](architecture/policy.yaml) — machine-enforced architecture policy.
- [`architecture/ownership.generated.yaml`](architecture/ownership.generated.yaml) — capability ownership.
- [`architecture/current.yaml`](architecture/current.yaml) — active exception ledger.

Historical plans, closure diaries, evidence dumps, snapshots, and superseded architecture notes are intentionally not stored in the working tree. Git history is the archive.

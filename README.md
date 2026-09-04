# PipelineGen

PipelineGen is a headless Go backend for media discovery, extraction, processing, indexing, and delivery.

## Core capabilities

- YouTube clip extraction and metadata processing.
- Stock and Artlist asset ingestion.
  - Stock clips are normalized to a canonical 1920×1080 / 24 fps / H.264 / AAC / `yuv420p` profile.
  - The `VERIFIED` state requires every clip to pass ffprobe checks on resolution, fps, codec, pixel format, audio codec, sample rate, and channels.
- Script, image, and voiceover workflows.
- PostgreSQL + pgvector media catalog, embeddings, semantic/hybrid search, and media outbox.
- SQLite-backed non-media jobs and transactional outbox processing.
- Google Drive publishing and delivery.

## Requirements

- Go 1.25+
- Python 3.10+
- FFmpeg and ffprobe
- yt-dlp
- PostgreSQL with pgvector for the media domain
- SQLite for non-media domains that have not migrated
- Optional: Qdrant for explicitly owned non-media use cases, Ollama, Google Drive credentials

## Run

For local development / bootstrap:

```bash
cp config.example.yaml config.yaml
```

For production deployments, use the canonical production template and provide
the media PostgreSQL DSN through the environment:

```bash
cp config.production.example.yaml config.yaml
export PIPELINEGEN_MEDIA_POSTGRES_DSN='postgres://USER:PASSWORD@HOST:PORT/pipelinegen_media?sslmode=disable'
```

`media_postgresql.enabled: true` is fail-closed: an empty or unreachable DSN
aborts startup. There is no SQLite or Qdrant fallback for media reads or
writes. Qdrant is disabled by default in the production template and may be
enabled only for an explicitly owned non-media capability.

The production template is GPU-oriented: `video.codec: h264_nvenc`,
`video.preset: p1`, and `video.crf` are the shared video encoder policy.
The host must expose NVENC (`ffmpeg -encoders` should list `h264_nvenc`).
Configured GPU video encodes fail closed: an unavailable encoder or runtime
encode error is returned to the caller and is never silently retried with
`libx264`/CPU. CPU use remains intentional for `ffprobe`, audio-only codecs,
metadata/hash/database/I/O work, and stream-copy operations (`-c copy`).
Do not remove the explicit codec or rely on an empty/default codec in a
GPU-oriented production deployment.

Then build + run:

```bash
go build -o pipelinegen ./cmd/server
go build -o pipelinegen-worker ./cmd/worker
go build -o admin ./cmd/admin
./pipelinegen --mode all
```

For the host deployment, use the native systemd units in
[`scripts/systemd/`](scripts/systemd/). PipelineGen server and worker are not
part of `docker-compose.yml`; that file starts only optional external
infrastructure such as Qdrant, the Artlist scraper, and SearXNG. The canonical
media PostgreSQL service is defined separately for the media-domain runtime.

```bash
go build -o bin/pipelinegen ./cmd/server
go build -o bin/pipelinegen-worker ./cmd/worker
sudo install -D -m 0644 scripts/systemd/pipelinegen.service scripts/systemd/pipelinegen-worker.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now pipelinegen.service pipelinegen-worker.service
```

The default HTTP port is `8000` and can be changed with `VELOX_PORT`.

## Ricreazione clip via Chronon

Per ricreare una clip già registrata nell’asset registry usando esclusivamente
PipelineGen → RenderingGen → Chronon3d:

```bash
RENDERINGGEN_QUEUE_URL=http://127.0.0.1:8081 \
  scripts/recreate_clip_chronon.sh SOURCE_ASSET_ID
```

Lo script invoca `POST /api/clips/render`, attende il job Master e verifica che
il risultato dichiari `backend=chronon_vulkan`. Un fallback Rust, FFmpeg o CUDA
non viene accettato per questo percorso. Il feature flag deve essere attivo e
il worker PipelineGen deve essere configurato con `RENDERINGGEN_QUEUE_URL`.

La migrazione una tantum dei riferimenti già presenti si esegue, senza
modificare SQLite, con:

```bash
./bin/admin backfill-media-postgres \
  --sqlite-dsn 'data/media/media.db.sqlite?_journal_mode=WAL' \
  --postgres-dsn 'postgres://USER:PASSWORD@HOST:PORT/pipelinegen_media?sslmode=disable'
go run ./scripts/operations/migrate-media-text-tracks-once.go \
  --postgres-dsn 'postgres://USER:PASSWORD@HOST:PORT/pipelinegen_media?sslmode=disable'
```

Il secondo comando trasferisce transcript e cue; entrambi sono idempotenti e
il primo include la verifica di parità. Questi comandi sono strumenti di
migrazione una tantum: il runtime media canonico resta PostgreSQL + pgvector.

## Local configuration and secrets

Keep repository configuration and host credentials separate:

1. Copy the non-secret configuration template and edit only local settings:

   ```bash
   cp config.example.yaml config.yaml
   ```

   `config.yaml` is local state; do not commit credentials, tokens, OAuth files,
   cookies, or private keys.

2. Provision the canonical admin secret through the host's secure operator
   process. The required file is `/etc/pipelinegen/pipelinegen.env`, owned by
   `root:pipelinegen-agents` with mode `0640`. It contains
   `VELOX_ADMIN_TOKEN` as a 64-character hexadecimal value. Do not create a
   second repository-local token file, paste the value into chat, shell
   history, command arguments, `config.yaml`, or logs.

3. Validate access without printing the token:

   ```bash
   scripts/with-velox-auth bash -c 'test -n "$VELOX_ADMIN_TOKEN"'
   ```

   The wrapper loads and validates the canonical secret, exports it only to
   the command it executes, and fails closed on missing or malformed input.

4. For a local process, keep the same boundary when credentials are needed:

   ```bash
   scripts/with-velox-auth ./bin/pipelinegen --mode all
   ```

   For a systemd deployment, the service environment is managed by the host;
   routine operators should use `pipelinegenctl` rather than sourcing secrets
   into an interactive shell.

## Daily operations versus administration

Daily, unprivileged operations are intentionally separate from host
administration:

| Activity | Command | Privilege behavior |
|---|---|---|
| Check service state | `scripts/systemd/pipelinegenctl status` | No sudo |
| Check `/ready` | `scripts/systemd/pipelinegenctl verify` | No sudo |
| Read sanitized logs | `scripts/systemd/pipelinegenctl logs` | No sudo |
| Restart and verify `/ready` + Drive canary | `scripts/systemd/pipelinegenctl restart-verify` | No interactive password; restricted `sudo -n` restart only |

`restart-verify` prints only `PASS` or `FAIL`. It loads credentials through
`scripts/with-velox-auth` and never prints tokens, Drive IDs, Drive URLs, or the
canary response.

Administrative host changes require the operator's normal sudo authorization
and should not be performed as a daily shortcut. Before installing the
restricted policy, verify the host's executable path:

```bash
command -v systemctl
```

The path in the sudoers policy must match that result. If it is not
`/usr/bin/systemctl`, pass the verified absolute path through
`PIPELINEGEN_SYSTEMCTL_PATH` when installing the policy. Because `sudo` may
filter environment variables, use the explicit form when needed:

```bash
sudo env PIPELINEGEN_SYSTEMCTL_PATH=/absolute/path/systemctl \
  scripts/systemd/sudoers/install_operator_access.sh --install
```

Administrative tasks include:

- install or change `/etc/sudoers.d/pipelinegen-operator`;
- run `migrate_to_systemd.sh`, `systemctl daemon-reload`, or enable/disable
  services;
- change the ownership or mode of `/etc/pipelinegen/pipelinegen.env`;
- rotate the admin/worker credentials with `scripts/rotate_token.sh` during an
  administrative change window only;
- modify unit files, drop-ins, system packages, or service ownership.

See [`scripts/systemd/README.md`](scripts/systemd/README.md) for the complete
operator matrix and the safe transition from one-time administration to daily
passwordless checks.

## Repository layout

```text
cmd/                     process entry points
internal/app/            composition root and wiring
internal/kernel/         shared semantic contracts
internal/capabilities/   business capabilities and typed ports
internal/platform/       adapters, transport, and external systems
internal/{api,application,domain,infrastructure}/ migration-only zones
pkg/                     leaf utilities
migrations/postgres/     canonical media-domain migrations
migrations/sqlite/       non-media / legacy migration surfaces
scripts/                 operational and CI utilities
tests/                   automated and operational tests
```

## Architecture rules

- PostgreSQL + pgvector is the single source of truth for the media domain.
- Media retrieval and media hydration must come from the same PostgreSQL adapter; no split database read path is permitted.
- Qdrant media reads and writes are forbidden; Qdrant may exist only for explicitly owned non-media use cases.
- SQLite remains authoritative only for non-media domains that have not migrated.
- Long-running work uses the job system.
- Post-commit side effects use the transactional outbox.
- Capability code depends on ports; concrete adapters live in `internal/platform`.
- Dependency construction belongs in `internal/app`.
- New architecture belongs only under `internal/{app,kernel,capabilities,platform}`; the former `api`, `application`, `domain`, and `infrastructure` roots are migration-only.
- New capabilities must use shared registries, resolvers, or samplers instead of duplicating routing logic.

## Verification

```bash
make verify-main
make certify-media-cutover
```

`make verify-main` is the canonical fail-closed pre-push gate.
`make certify-media-cutover` additionally pins the PostgreSQL media SSOT,
pgvector search, canonical Postgres indexing worker, and demolition rules.
See [`docs/operations/verify-main-workflow.md`](docs/operations/verify-main-workflow.md)
for the full workflow.

## Operational testing

For the Stock 9-phase battery, the Artlist clean test battery (`tests/operational/artlist/run_all.sh`), and the **RETRY_WAIT / `CANCELLED` per-job diagnostic recipe** (API + SQLite fallback), see:

- [`docs/stock_pipeline.md` — Stock pipeline guide](docs/stock_pipeline.md) (source types, JSON payload examples, Google Drive auth, and output destination).
- [`docs/operations/stock-e2e-runbook.md#§10` — Stock pipeline live battery](docs/operations/stock-e2e-runbook.md) (12-step single-script layer; `workflow_dispatch`-only).
- [`docs/operations/stock-e2e-runbook.md#§11` — Diagnostica RETRY_WAIT](docs/operations/stock-e2e-runbook.md).
- [`docs/operations/stock-e2e-runbook.md#§11.0` — Operator env contract](docs/operations/stock-e2e-runbook.md) (`VELOX_ADMIN_TOKEN`, `VELOX_PORT`, `VELOX_DRIVE_ARTLIST_ROOT`, `SCROLL_TIMEOUT=120`, `SKIP_HERMETICS=1` minimum set for the Artlist clean test).

Per godlike/06 SSOT: the runbook is the canonical owner of env-var contracts; README merely links to it.

## Canonical documentation

- [`ARCHITECTURE.md`](ARCHITECTURE.md) — current system design.
- [`AGENTS.md`](AGENTS.md) — engineering and Git rules.
- [`CANONICAL.md`](CANONICAL.md) — authoritative source map.
- [`docs/api/ACTIVE_API_GENERATED.md`](docs/api/ACTIVE_API_GENERATED.md) — generated HTTP routes.
- [`architecture/policy.yaml`](architecture/policy.yaml) — machine-enforced architecture policy.
- [`architecture/ownership.generated.yaml`](architecture/ownership.generated.yaml) — capability ownership.
- [`architecture/current.yaml`](architecture/current.yaml) — active exception ledger.

Historical plans, closure diaries, evidence dumps, snapshots, and superseded architecture notes are intentionally not stored in the working tree. Git history is the archive.

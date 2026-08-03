# PipelineGen

PipelineGen is a headless Go backend for media discovery, extraction, processing, indexing, and delivery.

## Core capabilities

- YouTube clip extraction and metadata processing.
- Stock and Artlist asset ingestion.
  - Stock clips are normalized to a canonical 1920×1080 / 24 fps / H.264 / AAC / `yuv420p` profile.
  - The `VERIFIED` state requires every clip to pass ffprobe checks on resolution, fps, codec, pixel format, audio codec, sample rate, and channels.
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

For local development / bootstrap (minimal — Qdrant OFF, clip_indexer OFF, artlist auto_download OFF):

```bash
cp config.example.yaml config.yaml
```

For production deployments (full surface — Qdrant ON, clip_indexer ON, artlist auto_download ON):

```bash
cp config.production.example.yaml config.yaml
```

Then build + run:

```bash
go build -o pipelinegen ./cmd/server
go build -o pipelinegen-worker ./cmd/worker
go build -o admin ./cmd/admin
./pipelinegen --mode all
```

The default HTTP port is `8000` and can be changed with `VELOX_PORT`.

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

## Remotion boundary

Remotion is intentionally kept in the sibling repository `../RemotionUpload`.
PipelineGen owns scripts, asset selection, sound effects, timing, persistence,
and delivery. It may emit a `remotion.render-job.v1` payload through the
[`pkg/remotionjob`](pkg/remotionjob) contract.

`RemotionUpload` owns only validation and video rendering. It must not be
imported by this Go module and must not query PipelineGen's SQLite/Qdrant or
choose providers.

For Shorts, the dedicated `POST /api/script/shorts/generate` endpoint returns
`remotion.shorts.v1`. It accepts already-approved text, clip references and
indexed sound-effect cues; `include_sound_effects` controls whether the cues
are emitted. It does not regenerate the script or select assets.

Video rendering is intentionally limited to the short-form YouTube path:
`POST /api/script/shorts/render` for a synchronous smoke test, or
`POST /api/script/shorts/render/async` for the production path. The async route
returns `202` with a `job_id`; the `render.video` worker calls Remotion at
`VELOX_REMOTION_URL` (default `http://127.0.0.1:4317`). Longform and compilation
compositions are rejected before the renderer or worker is reached. Set
`upload_to_drive: true` and pass `drive_folder_id` as either a folder ID or a
full Google Drive folder URL to upload a permitted Shorts render.

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

`make verify-main` is the canonical fail-closed pre-push gate. It is composed of smaller targets so you can run only the checks relevant to the area you are working on and get faster, isolated failure signals. See [`docs/operations/verify-main-workflow.md`](docs/operations/verify-main-workflow.md) for the full workflow.

## Operational testing

For the Stock 9-phase battery, the Artlist clean test (`tests/operational/artlist_live_e2e_verify.sh`), and the **RETRY_WAIT / `CANCELLED` per-job diagnostic recipe** (API + SQLite fallback), see:

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

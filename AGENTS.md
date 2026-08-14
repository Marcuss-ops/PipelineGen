# PipelineGen Engineering Rules

## Mission

PipelineGen is a headless, server-side media pipeline. Keep it deterministic, CPU-friendly, modular, and suitable for CLI, workers, and backend automation. Do not turn it into a browser application or GUI editor.

## Non-negotiable architecture rules

- SQLite is the canonical state store. Keep `mattn/go-sqlite3`; do not introduce FTS5 assumptions.
- Qdrant is a derived projection and must be completely rebuildable from SQLite.
- Durable side effects after database commits must use the transactional outbox.
- The only target internal roots are `internal/app`, `internal/kernel`, `internal/capabilities`, and `internal/platform`.
- `internal/app` is the only composition root and owns lifecycle/wiring, not business behavior.
- `internal/capabilities` owns business capabilities and typed ports; `internal/kernel` owns genuinely shared semantic contracts.
- `internal/platform` owns concrete adapters, transport mechanics, filesystem/process access, and external systems.
- `internal/application`, `internal/api`, `internal/infrastructure`, and `internal/domain` are migration-only zones: no new capabilities, public contracts, providers, routes, files, or packages. Changes there must be migration/removal work or a correctness/security fix and must match an owner/deadline/target entry in `architecture/package_hotspots.json`.
- Google Drive writes from application flows must use the canonical delivery publisher.
- Never represent an unavailable backend as a successful no-op. Fail closed with typed errors or do not register the capability.
- New routing, provider selection, source policy, sampling, or resolution logic must enter a shared registry, resolver, or sampler. Do not duplicate the same decision logic across handlers.
- Do not add features to production code unless the user explicitly requested them.

## Data and migration rules

- Apply migrations only to the database that owns the affected tables.
- Prefer expand, backfill, cutover, contract for compatibility changes.
- Version Qdrant projections with content, schema, preprocessing, and model versions rather than file hash alone.
- Preserve deterministic asset IDs and idempotent job/outbox keys.

## Operational rules

- Keep services headless and CPU-first unless a GPU path is explicitly requested.
- Never commit credentials, tokens, cookies, or private keys.
- Generated API documentation must match registered routes.
- Run `make verify-main` before pushing (the pre-push hook runs `make verify-main` automatically).
- DO NOT use `git push --no-verify` to bypass the pre-push gate — bypass is reserved for unblocking CI emergencies and must be paired with a fixup! followup.
- **Agent constraints during development**: NON eseguire `make verify-main` durante le iterazioni. Dopo ogni modifica:
    1. Esegui il test del package modificato.
    2. Esegui `make verify-agent` (foundation + static + test dei soli componenti impattati, ~1-3 min).
    3. Esegui `make verify-fast` solo dopo una milestone significativa.
    4. Quando TUTTO il task è completo, esegui UNA SOLA VOLTA `make verify-main`, immediatamente prima del push.
    5. Se `verify-main` è già passato e working tree/commit non sono cambiati, NON ripeterlo.
    6. Non eseguire `auth-check`, `make dev` o `make run` salvo richiesta esplicita.
    7. Non scartare modifiche locali non correlate.

## Authentication SSOT (Velox admin token)

PipelineGen has exactly one canonical source for admin credentials. New code and scripts MUST honor this contract.

- **Canonical secret file**: `/etc/pipelinegen/pipelinegen.env` (mode `0640`, owner `root:pipelinegen-agents`).
- **Required contents**: at minimum `VELOX_ADMIN_TOKEN=<64-char-hex>`; optionally `VELOX_WORKER_TOKEN=<64-char-hex>` and `VELOX_PORT=8000`.
- **Canonical variable name**: always `VELOX_ADMIN_TOKEN`. Do not introduce `ADMIN_TOKEN`, `X-Admin-Token`, hard-coded literals (`test-admin-token-12345` is forbidden), or alternate env-file locations.
- **Loader convention**: agents never read the file directly. Use `scripts/with-velox-auth` (loads, validates `^[a-fA-F0-9]{64}$`, exports, and `exec`s) or set `TOKEN_FILE=/etc/pipelinegen/pipelinegen.env` (exported by the top-level Makefile).
- **Pre-flight gate**: `make auth-check` runs `scripts/with-velox-auth` against `/api/artlist/job-consumer` and fails closed on non-200 — no token is ever printed.
- **Live verification gates**: `make verify-artlist-live`, `verify-images-live`, `verify-script-live`, `verify-vidrush-live` each depend on `auth-check` and route through `scripts/with-velox-auth`.
- **Hygiene**: token values must be redacted as `REDACTED` or `<64-hex>` in any captured output; raw tokens must never appear in shell history, log files, commit messages, transcripts, or CI logs.
- **Rotation**: use `scripts/rotate_token.sh` (regenerates 64-hex, replaces the file, restarts the service via the systemd `EnvironmentFile`, and verifies the post-rotate PID environment).
- **One-off setup** (run once on the deploy host with `sudo`): `groupadd -f pipelinegen-agents`, add the running user to it, then `chown root:pipelinegen-agents /etc/pipelinegen/pipelinegen.env && chmod 0640 /etc/pipelinegen/pipelinegen.env`. Never use `0644`.

## Git workflow

- Work directly on `main`.
- No feature branches and no pull requests for routine repository work.
- Before push: fetch and rebase on `origin/main`; never force-push.
- Push directly to `main`.
- After every push, inspect `git log -n 5 --oneline` and confirm the remote contains the intended commit.
- Keep commits focused and describe actual behavior, not planning history.

## Documentation rule

The working tree contains only current operational or machine-consumed documentation. Do not add action plans, closure journals, evidence dumps, archived snapshots, or duplicate source-of-truth documents. Git history is the archive.

See `CANONICAL.md` for the authoritative source map.

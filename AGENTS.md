# PipelineGen Engineering Rules

## Mission

PipelineGen is a headless, server-side media pipeline. Keep it deterministic, CPU-friendly, modular, and suitable for CLI, workers, and backend automation. Do not turn it into a browser application or GUI editor.

## Non-negotiable architecture rules

- SQLite is the canonical state store. Keep `mattn/go-sqlite3`; do not introduce FTS5 assumptions.
- Qdrant is a derived projection and must be completely rebuildable from SQLite.
- Durable side effects after database commits must use the transactional outbox.
- `internal/api` owns transport only. It must not own SQL, Drive SDK calls, FFmpeg execution, or provider orchestration.
- `internal/application` owns use cases and typed ports. It must not depend directly on infrastructure implementations.
- `internal/infrastructure` owns concrete adapters.
- `internal/app` is the only composition root.
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
- Run `make verify-main` before pushing.
- DO NOT use `git push --no-verify` to bypass the pre-push gate — bypass is reserved for unblocking CI emergencies and must be paired with a fixup! followup.

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

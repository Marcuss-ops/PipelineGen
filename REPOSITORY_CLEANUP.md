# PipelineGen Repository Cleanup

> STATUS: ACTIVE
>
> Entry point for directory and database cleanup.

## Mission

Remove duplicate and legacy namespaces while preserving routes, job types, database data and runtime behavior.

Canonical layers:

```text
cmd/                    binaries
internal/api/           HTTP transport
internal/app/           composition and lifecycle
internal/application/   use cases
internal/domain/        canonical contracts and types
internal/infrastructure concrete adapters
pkg/                    leaf utilities
```

## Read in order

> **Nota (June 2026)**: i tre documenti storici sotto `docs/architecture/` sono
> stati consolidati e rimossi. Il loro contenuto è ora distribuito tra i
> file canonici qui sotto, che vanno letti nell'ordine indicato.

1. `ARCHITECTURE.md` — sistema diagramma, data flow, ownership dei layer,
   canonical structure definition of done (§1–§13).
2. `architecture/ownership.yaml` — mappa canonica
   "qual è il proprietario di X?" (single source of truth per capability).
3. `architecture/current.yaml` — tracker ratchet delle wave di
   consolidamento (status monotone-decreasing, exit gate per wave).
4. `AGENTS.md` — regole critiche del sistema + pattern canonici + Git Lessons.
5. `docs/cleanup/README.md` — roadmap operativa delle attività ancora aperte,
   con piani dettagliati per Health boundary, Script orchestration,
   composition, documentazione e configurazione.

## Execution order

1. Repository truth snapshot.
2. Consolidate application roots under canonical capabilities.
3. Move content, images and voiceover out of `internal/media`.
4. Move catalog and intelligence packages out of `internal/media`.
5. Consolidate providers and eliminate `internal/sources`.
6. Compact API packages.
7. Centralize database ownership and data paths.
8. Remove empty legacy roots and enable strict gates.
9. Execute the remaining boundary plans under `docs/cleanup/` and promote
   architecture checks from tracked exceptions to hard gates.

## Rules

- One capability has one owner.
- Use one focused PR per move.
- Search existing code before creating packages.
- Use `git mv` for pure moves.
- Update all direct callers in the same PR.
- Delete the old path in the same PR.
- Do not leave aliases, re-export packages or forwarding wrappers.
- API contains transport only.
- Application contains orchestration only.
- Domain imports no outer layer.
- SQL lives only under `internal/infrastructure/database`.
- Concrete adapters are constructed only in `internal/app`.
- `pkg` imports nothing from `internal`.
- Update architecture trackers only from verified code state.
- Never use a baseline update to hide a violation.

## Git protocol

```bash
git fetch origin
git checkout main
git pull --ff-only origin main
git checkout -b codex/<focused-cleanup>
rg '<old/import/path>' --type go
git mv <old-path> <new-path>
# update imports, wiring and tests
rg '<old/import/path>' --type go
gofmt -w <changed-go-files>
go test <affected-packages>
go vet <affected-packages>
go build ./...
git status -sb
git diff origin/main...HEAD
git log -n 5 --oneline
```

## Stop conditions

Stop and open a focused issue when:

- two packages both own the same persistent state;
- the destination already contains another implementation;
- a move requires a compatibility package;
- an application package contains SQL or concrete process execution;
- a migration is inconsistent across real databases;
- a move changes public routes, job types or payloads.

Cleanup is complete only when the completion definition in
`docs/cleanup/README.md` passes on `main` and the architecture trackers match
the verified code state.

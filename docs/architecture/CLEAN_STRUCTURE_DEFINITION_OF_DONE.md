# Clean Structure Definition of Done

> This checklist is required before declaring directory and database cleanup complete.

## Certification identity

```text
commit SHA:
release/tag:
architecture tracker version:
primary DB path:
primary migration version:
observability DB policy:
reviewer:
date:
```

## Gate 1 — Allowed roots

- [ ] `internal/` contains only `api`, `app`, `application`, `domain`, `infrastructure`.
- [ ] `internal/media` does not exist.
- [ ] `internal/sources` does not exist.
- [ ] `internal/core` does not exist.
- [ ] legacy `internal/assets`, `internal/artifacts`, `internal/jobs`, `internal/scripts`, `internal/upload` do not exist.
- [ ] empty directories are removed.
- [ ] `architecture/ownership.yaml` matches the real tree.
- [ ] `architecture/migration.yaml` contains verified counts and states.

## Gate 2 — Application ownership

- [ ] association lives under `application/assets/association`.
- [ ] realtime lives under `application/assets/realtime`.
- [ ] ingest lives under `application/assets/ingest`.
- [ ] monitor lives under `application/assets/monitor`.
- [ ] Artlist orchestration has one canonical owner.
- [ ] YouTube orchestration has one canonical owner.
- [ ] stock orchestration has one canonical owner.
- [ ] content lives under `application/content`.
- [ ] image use cases live under `application/images`.
- [ ] voiceover use cases live under `application/voiceover`.
- [ ] no top-level capability duplicates remain.

## Gate 3 — Provider/source ownership

- [ ] one provider registry exists.
- [ ] provider dispatch occurs only through the registry.
- [ ] provider contracts/use cases are in application.
- [ ] concrete HTTP/scraper/yt-dlp adapters are in infrastructure.
- [ ] no direct source switch exists outside the registry.
- [ ] Node scraper has one Go client adapter.
- [ ] registry freezes after composition.

## Gate 4 — API transport

- [ ] canonical API roots are assets, content, images, jobs, scripts and system.
- [ ] `api/script` has migrated to `api/scripts`.
- [ ] `api/workers` has migrated to `api/jobs`.
- [ ] source/drive/realtime/search-query transports are owned by `api/assets`.
- [ ] image transports are owned by `api/images`.
- [ ] API contains no SQL.
- [ ] API constructs no concrete adapter.
- [ ] API starts no unowned background goroutine.
- [ ] route and payload compatibility tests pass.

## Gate 5 — Domain boundaries

- [ ] domain imports no API package.
- [ ] domain imports no application package.
- [ ] domain imports no infrastructure package.
- [ ] canonical asset types live in `domain/asset`.
- [ ] canonical job/worker/lease types live in `domain/job`.
- [ ] canonical script types live in `domain/script`.
- [ ] no legacy type alias preserves an old owner.

## Gate 6 — Infrastructure boundaries

- [ ] SQL exists only under `infrastructure/database`.
- [ ] process execution exists only under approved infrastructure adapters.
- [ ] Drive SDK use exists only under infrastructure/Drive adapters.
- [ ] Qdrant client code exists only under infrastructure/Qdrant.
- [ ] filesystem storage adapters exist under infrastructure/files.
- [ ] remote worker clients exist under infrastructure/remote.
- [ ] concrete adapters are constructed only in `internal/app`.

## Gate 7 — `pkg`

- [ ] `pkg` imports nothing from `internal`.
- [ ] every package is a true leaf utility.
- [ ] no business/domain type lives in `pkg`.
- [ ] no generic dumping-ground package was introduced.

## Gate 8 — Database files and ownership

- [ ] primary operational DB is explicitly registered.
- [ ] observability DB is explicitly registered or removed.
- [ ] no additional ad-hoc SQLite files exist.
- [ ] DB connections are opened only by infrastructure.
- [ ] `internal/app` contains no `sql.Open`.
- [ ] API/application/domain contain no `database/sql` imports.
- [ ] every table has one repository owner.
- [ ] Qdrant is documented as derived state.
- [ ] local caches/workspaces are documented as non-canonical.

## Gate 9 — Schema and migrations

- [ ] migration versions are unique.
- [ ] applied migration files are immutable.
- [ ] checksums are verified.
- [ ] fresh-database migration test passes.
- [ ] upgrade migration test passes.
- [ ] migration retry/reopen test passes.
- [ ] critical schema columns/indexes are checked.
- [ ] `PRAGMA integrity_check` returns `ok`.
- [ ] `PRAGMA foreign_key_check` returns no rows.
- [ ] DB doctor reports no pending/error state.

## Gate 10 — Data directory

- [ ] database paths are resolved from one configuration owner.
- [ ] workspace path is explicit.
- [ ] cache path is explicit.
- [ ] export path is explicit.
- [ ] temp cleanup has one owner.
- [ ] backups live outside the active data volume.
- [ ] path migration, if performed, has rollback evidence.

## Gate 11 — Backup and restore

- [ ] primary SQLite backup succeeds.
- [ ] backup checksum is recorded.
- [ ] backup integrity check passes.
- [ ] off-host copy exists.
- [ ] retention policy is active.
- [ ] observability backup/retention policy is explicit.
- [ ] Qdrant snapshot or rebuild procedure is verified.
- [ ] restore into clean staging succeeds.
- [ ] post-restore E2E read/write test passes.
- [ ] RTO and RPO are recorded.

## Gate 12 — No compatibility residue

- [ ] no old-path import remains.
- [ ] no forwarding package remains.
- [ ] no compatibility type alias remains.
- [ ] no wrapper exists only to preserve the old directory.
- [ ] no stale generated file references removed paths.
- [ ] comments and docs use canonical paths.

## Gate 13 — Build and tests

- [ ] `gofmt` clean.
- [ ] `go mod tidy` creates no diff.
- [ ] focused package tests pass.
- [ ] race tests pass for concurrent packages.
- [ ] repository tests pass.
- [ ] `go vet ./...` passes.
- [ ] `go build ./...` passes.
- [ ] architecture checks pass.
- [ ] strict architecture mode passes.
- [ ] CI is green on the certified commit.

## Required final commands

```bash
git status -sb
go mod tidy
git diff --exit-code -- go.mod go.sum
gofmt -w .
git diff --exit-code
go test ./...
go test -race ./internal/application/jobs/... ./internal/app/...
go vet ./...
go build ./...
go run ./scripts/archcheck --strict
bash scripts/ci-architectural-checks.sh
rg 'internal/media|internal/sources' --type go
rg 'internal/application/(association|realtime|ingest|monitor|artlist)' --type go
rg 'database/sql' internal/api internal/application internal/domain --type go
rg 'sql\.Open\(' internal --type go
```

Expected results for the final four searches: zero prohibited hits.

## Approval

```text
[ ] APPROVED — canonical directory structure complete
[ ] APPROVED — duplicate/legacy roots removed
[ ] APPROVED — database ownership and paths clean
[ ] APPROVED — backup and restore verified

Commit: ______________________________________
Reviewer: ____________________________________
Date: ________________________________________
Known limits: _________________________________
```

Any later change that reintroduces a legacy root, ad-hoc database, duplicate owner or forbidden import invalidates this approval.
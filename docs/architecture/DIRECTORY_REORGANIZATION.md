# Directory Reorganization Plan

> STATUS: ACTIVE
>
> Purpose: eliminate duplicate/legacy roots without changing behavior.

## 1. Target tree

```text
cmd/
  admin/
  server/
  worker/

internal/
  api/
    assets/
    content/
    images/
    jobs/
    middleware/
    scripts/
    system/
    transport/
    routes.go
    server.go

  app/
    bootstrap.go
    composition.go
    lifecycle.go
    registry.go
    shutdown.go
    modules/

  application/
    assets/
      association/
      catalog/
      classification/
      enrichment/
      index/
      ingest/
      monitor/
      ontology/
      providers/
        artlist/
        stock/
        youtube/
      realtime/
      resolve/
      sync/
      tree/
    content/
      books/
      lessons/
    images/
      generation/
      ingest/
      search/
      styles/
    jobs/
      assets/
      outbox/
      worker/
    scripts/
      batch/
      curation/
      documents/
      generate/
      jobs/
      memory/
      scenes/
    voiceover/
      service/
      sync/

  domain/
    asset/
    job/
    script/

  infrastructure/
    ai/
    config/
    database/
      sqlite/
        assets/
        content/
        jobs/
        observability/
        scripts/
    drive/
    files/
      storage/
    jobs/
      local/
    logging/
    media/
      ffmpeg/
      processor/
    observability/
    process/
    qdrant/
    remote/
    security/

pkg/
  <leaf utilities only>
```

This is a responsibility map, not permission to create every directory in advance. Create a directory only when real code moves into it.

## 2. Current roots to eliminate or consolidate

### Application duplicates

| Current path | Canonical destination | Rule |
|---|---|---|
| `internal/application/association` | `internal/application/assets/association` | Asset matching/association use cases |
| `internal/application/realtime` | `internal/application/assets/realtime` | Realtime asset indexing/search state |
| `internal/application/ingest` | `internal/application/assets/ingest` | Asset ingest orchestration |
| `internal/application/monitor` | `internal/application/assets/monitor` | Source/channel monitoring use cases |
| `internal/application/artlist` | split between `assets/providers/artlist` and infrastructure adapter | No second Artlist application root |

Before moving `application/artlist`, classify every file:

- provider selection/search policy → `application/assets/providers/artlist`;
- scraper HTTP/client implementation → `infrastructure/artlist` or existing concrete adapter location;
- persistence → `infrastructure/database/sqlite/assets`;
- transport DTO/handler → `api/assets`.

### `internal/media` legacy umbrella

| Current package | Canonical owner |
|---|---|
| `assetindex` | `application/assets/index` |
| `assettree` | `application/assets/tree` |
| `clipcatalog` | `application/assets/catalog` |
| `clipindexer` | `application/assets/index` plus concrete DB/Qdrant adapters in infrastructure |
| `clipresolver` | `application/assets/resolve` |
| `foldermemory` | `application/assets/catalog` or `application/assets/enrichment` after responsibility review |
| `autotag` | `application/assets/enrichment` |
| `classifier` | policy in `application/assets/classification`, model/client in `infrastructure/ai/classification` |
| `ontology` | `application/assets/ontology` |
| `semantic` | policy in `application/assets/enrichment`, external model code in `infrastructure/ai/semantic` |
| `catalogsync` | `application/assets/sync` |
| `stockpipeline` | provider policy in `application/assets/providers/stock`, concrete download/process code in infrastructure |
| `fullimages` | `application/images` |
| `generation` | `application/images/generation` |
| `books` | `application/content/books` |
| `lessons` | `application/content/lessons` |
| `voiceoversync` | `application/voiceover/sync` |
| `videomuscles` | classify as asset processing or remove if obsolete |
| `deletion.go` | `application/assets/delete` or lifecycle owner |

A package containing both policy and adapters must be split before the move. Do not move mixed architecture into a new mixed directory.

### Provider/source duplicates

| Current path | Canonical destination |
|---|---|
| `internal/sources/youtube` | provider contract/use case under `application/assets/providers/youtube`; yt-dlp/extractor under infrastructure |
| `internal/sources/artlist` | provider contract/use case under `application/assets/providers/artlist`; scraper/client under infrastructure |
| Node scraper | remains external runtime; Go provider calls it through one concrete client |

There must be one provider registry. Direct source switches outside that registry must be removed.

### API duplicates

| Current API path | Canonical destination |
|---|---|
| `api/drive` | `api/assets` |
| `api/realtime` | `api/assets` |
| `api/searchqueries` | `api/assets` |
| `api/sources` | `api/assets` |
| `api/fullimages` | `api/images` |
| `api/workers` | `api/jobs` |
| `api/script` | `api/scripts` |

Each canonical API feature should expose one handler façade and one route registration surface. Large implementation files may remain split, but the package must not own business logic or SQL.

## 3. PR sequence

### DIR-0 — Truth snapshot

Branch:

```text
codex/dir-truth-snapshot
```

Create a verified inventory:

```bash
find internal -type d | sort
find internal -type f -name '*.go' | sort
rg '^package ' internal --type go
rg 'internal/media|internal/sources' --type go
rg 'internal/application/(association|realtime|ingest|monitor|artlist)' --type go
```

Update counts in `architecture/migration.yaml`. Do not move code in this PR.

### DIR-1 — Application roots

Move in separate commits/PRs:

1. association;
2. realtime;
3. ingest;
4. monitor;
5. Artlist application split.

Exit gate:

```bash
rg 'internal/application/(association|realtime|ingest|monitor|artlist)' --type go
```

returns zero active imports.

### DIR-2 — Content, images, voiceover

Move low-coupling packages first:

```text
media/books
media/lessons
media/fullimages
media/generation
media/voiceoversync
```

Do not mix this with catalog/index changes.

### DIR-3 — Catalog and intelligence

Recommended order:

```text
assettree
clipcatalog
foldermemory
ontology
autotag
classifier
semantic
assetindex
clipindexer
clipresolver
catalogsync
deletion.go
```

For each package, separate:

- domain types;
- application policy/use cases;
- database repositories;
- AI/Qdrant/Drive/process adapters;
- app wiring.

### DIR-4 — Providers and sources

Move YouTube, Artlist and stock one provider at a time. The provider registry remains the only dispatch surface.

Exit gate:

```bash
rg 'internal/sources|internal/media/(stockpipeline|videomuscles)' --type go
```

returns zero.

### DIR-5 — API compaction

Move one API package per PR or one tightly related group. Preserve all routes and payloads with contract tests.

### DIR-6 — Delete legacy roots

Only after all callers are migrated:

```bash
find internal/media -type f
find internal/sources -type f
```

must return nothing before deleting roots.

## 4. Move protocol

For every move:

1. identify owner and callers;
2. identify persistence and external adapters;
3. split mixed files if required;
4. use `git mv`;
5. update package declaration/imports;
6. update composition wiring;
7. move tests with implementation;
8. remove aliases/wrappers;
9. run focused tests;
10. run full build;
11. update ownership/migration maps;
12. verify old path has zero references.

## 5. Prohibited shortcuts

- empty compatibility packages at old paths;
- type aliases preserving legacy imports;
- `common`, `shared`, `utils` dumping grounds inside `internal`;
- a second provider registry;
- application packages importing concrete SQLite, Drive, Qdrant or `os/exec`;
- API packages owning goroutine orchestration;
- moving files without moving their tests;
- changing behavior during a namespace-only PR;
- merging a move with unrelated refactors.

## 6. Package size rules

Guidelines:

```text
api root files                 <= 15
api feature package            <= 40 files
application capability bundle  <= 10 direct dependencies
app module bundle              <= 10 direct dependencies
infrastructure adapter         one external system/responsibility
```

When a package exceeds the limit, split by capability, not by arbitrary file type.

## 7. Required tests

```bash
gofmt -w <changed-files>
go test <affected-packages>
go test -race <concurrent-packages>
go vet <affected-packages>
go build ./...
go run ./scripts/archcheck
bash scripts/ci-architectural-checks.sh
```

API moves also require route/payload contract tests. Persistence moves require repository tests against temporary SQLite databases.

## 8. Exit gate

Directory cleanup is complete when:

- `internal/` contains only allowed roots from `architecture/ownership.yaml`;
- `internal/media` and `internal/sources` no longer exist;
- duplicate application roots are gone;
- API packages match canonical feature roots;
- zero old-path imports, aliases and forwarding packages remain;
- architecture maps match the real tree;
- strict checks and the full build pass.
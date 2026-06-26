# Target Repository Structure

## Goal

The repository must make ownership obvious from the filesystem. Business capabilities are vertical slices. Shared technical implementations live under platform packages. Shared domain concepts live in a small kernel.

## Target tree

```text
cmd/
  server/main.go
  worker/main.go
  admin/main.go
  archgen/main.go
  archcheck/main.go

internal/
  app/
    compose.go
    registry.go
    lifecycle.go
    shutdown.go

  kernel/
    asset/
    job/
    script/
    event/
    identity/
    errors/

  capabilities/
    assets/
    artlist/
    youtube/
    scripts/
    images/
    voiceover/
    content/
    channels/
    jobs/
    system/

  platform/
    config/
    sqlite/
    drive/
    qdrant/
    ffmpeg/
    process/
    filesystem/
    observability/
    httpserver/
    ollama/
    nvidia/
    youtube/

migrations/
  sqlite/

architecture/
  policy.yaml
  deprecations.yaml
  generated/

docs/
  architecture/godlike/
  generated/

scripts/
  ci/
  tools/
```

## `cmd`

Each binary entrypoint must be almost empty. It may:

1. create the root context;
2. load and validate config;
3. call `app.Compose`;
4. start the selected runtime mode;
5. wait for shutdown.

It must not instantiate repositories, register routes, parse domain payloads, open SQLite directly, or construct feature services.

## `internal/app`

This is the only composition root. It may import all internal packages because its only responsibility is assembly.

Allowed responsibilities:

- create platform clients;
- create transaction and database handles;
- build capabilities;
- register typed module descriptors;
- validate the completed graph;
- start and stop lifecycle hooks.

Forbidden responsibilities:

- business rules;
- SQL queries;
- DTO normalization;
- provider selection;
- ranking logic;
- feature-specific fallback behavior.

## `internal/kernel`

The kernel contains only stable concepts shared by multiple capabilities. It imports the Go standard library only, except for narrowly approved value-only libraries.

The kernel must not contain:

- Gin types;
- SQL or repository implementations;
- Google Drive, Qdrant, FFmpeg, Ollama, or HTTP clients;
- configuration structs;
- loggers;
- feature flags;
- application services.

A type belongs in the kernel only when at least two real capabilities need the same semantic contract. Convenience is not sufficient.

## `internal/capabilities`

Each directory owns one business capability. A capability may contain:

```text
module.go       typed descriptor and Build function
contract.go     commands, queries, results, public ports
service.go      orchestration/use cases
http.go         thin HTTP transport
jobs.go         job codecs and handlers
events.go       produced and consumed events
repository.go   consumer-owned persistence ports
adapters.go     capability-specific adapters to platform services
*_test.go       unit and integration tests
```

Subdirectories are allowed only when the capability becomes too large and the ownership remains clear. Generic folders named `service`, `repository`, `models`, `utils`, or `helpers` are forbidden as top-level dumping grounds.

## `internal/platform`

Platform packages implement technical capabilities. They do not own business semantics.

Examples:

- `platform/qdrant` knows how to create collections and search vectors;
- the Assets capability decides what an asset search means;
- `platform/drive` knows how to upload and move files;
- the owning capability decides when and why a file is delivered;
- `platform/sqlite` owns connection, transaction, migration, and pragma mechanics;
- capability repositories own SQL for their tables.

## Package size limits

Targets, enforced progressively:

- production file: maximum 500 lines;
- constructor: maximum 8 direct dependencies;
- capability descriptor: one per capability;
- transport package: one primary handler and one route registration path;
- no package may be split only to hide a god object.

When a package grows, split by use case or owned concept, not by arbitrary technical suffix.
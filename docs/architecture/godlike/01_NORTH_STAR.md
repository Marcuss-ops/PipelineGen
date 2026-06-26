# North Star

## Mission

PipelineGen is a headless control plane and workflow engine for media automation. It coordinates ingestion, enrichment, generation, storage, indexing, delivery, and background execution through one explicit modular monolith.

The architecture is successful when a developer can answer every ownership question with one path, one type, one registry entry, and one runtime authority.

## Core invariants

### One authority per fact

Every fact has one canonical owner:

- asset identity: canonical asset domain and primary SQLite tables;
- asset location: `asset_locations` or its final canonical equivalent;
- processing state: one processing model and one writer;
- job state: one job domain, one repository, one state machine;
- workflow state: one persisted workflow model when introduced;
- delivery state: one delivery model and one writer;
- runtime configuration: immutable validated config loaded at boot;
- semantic index: Qdrant projection derived from canonical metadata;
- routes and jobs: typed module descriptors registered once.

JSON blobs, Drive metadata, local paths, Qdrant payloads, generated manifests, and API responses are projections. They must not become competing authorities.

### One execution path

For each operation there is one canonical entrypoint:

- one ingest path per media kind;
- one enqueue path per job type;
- one handler per job type;
- one normalization function per request;
- one provider registry;
- one resolver registry;
- one sampler registry;
- one deletion path;
- one finalization path;
- one indexing/outbox path.

No direct writes may bypass the canonical use case.

### Fail closed

Mandatory dependencies are validated during composition. Missing required dependencies fail boot or disable the capability before route registration. The system must not mount handlers backed by `nil`, return success for unimplemented commands, or silently switch to a legacy path.

### Compiler-enforced boundaries

Architecture must be represented by package boundaries and typed contracts, not conventions alone. `interface{}`, broad `any`, runtime type assertions, dependency setters, pass-through wrappers, and cross-capability imports are treated as architecture debt.

### Deletion is part of delivery

A migration is incomplete while old routes, aliases, fields, constructors, jobs, config keys, tests, docs, metrics, or allowlists remain. Every cutover ends with CONTRACT: delete the superseded path.

## Final operating model

The final system is a modular monolith with:

- tiny `cmd` entrypoints;
- one `internal/app` composition layer;
- a minimal stable kernel for shared domain concepts;
- vertical capabilities that own transport, use cases, contracts, jobs, and adapters;
- platform packages for SQLite, Drive, Qdrant, FFmpeg, process execution, AI clients, observability, filesystem, and config;
- generated route, job, provider, dependency, and ownership manifests;
- strict CI that rejects architectural drift.

## Explicit non-goals

The target is not:

- a microservice rewrite;
- PostgreSQL migration before interfaces and ownership are clean;
- a generic framework with abstract factories everywhere;
- duplicate repositories hidden behind adapters;
- permanent compatibility layers;
- a second queue or second metadata database;
- a browser or GUI platform.

## Success signals

The program is complete when:

- `grep` finds no legacy compatibility packages, fake routes, duplicate job registrations, or dependency setters;
- route and job documentation is generated from code;
- every table, service, job, route, provider, and config section has one owner;
- deleting a capability requires removing one vertical package plus its registry entry and migrations;
- the architecture checker has no grandfathered baseline or allowlist;
- main remains green without hidden runtime fallbacks.
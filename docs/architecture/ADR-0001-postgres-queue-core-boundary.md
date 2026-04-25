# ADR-0001: Postgres primary, SQLite cache, real queue, core/app split

## Status
Accepted

## Decision
PipelineGen should move to this target architecture:

1. PostgreSQL becomes the primary persistence layer for jobs, workers, events, and orchestration state.
2. SQLite remains optional and local-only, used as cache/materialized state, not source of truth.
3. `queue.json` becomes legacy/dev-only. Production async flow should move to Redis Streams or NATS.
4. The reusable runtime should be treated as a core engine. VeloxEditing stays an app/product layer on top.

## Why
The current JSON-first model is simple but weak under concurrency, scale, and multi-worker orchestration.
A real relational store plus a real queue matches the existing job model much better.

## Consequences
### Persistence
- add migrations directory
- introduce database-backed repositories
- keep JSON only for compatibility/dev fallback

### Queue
- define one queue transport contract
- provide Redis Streams and NATS implementations
- stop treating `queue.json` as production authority

### Boundaries
- move reusable orchestration/runtime concerns under a core-engine boundary
- keep HTTP handlers, branding, product defaults, and deployment wiring in app-facing packages

## Migration order
1. Introduce interfaces and adapters
2. Add Postgres migrations and repositories
3. Add real queue transport
4. Make JSON fallback-only
5. Continue package split between engine and app

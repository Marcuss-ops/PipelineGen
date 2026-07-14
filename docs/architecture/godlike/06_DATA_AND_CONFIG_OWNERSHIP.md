# Data and Config Ownership

This document defines canonical ownership for data, configuration, and state in PipelineGen.

## Durable authority

SQLite is the durable authority for all canonical state. Any other store is a projection and must be rebuildable from SQLite.

## One owner per fact

Every fact in the system has one canonical owner. No two packages may independently compute or store the same fact.

## Database rules

- Apply migrations only to the database that owns the affected tables.
- Prefer expand, backfill, cutover, contract for compatibility changes.
- Preserve deterministic asset IDs and idempotent job/outbox keys.

## Qdrant projection

Qdrant is a derived projection and must be completely rebuildable from SQLite. It is never the source of truth.

## Drive and filesystem

Google Drive and local filesystem are side-effect surfaces. Writes must be durable in SQLite before being emitted through the transactional outbox.

## Configuration

Configuration is owned by the composition root (`internal/app`). Runtime configuration is loaded once at startup and treated as read-only.

## Future storage changes

Any new storage backend must be introduced behind a port, wired only at the composition root, and documented in this file before use.

# Data and Configuration Ownership

## Durable authority

The primary SQLite database is the authority for durable PipelineGen metadata. Qdrant, Drive metadata, local files, caches, and generated documents are derived views or delivery targets.

## One owner per fact

- Asset identity belongs to the asset domain.
- Asset locations belong to the location model.
- Processing status belongs to the processing model.
- Jobs and leases belong to the job domain.
- Workflow progress belongs to the workflow domain.
- Artifacts belong to the artifact model.
- Delivery state belongs to the delivery domain.
- Outbox records belong to the outbox domain.

The same fact must not have multiple independent writers.

## Database rules

- New SQLite migrations live under `migrations/sqlite/`.
- Released migrations are not edited.
- There is one migration ledger and one runner.
- Every table has one owning capability.
- HTTP handlers do not contain SQL.
- Application ports do not expose raw database handles.
- Cross-capability access uses typed ports, explicit read models, or events.

## Qdrant projection

SQLite owns identity, lifecycle, source facts, versions, metadata, and durable locations. Qdrant owns the searchable vector projection.

The canonical sequence is:

1. commit metadata in SQLite;
2. persist an outbox record in the same transaction;
3. update Qdrant asynchronously and idempotently;
4. track projection version and outcome;
5. allow a complete rebuild from SQLite.

## Drive and filesystem

Drive and local files represent locations, not identity. Upload, move, rename, and removal operations pass through one location or delivery service.

## Configuration

Configuration follows one boot pipeline:

```text
input -> load -> defaults -> validate -> immutable configuration
```

Defaults and validation are defined once. Business services receive narrow capability configuration. Runtime mutation and duplicated fallback values are forbidden.

## Future storage changes

Any later storage-engine migration uses the existing repository boundaries and the sequence EXPAND, BACKFILL, CUTOVER, CONTRACT. It must not introduce permanent dual-write or dual-read paths.
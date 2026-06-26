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

## Actions to execute

- Inventory every durable fact, table, column family, writer, reader, projection, file location, and configuration key.
- Assign one owning capability and one canonical writer to every durable fact and table.
- Add ownership metadata to migrations and generate a table ownership report.
- Move all schema execution to one migration runner and stop editing released migrations.
- Replace handler SQL and raw database exposure with narrow repository operations owned by the correct capability.
- Route cross-capability reads through typed ports, read models, or events.
- Make the SQLite transaction plus outbox record the canonical write boundary for Qdrant projections.
- Add idempotent projection versioning, retry visibility, and a tested full Qdrant rebuild from SQLite.
- Route Drive and filesystem changes through the canonical location/delivery service.
- Centralize configuration loading, defaults, environment mapping, validation, and immutability; pass narrow config slices to capabilities.
- Remove duplicate writers, duplicate defaults, fallback values, and obsolete environment aliases after cutover.

## Final DONE check

- [ ] Every durable fact and table has one documented owner and one writer.
- [ ] Generated ownership output matches migrations and repository implementations.
- [ ] There is one migration directory, ledger, and runner; released migrations are unchanged.
- [ ] No HTTP handler executes SQL and no application contract exposes a raw database handle.
- [ ] Cross-capability data access uses approved ports, read models, or events.
- [ ] Qdrant is fully rebuildable from SQLite and has no independent canonical writer.
- [ ] Metadata and outbox records commit atomically for projection-triggering changes.
- [ ] Drive and local files are represented as locations rather than competing asset identities.
- [ ] Configuration has one load/default/validate pipeline and is immutable after boot.
- [ ] No duplicate config key, runtime fallback, parallel writer, or permanent dual-read path remains.
- [ ] Data integrity, projection rebuild, migration, and architecture tests pass on `main`.
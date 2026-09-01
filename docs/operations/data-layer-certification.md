# Data layer certification

The final four-plane gate is:

```bash
make certify-data-layer
make certify-data-layer-json > certification.json
```

It is fail-closed. A missing database, missing migration snapshot, absent
restore evidence, or unexecuted staging harness is a failure. This prevents a
local unit-test pass from being mistaken for a completed migration.

The default paths are:

```text
data/media/media.db.sqlite
data/jobs/jobs.db.sqlite
data/cache/cache.db.sqlite
data/observability/api_requests.db.sqlite
```

For a migration certification, provide the old snapshots explicitly:

```bash
MEDIA_OLD_DB=/secure/snapshots/media-before.sqlite \
JOBS_OLD_DB=/secure/snapshots/jobs-before.sqlite \
make certify-data-layer
```

The staging-only gates require operator-owned harnesses. They must perform the
actual process kill/restart, plane outage, and backup-restore checks before
returning zero:

```bash
CERTIFY_LIVE=1 \
CERTIFY_LIVE_HARNESS=/secure/harnesses/restart-and-isolation.sh \
BACKUP_RESTORE_HARNESS=/secure/harnesses/backup-restore.sh \
CERTIFY_QDRANT=1 \
make certify-data-layer
```

The cache is intentionally excluded from durable backup. The certification
requires its failure to be treated as a miss; media, jobs, and observability
are the durable recovery planes.

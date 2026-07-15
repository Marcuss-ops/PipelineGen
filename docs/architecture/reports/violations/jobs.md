# Jobs architectural violations

Snapshot of production-code references that cross application boundaries
through `*sql.DB`, `net/http`, `os`, or `RootFolderOverride`.

| Package | Weighted severity |
|---|---:|
| `internal/application/jobs` | 2 |
| `internal/application/jobs/assets` | 11 |
| `internal/application/jobs/finalizer` | 6 |
| `internal/application/jobs/iobinder` | 8 |
| `internal/application/jobs/outbox` | 38 |
| `internal/application/jobs/outbox/metadataexport` | 2 |
| `internal/application/jobs/worker` | 2 |
| **Total** | **69** |

## Priority

The outbox surface dominates this area. Constructor and handler contracts should
consume narrow persistence and delivery ports; transaction ownership must remain
at the canonical finalization and composition boundaries.

Exact evidence belongs to the generated scanner output rather than this stable
navigation document.

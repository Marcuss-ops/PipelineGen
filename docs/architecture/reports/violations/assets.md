# Asset application architectural violations

Snapshot of production-code references that cross application boundaries
through `*sql.DB`, `net/http`, `os`, or `RootFolderOverride`.

| Package | Weighted severity |
|---|---:|
| `internal/application/assets/artifacts` | 3 |
| `internal/application/assets/delivery` | 5 |
| `internal/application/assets/ingest` | 9 |
| `internal/application/assets/lifecycle` | 2 |
| `internal/capabilities/assets/providers/artlist` | 10 |
| `internal/capabilities/assets/providers/stock/enrichment` | 8 |
| `internal/capabilities/assets/providers/stock/stockpipeline` | 25 |
| `internal/application/assets/sourcing/youtube` | 1 |
| `internal/application/assets/verification` | 8 |
| `internal/application/assets/videomuscles` | 7 |
| **Total** | **78** |

## Priority

The stock pipeline is the largest asset hotspot. Continue moving filesystem,
database and delivery mechanics behind typed ports owned by the composition
root. Artlist and ingest are the next highest-value migrations.

Exact evidence belongs to the generated scanner output rather than this stable
navigation document.

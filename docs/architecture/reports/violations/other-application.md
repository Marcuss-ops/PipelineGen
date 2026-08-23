# Other application architectural violations

Snapshot of production-code references that cross application boundaries
through `*sql.DB`, `net/http`, `os`, or `RootFolderOverride`.

| Package | Weighted severity |
|---|---:|
| `internal/application/books` | 5 |
| `internal/application/clips` | 3 |
| `internal/application/images` | 11 |
| `internal/application/iobinder` | 5 |
| `internal/application/qdrant/maintenance` | 3 |
| `internal/application/qdrant/reconciler` | 1 |
| `internal/application/voiceover` | 5 |
| `internal/application/voiceover/persistence` | 1 |
| `internal/application/workerdoctor` | 5 |
| `internal/application/youtube` | 8 |
| `internal/application/youtube/adapters` | 4 |
| `internal/application/youtube/usecase` | 8 |
| **Total** | **63** |

## Priority

Images and YouTube use cases are the largest remaining surfaces in this group.
Filesystem and HTTP work should move behind shared typed ports rather than being
reimplemented per capability.

Exact evidence belongs to the generated scanner output rather than this stable
navigation document.

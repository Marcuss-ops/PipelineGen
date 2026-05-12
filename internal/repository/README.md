# Repositories (internal/repository)

The `repository` package contains the data access layer (DAL) for PipelineGen. It implements the Repository Pattern to abstract the underlying database operations and provide a clean, domain-oriented interface for data persistence.

## Architecture

Most repositories in this package follow a consistent pattern:
- **Interface/Struct**: Defines the available data operations.
- **SQL Implementation**: Uses standard `database/sql` (via SQLite) to execute queries.
- **Domain Models**: Transforms raw SQL rows into objects defined in `pkg/models` or local `Record` structs.

## Key Repositories

### Clips Repository
The most complex repository in the system, managing media assets, folders, and metadata. It includes support for:
- Full-Text Search (FTS5) fallback.
- Perceptual Hash (PHash) lookup.
- Complex tree-like navigation (parent/child relationships).

### Jobs Repository
Tracks the state of all background jobs, including progress, logs, and failure reasons.

### Asset Tree Repository
Manages a unified view of all assets (Clips, Images, Voiceovers) in a hierarchical folder structure, regardless of their original source.

### Specialized Repositories
- `images`: Image-specific metadata and tagging.
- `voiceovers`: Records for generated text-to-speech assets.
- `scripts`: Storage for generated scripts and their versions.
- `monitors`: Configuration for YouTube channel monitoring.

## Common Patterns

### Unified Database Access
Most repositories share a common SQLite connection pool but operate on isolated tables or separate database files (managed by `internal/storage`).

### Migration Support
Many repositories include an `EnsureSchema` function to verify that their required tables exist, though the primary migration path is via the `migrations/` directory.

### Workspace Isolation
Where applicable, repositories enforce workspace-level isolation to ensure data from one project doesn't leak into another.

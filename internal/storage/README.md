# Storage Package

The `storage` package provides the low-level database infrastructure for PipelineGen. It is built around **SQLite** with an emphasis on performance, reliability, and concurrency.

## Core Features

- **WAL Mode (Write-Ahead Logging)**: All databases are configured in WAL mode, allowing multiple concurrent readers and one writer without blocking.
- **Connection Pooling**: Connections are pooled and configured with optimal pragmas for high-speed access.
- **In-Memory Support**: Supports `:memory:` databases with shared cache, ideal for testing and high-speed temporary storage.
- **Auto-Migrations**: Built-in migration runner that applies SQL scripts from the `migrations/` directory.
- **Safe Backups**: Uses SQLite's `VACUUM INTO` command to create consistent point-in-time backups without requiring file system locks or interrupting active processes.
- **FTS5 Integration**: Support for SQLite Full-Text Search (FTS5) for fast asset discovery.
- **Caching**: Includes a generic memory cache (`cache.go`) for reducing database hits on frequently accessed data.

## Optimal Pragmas

When a connection is established, the following pragmas are automatically applied:
- `journal_mode=WAL`: Enables high-concurrency logging.
- `synchronous=NORMAL`: Balances performance and safety in WAL mode.
- `busy_timeout=5000`: Waits up to 5 seconds before failing a locked transaction.
- `temp_store=MEMORY`: Keeps temporary tables in RAM.
- `mmap_size=30GB`: Leverages memory-mapped I/O for faster data access.

## Usage

```go
db, err := storage.NewSQLiteDB("./data", "velox.db.sqlite", log)
if err != nil {
    log.Fatal(err)
}
defer db.Close()

// Run migrations
err = db.RunMigrations(log, "./migrations/sqlite")
```

## Database Isolation

The project uses multiple isolated SQLite files to minimize blast radius and allow for independent scaling:
- `velox.db.sqlite`: Core system state, jobs, and registry.
- `media.db.sqlite`: Unified media assets, including YouTube clips, Artlist, stock, images, and voiceovers.
- `images.db.sqlite`: Legacy image assets and tagging metadata, if present in older data sets.

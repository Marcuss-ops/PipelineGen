# CLI Utilities (`cmd/`)

This directory now has two entrypoints:

- `cmd/server/`: the main HTTP server and workers
- `cmd/admin/`: one-shot admin and maintenance commands

## Admin Commands

Run with:

```bash
go run ./cmd/admin <command> [flags]
```

Available commands:

- `backfill-hash`
- `backfill-hash-v2`
- `backfill-asset-index`
- `backfill-asset-tree`
- `cleanup-orphans`
- `cleanup-all-orphans`
- `cleanup-artlist-empty-folders`
- `cleanup-stock-orphans`
- `delete-specific-folders`
- `sync-all-drive`
- `test-youtube`
- `verify-artlist-pipeline`

## Notes

- `cmd/server` remains the canonical runtime entrypoint.
- Older standalone command directories were folded into `cmd/admin` to keep the tree smaller.

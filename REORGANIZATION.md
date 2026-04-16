# Directory Reorganization - April 9, 2026

## Summary

The project directory structure has been reorganized for better clarity, maintainability, and separation of concerns.

## What Changed

### New Structure

| Old Path | New Path | Reason |
|----------|----------|--------|
| `go-master/` | `src/go-master/` | Source code organization |
| `video-stock-creator/` | `src/rust/` | Clearer naming for Rust source |
| `modules/` | `src/python-legacy/` | Clarify it's legacy code |
| `video-stock-creator.bundle` | `bin/video-stock-creator.bundle` | Binary files in dedicated dir |
| `go-master/server` | `bin/server` | Binary files in dedicated dir |
| `data/lightpanda/` | `tests/lightpanda/` | Test data not runtime data |
| `data/stock-links/` | `tests/stock-links/` | Test data not runtime data |
| `ARCHITETTURA_BACKEND.md` | `docs/ARCHITETTURA_BACKEND.md` | All docs in one place |
| `ENDPOINT_ATTIVI.md` | `docs/ENDPOINT_ATTIVI.md` | All docs in one place |
| `effects/EffettiVisiv/` | `effects/overlays/` | Consistent English naming |

### Removed

| Path | Reason |
|------|--------|
| `config/assets/` | Duplicate of `/assets/` |
| `modules/video/assets/` | Duplicate of `/assets/` |
| `modules/credentials.json` | Duplicate of `go-master/credentials.json` |
| `modules/token.json` | Duplicate of `go-master/token.json` |
| `config/backups/` | Old backup files no longer needed |
| `go-master/data/backups/` | Empty directory |
| `effects/Test/` | Partial Rust code copy (exists in `src/rust/src/ffmpeg/`) |
| `src/rust/Effects/` | Duplicate of `/effects/overlays/` |
| `utils/` | Only contained README.md |
| `src/python-legacy/youtubevideostockDonwloader/tests/` | Empty directory |

### Backward Compatibility

Symlinks have been created to maintain backward compatibility:

```
go-master -> src/go-master
rust -> src/rust
video-stock-creator.bundle -> bin/video-stock-creator.bundle
```

### New Files

- `.gitignore` - Root-level gitignore to exclude build artifacts, duplicates, and temporary files
- `REORGANIZATION.md` - This file

## Updated Files

- `start.sh` - Updated paths to reference new structure
- `README.md` - Updated directory structure documentation

## Benefits

1. **Clearer separation**: Source code (`src/`), binaries (`bin/`), tests (`tests/`), docs (`docs/`)
2. **No duplicates**: Single source of truth for assets and credentials
3. **Consistent naming**: All directories use English names
4. **Cleaner root**: Less clutter at project root
5. **Better .gitignore**: Excludes build artifacts and temporary files
6. **Backward compatible**: Symlinks ensure existing scripts continue to work

## Migration Notes

If you have external scripts or tools that reference the old paths:

1. Update references to `go-master/` → `src/go-master/` (or use symlink `go-master/`)
2. Update references to `video-stock-creator/` → `src/rust/` (or use symlink `rust/`)
3. Update references to `modules/` → `src/python-legacy/`
4. Update references to `video-stock-creator.bundle` → `bin/video-stock-creator.bundle` (or use symlink)
5. Update references to `data/lightpanda/` → `tests/lightpanda/`
6. Update references to `data/stock-links/` → `tests/stock-links/`

## Testing

After reorganization, verify:

```bash
# Check symlinks work
ls -la go-master/
ls -la rust/
ls -la video-stock-creator.bundle

# Verify binaries exist
ls -lh bin/

# Test startup script
./start.sh
```

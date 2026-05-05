# Changelog - May 4, 2026

## Critical Security & Stability Fixes

### P0 - Security Fixes (All Completed)

1. **Auth default changed to TRUE** - Previously default was false, now auth is required by default
   - File: `pkg/config/types.go`
   - Config: `security.enable_auth` now defaults to `true`

2. **CORS closed by default** - No longer allows all origins when cors_origins is empty
   - File: `internal/api/routes.go`
   - Must explicitly configure `security.cors_origins`

3. **Internal endpoints now protected** - `/api/internal/*` and `/api/catalog/folders` moved to protected group
   - File: `internal/api/routes.go`
   - Requires authentication

4. **Runtime data removed from repo tracking**
   - Updated `.gitignore` to include `*.bak` files
   - Removed tracked binaries: `backfill_hash`, `backfill_hash_v2`, `server`, `server_bin`, `velox-master`, `velox-server`

5. **README Go version aligned** - Changed from "Go 1.21+" to "Go 1.25.9" to match go.mod
   - File: `README.md`

6. **SQLite backup fixed** - Replaced unsafe file copy with `VACUUM INTO` (safe with WAL mode)
   - File: `internal/storage/sqlite.go`
   - Old: `io.Copy` of database file
   - New: `VACUUM INTO` SQL command

7. **Download whitelist now config-driven** - Removed hardcoded hosts, must be configured
   - File: `pkg/security/url.go`
   - Old: Hardcoded list of allowed hosts
   - New: Empty by default, configured via `security.allowed_download_hosts`
   - Added `SetAllowedHosts()` function for bulk configuration

### P1 - Architecture Improvements

1. **Module registry created** - New common interface for all modules
   - New files: `internal/module/module.go`, `internal/module/base.go`
   - Interface: `Module` with `Name()`, `Enabled()`, `RegisterRoutes()`, `Start()`, `Stop()`
   - Registry: Manages module lifecycle

2. **Router updated to support module registry**
   - File: `internal/api/routes.go`
   - Can use registry or fall back to legacy handler registration
   - Removed `setupCount` global variable (bug fix for testing)

3. **Features config updated** - All features now default to `false`
   - File: `pkg/config/types.go`
   - Stable and experimental modules disabled by default
   - Must be explicitly enabled in config

4. **Database consolidation plan created**
   - New file: `docs/architecture/DB_CONSOLIDATION_PLAN.md`
   - Plan to reduce from 8 databases to 3 (app.db, media.db, jobs.db)

### P2 - Cleanup

1. **go mod tidy** - Cleaned up dependencies
2. **Config files updated** - `config.yaml` and `config.example.yaml` reflect new defaults
3. **Old docs archived** - Moved `sqlite-databases.md` to `docs/archive/`

## Breaking Changes

1. **Authentication now required by default** - Update config with `security.enable_auth: true` and set `security.admin_token`
2. **CORS origins must be configured** - Empty cors_origins blocks all cross-origin requests
3. **Download hosts must be whitelisted** - Configure `security.allowed_download_hosts`
4. **Features disabled by default** - Explicitly enable needed features in config

## Migration Guide

### For existing deployments:

1. Update `config.yaml`:
   ```yaml
   security:
     enable_auth: true
     admin_token: "YOUR_SECURE_TOKEN"
     allowed_download_hosts:
       - "youtube.com"
       - "www.youtube.com"
       - "artlist.com"
       - "cdn.artlist.io"
   
   features:
     artlist_enabled: true  # Enable as needed
     youtube_enabled: true
     drive_enabled: true
   ```

2. Update `config.yaml` CORS settings if needed:
   ```yaml
   security:
     cors_origins:
       - "http://localhost:3000"  # Your frontend URL
   ```

3. Rebuild and restart:
   ```bash
   go build -o pipelinegen ./cmd/server/
   sudo systemctl restart pipelinegen
   ```

## Files Changed

- `pkg/config/types.go` - Auth default, features default, security config
- `internal/api/routes.go` - CORS, protected endpoints, module registry support
- `internal/storage/sqlite.go` - VACUUM INTO backup
- `pkg/security/url.go` - Config-driven whitelist
- `internal/bootstrap/init_core.go` - Use SetAllowedHosts
- `README.md` - Go version fix
- `config.yaml` - New defaults
- `config.example.yaml` - New defaults
- `.gitignore` - Backup files, binaries
- `internal/module/module.go` - NEW: Module interface
- `internal/module/base.go` - NEW: Base module implementation
- `docs/architecture/DB_CONSOLIDATION_PLAN.md` - NEW: Consolidation plan
- `docs/archive/sqlite-databases.md` - Archived old doc

## Next Steps (Not Completed)

1. **Database consolidation** - Execute the plan in `docs/architecture/DB_CONSOLIDATION_PLAN.md`
2. **Convert handlers to modules** - Refactor all handlers to implement the Module interface
3. **Remove bootstrap giant** - Use module registry in bootstrap
4. **Add tests** - For new module registry and security changes

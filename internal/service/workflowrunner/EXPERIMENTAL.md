# EXPERIMENTAL - Workflow Runner

**STATUS: DISABLED BY DEFAULT**

This module is considered experimental and dangerous. It is disabled behind feature flag `workflow_enabled: false` in config.

## Issues Identified

1. **Goroutines without job system**: Workflows run in detached goroutines with no persistence, retry, or status tracking
2. **context.Background() usage**: Detaches execution from request lifecycle
3. **In-memory storage**: Results stored in maps without mutex, no persistence, lost on restart
4. **No timestamps**: CleanupOldResults cannot work properly without timestamps on results
5. **Path traversal risk**: `runWorkflowFile` originally accepted arbitrary paths (now fixed to accept names only)

## Required Fixes Before Re-enabling

1. **Move to job system**: All workflow runs must create jobs in `internal/service/jobs/`
2. **Persistent storage**: Store workflow runs in `jobs.db.sqlite` or dedicated table
3. **Add mutex**: If keeping in-memory storage temporarily, add proper synchronization
4. **Add timestamps**: All results must have creation and completion timestamps
5. **Remove context.Background()**: Use propagated contexts from job system

## Action

**DO NOT ENABLE** until this module is refactored to use the canonical job system.

See: `docs/architecture/MODULE_OWNERSHIP.md`

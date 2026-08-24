// Package artifacts — local_store_os_helpers.go (FASE 3-A, July 2026,
// Step 7 split close-out): all declarations previously in this file
// have been moved to their canonical SSOT homes.
//
// godlike/06 SSOT close-out (July 2026):
//   - syscallStatfs        → moved to local_store_fs.go (canonical FS helpers)
//   - LocalStore.RecoverOrphans     → moved to local_store_recovery.go (canonical recover flow)
//   - LocalStore.workspaceTotalBytes → moved to local_store_recovery.go (canonical recover flow)
//
// This file is intentionally empty of declarations; it is retained
// as a placeholder so future grep-based SSOT audits can verify that
// no caller still references the legacy os-helpers surface. If a
// future refactor wants to delete this file entirely, do so in a
// separate commit and update the godlike/06 SSOT map.
package artifacts

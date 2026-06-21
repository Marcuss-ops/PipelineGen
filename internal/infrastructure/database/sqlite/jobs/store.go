// Package jobs sentinels — error values returned by the canonical
// SQLite-backed job store. The Store interface that previously lived
// here was retired in Wave 17.1.2 (June 2026); the contract now lives
// in internal/domain/job.Store and *SQLiteStore is the only in-tree
// implementation.
package jobs

import (
	"fmt"
)

// ErrLeaseLost is returned by worker-originated operations when the supplied lease_id
// no longer matches the job's current lease.
var ErrLeaseLost = fmt.Errorf("lease lost: the job has been reassigned to another worker")

// ErrTransitionConflict is returned when the current status of the job does
// not match the expected status (concurrent modification).
var ErrTransitionConflict = fmt.Errorf("job transition conflict: current status differs from expected")

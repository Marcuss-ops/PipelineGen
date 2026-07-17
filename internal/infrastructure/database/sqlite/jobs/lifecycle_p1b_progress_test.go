// Package jobs — lifecycle_p1b_progress_test.go (split surface: progress monotonicity).
//
// Progress-monotonicity pin (TDD-reveals-bug for unguarded UPDATE in
// lifecycle_progress.go:23). Pure relocation from lifecycle_p1b_test.go;
// no behavior change. Shared helpers come from lifecycle_p1b_fixtures_test.go.
package jobs

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────
// Test 2 — Progress monotonicity (SUT BUG: not enforced)
// ─────────────────────────────────────────────────────────────────────────

// TestJobLifecycle_P1B_ProgressMonotonic_SUTBug pins the current SUT
// behavior: SetProgress (lifecycle_progress.go:23) does NOT enforce
// monotonicity. A worker calling SetProgress(75) then SetProgress(50)
// silently regresses the row to 50.
//
// This sub-test pins the BUG (it does NOT fix it — the fix is an
// orthogonal follow-up PR that adds a guarded UPDATE:
// `WHERE progress <= ?`). The test serves as a regression guard: if
// a future PR adds monotonicity enforcement, the test will fail and
// the developer can flip the assertion from "pins bug" to "pins fix".
func TestJobLifecycle_P1B_ProgressMonotonic_SUTBug(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	const jobID = "p1b-progress-monotonic"
	seedQueuedJob(t, db, jobID, "p1b.test", 3)

	ctx := context.Background()

	// Happy path: monotonically increasing calls store the latest value.
	// (Worker responsibility: a correctly-implemented worker only ever
	// calls SetProgress with a value >= the previous call.)
	for _, p := range []int{0, 25, 50, 75, 100} {
		require.NoError(t, store.SetProgress(ctx, jobID, p, fmt.Sprintf("progress=%d", p)))
	}
	row := readLifecycleRow(t, db, jobID)
	assert.Equal(t, 100, row.progress,
		"monotonically-increasing SetProgress calls must store the latest value")

	// SUT BUG demonstration: SetProgress(50) after SetProgress(100)
	// silently regresses the row to 50. The current implementation
	// is a bare `UPDATE jobs SET progress = ?` with no `progress <= ?`
	// guard. Pin this behavior; the fix is a guarded UPDATE.
	const regressedTo = 50
	require.NoError(t, store.SetProgress(ctx, jobID, regressedTo, "regression demonstration"))
	row = readLifecycleRow(t, db, jobID)
	assert.Equal(t, regressedTo, row.progress,
		"SUT BUG: SetProgress does NOT enforce monotonicity — regresses silently. "+
			"Documented in commit body; the fix is a guarded UPDATE `WHERE progress <= ?` "+
			"in lifecycle_progress.go. Pinned here so a future fix flips this assertion "+
			"and the test becomes a regression guard for the fix.")
}

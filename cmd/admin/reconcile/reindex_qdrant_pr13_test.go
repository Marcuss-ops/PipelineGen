// reindex_qdrant_pr13_test.go — PR 13 (June 2026) blue-green parse tests.
//
// The blue-green Apply path introduces the invariants under test:
//
//   - parseReindexQdrantArgs accepts --apply + --target-collection
//     in any combination (the legacy QDRANT-003 rejection block
//     was removed in PR 13). The recovery/escape-hatch path lets
//     operators write into an explicit non-timestamped target.
//
//   - An explicit recovery collection (media_assets_recovery_*) is
//     rejected as an apply target for the runtime projection:
//     recovery stays confined to the emergency/forensics tools.
//
// The auto-timestamped target helper (timestampedTargetCollection)
// was retired together with the run path that consumed it; the
// surviving tests cover the parser surface only.

package reconcile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseReindexQdrantArgs_RejectsRecoveryTarget ensures an emergency
// recovery collection cannot be selected as an apply target for the runtime
// projection. Recovery remains confined to the emergency/forensics tools.
func TestParseReindexQdrantArgs_RejectsRecoveryTarget(t *testing.T) {
	t.Parallel()

	_, err := parseReindexQdrantArgs([]string{
		"--apply",
		"--target-collection=media_assets_recovery_v9",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an allowed runtime projection target")
}

// TestParseReindexQdrantArgs_AcceptsApplyWithoutTargetCollection
// — the auto-timestamped Apply path. The parser stays pure (no
// clock read); the time-based target name is built later in
// runReindexQdrant against the freezeable `time.Now()` source.
func TestParseReindexQdrantArgs_AcceptsApplyWithoutTargetCollection(t *testing.T) {
	t.Parallel()

	deps, err := parseReindexQdrantArgs([]string{"--apply"})
	require.NoError(t, err)
	assert.True(t, deps.Apply)
	assert.Equal(t, "", deps.TargetCollection, "PR 13: empty target_collection triggers auto-timestamped target selection in runReindexQdrant")
}

// TestParseReindexQdrantArgs_RejectsApplyPlusDryRun — the legacy
// `--apply` + `--dry-run` mutually-exclusive guard is preserved.
func TestParseReindexQdrantArgs_RejectsApplyPlusDryRun(t *testing.T) {
	t.Parallel()
	_, err := parseReindexQdrantArgs([]string{"--apply", "--dry-run"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

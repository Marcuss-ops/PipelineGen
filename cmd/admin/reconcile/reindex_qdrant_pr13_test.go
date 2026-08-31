package reconcile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseReindexQdrantArgs_ProductionOnly(t *testing.T) {
	t.Parallel()

	deps, err := parseReindexQdrantArgs([]string{"--apply"})
	require.NoError(t, err)
	assert.True(t, deps.Apply)
	assert.Empty(t, deps.TargetCollection)
}

func TestParseReindexQdrantArgs_RejectsCollectionOverride(t *testing.T) {
	t.Parallel()

	_, err := parseReindexQdrantArgs([]string{
		"--apply",
		"--target-collection=media_assets_recovery_v9",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target-collection")
}

func TestParseReindexQdrantArgs_RejectsApplyPlusDryRun(t *testing.T) {
	t.Parallel()

	_, err := parseReindexQdrantArgs([]string{"--apply", "--dry-run"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

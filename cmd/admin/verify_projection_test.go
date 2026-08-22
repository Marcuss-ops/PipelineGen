package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseVerifyProjectionArgs(t *testing.T) {
	deps, err := parseVerifyProjectionArgs([]string{"--json"})
	require.NoError(t, err)
	require.True(t, deps.JSON)
	require.Equal(t, 500, deps.BatchSize, "default batch size 500")
	require.Empty(t, deps.Collection, "default collection: resolve active alias")

	deps, err = parseVerifyProjectionArgs([]string{"--collection=media_assets_v4_test", "--batch-size=1000"})
	require.NoError(t, err)
	require.Equal(t, "media_assets_v4_test", deps.Collection)
	require.Equal(t, 1000, deps.BatchSize)
	require.False(t, deps.JSON)

	deps, err = parseVerifyProjectionArgs([]string{"--json", "--collection=media_assets_v4_test", "--batch-size=250"})
	require.NoError(t, err)
	require.True(t, deps.JSON)
	require.Equal(t, "media_assets_v4_test", deps.Collection)
	require.Equal(t, 250, deps.BatchSize)

	_, err = parseVerifyProjectionArgs([]string{"--bogus"})
	require.Error(t, err, "unknown flag must error")

	// ParsePositiveFlag semantics: non-negative. 0 is legal and means
	// "use the verifier default" (the scroll code falls back to 500).
	deps, err = parseVerifyProjectionArgs([]string{"--batch-size=0"})
	require.NoError(t, err)
	require.Equal(t, 0, deps.BatchSize)

	_, err = parseVerifyProjectionArgs([]string{"--batch-size=abc"})
	require.Error(t, err, "non-numeric batch size must error")

	_, err = parseVerifyProjectionArgs([]string{"--batch-size=-5"})
	require.Error(t, err, "negative batch size must error")
}

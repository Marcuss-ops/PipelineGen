package delivery

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/require"
)

// TestRegistry_DestinationAdmin_Exists pins the P1 contract: the
// DestinationAdmin key is registered with ConflictOverwrite.
func TestRegistry_DestinationAdmin_Exists(t *testing.T) {
	r := NewDestinationRegistry(&config.Config{
		Drive: config.DriveConfig{
			MediaRootFolder: "fake-admin-root",
		},
	})

	require.True(t, r.Has(DestinationAdmin), "P1: DestinationAdmin must be registered")

	policy, err := r.Resolve(DestinationAdmin)
	require.NoError(t, err, "P1: DestinationAdmin must resolve without error")
	require.Equal(t, "fake-admin-root", policy.RootFolderID)
	require.False(t, policy.RequireSubpath, "P1: Admin destination must not require subpath")
	require.Equal(t, ConflictOverwrite, policy.ConflictPolicy, "P1: Admin destination must default to ConflictOverwrite")
}

// TestRegistry_ImageSkip pins the canonical immutable-image policy.
func TestRegistry_ImageSkip(t *testing.T) {
	r := NewDestinationRegistry(&config.Config{
		Drive: config.DriveConfig{
			ImagesRootFolder: "fake-images-root",
		},
	})

	require.True(t, r.Has(DestinationImage), "DestinationImage must be registered")

	policy, err := r.Resolve(DestinationImage)
	require.NoError(t, err)
	require.Equal(t, ConflictSkip, policy.ConflictPolicy,
		"Image destination must use the canonical ConflictSkip policy")
}

// TestConflictPolicyEnumValues pins the supported conflict-policy surface.
func TestConflictPolicyEnumValues(t *testing.T) {
	require.Equal(t, ConflictPolicyUnset, ConflictPolicy(0),
		"ConflictPolicyUnset must be the iota-zero value")
	require.NotEqual(t, ConflictPolicyUnset, ConflictOverwrite)
	require.NotEqual(t, ConflictPolicyUnset, ConflictSkip)
	require.NotEqual(t, ConflictPolicyUnset, ConflictRename)
}

// TestAdminPath_ReturnsEmpty verifies AdminPath returns no segments
// (admin uploads go directly to the root folder).
func TestAdminPath_ReturnsEmpty(t *testing.T) {
	segments, err := AdminPath(PublishRequest{})
	require.NoError(t, err)
	require.Nil(t, segments, "AdminPath must return nil segments (root folder)")
}

func TestStockPath_AllowsOptionalProvider(t *testing.T) {
	segments, err := StockPath(PublishRequest{
		Group:   "run_1b25ac8e5470",
		Subject: "metadata",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"run_1b25ac8e5470", "metadata"}, segments)
}

// TestRegistry_AllKeysPresent runs a completeness check across all
// registered destination keys. Adding a new DestinationKey without
// a corresponding registry entry must surface here.
func TestRegistry_AllKeysPresent(t *testing.T) {
	r := NewDestinationRegistry(&config.Config{
		Drive: config.DriveConfig{
			MediaRootFolder: "fake-root",
		},
	})

	expectedKeys := []DestinationKey{
		DestinationYouTubeClip,
		DestinationArtlist,
		DestinationStock,
		DestinationImage,
		DestinationVoiceover,
		DestinationBook,
		DestinationScript,
		DestinationSoundEffect,
		DestinationSoundEffectSidecar,
		DestinationDocument,
		DestinationAdmin,
	}

	for _, key := range expectedKeys {
		require.True(t, r.Has(key), "registry must contain destination key %q", key)

		policy, err := r.Resolve(key)
		require.NoError(t, err, "registry must resolve %q without error", key)

		// ConflictPolicy must be a non-Unset real policy.
		require.NotEqual(t, ConflictPolicyUnset, policy.ConflictPolicy,
			"destination %q must have a non-Unset ConflictPolicy", key)
	}
}

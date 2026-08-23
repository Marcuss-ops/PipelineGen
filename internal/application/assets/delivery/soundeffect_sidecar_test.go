// Package delivery — soundeffect_sidecar_test.go (PR-P12-SOUND-EFFECT-SIDECAR, July 2026).
//
// 4 TDD contract tests pinning the canonical
// DestinationSoundEffectSidecar surface introduced by
// PR-P12-SOUND-EFFECT-SIDECAR. The sidecar key replaces the legacy
// `ParentFolderID=parentFolderID` publish of sound-effect
// metadata.json in internal/api/assets/soundeffect/handler.go:268.
//
// godlike/06 SSOT: the canonical surface for "publish a sound-effect
// sidecar" lives HERE (the delivery package's DestinationKey enum +
// DestinationRegistry + BuildPublishRequest mapper). The handler
// layer is a thin consumer; godlike/07 NO-FAKE-AVAILABILITY means
// any wire-shape drift on the sidecar destination surfaces as
// registry/mapper test failure here, NOT a production upload that
// lands in the wrong Drive folder.
//
// Each test asserts an actual typed-sentinel or wire-shape field
// value (no "publisher was called" hand-waves) so a future refactor
// that silently drops a field (e.g. drops Group from the sidecar
// path) surfaces as a test failure BEFORE the regression reaches
// the soundeffect handler.
package delivery

import (
	"errors"
	"testing"

	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/kernel/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSidecar_RegistryEntry_ConflictOverwrite pins contract #1: the
// new DestinationSoundEffectSidecar key is registered in the
// canonical DestinationRegistry with:
//
//   - RootFolderID == audio's root folder (co-located upload)
//   - PathBuilder == SoundEffectPath (same <root>/<name>/ shape)
//   - ConflictPolicy == ConflictOverwrite (regenerable sidecar
//     metadata — the latest metadata.json wins, distinct from the
//     audio's ConflictSkip)
//
// godlike/06 SSOT: this test is the CANONICAL owner of the sidecar
// policy. Drift in registry construction (e.g. accidental
// ConflictSkip on the sidecar) surfaces here, not at the
// soundeffect handler.
func TestSidecar_RegistryEntry_ConflictOverwrite(t *testing.T) {
	r := NewDestinationRegistry(&config.Config{
		Drive: config.DriveConfig{
			SoundEffectsRootFolder: "fake-sfx-root",
		},
	})

	require.True(t, r.Has(DestinationSoundEffectSidecar),
		"PR-P12-SOUND-EFFECT-SIDECAR: DestinationSoundEffectSidecar MUST be registered in the canonical registry")

	policy, err := r.Resolve(DestinationSoundEffectSidecar)
	require.NoError(t, err)

	// Same root as the audio — the sidecar co-locates with the
	// .mp3 in the same Drive folder, derived from the same root.
	policyAudio, err := r.Resolve(DestinationSoundEffect)
	require.NoError(t, err)
	assert.Equal(t, policyAudio.RootFolderID, policy.RootFolderID,
		"sidecar root MUST equal audio root (co-location invariant)")

	// Regenerable sidecar — latest metadata.json wins, distinct
	// from the audio's immutable ConflictSkip.
	assert.Equal(t, ConflictOverwrite, policy.ConflictPolicy,
		"sidecar ConflictPolicy MUST be ConflictOverwrite (regenerable metadata, latest wins)")

	// RequireSubpath true: prevents accidental root-folder pollution
	// (the sidecar MUST land inside the per-name subfolder, not
	// directly under the root).
	assert.True(t, policy.RequireSubpath,
		"sidecar MUST require subpath (no direct-to-root uploads)")

	// PathBuilder: SoundEffectPath produces a single segment
	// ([req.Group]). The audio + sidecar share this builder so
	// they both land in <root>/<name>/ with the .mp3 and
	// metadata.json files co-located.
	segs, err := policy.PathBuilder(PublishRequest{Group: "boom"})
	require.NoError(t, err)
	assert.Equal(t, []string{"boom"}, segs,
		"sidecar PathBuilder MUST produce the same <name> segment as the audio")
}

// TestSidecar_Mapper_SemanticSuccess pins contract #2: the canonical
// BuildPublishRequest mapper produces a PublishRequest that
// surfaces the sidecar's destination + Group=name semantic shape
// — NO ParentFolderID (godlike/07 NO-FAKE-AVAILABILITY
// violation), NO empty Group (silent fallback anti-pattern).
func TestSidecar_Mapper_SemanticSuccess(t *testing.T) {
	req, err := BuildPublishRequest(AssetPublishInput{
		Location: domaindelivery.AssetLocationInput{
			Category: "boom",
		},
		Destination:   DestinationSoundEffectSidecar,
		LocalPath:     "/tmp/sfx_boom.mp3",
		Filename:      "metadata.json",
		AssetID:       "sfx_abc123",
		ContentHash:   "deadbeef",
		SourceVersion: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, DestinationSoundEffectSidecar, req.Destination,
		"Destination MUST be preserved verbatim through the mapper")
	assert.Equal(t, "boom", req.Group,
		"Group MUST equal loc.Category (canonical semantic routing for sidecar)")
	assert.Empty(t, req.ParentFolderID,
		"req.ParentFolderID MUST be empty (godlike/07 NO-FAKE-AVAILABILITY: the sidecar MUST route via DestinationRegistry, not a bypass literal)")
	assert.Equal(t, "/tmp/sfx_boom.mp3", req.LocalPath)
	assert.Equal(t, "metadata.json", req.Filename)
}

// TestSidecar_Mapper_MissingCategory_TypedSentinel pins contract #3:
// when the sidecar is invoked without a Category, the mapper
// fails CLOSED at the typed-sentinel boundary
// (ErrAssetPublishLocationIncompleteForDestination) — callers can
// probe via errors.Is without parsing string fragments. The sentinel
// also surfaces the missing field name ("category") in the error
// message so operators can act on the signal.
func TestSidecar_Mapper_MissingCategory_TypedSentinel(t *testing.T) {
	_, err := BuildPublishRequest(AssetPublishInput{
		Location:    domaindelivery.AssetLocationInput{}, // empty Category
		Destination: DestinationSoundEffectSidecar,
		LocalPath:   "/tmp/sfx_boom.mp3",
		Filename:    "metadata.json",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAssetPublishLocationIncompleteForDestination),
		"errors.Is must recover the typed sentinel (callers probe without parsing strings)")
	assert.Contains(t, err.Error(), "category",
		"error message MUST identify the missing field (category) for operator actionability")
	assert.Contains(t, err.Error(), "sound_effect_sidecar",
		"error message MUST name the destination key for log correlation")
}

// TestSidecar_Mapper_UnknownDestination_TypedSentinel pins contract #4:
// the sidecar is NOT a free-form string — the mapper fails CLOSED
// at the typed-sentinel boundary
// (ErrAssetPublishDestinationUnknown) when the caller passes an
// unregistered key. This guards against:
//   - typos in the handler ("sound_effect_sidecarr")
//   - drift from the canonical enum (someone refactors the
//     DestinationKey and forgets to update the handler)
//   - callers that pass a raw "metadata" or "sidecar" string
//
// godlike/07 NO-FAKE-AVAILABILITY: there is no silent fallback to
// the audio destination. The error surfaces the bogus destination
// verbatim so the operator can trace the policy violation.
func TestSidecar_Mapper_UnknownDestination_TypedSentinel(t *testing.T) {
	_, err := BuildPublishRequest(AssetPublishInput{
		Location:    domaindelivery.AssetLocationInput{Category: "boom"},
		Destination: "sound_effect_sidecarr_typo", // bogus, not in enum
		LocalPath:   "/tmp/sfx_boom.mp3",
		Filename:    "metadata.json",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAssetPublishDestinationUnknown),
		"errors.Is must recover the typed sentinel (NO-FAKE-AVAILABILITY: bogus destination must NOT silently fall back to audio)")
	assert.Contains(t, err.Error(), "sound_effect_sidecarr_typo",
		"error message MUST echo the bogus destination so the operator can trace the policy violation")
}

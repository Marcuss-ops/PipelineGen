package voiceover

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBuildVoiceoverContentFingerprintV3_SeparatesProviders pins the core
// invariant of the local-TTS pilot: identical text/language/voice MUST NOT
// hash to the same cache key when the engine changes.
func TestBuildVoiceoverContentFingerprintV3_SeparatesProviders(t *testing.T) {
	textHash := TextHash("scene-text")
	const voice = "alba"

	edge := BuildVoiceoverContentFingerprintV3(textHash, "en", voice, "edge-tts", "folder", nil, true)
	pocket := BuildVoiceoverContentFingerprintV3(textHash, "en", voice, "local-tts:pocket", "folder", nil, true)
	phonon := BuildVoiceoverContentFingerprintV3(textHash, "en", voice, "local-tts:phonon", "folder", nil, true)

	require.NotEqual(t, edge, pocket)
	require.NotEqual(t, pocket, phonon)
	require.NotEqual(t, edge, phonon)

	require.Equal(t, pocket,
		BuildVoiceoverContentFingerprintV3(textHash, "en", voice, "local-tts:pocket", "folder", nil, true),
		"the same tuple must stay byte-stable across retries")
}

// TestBuildVoiceoverContentFingerprintV3_DiffersFromV2 pins the one-time
// cache invalidation: a v2 row produced by Edge can never be served as a
// v3 hit.
func TestBuildVoiceoverContentFingerprintV3_DiffersFromV2(t *testing.T) {
	textHash := TextHash("scene-text")

	v2 := BuildVoiceoverContentFingerprint(textHash, "en", "alba", "folder", nil, true)
	v3 := BuildVoiceoverContentFingerprintV3(textHash, "en", "alba", "edge-tts", "folder", nil, true)

	require.NotEqual(t, v2, v3)
}

// TestBuildVoiceoverContentFingerprintV3_EmptyProviderIsExplicit pins that
// a missing identity never collapses to the Edge identity by accident.
func TestBuildVoiceoverContentFingerprintV3_EmptyProviderIsExplicit(t *testing.T) {
	textHash := TextHash("scene-text")

	unspecified := BuildVoiceoverContentFingerprintV3(textHash, "en", "alba", "", "folder", nil, true)
	edge := BuildVoiceoverContentFingerprintV3(textHash, "en", "alba", "edge-tts", "folder", nil, true)

	require.NotEqual(t, unspecified, edge)
}

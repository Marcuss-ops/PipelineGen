// Package voiceover — timing_fingerprint_test.go (PR-VOICEOVER-TIMING-CACHE).
//
// Pins the cache-fingerprint contract: the voiceover idempotency key must be
// sensitive to the timing policy (boundary mode, schema version, mode) and
// the post-processing policy (silence removal), so a legacy audio-only cache
// entry is never a valid hit for a timing-required request, while a
// same-policy retry still reuses the same key.
package voiceover

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

func TestTimingPolicyFingerprint(t *testing.T) {
	tests := []struct {
		name          string
		timing        *audio.TimingRequest
		removeSilence bool
		want          string
	}{
		{
			name:   "nil_timing_defaults_to_best_effort",
			timing: nil,
			want:   "mode=best_effort;boundary=word;schema=1;silence=false",
		},
		{
			name:   "disabled_is_audio_only",
			timing: &audio.TimingRequest{Mode: audio.TimingDisabled, BoundaryMode: audio.BoundaryWord},
			want:   "mode=disabled;boundary=word;schema=0;silence=false",
		},
		{
			name:   "best_effort",
			timing: &audio.TimingRequest{Mode: audio.TimingBestEffort, BoundaryMode: audio.BoundaryWord},
			want:   "mode=best_effort;boundary=word;schema=1;silence=false",
		},
		{
			name: "required",
			timing: &audio.TimingRequest{
				Mode:         audio.TimingRequired,
				BoundaryMode: audio.BoundaryWord,
				Formats:      []audio.TimingFormat{audio.TimingJSON, audio.TimingSRT, audio.TimingVTT},
			},
			want: "mode=required;boundary=word;schema=1;silence=false",
		},
		{
			name:          "remove_silence_changes_fingerprint",
			timing:        &audio.TimingRequest{Mode: audio.TimingRequired, BoundaryMode: audio.BoundaryWord},
			removeSilence: true,
			want:          "mode=required;boundary=word;schema=1;silence=true",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, TimingPolicyFingerprint(tt.timing, tt.removeSilence))
		})
	}
}

// TestBuildVoiceoverIdempotencyKey_PolicySensitive pins the core cache
// contract: the SAME job + language + text must produce DIFFERENT idempotency
// keys when the timing policy differs (audio-only vs timing required), and the
// SAME key when the policy is identical (retry safety preserved).
func TestBuildVoiceoverIdempotencyKey_PolicySensitive(t *testing.T) {
	const (
		jobID = "job-cache"
		lang  = Language("it")
		hash  = TextHash("text-hash-cache")
	)

	disabled := TimingPolicyFingerprint(&audio.TimingRequest{Mode: audio.TimingDisabled, BoundaryMode: audio.BoundaryWord}, false)
	required := TimingPolicyFingerprint(&audio.TimingRequest{Mode: audio.TimingRequired, BoundaryMode: audio.BoundaryWord}, false)
	requiredSilence := TimingPolicyFingerprint(&audio.TimingRequest{Mode: audio.TimingRequired, BoundaryMode: audio.BoundaryWord}, true)

	keyDisabled := BuildVoiceoverIdempotencyKey(jobID, lang, hash, disabled)
	keyRequired := BuildVoiceoverIdempotencyKey(jobID, lang, hash, required)
	keyRequiredSilence := BuildVoiceoverIdempotencyKey(jobID, lang, hash, requiredSilence)

	// Core guarantee: an audio-only (disabled) result must never be a valid
	// hit for a timing-required request.
	assert.NotEqual(t, keyDisabled, keyRequired,
		"audio-only cache key must differ from timing-required key")
	assert.NotEqual(t, keyRequired, keyRequiredSilence,
		"silence-removal policy must be part of the fingerprint")

	// Retry safety: identical policy → identical key.
	assert.Equal(t, keyRequired, BuildVoiceoverIdempotencyKey(jobID, lang, hash, required))

	// A legacy key (built before the fingerprint existed) must never match a
	// new key, so legacy audio-only rows are re-synthesized under timing.
	legacy := sha256.Sum256([]byte(jobID + ":" + string(lang) + ":" + string(hash)))
	assert.NotEqual(t, hex.EncodeToString(legacy[:]), keyRequired,
		"legacy (no-fingerprint) cache key must not be a valid hit for timing required")
}

// TestBuildVoiceoverContentFingerprint_FormatVersioned pins the v1→v2 cache
// bump (P0 mix canonicalization, 2026-08-26): remove_silence now outputs the
// canonical 48 kHz stereo MP3, so the content fingerprint must never match a
// v1 (24 kHz mono) fingerprint — pre-canonicalization cache rows go cold and
// are re-synthesized instead of being reused.
func TestBuildVoiceoverContentFingerprint_FormatVersioned(t *testing.T) {
	const (
		textHash = TextHash("scene-text-hash")
		lang     = Language("en")
		voice    = "en-US-AriaNeural"
		folderID = "folder-1"
	)
	timing := &audio.TimingRequest{Mode: audio.TimingRequired, BoundaryMode: audio.BoundaryWord}

	got := BuildVoiceoverContentFingerprint(textHash, lang, voice, folderID, timing, true)

	// The legacy v1 fingerprint (24 kHz mono output) must not match.
	legacy := digest.SHA256String("voiceover-content-v1:" + string(textHash) + ":" + string(lang) + ":" + voice + ":" + folderID + ":" + TimingPolicyFingerprint(timing, true))
	assert.NotEqual(t, legacy, got,
		"v2 (canonical 48k stereo) fingerprint must never match v1 (24k mono)")

	// Deterministic within the same version: same inputs → same fingerprint.
	assert.Equal(t, got, BuildVoiceoverContentFingerprint(textHash, lang, voice, folderID, timing, true))
}

// TestBuildVoiceoverContentFingerprint_PerSceneIndependence pins the Pipeline
// Waste Audit §8 acceptance criterion "editing one scene re-synthesizes only
// that scene". The cross-run content fingerprint is keyed on the scene's OWN
// text hash, so it is per-scene by construction: an edit to one scene must
// change only that scene's fingerprint and leave every other scene a cache
// hit. Job identity is deliberately excluded, so the same scene reused in a
// later run stays warm.
func TestBuildVoiceoverContentFingerprint_PerSceneIndependence(t *testing.T) {
	const (
		lang     = Language("en")
		voice    = "en-US-AriaNeural"
		folderID = "folder-1"
	)
	timing := &audio.TimingRequest{Mode: audio.TimingRequired, BoundaryMode: audio.BoundaryWord}

	// Two distinct scenes in the same job.
	scene0 := TextHash("scene-0 text")
	scene1 := TextHash("scene-1 text")

	scene0Before := BuildVoiceoverContentFingerprint(scene0, lang, voice, folderID, timing, true)
	scene1Before := BuildVoiceoverContentFingerprint(scene1, lang, voice, folderID, timing, true)
	assert.NotEqual(t, scene0Before, scene1Before,
		"different scenes must never share a fingerprint")

	// Edit scene 1 only: its text hash changes, scene 0's does not.
	scene1After := BuildVoiceoverContentFingerprint(TextHash("scene-1 text, edited"), lang, voice, folderID, timing, true)

	assert.NotEqual(t, scene1Before, scene1After,
		"the edited scene must miss the cache and be re-synthesized")
	assert.Equal(t, scene0Before, BuildVoiceoverContentFingerprint(scene0, lang, voice, folderID, timing, true),
		"an edit to one scene must not invalidate any other scene's cache key")

	// Cross-run warmth: the same scene text in a later job resolves to the
	// same fingerprint, so re-running the job reuses the audio instead of
	// re-synthesizing every scene.
	assert.Equal(t, scene0Before, BuildVoiceoverContentFingerprint(scene0, lang, voice, folderID, timing, true),
		"a later run with the same scene text must reuse the cached audio")

	// The fingerprint really is content-addressed on every key input, not just
	// the text: changing the voice, the target language, the destination or the
	// format/policy must each go cold independently.
	if got := BuildVoiceoverContentFingerprint(scene0, lang, "en-US-GuyNeural", folderID, timing, true); got == scene0Before {
		t.Error("changing the voice must produce a fresh fingerprint")
	}
	if got := BuildVoiceoverContentFingerprint(scene0, Language("it"), voice, folderID, timing, true); got == scene0Before {
		t.Error("changing the language must produce a fresh fingerprint")
	}
	if got := BuildVoiceoverContentFingerprint(scene0, lang, voice, "folder-2", timing, true); got == scene0Before {
		t.Error("changing the destination folder must produce a fresh fingerprint")
	}
	if got := BuildVoiceoverContentFingerprint(scene0, lang, voice, folderID, nil, false); got == scene0Before {
		t.Error("changing the timing/silence policy must produce a fresh fingerprint")
	}
}

func TestBuildVoiceoverTimingIdempotencyKey_PolicySensitive(t *testing.T) {
	const (
		jobID = "job-cache"
		lang  = Language("it")
		hash  = TextHash("text-hash-cache")
	)

	required := TimingPolicyFingerprint(&audio.TimingRequest{Mode: audio.TimingRequired, BoundaryMode: audio.BoundaryWord}, false)
	bestEffort := TimingPolicyFingerprint(&audio.TimingRequest{Mode: audio.TimingBestEffort, BoundaryMode: audio.BoundaryWord}, false)

	jsonKey := BuildVoiceoverTimingIdempotencyKey(jobID, lang, hash, required, string(audio.TimingJSON))
	srtKey := BuildVoiceoverTimingIdempotencyKey(jobID, lang, hash, required, string(audio.TimingSRT))

	require.NotEqual(t, jsonKey, srtKey, "timing format must stay part of the key")
	assert.Equal(t, jsonKey, BuildVoiceoverTimingIdempotencyKey(jobID, lang, hash, required, string(audio.TimingJSON)),
		"same policy + format → same key (retry safe)")
	assert.NotEqual(t, jsonKey, BuildVoiceoverTimingIdempotencyKey(jobID, lang, hash, bestEffort, string(audio.TimingJSON)),
		"changed timing policy must produce a fresh timing file key")
}

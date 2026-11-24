package contract

import (
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// AudioPrefetchAssetReport is the per-asset outcome of one prefetch attempt.
// It is the evidence that answers "did the prefetch actually deliver this
// asset, and how long did the resolution take".
type AudioPrefetchAssetReport struct {
	// Kind is "bgm", "sfx" or "clip_audio".
	Kind string `json:"kind"`
	// AssetID is the id the CALLER asked for: the public payload alias for
	// BGM/SFX ("bgm1") and the clip id for Kind=clip_audio.
	AssetID string `json:"asset_id"`
	// ResolvedID is the canonical registry id the resolution actually used.
	// BGM/SFX public aliases are mapped through audio.CanonicalAssetID (alias
	// → Drive identity), so this differs from AssetID whenever an alias was
	// used; clip ids pass through unchanged. Empty when the resolution never
	// ran (deadline).
	ResolvedID string `json:"resolved_id,omitempty"`
	// OK is true when the asset was resolved and cached.
	OK bool `json:"ok"`
	// DurationMS is the owner-measured resolution wall time (0 when the
	// resolution never ran because the prefetch deadline expired first).
	DurationMS int64 `json:"duration_ms,omitempty"`
	// Error is the resolution error, empty when OK.
	Error string `json:"error,omitempty"`
}

// AudioPrefetchResult carries pre-resolved audio assets ready for
// the audio compile phase, plus the bounded summary of what the
// prefetch actually delivered.
//
// The runtime carriers (maps + adapters) are json:"-" because they hold
// source references and local paths: they are consumed in-process only.
// The counts/failures ARE serialized so the job payload exposes the
// prefetch outcome to operators and to the polling API.
type AudioPrefetchResult struct {
	// ResolvedAudio holds pre-resolved BGM/SFX asset paths.
	// Map key is asset_id. Only populated when BGM/SFX intents
	// are present and the AudioAssetSource is wired.
	ResolvedAudio map[string]capabilityaudio.ResolvedAudioAsset `json:"-"`

	// ClipAudioPaths holds pre-materialized clip audio paths.
	// Map key is clip_id. Only populated when MixPolicy requires
	// original audio and ClipAudioAssetSource is wired.
	ClipAudioPaths map[string]string `json:"-"`

	// AudioAssetSource is the caching adapter that serves both
	// the original source (for BGM/SFX) and the clip audio source.
	// When non-nil, runAudioCompilePhase uses this instead of
	// the runner's raw audioAssetSource.
	AudioSource AudioAssetSource `json:"-"`

	// ClipAudioSource is the caching adapter for clip audio.
	ClipAudioSource ClipAudioAssetSource `json:"-"`

	// ── serializable summary (polling / evidence) ──────────────

	// BGMRequested/SFXRequested/ClipAudioRequested are the planned counts.
	BGMRequested       int `json:"bgm_requested,omitempty"`
	SFXRequested       int `json:"sfx_requested,omitempty"`
	ClipAudioRequested int `json:"clip_audio_requested,omitempty"`
	// BGMSFXResolved is the number of BGM/SFX assets cached.
	BGMSFXResolved int `json:"bgm_sfx_resolved,omitempty"`
	// ClipAudioReady is the number of clip-audio assets cached.
	ClipAudioReady int `json:"clip_audio_ready,omitempty"`
	// DurationMS is the prefetch wall time measured by its owner.
	DurationMS int64 `json:"duration_ms,omitempty"`
	// Degraded is true when at least one requested asset was NOT cached
	// (failure or deadline). Degraded prefetch is not an error: audio
	// compile resolves the remainder synchronously through the cache
	// adapters' fall-through path.
	Degraded bool `json:"degraded,omitempty"`
	// Failures describes the uncached assets, bounded.
	Failures []string `json:"failures,omitempty"`
	// Assets reports every attempt (bounded, sorted for stable output).
	Assets []AudioPrefetchAssetReport `json:"assets,omitempty"`
}

// CachedCount returns how many assets the prefetch actually made ready.
func (r *AudioPrefetchResult) CachedCount() int {
	if r == nil {
		return 0
	}
	return r.BGMSFXResolved + r.ClipAudioReady
}

// RequestedCount returns how many assets the prefetch was asked to resolve.
func (r *AudioPrefetchResult) RequestedCount() int {
	if r == nil {
		return 0
	}
	return r.BGMRequested + r.SFXRequested + r.ClipAudioRequested
}

package audio

import (
	"context"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// AudioAssetSource resolves ONE background-music or sound-effect asset_id to
// its canonical ResolvedAudioAsset (verified local path + certified source
// duration). Fail-closed: an unknown asset_id or an asset with no usable local
// copy is a typed error, never a silent empty path.
type AudioAssetSource interface {
	ResolveAudioAsset(ctx context.Context, assetID string) (capabilityaudio.ResolvedAudioAsset, error)
}

// ClipAudioAssetSource is the optional resolver for original clip audio. It is
// deliberately separate from AudioAssetSource because BGM/SFX resolution and
// clip-source materialization have different contracts.
type ClipAudioAssetSource interface {
	ResolveClipAudioAsset(ctx context.Context, assetID string) (capabilityaudio.ResolvedAudioAsset, error)
}

// AudioPrefetchAssetReport is the per-asset outcome of one prefetch attempt.
type AudioPrefetchAssetReport struct {
	// Kind is "bgm", "sfx" or "clip_audio".
	Kind string `json:"kind"`
	// AssetID is the id the CALLER asked for: the public payload alias for
	// BGM/SFX ("bgm1") and the clip id for Kind=clip_audio.
	AssetID string `json:"asset_id"`
	// ResolvedID is the canonical registry id the resolution actually used.
	ResolvedID string `json:"resolved_id,omitempty"`
	// OK is true when the asset was resolved and cached.
	OK bool `json:"ok"`
	// DurationMS is the owner-measured resolution wall time.
	DurationMS int64 `json:"duration_ms,omitempty"`
	// Error is the resolution error, empty when OK.
	Error string `json:"error,omitempty"`
}

// AudioPrefetchResult carries pre-resolved audio assets ready for the audio
// compile phase, plus the bounded summary of what the prefetch delivered.
//
// The runtime carriers (maps + adapters) are json:"-" because they hold source
// references and local paths: they are consumed in-process only. The
// counts/failures ARE serialized so the job payload exposes the prefetch
// outcome to operators and to the polling API.
type AudioPrefetchResult struct {
	ResolvedAudio   map[string]capabilityaudio.ResolvedAudioAsset `json:"-"`
	ClipAudioPaths  map[string]string                             `json:"-"`
	AudioSource     AudioAssetSource                              `json:"-"`
	ClipAudioSource ClipAudioAssetSource                          `json:"-"`

	BGMRequested       int                        `json:"bgm_requested,omitempty"`
	SFXRequested       int                        `json:"sfx_requested,omitempty"`
	ClipAudioRequested int                        `json:"clip_audio_requested,omitempty"`
	BGMSFXResolved     int                        `json:"bgm_sfx_resolved,omitempty"`
	ClipAudioReady     int                        `json:"clip_audio_ready,omitempty"`
	DurationMS         int64                      `json:"duration_ms,omitempty"`
	Degraded           bool                       `json:"degraded,omitempty"`
	Failures           []string                   `json:"failures,omitempty"`
	Assets             []AudioPrefetchAssetReport `json:"assets,omitempty"`
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

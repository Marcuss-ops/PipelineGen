// Package scriptgeneration — audio_prefetch.go implements the P1.1
// audio asset prefetch: BGM/SFX resolution + clip audio materialization
// run in parallel with TTS so they are ready when audio compile starts.
//
// Without prefetch, prepareClipAudioAssets (clip audio materialization)
// and BGM/SFX resolution both block in runAudioCompilePhase AFTER the
// voiceover phase completes. With prefetch, both start in the prepare
// goroutine that already runs VidRush/DocsPrepare concurrently with TTS,
// so by the time audio compile reaches them the I/O is done.
//
// The prefetch caches resolved assets in a concurrency-safe map. Audio
// compile reads from the cache via AudioAssetSource / ClipAudioAssetSource
// adapters that serve cache hits without blocking.
package scriptgeneration

import (
	"context"
	"fmt"
	"sync"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// AudioPrefetchResult carries pre-resolved audio assets ready for
// the audio compile phase. Nil fields mean no prefetch was done.
type AudioPrefetchResult struct {
	// ResolvedAudio holds pre-resolved BGM/SFX asset paths.
	// Map key is asset_id. Only populated when BGM/SFX intents
	// are present and the AudioAssetSource is wired.
	ResolvedAudio map[string]capabilityaudio.ResolvedAudioAsset

	// ClipAudioPaths holds pre-materialized clip audio paths.
	// Map key is clip_id. Only populated when MixPolicy requires
	// original audio and ClipAudioAssetSource is wired.
	ClipAudioPaths map[string]string

	// AudioAssetSource is the caching adapter that serves both
	// the original source (for BGM/SFX) and the clip audio source.
	// When non-nil, runAudioCompilePhase uses this instead of
	// the runner's raw audioAssetSource.
	AudioSource AudioAssetSource

	// ClipAudioSource is the caching adapter for clip audio.
	ClipAudioSource ClipAudioAssetSource
}

// audioPrefetchCache is a concurrency-safe cache used by the
// prefetch adapters. Write once (during prefetch), read many
// (during audio compile).
type audioPrefetchCache struct {
	mu        sync.RWMutex
	audio     map[string]capabilityaudio.ResolvedAudioAsset // asset_id → resolved
	clipAudio map[string]capabilityaudio.ResolvedAudioAsset // clip_id → resolved original audio
}

func newAudioPrefetchCache() *audioPrefetchCache {
	return &audioPrefetchCache{
		audio:     make(map[string]capabilityaudio.ResolvedAudioAsset),
		clipAudio: make(map[string]capabilityaudio.ResolvedAudioAsset),
	}
}

// cachedAudioAssetSource wraps the real AudioAssetSource with a
// prefetch cache. Cache hits return immediately; cache misses
// fall through to the real source (layered, never replaced).
type cachedAudioAssetSource struct {
	real  AudioAssetSource
	cache *audioPrefetchCache
}

func (c *cachedAudioAssetSource) ResolveAudioAsset(ctx context.Context, assetID string) (capabilityaudio.ResolvedAudioAsset, error) {
	c.cache.mu.RLock()
	if r, ok := c.cache.audio[assetID]; ok {
		c.cache.mu.RUnlock()
		return r, nil
	}
	c.cache.mu.RUnlock()
	if c.real == nil {
		return capabilityaudio.ResolvedAudioAsset{}, fmt.Errorf("audio asset %q not resolved (no source wired)", assetID)
	}
	return c.real.ResolveAudioAsset(ctx, assetID)
}

// cachedClipAudioAssetSource wraps the real ClipAudioAssetSource
// with a prefetch cache.
type cachedClipAudioAssetSource struct {
	real  ClipAudioAssetSource
	cache *audioPrefetchCache
}

func (c *cachedClipAudioAssetSource) ResolveClipAudioAsset(ctx context.Context, clipID string) (capabilityaudio.ResolvedAudioAsset, error) {
	c.cache.mu.RLock()
	if resolved, ok := c.cache.clipAudio[clipID]; ok {
		c.cache.mu.RUnlock()
		return resolved, nil
	}
	c.cache.mu.RUnlock()
	if c.real == nil {
		return capabilityaudio.ResolvedAudioAsset{}, fmt.Errorf("clip audio %q not resolved (no source wired)", clipID)
	}
	return c.real.ResolveClipAudioAsset(ctx, clipID)
}

// PrefetchAudioAssets resolves BGM/SFX assets and materializes
// clip audio for a run, running all I/O concurrently. It returns
// a result carrying caching adapters that audio compile can consume
// without blocking.
//
// When no BGM/SFX intents are present and MixPolicy does not require
// original clip audio, returns an empty result (no prefetch needed).
//
// godlike/07 fail-closed: a nil source for a required resolution
// returns an error immediately (the prefetch itself fails, not the
// audio compile that would discover the nil source later).
func PrefetchAudioAssets(
	ctx context.Context,
	bgmIDs []string,
	sfxIDs []string,
	audioSource AudioAssetSource,
	clipIDs []string,
	clipAudioSource ClipAudioAssetSource,
	policy capabilityaudio.AudioMixPolicy,
) (*AudioPrefetchResult, error) {
	needsAudio := len(bgmIDs) > 0 || len(sfxIDs) > 0
	needsClipAudio := policy.Normalize() == capabilityaudio.MixVoiceoverWithDuckedClip && len(clipIDs) > 0

	if !needsAudio && !needsClipAudio {
		return &AudioPrefetchResult{}, nil
	}

	cache := newAudioPrefetchCache()
	g, gCtx := concurrent.WithContext(ctx)

	// ── BGM/SFX asset resolution ──────────────────────────────
	if needsAudio {
		if audioSource == nil {
			return nil, fmt.Errorf("audio prefetch: BGM/SFX intents require an audio asset source")
		}
		allIDs := make([]string, 0, len(bgmIDs)+len(sfxIDs))
		allIDs = append(allIDs, bgmIDs...)
		allIDs = append(allIDs, sfxIDs...)
		for _, id := range allIDs {
			id := id
			g.Go("prefetch-bgm-sfx-"+id, func() error {
				resolved, err := audioSource.ResolveAudioAsset(gCtx, id)
				if err != nil {
					return fmt.Errorf("resolve BGM/SFX %q: %w", id, err)
				}
				cache.mu.Lock()
				cache.audio[id] = resolved
				cache.mu.Unlock()
				return nil
			})
		}
	}

	// ── Clip audio materialization ────────────────────────────
	if needsClipAudio {
		if clipAudioSource == nil {
			return nil, fmt.Errorf("audio prefetch: VOICEOVER_DUCKED_CLIP requires a clip audio source")
		}
		for _, id := range clipIDs {
			id := id
			g.Go("prefetch-clip-audio-"+id, func() error {
				resolved, err := clipAudioSource.ResolveClipAudioAsset(gCtx, id)
				if err != nil {
					return fmt.Errorf("materialize clip audio %q: %w", id, err)
				}
				cache.mu.Lock()
				cache.clipAudio[id] = resolved
				cache.mu.Unlock()
				return nil
			})
		}
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	var cachedAudio AudioAssetSource
	if audioSource != nil {
		cachedAudio = &cachedAudioAssetSource{real: audioSource, cache: cache}
	}
	var cachedClipAudio ClipAudioAssetSource
	if clipAudioSource != nil {
		cachedClipAudio = &cachedClipAudioAssetSource{real: clipAudioSource, cache: cache}
	}

	clipAudioPaths := make(map[string]string, len(cache.clipAudio))
	for id, resolved := range cache.clipAudio {
		clipAudioPaths[id] = resolved.Path
	}
	return &AudioPrefetchResult{
		ResolvedAudio:   cache.audio,
		ClipAudioPaths:  clipAudioPaths,
		AudioSource:     cachedAudio,
		ClipAudioSource: cachedClipAudio,
	}, nil
}

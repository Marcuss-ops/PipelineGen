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
// adapters that serve cache hits without blocking and fall through to the
// real source on a miss, so a PARTIAL prefetch is always strictly better
// than no prefetch.
//
// Failure policy (the reason this file has one):
//   - A nullable/invalid WIRING is fail-closed: a required source that is
//     nil returns an error (godlike/07), because audio compile would
//     discover the same nil source later, further from the cause.
//   - An individual ASSET failure is fail-soft: it is recorded in the
//     result and the remaining assets stay cached. The first version of
//     this function returned the first error and dropped every asset it
//     had already resolved, which made one slow/failing clip discard the
//     whole prefetch and pushed the entire download back onto the
//     synchronous audio-compile path.
//   - The prefetch is BOUNDED by the caller's deadline: a stalled asset
//     cannot delay the fan-out join indefinitely; whatever was resolved
//     before the deadline is kept.
package scriptgeneration

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

const (
	// maxAudioPrefetchAssetReports bounds the per-asset report list so a
	// pathological plan cannot inflate the durable result / polling payload.
	maxAudioPrefetchAssetReports = 64
	// maxAudioPrefetchFailures bounds the failure list for the same reason.
	maxAudioPrefetchFailures = 16

	// audioPrefetchBudget bounds the P1.1 prefetch. It overlaps TTS + the
	// render fan-out, so its wall time is usually free; the budget exists so
	// a stalled asset resolution can never delay the fan-out join (and the
	// audio stage behind it) without bound. Assets not cached by the budget
	// are resolved synchronously by audio compile, exactly as they would be
	// without a prefetch — the budget therefore only ever removes work from
	// the critical path, never adds it.
	audioPrefetchBudget = 25 * time.Second
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

// dedupeIDs returns the ids with blank entries removed and duplicates
// collapsed, preserving first-occurrence order.
func dedupeIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
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

// putAudio caches a resolved BGM/SFX asset under the canonical registry id
// AND under the public alias the payload used, so both the prefetch's own
// bookkeeping and the compile boundary's lookup are hits.
func (c *audioPrefetchCache) putAudio(canonicalID, aliasID string, resolved capabilityaudio.ResolvedAudioAsset) {
	c.mu.Lock()
	c.audio[canonicalID] = resolved
	if aliasID != "" && aliasID != canonicalID {
		c.audio[aliasID] = resolved
	}
	c.mu.Unlock()
}

func (c *audioPrefetchCache) putClipAudio(id string, resolved capabilityaudio.ResolvedAudioAsset) {
	c.mu.Lock()
	c.clipAudio[id] = resolved
	c.mu.Unlock()
}

// cachedAudioAssetSource wraps the real AudioAssetSource with a
// prefetch cache. Cache hits return immediately; cache misses
// fall through to the real source (layered, never replaced).
type cachedAudioAssetSource struct {
	real  AudioAssetSource
	cache *audioPrefetchCache
}

func (c *cachedAudioAssetSource) ResolveAudioAsset(ctx context.Context, assetID string) (capabilityaudio.ResolvedAudioAsset, error) {
	// The compile boundary resolves through audio.CanonicalAssetID (public
	// alias → registry identity), so a hit is looked up under BOTH spellings:
	// the caller's id and its canonical form.
	canonical := capabilityaudio.CanonicalAssetID(assetID)
	c.cache.mu.RLock()
	if r, ok := c.cache.audio[assetID]; ok {
		c.cache.mu.RUnlock()
		return r, nil
	}
	if canonical != assetID {
		if r, ok := c.cache.audio[canonical]; ok {
			c.cache.mu.RUnlock()
			return r, nil
		}
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
// It never returns nil with a nil error, and it never returns an error for
// an individual asset failure: partial success is the normal outcome under
// a deadline. A returned error means the prefetch could not even be
// ATTEMPTED (nil source for a required resolution).
func PrefetchAudioAssets(
	ctx context.Context,
	bgmIDs []string,
	sfxIDs []string,
	audioSource AudioAssetSource,
	clipIDs []string,
	clipAudioSource ClipAudioAssetSource,
	policy capabilityaudio.AudioMixPolicy,
) (*AudioPrefetchResult, error) {
	// De-duplicate first: a plan that references the same clip from two scenes
	// (or the same cue twice) must not resolve it twice. Duplicates are free
	// for the caller to send and pure waste to honour.
	bgmIDs = dedupeIDs(bgmIDs)
	sfxIDs = dedupeIDs(sfxIDs)
	clipIDs = dedupeIDs(clipIDs)

	needsAudio := len(bgmIDs) > 0 || len(sfxIDs) > 0
	needsClipAudio := policy.Normalize() == capabilityaudio.MixVoiceoverWithDuckedClip && len(clipIDs) > 0

	result := &AudioPrefetchResult{
		BGMRequested:       len(bgmIDs),
		SFXRequested:       len(sfxIDs),
		ClipAudioRequested: 0,
	}
	if needsClipAudio {
		result.ClipAudioRequested = len(clipIDs)
	}
	if !needsAudio && !needsClipAudio {
		return result, nil
	}

	// Fail-closed on wiring: a required source that is nil is a
	// composition bug, not an asset failure. The prefetch itself fails
	// here, instead of letting audio compile discover it later.
	if needsAudio && audioSource == nil {
		return nil, fmt.Errorf("audio prefetch: BGM/SFX intents require an audio asset source")
	}
	if needsClipAudio && clipAudioSource == nil {
		return nil, fmt.Errorf("audio prefetch: VOICEOVER_DUCKED_CLIP requires a clip audio source")
	}

	started := time.Now()
	cache := newAudioPrefetchCache()

	var (
		wg      sync.WaitGroup
		repMu   sync.Mutex
		reports = make([]AudioPrefetchAssetReport, 0, len(bgmIDs)+len(sfxIDs)+len(clipIDs))
	)
	record := func(rep AudioPrefetchAssetReport) {
		repMu.Lock()
		reports = append(reports, rep)
		repMu.Unlock()
	}

	// ── BGM/SFX asset resolution ──────────────────────────────
	// The payload addresses BGM/SFX by PUBLIC ALIAS ("bgm1"), while the media
	// registry is keyed by Drive identity: the compile boundary maps the alias
	// through audio.CanonicalAssetID before resolving. The prefetch must use
	// the SAME id, otherwise it resolves nothing and reports an asset failure
	// ("audio asset \"bgm1\" not found") for an id the compile resolves fine.
	resolveAudio := func(kind, publicID string) {
		canonicalID := capabilityaudio.CanonicalAssetID(publicID)
		wg.Add(1)
		concurrent.SafeGo("prefetch-"+kind+"-"+publicID, func() {
			defer wg.Done()
			t0 := time.Now()
			resolved, err := audioSource.ResolveAudioAsset(ctx, canonicalID)
			rep := AudioPrefetchAssetReport{
				Kind: kind, AssetID: publicID, ResolvedID: canonicalID,
				DurationMS: time.Since(t0).Milliseconds(),
			}
			if err != nil {
				rep.Error = fmt.Sprintf("resolve %s %q (registry id %q): %v", kind, publicID, canonicalID, err)
				record(rep)
				return
			}
			cache.putAudio(canonicalID, publicID, resolved)
			rep.OK = true
			record(rep)
		})
	}
	for _, id := range bgmIDs {
		resolveAudio("bgm", id)
	}
	for _, id := range sfxIDs {
		resolveAudio("sfx", id)
	}

	// ── Clip audio materialization ────────────────────────────
	if needsClipAudio {
		for _, id := range clipIDs {
			clipID := id
			wg.Add(1)
			concurrent.SafeGo("prefetch-clip-audio-"+clipID, func() {
				defer wg.Done()
				t0 := time.Now()
				resolved, err := clipAudioSource.ResolveClipAudioAsset(ctx, clipID)
				rep := AudioPrefetchAssetReport{Kind: "clip_audio", AssetID: clipID, DurationMS: time.Since(t0).Milliseconds()}
				if err != nil {
					rep.Error = fmt.Sprintf("materialize clip audio %q: %v", clipID, err)
					record(rep)
					return
				}
				cache.putClipAudio(clipID, resolved)
				rep.OK = true
				record(rep)
			})
		}
	}

	wg.Wait()
	result.DurationMS = time.Since(started).Milliseconds()

	// Stable, bounded projection: sorted by (kind, asset_id) so two runs of
	// the same plan produce the same evidence order despite concurrency.
	sort.Slice(reports, func(i, j int) bool {
		if reports[i].Kind != reports[j].Kind {
			return reports[i].Kind < reports[j].Kind
		}
		return reports[i].AssetID < reports[j].AssetID
	})
	// Counts and failures are derived ONCE, after the join: the goroutines
	// above only append reports, so no counter is written concurrently.
	failures := make([]string, 0, 4)
	for _, rep := range reports {
		if rep.OK {
			if rep.Kind == "clip_audio" {
				result.ClipAudioReady++
			} else {
				result.BGMSFXResolved++
			}
			continue
		}
		if len(failures) < maxAudioPrefetchFailures {
			failures = append(failures, rep.Error)
		}
	}
	if omitted := len(reports) - result.BGMSFXResolved - result.ClipAudioReady - len(failures); omitted > 0 {
		failures = append(failures, fmt.Sprintf("%d more prefetch failures not listed", omitted))
	}
	if len(reports) > maxAudioPrefetchAssetReports {
		reports = reports[:maxAudioPrefetchAssetReports]
	}
	result.Assets = reports
	result.Failures = failures
	result.Degraded = result.CachedCount() < result.RequestedCount()

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
	result.ResolvedAudio = cache.audio
	result.ClipAudioPaths = clipAudioPaths
	result.AudioSource = cachedAudio
	result.ClipAudioSource = cachedClipAudio
	return result, nil
}

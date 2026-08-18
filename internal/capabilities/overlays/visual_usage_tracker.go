// Package overlays — visual_usage_tracker.go owns the VisualUsageTracker: the
// per-render registry that stops the same entity from showing the same image
// over and over.
//
// Pipeline position:
//
//	EntityMediaResolver (candidate assets, quality order)
//	→ VisualUsageTracker.Pick (rotation + cooldown)
//	→ VisualIntent.AssetID (+ ForceVariant → sampler picks a different
//	  preset/animation when the same image must be reused)
//
// Rules (deterministic, in order):
//
//  1. Rotation — among the entity's assets that are NOT in cooldown, the
//     tracker round-robins per entity, so a six-time appearance of Floyd
//     cycles through floyd_01, floyd_03, floyd_02, … instead of always
//     showing the highest-quality image.
//  2. Same-asset cooldown — an asset is "fresh" only when it has never been
//     used or its last use is at least SameAssetCooldownUS ago.
//  3. Fallback — when every asset is still in cooldown, the tracker reuses
//     the least-recently-used asset and flags ForceVariant so the downstream
//     sampler compensates with a different preset/animation (same image,
//     different treatment).
//
// The tracker is STATEFUL and per-render: create one per pipeline run (or call
// Reset between runs). There is no process-wide singleton — usage state must
// never leak across renders.
package overlays

// UsageTrackerConfig is the single knob of the VisualUsageTracker.
type UsageTrackerConfig struct {
	// SameAssetCooldownUS is the minimum time between two uses of the same
	// asset, in microseconds. Non-positive falls back to the default (15s).
	SameAssetCooldownUS int64
}

// DefaultUsageTrackerConfig returns the canonical cooldown: 15 seconds.
func DefaultUsageTrackerConfig() UsageTrackerConfig {
	return UsageTrackerConfig{SameAssetCooldownUS: 15_000_000}
}

// UsagePick is the tracker's deterministic selection for one occurrence.
type UsagePick struct {
	// AssetID is the selected asset (empty when no candidates were given).
	AssetID string
	// ForceVariant is true when the selected asset had to be reused inside
	// its cooldown (no fresh alternative existed): the sampler should vary the
	// preset/animation so the visual still changes.
	ForceVariant bool
}

// VisualUsageTracker tracks asset usage across one render so repeated
// appearances of an entity rotate through its available assets and never
// hammer the same image back-to-back.
type VisualUsageTracker struct {
	cooldown int64
	lastUsed map[string]int64 // assetID → last use time (us)
	rotation map[string]int   // entityID → round-robin pointer
}

// NewVisualUsageTracker returns a tracker with the given config (non-positive
// cooldown falls back to the default).
func NewVisualUsageTracker(cfg UsageTrackerConfig) *VisualUsageTracker {
	if cfg.SameAssetCooldownUS <= 0 {
		cfg.SameAssetCooldownUS = DefaultUsageTrackerConfig().SameAssetCooldownUS
	}
	return &VisualUsageTracker{
		cooldown: cfg.SameAssetCooldownUS,
		lastUsed: map[string]int64{},
		rotation: map[string]int{},
	}
}

// Pick selects the asset for one occurrence of an entity at time nowUS.
// candidates are the entity's available assets in the caller's preferred order
// (e.g. quality order from the media resolver). It returns the selected asset
// and whether a visual variant must be forced (fallback reuse).
//
// The same (entityID, candidates, history, nowUS) sequence always yields the
// same picks: rotation and cooldown are pure deterministic state, never
// wall-clock or random.
func (t *VisualUsageTracker) Pick(entityID string, candidates []string, nowUS int64) UsagePick {
	if t == nil || len(candidates) == 0 {
		return UsagePick{}
	}

	var fresh []string
	for _, assetID := range candidates {
		last, used := t.lastUsed[assetID]
		if !used || nowUS-last >= t.cooldown {
			fresh = append(fresh, assetID)
		}
	}

	var pick UsagePick
	if len(fresh) > 0 {
		idx := t.rotation[entityID] % len(fresh)
		pick.AssetID = fresh[idx]
		t.rotation[entityID]++
	} else {
		pick.AssetID = t.leastRecentlyUsed(candidates)
		pick.ForceVariant = true
	}
	t.lastUsed[pick.AssetID] = nowUS
	return pick
}

// Reset clears all usage state so the tracker can be reused for a new render.
func (t *VisualUsageTracker) Reset() {
	if t == nil {
		return
	}
	t.lastUsed = map[string]int64{}
	t.rotation = map[string]int{}
}

// leastRecentlyUsed returns the candidate with the oldest last-use time (ties
// broken by candidate order). Never-used assets carry time 0 and win first.
func (t *VisualUsageTracker) leastRecentlyUsed(candidates []string) string {
	best := candidates[0]
	bestLast := t.lastUsed[best]
	for _, assetID := range candidates[1:] {
		if last := t.lastUsed[assetID]; last < bestLast {
			best, bestLast = assetID, last
		}
	}
	return best
}

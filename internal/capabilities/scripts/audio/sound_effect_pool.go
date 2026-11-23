package audio

// sound_effect_pool.go owns the SELECTION POOL of one-shot SFX the editor
// picks for a clip start. The catalog itself is owned by the media SSOT
// (source=sound_effect rows); this file only filters that projection to the
// families an editorial cue may use and makes the order deterministic.
//
// The pool lives in the audio target package so the root scripts package keeps
// only the Runner method that adapts its audio source. The capability never
// imports the registry or PostgreSQL.

import (
	"context"
	"sort"
	"strings"
)

// SoundEffectCandidate is the selection-relevant projection of one indexed
// sound effect. AssetID is the id the audio resolver accepts (the media
// registry identity), never a filename.
type SoundEffectCandidate struct {
	AssetID    string
	Family     string
	Subtype    string
	Mood       string
	Energy     string
	DurationUS int64
	Tags       []string
}

// SoundEffectPoolSource lists the indexed one-shot effects. The composition
// root implements it over the PostgreSQL media SSOT; a source that only
// resolves individual assets legitimately does not implement it, and the
// selector then falls back to the built-in whoosh vocabulary.
type SoundEffectPoolSource interface {
	ListSoundEffectCandidates(ctx context.Context) ([]SoundEffectCandidate, error)
}

// editorialCueFamilies are the families a clip-start/overlay cue may use.
// Music, ambience and meme families are excluded on purpose: they are valid
// assets, but they are not a transition accent. A caller that wants them can
// still pin the asset_id explicitly. The curated camera/UI set uses foley,
// photo and ui alongside the whoosh family, so all three are admitted here.
var editorialCueFamilies = map[string]struct{}{
	"whoosh":     {},
	"transition": {},
	"impact":     {},
	"riser":      {},
	"foley":      {},
	"photo":      {},
	"ui":         {},
}

const (
	// minCueSourceUS excludes clicks/blips too short to read as a transition
	// accent when the cue is trimmed to the clip-start window.
	minCueSourceUS = int64(120_000)
	// maxCueSourceUS excludes long risers/ambience beds from the one-shot
	// pool; they remain selectable by explicit asset_id.
	maxCueSourceUS = int64(15_000_000)
)

// FilterSoundEffectCandidates returns the ordered, unique asset ids the
// deterministic selector may use for this run. An empty result is not an
// error: the selector falls back to the built-in whoosh aliases, so a
// deployment whose SFX library has not been indexed keeps rendering instead of
// failing.
func FilterSoundEffectCandidates(candidates []SoundEffectCandidate) []string {
	seen := make(map[string]struct{}, len(candidates))
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		family := strings.ToLower(strings.TrimSpace(candidate.Family))
		if _, ok := editorialCueFamilies[family]; !ok {
			continue
		}
		if candidate.DurationUS < minCueSourceUS || candidate.DurationUS > maxCueSourceUS {
			continue
		}
		id := strings.TrimSpace(candidate.AssetID)
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Package scriptgeneration — background_music_resolver.go converts the
// wire-level BGM intents into canonical ResolvedBGM windows.
//
// The domain always works with []BackgroundMusicIntent; this resolver is
// the boundary that turns each intent's millisecond window command
// (start_ms / end / end_ms / video_end) into an absolute microsecond
// window on the canonical timeline, and normalizes every other
// millisecond field (fades, duck ramps) to canonical microseconds. The
// loop expander and the automation compiler consume only resolved facts.
package scriptgeneration

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// BackgroundMusicResolver turns BGM intents into resolved windows. It is
// stateless and pure: same timeline + intents → same windows, always.
type BackgroundMusicResolver struct{}

// NewBackgroundMusicResolver builds the canonical resolver. No
// dependencies.
func NewBackgroundMusicResolver() *BackgroundMusicResolver {
	return &BackgroundMusicResolver{}
}

// Resolve converts every BGM intent into one audio.ResolvedBGM with an
// absolute window on the canonical timeline:
//
//   - StartMS → window start (0 = video start);
//   - End.IsVideoEnd() → window end = CanonicalTimeline.DurationUS;
//   - absolute end (end / end_ms alias) → window end = that offset.
//
// Fades and duck ramps are normalized ms → µs. Fail-closed: blank
// asset_id, negative offsets/ramps, windows ending before they start, or
// windows exceeding the timeline all fail the whole resolution.
func (r *BackgroundMusicResolver) Resolve(timeline audio.CanonicalTimeline, intents []scriptpkg.BackgroundMusicIntent) ([]audio.ResolvedBGM, error) {
	if err := timeline.Validate(); err != nil {
		return nil, fmt.Errorf("resolve background music: %w", err)
	}
	out := make([]audio.ResolvedBGM, 0, len(intents))
	for i, intent := range intents {
		assetID := strings.TrimSpace(intent.AssetID)
		if assetID == "" {
			return nil, fmt.Errorf("resolve background music: bgm %d requires an asset_id", i)
		}
		startUS := intent.StartMS * 1000
		if startUS < 0 {
			return nil, fmt.Errorf("resolve background music: bgm %d (%s) start_ms must be >= 0", i, assetID)
		}
		if startUS >= timeline.DurationUS {
			return nil, fmt.Errorf("resolve background music: bgm %d (%s) starts at %dus, outside the %dus timeline", i, assetID, startUS, timeline.DurationUS)
		}
		endUS := timeline.DurationUS
		if !intent.End.IsVideoEnd() {
			endUS = intent.End.Ms * 1000
			if endUS > timeline.DurationUS {
				return nil, fmt.Errorf("resolve background music: bgm %d (%s) ends at %dus, beyond the %dus timeline", i, assetID, endUS, timeline.DurationUS)
			}
		}
		if endUS <= startUS {
			return nil, fmt.Errorf("resolve background music: bgm %d (%s) window [%d,%d) is empty", i, assetID, startUS, endUS)
		}
		fadeInUS := intent.FadeInMS * 1000
		fadeOutUS := intent.FadeOutMS * 1000
		if fadeInUS < 0 || fadeOutUS < 0 {
			return nil, fmt.Errorf("resolve background music: bgm %d (%s) fades must be >= 0 (got in=%dus out=%dus)", i, assetID, fadeInUS, fadeOutUS)
		}
		duckAttackUS := intent.DuckAttackMS * 1000
		duckReleaseUS := intent.DuckReleaseMS * 1000
		if duckAttackUS < 0 || duckReleaseUS < 0 {
			return nil, fmt.Errorf("resolve background music: bgm %d (%s) duck ramps must be >= 0 (got attack=%dus release=%dus)", i, assetID, duckAttackUS, duckReleaseUS)
		}
		out = append(out, audio.ResolvedBGM{
			AssetID:            assetID,
			TimelineStartUS:    startUS,
			DurationUS:         endUS - startUS,
			Loop:               intent.Loop,
			GainDB:             intent.GainDB,
			FadeInUS:           fadeInUS,
			FadeOutUS:          fadeOutUS,
			DuckUnderVoiceover: intent.DuckUnderVoiceover,
			DuckGainDB:         intent.DuckGainDB,
			DuckAttackUS:       duckAttackUS,
			DuckReleaseUS:      duckReleaseUS,
		})
	}
	return out, nil
}

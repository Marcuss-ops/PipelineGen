// Package scriptgeneration — audio_loop_expander.go expands a resolved
// BGM layer into deterministic timeline events.
//
// Rule (Go decides, Rust executes): the compiled plan must contain every
// repetition as an explicit event with its own timeline window and source
// range. The renderer never loops — it would have to guess how many times
// a short music file must repeat to cover the video. This expander makes
// that decision once, in Go, from the certified source length:
//
//	video = 145s, music = 40s, loop → 4 events:
//	  0→40 (source 0→40), 40→80 (source 0→40),
//	  80→120 (source 0→40), 120→145 (source 0→25, truncated)
//
// The last event is truncated so the loop ends exactly on the BGM window
// end (which, for end=video_end, is CanonicalTimeline.DurationUS) — the
// audio master duration never drifts from the video duration.
package scriptgeneration

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// AudioLoopExpander expands one resolved BGM layer into one or more
// canonical AudioLayer events. It is stateless and pure: same window +
// source length → same events, always.
type AudioLoopExpander struct{}

// NewAudioLoopExpander builds the canonical expander. No dependencies.
func NewAudioLoopExpander() *AudioLoopExpander {
	return &AudioLoopExpander{}
}

// Expand turns one resolved BGM layer into deterministic AudioLayer
// events:
//
//   - loop=false: a single event covering min(window, source). A source
//     shorter than the window leaves the remaining window as BGM silence
//     (no events are emitted for it).
//   - loop=true: events tile the window in source-length steps, each
//     restarting from source 0; the last event is truncated so the loop
//     ends exactly on the window end.
//   - sourceDurationUS <= 0 fails closed: without the certified source
//     length the expansion is not deterministic — a silent guess would
//     either overrun the video or leave a gap.
func (r *AudioLoopExpander) Expand(bgm audio.ResolvedBGM, sourceDurationUS int64) ([]audio.AudioLayer, error) {
	if strings.TrimSpace(bgm.AssetID) == "" {
		return nil, errors.New("scriptgeneration: bgm expansion requires an asset_id")
	}
	if bgm.TimelineStartUS < 0 {
		return nil, fmt.Errorf("scriptgeneration: bgm %s window start %dus is negative", bgm.AssetID, bgm.TimelineStartUS)
	}
	if bgm.DurationUS <= 0 {
		return nil, fmt.Errorf("scriptgeneration: bgm %s has no window (duration_us=%d)", bgm.AssetID, bgm.DurationUS)
	}
	if sourceDurationUS <= 0 {
		return nil, fmt.Errorf("scriptgeneration: bgm %s source duration is unknown (%dus); loop expansion is not deterministic without it", bgm.AssetID, sourceDurationUS)
	}
	if !bgm.Loop {
		// Single event truncated to the window: a longer source is cut at
		// the window end, a shorter source ends early and the rest of the
		// window stays silent (no synthetic filler is invented).
		return []audio.AudioLayer{{
			AssetID:         bgm.AssetID,
			TimelineStartUS: bgm.TimelineStartUS,
			DurationUS:      min(sourceDurationUS, bgm.DurationUS),
			GainDB:          bgm.GainDB,
		}}, nil
	}
	var layers []audio.AudioLayer
	remaining := bgm.DurationUS
	at := bgm.TimelineStartUS
	for i := 0; remaining > 0; i++ {
		dur := min(sourceDurationUS, remaining)
		layers = append(layers, audio.AudioLayer{
			AssetID:         bgm.AssetID,
			TimelineStartUS: at,
			DurationUS:      dur,
			GainDB:          bgm.GainDB,
		})
		at += dur
		remaining -= dur
	}
	return layers, nil
}

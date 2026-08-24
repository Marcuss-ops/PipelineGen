package assets

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// CuesWithText keeps canonical source timing while projecting an already
// translated full transcript onto the same cue count. The source translation
// remains authoritative in asset_text_tracks; this only distributes the
// translated text across the source-language cue windows so every language
// carries identical timing/order/segmentation (the "segmenti/timestamp
// invariati" invariant).
//
// godlike/06 SSOT: this is the SOLE canonical owner of the full-text→cues
// distribution formula. cmd/admin and the multilingual renderer MUST call
// this helper rather than re-deriving the word-slicing logic inline.
func CuesWithText(timing []asset.TimedCue, text string) []asset.TimedCue {
	words := strings.Fields(text)
	out := make([]asset.TimedCue, len(timing))
	for i, cue := range timing {
		start := i * len(words) / len(timing)
		end := (i + 1) * len(words) / len(timing)
		if end <= start && start < len(words) {
			end = start + 1
		}
		if start > len(words) {
			start = len(words)
		}
		if end > len(words) {
			end = len(words)
		}
		out[i] = cue
		out[i].Text = strings.Join(words[start:end], " ")
	}
	return out
}

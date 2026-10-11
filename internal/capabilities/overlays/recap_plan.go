package overlays

// Package overlays — recap_plan.go wires the Automatic Recap Builder into
// the production overlay plan.
//
// A recap card is a visual summary of already-narrated chapters shown when a
// placement scene begins ("Previously in this video…"). It rides the
// certified QUOTE text primitive (centered card, registry preset + motion),
// so no RenderingGen change is needed: unknown templates fail closed at the
// worker, and QUOTE is already registered on both sides.
//
// Provenance and timing rules (fail-soft, never guessed):
//   - candidates come from BuildRecapCandidatesThrough: only sources whose
//     sentence ids are at or before NarratedThrough survive, so later
//     chapters can never leak into an earlier recap;
//   - cards are laid out sequentially from the placement scene's canonical
//     window; a card that does not fit entirely is dropped, never squeezed
//     or overflowed past the window (X10: no element outside timing);
//   - an invalid window or zero candidates is a legitimate no-op (short
//     videos may have no recap at all — R02).
// Deterministic: same placements → same items.

import (
	"fmt"
	"strings"
)

// RecapCardDurationUS is the fixed on-screen duration of one recap card.
// RecapMinCardSlotUS is the smallest remaining window still worth a card:
// a smaller leftover stays empty rather than squeezing a card in.
const (
	RecapCardDurationUS int64 = 4_000_000
	RecapMinCardSlotUS  int64 = 2_000_000
)

// RecapPlacement is one recap slot bound to a placement scene's canonical
// window. SentenceIDs inside Sources use the caller's narration index
// space; NarratedThrough is the last already-narrated index at the moment
// the placement scene begins.
type RecapPlacement struct {
	SceneID         string
	WindowStartUS   int64
	WindowEndUS     int64
	Sources         []RecapSource
	NarratedThrough int
	MaxCards        int
}

// BuildRecapPlanItems lowers recap placements to overlay items on the
// certified quote primitive. Items are returned in placement order with
// sequential, non-overlapping windows; placements that cannot contribute
// are skipped silently.
func BuildRecapPlanItems(planID string, placements []RecapPlacement) []OverlayItem {
	var out []OverlayItem
	usedIDs := map[string]struct{}{}
	for _, p := range placements {
		sceneID := strings.TrimSpace(p.SceneID)
		if sceneID == "" || p.WindowEndUS <= p.WindowStartUS {
			continue
		}
		maxCards := p.MaxCards
		if maxCards <= 0 {
			maxCards = 3
		}
		cands := BuildRecapCandidatesThrough(p.Sources, maxCards, p.NarratedThrough)
		if len(cands) == 0 {
			continue
		}
		cursor := p.WindowStartUS
		for _, c := range cands {
			remaining := p.WindowEndUS - cursor
			if remaining < RecapMinCardSlotUS {
				break
			}
			dur := RecapCardDurationUS
			if dur > remaining {
				dur = remaining
			}
			startMS := cursor / 1000
			endMS := (cursor + dur + 999) / 1000
			if endMS <= startMS {
				break
			}
			id := itemID(sceneID, "recap", c.Title)
			if _, dup := usedIDs[id]; dup {
				for n := 2; ; n++ {
					alt := fmt.Sprintf("%s-%d", id, n)
					if _, taken := usedIDs[alt]; !taken {
						id = alt
						break
					}
				}
			}
			usedIDs[id] = struct{}{}
			out = append(out, OverlayItem{
				ID: id, SceneID: sceneID,
				PresetID: selectPhrasePreset(planID, sceneID, id),
				MotionID: SelectTextMotion(planID, sceneID, id),
				Kind:     "quote", TemplateID: "QUOTE", Text: c.Title,
				StartMs: startMS, EndMs: endMS, StartUS: cursor, DurationUS: dur,
				Params: map[string]any{"position": "center", "style": "quote", "priority": c.Score, "recap": true},
			})
			cursor += dur
		}
	}
	if out == nil {
		return []OverlayItem{}
	}
	return out
}

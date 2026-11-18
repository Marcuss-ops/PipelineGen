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

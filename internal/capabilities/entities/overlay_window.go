package entities

// MaxEntityOverlayDurationUS is the hard editorial ceiling for entity image
// overlays. A long spoken mention cannot make the image layer run indefinitely.
const MaxEntityOverlayDurationUS int64 = 5_000_000

// MinEntityOverlayDurationUS is the readability floor of the dynamic entity
// window: a very short spoken mention still stays on screen long enough to
// read the name and caption instead of collapsing into a flash.
const MinEntityOverlayDurationUS int64 = 1_000_000

// EntityOverlayHoldoutUS is the tail hold added after the last spoken word of
// a mention so the card remains readable once the narration moves on.
const EntityOverlayHoldoutUS int64 = 500_000

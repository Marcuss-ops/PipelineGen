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

// EntitySpokenWindowDuration derives the DYNAMIC entity-overlay duration from
// the certified spoken window of one mention: the audio the entity actually
// occupies plus a half-second readability hold, clamped into
// [MinEntityOverlayDurationUS, MaxEntityOverlayDurationUS]. The duration is
// never estimated from text length or scene duration — only the certified
// audio positions count.
func EntitySpokenWindowDuration(audioStartUS, audioEndUS int64) int64 {
	spoken := audioEndUS - audioStartUS
	if spoken < 0 {
		spoken = 0
	}
	duration := spoken + EntityOverlayHoldoutUS
	if duration < MinEntityOverlayDurationUS {
		duration = MinEntityOverlayDurationUS
	}
	if duration > MaxEntityOverlayDurationUS {
		duration = MaxEntityOverlayDurationUS
	}
	return ((duration + 999) / 1000) * 1000
}

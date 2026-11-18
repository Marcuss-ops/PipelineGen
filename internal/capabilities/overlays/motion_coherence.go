package overlays

// Directional coherence ("coherence seed"). Each lane used to shuffle its own
// motion pool, so one video could enter from the right in one phrase, from the
// left in the next, and move the camera back and forth. The run's motion seed
// now also picks one dominant horizontal direction; directional motions that
// point the other way are excluded from the lanes that use this filter, while
// neutral motions remain available. The choice stays a pure function of the
// seed, so retries and identical inputs produce bit-identical plans.
const (
	CoherenceDirectionLeft  = "left"
	CoherenceDirectionRight = "right"
)

// coherenceSeedMarker separates an explicit coherence seed from the run seed
// inside the seed string every selector receives. Selectors keep their single
// jobID parameter; the marker carries the explicit coherence seed through it.
const coherenceSeedMarker = "\x00coherence:"

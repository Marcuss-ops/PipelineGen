package overlays

import (
	"hash/fnv"
	"strings"
)

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

// WithCoherenceSeed appends an explicit coherence seed to a run seed. An empty
// coherence seed returns the run seed unchanged, so omitting the payload field
// keeps every plan byte-identical to the run-seed default.
func WithCoherenceSeed(runSeed, coherenceSeed string) string {
	coherenceSeed = strings.TrimSpace(coherenceSeed)
	if coherenceSeed == "" {
		return runSeed
	}
	return runSeed + coherenceSeedMarker + coherenceSeed
}

// coherenceKey returns the seed that determines the dominant direction: the
// explicit coherence seed when present, otherwise the full run seed.
func coherenceKey(seed string) string {
	if index := strings.LastIndex(seed, coherenceSeedMarker); index >= 0 {
		return seed[index+len(coherenceSeedMarker):]
	}
	return seed
}

// CoherenceDominantDirection returns the run's dominant horizontal direction.
// It is deterministic in seed. An empty seed yields "" (coherence disabled).
func CoherenceDominantDirection(seed string) string {
	seed = strings.TrimSpace(coherenceKey(seed))
	if seed == "" {
		return ""
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte("coherence\x00" + seed))
	if hasher.Sum32()%2 == 0 {
		return CoherenceDirectionLeft
	}
	return CoherenceDirectionRight
}

// motionHorizontalDirection classifies a catalog motion id by its horizontal
// token. "left" and "right" are matched as whole underscore-separated tokens,
// so "slide_from_right" and "image_slide_left_reveal" are both recognised. A
// motion naming both sides, or neither, is neutral ("").
func motionHorizontalDirection(id string) string {
	hasLeft, hasRight := false, false
	for _, token := range strings.Split(id, "_") {
		switch token {
		case "left":
			hasLeft = true
		case "right":
			hasRight = true
		}
	}
	switch {
	case hasLeft && !hasRight:
		return CoherenceDirectionLeft
	case hasRight && !hasLeft:
		return CoherenceDirectionRight
	}
	return ""
}

// oppositeDirection returns the horizontal direction opposing dominant, or ""
// when there is no dominant direction.
func oppositeDirection(dominant string) string {
	switch dominant {
	case CoherenceDirectionLeft:
		return CoherenceDirectionRight
	case CoherenceDirectionRight:
		return CoherenceDirectionLeft
	}
	return ""
}

// coherentMotionPool keeps the dominant-direction and neutral motions of a
// pool and drops the opposite-direction ones. The filter only applies when the
// pool contains at least one dominant-direction motion; otherwise the pool is
// returned unchanged so no lane is ever left empty.
func coherentMotionPool(pool []string, runSeed string) []string {
	dominant := CoherenceDominantDirection(runSeed)
	if dominant == "" || len(pool) == 0 {
		return pool
	}
	hasDominant := false
	for _, id := range pool {
		if motionHorizontalDirection(id) == dominant {
			hasDominant = true
			break
		}
	}
	if !hasDominant {
		return pool
	}
	out := make([]string, 0, len(pool))
	for _, id := range pool {
		if direction := motionHorizontalDirection(id); direction == "" || direction == dominant {
			out = append(out, id)
		}
	}
	return out
}

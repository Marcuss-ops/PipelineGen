// Package overlays — preset_motion_pools.go: the certified, read-only motion
// pool accessors for the phrase/text lanes.
//
// Split out of preset_motion_selection.go to respect max_lines_per_file_strict
// (same package, no behavior change).
package overlays

import "strconv"

// ShortPhraseMotionsByWord returns the certified short-phrase entrance pool
// keyed by the phrase's word count ("1".."5"). A short phrase of two words
// cannot render the same entrance as a five-word one: the registry owns one
// bucket per length, and the planner rotates the bucket that matches the item
// it is compiling. Typewriter recipes are word-count agnostic and therefore
// appear in every bucket.
func ShortPhraseMotionsByWord() map[string][]string {
	out := make(map[string][]string, len(shortPhraseMotionCandidates))
	for words, pool := range shortPhraseMotionCandidates {
		if len(pool) == 0 {
			continue
		}
		out[strconv.Itoa(words)] = append([]string(nil), pool...)
	}
	return out
}

// LongPhraseMotions returns the certified long-phrase entrance pool (6+ words).
// The long lane has no per-length buckets: the registry owns one family, and the
// planner rotates it with the same deterministic sampler the short lane uses.
// Without it every long phrase carries an empty motion and the renderer repeats
// the preset's single entrance for all of them.
func LongPhraseMotions() []string {
	return append([]string(nil), longPhraseMotionCandidates...)
}

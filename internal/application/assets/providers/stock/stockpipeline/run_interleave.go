package stockpipeline

// InterleaveClips takes a slice of clip groups (one slice per source
// video), shuffles each source-group internally to preserve dynamic
// randomness, then interleaves the source-groups step-by-step so the
// final output rotates among sources.
//
// FASE 2.4 (July 2026): the legacy parallel-array signature
// ([][]string clipsBySource, [][]string titlesBySource, [][]string sourceIDsBySource)
// is collapsed into a single [][]Clip input. Each Clip carries Path /
// Title / SourceID / Status / SizeBytes / DurationSec / Err; the caller
// does the per-source slices verbatim and InterleaveClips no longer
// has to reason about cross-slice index alignment (the previous
// implementation relied on len(clipsBySource[i])==len(titlesBySource[i])==
// len(sourceIDsBySource[i]) for every i — a fragile contract that
// produced silent misalignment on partial-success batches).
//
// Behaviour preserved:
//   - shuffle WITHIN each source group before interleaving (preserves
//     dynamic randomness that doesn't break the per-source clip
//     ordering aesthetic)
//   - interleave ACROSS source groups: round-robin over the maximum
//     group length, appending per-step entries from each group until
//     that source is exhausted
//   - non-Succeeded Clips are filtered out of the output (Succeeded()
//     predicate aligns with downstream renderChunk's filter stage)
//   - empty input groups are silently skipped
func InterleaveClips(clipsBySource [][]Clip) []Clip {
	active := make([][]Clip, 0, len(clipsBySource))
	for _, list := range clipsBySource {
		if len(list) == 0 {
			continue
		}
		shuffled := make([]Clip, len(list))
		copy(shuffled, list)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		// Drop non-Succeeded entries from the active pool — the
		// legacy output fed only produced clips into the render
		// chain so this normalisation preserves the historical
		// behaviour while staying self-consistent if a caller
		// happens to feed Failed entries (e.g. for debugging).
		filtered := shuffled[:0]
		for _, c := range shuffled {
			if c.Succeeded() {
				filtered = append(filtered, c)
			}
		}
		if len(filtered) > 0 {
			active = append(active, filtered)
		}
	}

	var out []Clip
	maxLen := 0
	for _, list := range active {
		if len(list) > maxLen {
			maxLen = len(list)
		}
	}
	for step := 0; step < maxLen; step++ {
		for srcIdx := 0; srcIdx < len(active); srcIdx++ {
			if step < len(active[srcIdx]) {
				out = append(out, active[srcIdx][step])
			}
		}
	}
	return out
}

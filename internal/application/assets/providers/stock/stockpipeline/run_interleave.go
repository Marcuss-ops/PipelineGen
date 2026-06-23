package stockpipeline

// InterleaveClips takes slices of clips and titles grouped by video source, shuffles them internally,
// and interleaves them source-by-source.
func InterleaveClips(clipsBySource [][]string, titlesBySource [][]string, sourceIDsBySource [][]string) ([]string, []string, []string) {
	var activeClips [][]string
	var activeTitles [][]string
	var activeSourceIDs [][]string
	for idx, list := range clipsBySource {
		if len(list) > 0 {
			// Shuffle within each source first to preserve dynamic randomness
			shuffledClips := make([]string, len(list))
			copy(shuffledClips, list)
			shuffledTitles := make([]string, len(titlesBySource[idx]))
			copy(shuffledTitles, titlesBySource[idx])
			shuffledSourceIDs := make([]string, len(sourceIDsBySource[idx]))
			copy(shuffledSourceIDs, sourceIDsBySource[idx])

			rng.Shuffle(len(shuffledClips), func(i, j int) {
				shuffledClips[i], shuffledClips[j] = shuffledClips[j], shuffledClips[i]
				shuffledTitles[i], shuffledTitles[j] = shuffledTitles[j], shuffledTitles[i]
				if i < len(shuffledSourceIDs) && j < len(shuffledSourceIDs) {
					shuffledSourceIDs[i], shuffledSourceIDs[j] = shuffledSourceIDs[j], shuffledSourceIDs[i]
				}
			})
			activeClips = append(activeClips, shuffledClips)
			activeTitles = append(activeTitles, shuffledTitles)
			activeSourceIDs = append(activeSourceIDs, shuffledSourceIDs)
		}
	}

	var processedClips []string
	var clipTitles []string
	var sourceIDs []string

	maxLen := 0
	for _, list := range activeClips {
		if len(list) > maxLen {
			maxLen = len(list)
		}
	}

	for step := 0; step < maxLen; step++ {
		for srcIdx := 0; srcIdx < len(activeClips); srcIdx++ {
			if step < len(activeClips[srcIdx]) {
				processedClips = append(processedClips, activeClips[srcIdx][step])
				clipTitles = append(clipTitles, activeTitles[srcIdx][step])
				if step < len(activeSourceIDs[srcIdx]) {
					sourceIDs = append(sourceIDs, activeSourceIDs[srcIdx][step])
				}
			}
		}
	}
	return processedClips, clipTitles, sourceIDs
}

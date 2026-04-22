package scriptdocs

// associateFullArtlistFast builds Artlist-only associations using the modular pipeline.
// It now supports dynamic search fallback to ensure we find the best clips.
func (s *ScriptDocService) associateFullArtlistFast(frasi []string, topic string) []ClipAssociation {
	if s.artlistIndex == nil || len(frasi) == 0 {
		return nil
	}

	// We use the standard pipeline but force ARTLIST focus via options
	usedClipIDs := make(map[string]bool)
	
	// We allow fresh search (top 50) and JIT for better variety
	associations := s.associateClipsWithDedupOptions(
		frasi, 
		usedClipIDs, 
		StockFolder{Name: "Artlist", ID: "artlist"}, 
		topic, 
		true, // allowFreshSearch: YES, to avoid bias and get new clips
		true, // allowJIT: YES, for final fallbacks
	)

	// Filter to ensure we mostly return ARTLIST/DYNAMIC types for this mode
	var filtered []ClipAssociation
	for _, assoc := range associations {
		if assoc.Type == "ARTLIST" || assoc.Type == "DYNAMIC" || assoc.Type == "JIT" {
			filtered = append(filtered, assoc)
		}
	}

	return filtered
}

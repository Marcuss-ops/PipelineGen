package scriptdocs

// ClipConcept maps multilingual keywords to an Artlist search term.
type ClipConcept struct {
	Keywords []string `json:"keywords"`
	Term     string   `json:"term"`
	BaseConf float64  `json:"base_conf"`
}

type clipConcept = ClipConcept

func GetConceptMap() []ClipConcept { return conceptMap }

const (
	minDirectAssociationConfidence = 0.70
	forceDynamicSearchConfidence   = 0.90
)

// associateClips associates each important phrase with one primary clip choice.
func (s *ScriptDocService) associateClips(frasi []string, stockFolder StockFolder, topic string) []ClipAssociation {
	s.folderTopic = stockFolder.Name
	return s.associateClipsInternal(frasi, stockFolder, topic, true, true)
}

// associateClipsForDocs resolves clips without triggering fresh search or JIT side effects.
func (s *ScriptDocService) associateClipsForDocs(frasi []string, stockFolder StockFolder, topic string) []ClipAssociation {
	s.folderTopic = stockFolder.Name
	return s.associateClipsInternal(frasi, stockFolder, topic, false, false)
}

func (s *ScriptDocService) associateClipsInternal(frasi []string, stockFolder StockFolder, topic string, allowFreshSearch bool, allowJIT bool) []ClipAssociation {
	if normalizeAssociationMode(s.currentAssociationMode) == AssociationModeFullArtlist {
		return s.associateFullArtlistFast(frasi, topic)
	}
	usedClipIDs := make(map[string]bool)
	return s.associateClipsWithDedupOptions(frasi, usedClipIDs, stockFolder, topic, allowFreshSearch, allowJIT)
}

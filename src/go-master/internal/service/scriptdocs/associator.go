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
	if normalizeAssociationMode(s.currentAssociationMode) == AssociationModeFullArtlist {
		return s.associateFullArtlistFast(frasi, topic)
	}
	usedClipIDs := make(map[string]bool)
	return s.associateClipsWithDedup(frasi, usedClipIDs, stockFolder, topic)
}

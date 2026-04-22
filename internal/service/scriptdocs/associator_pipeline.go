package scriptdocs

import (
	"context"
	"math/rand"
	"strings"
	"time"

	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/translation"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// associateClipsWithDedupOptions associates each important phrase with one primary clip choice.
func (s *ScriptDocService) associateClipsWithDedupOptions(frasi []string, usedClipIDs map[string]bool, stockFolder StockFolder, topic string, allowFreshSearch bool, allowJIT bool) []ClipAssociation {
	const minConfidence = 0.32
	var associations []ClipAssociation
	translator := translation.NewClipSearchTranslator()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	artlistRoundRobin := make(map[string]int)
	visualHistory := make([]string, 0, 3)

	// Pre-load data
	dynamicClips := s.getDynamicClips()
	stockClipByID, folderPathByID := s.getStockData()

	for _, frase := range frasi {
		assoc := s.findBestAssociation(frase, topic, usedClipIDs, dynamicClips, stockClipByID, folderPathByID, translator, rng, artlistRoundRobin, allowFreshSearch, allowJIT, visualHistory)
		if assoc != nil && (assoc.Confidence >= minConfidence || assoc.Type == "STOCK") {
			associations = append(associations, *assoc)
			
			// Update visual history for pacing
			concept := ""
			if assoc.Clip != nil {
				concept = assoc.Clip.Term
			} else if assoc.MatchedKeyword != "" {
				concept = assoc.MatchedKeyword
			}
			if concept != "" {
				visualHistory = append(visualHistory, concept)
				if len(visualHistory) > 3 {
					visualHistory = visualHistory[1:]
				}
			}
		}
	}

	logger.Info("Clip association completed",
		zap.Int("phrases", len(frasi)),
		zap.Int("associations", len(associations)),
	)
	return associations
}

// findBestAssociation is the central brain for clip matching logic.
func (s *ScriptDocService) findBestAssociation(frase, topic string, usedClipIDs map[string]bool, dynamicClips []clipsearch.SearchResult, stockClips map[string]stockdb.StockClipEntry, folderPaths map[string]string, translator *translation.ClipSearchTranslator, rng *rand.Rand, artlistRoundRobin map[string]int, allowFreshSearch bool, allowJIT bool, visualHistory []string) *ClipAssociation {
	s.currentTopic = topic
	trace := newAssetResolution(
		"scriptdocs.clip",
		"dynamic-cache",
		"stockdb",
		"artlist",
		"semantic-tags",
		"dynamic-search",
		"jit-stock",
	).addNote("modular fallback pipeline")

	ctx := context.Background()

	// 1. Semantic Tagging & Director Intent (The "Director" Logic)
	// Extract 3 visual tags using LLM for precision
	visualTags := s.extractSemanticVisualTags(ctx, frase, topic)
	visualSearchTerm := ""
	if len(visualTags) > 0 {
		visualSearchTerm = visualTags[0] // Primary director suggestion
	}

	// 2. Exact Match & Concept Map Scoring
	scores := s.scoreConcepts(ctx, frase, frase+" "+topic, visualHistory, visualSearchTerm)
	
	// Try Top 3 Concept Hits
	for i, best := range scores {
		if i >= 3 {
			break
		}
		// Try Cache/DB first (High Confidence)
		if assoc := s.tryExistingMatch(frase, best.bestKeyword, usedClipIDs, dynamicClips, stockClips); assoc != nil {
			assoc.Resolution = cloneAssetResolution(trace).withOutcome("dynamic-cache-or-stockdb", "direct concept hit", false)
			return assoc
		}
		// Try Artlist curated DB (Medium-High Confidence)
		if assoc := s.tryArtlistMatch(frase, topic, best.cm.Term, best.cm.BaseConf, best.score, usedClipIDs, translator, rng, artlistRoundRobin); assoc != nil {
			assoc.Resolution = cloneAssetResolution(trace).withOutcome("artlist", "concept hit matched curated artlist", false)
			return assoc
		}
	}

	// 3. Semantic Visual Tags Fallback (New Precision Layer)
	for _, tag := range visualTags {
		if assoc := s.tryExistingMatch(frase, tag, usedClipIDs, dynamicClips, stockClips); assoc != nil {
			assoc.Confidence = 0.85
			assoc.Resolution = cloneAssetResolution(trace).withOutcome("dynamic-cache-or-stockdb", "semantic tag matched existing clip", false)
			return assoc
		}
		if assoc := s.tryArtlistMatch(frase, topic, tag, 0.78, 3, usedClipIDs, translator, rng, artlistRoundRobin); assoc != nil {
			assoc.Resolution = cloneAssetResolution(trace).withOutcome("artlist", "semantic tag matched artlist", false)
			return assoc
		}
	}

	// 4. Dynamic Live Search (Top 50 Artlist)
	if allowFreshSearch && s.clipSearch != nil && len(visualTags) > 0 {
		// We use visualTags for dynamic search to be more precise than raw script words
		results, err := s.clipSearch.SearchClipsWithOptions(ctx, visualTags, clipsearch.SearchOptions{
			ForceFresh: true,
			MaxClipsPerKeyword: 3, // Search top 50 in reality but pick best 3
		})
		if err == nil && len(results) > 0 {
			dc := pickFirstArtlistDynamicResult(results, stockClips, folderPaths)
			if dc != nil {
				clip := buildDynamicArtlistClip(*dc, visualTags[0])
				usedClipIDs[clip.URL] = true
				return &ClipAssociation{
					Phrase:         frase,
					Type:           "ARTLIST",
					Clip:           &clip,
					Confidence:     0.75,
					MatchedKeyword: visualTags[0],
					Resolution:     cloneAssetResolution(trace).withOutcome("dynamic-search", "live artlist search produced results", false),
				}
			}
		}
	}

	// 5. JIT & Stock Folder Fallback
	if allowJIT && s.jitResolver != nil && s.allowJITFallback() {
		if res := s.resolveJITClip(ctx, topic, frase, 0, 0); res != nil {
			assoc := s.jitResultToAssociation(res)
			if assoc != nil {
				assoc.Resolution = cloneAssetResolution(trace).withOutcome(strings.TrimSpace(res.SourceKind), "jit fallback created a new asset", res.Cached)
				return assoc
			}
		}
	}

	// Final generic Stock fallback
	stockFolderObj, ok := s.stockFolders[normalizeTopicKey(topic)]
	if !ok {
		stockFolderObj = StockFolder{Name: "Stock", ID: "root"}
	}

	return &ClipAssociation{
		Phrase:      frase,
		Type:        "STOCK",
		StockFolder: &stockFolderObj,
		Confidence:  0.50,
		Resolution:  cloneAssetResolution(trace).withOutcome("stock", "generic fallback", false),
	}
}

func (s *ScriptDocService) getDynamicClips() []clipsearch.SearchResult {
	s.dynamicClipsMu.Lock()
	defer s.dynamicClipsMu.Unlock()
	res := make([]clipsearch.SearchResult, len(s.dynamicClips))
	copy(res, s.dynamicClips)
	return res
}

func (s *ScriptDocService) getStockData() (map[string]stockdb.StockClipEntry, map[string]string) {
	stockClipByID := make(map[string]stockdb.StockClipEntry)
	folderPathByID := make(map[string]string)
	if s.stockDB == nil {
		return stockClipByID, folderPathByID
	}

	if allFolders, err := s.stockDB.GetAllFolders(); err == nil {
		for _, f := range allFolders {
			folderPathByID[f.DriveID] = f.FullPath
		}
	}
	if allClips, err := s.stockDB.GetAllClips(); err == nil {
		for _, c := range allClips {
			stockClipByID[c.ClipID] = c
		}
	}
	return stockClipByID, folderPathByID
}

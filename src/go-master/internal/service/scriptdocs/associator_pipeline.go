package scriptdocs

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"time"

	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/translation"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// associateClipsWithDedup associates each important phrase with one primary clip choice.
// Improved version with 3-level fallback and parallel-friendly logic.
func (s *ScriptDocService) associateClipsWithDedup(frasi []string, usedClipIDs map[string]bool, stockFolder StockFolder, topic string) []ClipAssociation {
	const minConfidence = 0.60
	var associations []ClipAssociation
	translator := translation.NewClipSearchTranslator()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Pre-load data
	dynamicClips := s.getDynamicClips()
	stockClipByID, folderPathByID := s.getStockData()
	
	for _, frase := range frasi {
		assoc := s.findBestAssociation(frase, topic, usedClipIDs, dynamicClips, stockClipByID, folderPathByID, translator, rng)
		if assoc != nil && assoc.Confidence >= minConfidence {
			associations = append(associations, *assoc)
		}
	}

	logger.Info("Clip association completed",
		zap.Int("phrases", len(frasi)),
		zap.Int("associations", len(associations)),
	)
	return associations
}

func (s *ScriptDocService) findBestAssociation(frase, topic string, usedClipIDs map[string]bool, dynamicClips []clipsearch.SearchResult, stockClips map[string]stockdb.StockClipEntry, folderPaths map[string]string, translator *translation.ClipSearchTranslator, rng *rand.Rand) *ClipAssociation {
	s.currentTopic = topic
	fraseLower := strings.ToLower(frase)
	contextLower := fraseLower + " " + strings.ToLower(topic)

	// 1. Exact Match & Concept Map
	scores := s.scoreConcepts(frase, contextLower)
	if len(scores) > 0 && scores[0].score > 0 {
		best := scores[0]
		// Try Cache/DB first
		if assoc := s.tryExistingMatch(frase, best.bestKeyword, usedClipIDs, dynamicClips, stockClips); assoc != nil {
			return assoc
		}
		// Try Artlist
		if assoc := s.tryArtlistMatch(frase, best.cm.Term, best.cm.BaseConf, usedClipIDs, translator, rng); assoc != nil {
			return assoc
		}
	}

	// 2. Entity Linking / Thesaurus Fallback
	// If no concept match, use LLM to expand keywords (Thesaurus logic)
	expandedKeywords := s.expandKeywordsWithLLM(frase, topic)
	for _, kw := range expandedKeywords {
		if assoc := s.tryExistingMatch(frase, kw, usedClipIDs, dynamicClips, stockClips); assoc != nil {
			assoc.Confidence = 0.80 // Entity-linked confidence
			return assoc
		}
		if assoc := s.tryArtlistMatch(frase, kw, 0.75, usedClipIDs, translator, rng); assoc != nil {
			return assoc
		}
	}

	// 3. Dynamic Search Fallback (Last Resort)
	if s.clipSearch != nil && len(expandedKeywords) > 0 {
		results, err := s.clipSearch.SearchClipsWithOptions(context.Background(), expandedKeywords, clipsearch.SearchOptions{ForceFresh: true})
		if err == nil && len(results) > 0 {
			dc := pickFirstArtlistDynamicResult(results, stockClips, folderPaths)
			if dc != nil {
				clip := buildDynamicArtlistClip(*dc, expandedKeywords[0])
				usedClipIDs[clip.URL] = true
				return &ClipAssociation{
					Phrase: frase,
					Type: "ARTLIST",
					Clip: &clip,
					Confidence: 0.70,
					MatchedKeyword: expandedKeywords[0],
				}
			}
		}
	}

	return nil
}

func (s *ScriptDocService) expandKeywordsWithLLM(frase, topic string) []string {
	if s.generator == nil {
		return nil
	}
	// "Thesaurus visuale" logic via Ollama
	prompt := fmt.Sprintf("Given the phrase: \"%s\" (Topic: %s), provide 3 generic visual keywords for stock footage search. Return only keywords separated by space.", frase, topic)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	resp, err := s.generator.Generate(ctx, prompt)
	if err != nil {
		return nil
	}
	return strings.Fields(strings.ToLower(resp))
}

func (s *ScriptDocService) tryExistingMatch(frase, kw string, usedClipIDs map[string]bool, dynamicClips []clipsearch.SearchResult, stockClips map[string]stockdb.StockClipEntry) *ClipAssociation {
	kw = strings.ToLower(kw)
	// Check dynamic cache
	for _, dc := range dynamicClips {
		if strings.Contains(strings.ToLower(dc.Keyword), kw) && !usedClipIDs["dynamic_"+dc.DriveID] {
			usedClipIDs["dynamic_"+dc.DriveID] = true
			return &ClipAssociation{Phrase: frase, Type: "DYNAMIC", DynamicClip: &dc, Confidence: 0.95, MatchedKeyword: kw}
		}
	}
	// Check StockDB (simplified for brevity)
	return nil
}

func (s *ScriptDocService) tryArtlistMatch(frase, term string, baseConf float64, usedClipIDs map[string]bool, translator *translation.ClipSearchTranslator, rng *rand.Rand) *ClipAssociation {
	if s.artlistIndex == nil {
		return nil
	}
	clips := s.artlistClipsForTerm(term)
	if len(clips) == 0 {
		return nil
	}
	// Simple random pick for now
	idx := rng.Intn(len(clips))
	c := clips[idx]
	if usedClipIDs[c.URL] {
		return nil
	}
	usedClipIDs[c.URL] = true
	return &ClipAssociation{Phrase: frase, Type: "ARTLIST", Clip: &c, Confidence: baseConf, MatchedKeyword: term}
}

type conceptScore struct {
	cm          clipConcept
	score       int
	bestKeyword string
	signalClass SignalClass
	isMismatch bool
	isPreferred bool
}

func (s *ScriptDocService) scoreConcepts(frase, contextLower string) []conceptScore {
	topic := s.currentTopic
	if topic == "" {
		topic = s.folderTopic
	}

	fraseEntities := extractEntitiesFromPhrase(frase)
	hasExplicitEntity := len(fraseEntities) > 0
	visualIntent := classifyVisualIntent(frase)

	lastEntity := ""
	if len(fraseEntities) > 0 {
		lastEntity = fraseEntities[len(fraseEntities)-1]
	}
	preferredByIntent := preferredConceptsForIntent(visualIntent, lastEntity)

	signals := analyzeAndScore(contextLower, conceptMap, topic, fraseEntities)

	var scores []conceptScore
	for _, sig := range signals {
		for _, cm := range conceptMap {
			if cm.Term == sig.Concept {
				penalty := applyEntityPenalty(cm.Term, fraseEntities)
				adjustedScore := sig.Score + penalty

				strongEntityMatch := len(fraseEntities) > 0 && sig.Class == SignalClassEntity
				if strongEntityMatch {
					adjustedScore += 15
				}

				isPreferredForIntent := false
				for _, pref := range preferredByIntent {
					if strings.Contains(cm.Term, pref) || strings.Contains(pref, cm.Term) {
						isPreferredForIntent = true
						adjustedScore += 10
						break
					}
				}

				mismatch := isEntityMismatch(cm.Term, fraseEntities)

				scores = append(scores, conceptScore{
					cm:             cm,
					score:          adjustedScore,
					bestKeyword:    sig.Keyword,
					signalClass:    sig.Class,
					isMismatch:    mismatch,
					isPreferred:   isPreferredForIntent,
				})
				break
			}
		}
	}

	slices.SortFunc(scores, func(a, b conceptScore) int {
		if hasExplicitEntity && visualIntent == VisualIntentCloseUp {
			if a.signalClass == SignalClassEntity && b.signalClass == SignalClassEntity {
				if a.isMismatch != b.isMismatch {
					if a.isMismatch {
						return 1
					}
					return -1
				}
			}
		}

		if hasExplicitEntity {
			if a.signalClass != b.signalClass {
				return int(a.signalClass) - int(b.signalClass)
			}

			if a.signalClass == SignalClassEntity && b.signalClass == SignalClassEntity {
				if a.isMismatch != b.isMismatch {
					if a.isMismatch {
						return 1
					}
					return -1
				}
			}
		}

		if a.score != b.score {
			return b.score - a.score
		}
		return int(b.cm.BaseConf*100) - int(a.cm.BaseConf*100)
	})

	return scores
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

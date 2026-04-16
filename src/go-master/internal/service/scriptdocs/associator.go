package scriptdocs

import (
	"strings"

	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/stockdb"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// ClipConcept maps multilingual keywords to an Artlist search term.
type ClipConcept struct {
	Keywords []string `json:"keywords"`
	Term     string   `json:"term"`
	BaseConf float64  `json:"base_conf"`
}

// clipConcept (alias for internal use)
type clipConcept = ClipConcept

// conceptMap is defined in conceptmap_data.go (multilingual keyword → Artlist term mapping).

// GetConceptMap returns the full concept map for use by external packages.
func GetConceptMap() []ClipConcept {
	return conceptMap
}

// associateClips associates each important phrase with a clip, ensuring NO duplicates.
func (s *ScriptDocService) associateClips(frasi []string) []ClipAssociation {
	usedClipIDs := make(map[string]bool)
	return s.associateClipsWithDedup(frasi, usedClipIDs)
}

// scoreConceptForPhrase calculates how many keywords match in the phrase for a concept.
// Returns the match count and the best keyword matched.
func scoreConceptForPhrase(fraseLower string, cm clipConcept) (int, string) {
	bestKeyword := ""
	bestLen := 0
	matchCount := 0

	for _, kw := range cm.Keywords {
		kwLower := strings.ToLower(kw)
		if strings.Contains(fraseLower, kwLower) {
			matchCount++
			// Track longest keyword match (more specific = better)
			if len(kw) > bestLen {
				bestLen = len(kw)
				bestKeyword = kw
			}
		}
	}

	return matchCount, bestKeyword
}

// associateClipsWithDedup associates clips ensuring no clip is used twice.
// Priority: 1) Dynamic clips (from clipsearch) → 2) StockDB → 3) Artlist
// Uses weighted scoring to pick best concept per phrase.
func (s *ScriptDocService) associateClipsWithDedup(frasi []string, usedClipIDs map[string]bool) []ClipAssociation {
	// Track how many clips per term have been used (for round-robin)
	termUsageCount := make(map[string]int)
	var associations []ClipAssociation

	// Get dynamic clips (from clipsearch) — these are the most relevant
	s.dynamicClipsMu.Lock()
	dynamicClips := make([]clipsearch.SearchResult, len(s.dynamicClips))
	copy(dynamicClips, s.dynamicClips)
	s.dynamicClipsMu.Unlock()

	// Build dynamic clip lookup by keyword
	dynamicClipByKeyword := make(map[string]clipsearch.SearchResult)
	for _, dc := range dynamicClips {
		dynamicClipByKeyword[strings.ToLower(dc.Keyword)] = dc
	}

	// Try to get available clips from DB first (deduplicated)
	var dbClips []stockdb.StockClipEntry
	if s.stockDB != nil {
		usedIDs := make([]string, 0, len(usedClipIDs))
		for id := range usedClipIDs {
			usedIDs = append(usedIDs, id)
		}

		// Search clips by tags matching our concepts
		conceptTags := []string{"people", "city", "technology", "crowd", "arrest", "tech", "fight", "boxing", "gym", "sport"}
		allClips, err := s.stockDB.SearchClipsByTags(conceptTags)
		if err == nil {
			// Filter out already used clips
			for _, c := range allClips {
				if !usedClipIDs[c.ClipID] {
					dbClips = append(dbClips, c)
				}
			}
		}
	}

	// Build a map of available DB clips by tag for fast lookup
	dbClipByTag := make(map[string][]stockdb.StockClipEntry)
	for _, clip := range dbClips {
		tags := strings.ToLower(strings.Join(clip.Tags, ","))
		for _, tag := range strings.Split(tags, ",") {
			tag = strings.TrimSpace(tag)
			dbClipByTag[tag] = append(dbClipByTag[tag], clip)
		}
	}

	dbClipIndex := make(map[string]int) // Track which DB clip to use next per tag

	for _, frase := range frasi {
		fraseLower := strings.ToLower(frase)

		// Step 1: Calculate score for EACH concept to find best match
		type conceptScore struct {
			cm          clipConcept
			score       int
			bestKeyword string
		}
		var scores []conceptScore

		for _, cm := range conceptMap {
			matchCount, bestKw := scoreConceptForPhrase(fraseLower, cm)
			if matchCount > 0 {
				scores = append(scores, conceptScore{cm: cm, score: matchCount, bestKeyword: bestKw})
			}
		}

		// Sort by score descending (highest keyword matches = best concept)
		// Secondary sort: prefer concepts with higher base confidence
		for i := 0; i < len(scores)-1; i++ {
			for j := i + 1; j < len(scores); j++ {
				if scores[j].score > scores[i].score ||
					(scores[j].score == scores[i].score && scores[j].cm.BaseConf > scores[i].cm.BaseConf) {
					scores[i], scores[j] = scores[j], scores[i]
				}
			}
		}

		// Step 2: Try to associate using concepts in order of score
		for _, cs := range scores {
			cm := cs.cm
			kw := cs.bestKeyword

			// 1. Try dynamic clips first (from clipsearch) — most relevant
			if dc, ok := dynamicClipByKeyword[kw]; ok {
				if !usedClipIDs["dynamic_"+dc.DriveID] {
					usedClipIDs["dynamic_"+dc.DriveID] = true

					associations = append(associations, ClipAssociation{
						Phrase:         frase,
						Type:           "DYNAMIC",
						DynamicClip:    &dc,
						Confidence:     cm.BaseConf + 0.1, // Higher priority
						MatchedKeyword: kw,
					})
					break
				}
			}

			// 2. Try to find a DB clip (deduplicated)
			if clips, ok := dbClipByTag[kw]; ok && len(clips) > 0 {
				idx := dbClipIndex[kw] % len(clips)
				clip := clips[idx]
				dbClipIndex[kw]++

				// Mark as used
				usedClipIDs[clip.ClipID] = true

				associations = append(associations, ClipAssociation{
					Phrase:         frase,
					Type:           "STOCK_DB",
					ClipDB:         &clip,
					Confidence:     cm.BaseConf,
					MatchedKeyword: kw,
				})
				break
			}

			// 3. Fall back to Artlist
			if s.artlistIndex != nil {
				if clips, ok := s.artlistIndex.ByTerm[cm.Term]; ok && len(clips) > 0 {
					usageIdx := termUsageCount[cm.Term] % len(clips)
					clip := clips[usageIdx]
					termUsageCount[cm.Term]++

					// Mark as used by Drive ID
					if clip.FolderID != "" {
						usedClipIDs[clip.FolderID+"_"+clip.Name] = true
					}

					associations = append(associations, ClipAssociation{
						Phrase:         frase,
						Type:           "ARTLIST",
						Clip:           &clip,
						Confidence:     cm.BaseConf,
						MatchedKeyword: kw,
					})
					break
				}
			}
		}
	}

	logger.Info("Clip association completed",
		zap.Int("phrases", len(frasi)),
		zap.Int("associations", len(associations)),
		zap.Int("dynamic_clips", len(dynamicClips)),
		zap.Int("db_clips", len(dbClips)),
	)

	return associations
}

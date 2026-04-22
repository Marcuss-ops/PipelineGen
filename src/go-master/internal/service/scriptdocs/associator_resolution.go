package scriptdocs

import (
	"math/rand"
	"strings"

	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/translation"
)

func (s *ScriptDocService) tryExistingMatch(frase, kw string, usedClipIDs map[string]bool, dynamicClips []clipsearch.SearchResult, stockClips map[string]stockdb.StockClipEntry) *ClipAssociation {
	kw = strings.ToLower(kw)
	// 1. Check dynamic cache (already downloaded/processed)
	for _, dc := range dynamicClips {
		if (strings.Contains(strings.ToLower(dc.Keyword), kw) || strings.Contains(strings.ToLower(dc.Filename), kw)) && !usedClipIDs["dynamic_"+dc.DriveID] {
			usedClipIDs["dynamic_"+dc.DriveID] = true
			copyDc := dc
			return &ClipAssociation{
				Phrase:         frase,
				Type:           "DYNAMIC",
				DynamicClip:    &copyDc,
				Confidence:     0.95,
				MatchedKeyword: kw,
			}
		}
	}
	// 2. Check StockDB (previously indexed)
	for id, entry := range stockClips {
		if usedClipIDs[id] {
			continue
		}
		for _, tag := range entry.Tags {
			if strings.EqualFold(tag, kw) {
				usedClipIDs[id] = true
				copyEntry := entry
				return &ClipAssociation{
					Phrase:         frase,
					Type:           "STOCK",
					ClipDB:         &copyEntry,
					Confidence:     0.90,
					MatchedKeyword: kw,
				}
			}
		}
	}
	return nil
}

func (s *ScriptDocService) tryArtlistMatch(frase, topic, term string, baseConf float64, conceptScore int, usedClipIDs map[string]bool, translator *translation.ClipSearchTranslator, rng *rand.Rand, artlistRoundRobin map[string]int) *ClipAssociation {
	if s.artlistIndex == nil {
		return nil
	}
	
	term = normalizeKeyword(term)
	if term == "" {
		return nil
	}

	// Direct curated lookup
	if clips := s.artlistClipsForTerm(term); len(clips) > 0 {
		if clip, ok := pickArtlistClipRoundRobin(term, clips, usedClipIDs, artlistRoundRobin, func(string) bool { return true }); ok && clip != nil {
			usedClipIDs[clip.URL] = true
			return &ClipAssociation{
				Phrase:         frase,
				Type:           "ARTLIST",
				Clip:           clip,
				Confidence:     artlistConfidence(baseConf, conceptScore, frase, topic, term, []string{term}),
				MatchedKeyword: term,
			}
		}
	}

	// Translated lookup
	if translator != nil {
		if tr := translator.TranslateKeywords([]string{term}); len(tr) > 0 {
			translated := normalizeKeyword(tr[0])
			if translated != "" && translated != term {
				if clips := s.artlistClipsForTerm(translated); len(clips) > 0 {
					if clip, ok := pickArtlistClipRoundRobin(translated, clips, usedClipIDs, artlistRoundRobin, func(string) bool { return true }); ok && clip != nil {
						usedClipIDs[clip.URL] = true
						return &ClipAssociation{
							Phrase:         frase,
							Type:           "ARTLIST",
							Clip:           clip,
							Confidence:     artlistConfidence(baseConf, conceptScore, frase, topic, translated, []string{translated}),
							MatchedKeyword: translated,
						}
					}
				}
			}
		}
	}

	// NEW: Fuzzy Search in Local DB
	// If no direct hit, search the local index for the best semantic matches
	results := s.artlistIndex.Search([]string{term}, 5)
	if len(results) > 0 {
		for _, clip := range results {
			if usedClipIDs[clip.URL] {
				continue
			}
			usedClipIDs[clip.URL] = true
			copyClip := clip
			return &ClipAssociation{
				Phrase:         frase,
				Type:           "ARTLIST",
				Clip:           &copyClip,
				Confidence:     artlistConfidence(baseConf-0.05, conceptScore, frase, topic, term, []string{term}),
				MatchedKeyword: term,
			}
		}
	}

	return nil
}

func (s *ScriptDocService) artlistClipsForTerm(term string) []ArtlistClip {
	if s.artlistIndex == nil {
		return nil
	}
	term = normalizeKeyword(term)
	if term == "" {
		return nil
	}
	return s.artlistIndex.ByTerm[term]
}

func artlistConfidence(baseConf float64, conceptScore int, frase, topic, matchedTerm string, rankedTerms []string) float64 {
	conf := baseConf
	if conceptScore > 5 {
		conf += 0.10
	}
	// Bonus for direct keyword presence in phrase
	if strings.Contains(strings.ToLower(frase), strings.ToLower(matchedTerm)) {
		conf += 0.15
	}
	if conf > 0.95 {
		conf = 0.95
	}
	if conf < 0.35 {
		conf = 0.35
	}
	return conf
}

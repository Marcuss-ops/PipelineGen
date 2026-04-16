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

type clipConcept = ClipConcept

func GetConceptMap() []ClipConcept { return conceptMap }

// associateClips associates each important phrase with one primary clip choice.
func (s *ScriptDocService) associateClips(frasi []string, stockFolder StockFolder, topic string) []ClipAssociation {
	usedClipIDs := make(map[string]bool)
	return s.associateClipsWithDedup(frasi, usedClipIDs, stockFolder, topic)
}

func scoreConceptForPhrase(fraseLower string, cm clipConcept) (int, string) {
	bestKeyword := ""
	bestLen := 0
	matchCount := 0
	for _, kw := range cm.Keywords {
		kwLower := strings.ToLower(kw)
		if strings.Contains(fraseLower, kwLower) {
			matchCount++
			if len(kw) > bestLen {
				bestLen = len(kw)
				bestKeyword = kw
			}
		}
	}
	return matchCount, bestKeyword
}

func phraseMatchesTopicFolder(phrase, topic string, stockFolder StockFolder) (bool, string) {
	if stockFolder.ID == "" || stockFolder.ID == "root" || stockFolder.Name == "Stock" {
		return false, ""
	}
	phraseLower := strings.ToLower(phrase)
	for _, tok := range significantTokens(topic) {
		if strings.Contains(phraseLower, tok) {
			return true, tok
		}
	}
	folderTokens := significantTokens(strings.ReplaceAll(stockFolder.Name, "/", " "))
	for _, tok := range folderTokens {
		if strings.Contains(phraseLower, tok) {
			return true, tok
		}
	}
	return false, ""
}

// Priority: Topic folder → Dynamic clips → StockDB → Artlist.
func (s *ScriptDocService) associateClipsWithDedup(frasi []string, usedClipIDs map[string]bool, stockFolder StockFolder, topic string) []ClipAssociation {
	termUsageCount := make(map[string]int)
	var associations []ClipAssociation

	s.dynamicClipsMu.Lock()
	dynamicClips := make([]clipsearch.SearchResult, len(s.dynamicClips))
	copy(dynamicClips, s.dynamicClips)
	s.dynamicClipsMu.Unlock()

	dynamicClipByKeyword := make(map[string]clipsearch.SearchResult)
	for _, dc := range dynamicClips {
		dynamicClipByKeyword[strings.ToLower(dc.Keyword)] = dc
	}

	var dbClips []stockdb.StockClipEntry
	if s.stockDB != nil {
		conceptTags := []string{"people", "city", "technology", "crowd", "arrest", "tech", "fight", "boxing", "gym", "sport", "courtroom"}
		allClips, err := s.stockDB.SearchClipsByTags(conceptTags)
		if err == nil {
			for _, c := range allClips {
				if !usedClipIDs[c.ClipID] {
					dbClips = append(dbClips, c)
				}
			}
		}
	}

	dbClipByTag := make(map[string][]stockdb.StockClipEntry)
	for _, clip := range dbClips {
		tags := strings.ToLower(strings.Join(clip.Tags, ","))
		for _, tag := range strings.Split(tags, ",") {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				dbClipByTag[tag] = append(dbClipByTag[tag], clip)
			}
		}
	}
	dbClipIndex := make(map[string]int)

	for _, frase := range frasi {
		fraseLower := strings.ToLower(frase)

		if ok, matched := phraseMatchesTopicFolder(frase, topic, stockFolder); ok {
			associations = append(associations, ClipAssociation{
				Phrase:         frase,
				Type:           "STOCK_FOLDER",
				StockFolder:    &stockFolder,
				Confidence:     0.96,
				MatchedKeyword: matched,
			})
			continue
		}

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
		for i := 0; i < len(scores)-1; i++ {
			for j := i + 1; j < len(scores); j++ {
				if scores[j].score > scores[i].score || (scores[j].score == scores[i].score && scores[j].cm.BaseConf > scores[i].cm.BaseConf) {
					scores[i], scores[j] = scores[j], scores[i]
				}
			}
		}

		for _, cs := range scores {
			cm := cs.cm
			kw := cs.bestKeyword
			if dc, ok := dynamicClipByKeyword[strings.ToLower(kw)]; ok {
				if !usedClipIDs["dynamic_"+dc.DriveID] {
					usedClipIDs["dynamic_"+dc.DriveID] = true
					associations = append(associations, ClipAssociation{Phrase: frase, Type: "DYNAMIC", DynamicClip: &dc, Confidence: cm.BaseConf + 0.1, MatchedKeyword: kw})
					break
				}
			}
			if clips, ok := dbClipByTag[strings.ToLower(kw)]; ok && len(clips) > 0 {
				idx := dbClipIndex[strings.ToLower(kw)] % len(clips)
				clip := clips[idx]
				dbClipIndex[strings.ToLower(kw)]++
				usedClipIDs[clip.ClipID] = true
				associations = append(associations, ClipAssociation{Phrase: frase, Type: "STOCK_DB", ClipDB: &clip, Confidence: cm.BaseConf, MatchedKeyword: kw})
				break
			}
			if s.artlistIndex != nil {
				if clips, ok := s.artlistIndex.ByTerm[cm.Term]; ok && len(clips) > 0 {
					usageIdx := termUsageCount[cm.Term] % len(clips)
					clip := clips[usageIdx]
					termUsageCount[cm.Term]++
					if clip.FolderID != "" {
						usedClipIDs[clip.FolderID+"_"+clip.Name] = true
					}
					associations = append(associations, ClipAssociation{Phrase: frase, Type: "ARTLIST", Clip: &clip, Confidence: cm.BaseConf, MatchedKeyword: kw})
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
